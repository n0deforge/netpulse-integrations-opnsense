# wifictl pusher

Read-only pusher for a self-hosted Wi-Fi controller (FastAPI app exposing
`GET /api/state` with `aps[]`: info, radios, clients, ping). Each AP the
controller manages becomes its own NetPulse **external** unit.

| NetPulse field | Source |
|---|---|
| `system.board` (model, firmware), `sysinfo.uptime` | `aps[].info` |
| `system.bridgeMac` / `ifaceMacs` | `aps[].info.mac` |
| `system.latencyMs` / `lossPct` | controller ping to the AP |
| `wireless.radios` (one per band: channel, width, clients) | primary enabled SSID per band |
| `wireless.clients` (band, signal) | `aps[].clients`; RSSI omitted (0) when the AP does not report it |
| `arp`, `dhcp.reservations` (names) | client IP / name as the AP knows them |

An AP the controller cannot read (`api != ok`) or whose data is older than
4x the controller poll interval fails the collection, so NetPulse marks the
unit stale instead of showing frozen clients. Only `GET /api/state` and the
controller login are called.

Not available from the controller: neighbour scans (channel analyzer),
noise floor, BSSIDs, 802.11r state.

## Configuration

```sh
NETPULSE_SERVER=http://netpulse:3000
NETPULSE_INTERVAL=30s
WIFICTL_URL=http://controller:8088
WIFICTL_PASSWORD=...            # controller ADMIN_PASSWORD
WIFICTL_UNITS=t18,asus          # controller AP ids
WIFICTL_T18_SLUG=t18-21k-pro    # NetPulse unit slug per AP
WIFICTL_T18_TOKEN=...           # POST /api/agents {"slug": ...}
WIFICTL_ASUS_SLUG=rt-ac1900p
WIFICTL_ASUS_TOKEN=...
```

## Build note

`go.mod` replaces `github.com/gnacho/netpulse/agent` with `../src/agent`,
a checkout of [netpulse-opnsense](https://github.com/n0deforge/netpulse-opnsense)
(branch `opnsense`, for the `wireguard` payload field) next to this repo.
