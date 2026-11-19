# Changelog

## opnsense/v0.1.0-n0de.3 (n0deforge fork)

- Tailscale peers from the os-tailscale plugin, sent in the WireGuard section as interface `tailscale0`: MagicDNS name, Tailscale IPs, handshake, transfer, direct endpoint and control-plane presence (`online`). Needs the plugin's `Tailscale` privilege, which is broader than read-only; documented in the privilege table.
- WAN latency and loss need `System: Gateways`, not `Status: Gateways`: OPNsense attaches `routes/gateway/status` only to the former. Documented, with the trade-off.
- Validated on OPNsense 26.1.11: 7 tailnet peers (6 online, 1 offline), per-client traffic from `traffic/top` on five VLAN interfaces.

## opnsense/v0.1.0-n0de.2 (n0deforge fork)

- WAN latency and loss from dpinger gateway status (privilege: see n0de.3).
- Assigned IPv4 subnets sent as networks (name, CIDR, VLAN tag) and the VLAN of every ARP MAC, from the interface it is learned on.
- Documented the traffic privileges and the NetFlow prerequisite of Network Insight.

## opnsense/v0.1.0-n0de.1 (n0deforge fork)

- DHCP leases from Kea, dnsmasq and ISC, merged by MAC; expired leases dropped, reservations kept.
- Interface overview: WAN device, IPv4, gateway and DNS; WAN autodetected when `OPNSENSE_WAN_INTERFACE` is empty; port labels from the assigned interface description and speed from the media string; LAN MAC as bridge MAC and all interface MACs announced.
- Device names from the ARP table hostname, sent as `discovery.hostByIp`.
- WireGuard peers from `wireguard/service/show` in the new `wireguard` payload section (needs the netpulse-opnsense server).
- Base tests serve 404 for the optional endpoints, so their absence is covered.
- Validated against OPNsense 26.1.11 (PPPoE WAN, dnsmasq DHCP with 42 leases, one WireGuard client tunnel, 25 ARP entries over six VLANs).

## Unreleased

- Added the first beta read-only OPNsense pusher, based on official API documentation and synthetic fixtures.
- Reports system identity, interface counters, IPv4 ARP and optional WAN rates from an explicitly selected interface.
- Supports system CA verification, a CA file, a full-certificate SHA-256 pin and an explicit insecure opt-out.
- Added standalone amd64/arm64 packaging and shared-supervisor registration.
- Fixed the Diagnostics API paths, which pointed at a namespace that returns 404 on a real system.
- Counter decoding accepts the unquoted numbers OPNsense 26.7 returns and reads the interface totals from the link row.
- Validated end to end against a fresh OPNsense 26.7 system with a real API key.
- `OPNSENSE_CERT_FP` accepts the colon separated fingerprint printed by the OPNsense GUI.
- The ethernet interfaces are announced as ports, so the dashboard shows their counters. There is no speed and no link description beyond the interface name.
- Reports memory and the hottest temperature sensor, both best effort, so a missing dashboard privilege does not cost the counters.
- Reports uptime and load average from `system_time`, and CPU usage from the first event of the `cpu_usage/stream` SSE feed.
- Reports per-client traffic from `traffic/top` by integrating the sampled rates into per-MAC totals, with the ARP table as the IP to MAC bridge.
- Per-client traffic now prefers Network Insight (`FlowSourceAddrTotals`), which keeps exact per-host byte totals, and falls back to `traffic/top` when the Insight privilege is missing. `out` is traffic towards the host (its download) and `in` is traffic from it, both filtered by the ARP table.
- Validation on user hardware is still required before marking the integration stable.
