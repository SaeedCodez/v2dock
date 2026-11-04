# v2dock test site

A small "network check" page for trying v2dock. It runs as its own container;
select that container in v2dock, open the page, and it shows which services
the container can reach.

Every check is made by the Go server inside the container, never by your
browser, and only to a fixed list of URLs compiled into the binary.

What it shows:

- **Egress**: the public IP, country and ISP the outside world sees
  (api.ipify.org and ip-api.com, with ipwho.is as a fallback).
- **Iranian services**: Digikala, Aparat, Divar, Snapp, Shaparak, Eitaa,
  Varzesh3. These should go direct when the Iran bypass is on.
- **Filtered in Iran**: YouTube, Telegram, X, Instagram, Facebook,
  Wikipedia. From Iran these only work through the VPN.
- **International / sanctioned**: Docker Hub, GitHub, Google, OpenAI,
  Cloudflare. These often refuse Iranian IPs.

A service counts as reachable (green) when any HTTP response with a status
below 500 comes back, so a 401 or 403 still means the network path works.
Red means a DNS failure, timeout, reset, TLS error or 5xx. A name that resolves
to a private address such as 10.10.34.x points to DNS filtering.

## Run it with v2dock

From the repository root:

```sh
docker compose -f docker-compose.yml -f docker-compose.local.yml -f testsite/docker-compose.yml up -d --build
```

Open <http://127.0.0.1:8089>. In v2dock (<http://127.0.0.1:8088>) route the
`testsite` container, then press **Run checks again**. With the VPN on, the
egress country should leave Iran; with the Iran bypass on, the Iranian
services should still answer directly.

## Run it on its own

```sh
docker build -t v2dock-testsite:local testsite
docker run --rm -p 127.0.0.1:8089:8080 v2dock-testsite:local
```

Or without Docker: `cd testsite && go run .`

If pulling `golang:1.26-alpine` is too slow, `Dockerfile.prebuilt` packages a
binary cross-compiled on the host into `.bin/` (steps are in that file).

## Endpoints

| Path | Returns |
| --- | --- |
| `GET /` | the page |
| `GET /api/checks` | `{egress, checks: [{id, name, group, url, ok, status, ms, error, ip}], ranAt}` |
| `GET /api/egress` | `{ip, country, countryCode, isp, error}` |
| `GET /healthz` | `ok` |

Checks run concurrently with an 8 second timeout each. Only one run happens
at a time, and its result is reused for 5 seconds.
