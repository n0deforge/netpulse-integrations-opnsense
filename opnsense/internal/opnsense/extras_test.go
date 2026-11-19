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
