# Docker: NetPulse with OPNsense and a Wi-Fi controller

This directory runs the fork's NetPulse server and the two pushers it was built
around: OPNsense as the gateway, and a self-hosted Wi-Fi controller for the
access points. Each pusher is optional and sits behind a compose profile.

| Service | Image | Profile |
|---|---|---|
| `netpulse` | `ghcr.io/n0deforge/netpulse-opnsense` | always |
| `opnsense-pusher` | `ghcr.io/n0deforge/netpulse-integrations-opnsense` | `opnsense` |
| `wifictl-pusher` | same image | `wifictl` |

The pushers can also be built from this checkout: `docker compose build`.

## 1. Start the server

```sh
cp netpulse.env.example netpulse.env     # set AUTH_PASS
docker compose up -d netpulse
```

NetPulse listens on `127.0.0.1:3000`. Set `NETPULSE_PUBLISH_ADDR=0.0.0.0`
(and optionally `NETPULSE_PORT`) in a `.env` file next to `compose.yaml` to
reach it from the LAN. Log in as `admin` with `AUTH_PASS`.

On first boot NetPulse adds a "Gateway" pointing at the Docker bridge, because
the container cannot see your LAN. Delete it in Settings; the OPNsense unit
created below replaces it.

## 2. Create the units

Every router fed by a pusher is an `external` unit with its own agent token.
`register.sh` does both through the admin API and prints the token once:

```sh
./register.sh unit OPNsense 192.168.1.1 gateway
./register.sh unit "Living room AP" 192.168.1.2
./register.sh list
```

The host is informational for external units (NetPulse uses it to recognise
the unit in ARP tables); use the management IP.

## 3. OPNsense

On the firewall:

1. **System > Access > Users**: add a user, for example `netpulse`, with the
   privileges listed in [opnsense/README.md](../../opnsense/README.md#api-privileges).
   The optional ones add DHCP leases, WAN details and WireGuard peers.
2. Open that user, **API keys > +**, and keep the downloaded key file.
3. **Firewall > Rules**: allow the Docker host to reach *This Firewall* on the
   GUI port. The GUI may listen only on some interfaces
   (**System > Settings > Administration > Listen Interfaces**); point
   `OPNSENSE_HOST` at one of those addresses, not just the nearest one.

Then:

```sh
cp opnsense.env.example opnsense.env && chmod 600 opnsense.env
./register.sh fingerprint 192.168.1.1:443   # paste into OPNSENSE_CERT_FP
# fill NETPULSE_TOKEN, OPNSENSE_HOST, OPNSENSE_USER, OPNSENSE_PASS
docker compose --profile opnsense up -d
docker compose logs -f opnsense-pusher       # "pushed" every 30 s
```

## 4. Wi-Fi controller

The controller must expose `GET /api/state` as described in
[wifictl/README.md](../../wifictl/README.md). Create one unit per AP, then:

```sh
cp wifictl.env.example wifictl.env && chmod 600 wifictl.env
# WIFICTL_URL, WIFICTL_PASSWORD, WIFICTL_UNITS and one SLUG/TOKEN pair per AP
docker compose --profile wifictl up -d
```

`host.docker.internal` resolves to the Docker host, for a controller published
on the same machine.

## What to expect

After a minute (NetPulse waits 60 s before trusting a new MAC):

- **Topology**: Internet, the OPNsense unit, each AP under it and the Wi-Fi
  clients under their AP. Wired clients hang off the gateway; their switch port
  is only known when a managed switch reports it (SNMP).
- **Devices**: everything in the firewall's ARP table and DHCP leases, named
  from DHCP, the ARP hostname or the AP.
- **WireGuard**: peers of every OPNsense WireGuard interface, with alerts and
  30-day transfer. This needs the fork's server image; upstream ignores the data.

## Updating

```sh
docker compose pull
docker compose --profile opnsense --profile wifictl up -d
```

Data lives in the `netpulse-data` volume and survives recreation.
