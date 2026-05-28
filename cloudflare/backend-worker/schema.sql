-- ── Proxy Tester Backend Schema ──────────────────────────────────────────────
-- Auth: ProxyKit license keys generated on Whop purchase (webhook-driven).
-- No passwords stored anywhere.

-- Users (keyed by Whop user_id, created on first key validation)
CREATE TABLE IF NOT EXISTS users (
  id              TEXT PRIMARY KEY,   -- whop user_id  (e.g. "user_xxxxxx")
  membership_id   TEXT,               -- whop membership id
  license_hint    TEXT,               -- last 4 chars of license key (display only)
  plan            TEXT NOT NULL DEFAULT 'pro',
  whop_product_id TEXT,
  whop_plan_id    TEXT,
  created_at      INTEGER NOT NULL,
  last_seen       INTEGER
);

-- License keys (generated on membership_went_valid webhook)
CREATE TABLE IF NOT EXISTS license_keys (
  key           TEXT PRIMARY KEY,             -- e.g. PT-A3F29B-C84E72D0-3FEFD8
  user_id       TEXT NOT NULL,                -- whop user_id
  membership_id TEXT,                         -- whop membership_id
  status        TEXT NOT NULL DEFAULT 'active', -- active | revoked
  created_at    INTEGER NOT NULL,
  revoked_at    INTEGER,
  FOREIGN KEY (user_id) REFERENCES users(id)
);

-- User sessions (fast lookup via KV, D1 kept for audit)
CREATE TABLE IF NOT EXISTS user_sessions (
  token       TEXT PRIMARY KEY,
  user_id     TEXT NOT NULL,
  created_at  INTEGER NOT NULL,
  expires_at  INTEGER NOT NULL,
  FOREIGN KEY (user_id) REFERENCES users(id)
);

-- Synced job results
CREATE TABLE IF NOT EXISTS synced_results (
  id          TEXT PRIMARY KEY,
  user_id     TEXT NOT NULL,
  list_name   TEXT,
  total       INTEGER,
  passed      INTEGER,
  failed      INTEGER,
  pass_rate   REAL,
  avg_ms      INTEGER,
  ip_analysis TEXT,
  data_usage  TEXT,
  top_proxies TEXT,
  synced_at   INTEGER NOT NULL,
  FOREIGN KEY (user_id) REFERENCES users(id)
);

-- ── Device locking (single-instance key enforcement) ──────────────────────────
-- device_id is set on first key validation; subsequent mismatches are rejected + logged.
-- ALTER TABLE license_keys ADD COLUMN device_id TEXT;  ← run once on existing DB

-- ── User preferences (webhook opt-in/out) ────────────────────────────────────
CREATE TABLE IF NOT EXISTS user_preferences (
  user_id                  TEXT PRIMARY KEY,
  discord_webhook_url      TEXT,               -- user's own Discord webhook
  global_discord_opt       INTEGER NOT NULL DEFAULT 0, -- 1 = subscribed to ProxyKit server alerts
  notify_px_changes        INTEGER NOT NULL DEFAULT 1,
  notify_provider_issues   INTEGER NOT NULL DEFAULT 1,
  updated_at               INTEGER NOT NULL,
  FOREIGN KEY (user_id) REFERENCES users(id)
);

-- ── Anonymous in-app suggestions ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS suggestions (
  id           TEXT PRIMARY KEY,
  body         TEXT NOT NULL,
  category     TEXT,             -- 'feature' | 'bug' | 'general'
  submitted_at INTEGER NOT NULL
);

-- ── Security / suspicious-access log ─────────────────────────────────────────
CREATE TABLE IF NOT EXISTS access_logs (
  id         TEXT PRIMARY KEY,
  event_type TEXT NOT NULL,  -- 'bad_key' | 'wrong_device' | 'revoked_key' | 'rate_limit' | 'admin_probe'
  ip         TEXT,
  key_hint   TEXT,
  user_id    TEXT,
  device_id  TEXT,
  detail     TEXT,
  created_at INTEGER NOT NULL
);

-- ── Simple counters for admin metrics ────────────────────────────────────────
CREATE TABLE IF NOT EXISTS counters (
  key   TEXT PRIMARY KEY,
  value INTEGER NOT NULL DEFAULT 0
);

