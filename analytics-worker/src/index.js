// ── Proxy Tester Analytics Worker ────────────────────────────────────────────
// Endpoints:
//   POST /ingest          — receive anonymised stats from a client run
//   GET  /stats           — public aggregate (used for in-app community comparison)
//   GET  /admin?key=KEY   — full admin dashboard (HTML, password-protected)

const CORS = {
  'Access-Control-Allow-Origin':  '*',
  'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
  'Access-Control-Allow-Headers': 'Content-Type',
};

// Simple in-memory rate-limit (resets per isolate lifetime, ~few minutes on Workers)
const RATE = new Map();
function rateLimit(ip, limit = 30, windowMs = 60_000) {
  const now = Date.now();
  const entry = RATE.get(ip) || { count: 0, start: now };
  if (now - entry.start > windowMs) { entry.count = 0; entry.start = now; }
  entry.count++;
  RATE.set(ip, entry);
  return entry.count > limit;
}

export default {
  async fetch(request, env) {
    const url    = new URL(request.url);
    const method = request.method;

    if (method === 'OPTIONS') return new Response(null, { headers: CORS });

    if (url.pathname === '/ingest' && method === 'POST') return handleIngest(request, env);
    if (url.pathname === '/stats'  && method === 'GET')  return handleStats(env);
    if (url.pathname === '/admin'  && method === 'GET')  return handleAdmin(request, env, url);

    return new Response('Not Found', { status: 404 });
  },
};

// ── POST /ingest ──────────────────────────────────────────────────────────────
async function handleIngest(request, env) {
  const ip = request.headers.get('CF-Connecting-IP') || 'unknown';
  if (rateLimit(ip)) return jsonRes({ error: 'Rate limited' }, 429);

  let body;
  try { body = await request.json(); } catch { return jsonRes({ error: 'Invalid JSON' }, 400); }

  const { client_id, proxies_tested } = body;
  if (!client_id || typeof proxies_tested !== 'number') {
    return jsonRes({ error: 'Missing client_id or proxies_tested' }, 400);
  }

  // Sanitise — strip anything that could be a proxy IP or credential
  const s = {
    client_id:           String(client_id).replace(/[^a-zA-Z0-9_-]/g, '').slice(0, 64),
    app_version:         String(body.app_version    || 'unknown').slice(0, 20),
    proxies_tested:      clamp(Math.round(body.proxies_tested || 0), 0, 500_000),
    proxies_passed:      clamp(Math.round(body.proxies_passed || 0), 0, 500_000),
    avg_ms:              body.avg_ms != null ? clamp(Math.round(body.avg_ms), 0, 99_999) : null,
    has_target:          body.has_target ? 1 : 0,
    target_pass_rate:    body.target_pass_rate != null ? clamp(parseFloat(body.target_pass_rate), 0, 100) : null,
    ip_type_residential: clamp(Math.round(body.ip_type_residential || 0), 0, 500_000),
    ip_type_mobile:      clamp(Math.round(body.ip_type_mobile      || 0), 0, 500_000),
    ip_type_datacenter:  clamp(Math.round(body.ip_type_datacenter  || 0), 0, 500_000),
    ip_type_unknown:     clamp(Math.round(body.ip_type_unknown     || 0), 0, 500_000),
    top_isps:            safePick(body.top_isps,     [], 10, sanitiseIsp),
    vendor_counts:       safePick(body.vendor_counts, {}, null, null, true),
    status_counts:       safePick(body.status_counts, {}, null, null, true),
    country_counts:      safePick(body.country_counts,{}, null, null, true),
  };

  try {
    await env.DB.prepare(`
      INSERT INTO runs (
        client_id, app_version, proxies_tested, proxies_passed, avg_ms,
        has_target, target_pass_rate,
        ip_type_residential, ip_type_mobile, ip_type_datacenter, ip_type_unknown,
        top_isps, vendor_counts, status_counts, country_counts
      ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
    `).bind(
      s.client_id, s.app_version, s.proxies_tested, s.proxies_passed, s.avg_ms,
      s.has_target, s.target_pass_rate,
      s.ip_type_residential, s.ip_type_mobile, s.ip_type_datacenter, s.ip_type_unknown,
      JSON.stringify(s.top_isps), JSON.stringify(s.vendor_counts),
      JSON.stringify(s.status_counts), JSON.stringify(s.country_counts),
    ).run();

    return jsonRes({ ok: true });
  } catch (e) {
    console.error('DB insert error:', e);
    return jsonRes({ error: 'Database error' }, 500);
  }
}

