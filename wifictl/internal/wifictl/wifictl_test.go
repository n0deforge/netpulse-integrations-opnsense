package wifictl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

const sample = `{"generated":1800000000,"poll_interval":30,"aps":[
 {"id":"t18","name":"T18","kind":"t18","api":"ok","error":null,"polled_at":1800000000,
  "info":{"model":"T18-21K Pro","firmware":"V1.0.34","uptime_s":100,"mac":"14:ea:a1:00:00:80","mode":null},
  "radios":[{"id":"2g","editable":true,"band":"2.4 GHz","ssid":"A","enabled":true,"channel":11,"width":"Auto"},
            {"id":"5g","editable":true,"band":"5 GHz","ssid":"A5","enabled":true,"channel":149,"width":"40 MHz"},
            {"id":null,"editable":false,"band":"5 GHz","ssid":"guest","enabled":false,"channel":149,"width":null}],
  "clients":[{"mac":"aa:bb:cc:00:00:01","name":"phone","ip":"10.0.20.5","band":"5 GHz","rssi":null},
             {"mac":"aa:bb:cc:00:00:02","name":null,"ip":null,"band":"2.4 GHz","rssi":"-61"}],
  "ping":{"up":true,"rtt_ms":0.5,"loss_pct":0}},
 {"id":"asus2","name":"ASUS2","kind":"asus","api":"ok","error":null,"polled_at":1800000000,
  "info":{"model":"RT-AC68U","mac":"04:d4:c4:00:00:a0","firmware_available":"3.0.0.4.386_52102"},
  "radios":[{"id":"5g","editable":true,"band":"5 GHz","ssid":"B","enabled":true,"channel":149,"width":"Auto"}],
  "clients":[{"mac":"aa:bb:cc:00:00:09","name":"tv","ip":"10.0.10.9","band":"5 GHz","rssi":"-55"}],
  "raw":{"clients":{"get_clientlist":{"maclist":["AA:BB:CC:00:00:09"],"AA:BB:CC:00:00:09":{"curTx":"144.4","curRx":"130","isWL":"2"}}}},
  "ping":{}},
 {"id":"asus","name":"ASUS","kind":"asus","api":"auth_error","error":"login rejected","polled_at":1800000000,
  "info":{},"radios":[],"clients":[],"ping":{}}]}`

func TestTranslate(t *testing.T) {
	var st State
	if err := json.Unmarshal([]byte(sample), &st); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1800000010, 0)
	d, err := Translate(st.APs[0], 30, now)
	if err != nil {
		t.Fatal(err)
	}
	if d.System.BridgeMAC != "14:EA:A1:00:00:80" || d.System.Board.Model != "T18-21K Pro" || d.System.SysInfo.Uptime != 100 {
		t.Fatalf("system = %+v", d.System)
	}
	w := d.Wireless
	if len(w.Clients) != 2 || w.Clients["AA:BB:CC:00:00:01"].SignalDbm != 0 || w.Clients["AA:BB:CC:00:00:02"].SignalDbm != -61 {
		t.Fatalf("clients = %+v", w.Clients)
	}
	if len(w.Radios) != 2 || w.Radios[1].Channel != 149 || w.Radios[1].WidthMhz != 40 || w.Radios[1].Clients != 1 {
		t.Fatalf("radios = %+v", w.Radios)
	}
	if d.Arp["AA:BB:CC:00:00:01"] != "10.0.20.5" || len(d.DHCP.Reservations) != 1 {
		t.Fatalf("arp/names = %v %+v", d.Arp, d.DHCP)
	}
	a2, err := Translate(st.APs[1], 30, now)
	if err != nil {
		t.Fatal(err)
	}
	if a2.System.FirmwareAvailable != "3.0.0.4.386_52102" || d.System.FirmwareAvailable != "" {
		t.Fatalf("firmware available = %q / %q", a2.System.FirmwareAvailable, d.System.FirmwareAvailable)
	}
	if c := a2.Wireless.Clients["AA:BB:CC:00:00:09"]; c.TxRateMbps != 144.4 || c.RxRateMbps != 130 {
		t.Fatalf("asus link rate = %+v", c)
	}
	if c := d.Wireless.Clients["AA:BB:CC:00:00:01"]; c.TxRateMbps != 0 {
		t.Fatalf("t18 must not report a link rate: %+v", c)
	}
	if _, err := Translate(st.APs[2], 30, now); err == nil {
		t.Fatal("auth_error AP must fail collection")
	}
	if _, err := Translate(st.APs[0], 30, now.Add(10*time.Minute)); err == nil {
		t.Fatal("stale AP must fail collection")
	}
}

func TestClientLogsInOn401(t *testing.T) {
	logged := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/login":
			var b map[string]string
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["password"] != "pw" {
				http.Error(w, "no", 401)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "wc", Value: "ok", Path: "/"})
			logged = true
		case "/api/state":
			if c, err := r.Cookie("wc"); err != nil || c.Value != "ok" {
				http.Error(w, "login", 401)
				return
			}
			_, _ = w.Write([]byte(sample))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "pw", time.Minute)
	st, err := c.State(context.Background())
	if err != nil || !logged || len(st.APs) != 3 {
		t.Fatalf("state=%v err=%v logged=%v", st, err, logged)
	}
}

// TestLiveFixture: WIFICTL_LIVE_STATE=/path/state.json translates a real
// controller dump (not committed) and prints the summary.
func TestLiveFixture(t *testing.T) {
	path := os.Getenv("WIFICTL_LIVE_STATE")
	if path == "" {
		t.Skip("WIFICTL_LIVE_STATE not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	for _, ap := range st.APs {
		d, err := Translate(ap, st.PollInterval, time.Unix(int64(st.Generated), 0))
		if err != nil {
			t.Fatalf("%s: %v", ap.ID, err)
		}
		t.Logf("%s: model=%s fw=%s mac=%s radios=%+v clients=%d arp=%d names=%d",
			ap.ID, d.System.Board.Model, d.System.Board.Release.Version, d.System.BridgeMAC,
			d.Wireless.Radios, len(d.Wireless.Clients), len(d.Arp), func() int {
				if d.DHCP == nil {
					return 0
				}
				return len(d.DHCP.Reservations)
			}())
	}
}
