# AGENTS.md

v2dock routes chosen Docker containers' international traffic through a
V2Ray/Xray server, so servers keep reaching the outside world during internet
shutdowns. Go backend (`internal/`), React UI (`ui/`), SQLite state.

## Operating v2dock (status, servers, routing)

Use the CLI. Full reference: [`docs/cli.md`](docs/cli.md). Skill:
[`.claude/skills/v2dock/SKILL.md`](.claude/skills/v2dock/SKILL.md).

- On the server: `docker exec <agent-container> v2dock --json <command>`
  (find it: `docker ps --filter label=io.v2dock.role=agent --format '{{.Names}}'`).
- Remotely: `V2DOCK_URL=https://… V2DOCK_TOKEN=v2d_… v2dock --json <command>`.

Rules:

1. Always pass `--json`; check the exit code and the `ok` field.
2. `disconnect`, `route remove`, `sub remove` and `connect` (to a different
   server) change live traffic of production containers. Ask the user before
   running them unless they asked for exactly that.
3. Never print subscription URLs, server links or tokens. The API never returns
   them; do not try to read them from the database.
4. Exit 5 with `code: "busy"` means a server test is running; wait for it
   (`server test --wait` after it finishes) instead of starting another.

## Developing

```sh
cd ui && npm ci && npm run build && cd ..   # UI -> internal/web/dist (embedded)
go vet ./... && go test ./...                # geo data for tests: see dev/README.md
docker compose -f docker-compose.yml -f docker-compose.local.yml up -d --build
```

Layout:

```
cmd/v2dock/          one binary: agent | web | guard daemons + the CLI
internal/agent/      daemon: reconcile loop, API (unix socket), auth, failover, quotas
internal/xcore/      Xray-core embedded: config builder, instances, real-delay probe
internal/links/      share-link and subscription parser (untrusted input)
internal/netfw/      nftables redirect table (+ UFW accept rules)
internal/docker/     read-only Docker client + guard (socket allowlist proxy)
internal/store/      SQLite schema and queries
internal/web/        gateway: static UI, /api reverse proxy, security headers
internal/cli/        CLI commands
ui/                  React + Tailwind source
testsite/            network-check site used to test routing
dev/mock/            fake V2Ray server + subscription for local tests
```

Conventions:

- Every state change goes through `Agent.Reconcile`, which is serialised and
  idempotent (it hashes the Xray config + client IPs and does nothing when
  unchanged). Never start Xray or touch nftables anywhere else.
- Container identity is `ContainerKey` (compose project/service, else name),
  stable across Coolify redeploys.
- New API endpoint → handler in `internal/agent/api.go`, CLI command in
  `internal/cli/cli.go`, line in `docs/cli.md`, UI helper in `ui/src/lib/api.js`.
- Security-sensitive changes (auth, gateway, guard allowlist, firewall
  rendering, link parsing) need a test next to the existing ones.
- Run `gofmt -l .`, `go vet ./...`, `go test ./...` before calling it done.
