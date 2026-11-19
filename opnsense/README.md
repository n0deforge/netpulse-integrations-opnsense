# OPNsense read-only pusher

Status: **beta, validated against OPNsense 26.7 in a VM and on a user unit, still pending a stable tag**. The implementation is based on the official OPNsense API documentation and response shapes in the official OPNsense Core source, and it was checked end to end against a fresh OPNsense 26.7 VM with a real API key and a local ingest endpoint. A user then ran it against a real 26.7 unit and filed #53, #55 and #56, all addressed on main. Fixtures are captured from those systems, plus a quoted variant that older releases may produce. Beta packaging makes the integration available for testing; it does not claim stable support yet.

The [n0deforge fork](https://github.com/n0deforge/netpulse-integrations-opnsense) adds DHCP leases, the interface overview, ARP hostnames and WireGuard peers (marked *fork* below). Those were validated against **OPNsense 26.1.11** on a VLAN-segmented home network with PPPoE WAN, dnsmasq DHCP, a WireGuard client tunnel and a 7-device Tailscale tailnet.

## Implemented coverage

- HTTPS API requests with the API key and secret in HTTP Basic Auth.
- System hostname, product version, release description and target architecture from `system_information`.
- Per-interface cumulative RX/TX bytes and packet errors from `get_interface_statistics`, exposed as `netIf`. The interface totals come from the `<Link#N>` row, so an interface with several addresses is counted once. Counter values are accepted both as JSON numbers and as quoted decimals.
- The ethernet interfaces are also announced as ports (`fdb.ports`), which is what lets the dashboard show their counters. Loopback, `pflog`, `pfsync` and `enc` are excluded, and no negotiated speed is reported because the API does not expose it.
- IPv4 ARP entries, interface attribution and expired state from `search_arp`.
- Memory from `system_resources` and the hottest sensor from `system_temperature`. Both are best effort: a user without the dashboard privilege keeps the counters and the ARP.
- Uptime and load average from `system_time`, and CPU usage from the first event of `cpu_usage/stream`. The firewall serves the CPU as an SSE stream that never ends by itself, so the pusher reads one event and closes the connection. These are best effort too. The load average travels in the payload for parity with the native agent, which reports it from ubus, but the dashboard does not render it yet.
- Per-client traffic for the Clients view. Network Insight (`FlowSourceAddrTotals`) is the preferred source and keeps exact per-host byte totals: the pusher reads the fixed "today" window and reports `out` (traffic towards the host, its download) and `in` (traffic from it) per MAC, using the ARP table for the IP to MAC mapping. Without the `Diagnostics: Network Insight` privilege the endpoint answers 403 and the pusher falls back to `traffic/top`, which integrates a 2 second iftop rate into a running total per MAC; that number is an average of samples, not bytes counted by the firewall, and it misses transfers shorter than the poll interval. In both cases a host that is not in the ARP table is not reported, and the download/upload split was checked against a known download (#65, #78).
- Optional WAN RX/TX rates from counter deltas when an interface is explicitly selected.
- Standard CA verification, a PEM CA file, a full-certificate SHA-256 pin or an explicit insecure opt-out.
- Terminal classification for HTTP 401 and 403 responses. Other HTTP failures remain retryable.
- A standalone `opnsense-pusher` for the NetPulse managed installer, plus registration in the shared integrations supervisor.

All requests use GET. The integration does not write OPNsense configuration, install firmware or perform upgrades.

### Fork additions

All best effort: a missing plugin or privilege (403/404) drops that section and never fails the push.

- *fork* DHCP leases from Kea (`kea/leases4/search`), dnsmasq (`dnsmasq/leases/search`) and ISC (`dhcpv4/leases/searchLease`), merged by MAC. Expired leases are dropped; reservations without an expiry are kept.
- *fork* Interface overview (`interfaces/overview/interfacesInfo`): the `wan` interface gives the WAN device, IPv4, gateway and DNS, and is used for WAN rates when `OPNSENSE_WAN_INTERFACE` is empty. Ports get the assigned interface description as label and the negotiated speed from the media string. The `lan` MAC becomes the bridge MAC and every interface MAC is announced, so the firewall is never drawn as a client.
- *fork* Device names from the hostname OPNsense resolves for each ARP entry, sent as `discovery.hostByIp`. Names appear even without a DHCP lease privilege.
- *fork* WAN latency and loss from dpinger (`routes/gateway/status`, privilege `System: Gateways`): the gateway whose address is the WAN gateway, else one named WAN. Feeds the WAN card and the health score.
- *fork* Every assigned IPv4 subnet (VLANs, LAN, MGMT) as a NetPulse network with its name, CIDR and VLAN tag, and the VLAN each ARP MAC is seen on (`vlan0.10` gives 10). NetPulse uses them for device VLANs, per-network client lists and the per-VLAN topology view.
- *fork* Tailscale peers from the os-tailscale plugin (`tailscale/status/status`, the plugin's `tailscale status --json`), added to the WireGuard section as interface `tailscale0`: device name, Tailscale IPs, last handshake, cumulative transfer and the direct endpoint when the link is not relayed. Tailscale runs WireGuard underneath, so the NetPulse WireGuard panel, alerts and 30-day history apply unchanged. A peer that is online but idle has no recent handshake and shows as inactive, like an idle WireGuard peer.
- *fork* WireGuard peers from `wireguard/service/show`: interface, peer name, endpoint, allowed IPs, last handshake and cumulative transfer, sent in the `wireguard` payload section. The NetPulse server needs [netpulse-opnsense](https://github.com/n0deforge/netpulse-opnsense) to read it; an upstream server ignores the field.

## Deliberate omissions and unresolved behavior

- No interface is assumed to be WAN. Without explicit selection, WAN metadata and traffic rates are omitted. *Fork:* the interface OPNsense assigns as `wan` is used when the overview is readable.
- The first WAN poll establishes a counter baseline and emits no rate. Counter resets also emit no rate for that sample.
- Link state, negotiated speed and WAN IP/gateway/DNS are not inferred from interface statistics. *Fork:* they come from the interface overview instead. A PPPoE WAN has no media, so its port reports no speed.
- NDP/IPv6 neighbor data is not mixed into the IPv4-only ARP payload.
- Aliases and gateway monitoring are outside this first slice. *Fork:* DHCP leases are covered.

## API privileges

The key belongs to an OPNsense user, and the key is the username while the secret
is the password. On 26.7 these privileges are enough for a read-only collector,
no more:

| Privilege | Endpoints it covers |
| --- | --- |
| `Diagnostics: ARP Table` | `diagnostics/interface/get_arp` |
| `Diagnostics: Netstat` | `diagnostics/interface/get_interface_statistics` |
| `Lobby: Dashboard` | `diagnostics/system/system_information` |
| `Diagnostics: Network Insight` | `diagnostics/networkinsight/top` (optional, exact per-client totals) |
| `Reporting: Traffic` | `diagnostics/traffic/top` (optional, per-client traffic fallback) |
| `Status: Interfaces` | `interfaces/overview/interfacesInfo` (*fork*, optional, WAN details, port labels and speed) |
| `Services: Dnsmasq DNS/DHCP` | `dnsmasq/leases/search` (*fork*, optional, DHCP leases on dnsmasq) |
| `Services: Kea DHCP` | `kea/leases4/search` (*fork*, optional, DHCP leases on Kea) |
| `Status: DHCP leases` | `dhcpv4/leases/searchLease` (*fork*, optional, legacy ISC DHCP) |
| `VPN: WireGuard` | `wireguard/service/show` (*fork*, optional, WireGuard peers) |
| `System: Gateways` | `routes/gateway/status` (*fork*, optional, WAN latency and loss). **Broader than needed:** OPNsense attaches the status endpoint only to this privilege, which also covers `routing/settings/*` and can change gateway configuration. `Status: Gateways` does not cover it. The pusher only reads the status |
| `Tailscale` | `tailscale/status/status` (*fork*, optional, Tailscale peers). **Broader than needed:** the plugin has one privilege that also covers its settings, service and authentication endpoints, which can change the configuration. The pusher only reads the status endpoint; grant it only if that trade-off is acceptable |

Per-client traffic needs one of the two traffic privileges above. `Reporting: Traffic` works out of the box (it samples with iftop). `Diagnostics: Network Insight` gives exact byte totals but only has data once **Reporting > NetFlow** captures the LAN-side interfaces with *Capture local* enabled. Set `OPNSENSE_TRAFFIC_INTERFACES` to the logical names of those interfaces (`opt2,opt3,...`), not just `lan`.

`System: Firmware` is not needed, even though `core/firmware/status` answers 403
without it. Every request the pusher makes lives in the `diagnostics` module. The
interface label lookup is best effort and falls back to the raw interface name
when the user cannot read it.

On OPNsense 26.7 these three privileges also answer `system_time`,
`system_resources`, `system_temperature` and `cpu_usage/stream` (reported in
#55), so the system vitals need no extra grant. `activity/get_activity` does
answer 403 with them and would need `Diagnostics: System Activity`, which is why
the pusher reads the CPU stream instead.

## Managed NetPulse installation

The catalog entry installs the standalone binary on the NetPulse host. The current generic managed installer supplies the host, API key and API secret, but it does not expose a WAN interface, CA file or certificate pin.

Two beta limitations follow from that existing installer contract:

- It sets `OPNSENSE_INSECURE=1`, so verification of the OPNsense certificate is disabled. Use this mode only on a trusted network during the beta.
- It does not set `OPNSENSE_WAN_INTERFACE`, so WAN rates are omitted. System data, interface counters and ARP are still collected.

For authenticated TLS or WAN rates, use the standalone environment or shared supervisor configuration below.

## Standalone configuration

```sh
export OPNSENSE_HOST=https://opnsense.example
export OPNSENSE_USER=REPLACE_WITH_API_KEY
export OPNSENSE_PASS=REPLACE_WITH_API_SECRET
export OPNSENSE_CA_CERT=/etc/netpulse/opnsense-ca.pem
export OPNSENSE_WAN_INTERFACE=em0
export OPNSENSE_TRAFFIC_INTERFACES=lan
export NETPULSE_SERVER=http://netpulse.example:3000
export NETPULSE_TOKEN=REPLACE_WITH_EXTERNAL_AGENT_TOKEN
export NETPULSE_SLUG=opnsense-lab
export NETPULSE_INTERVAL=60s
./opnsense-pusher
```

`OPNSENSE_HOST` accepts a hostname, IP or HTTPS base URL. Plain HTTP is not accepted. `OPNSENSE_USER` is the API key and `OPNSENSE_PASS` is its secret. The secret is displayed only once when the key is created.

`OPNSENSE_TRAFFIC_INTERFACES` is the comma separated list of logical interface names whose per-host rates feed the Clients view (`lan` by default, `lan,opt1` for more than one). The managed installer does not set it, so a firewall whose LAN is not `lan` needs the standalone or shared-supervisor configuration for per-client traffic. The feature also needs the `Reporting: Traffic` privilege on the API user, which the README above lists as optional.

TLS trust options for the OPNsense connection:

| Variable | Meaning |
|---|---|
| `OPNSENSE_CA_CERT` | Path to a PEM file containing the CA that signed the firewall certificate, added to the system roots |
| `OPNSENSE_CERT_FP` | One SHA-256 fingerprint of the full leaf certificate. Colons and spaces are ignored, so the value can be pasted as the GUI prints it. |
| `OPNSENSE_INSECURE=1` | Skips certificate verification as an explicit last-resort opt-out |

`OPNSENSE_CA_CERT` and `OPNSENSE_CERT_FP` are mutually exclusive. With neither, normal system-root and hostname verification applies. These options cover only the connection to OPNsense. For an HTTPS NetPulse destination, the existing `NETPULSE_SERVER_FP` variable pins the NetPulse server SPKI.

## Shared supervisor configuration

Add an object to the supervisor `integrations` array:

```json
{
  "type": "opnsense",
  "slug": "opnsense-lab",
  "token": "REPLACE_WITH_EXTERNAL_AGENT_TOKEN",
  "interval": "60s",
  "host": "https://opnsense.example",
  "user": "REPLACE_WITH_API_KEY",
  "password": "REPLACE_WITH_API_SECRET",
  "wan_interface": "em0",
  "ca_cert": "/etc/netpulse/opnsense-ca.pem",
  "insecure": false
}
```

The supervisor uses `cert_fp` with the same meaning as `OPNSENSE_CERT_FP`, including the colon handling. Keep its configuration readable only by its service account because it contains the API secret and NetPulse agent token.

## Local verification

```sh
go test ./opnsense/... ./cmd/integrations-supervisor -count=1
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
```

The tests use `httptest` TLS servers and synthetic fixtures under `opnsense/internal/opnsense/testdata`. They do not contact an OPNsense system.

## Evidence needed before leaving beta

1. Run against a supported OPNsense version with a dedicated read-only API user.
2. Capture sanitized responses for system information, interface statistics and ARP, including interface counter values and content types.
3. Confirm the exact privileges required for each endpoint.
4. Verify counter units, counter reset behavior, WAN rate signs and interface names over several polls.
5. Decide from evidence whether NDP, link state, DHCP or WAN address data can be represented without guessing.
6. Add compatibility fixtures for every observed response variant.

## Official sources

- API keys and Basic Auth: https://docs.opnsense.org/development/how-tos/api.html
- API conventions: https://docs.opnsense.org/development/api.html
- Diagnostics API: https://docs.opnsense.org/development/api/core/diagnostics.html
- User privileges: https://docs.opnsense.org/manual/users.html
- System response implementation: https://github.com/opnsense/core/blob/master/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/SystemController.php
- Interface and neighbor response implementation: https://github.com/opnsense/core/blob/master/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/InterfaceController.php
- ARP source fields: https://github.com/opnsense/core/blob/master/src/opnsense/scripts/interfaces/list_arp.py
- NDP source fields: https://github.com/opnsense/core/blob/master/src/opnsense/scripts/interfaces/list_ndp.py
