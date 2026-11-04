# v2dock CLI

The `v2dock` binary is also the command line. It is built for scripts and AI
agents: every command takes `--json`, prints exactly one JSON object with an
`ok` field, and exits with a stable code.

## Where to run it

**On the server (recommended)** — inside the **agent** container, over the
unix socket, no password needed (the agent trusts only root callers on its
socket; whoever can `docker exec` is already root). Running the CLI in the web
container does not work without a token, by design:

```sh
docker exec <agent-container> v2dock --json status
```

On Coolify the container is named like `agent-<uuid>`; find it with
`docker ps --filter label=io.v2dock.role=agent --format '{{.Names}}'`.

**From anywhere** — over HTTPS with an API token (Settings → API tokens, or
`v2dock token create`):

```sh
export V2DOCK_URL=https://v2dock.example.com
export V2DOCK_TOKEN=v2d_...
v2dock --json status
```

Read-only tokens can run every listing command; changing anything needs a
full-access token. No token can manage passwords, 2FA or tokens.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | error |
| 2 | invalid usage or input |
| 3 | not found |
| 4 | not authenticated / not allowed |
| 5 | busy or conflict (e.g. a server test is already running) |
| 6 | v2dock unreachable |

With `--json`, failures print `{"ok":false,"code":"...","error":"...","exit":N}`.

## Commands

### Connection

| Command | What it does |
|---|---|
| `status` | connection state, active server, health (real delay), Iran bypass, routed containers, today's traffic |
| `connect <server-id>` | use this server |
| `connect --best [--sub ID]` | real-delay test up to 40 servers (fastest-known first) and connect the fastest that works |
| `disconnect` | stop the proxy; routed containers use their normal network |

### Subscriptions and servers

| Command | What it does |
|---|---|
| `sub list` | subscriptions with server count, last update, plan usage; URLs masked |
| `sub add <url> [--name N] [--hours 12]` | add and download (falls back to downloading through the VPN) |
| `sub refresh <id>` / `sub refresh --all` | download again |
| `sub rename <id> <name>` | |
| `sub remove <id>` | also removes its servers |
| `server list [--sub ID] [--sort delay\|tcp\|name] [--limit N]` | servers with last TCP ping and real delay; `●` marks the active one |
| `server test [--tcp] [--sub ID] [--ids a,b] [--wait]` | start a test (real delay by default); `--wait` blocks and, with `--json`, returns the sorted list |
| `server add '<link>' [--name N]` | add one link to the "Manual" list |
| `server remove <id>` | Manual list only |

### Containers

| Command | What it does |
|---|---|
| `container list [--all]` | containers, whether they use the VPN, today's VPN/direct bytes, quota, key |
| `route list` | containers that use the VPN, with quotas |
| `route add <container> [--quota 10GB] [--period daily\|weekly\|monthly\|total] [--action block\|direct]` | send the container's international traffic through the VPN; re-running updates the quota (`--quota 0` removes it) |
| `route remove <container>` | back to the normal network |
| `route reset <container>` | reset quota usage |
| `usage [<container>] [--range 1h\|24h\|7d\|30d]` | VPN vs direct bytes, average CPU, peak memory |

`<container>` may be the key from `container list` (`compose:<project>/<service>`
or `name:<name>`), the container name, an id prefix, or the label. Keys are
stable across redeploys, so Coolify apps keep their routing and quotas.

Quota actions: `block` keeps Iranian destinations working but blocks
everything else once the limit is reached; `direct` takes the container off
the VPN until the next period.

### Settings and security

| Command | What it does |
|---|---|
| `settings` | show settings |
| `settings set key=value ...` | keys: `bypass-iran`, `auto-failover`, `failover-threshold`, `failover-scope` (`all`/`subscription`), `health-interval` (s), `test-url`, `remote-dns`, `domestic-dns` |
| `events [--limit N] [--kind K]` | activity log; kinds: connection, failover, health, route, quota, subscription, settings, security |
| `token create <name> [--scope read\|admin]` / `token list` / `token revoke <id>` | API tokens (server-side only) |
| `password set` | reads a new password from stdin (server-side only) |
| `diag` | firewall rules installed, Docker reachable, routed IPs |

## Recipes

```sh
# First setup from a terminal
docker exec v2dock-agent v2dock sub add 'https://panel.example.com/sub/XXXX' --name provider
docker exec v2dock-agent v2dock connect --best
docker exec v2dock-agent v2dock route add my-api --quota 50GB --period monthly

# An agent checking health and repairing
v2dock --json status | jq '.health'
v2dock --json server test --wait --sub abcd1234 | jq '.servers[0]'
v2dock --json connect --best

# Reset a forgotten password
echo 'a new long passphrase' | docker exec -i v2dock-agent v2dock password set
```
