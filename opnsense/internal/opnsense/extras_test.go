package opnsense

import (
	"encoding/json"
	"testing"
	"time"
)

func rows(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var r rowsResponse
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	return r.Rows
}

func TestParseWireGuard(t *testing.T) {
	wg := parseWireGuard(rows(t, `{"rows":[
	 {"if":"wg0","type":"interface","public-key":"SRV=","listen-port":"51820","status":"up","name":"HomeVPN"},
	 {"if":"wg0","type":"peer","public-key":"PEER1=","name":"phone","endpoint":"203.0.113.9:4500",
	  "allowed-ips":"10.9.0.2/32, fd00::2/128","latest-handshake":1800000000,"transfer-rx":1234,"transfer-tx":"5678"},
	 {"if":"wg0","type":"peer","public-key":"PEER2=","endpoint":"(none)","allowed-ips":"10.9.0.3/32","latest-handshake":0,
	  "transfer-rx":0,"transfer-tx":0}]}`))
	if len(wg.Interfaces) != 1 || wg.Interfaces[0] != "wg0" {
		t.Fatalf("interfaces = %v", wg.Interfaces)
	}
	if len(wg.Peers) != 2 {
		t.Fatalf("peers = %d", len(wg.Peers))
	}
	p := wg.Peers[0]
	if p.Name != "phone" || p.Endpoint != "203.0.113.9:4500" || p.AllowedIPs != "10.9.0.2/32,fd00::2/128" ||
		p.LastHandshake != 1800000000 || p.RxBytes != 1234 || p.TxBytes != 5678 {
		t.Fatalf("peer = %+v", p)
	}
	if wg.Peers[1].Endpoint != "" {
		t.Fatalf("(none) endpoint kept: %+v", wg.Peers[1])
	}
}

func TestParseLeasesAcrossServers(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	kea := parseLeases(rows(t, `{"rows":[{"address":"10.0.10.50","hwaddr":"aa:bb:cc:dd:ee:01","hostname":"laptop.","expire":1800003600},
	 {"address":"10.0.10.51","hwaddr":"aa:bb:cc:dd:ee:02","hostname":"old","expire":1700000000}]}`), now)
	if len(kea) != 1 || kea[0].MAC != "AA:BB:CC:DD:EE:01" || kea[0].Hostname != "laptop" || *kea[0].LeaseExpiresAt != 1800003600 {
		t.Fatalf("kea = %+v", kea)
	}
	isc := parseLeases(rows(t, `{"rows":[{"address":"10.0.20.9","mac":"AA-BB-CC-DD-EE-03","hostname":"tv","ends":"","status":"online","type":"static"},
	 {"address":"10.0.20.10","mac":"aa:bb:cc:dd:ee:04","hostname":"x","status":"expired"}]}`), now)
	if len(isc) != 1 || isc[0].IP != "10.0.20.9" || isc[0].LeaseExpiresAt != nil {
		t.Fatalf("isc = %+v", isc)
	}
}

func TestOverviewAndMediaSpeed(t *testing.T) {
	ov := parseOverview(rows(t, `{"rows":[
	 {"identifier":"wan","description":"WAN","device":"igb0","macaddr":"00:11:22:33:44:55","status":"up",
	  "media":"1000baseT <full-duplex>","addr4":"198.51.100.7/24","gateways":["198.51.100.1"],"dnsservers":["1.1.1.1"]},
	 {"identifier":"lan","description":"LAN","device":"igb1","macaddr":"00:11:22:33:44:66","status":"up","media":"2500Base-T <full-duplex>"}]}`))
	if len(ov) != 2 || !ov[0].Up || ov[0].Gateways[0] != "198.51.100.1" {
		t.Fatalf("overview = %+v", ov)
	}
	if mediaSpeed(ov[0].Media) != "1000 Mbps" || mediaSpeed(ov[1].Media) != "2500 Mbps" || mediaSpeed("10Gbase-SR") != "10000 Mbps" || mediaSpeed("autoselect") != "" {
		t.Fatal("mediaSpeed")
	}
}

func TestARPHostnames(t *testing.T) {
	var raw arpResponse
	if err := json.Unmarshal([]byte(`{"rows":[
	 {"mac":"aa:bb:cc:00:00:01","ip":"10.0.30.5","intf":"vlan0.30","hostname":"nas.home.arpa."},
	 {"mac":"aa:bb:cc:00:00:02","ip":"10.0.30.6","intf":"vlan0.30","hostname":""},
	 {"mac":"aa:bb:cc:00:00:03","ip":"10.0.30.7","intf":"vlan0.30","hostname":"10.0.30.7"},
	 {"mac":"aa:bb:cc:00:00:04","ip":"10.0.30.8","intf":"vlan0.30","hostname":"?"}]}`), &raw); err != nil {
		t.Fatal(err)
	}
	h := arpHostnames(raw)
	if len(h) != 1 || h["10.0.30.5"] != "nas.home.arpa" {
		t.Fatalf("hosts = %v", h)
	}
}

