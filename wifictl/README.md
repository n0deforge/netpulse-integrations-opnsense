# Wi-Fi controller read-only pusher

Status: **beta, validated against one controller managing a T18-21K Pro (firmware V1.0.34) and an ASUS RT-AC68U/RT-AC1900P in access point mode (Asuswrt 3.0.0.4.386_52062)**. Maintained in the [n0deforge fork](https://github.com/n0deforge/netpulse-integrations-opnsense); not part of upstream.

Some access points have no NetPulse agent and no usable vendor API of their own, but are already managed by a small self-hosted controller that logs into each AP and normalises what it reports. This pusher reads that controller instead of the APs, so the AP credentials stay in one place and NetPulse gets one external unit per AP.

The controller is expected to expose `GET /api/state` with the shape below. It is the API of the controller this was built for; another controller can implement the same document to reuse the pusher.

```json
{
  "poll_interval": 30,
  "aps": [{
    "id": "t18", "name": "T18-21K Pro", "kind": "t18", "api": "ok", "polled_at": 1800000000,
    "info": {"model": "T18-21K Pro", "firmware": "V1.0.34", "uptime_s": 1186046, "mac": "14:ea:a1:00:00:80"},
    "radios": [{"id": "5g", "editable": true, "band": "5 GHz", "ssid": "home", "enabled": true, "channel": 149, "width": "40 MHz"}],
    "clients": [{"mac": "aa:bb:cc:00:00:01", "name": "phone", "ip": "10.0.20.5", "band": "5 GHz", "rssi": -61}],
    "ping": {"up": true, "rtt_ms": 0.5, "loss_pct": 0}
  }]
}
```

## Implemented coverage

| NetPulse field | Source |
|---|---|
| `system.board` (model, hostname, firmware) | `aps[].info`, `aps[].name` |
| `system.sysinfo.uptime` | `aps[].info.uptime_s` |
| `system.bridgeMac`, `system.ifaceMacs` | `aps[].info.mac`, so NetPulse anchors the AP under the gateway |
| `system.latencyMs`, `system.lossPct` | the controller's own ping to the AP |
| `system.backhaul` | always `cable` |
| `wireless.radios` | one per band from the primary enabled SSID: channel, width, client count |
| `wireless.clients` | `aps[].clients`: band and signal in dBm; link rate (`txRateMbps`/`rxRateMbps`) from the Asuswrt raw client list (`curTx`/`curRx`) |
| `arp` | client IP as the AP reports it |
| `dhcp.reservations` | client name as the AP reports it (ASUS nicknames, T18 station names) |

The controller is polled once per cycle and the result is shared by every AP unit, so two APs cost one request.

## Deliberate omissions and unresolved behavior

- An AP the controller cannot read (`api` other than `ok`) or whose `polled_at` is older than four controller poll intervals fails the collection. NetPulse then marks the unit stale instead of showing frozen clients.
- Signal is sent as `0` when the AP does not report RSSI (the T18 firmware does not). NetPulse treats values `>= 0` as unknown.
- Link rates come only from Asuswrt (`curTx`/`curRx`, PHY rates in Mbps). The T18 station list reports instantaneous throughput (`downSpeed`/`upSpeed`), which is not a link rate and is not sent.
- Only the primary SSID of each band is described as a radio. Disabled and secondary SSIDs are skipped.
- No neighbour scan, noise floor, BSSIDs, transmit power or 802.11r state: the controller does not expose them, so the channel analyzer and roaming-daemon views stay empty. Roaming between APs is still recorded by NetPulse from the client moving between units.
- Read-only. The controller can change SSIDs, channels and reboot APs; this pusher only calls `POST /api/login` and `GET /api/state`.

## Configuration

Create one router per AP in NetPulse as type `external` (Settings, or `POST /api/config/routers`), then create an agent token for each slug (`POST /api/agents` with `{"slug": "..."}`).

```sh
export NETPULSE_SERVER=http://netpulse:3000
export NETPULSE_INTERVAL=30s
export WIFICTL_URL=http://controller:8088
export WIFICTL_PASSWORD=REPLACE_WITH_CONTROLLER_ADMIN_PASSWORD
export WIFICTL_UNITS=t18,asus
export WIFICTL_T18_SLUG=t18-21k-pro
export WIFICTL_T18_TOKEN=REPLACE_WITH_AGENT_TOKEN
export WIFICTL_ASUS_SLUG=rt-ac1900p
export WIFICTL_ASUS_TOKEN=REPLACE_WITH_AGENT_TOKEN
./wifictl-pusher
```

| Variable | Meaning |
|---|---|
| `WIFICTL_URL` | Base URL of the controller |
| `WIFICTL_PASSWORD` | Controller admin password; used only to obtain a session cookie. Empty when the controller has no login |
| `WIFICTL_UNITS` | Comma separated controller AP ids (`aps[].id`) to push |
| `WIFICTL_<ID>_SLUG` | NetPulse slug for that AP, `<ID>` upper-cased |
| `WIFICTL_<ID>_TOKEN` | Agent token for that slug |
| `NETPULSE_SERVER`, `NETPULSE_INTERVAL`, `NETPULSE_SERVER_FP`, `NETPULSE_CA_FILE` | As for every pusher in this repository |

A compose file that runs this pusher next to NetPulse and the OPNsense pusher is in [`deploy/docker`](../deploy/docker).

## Local verification

```sh
go test ./wifictl/... -count=1
WIFICTL_LIVE_STATE=/path/to/state.json go test ./wifictl/... -run LiveFixture -v
```

The second command translates a real controller dump without committing it and prints what each AP would push.