// ── GET /stats ────────────────────────────────────────────────────────────────
async function handleStats(env) {
  try {
    const stats = await aggregate(env);
    // Return public subset only — no run-level data, no client info
    return jsonRes({
      total_proxies_tested:  stats.total_proxies,
      total_runs:            stats.total_runs,
      avg_pass_rate:         stats.avg_pass_rate,
      avg_speed_ms:          stats.avg_speed_ms,
      active_clients_7d:     stats.active_7d,
      ip_type_distribution:  stats.types,
      top_isps:              stats.top_isps.slice(0, 10),
      vendor_detection:      stats.vendors,
    }, 200, { 'Cache-Control': 'public, max-age=300' }); // cache 5 min
  } catch (e) {
    return jsonRes({ error: 'Stats unavailable' }, 503);
  }
}

// ── GET /admin?key=KEY ────────────────────────────────────────────────────────
async function handleAdmin(request, env, url) {
  const key = url.searchParams.get('key') || '';
  if (!env.ADMIN_KEY || key !== env.ADMIN_KEY) {
    return new Response('Unauthorized — pass ?key=YOUR_ADMIN_KEY', { status: 401 });
  }

  try {
    const stats = await aggregate(env);

    const recent = await env.DB.prepare(`
      SELECT client_id, app_version, proxies_tested, proxies_passed,
             avg_ms, has_target, target_pass_rate, created_at
      FROM   runs
      ORDER  BY created_at DESC
      LIMIT  100
    `).all();

    const versions = await env.DB.prepare(`
      SELECT app_version, COUNT(*) as runs, COUNT(DISTINCT client_id) as clients
      FROM   runs
      GROUP  BY app_version
      ORDER  BY runs DESC
      LIMIT  15
    `).all();

    return new Response(adminHTML(stats, recent.results || [], versions.results || [], key), {
      headers: { 'Content-Type': 'text/html; charset=utf-8' },
    });
  } catch (e) {
    return new Response('Error: ' + e.message, { status: 500 });
  }
}

// ── Aggregation helper ────────────────────────────────────────────────────────
async function aggregate(env) {
  const [totals, active] = await Promise.all([
    env.DB.prepare(`
      SELECT COUNT(*)                                                     AS runs,
             SUM(proxies_tested)                                         AS proxies,
             ROUND(AVG(CAST(proxies_passed AS REAL)
                       / MAX(proxies_tested, 1) * 100), 1)              AS pass_rate,
             ROUND(AVG(avg_ms), 0)                                       AS speed,
             COUNT(DISTINCT client_id)                                   AS clients,
             SUM(ip_type_residential)                                    AS residential,
             SUM(ip_type_mobile)                                         AS mobile,
             SUM(ip_type_datacenter)                                     AS datacenter,
             SUM(ip_type_unknown)                                        AS unknown
      FROM   runs
    `).first(),
    env.DB.prepare(`
      SELECT COUNT(DISTINCT client_id) AS n
      FROM   runs
      WHERE  created_at > ?
    `).bind(Math.floor(Date.now() / 1000) - 7 * 86_400).first(),
  ]);

  // Aggregate vendor_counts across all rows
  const vendorRows = await env.DB.prepare(
    `SELECT vendor_counts FROM runs WHERE vendor_counts != '{}'`
  ).all();
  const vendors = {};
  for (const row of vendorRows.results || []) {
    try {
      for (const [k, v] of Object.entries(JSON.parse(row.vendor_counts))) {
        vendors[k] = (vendors[k] || 0) + (v || 0);
      }
    } catch {}
  }

  // Aggregate ISP histogram
  const ispRows = await env.DB.prepare(
    `SELECT top_isps FROM runs WHERE top_isps != '[]'`
  ).all();
  const ispMap = {};
  for (const row of ispRows.results || []) {
    try {
      for (const e of JSON.parse(row.top_isps)) {
        const n = e.name || e;
        if (n) ispMap[n] = (ispMap[n] || 0) + (e.count || 1);
      }
    } catch {}
  }
  const top_isps = Object.entries(ispMap)
    .sort((a, b) => b[1] - a[1])
    .slice(0, 20)
    .map(([name, count]) => ({ name, count }));

  return {
    total_runs:    totals.runs     || 0,
    total_proxies: totals.proxies  || 0,
    avg_pass_rate: totals.pass_rate,
    avg_speed_ms:  totals.speed,
    total_clients: totals.clients  || 0,
    active_7d:     active.n        || 0,
    types: {
      residential: totals.residential || 0,
      mobile:      totals.mobile      || 0,
      datacenter:  totals.datacenter  || 0,
      unknown:     totals.unknown     || 0,
    },
    top_isps,
    vendors,
  };
}

