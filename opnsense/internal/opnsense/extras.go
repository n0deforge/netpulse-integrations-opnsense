package opnsense

// extras.go: optional sections layered on top of the read-only base poll —
// DHCP leases (Kea, dnsmasq or ISC), the interface overview (WAN address,
// gateway, link state, interface MACs) and WireGuard peers. Every call is
// best effort: a missing plugin or privilege (404/403) omits that section and
// never fails the push.

import (
	"context"
	"encoding/json"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gnacho/netpulse/agent/probe"
)

const (
	keaLeasesPath     = "/api/kea/leases4/search"
	dnsmasqLeasesPath = "/api/dnsmasq/leases/search"
	iscLeasesPath     = "/api/dhcpv4/leases/searchLease"
	ifOverviewPath    = "/api/interfaces/overview/interfacesInfo"
	wireguardShowPath = "/api/wireguard/service/show"
)

type rowsResponse struct {
	Rows []map[string]any `json:"rows"`
}

// str reads the first non-empty key of a loosely typed API row.
func str(row map[string]any, keys ...string) string {
	for _, k := range keys {
		switch v := row[k].(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case json.Number:
			return v.String()
		case bool:
			return strconv.FormatBool(v)
		}
	}
	return ""
}

func num(row map[string]any, keys ...string) int64 {
	s := str(row, keys...)
	if s == "" {
		return 0
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f)
	}
	return 0
}

