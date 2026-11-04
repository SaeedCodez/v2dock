# Development helpers

## Geo data for tests

`internal/xcore` tests that build the full routing config need the Iran geo
files. They are not committed:

```sh
mkdir -p dev/assets
curl -fsSL -o dev/assets/geoip.dat  https://github.com/chocolate4u/Iran-v2ray-rules/releases/latest/download/geoip-lite.dat
curl -fsSL -o dev/assets/geosite.dat https://github.com/chocolate4u/Iran-v2ray-rules/releases/latest/download/geosite-lite.dat
```

## Mock V2Ray server

`dev/mock` runs a real Xray server (VLESS, VMess-WS, Shadowsocks-2022) on a
private Docker network plus a subscription URL, so the whole flow can be
tested without a provider. The credentials in that folder are public; never
use them anywhere else.

```sh
docker compose -f dev/mock/docker-compose.yml up -d
# subscription URL to add in v2dock:  http://172.31.250.11/sub.txt
```

The subscription also lists a dead server (`172.31.250.99`) for failover tests.

## Full local stack

```sh
docker compose -f docker-compose.yml -f docker-compose.local.yml -f testsite/docker-compose.yml up -d --build
```

- v2dock: http://localhost:8088 (setup token: `docker compose logs agent | grep setupToken`,
  or set `V2DOCK_ADMIN_PASSWORD` in `.env`)
- test site: http://localhost:8089 — turn on VPN for the `testsite` container
  and run the checks again.
