---
name: v2dock
description: Operate a v2dock install from the terminal - check the VPN connection, test and switch V2Ray servers, manage subscriptions, choose which Docker containers go through the VPN, set per-container VPN quotas, and read usage. Use when the user asks about v2dock, keeping servers online during an internet shutdown, or routing a container through V2Ray/Xray.
---

# Operating v2dock

v2dock runs as three containers (guard, agent, web). Talk to it with the
`v2dock` CLI, always with `--json`.

## 1. Find the CLI

On the server:

```sh
AGENT=$(docker ps --filter label=io.v2dock.role=agent --format '{{.Names}}' | head -1)
docker exec "$AGENT" v2dock --json status
```

Elsewhere, with an API token from the user (never invent one):

```sh
V2DOCK_URL=https://v2dock.example.com V2DOCK_TOKEN=v2d_... v2dock --json status
```

Exit codes: 0 ok, 2 bad input, 3 not found, 4 auth, 5 busy, 6 unreachable.

## 2. Read the situation first

```sh
v2dock --json status        # connected? which server? health.ok / health.delayMs / health.failures
v2dock --json route list    # which containers use the VPN, quotas, exceeded?
v2dock --json events --limit 20
```

`status.connected=false` means no VPN: routed containers use the normal network.
`health.failures>0` means the active server is failing; auto failover switches
after `failoverThreshold` failures if `autoFailover` is on.

## 3. Common tasks

| Goal | Commands |
|---|---|
| Add a subscription | `sub add '<url>' --name NAME` (then `server list --sort delay`) |
| Find working servers | `server test --wait` (real delay) or `server test --tcp --wait` |
| Connect the fastest | `connect --best` (optionally `--sub ID`) |
| Use a specific server | `connect <server-id>` |
| Put a container on the VPN | `container list` → `route add <name-or-key>` |
| Limit its VPN traffic | `route add <container> --quota 20GB --period monthly --action block` |
| Take it off the VPN | `route remove <container>` |
| See usage | `usage --range 24h` or `usage <container> --range 7d` |
| Keep Iranian traffic direct | `settings set bypass-iran=true` (default) |
| Diagnose routing | `diag` (firewall table, docker, routed IPs) |

## 4. Ask before you

- `disconnect`, `connect` to a different server, `route remove`, `sub remove`,
  `settings set bypass-iran=false`: these change live production traffic.
- Creating admin-scope tokens or changing the password.

## 5. Never

- Print or store subscription URLs, server links, tokens or passwords.
- Run two `server test` at once (exit 5 `busy`): wait for the first.
