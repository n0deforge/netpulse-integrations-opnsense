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