func strList(row map[string]any, key string) []string {
	switch v := row[key].(type) {
	case []any:
		out := []string{}
		for _, x := range v {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case string:
		if strings.TrimSpace(v) != "" {
			return []string{strings.TrimSpace(v)}
		}
	}
	return nil
}

func canonMAC(raw string) string {
	mac, err := net.ParseMAC(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToUpper(mac.String())
}

// ---- DHCP ----------------------------------------------------------------

// dhcpLeases merges the leases of every DHCP server that answers. Returns nil
// when none does, so the server keeps the last good value.
func (p *Poller) dhcpLeases(ctx context.Context) *probe.DHCPData {
	seen := map[string]bool{}
	var leases []probe.DhcpLease
	answered := false
	for _, path := range []string{keaLeasesPath, dnsmasqLeasesPath, iscLeasesPath} {
		var resp rowsResponse
		if err := p.get(ctx, path, &resp); err != nil {
			continue
		}
		answered = true
		for _, l := range parseLeases(resp.Rows, p.now()) {
			if !seen[l.MAC] {
				seen[l.MAC] = true
				leases = append(leases, l)
			}
		}
	}
	if !answered {
		return nil
	}
	sort.Slice(leases, func(i, j int) bool { return leases[i].MAC < leases[j].MAC })
	if leases == nil {
		leases = []probe.DhcpLease{}
	}
	return &probe.DHCPData{Leases: leases}
}

func parseLeases(rows []map[string]any, now time.Time) []probe.DhcpLease {
	var out []probe.DhcpLease
	for _, row := range rows {
		mac := canonMAC(str(row, "hwaddr", "mac", "hw_address"))
		ip := net.ParseIP(str(row, "address", "ip", "ip_address"))
		if mac == "" || ip == nil || ip.To4() == nil {
			continue
		}
		// ISC marks dead leases explicitly; skip them instead of reviving a name.
		if strings.ToLower(str(row, "status", "state")) == "expired" {
			continue
		}
		l := probe.DhcpLease{
			MAC:      mac,
			IP:       ip.To4().String(),
			Hostname: strings.TrimSuffix(str(row, "hostname", "client-hostname"), "."),
			ClientID: str(row, "client_id", "clientid"),
		}
		if l.Hostname == "*" {
			l.Hostname = ""
		}
		if exp := leaseExpiry(row); exp > 0 {
			if exp < now.Unix() {
				continue
			}
			l.LeaseExpiresAt = &exp
		}
		out = append(out, l)
	}
	return out
}

func leaseExpiry(row map[string]any) int64 {
	if n := num(row, "expire", "expires", "valid_until"); n > 1_000_000_000 {
		return n
	}
	if s := str(row, "ends", "end"); s != "" {
		for _, layout := range []string{"2006/01/02 15:04:05", "2006-01-02 15:04:05", time.RFC3339} {
			if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
				return t.Unix()
			}
		}
	}
	return 0
}

// ---- interface overview ----------------------------------------------------

type ifOverview struct {
	Identifier  string // "wan", "lan", "opt1"
	Description string
	Device      string
	MAC         string
	Up          bool
	Media       string
	Addr4       string
	Gateways    []string
	DNS         []string
}

func (p *Poller) interfaceOverview(ctx context.Context) []ifOverview {
	var resp rowsResponse
	if err := p.get(ctx, ifOverviewPath, &resp); err != nil {
		return nil
	}
	return parseOverview(resp.Rows)
}

func parseOverview(rows []map[string]any) []ifOverview {
	var out []ifOverview
	for _, row := range rows {
		dev := str(row, "device")
		if dev == "" {
			continue
		}
		o := ifOverview{
			Identifier:  strings.ToLower(str(row, "identifier")),
			Description: str(row, "description"),
			Device:      dev,
			MAC:         canonMAC(str(row, "macaddr", "macaddr_hw")),
			Media:       str(row, "media"),
			Addr4:       str(row, "addr4"),
			Gateways:    strList(row, "gateways"),
			DNS:         strList(row, "dnsservers"),
		}
		status := strings.ToLower(str(row, "status"))
		o.Up = status == "up" || status == "active" || status == "associated"
		if o.Addr4 == "" {
			if v4 := strList(row, "ipv4"); len(v4) > 0 {
				o.Addr4 = v4[0]
			}
			if m, ok := row["ipv4"].([]any); ok && len(m) > 0 {
				if e, ok := m[0].(map[string]any); ok {
					o.Addr4 = str(e, "ipaddr")
				}
			}
		}
		out = append(out, o)
	}
	return out
}

// mediaSpeed turns FreeBSD media text ("1000baseT <full-duplex>",
// "2500Base-T") into the "1000 Mbps" form the UI shows.
func mediaSpeed(media string) string {
	m := strings.ToLower(media)
	for i := 0; i < len(m); i++ {
		if m[i] < '0' || m[i] > '9' {
			continue
		}
		j := i
		for j < len(m) && (m[j] >= '0' && m[j] <= '9') {
			j++
		}
		if strings.HasPrefix(m[j:], "base") || strings.HasPrefix(m[j:], "gbase") {
			n, _ := strconv.Atoi(m[i:j])
			if strings.HasPrefix(m[j:], "gbase") {
				n *= 1000
			}
			if n > 0 {
				return strconv.Itoa(n) + " Mbps"
			}
		}
		i = j
	}
	return ""
}

// applyOverview enriches ports, WAN and identity from the interface overview.
func applyOverview(data *probe.PayloadData, ov []ifOverview) (wanDevice string) {
	if len(ov) == 0 {
		return ""
	}
	byDev := map[string]ifOverview{}
	macs := map[string]bool{}
	for _, o := range ov {
		byDev[o.Device] = o
		if o.MAC != "" {
			macs[o.MAC] = true
		}
		if o.Identifier == "wan" {
			wanDevice = o.Device
		}
		if o.Identifier == "lan" && o.MAC != "" && data.System != nil {
			data.System.BridgeMAC = o.MAC
		}
	}
	if data.System != nil {
		for m := range macs {
			data.System.IfaceMACs = append(data.System.IfaceMACs, m)
		}
		sort.Strings(data.System.IfaceMACs)
	}
	if data.FDB != nil {
		for i, port := range data.FDB.Ports {
			o, ok := byDev[port.Iface]
			if !ok {
				continue
			}
			if o.Description != "" {
				data.FDB.Ports[i].Label = o.Description + " (" + port.Iface + ")"
			}
			if sp := mediaSpeed(o.Media); sp != "" && port.Up {
				data.FDB.Ports[i].Speed = sp
			}
		}
	}
	if wanDevice != "" {
		w := byDev[wanDevice]
		wan := &probe.WanInfo{Device: wanDevice, Port: wanDevice, DNS: w.DNS}
		if ip, _, err := net.ParseCIDR(w.Addr4); err == nil {
			wan.IP = ip.String()
		} else if ip := net.ParseIP(w.Addr4); ip != nil {
			wan.IP = ip.String()
		}
		if len(w.Gateways) > 0 {
			wan.Gateway = w.Gateways[0]
		}
		data.Wan = wan
	}
	return wanDevice
}

// ---- WireGuard -------------------------------------------------------------

func (p *Poller) wireguard(ctx context.Context) *probe.WireGuardData {
	var resp rowsResponse
	if err := p.get(ctx, wireguardShowPath, &resp); err != nil {
		return nil
	}
	return parseWireGuard(resp.Rows)
}

func parseWireGuard(rows []map[string]any) *probe.WireGuardData {
	wg := &probe.WireGuardData{Interfaces: []string{}, Peers: []probe.WireGuardPeer{}}
	ifSeen := map[string]bool{}
	for _, row := range rows {
		ifc := str(row, "if", "interface")
		if ifc != "" && !ifSeen[ifc] {
			ifSeen[ifc] = true
			wg.Interfaces = append(wg.Interfaces, ifc)
		}
		if strings.ToLower(str(row, "type")) != "peer" {
			continue
		}
		key := str(row, "public-key", "public_key", "pubkey")
		if key == "" {
			continue
		}
		ep := str(row, "endpoint")
		if ep == "(none)" {
			ep = ""
		}
		allowed := strings.ReplaceAll(str(row, "allowed-ips", "allowed_ips", "allowedips"), " ", "")
		if allowed == "(none)" {
			allowed = ""
		}
		wg.Peers = append(wg.Peers, probe.WireGuardPeer{
			Interface:     ifc,
			PublicKey:     key,
			Name:          str(row, "name", "description"),
			Endpoint:      ep,
			AllowedIPs:    allowed,
			LastHandshake: num(row, "latest-handshake", "latest_handshake"),
			RxBytes:       num(row, "transfer-rx", "transfer_rx"),
			TxBytes:       num(row, "transfer-tx", "transfer_tx"),
		})
	}
	sort.Strings(wg.Interfaces)
	return wg
}

// arpHostnames: OPNsense resolves each ARP entry (Unbound/DHCP registered
// names) and returns it as "hostname". It travels as discovery.hostByIp, the
// neutral IP -> name map the server already uses for devices without a DHCP
// hostname, so names show up even when the DHCP lease privilege is missing.
func arpHostnames(raw arpResponse) map[string]string {
	out := map[string]string{}
	for _, row := range raw.Rows {
		host := strings.TrimSuffix(strings.TrimSpace(row.Hostname), ".")
		ip := net.ParseIP(strings.TrimSpace(row.IP))
		if host == "" || host == "?" || ip == nil || ip.To4() == nil {
			continue
		}
		if net.ParseIP(host) != nil {
			continue // unresolved: OPNsense echoes the address
		}
		out[ip.To4().String()] = host
	}
	return out
}

// ---- gateway quality, networks and VLANs ----------------------------------

const gatewayStatusPath = "/api/routes/gateway/status"

type gatewayStatusResponse struct {
	Items []map[string]any `json:"items"`
}

// gatewayQuality reads dpinger's view of the WAN gateway: delay ("12.3 ms")
// and loss ("0.0 %"). The gateway whose address is the WAN gateway wins, then
// one whose name mentions WAN, then the first monitored one.
func (p *Poller) gatewayQuality(ctx context.Context, wanGateway string) (latency, loss *float64) {
	var resp gatewayStatusResponse
	if err := p.get(ctx, gatewayStatusPath, &resp); err != nil {
		return nil, nil
	}
	return pickGatewayQuality(resp.Items, wanGateway)
}

func pickGatewayQuality(items []map[string]any, wanGateway string) (latency, loss *float64) {
	var best map[string]any
	score := -1
	for _, it := range items {
		s := 0
		if d := str(it, "delay"); d == "" || d == "~" {
			continue // not monitored
		}
		if wanGateway != "" && str(it, "address", "gateway") == wanGateway {
			s += 4
		}
		if strings.Contains(strings.ToUpper(str(it, "name")), "WAN") {
			s += 2
		}
		if strings.EqualFold(str(it, "status_translated"), "online") {
			s++
		}
		if s > score {
			best, score = it, s
		}
	}
	if best == nil {
		return nil, nil
	}
	if v, ok := leadingFloat(str(best, "delay")); ok {
		latency = &v
	}
	if v, ok := leadingFloat(str(best, "loss")); ok {
		loss = &v
	}
	return latency, loss
}

// leadingFloat parses "12.3 ms", "0.0 %" or "12.3".
func leadingFloat(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	end := 0
	for end < len(s) && (s[end] >= '0' && s[end] <= '9' || s[end] == '.') {
		end++
	}
	if end == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(s[:end], 64)
	return v, err == nil
}

// vlanOf returns the 802.1Q tag of a FreeBSD VLAN interface ("vlan0.10",
// "igc1_vlan10", "vlan10"), or 0.
func vlanOf(dev string) int {
	d := strings.ToLower(strings.TrimSpace(dev))
	for _, sep := range []string{".", "_vlan", "vlan"} {
		if i := strings.LastIndex(d, sep); i >= 0 {
			if n, err := strconv.Atoi(d[i+len(sep):]); err == nil && n > 0 && n < 4095 {
				return n
			}
		}
	}
	return 0
}

// overviewNetworks describes every assigned IPv4 subnet (VLANs, LAN, MGMT)
// as a NetPulse network: name from the interface description, CIDR from the
// interface address and the VLAN tag from the device. WAN, loopback and /32
// tunnels are left out.
func overviewNetworks(ov []ifOverview) []probe.UciNetwork {
	var out []probe.UciNetwork
	for _, o := range ov {
		if o.Identifier == "" || o.Identifier == "wan" || o.Identifier == "lo0" {
			continue
		}
		_, ipnet, err := net.ParseCIDR(o.Addr4)
		if err != nil || ipnet.IP.To4() == nil {
			continue
		}
		if ones, _ := ipnet.Mask.Size(); ones >= 31 {
			continue
		}
		name := o.Description
		if name == "" {
			name = o.Identifier
		}
		out = append(out, probe.UciNetwork{
			Name: name, Device: o.Device, Proto: "static", Subnet: ipnet.String(), Vlan: vlanOf(o.Device),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// arpVLANs maps each ARP MAC to the VLAN of the interface it was learned on.
func arpVLANs(arpDevs map[string]string) map[string]int {
	out := map[string]int{}
	for mac, dev := range arpDevs {
		if v := vlanOf(dev); v > 0 {
			out[mac] = v
		}
	}
	return out
}

// ---- Tailscale ---------------------------------------------------------------

// tailscaleStatusPath returns `tailscale status --json` from the os-tailscale
// plugin (its only status endpoint). The plugin has a single privilege,
// "Tailscale", which also covers its settings and service endpoints; the
// pusher only ever reads this one.
const tailscaleStatusPath = "/api/tailscale/status/status"

type tsPeer struct {
	PublicKey     string   `json:"PublicKey"`
	HostName      string   `json:"HostName"`
	DNSName       string   `json:"DNSName"`
	TailscaleIPs  []string `json:"TailscaleIPs"`
	Online        bool     `json:"Online"`
	LastHandshake string   `json:"LastHandshake"`
	RxBytes       int64    `json:"RxBytes"`
	TxBytes       int64    `json:"TxBytes"`
	CurAddr       string   `json:"CurAddr"`
}

type tsStatus struct {
	BackendState string            `json:"BackendState"`
	Self         *tsPeer           `json:"Self"`
	Peer         map[string]tsPeer `json:"Peer"`
	Error        string            `json:"error"`
}

func (p *Poller) tailscale(ctx context.Context) []probe.WireGuardPeer {
	var st tsStatus
	if err := p.get(ctx, tailscaleStatusPath, &st); err != nil {
		return nil
	}
	return parseTailscale(st)
}

// parseTailscale maps tailnet peers onto WireGuard peer rows. Tailscale runs
// WireGuard underneath and reports the same facts per peer: last handshake and
// cumulative bytes. A peer that is online in the control plane but idle has no
// recent handshake and therefore shows as inactive, exactly as an idle
// WireGuard peer does. The direct endpoint is reported only when the link is
// not relayed through DERP. Returns nil when Tailscale is not running.
func parseTailscale(st tsStatus) []probe.WireGuardPeer {
	if st.Error != "" || (st.BackendState != "" && st.BackendState != "Running") {
		return nil
	}
	keys := make([]string, 0, len(st.Peer))
	for k := range st.Peer {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	peers := []probe.WireGuardPeer{}
	for _, k := range keys {
		tp := st.Peer[k]
		key := tp.PublicKey
		if key == "" {
			key = k
		}
		name := strings.SplitN(strings.TrimSuffix(tp.DNSName, "."), ".", 2)[0]
		if name == "" {
			name = tp.HostName
		}
		var allowed []string
		for _, ip := range tp.TailscaleIPs {
			if a := net.ParseIP(ip); a != nil {
				if a.To4() != nil {
					allowed = append(allowed, a.String()+"/32")
				} else {
					allowed = append(allowed, a.String()+"/128")
				}
			}
		}
		online := tp.Online
		var hs int64
		if t, err := time.Parse(time.RFC3339Nano, tp.LastHandshake); err == nil && t.Year() > 1970 {
			hs = t.Unix()
		}
		peers = append(peers, probe.WireGuardPeer{
			Interface:     "tailscale0",
			PublicKey:     key,
			Name:          name,
			Endpoint:      tp.CurAddr,
			AllowedIPs:    strings.Join(allowed, ","),
			LastHandshake: hs,
			RxBytes:       tp.RxBytes,
			TxBytes:       tp.TxBytes,
			Online:        &online,
		})
	}
	return peers
}

// mergeTailscale adds the tailnet as interface tailscale0 to the WireGuard
// section, creating the section when WireGuard itself is not readable.
func mergeTailscale(wg *probe.WireGuardData, ts []probe.WireGuardPeer) *probe.WireGuardData {
	if ts == nil {
		return wg
	}
	if wg == nil {
		wg = &probe.WireGuardData{Interfaces: []string{}, Peers: []probe.WireGuardPeer{}}
	}
	wg.Interfaces = append(wg.Interfaces, "tailscale0")
	wg.Peers = append(wg.Peers, ts...)
	return wg
}
