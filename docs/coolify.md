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
names. Turning VPN on for an app follows it across redeploys (the selection is
keyed by the compose project and service, which Coolify keeps stable).

Avoid routing Coolify itself (`coolify`, `coolify-proxy`, `coolify-db`,
`coolify-redis`) unless you know why: the proxy then fetches certificates
through the VPN, and the dashboard's own outbound calls depend on it.

## Notes

- Redeploying v2dock removes its firewall rules first; routed containers use
  the normal network for the few seconds until the new agent is up.
- If the host runs UFW, v2dock adds two INPUT rules that accept only its own
  redirected connections, and removes them when it stops.
- The agent needs a kernel with nftables NAT (every current distribution).
  `docker exec $AGENT v2dock diag` shows what is installed.
