# ProxyKit — Claude Code Context

## What This Project Is
ProxyKit is a Windows desktop app (Go + WebView2) for testing proxy lists against retail/sneaker sites, with bot-protection detection, analytics, scheduled tests, and cloud sync. It ships as a standalone `.exe` via `go-webview2` (CGO-free).

## Architecture
- **Go backend** (`localhost:3737`) — HTTP server + WebView2 window
- **Frontend** — single `index.html` served by the Go server (no build step)
- **Cloudflare Workers** + D1 (SQLite) + KV — cloud backend at `cloudflare/backend-worker/`
- **License system** — `PT-XXXXXX-XXXXXXXX-XXXXXX` keys, device-locked, validated via Cloudflare worker

## Key Files
| File | Purpose |
|---|---|
| `main.go` | Entry point, WebView2 window setup |
| `handlers.go` | All HTTP route handlers |
| `types.go` | Data types (`Job`, `Session`, `NotifyConfig`, etc.) |
| `storage.go` | JSON file persistence (`readJSONFile`/`writeJSONFile`) |
| `px_detector.go` | Bot-protection monitor (probes Nike, Adidas, etc.) |
| `cloudsync.go` | Cloud auth, sync, device transfer |
| `schedules.go` | Scheduled test runner |
| `analytics.go` | Community analytics + Discord notifications |
| `export.go` | App data export/import (ZIP bundle) |
| `index.html` | Entire frontend (~5200 lines, single file) |
| `cloudflare/backend-worker/src/index.js` | Cloudflare Worker (auth, PX config, preferences, analytics) |
| `cloudflare/backend-worker/schema.sql` | D1 schema |

## Data / State Files (stored in DataDir)
- `cloud_auth.json` — license key + token
- `notify.json` — Discord webhook config (per-type)
- `schedules.json` — scheduled test list
- `px_status.json` — last-known PX site statuses
- `ai-keys.json` — AI API keys (Claude, OpenAI, Gemini)
- `score-config.json` — proxy score weights
- `analytics.json` — community analytics opt-in
- `calibration.json` — data usage calibration
- `device_id.json` — machine UUID (never exported)

## Frontend Structure (index.html)
Pages (top nav): **TESTER** | PROVIDERS | MONITOR | SETTINGS | INTEGRATIONS

### TESTER page
- Left: proxy list input (paste/file/saved/API tabs), test config (presets: ANTIBOT/IP/SPEED/CUSTOM), schedule creation
- Right: runs list, session detail, results table, elite filter pipeline
- Bottom panel: **Scheduled Tests** list (moved here from Integrations)

### MONITOR page
- PX site status grid (card-based, colour-coded by status)
- Right: quick link to Settings → Notifications

### SETTINGS page
Left-nav categories:
- **Account** — PT- license key sign-in / sign-out, device transfer
- **Notifications** — per-type Discord webhooks (PX Changes, Job Complete, Provider Issues, System Alerts) + global fallback + ProxyKit server opt-in
- **AI** — Claude/OpenAI/Gemini API keys
- **Appearance** — light/dark mode toggle
- **Scoring** — proxy score weight sliders
- **Privacy** — community analytics opt-in
- **Data** — export/import ZIP bundle, device migration
- **Feedback** — opens feedback modal
- **Debug** — debug log viewer

### INTEGRATIONS page
- Proxy provider API integrations (add/remove)
- Provider types:
  - Full REST API: **Byteful** (dual-key: public + private, `api.byteful.com`), Webshare, Proxies.fo, IPRoyal, Bright Data
  - Dashboard URL (no public API): LemonClub, Wired, Unknown, Wolves, Wealth, Lavish, IPFist, Bart, Donut
  - Custom / Generic
- "How It Works" explains both provider types
- Discord config moved to Settings → Notifications

## NotifyConfig (notify.json)
```go
type NotifyConfig struct {
    DiscordWebhook        string // global fallback
    WebhookPXChanges      string
    WebhookJobComplete    string
    WebhookProviderIssues string
    WebhookSystemAlerts   string
    EnablePXChanges      bool // default true
    EnableJobComplete    bool // default true
    EnableProviderIssues bool // default true
    EnableSystemAlerts   bool // default true
}
```

## Auth Gate
Full-screen overlay (`z-index:9999`) blocks app until `GET /api/cloud/verify` succeeds. Offline fallback: if backend unreachable, trust local session. Sign-out re-shows gate.

Key functions: `initAuth()`, `gateSignIn()`, `_hideAuthGate()`, `_showAuthGate()`

## PX Monitor
- `px_detector.go` probes sites every 10 min (configurable via admin)
- Admin manages site list in D1; app pulls via `GET /px/config` on startup
- Status: `clean` / `soft` / `hard` / `unknown`
- PX change fires local webhook (`WebhookPXChanges`) + reports to backend
- `renderPXGrid(sites)` — card-based UI with colour stripe, protection type, detail line

## Cloud / Cloudflare
- Worker deployed at `https://proxykit-backend.trey-s.workers.dev` (set in `cloudsync.go`)
- Routes: `/auth/validate-key`, `/auth/transfer-device`, `/auth/verify`, `/auth/logout`, `/px/config`, `/px/change`, `/preferences`, `/suggestions`, `/analytics/*`
- Admin panel at `/admin?key=<admin_secret>` (KV: `ADMIN_KEY`)
- **SECURITY**: Whop API key and webhook secret were set via CLI only — never written to files

## Build
```bash
cd C:\Users\treys\Downloads\ProxyTester-Go
go build ./...   # produces ProxyTester.exe
```
No CGO, no external build tools. Frontend is embedded `index.html`.

## Deploy Worker
```bash
cd cloudflare/backend-worker
npx wrangler deploy
```

## Conventions
- `const API = window.location.origin` in frontend — works with WebView2 at localhost
- `writeJSON(w, code, v)` / `errJSON(w, code, msg)` for all API responses
- All persistent data in `DataDir` (set in `main.go`)
- JS: no frameworks, vanilla ES5-compatible, `esc(s)` for HTML escaping
- Settings pane switching: `setSettingsCat(cat)` shows/hides `.settings-pane` divs