-- ── PX site configuration (admin-managed, synced to Go clients) ───────────────
CREATE TABLE IF NOT EXISTS px_sites (
  id           TEXT PRIMARY KEY,   -- e.g. 'nike'
  name         TEXT NOT NULL,
  url          TEXT NOT NULL,
  protection   TEXT,
  body_kw      TEXT,               -- JSON array of strings
  hard_codes   TEXT,               -- JSON array of ints
  header_keys  TEXT,               -- JSON array of strings
  enabled      INTEGER NOT NULL DEFAULT 1,
  sort_order   INTEGER NOT NULL DEFAULT 0,
  updated_at   INTEGER NOT NULL
);

-- ── General app config (key/value) ────────────────────────────────────────────
-- Keys: 'px_interval_m'
CREATE TABLE IF NOT EXISTS app_config (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);

-- ── PX site seed data (all sites monitored by the Go client) ─────────────────
-- INSERT OR IGNORE so re-running the schema is safe.
INSERT OR IGNORE INTO px_sites (id,name,url,protection,body_kw,hard_codes,header_keys,enabled,sort_order,updated_at) VALUES
 ('nike',        'Nike / SNKRS',        'https://www.nike.com/launch',              'Akamai BM',   '["_abck","ak_bmsc","AkamaiGuard","bm_sz"]',   '[403,429]', '["x-akamai-request-id","x-check-cacheable"]', 1, 1,  strftime('%s','now')),
 ('adidas',      'Adidas',              'https://www.adidas.com/us',                'Akamai BM',   '["_abck","ak_bmsc"]',                         '[403,429]', '["x-akamai-request-id"]',                    1, 2,  strftime('%s','now')),
 ('supreme',     'Supreme',             'https://www.supremenewyork.com/shop/all',  'Shape/F5',    '["shape_utmb","f5-scrutinizer","_utmb"]',      '[403,429]', '[]',                                         1, 3,  strftime('%s','now')),
 ('footlocker',  'Foot Locker',         'https://www.footlocker.com/',              'PerimeterX',  '["_pxhd","_pxvid","_px2","perimeterx"]',       '[403]',     '["x-px-version"]',                           1, 4,  strftime('%s','now')),
 ('jdsports',    'JD Sports',           'https://www.jdsports.com/',                'DataDome',    '["datadome","_dd_s"]',                         '[403]',     '["x-datadome"]',                             1, 5,  strftime('%s','now')),
 ('finishline',  'Finish Line',         'https://www.finishline.com/',              'Akamai BM',   '["_abck","ak_bmsc"]',                         '[403,429]', '[]',                                         1, 6,  strftime('%s','now')),
 ('yeezysupply', 'Yeezy Supply',        'https://www.yeezysupply.com/',             'Akamai BM',   '["_abck"]',                                   '[403,429]', '[]',                                         1, 7,  strftime('%s','now')),
 ('jordan',      'Jordan Brand',        'https://www.jordan.com/',                  'Akamai BM',   '["_abck"]',                                   '[403,429]', '[]',                                         1, 8,  strftime('%s','now')),
 ('shopify',     'Shopify Checkout',    'https://www.shopify.com/checkout',         'Cloudflare',  '["cf-challenge","__cf_bm","cloudflare"]',      '[403,503]', '[]',                                         1, 9,  strftime('%s','now')),
 ('walmart',     'Walmart',             'https://www.walmart.com/',                 'Akamai BM',   '["_abck","ak_bmsc"]',                         '[403,429]', '[]',                                         1, 10, strftime('%s','now'));

-- Default app config
INSERT OR IGNORE INTO app_config (key,value,updated_at) VALUES
 ('px_interval_m', '10', strftime('%s','now')),
 ('maintenance_mode', '0', strftime('%s','now')),
 ('analytics_enabled', '1', strftime('%s','now')),
 ('cloud_sync_enabled', '1', strftime('%s','now'));

-- ── Indexes ───────────────────────────────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_keys_user        ON license_keys(user_id);
CREATE INDEX IF NOT EXISTS idx_keys_membership  ON license_keys(membership_id);
CREATE INDEX IF NOT EXISTS idx_results_user     ON synced_results(user_id, synced_at DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_user    ON user_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON user_sessions(expires_at);
CREATE INDEX IF NOT EXISTS idx_users_membership ON users(membership_id);
CREATE INDEX IF NOT EXISTS idx_logs_time        ON access_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_logs_type        ON access_logs(event_type);
CREATE INDEX IF NOT EXISTS idx_sugg_time        ON suggestions(submitted_at DESC);
