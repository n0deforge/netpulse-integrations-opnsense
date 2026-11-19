// Package wifictl reads the self-hosted Wi-Fi controller (FastAPI app that
// polls a T18 and an ASUS AP) and translates each AP it manages into a
// NetPulse external unit: board, uptime, radios, associated clients and the
// controller's own ping to the AP. Read-only: only GET /api/state (plus the
// controller's login) is ever called.
package wifictl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gnacho/netpulse/agent/probe"
)

// State mirrors GET /api/state of the controller.
type State struct {
	Generated    float64 `json:"generated"`
	PollInterval int     `json:"poll_interval"`
	APs          []AP    `json:"aps"`
}

type AP struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	URL      string   `json:"url"`
	API      string   `json:"api"`
	Error    *string  `json:"error"`
	PolledAt *float64 `json:"polled_at"`
	Info     struct {
		Model    *string  `json:"model"`
		Firmware *string  `json:"firmware"`
		UptimeS  *float64 `json:"uptime_s"`
		MAC      *string  `json:"mac"`
		Mode     *string  `json:"mode"`
	} `json:"info"`
	Radios  []APRadio  `json:"radios"`
	Clients []APClient `json:"clients"`
	Ping    struct {
		Up      *bool    `json:"up"`
		RTTms   *float64 `json:"rtt_ms"`
		LossPct *float64 `json:"loss_pct"`
	} `json:"ping"`
}

type APRadio struct {
	ID       *string `json:"id"`
	Editable bool    `json:"editable"`
	Band     string  `json:"band"`
	SSID     string  `json:"ssid"`
	Enabled  bool    `json:"enabled"`
	Channel  any     `json:"channel"`
	Width    *string `json:"width"`
}

type APClient struct {
	MAC  string  `json:"mac"`
	Name *string `json:"name"`
	IP   *string `json:"ip"`
	Band string  `json:"band"`
	RSSI any     `json:"rssi"`
}

// Client fetches and caches the controller state for all AP units.
type Client struct {
	base     string
	password string
	http     *http.Client
	maxAge   time.Duration

	mu      sync.Mutex
	cached  *State
	fetched time.Time
}

func New(base, password string, maxAge time.Duration) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{
		base:     strings.TrimRight(base, "/"),
		password: password,
		http:     &http.Client{Timeout: 10 * time.Second, Jar: jar},
		maxAge:   maxAge,
	}
}

// State returns the controller state, refreshing it when older than maxAge so
// several AP units pushing in the same cycle share one request.
func (c *Client) State(ctx context.Context) (*State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached != nil && time.Since(c.fetched) < c.maxAge {
		return c.cached, nil
	}
	st, err := c.fetch(ctx)
	if err == errLogin {
		if err = c.login(ctx); err != nil {
			return nil, err
		}
		st, err = c.fetch(ctx)
	}
	if err != nil {
		return nil, err
	}
	c.cached, c.fetched = st, time.Now()
	return st, nil
}

var errLogin = fmt.Errorf("wifictl: login required")

func (c *Client) fetch(ctx context.Context) (*State, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/state", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wifictl: GET /api/state: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errLogin
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("wifictl: GET /api/state: %s", resp.Status)
	}
	var st State
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&st); err != nil {
		return nil, fmt.Errorf("wifictl: decode state: %w", err)
	}
	return &st, nil
}

