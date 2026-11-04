CREATE TABLE IF NOT EXISTS kv (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS subscriptions (
  id                TEXT PRIMARY KEY,
  name              TEXT NOT NULL,
  url               TEXT NOT NULL DEFAULT '',
  kind              TEXT NOT NULL DEFAULT 'url',
  auto_update_hours INTEGER NOT NULL DEFAULT 12,
  last_fetch_at     INTEGER NOT NULL DEFAULT 0,
  last_success_at   INTEGER NOT NULL DEFAULT 0,
  last_error        TEXT NOT NULL DEFAULT '',
  userinfo          TEXT NOT NULL DEFAULT '',
  skipped           TEXT NOT NULL DEFAULT '',
  via_vpn           INTEGER NOT NULL DEFAULT 0,
  created_at        INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS servers (
  id              TEXT NOT NULL,
  subscription_id TEXT NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
  position        INTEGER NOT NULL,
  name            TEXT NOT NULL,
  protocol        TEXT NOT NULL,
  address         TEXT NOT NULL,
  port            INTEGER NOT NULL,
  transport       TEXT NOT NULL,
  security        TEXT NOT NULL,
  warning         TEXT NOT NULL DEFAULT '',
  link            TEXT NOT NULL,
  outbound        TEXT NOT NULL,
  tcp_ms          INTEGER,
  tcp_error       TEXT NOT NULL DEFAULT '',
  tcp_at          INTEGER NOT NULL DEFAULT 0,
  delay_ms        INTEGER,
  delay_error     TEXT NOT NULL DEFAULT '',
  delay_at        INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (subscription_id, id)
);
CREATE INDEX IF NOT EXISTS servers_id ON servers(id);

CREATE TABLE IF NOT EXISTS routes (
  key          TEXT PRIMARY KEY,
  label        TEXT NOT NULL,
  quota_bytes  INTEGER NOT NULL DEFAULT 0,
  quota_period TEXT NOT NULL DEFAULT 'monthly',
  quota_action TEXT NOT NULL DEFAULT 'block',
  quota_used   INTEGER NOT NULL DEFAULT 0,
  period_start INTEGER NOT NULL DEFAULT 0,
  exceeded     INTEGER NOT NULL DEFAULT 0,
  created_at   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS usage_min (
  ts       INTEGER NOT NULL,
  key      TEXT NOT NULL,
  vpn_up   INTEGER NOT NULL DEFAULT 0,
  vpn_down INTEGER NOT NULL DEFAULT 0,
  dir_up   INTEGER NOT NULL DEFAULT 0,
  dir_down INTEGER NOT NULL DEFAULT 0,
  net_rx   INTEGER NOT NULL DEFAULT 0,
  net_tx   INTEGER NOT NULL DEFAULT 0,
  cpu_sum  REAL NOT NULL DEFAULT 0,
  cpu_n    INTEGER NOT NULL DEFAULT 0,
  mem_max  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (ts, key)
);

CREATE TABLE IF NOT EXISTS usage_hour (
  ts       INTEGER NOT NULL,
  key      TEXT NOT NULL,
  vpn_up   INTEGER NOT NULL DEFAULT 0,
  vpn_down INTEGER NOT NULL DEFAULT 0,
  dir_up   INTEGER NOT NULL DEFAULT 0,
  dir_down INTEGER NOT NULL DEFAULT 0,
  net_rx   INTEGER NOT NULL DEFAULT 0,
  net_tx   INTEGER NOT NULL DEFAULT 0,
  cpu_sum  REAL NOT NULL DEFAULT 0,
  cpu_n    INTEGER NOT NULL DEFAULT 0,
  mem_max  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (ts, key)
);

CREATE TABLE IF NOT EXISTS events (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  ts      INTEGER NOT NULL,
  level   TEXT NOT NULL,
  kind    TEXT NOT NULL,
  message TEXT NOT NULL,
  actor   TEXT NOT NULL DEFAULT '',
  ip      TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS sessions (
  id_hash    TEXT PRIMARY KEY,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL,
  ip         TEXT NOT NULL DEFAULT '',
  user_agent TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS api_tokens (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  hash         TEXT NOT NULL UNIQUE,
  scope        TEXT NOT NULL,
  created_at   INTEGER NOT NULL,
  last_used_at INTEGER NOT NULL DEFAULT 0
);
