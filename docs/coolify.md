# Deploying on Coolify

v2dock deploys as a normal Docker Compose resource. Only the `web` service is
exposed through Coolify's proxy; the other two never join a network
(`guard` has none, `agent` uses the host network, which Coolify leaves alone).

## Steps

1. **Projects → (your project) → + New → Public Repository**
   `https://github.com/SaeedCodez/v2dock`, branch `main`.
2. **Build Pack: Docker Compose**, compose file `/docker-compose.yml`.
3. On the **web** service, set your domain (e.g. `https://v2dock.example.com`).
   Coolify routes it to port 8080 through `SERVICE_FQDN_WEB_8080`.
4. Optional environment variables:
   - `V2DOCK_VERSION`: pin a release instead of `latest`.
   - `TZ`: time zone for daily/weekly/monthly quotas (default `Asia/Tehran`).
5. **Deploy.** Log in with the password in `SERVICE_PASSWORD_ADMIN`
   (Environment Variables tab; Coolify generated it). Change it, and turn on
   two-factor authentication, under Settings.

The image is pulled from `ghcr.io/saeedcodez/v2dock`, which is reachable from
Iranian servers. Nothing is built on your server.

## CLI on the server

Coolify names containers `<service>-<uuid>`:

```sh
AGENT=$(docker ps --filter label=io.v2dock.role=agent --format '{{.Names}}')
docker exec $AGENT v2dock status
```

Coolify's **Terminal** tab on the `agent` service works too (`v2dock status`).

## What to route

Your Coolify apps appear on the **Containers** page under their Coolify
names. Turning VPN on for an app follows it across redeploys and restarts. The
selection is keyed by the Coolify application uuid (`coolify:app/<uuid>`):
Coolify names an app's compose service `<uuid>-<deploy timestamp>`, so the
compose project/service pair changes on every deploy. Coolify services and
other compose stacks, whose service names are stable, are keyed by compose
project/service. Pull-request previews are separate deployments with their own
key. Routes saved under an app's old per-deploy key are moved to the stable key
automatically after upgrading.

Avoid routing Coolify itself (`coolify`, `coolify-proxy`, `coolify-db`,
`coolify-redis`) unless you know why: the proxy then fetches certificates
through the VPN, and the dashboard's own outbound calls depend on it.

## Troubleshooting

**The service stays "exited" and nothing is pulled.** Coolify's log shows
`CoolifyTask ... FAIL` right after `StartService`, and on the server
`docker compose up` reports *network <uuid> declared as external, but could
not be found*. Coolify gives the stack an external network named after the
service UUID but, because `agent` and `guard` use `network_mode`, it may not
create it. Create it once on the server, then start the service again:

```sh
docker network create --attachable <service-uuid>
```

**`all predefined address pools have been fully subnetted`.** Docker's
default pools allow about 31 networks; a busy Coolify server runs out, and
every new resource then fails the same way. Either give this network an
explicit subnet that is not in use:

```sh
docker network create --attachable --subnet 10.250.251.0/24 <service-uuid>
```

or, for all future resources, add smaller pools to `/etc/docker/daemon.json`
and restart Docker (this restarts every container on the server):

```json
{ "default-address-pools": [ { "base": "10.200.0.0/16", "size": 24 } ] }
```

## Notes

- Redeploying v2dock removes its firewall rules first; routed containers use
  the normal network for the few seconds until the new agent is up.
- If the host runs UFW, v2dock adds two INPUT rules that accept only its own
  redirected connections, and removes them when it stops.
- The agent needs a kernel with nftables NAT (every current distribution).
  `docker exec $AGENT v2dock diag` shows what is installed.