// ── Admin HTML ────────────────────────────────────────────────────────────────
function adminHTML(s, runs, versions, key) {
  const f  = n  => n != null ? Number(n).toLocaleString() : '—';
  const p  = n  => n != null ? n + '%' : '—';
  const tt = s.types.residential + s.types.mobile + s.types.datacenter + s.types.unknown || 1;

  function typeBar(label, val, col) {
    const pct = Math.round(val / tt * 100);
    return `<div style="margin:5px 0;display:flex;align-items:center;gap:8px">
      <span style="width:100px;font-size:12px">${label}</span>
      <div style="flex:1;background:#1c2333;border-radius:3px;height:10px;overflow:hidden">
        <div style="width:${pct}%;height:100%;background:${col};border-radius:3px"></div>
      </div>
      <span style="font-size:11px;color:#5d7290;width:80px">${f(val)} (${pct}%)</span>
    </div>`;
  }

  const vendorHtml = Object.entries(s.vendors).sort((a,b)=>b[1]-a[1])
    .map(([k,v]) => `<tr><td>${k}</td><td>${f(v)}</td></tr>`).join('') ||
    '<tr><td colspan="2" style="color:#5d7290">No detections yet</td></tr>';

  const ispHtml = s.top_isps.slice(0,15)
    .map(x => `<tr><td>${esc(x.name)}</td><td>${f(x.count)}</td></tr>`).join('') ||
    '<tr><td colspan="2" style="color:#5d7290">No data</td></tr>';

  const verHtml = versions
    .map(v => `<tr><td>${v.app_version}</td><td>${f(v.runs)}</td><td>${f(v.clients)}</td></tr>`).join('') ||
    '<tr><td colspan="3" style="color:#5d7290">No data</td></tr>';

  const runHtml = runs.map(r => {
    const pr = r.proxies_tested > 0 ? Math.round(r.proxies_passed / r.proxies_tested * 100) + '%' : '—';
    const dt = new Date(r.created_at * 1000).toISOString().slice(0, 16).replace('T', ' ');
    return `<tr>
      <td style="font-family:monospace;font-size:11px">${r.client_id.slice(0,10)}…</td>
      <td>${r.app_version}</td>
      <td>${f(r.proxies_tested)}</td>
      <td>${pr}</td>
      <td>${r.avg_ms != null ? r.avg_ms + 'ms' : '—'}</td>
      <td>${r.has_target ? (r.target_pass_rate != null ? Math.round(r.target_pass_rate) + '%' : '?') : '—'}</td>
      <td style="font-size:11px;color:#5d7290">${dt}</td>
    </tr>`;
  }).join('') || '<tr><td colspan="7" style="color:#5d7290">No runs yet</td></tr>';

  return `<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8">
<title>Proxy Tester Analytics</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{background:#0d1117;color:#dce8f5;font-family:'Segoe UI',system-ui,sans-serif;padding:24px;line-height:1.5}
h1{font-size:18px;font-weight:800;letter-spacing:.12em;color:#818cf8}
.sub{font-size:12px;color:#5d7290;margin:4px 0 24px}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(160px,1fr));gap:12px;margin-bottom:24px}
.kpi{background:#161b27;border:1px solid #242e42;border-radius:10px;padding:16px}
.kpi-v{font-size:28px;font-weight:900;color:#818cf8;font-family:monospace}
.kpi-l{font-size:10px;font-weight:700;letter-spacing:.1em;color:#5d7290;margin-top:4px;text-transform:uppercase}
.row{display:grid;grid-template-columns:1fr 1fr;gap:16px;margin-bottom:16px}
@media(max-width:640px){.row{grid-template-columns:1fr}}
.card{background:#161b27;border:1px solid #242e42;border-radius:10px;padding:16px}
.card-h{font-size:10px;font-weight:800;letter-spacing:.12em;color:#5d7290;text-transform:uppercase;margin-bottom:12px}
table{width:100%;border-collapse:collapse;font-size:12px}
th{text-align:left;padding:6px 8px;font-size:10px;font-weight:700;letter-spacing:.08em;color:#5d7290;border-bottom:1px solid #242e42;text-transform:uppercase}
td{padding:6px 8px;border-bottom:1px solid rgba(36,46,66,.5)}
tr:last-child td{border-bottom:none}
.wide{margin-bottom:16px}
.scroll{overflow-x:auto;max-height:360px;overflow-y:auto}
</style>
</head><body>
<h1>⬡ PROXY TESTER ANALYTICS</h1>
<div class="sub">Admin dashboard · Anonymised data only · No proxy IPs ever stored · <a href="?key=${key}" style="color:#818cf8">Refresh</a></div>

<div class="grid">
  <div class="kpi"><div class="kpi-v">${f(s.total_proxies)}</div><div class="kpi-l">Proxies Tested</div></div>
  <div class="kpi"><div class="kpi-v">${f(s.total_runs)}</div><div class="kpi-l">Total Runs</div></div>
  <div class="kpi"><div class="kpi-v">${p(s.avg_pass_rate)}</div><div class="kpi-l">Avg Pass Rate</div></div>
  <div class="kpi"><div class="kpi-v">${s.avg_speed_ms != null ? s.avg_speed_ms + 'ms' : '—'}</div><div class="kpi-l">Avg Speed</div></div>
  <div class="kpi"><div class="kpi-v">${f(s.total_clients)}</div><div class="kpi-l">All-time Clients</div></div>
  <div class="kpi"><div class="kpi-v">${f(s.active_7d)}</div><div class="kpi-l">Active (7d)</div></div>
</div>

<div class="row">
  <div class="card">
    <div class="card-h">IP Type Distribution</div>
    ${typeBar('Residential', s.types.residential, '#60a5fa')}
    ${typeBar('Mobile',      s.types.mobile,      '#c084fc')}
    ${typeBar('Datacenter',  s.types.datacenter,  '#ff5c5c')}
    ${typeBar('Unknown',     s.types.unknown,     '#5d7290')}
  </div>
  <div class="card">
    <div class="card-h">Anti-Bot Vendor Detections</div>
    <table><tbody>${vendorHtml}</tbody></table>
  </div>
</div>

<div class="row">
  <div class="card">
    <div class="card-h">Top ISPs (all time)</div>
    <div class="scroll">
      <table><thead><tr><th>ISP</th><th>Count</th></tr></thead><tbody>${ispHtml}</tbody></table>
    </div>
  </div>
  <div class="card">
    <div class="card-h">Version Distribution</div>
    <table><thead><tr><th>Version</th><th>Runs</th><th>Clients</th></tr></thead><tbody>${verHtml}</tbody></table>
  </div>
</div>

<div class="card wide">
  <div class="card-h">Recent Runs (last 100)</div>
  <div class="scroll">
    <table>
      <thead><tr><th>Client</th><th>Version</th><th>Tested</th><th>Pass Rate</th><th>Avg MS</th><th>Target</th><th>Time (UTC)</th></tr></thead>
      <tbody>${runHtml}</tbody>
    </table>
  </div>
</div>
</body></html>`;
}