func (c *Client) login(ctx context.Context) error {
	if c.password == "" {
		return fmt.Errorf("wifictl: controller requires login and WIFICTL_PASSWORD is empty")
	}
	body, _ := json.Marshal(map[string]string{"password": c.password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/login", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("wifictl: login: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("wifictl: login: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

// Collector returns the payload collector for one AP id ("t18", "asus").
func (c *Client) Collector(apID string) func(context.Context) (*probe.PayloadData, error) {
	return func(ctx context.Context) (*probe.PayloadData, error) {
		st, err := c.State(ctx)
		if err != nil {
			return nil, err
		}
		for _, ap := range st.APs {
			if ap.ID == apID {
				return Translate(ap, st.PollInterval, time.Now())
			}
		}
		return nil, fmt.Errorf("wifictl: controller has no AP %q", apID)
	}
}

var widthRe = regexp.MustCompile(`(\d+)\s*MHz`)

// Translate maps one controller AP into NetPulse payload sections. An AP the
// controller cannot read (auth error, unreachable) or whose data is stale is
// reported as a failed collection, so NetPulse marks the unit stale instead
// of showing frozen clients.
func Translate(ap AP, pollInterval int, now time.Time) (*probe.PayloadData, error) {
	if ap.API != "ok" {
		msg := ap.API
		if ap.Error != nil {
			msg += ": " + *ap.Error
		}
		return nil, fmt.Errorf("wifictl: %s not readable by the controller (%s)", ap.Name, msg)
	}
	if pollInterval <= 0 {
		pollInterval = 30
	}
	if ap.PolledAt == nil || now.Sub(time.Unix(int64(*ap.PolledAt), 0)) > time.Duration(4*pollInterval)*time.Second {
		return nil, fmt.Errorf("wifictl: %s data is stale on the controller", ap.Name)
	}

	board := &probe.BoardInfo{Hostname: ap.Name, System: "wifictl/" + ap.Kind}
	if ap.Info.Model != nil {
		board.Model = *ap.Info.Model
	}
	if ap.Info.Firmware != nil {
		board.Release.Version = *ap.Info.Firmware
		board.Release.Description = strings.TrimSpace(board.Model + " " + *ap.Info.Firmware)
	}
	sys := &probe.SystemData{Board: board, Backhaul: "cable"}
	if ap.Info.UptimeS != nil {
		sys.SysInfo = &probe.SysInfo{Uptime: *ap.Info.UptimeS}
	}
	if ap.Info.MAC != nil {
		if m := canonMAC(*ap.Info.MAC); m != "" {
			sys.BridgeMAC = m
			sys.IfaceMACs = []string{m}
		}
	}
	if ap.Ping.RTTms != nil {
		v := *ap.Ping.RTTms
		sys.LatencyMs = &v
	}
	if ap.Ping.LossPct != nil {
		v := *ap.Ping.LossPct
		sys.LossPct = &v
	}

	clients := map[string]probe.WirelessClient{}
	perBand := map[string]int{}
	arp := map[string]string{}
	var names []probe.DhcpReservation
	for _, c := range ap.Clients {
		mac := canonMAC(c.MAC)
		if mac == "" {
			continue
		}
		band := normBand(c.Band)
		wc := probe.WirelessClient{Band: band}
		if s, ok := toInt(c.RSSI); ok && s < 0 {
			wc.SignalDbm = s
		}
		clients[mac] = wc
		perBand[band]++
		ip := ""
		if c.IP != nil {
			if p := net.ParseIP(strings.TrimSpace(*c.IP)); p != nil && p.To4() != nil {
				ip = p.To4().String()
				arp[mac] = ip
			}
		}
		if c.Name != nil && strings.TrimSpace(*c.Name) != "" {
			names = append(names, probe.DhcpReservation{MAC: mac, Name: strings.TrimSpace(*c.Name), IP: ip})
		}
	}

	// One radio per band: the primary (editable) SSID carries the live
	// channel and width; client counts come from the station list.
	radios := []probe.Radio{}
	seen := map[string]bool{}
	sorted := append([]APRadio(nil), ap.Radios...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Editable && !sorted[j].Editable })
	for _, r := range sorted {
		band := normBand(r.Band)
		if band == "" || seen[band] || !r.Enabled {
			continue
		}
		seen[band] = true
		radio := probe.Radio{Name: band, Clients: perBand[band]}
		if ch, ok := toInt(r.Channel); ok {
			radio.Channel = ch
		}
		if r.Width != nil {
			if m := widthRe.FindStringSubmatch(*r.Width); m != nil {
				radio.WidthMhz, _ = strconv.Atoi(m[1])
			}
		}
		radios = append(radios, radio)
	}

	data := &probe.PayloadData{
		System:   sys,
		Wireless: &probe.WirelessData{Clients: clients, Radios: radios},
	}
	if len(arp) > 0 {
		data.Arp = arp
	}
	if len(names) > 0 {
		sort.Slice(names, func(i, j int) bool { return names[i].MAC < names[j].MAC })
		data.DHCP = &probe.DHCPData{Leases: []probe.DhcpLease{}, Reservations: names}
	}
	return data, nil
}

func normBand(b string) string {
	s := strings.ToLower(strings.ReplaceAll(b, " ", ""))
	switch {
	case strings.HasPrefix(s, "2.4"), s == "2g", s == "2.4g":
		return "2.4 GHz"
	case strings.HasPrefix(s, "5"):
		return "5 GHz"
	case strings.HasPrefix(s, "6"):
		return "6 GHz"
	}
	return ""
}

func canonMAC(raw string) string {
	m, err := net.ParseMAC(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToUpper(m.String())
}

func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(x)); err == nil {
			return n, true
		}
	}
	return 0, false
}
