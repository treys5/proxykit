-- Run with: npx wrangler d1 execute proxykit-analytics --file=schema.sql

CREATE TABLE IF NOT EXISTS runs (
  id                   INTEGER PRIMARY KEY AUTOINCREMENT,
  client_id            TEXT    NOT NULL,          -- random UUID, not tied to user identity
  app_version          TEXT    DEFAULT 'unknown',
  proxies_tested       INTEGER DEFAULT 0,
  proxies_passed       INTEGER DEFAULT 0,
  avg_ms               REAL,
  has_target           INTEGER DEFAULT 0,         -- 1 if target URL was tested
  target_pass_rate     REAL,                      -- 0-100
  ip_type_residential  INTEGER DEFAULT 0,
  ip_type_mobile       INTEGER DEFAULT 0,
  ip_type_datacenter   INTEGER DEFAULT 0,
  ip_type_unknown      INTEGER DEFAULT 0,
  top_isps             TEXT    DEFAULT '[]',      -- JSON: [{name, count}, ...]
  vendor_counts        TEXT    DEFAULT '{}',      -- JSON: {px:n, akamai:n, ...}
  status_counts        TEXT    DEFAULT '{}',      -- JSON: {2xx:n, 403:n, ...}
  country_counts       TEXT    DEFAULT '{}',      -- JSON: {US:n, GB:n, ...}
  created_at           INTEGER DEFAULT (unixepoch())
);

CREATE INDEX IF NOT EXISTS idx_runs_client    ON runs(client_id);
CREATE INDEX IF NOT EXISTS idx_runs_created   ON runs(created_at);
CREATE INDEX IF NOT EXISTS idx_runs_version   ON runs(app_version);
