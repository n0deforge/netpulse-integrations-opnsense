# Changelog

## wifictl/v0.1.0

- First release. Reads `GET /api/state` of a self-hosted Wi-Fi controller and pushes each managed AP as its own external unit.
- Board, firmware, uptime, MAC and the controller's ping to the AP; one radio per band with channel, width and client count; associated clients with band and signal.
- Client IPs and names as the APs report them, sent as `arp` and `dhcp.reservations`.
- One controller request per cycle shared by all AP units; login on 401 with the controller admin password.
- Unreadable or stale APs fail the collection so NetPulse marks them stale.
- Validated with a T18-21K Pro (no RSSI reported) and an ASUS RT-AC68U in AP mode (14 clients across both APs).