// ── Helpers ───────────────────────────────────────────────────────────────────
function clamp(v, min, max) { return Math.max(min, Math.min(max, v)); }

function esc(s) {
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

// Sanitise an ISP object — only keep name (string) and count (int)
function sanitiseIsp(entry) {
  if (typeof entry === 'string') return { name: entry.slice(0, 80), count: 1 };
  if (typeof entry === 'object' && entry) {
    return { name: String(entry.name || '').slice(0, 80), count: clamp(Math.round(entry.count || 1), 0, 1e6) };
  }
  return null;
}

// Safe JSON value extraction with optional per-item transform
function safePick(val, fallback, maxItems, transform, isObj) {
  try {
    const parsed = typeof val === 'string' ? JSON.parse(val) : val;
    if (isObj && parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      // Keep only simple key->number entries, strip anything that looks like an IP
      const out = {};
      for (const [k, v] of Object.entries(parsed)) {
        if (k.length < 40 && !k.match(/\d{1,3}\.\d{1,3}/)) out[k] = clamp(Number(v) || 0, 0, 1e7);
      }
      return out;
    }
    if (Array.isArray(parsed)) {
      const items = maxItems ? parsed.slice(0, maxItems) : parsed;
      return transform ? items.map(transform).filter(Boolean) : items;
    }
  } catch {}
  return fallback;
}

function jsonRes(data, status = 200, extra = {}) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { ...CORS, 'Content-Type': 'application/json', ...extra },
  });
}
