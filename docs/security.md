# Security model

v2dock has to change another container's network path, which needs real
privileges on the host. The design keeps those privileges away from everything
that faces the network or parses untrusted input from the web.

## Three processes, three privilege levels

| Service | Runs as | Network | Capabilities | Can touch |
|---|---|---|---|---|
| `guard` | root | **none** | none | `/var/run/docker.sock` (read-only allowlist) |
| `agent` | root, group 65532 | host | `NET_ADMIN` only | nftables / policy routing, its SQLite DB, Xray |
| `web` | uid 65532 | Docker bridge (Coolify proxy) | none | the agent's unix socket |

All three run with `read_only: true`, `no-new-privileges` and `cap_drop: [ALL]`.

**guard** is the only process with the Docker socket. It forwards exactly these
`GET` calls and refuses everything else (`internal/docker/guard.go`, tested in
`guard_test.go`):

- `/_ping`, `/version`
- `/containers/json` (list; never contains environment variables)
- `/containers/{id}/stats`
- `/events`

There is no `inspect` (which would expose every container's environment
secrets), no `exec`, no create/start/stop, no images, no volumes, no `POST`.
Request headers from the caller are dropped. A compromised agent therefore can
neither read your other apps' secrets nor control containers.

**agent** needs `network_mode: host` and `NET_ADMIN` to install the nftables
rules that redirect other containers' connections; that is the minimum the
kernel requires. Its API is a unix socket (mode 0660, group 65532) in a volume
shared only with `web`; it opens no API port. Xray runs inside the agent
process, so there is no Xray API port and no SOCKS/HTTP proxy port. The only
listeners are the two transparent-proxy ports (TCP 12701, DNS 12702). They
accept only connections that v2dock's own rule redirected: nftables drops
every packet to them whose conntrack entry is not DNAT, and inside Xray any
source that is not a selected container hits a final `block` rule.

The agent owns only its own firewall objects: table `ip v2dock` and, on hosts
whose `INPUT` chain drops traffic (UFW), two `ACCEPT` rules that match only
redirected (`--ctstate DNAT`) connections to those two ports. All of it is
removed on shutdown, so a stopped v2dock never leaves containers pointing at a
dead proxy. Rules are rendered from parsed `netip.Addr` values, never from
strings a user or subscription supplied.

Why redirect (DNAT) rather than TPROXY: Docker enables `br_netfilter`, which
runs the prerouting hooks while the packet is still on the bridge; the socket a
TPROXY rule assigns there is dropped when the packet reaches the IP layer, so
the kernel resets the connection. Destination NAT survives that path (Docker's
published ports depend on it), and Xray reads the original destination from
conntrack.

**web** is the internet-facing gateway. It serves the embedded UI and forwards
`/api/*` to the agent socket. It runs as an unprivileged user with no
capabilities and no access to Docker or the host network.

## Authentication

- **One admin password**, hashed with Argon2id (64 MiB, t=3). At most two
  hashes run at once, which bounds memory under a login flood.
- **First run** cannot be hijacked: either the password comes from
  `SERVICE_PASSWORD_ADMIN` / `V2DOCK_ADMIN_PASSWORD`, or the agent prints a
  one-time setup token to its log and the setup page requires it.
  The variable is read only while no password exists, so changing the
  password in Settings afterwards makes it irrelevant; the agent also clears
  it from its own environment after reading it.
- **Login throttling** per client IP (5 failures → 1 min lock, doubling up to
  an hour) plus a global brake against distributed guessing. Failed logins are
  written to the activity log with the client IP.
- **Optional TOTP 2FA** (RFC 6238, ±30 s, a code can be used only once).
- **Sessions**: 256-bit random tokens; only their SHA-256 is stored. Cookie is
  `HttpOnly`, `SameSite=Strict`, and `Secure` + `__Host-` prefixed behind HTTPS.
  7-day absolute and 24-hour idle expiry. Changing the password ends every
  session; "Sign out everywhere" does too.
- **API tokens** for the CLI / AI agents: shown once, stored hashed, `read`
  (GET only) or `admin` scope. No token can create tokens, change the password
  or touch 2FA.

## Request handling

- **CSRF**: cookie-authenticated state changes need the `X-V2dock-Csrf` header,
  which a cross-site page cannot send without a CORS preflight; the gateway
  answers every preflight with 403 and never sends CORS headers. The gateway
  also rejects unsafe requests whose `Origin` is another site or whose
  `Sec-Fetch-Site` is `cross-site`.
- **Client IP** comes from `X-Forwarded-For` only when the direct peer is a
  trusted proxy (private ranges by default, i.e. Coolify's Traefik), walking
  from the right so a client cannot pick its own address.
- **Headers**: strict CSP (`script-src 'self'`, no inline scripts),
  `frame-ancestors 'none'`, `nosniff`, `no-referrer`, COOP/CORP, HSTS on HTTPS.
- **Input**: JSON bodies are size-limited (1 MB) and decoded strictly (unknown
  fields are errors). Names, URLs, DNS servers, quota values and container keys
  are validated server-side.

## Untrusted subscription content

Subscription bodies come from a remote server and are treated as hostile:
size-capped (4 MB, 2000 servers), every link parsed into a typed structure,
host names and ports validated, display names stripped of control and
bidi-override characters and length-limited, and only known protocols,
transports and options are turned into Xray configuration. Links whose
options Xray no longer supports are skipped with a reason instead of being
passed through. Xray itself also refuses plaintext VLESS/Trojan to public
addresses.

Subscription URLs and server links contain credentials. They are stored only in
the agent's database (directory 0700, file 0600) and never returned by the API:
URLs are shown masked, server links not at all.

## What v2dock does not protect against

- Someone with root on the host, or able to `docker exec` into the agent, is
  trusted (the CLI works over the socket without a password for that reason).
- IPv6: only IPv4 traffic of routed containers is diverted. Docker networks are
  IPv4-only by default; if you enabled IPv6 on a network, that traffic is not
  proxied.
- UDP other than DNS (QUIC, NTP, game protocols) is not proxied; it uses the
  normal network. Clients fall back from QUIC to TCP, which is proxied.
- Containers using `network_mode: host` share the host's network and cannot be
  routed individually.

## Reporting

Please open a private security advisory on GitHub rather than a public issue.