func TestGatewayQuality(t *testing.T) {
	var resp gatewayStatusResponse
	if err := json.Unmarshal([]byte(`{"items":[
	 {"name":"WG_MULLVAD","address":"10.64.0.1","status_translated":"Online","delay":"80.1 ms","loss":"0.0 %"},
	 {"name":"WAN_PPPOE","address":"192.0.2.1","status_translated":"Online","delay":"9.4 ms","loss":"1.0 %"},
	 {"name":"IDLE","address":"10.9.9.9","status_translated":"Unknown","delay":"~","loss":"~"}],"status":"ok"}`), &resp); err != nil {
		t.Fatal(err)
	}
	lat, loss := pickGatewayQuality(resp.Items, "192.0.2.1")
	if lat == nil || *lat != 9.4 || loss == nil || *loss != 1.0 {
		t.Fatalf("lat=%v loss=%v", lat, loss)
	}
	if lat, _ := pickGatewayQuality(resp.Items[2:], ""); lat != nil {
		t.Fatal("unmonitored gateway must give no latency")
	}
}

func TestNetworksAndVLANs(t *testing.T) {
	ov := []ifOverview{
		{Identifier: "opt4", Description: "VLAN10_HOME", Device: "vlan0.10", Addr4: "10.0.10.1/26"},
		{Identifier: "lan", Description: "MGMT", Device: "igc5", Addr4: "10.0.255.254/26"},
		{Identifier: "wan", Description: "WAN", Device: "pppoe0", Addr4: "198.51.100.7/32"},
		{Identifier: "opt6", Description: "Mullvad", Device: "wg0", Addr4: "10.74.10.2/32"},
		{Identifier: "", Description: "Unassigned", Device: "vlan0.100"},
	}
	n := overviewNetworks(ov)
	if len(n) != 2 || n[0].Name != "MGMT" || n[0].Vlan != 0 || n[1].Subnet != "10.0.10.0/26" || n[1].Vlan != 10 {
		t.Fatalf("networks = %+v", n)
	}
	v := arpVLANs(map[string]string{"AA:00:00:00:00:01": "vlan0.30", "AA:00:00:00:00:02": "igc5", "AA:00:00:00:00:03": "igc1_vlan20"})
	if len(v) != 2 || v["AA:00:00:00:00:01"] != 30 || v["AA:00:00:00:00:03"] != 20 {
		t.Fatalf("vlans = %v", v)
	}
}

func TestParseTailscale(t *testing.T) {
	var st tsStatus
	if err := json.Unmarshal([]byte(`{
	 "Version":"1.80.0","BackendState":"Running",
	 "Self":{"PublicKey":"nodekey:self","HostName":"fw","DNSName":"fw.example-tailnet.ts.net.","TailscaleIPs":["100.64.0.1"],"Online":true},
	 "Peer":{
	  "nodekey:bbbb":{"PublicKey":"nodekey:bbbb","HostName":"Laptop","DNSName":"laptop.example-tailnet.ts.net.",
	   "TailscaleIPs":["100.64.0.2","fd7a:115c:a1e0::2"],"Online":true,"LastHandshake":"2027-01-15T10:00:00.123Z",
	   "RxBytes":1048576,"TxBytes":2048,"CurAddr":"192.0.2.10:41641","Relay":"nyc"},
	  "nodekey:aaaa":{"PublicKey":"nodekey:aaaa","HostName":"phone","DNSName":"",
	   "TailscaleIPs":["100.64.0.3"],"Online":false,"LastHandshake":"0001-01-01T00:00:00Z","RxBytes":0,"TxBytes":0,"CurAddr":"","Relay":"nyc"}}}`), &st); err != nil {
		t.Fatal(err)
	}
	peers := parseTailscale(st)
	if len(peers) != 2 {
		t.Fatalf("peers = %d", len(peers))
	}
	// sorted by node key: aaaa first
	if p := peers[0]; p.Name != "phone" || p.LastHandshake != 0 || p.Endpoint != "" || p.AllowedIPs != "100.64.0.3/32" {
		t.Fatalf("idle peer = %+v", p)
	}
	if p := peers[1]; p.Name != "laptop" || p.Interface != "tailscale0" || p.AllowedIPs != "100.64.0.2/32,fd7a:115c:a1e0::2/128" ||
		p.LastHandshake != mustUnix(t, "2027-01-15T10:00:00Z") ||
		p.RxBytes != 1048576 || p.Endpoint != "192.0.2.10:41641" {
		t.Fatalf("active peer = %+v", p)
	}
	if parseTailscale(tsStatus{BackendState: "Stopped"}) != nil || parseTailscale(tsStatus{Error: "not running"}) != nil {
		t.Fatal("stopped tailscale must be absent")
	}
	wg := mergeTailscale(nil, peers)
	if len(wg.Interfaces) != 1 || wg.Interfaces[0] != "tailscale0" || len(wg.Peers) != 2 {
		t.Fatalf("merge = %+v", wg)
	}
	if mergeTailscale(nil, nil) != nil {
		t.Fatal("no tailscale, no wireguard: section must stay absent")
	}
}

func mustUnix(t *testing.T, s string) int64 {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return tm.Unix()
}
