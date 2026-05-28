// ── ProxyKit Backend Worker ───────────────────────────────────────────────────
// Custom license key system built on Cloudflare Workers + D1 + KV.
//
// Endpoints:
//   POST /webhook                — Whop membership events
//   GET  /hub  (+ /experiences/ + /dashboard/)  — License key hub (Whop App)
//   POST /hub/verify             — Exchange Whop user token for key
//   POST /auth/validate-key      — Validate PT- key → 30-day session token
//   POST /auth/logout            — Invalidate session
//   GET  /auth/me                — Current user info
//   POST /results/sync           — Upload anonymised job results
//   GET/DELETE /results/:id      — Retrieve or delete a result
//   GET/POST /user/preferences   — Webhook opt-in/out settings
//   POST /suggestions            — Anonymous in-app feedback
//   POST /px/change              — PX status change report (fan-out webhooks)
//   GET  /dashboard              — User web dashboard (cookie-auth)
//   GET  /admin                  — Admin panel
//   POST /admin/issue-key        — Manually issue key
//   POST /admin/revoke-key       — Revoke key
//   POST /admin/clear-device     — Clear device lock for a key
//
// Required secrets (wrangler secret put):
//   WHOP_WEBHOOK_SECRET   WHOP_API_KEY   ADMIN_KEY
//   DISCORD_PX_WEBHOOK    — global ProxyKit channel webhook for PX alerts
//
// Optional vars ([vars]):
//   WHOP_PRODUCT_ID       DISCORD_INVITE_URL

const CORS = {
  'Access-Control-Allow-Origin':  '*',
  'Access-Control-Allow-Methods': 'GET, POST, PUT, DELETE, OPTIONS',
  'Access-Control-Allow-Headers': 'Content-Type, Authorization',
};

const TOKEN_TTL_SECS = 30 * 86400;

// ── Router ────────────────────────────────────────────────────────────────────

export default {
  async fetch(request, env) {
    const url    = new URL(request.url);
    const method = request.method;
    const path   = url.pathname.length > 1 ? url.pathname.replace(/\/+$/, '') : url.pathname;

    if (method === 'OPTIONS') return new Response(null, { headers: CORS });

    // Increment global request counter (best-effort, non-blocking)
    env.DB.prepare(`INSERT INTO counters (key,value) VALUES ('total_requests',1)
      ON CONFLICT(key) DO UPDATE SET value=value+1`).run().catch(()=>{});

    // ── Whop webhook ──────────────────────────────────────────────────────
    if (path === '/webhook' && method === 'POST')
      return handleWebhook(request, env);

    // ── Whop App experience views ─────────────────────────────────────────
    if ((path.startsWith('/experiences/') || path.startsWith('/dashboard/')) && method === 'GET')
      return handleHub(request, env, url);

    if (path === '/hub' && method === 'GET')
      return handleHub(request, env, url);
    if (path === '/hub/verify' && method === 'POST')
      return handleHubVerify(request, env);

    // ── Public auth ───────────────────────────────────────────────────────
    if (path === '/auth/validate-key' && method === 'POST')
      return handleValidateKey(request, env);
    if (path === '/auth/logout' && method === 'POST')
      return handleLogout(request, env);

    // ── Anonymous suggestions (no auth required) ──────────────────────────
    if (path === '/suggestions' && method === 'POST')
      return handleSuggestions(request, env);

    // ── Protected API (Bearer token) ──────────────────────────────────────
    const user = await getAuthUser(request, env);

    if (path === '/auth/me' && method === 'GET') {
      if (!user) return unauth();
      return jsonRes({ id: user.id, license_hint: user.license_hint, plan: user.plan });
    }
    if (path === '/results/sync'     && method === 'POST')   { if (!user) return unauth(); return handleSync(request, env, user); }
    if (path === '/results'          && method === 'GET')    { if (!user) return unauth(); return handleListResults(request, env, user); }
    if (path.startsWith('/results/') && method === 'GET')    { if (!user) return unauth(); return handleGetResult(path.slice(9), env, user); }
    if (path.startsWith('/results/') && method === 'DELETE') { if (!user) return unauth(); return handleDeleteResult(path.slice(9), env, user); }

    if (path === '/user/preferences' && method === 'GET')    { if (!user) return unauth(); return handleGetPrefs(env, user); }
    if (path === '/user/preferences' && method === 'POST')   { if (!user) return unauth(); return handleSetPrefs(request, env, user); }

    if (path === '/px/change'  && method === 'POST')          { if (!user) return unauth(); return handlePXChange(request, env, user); }
    if (path === '/px/config'  && method === 'GET')            return handleGetPXConfig(env);

    // ── Update manifest (requires valid session) ──────────────────────────
    if (path === '/update/latest' && method === 'GET')         { if (!user) return unauth(); return handleUpdateLatest(env); }

    // ── Self-service device transfer ──────────────────────────────────────
    if (path === '/auth/transfer-device' && method === 'POST') return handleTransferDevice(request, env);

    // ── Dashboard (cookie-auth) ────────────────────────────────────────────
    if (path === '/dashboard' || path === '/') {
      const dashUser = await getAuthUserFromCookie(request, env);
      if (!dashUser) return dashboardLogin();
      return dashboardPage(dashUser, env);
    }
    if (path === '/dashboard/validate' && method === 'POST') return handleDashboardValidate(request, env);
    if (path === '/dashboard/logout'   && method === 'POST') return handleDashboardLogout(request, env);

    // ── Admin ─────────────────────────────────────────────────────────────
    if (path === '/admin'                  && method === 'GET')  return handleAdmin(request, env, url);
    if (path === '/admin/issue-key'        && method === 'POST') return handleAdminIssueKey(request, env);
    if (path === '/admin/revoke-key'       && method === 'POST') return handleAdminRevokeKey(request, env);
    if (path === '/admin/clear-device'     && method === 'POST') return handleAdminClearDevice(request, env);
    if (path === '/admin/revoke-sessions'  && method === 'POST') return handleAdminRevokeSessions(request, env);
    if (path === '/admin/px-config'        && method === 'POST') return handleAdminPXConfig(request, env);
    if (path === '/admin/px-config/delete' && method === 'POST') return handleAdminPXConfigDelete(request, env);
    if (path === '/admin/live-stats'        && method === 'GET')  return handleAdminLiveStats(request, env);
    if (path === '/admin/publish-release'   && method === 'POST') return handleAdminPublishRelease(request, env);
    if (path === '/admin/notify-update'     && method === 'POST') return handleAdminNotifyUpdate(request, env);

    return new Response('Not Found', { status: 404 });
  },
};

// ── Key generation ────────────────────────────────────────────────────────────

function generateLicenseKey() {
  const bytes = crypto.getRandomValues(new Uint8Array(10));
  const hex   = Array.from(bytes).map(b => b.toString(16).padStart(2, '0').toUpperCase()).join('');
  return `PT-${hex.slice(0, 6)}-${hex.slice(6, 14)}-${hex.slice(14, 20)}`;
}

async function mintKey(env, userId, membershipId) {
  let key = generateLicenseKey();
  for (let i = 0; i < 5; i++) {
    const hit = await env.DB.prepare('SELECT key FROM license_keys WHERE key = ?').bind(key).first();
    if (!hit) break;
    key = generateLicenseKey();
  }
  const hint = key.slice(-4);
  const now  = Math.floor(Date.now() / 1000);
  await env.DB.prepare(`
    INSERT INTO license_keys (key, user_id, membership_id, status, created_at)
    VALUES (?, ?, ?, 'active', ?)
  `).bind(key, userId, membershipId || null, now).run();
  await env.DB.prepare('UPDATE users SET license_hint = ?, last_seen = ? WHERE id = ?').bind(hint, now, userId).run();
  return key;
}

// ── Whop webhook ──────────────────────────────────────────────────────────────

async function handleWebhook(request, env) {
  const body = await request.text();

  if (env.WHOP_WEBHOOK_SECRET) {
    const msgId        = request.headers.get('webhook-id')        || '';
    const msgTimestamp = request.headers.get('webhook-timestamp') || '';
    const msgSig       = request.headers.get('webhook-signature') || '';

    const signed = `${msgId}.${msgTimestamp}.${body}`;
    const enc    = new TextEncoder();

    const rawSecret = env.WHOP_WEBHOOK_SECRET.startsWith('whsec_')
      ? env.WHOP_WEBHOOK_SECRET.slice(6)
      : env.WHOP_WEBHOOK_SECRET.startsWith('ws_')
        ? env.WHOP_WEBHOOK_SECRET.slice(3)
        : env.WHOP_WEBHOOK_SECRET;

    let secretBytes;
    try {
      secretBytes = Uint8Array.from(atob(rawSecret), c => c.charCodeAt(0));
    } catch {
      if (/^[0-9a-f]+$/i.test(rawSecret) && rawSecret.length % 2 === 0) {
        secretBytes = new Uint8Array(rawSecret.match(/.{2}/g).map(b => parseInt(b, 16)));
      } else {
        secretBytes = enc.encode(rawSecret);
      }
    }

    const cryptoKey = await crypto.subtle.importKey(
      'raw', secretBytes, { name: 'HMAC', hash: 'SHA-256' }, false, ['sign'],
    );
    const sigBytes = await crypto.subtle.sign('HMAC', cryptoKey, enc.encode(signed));
    const computed = 'v1,' + btoa(String.fromCharCode(...new Uint8Array(sigBytes)));
    const valid    = msgSig.split(' ').some(s => s === computed);
    if (!valid) return new Response('Unauthorized', { status: 401 });
  }

  let event;
  try { event = JSON.parse(body); } catch { return jsonRes({ ok: true }); }

  const rawType    = (event.type || event.event || '').toLowerCase();
  const membership = event.data || event.membership || event;
  const now        = Math.floor(Date.now() / 1000);

  if (
    rawType === 'membership.activated'    ||
    rawType === 'membership_went_valid'   ||
    rawType === 'app_membership_went_valid'
  ) {
    const userId       = membership?.user_id || membership?.user?.id;
    const membershipId = membership?.id       || membership?.membership_id;
    const productId    = membership?.product_id;
    const planId       = membership?.plan_id;

    if (!userId) return jsonRes({ ok: true, skipped: 'no user_id' });
    if (env.WHOP_PRODUCT_ID && productId && productId !== env.WHOP_PRODUCT_ID)
      return jsonRes({ ok: true, skipped: 'product_id mismatch' });

    await env.DB.prepare(`
      INSERT INTO users (id, membership_id, plan, whop_product_id, whop_plan_id, created_at, last_seen)
      VALUES (?, ?, 'pro', ?, ?, ?, ?)
      ON CONFLICT(id) DO UPDATE SET
        membership_id=excluded.membership_id, whop_product_id=excluded.whop_product_id,
        whop_plan_id=excluded.whop_plan_id, last_seen=excluded.last_seen
    `).bind(userId, membershipId||null, productId||null, planId||null, now, now).run();

    const existing = await env.DB.prepare(
      `SELECT key FROM license_keys WHERE user_id = ? AND status = 'active' LIMIT 1`
    ).bind(userId).first();

    let licenseKey = existing?.key;
    if (!licenseKey) {
      licenseKey = await mintKey(env, userId, membershipId);
      if (env.WHOP_API_KEY && membershipId) {
        try {
          await fetch(`https://api.whop.com/api/v1/memberships/${membershipId}`, {
            method: 'PATCH',
            headers: { 'Authorization': `Bearer ${env.WHOP_API_KEY}`, 'Content-Type': 'application/json' },
            body: JSON.stringify({ metadata: { proxykit_license_key: licenseKey } }),
          });
        } catch { /* non-fatal */ }
      }
    }
    return jsonRes({ ok: true, key_issued: !existing, user_id: userId });
  }

  if (
    rawType === 'membership.deactivated'   ||
    rawType === 'membership_went_invalid'  ||
    rawType === 'app_membership_went_invalid'
  ) {
    const membershipId = membership?.id || membership?.membership_id;
    const userId       = membership?.user_id || membership?.user?.id;
    if (membershipId) {
      await env.DB.prepare(
        `UPDATE license_keys SET status='revoked', revoked_at=? WHERE membership_id=?`
      ).bind(now, membershipId).run();
    } else if (userId) {
      await env.DB.prepare(
        `UPDATE license_keys SET status='revoked', revoked_at=? WHERE user_id=? AND status='active'`
      ).bind(now, userId).run();
    }
    return jsonRes({ ok: true, revoked: membershipId || userId || 'none' });
  }

  return jsonRes({ ok: true, unhandled: rawType });
}

// ── Hub page ──────────────────────────────────────────────────────────────────

async function handleHub(request, env, url) {
  const serverToken = request.headers.get('x-whop-user-token') || '';
  const queryToken  = url.searchParams.get('token') || '';
  const bearerToken = serverToken || queryToken;

  let userId  = null;
  let keyData = null;

  if (bearerToken && env.WHOP_API_KEY) {
    userId = await resolveWhopUserId(bearerToken, env);
  }

  if (userId) {
    keyData = await env.DB.prepare(
      `SELECT key, status, created_at FROM license_keys WHERE user_id = ? AND status = 'active' LIMIT 1`
    ).bind(userId).first() || null;

    if (!keyData) {
      const now = Math.floor(Date.now() / 1000);
      await env.DB.prepare(`
        INSERT INTO users (id, plan, created_at, last_seen) VALUES (?, 'pro', ?, ?)
        ON CONFLICT(id) DO UPDATE SET last_seen=excluded.last_seen
      `).bind(userId, now, now).run();
      const newKey = await mintKey(env, userId, null);
      keyData = { key: newKey, created_at: now };
    } else {
      const now = Math.floor(Date.now() / 1000);
      await env.DB.prepare('UPDATE users SET last_seen=? WHERE id=?').bind(now, userId).run();
    }
  }

  const discordInvite = env.DISCORD_INVITE_URL || '';
  return new Response(hubHTML(keyData, !!bearerToken, discordInvite), {
    headers: { 'Content-Type': 'text/html; charset=utf-8' },
  });
}

async function handleHubVerify(request, env) {
  let body;
  try { body = await request.json(); } catch { return jsonRes({ error: 'Invalid JSON' }, 400); }
  const token = (body.token || '').trim();
  if (!token) return jsonRes({ error: 'token required' }, 400);

  const userId = await resolveWhopUserId(token, env);
  if (!userId) return jsonRes({ error: 'Could not verify identity with Whop' }, 401);

  const row = await env.DB.prepare(
    `SELECT key, status, created_at FROM license_keys WHERE user_id = ? AND status = 'active' LIMIT 1`
  ).bind(userId).first();
  if (!row) return jsonRes({ error: 'no_key', message: 'No active key — purchase may still be processing.' }, 404);
  return jsonRes({ key: row.key, created_at: row.created_at });
}

async function resolveWhopUserId(token, env) {
  try {
    const r = await fetch('https://api.whop.com/api/v2/me', { headers: { Authorization: `Bearer ${token}` } });
    if (r.ok) { const me = await r.json(); const id = me?.id || me?.user?.id; if (id) return id; }
  } catch { /* fall through */ }
  try {
    const r = await fetch('https://api.whop.com/v5/me', { headers: { Authorization: `Bearer ${token}` } });
    if (r.ok) { const me = await r.json(); const id = me?.id || me?.user_id; if (id) return id; }
  } catch { /* fall through */ }
  return null;
}

// ── Rate-limit helper (KV-backed, per-IP sliding window) ─────────────────────

async function checkRateLimit(env, identifier, limit, windowSec) {
  if (!env.KV) return false;
  const bucket = Math.floor(Date.now() / 1000 / windowSec);
  const rlKey  = `rl:${identifier}:${bucket}`;
  const cur    = parseInt(await env.KV.get(rlKey).catch(() => '0') || '0');
  if (cur >= limit) return true; // over limit
  await env.KV.put(rlKey, String(cur + 1), { expirationTtl: windowSec * 2 });
  return false;
}

// ── POST /auth/validate-key ───────────────────────────────────────────────────
// Validates a PT- license key.  On first use, binds the key to the device_id
// supplied by the Go app.  Subsequent mismatches are rejected + logged.

async function handleValidateKey(request, env) {
  let body;
  try { body = await request.json(); } catch { return jsonRes({ error: 'Invalid JSON' }, 400); }

  const key      = (body.license_key || '').trim().toUpperCase().slice(0, 64);
  const deviceId = (body.device_id   || '').trim().slice(0, 64);
  const ip       = request.headers.get('CF-Connecting-IP') || 'unknown';

  // Rate-limit: 10 key validation attempts per IP per minute
  if (await checkRateLimit(env, `validate:${ip}`, 10, 60)) {
    await logAccess(env, 'rate_limit', ip, key.slice(-4) || '????', null, deviceId, 'validate-key rate limit');
    return jsonRes({ error: 'Too many requests. Please wait a moment and try again.' }, 429);
  }

  if (!key.startsWith('PT-')) {
    await logAccess(env, 'bad_key', ip, key.slice(-4) || '????', null, deviceId, 'bad key format');
    return jsonRes({ error: 'Invalid license key format' }, 400);
  }

  const row = await env.DB.prepare(`
    SELECT lk.key, lk.user_id, lk.membership_id, lk.status, lk.device_id,
           u.license_hint, u.plan, u.membership_id AS user_membership_id
    FROM license_keys lk
    JOIN users u ON u.id = lk.user_id
    WHERE lk.key = ?
  `).bind(key).first();

  if (!row) {
    await logAccess(env, 'bad_key', ip, key.slice(-4), null, deviceId, 'key not found');
    return jsonRes({ error: 'License key not found' }, 401);
  }
  if (row.status !== 'active') {
    await logAccess(env, 'revoked_key', ip, key.slice(-4), row.user_id, deviceId, `key is ${row.status}`);
    return jsonRes({ error: `License key is ${row.status}` }, 401);
  }

  // Device locking
  if (deviceId) {
    if (!row.device_id) {
      // First activation — bind to this device
      await env.DB.prepare('UPDATE license_keys SET device_id=? WHERE key=?').bind(deviceId, key).run();
    } else if (row.device_id !== deviceId) {
      // Different device — reject and log
      await logAccess(env, 'wrong_device', ip, key.slice(-4), row.user_id, deviceId,
        `expected ${row.device_id.slice(0,8)}… got ${deviceId.slice(0,8)}…`);
      return jsonRes({ error: 'This key is already activated on another device. Contact support to transfer.' }, 401);
    }
  }

  const now  = Math.floor(Date.now() / 1000);
  const user = {
    id:            row.user_id,
    license_hint:  row.license_hint,
    plan:          row.plan || 'pro',
    membership_id: row.membership_id || row.user_membership_id,
  };
  const token = await issueToken(env, user);
  await env.DB.prepare('UPDATE users SET last_seen=? WHERE id=?').bind(now, row.user_id).run();
  await env.DB.prepare(`INSERT INTO counters (key,value) VALUES ('total_validations',1)
    ON CONFLICT(key) DO UPDATE SET value=value+1`).run();

  return jsonRes({ ok: true, token, user });
}

// ── User preferences ──────────────────────────────────────────────────────────

async function handleGetPrefs(env, user) {
  const row = await env.DB.prepare('SELECT * FROM user_preferences WHERE user_id=?').bind(user.id).first();
  if (!row) return jsonRes({ discord_webhook_url:'', global_discord_opt:false, notify_px_changes:true, notify_provider_issues:true });
  return jsonRes({
    discord_webhook_url:    row.discord_webhook_url   || '',
    global_discord_opt:     !!row.global_discord_opt,
    notify_px_changes:      row.notify_px_changes !== 0,
    notify_provider_issues: row.notify_provider_issues !== 0,
  });
}

async function handleSetPrefs(request, env, user) {
  let body;
  try { body = await request.json(); } catch { return jsonRes({ error: 'Invalid JSON' }, 400); }

  // Validate Discord webhook URL — must be discord.com or empty
  let webhookUrl = (body.discord_webhook_url || '').trim().slice(0, 512);
  if (webhookUrl && !/^https:\/\/(discord\.com|discordapp\.com)\/api\/webhooks\/\d+\/[\w-]+$/.test(webhookUrl)) {
    return jsonRes({ error: 'Invalid Discord webhook URL' }, 400);
  }
  const globalOpt   = body.global_discord_opt   ? 1 : 0;
  const notifyPX    = body.notify_px_changes      !== false ? 1 : 0;
  const notifyProv  = body.notify_provider_issues !== false ? 1 : 0;
  const now         = Math.floor(Date.now() / 1000);

  await env.DB.prepare(`
    INSERT INTO user_preferences (user_id, discord_webhook_url, global_discord_opt, notify_px_changes, notify_provider_issues, updated_at)
    VALUES (?, ?, ?, ?, ?, ?)
    ON CONFLICT(user_id) DO UPDATE SET
      discord_webhook_url=excluded.discord_webhook_url,
      global_discord_opt=excluded.global_discord_opt,
      notify_px_changes=excluded.notify_px_changes,
      notify_provider_issues=excluded.notify_provider_issues,
      updated_at=excluded.updated_at
  `).bind(user.id, webhookUrl||null, globalOpt, notifyPX, notifyProv, now).run();

  return jsonRes({ ok: true });
}

// ── PX change reporting + webhook fan-out ─────────────────────────────────────

async function handlePXChange(request, env, user) {
  let body;
  try { body = await request.json(); } catch { return jsonRes({ error: 'Invalid JSON' }, 400); }

  const { site_id, site_name, protection, old_status, new_status, detail, changed_at } = body;
  if (!site_id || !new_status) return jsonRes({ error: 'site_id and new_status required' }, 400);

  await env.DB.prepare(`INSERT INTO counters (key,value) VALUES ('px_changes',1)
    ON CONFLICT(key) DO UPDATE SET value=value+1`).run();

  const statusEmoji = { clean:'✅', soft:'⚠️', hard:'🚫', unknown:'❓' };
  const emoji       = statusEmoji[new_status] || '🔔';
  const msg = `${emoji} **${esc(site_name||site_id)}** protection status changed\n` +
              `**${(old_status||'?').toUpperCase()} → ${new_status.toUpperCase()}** (${esc(protection||'')})\n` +
              (detail ? `*${esc(detail)}*` : '');

  const embed = {
    title:       `${emoji} ${site_name || site_id} — ${new_status.toUpperCase()}`,
    description: `**${(old_status||'?').toUpperCase()} → ${new_status.toUpperCase()}**\n${protection || ''}${detail ? `\n*${detail}*` : ''}`,
    color:       new_status==='hard' ? 0xff5c5c : new_status==='soft' ? 0xff9f43 : 0x4ade80,
    footer:      { text: 'ProxyKit PX Monitor' },
    timestamp:   new Date((changed_at||Date.now()/1000)*1000).toISOString(),
  };

  // 1. Global ProxyKit Discord channel
  if (env.DISCORD_PX_WEBHOOK) {
    fetch(env.DISCORD_PX_WEBHOOK, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ content: msg, embeds: [embed] }),
    }).catch(()=>{});
  }

  // 2. Personal webhooks for users who opted in
  try {
    const prefs = await env.DB.prepare(`
      SELECT discord_webhook_url FROM user_preferences
      WHERE notify_px_changes=1 AND discord_webhook_url IS NOT NULL AND discord_webhook_url != ''
    `).all();
    for (const pref of (prefs.results||[])) {
      fetch(pref.discord_webhook_url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ embeds: [embed] }),
      }).catch(()=>{});
    }
  } catch { /* non-fatal */ }

  return jsonRes({ ok: true });
}

// ── Anonymous suggestions ─────────────────────────────────────────────────────

async function handleSuggestions(request, env) {
  let body;
  try { body = await request.json(); } catch { return jsonRes({ error: 'Invalid JSON' }, 400); }

  const text     = (body.body || '').trim().slice(0, 2000);
  const category = ['feature','bug','general'].includes(body.category) ? body.category : 'general';

  if (!text) return jsonRes({ error: 'body required' }, 400);

  const id  = randomHex(8);
  const now = Math.floor(Date.now() / 1000);
  await env.DB.prepare('INSERT INTO suggestions (id,body,category,submitted_at) VALUES (?,?,?,?)')
    .bind(id, text, category, now).run();
  await env.DB.prepare(`INSERT INTO counters (key,value) VALUES ('total_suggestions',1)
    ON CONFLICT(key) DO UPDATE SET value=value+1`).run();

  return jsonRes({ ok: true, id });
}

// ── Session helpers ───────────────────────────────────────────────────────────

function randomHex(bytes = 32) {
  const arr = crypto.getRandomValues(new Uint8Array(bytes));
  return Array.from(arr).map(b => b.toString(16).padStart(2, '0')).join('');
}

async function issueToken(env, user) {
  const token    = randomHex(32);
  const now      = Math.floor(Date.now() / 1000);
  const expiresAt = now + TOKEN_TTL_SECS;
  await env.SESSIONS.put('token:' + token, JSON.stringify(user), { expirationTtl: TOKEN_TTL_SECS });
  // Track in D1 so admin can enumerate and revoke sessions per-user
  env.DB.prepare(`
    INSERT INTO user_sessions (token, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)
    ON CONFLICT(token) DO NOTHING
  `).bind(token, user.id, now, expiresAt).run().catch(()=>{});
  return token;
}

async function getAuthUser(request, env) {
  const auth  = request.headers.get('Authorization') || '';
  const token = auth.startsWith('Bearer ') ? auth.slice(7).trim() : null;
  if (!token) return null;
  const raw = await env.SESSIONS.get('token:' + token);
  return raw ? JSON.parse(raw) : null;
}

async function getAuthUserFromCookie(request, env) {
  const cookie = request.headers.get('Cookie') || '';
  const m      = cookie.match(/pt_session=([a-f0-9]+)/);
  if (!m) return null;
  const raw = await env.SESSIONS.get('token:' + m[1]);
  return raw ? JSON.parse(raw) : null;
}

function unauth() { return jsonRes({ error: 'Unauthorized' }, 401); }

// ── POST /auth/logout ─────────────────────────────────────────────────────────

async function handleLogout(request, env) {
  const auth  = request.headers.get('Authorization') || '';
  const token = auth.startsWith('Bearer ') ? auth.slice(7).trim() : null;
  if (token) await env.SESSIONS.delete('token:' + token);
  return jsonRes({ ok: true });
}

// ── Results sync / list / get / delete ───────────────────────────────────────

async function handleSync(request, env, user) {
  let body;
  try { body = await request.json(); } catch { return jsonRes({ error: 'Invalid JSON' }, 400); }

  const { job_id, list_name, total, passed, failed, avg_ms, ip_analysis, data_usage, top_proxies } = body;
  if (!job_id) return jsonRes({ error: 'job_id required' }, 400);

  const safeProxies = (top_proxies || []).slice(0, 100).map(p => ({
    egress_ip: p.egress_ip || null, ip_type: p.ip_type || 'unknown',
    avg_ms: p.avg_ms || null, score: p.score || null, isp: p.isp||null, country: p.country||null,
  }));

  const now      = Math.floor(Date.now() / 1000);
  const passRate = total > 0 ? Math.round(passed / total * 1000) / 10 : null;

  await env.DB.prepare(`
    INSERT INTO synced_results
      (id,user_id,list_name,total,passed,failed,pass_rate,avg_ms,ip_analysis,data_usage,top_proxies,synced_at)
    VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
    ON CONFLICT(id) DO UPDATE SET
      list_name=excluded.list_name, total=excluded.total, passed=excluded.passed,
      failed=excluded.failed, pass_rate=excluded.pass_rate, avg_ms=excluded.avg_ms,
      ip_analysis=excluded.ip_analysis, data_usage=excluded.data_usage,
      top_proxies=excluded.top_proxies, synced_at=excluded.synced_at
  `).bind(
    job_id, user.id, String(list_name||'Unknown').slice(0,200),
    clamp(Math.round(total||0),0,5_000_000), clamp(Math.round(passed||0),0,5_000_000),
    clamp(Math.round(failed||0),0,5_000_000), passRate,
    avg_ms ? clamp(Math.round(avg_ms),0,99_999) : null,
    JSON.stringify(ip_analysis||{}), JSON.stringify(data_usage||{}), JSON.stringify(safeProxies), now,
  ).run();

  return jsonRes({ ok: true, synced_at: now });
}

async function handleListResults(request, env, user) {
  const url    = new URL(request.url);
  const limit  = Math.min(parseInt(url.searchParams.get('limit')||'50'), 200);
  const offset = parseInt(url.searchParams.get('offset')||'0');
  const rows   = await env.DB.prepare(`
    SELECT id,list_name,total,passed,failed,pass_rate,avg_ms,synced_at
    FROM synced_results WHERE user_id=? ORDER BY synced_at DESC LIMIT ? OFFSET ?
  `).bind(user.id, limit, offset).all();
  const count  = await env.DB.prepare('SELECT COUNT(*) as n FROM synced_results WHERE user_id=?').bind(user.id).first();
  return jsonRes({ total: count.n, offset, limit, results: rows.results||[] });
}

async function handleGetResult(id, env, user) {
  const row = await env.DB.prepare('SELECT * FROM synced_results WHERE id=? AND user_id=?').bind(id, user.id).first();
  if (!row) return jsonRes({ error: 'Not found' }, 404);
  return jsonRes({ ...row, ip_analysis: safeParseJSON(row.ip_analysis), data_usage: safeParseJSON(row.data_usage), top_proxies: safeParseJSON(row.top_proxies) });
}

async function handleDeleteResult(id, env, user) {
  const row = await env.DB.prepare('SELECT id FROM synced_results WHERE id=? AND user_id=?').bind(id, user.id).first();
  if (!row) return jsonRes({ error: 'Not found' }, 404);
  await env.DB.prepare('DELETE FROM synced_results WHERE id=?').bind(id).run();
  return jsonRes({ ok: true, deleted: id });
}

// ── Self-service device transfer ──────────────────────────────────────────────
// Allows a user to move their key to a new device without admin help.
// They provide their PT- key; we clear the old device_id, bind the new one, issue a token.
// The event is logged for admin review.

async function handleTransferDevice(request, env) {
  let body;
  try { body = await request.json(); } catch { return jsonRes({ error: 'Invalid JSON' }, 400); }

  const key      = (body.license_key || '').trim().toUpperCase();
  const deviceId = (body.device_id   || '').trim().slice(0, 64);
  const ip       = request.headers.get('CF-Connecting-IP') || '';

  if (!key.startsWith('PT-'))  return jsonRes({ error: 'Invalid license key format' }, 400);
  if (!deviceId)                return jsonRes({ error: 'device_id required' }, 400);

  const row = await env.DB.prepare(`
    SELECT lk.key, lk.user_id, lk.status, lk.device_id,
           u.license_hint, u.plan, u.membership_id
    FROM license_keys lk JOIN users u ON u.id = lk.user_id WHERE lk.key = ?
  `).bind(key).first();

  if (!row)                    return jsonRes({ error: 'License key not found' }, 401);
  if (row.status !== 'active') return jsonRes({ error: `Key is ${row.status}` }, 401);

  const oldDevice = row.device_id || '(unbound)';
  await env.DB.prepare('UPDATE license_keys SET device_id=? WHERE key=?').bind(deviceId, key).run();
  await logAccess(env, 'device_transfer', ip, key.slice(-4), row.user_id, deviceId,
    `transferred from ${oldDevice.slice(0,12)}… to ${deviceId.slice(0,12)}…`);

  const now  = Math.floor(Date.now() / 1000);
  const user = { id: row.user_id, license_hint: row.license_hint, plan: row.plan || 'pro', membership_id: row.membership_id };
  const token = await issueToken(env, user);
  await env.DB.prepare('UPDATE users SET last_seen=? WHERE id=?').bind(now, row.user_id).run();

  return jsonRes({ ok: true, token, user, transferred_from: oldDevice.slice(0,12)+'…' });
}

// ── PX site config (admin-managed, public read) ───────────────────────────────

// Default PX site seed — inserted on first call if the table is empty.
const PX_SITE_SEED = [
  {id:'nike',        name:'Nike / SNKRS',        url:'https://www.nike.com/launch',             protection:'Akamai BM',  body_kw:['_abck','ak_bmsc','AkamaiGuard','bm_sz'],    hard_codes:[403,429], header_keys:['x-akamai-request-id','x-check-cacheable'], sort_order:1},
  {id:'adidas',      name:'Adidas',               url:'https://www.adidas.com/us',               protection:'Akamai BM',  body_kw:['_abck','ak_bmsc'],                          hard_codes:[403,429], header_keys:['x-akamai-request-id'],                     sort_order:2},
  {id:'supreme',     name:'Supreme',              url:'https://www.supremenewyork.com/shop/all', protection:'Shape/F5',   body_kw:['shape_utmb','f5-scrutinizer','_utmb'],      hard_codes:[403,429], header_keys:[],                                          sort_order:3},
  {id:'footlocker',  name:'Foot Locker',          url:'https://www.footlocker.com/',             protection:'PerimeterX', body_kw:['_pxhd','_pxvid','_px2','perimeterx'],      hard_codes:[403],     header_keys:['x-px-version'],                            sort_order:4},
  {id:'jdsports',    name:'JD Sports',            url:'https://www.jdsports.com/',               protection:'DataDome',   body_kw:['datadome','_dd_s'],                         hard_codes:[403],     header_keys:['x-datadome'],                              sort_order:5},
  {id:'finishline',  name:'Finish Line',          url:'https://www.finishline.com/',             protection:'Akamai BM',  body_kw:['_abck','ak_bmsc'],                          hard_codes:[403,429], header_keys:[],                                          sort_order:6},
  {id:'yeezysupply', name:'Yeezy Supply',         url:'https://www.yeezysupply.com/',            protection:'Akamai BM',  body_kw:['_abck'],                                   hard_codes:[403,429], header_keys:[],                                          sort_order:7},
  {id:'jordan',      name:'Jordan Brand',         url:'https://www.jordan.com/',                 protection:'Akamai BM',  body_kw:['_abck'],                                   hard_codes:[403,429], header_keys:[],                                          sort_order:8},
  {id:'shopify',     name:'Shopify Checkout',     url:'https://www.shopify.com/checkout',        protection:'Cloudflare', body_kw:['cf-challenge','__cf_bm','cloudflare'],      hard_codes:[403,503], header_keys:[],                                          sort_order:9},
  {id:'walmart',     name:'Walmart',              url:'https://www.walmart.com/',                protection:'Akamai BM',  body_kw:['_abck','ak_bmsc'],                          hard_codes:[403,429], header_keys:[],                                          sort_order:10},
];

async function seedPXSitesIfEmpty(env) {
  const count = await env.DB.prepare('SELECT COUNT(*) as n FROM px_sites').first();
  if ((count?.n || 0) > 0) return;
  const now = Math.floor(Date.now() / 1000);
  await Promise.all(PX_SITE_SEED.map(s =>
    env.DB.prepare(`INSERT OR IGNORE INTO px_sites
      (id,name,url,protection,body_kw,hard_codes,header_keys,enabled,sort_order,updated_at)
      VALUES (?,?,?,?,?,?,?,1,?,?)`)
    .bind(s.id, s.name, s.url, s.protection,
      JSON.stringify(s.body_kw), JSON.stringify(s.hard_codes), JSON.stringify(s.header_keys),
      s.sort_order, now).run()
  ));
}

async function handleGetPXConfig(env) {
  // Seed all default sites if the table is empty (e.g. fresh deployment)
  await seedPXSitesIfEmpty(env).catch(() => {});

  const [sitesResult, intervalRow] = await Promise.all([
    env.DB.prepare('SELECT * FROM px_sites WHERE enabled=1 ORDER BY sort_order ASC, name ASC').all(),
    env.DB.prepare("SELECT value FROM app_config WHERE key='px_interval_m'").first(),
  ]);
  const sites = (sitesResult.results || []).map(s => ({
    id:           s.id,
    name:         s.name,
    url:          s.url,
    protection:   s.protection || '',
    body_kw:      safeParseJSON(s.body_kw),
    hard_codes:   safeParseJSON(s.hard_codes),
    header_keys:  safeParseJSON(s.header_keys),
    enabled:      true,  // query is already WHERE enabled=1; Go client skips falsy entries
  }));
  return jsonRes({ sites, interval_m: parseInt(intervalRow?.value || '10') });
}

async function handleAdminPXConfig(request, env) {
  if (!checkAdminKey(request, env)) return new Response('Unauthorized', { status: 401 });
  let body;
  try { body = await request.json(); } catch { return jsonRes({ error: 'Invalid JSON' }, 400); }

  const now = Math.floor(Date.now() / 1000);

  // Update global interval
  if (body.interval_m != null) {
    const v = Math.max(1, Math.min(120, parseInt(body.interval_m) || 10));
    await env.DB.prepare(`INSERT INTO app_config (key,value,updated_at) VALUES ('px_interval_m',?,?)
      ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`)
      .bind(String(v), now).run();
  }

  // Upsert a site
  if (body.site) {
    const s = body.site;
    const id = (s.id || '').trim().toLowerCase().replace(/\W/g, '_');
    if (!id || !s.name || !s.url) return jsonRes({ error: 'site.id, site.name, site.url required' }, 400);
    await env.DB.prepare(`
      INSERT INTO px_sites (id,name,url,protection,body_kw,hard_codes,header_keys,enabled,sort_order,updated_at)
      VALUES (?,?,?,?,?,?,?,?,?,?)
      ON CONFLICT(id) DO UPDATE SET
        name=excluded.name, url=excluded.url, protection=excluded.protection,
        body_kw=excluded.body_kw, hard_codes=excluded.hard_codes, header_keys=excluded.header_keys,
        enabled=excluded.enabled, sort_order=excluded.sort_order, updated_at=excluded.updated_at
    `).bind(
      id, s.name, s.url, s.protection||null,
      JSON.stringify(s.body_kw||[]),
      JSON.stringify(s.hard_codes||[]),
      JSON.stringify(s.header_keys||[]),
      s.enabled !== false ? 1 : 0,
      s.sort_order || 0, now,
    ).run();
  }

  // Toggle enabled flag
  if (body.toggle_id) {
    const cur = await env.DB.prepare('SELECT enabled FROM px_sites WHERE id=?').bind(body.toggle_id).first();
    if (cur) await env.DB.prepare('UPDATE px_sites SET enabled=?,updated_at=? WHERE id=?')
      .bind(cur.enabled ? 0 : 1, now, body.toggle_id).run();
  }

  // Set a generic app_config key (for feature toggles beyond px_interval_m)
  if (body.config_key != null && body.config_value != null) {
    const safeKey = String(body.config_key || '').trim().slice(0, 64);
    const safeVal = String(body.config_value ?? '').trim().slice(0, 256);
    if (safeKey) {
      await env.DB.prepare(`INSERT INTO app_config (key,value,updated_at) VALUES (?,?,?)
        ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`)
        .bind(safeKey, safeVal, now).run();
    }
  }

  return jsonRes({ ok: true });
}

async function handleAdminPXConfigDelete(request, env) {
  if (!checkAdminKey(request, env)) return new Response('Unauthorized', { status: 401 });
  let body;
  try { body = await request.json(); } catch { return jsonRes({ error: 'Invalid JSON' }, 400); }
  if (!body.id) return jsonRes({ error: 'id required' }, 400);
  await env.DB.prepare('DELETE FROM px_sites WHERE id=?').bind(body.id).run();
  return jsonRes({ ok: true });
}

// ── Admin: revoke all sessions for a user ────────────────────────────────────

async function handleAdminRevokeSessions(request, env) {
  if (!checkAdminKey(request, env)) return new Response('Unauthorized', { status: 401 });
  let body;
  try { body = await request.json(); } catch { return jsonRes({ error: 'Invalid JSON' }, 400); }
  const userId = (body.user_id || '').trim();
  if (!userId) return jsonRes({ error: 'user_id required' }, 400);

  // Fetch all active session tokens from D1
  const rows = await env.DB.prepare(
    'SELECT token FROM user_sessions WHERE user_id=?'
  ).bind(userId).all();

  let deleted = 0;
  for (const row of (rows.results || [])) {
    await env.SESSIONS.delete('token:' + row.token).catch(()=>{});
    deleted++;
  }
  // Remove from D1 too
  await env.DB.prepare('DELETE FROM user_sessions WHERE user_id=?').bind(userId).run();

  await logAccess(env, 'sessions_revoked', null, null, userId, null, `admin revoked ${deleted} sessions`);
  return jsonRes({ ok: true, sessions_revoked: deleted });
}

// ── Access logging ────────────────────────────────────────────────────────────

async function logAccess(env, eventType, ip, keyHint, userId, deviceId, detail) {
  const id  = randomHex(8);
  const now = Math.floor(Date.now() / 1000);
  await env.DB.prepare(`
    INSERT INTO access_logs (id,event_type,ip,key_hint,user_id,device_id,detail,created_at)
    VALUES (?,?,?,?,?,?,?,?)
  `).bind(id, eventType, ip||null, keyHint||null, userId||null, deviceId||null, detail||null, now).run().catch(()=>{});
}

// ── Admin ─────────────────────────────────────────────────────────────────────

async function handleAdmin(request, env, url) {
  if (!checkAdminKey(request, env)) {
    await logAccess(env, 'admin_probe',
      request.headers.get('CF-Connecting-IP')||'', null, null, null, 'unauthorized admin access');
    return new Response('Unauthorized', { status: 401 });
  }

  const thirtyDaysAgo = Math.floor(Date.now()/1000) - 30*86400;

  const [
    userCount, keyCount, resultCount, totalProxies, avgPassRate,
    countersRows, suggCount, logCount, perUser, recentLogs, recentSuggs, pxSitesRows, intervalRow,
    dailyJobsRows, appConfigRows,
  ] = await Promise.all([
    env.DB.prepare('SELECT COUNT(*) as n FROM users').first(),
    env.DB.prepare(`SELECT COUNT(*) as n FROM license_keys WHERE status='active'`).first(),
    env.DB.prepare('SELECT COUNT(*) as n FROM synced_results').first(),
    env.DB.prepare('SELECT SUM(total) as n FROM synced_results').first(),
    env.DB.prepare('SELECT ROUND(AVG(pass_rate),1) as n FROM synced_results WHERE pass_rate IS NOT NULL').first(),
    env.DB.prepare('SELECT key,value FROM counters').all(),
    env.DB.prepare('SELECT COUNT(*) as n FROM suggestions').first(),
    env.DB.prepare('SELECT COUNT(*) as n FROM access_logs').first(),
    env.DB.prepare(`
      SELECT u.id, u.membership_id, u.license_hint, u.plan, u.created_at, u.last_seen,
        lk.key, lk.status as key_status, lk.device_id,
        COUNT(r.id) AS jobs_synced, SUM(r.total) AS proxies_tested,
        ROUND(AVG(r.pass_rate),1) AS avg_pass_rate, ROUND(AVG(r.avg_ms)) AS avg_ms,
        MAX(r.synced_at) AS last_job_at
      FROM users u
      LEFT JOIN license_keys lk ON lk.user_id=u.id AND lk.status='active'
      LEFT JOIN synced_results r ON r.user_id=u.id
      GROUP BY u.id ORDER BY last_job_at DESC NULLS LAST LIMIT 100
    `).all(),
    env.DB.prepare('SELECT * FROM access_logs ORDER BY created_at DESC LIMIT 100').all(),
    env.DB.prepare('SELECT * FROM suggestions ORDER BY submitted_at DESC LIMIT 50').all(),
    env.DB.prepare('SELECT * FROM px_sites ORDER BY sort_order ASC, name ASC').all(),
    env.DB.prepare("SELECT value FROM app_config WHERE key='px_interval_m'").first(),
    env.DB.prepare(`
      SELECT date(synced_at,'unixepoch') as day,
             COUNT(*) as jobs, COALESCE(SUM(total),0) as proxies
      FROM synced_results WHERE synced_at > ? GROUP BY day ORDER BY day
    `).bind(thirtyDaysAgo).all(),
    env.DB.prepare('SELECT key, value FROM app_config').all(),
  ]);

  const counters = {};
  for (const row of (countersRows.results||[])) counters[row.key] = row.value;

  const appConfig = {};
  for (const row of (appConfigRows.results||[])) appConfig[row.key] = row.value;

  const adminKey = url.searchParams.get('key') || '';
  return new Response(adminHTML({
    users:             userCount.n  || 0,
    active_keys:       keyCount.n   || 0,
    results:           resultCount.n || 0,
    total_proxies:     totalProxies.n || 0,
    avg_pass_rate:     avgPassRate.n  || 0,
    suggestions:       suggCount.n   || 0,
    log_count:         logCount.n    || 0,
    total_requests:    counters['total_requests']    || 0,
    total_validations: counters['total_validations'] || 0,
    px_changes:        counters['px_changes']        || 0,
    per_user:          perUser.results || [],
    recent_logs:       recentLogs.results || [],
    recent_suggs:      recentSuggs.results || [],
    px_sites:          pxSitesRows.results || [],
    px_interval_m:     parseInt(intervalRow?.value || '10'),
    daily_jobs:        dailyJobsRows.results || [],
    app_config:        appConfig,
  }, adminKey), { headers: { 'Content-Type': 'text/html; charset=utf-8' } });
}

// ── GET /admin/live-stats — lightweight live counter poll ─────────────────────

async function handleAdminLiveStats(request, env) {
  if (!checkAdminKey(request, env)) return new Response('Unauthorized', { status: 401 });
  const [countersRows, jobs24h] = await Promise.all([
    env.DB.prepare('SELECT key,value FROM counters').all(),
    env.DB.prepare('SELECT COUNT(*) as n FROM synced_results WHERE synced_at > ?')
      .bind(Math.floor(Date.now()/1000) - 86400).first(),
  ]);
  const counters = {};
  for (const row of (countersRows.results||[])) counters[row.key] = row.value;
  return jsonRes({ counters, jobs_24h: jobs24h?.n || 0, ts: Math.floor(Date.now()/1000) }, 200, CORS);
}

async function handleAdminIssueKey(request, env) {
  if (!checkAdminKey(request, env)) return new Response('Unauthorized', { status: 401 });
  let body;
  try { body = await request.json(); } catch { return jsonRes({ error: 'Invalid JSON' }, 400); }

  const userId = (body.user_id || body.whop_user_id || '').trim();
  if (!userId) return jsonRes({ error: 'user_id required' }, 400);

  const now = Math.floor(Date.now() / 1000);
  await env.DB.prepare(`INSERT INTO users (id,plan,created_at,last_seen) VALUES (?,'pro',?,?)
    ON CONFLICT(id) DO UPDATE SET last_seen=excluded.last_seen`).bind(userId, now, now).run();

  const existing = await env.DB.prepare(
    `SELECT key FROM license_keys WHERE user_id=? AND status='active' LIMIT 1`
  ).bind(userId).first();

  if (existing && !body.force) return jsonRes({ ok: true, key: existing.key, already_had_key: true });

  const key = await mintKey(env, userId, body.membership_id || null);
  return jsonRes({ ok: true, key, user_id: userId, issued_at: now });
}

async function handleAdminRevokeKey(request, env) {
  if (!checkAdminKey(request, env)) return new Response('Unauthorized', { status: 401 });
  let body;
  try { body = await request.json(); } catch { return jsonRes({ error: 'Invalid JSON' }, 400); }

  const keyStr = (body.key     || '').trim().toUpperCase();
  const userId = (body.user_id || '').trim();
  if (!keyStr && !userId) return jsonRes({ error: 'key or user_id required' }, 400);

  const now = Math.floor(Date.now() / 1000);
  let result;
  if (keyStr) {
    result = await env.DB.prepare(
      `UPDATE license_keys SET status='revoked', revoked_at=? WHERE key=? AND status='active'`
    ).bind(now, keyStr).run();
  } else {
    result = await env.DB.prepare(
      `UPDATE license_keys SET status='revoked', revoked_at=? WHERE user_id=? AND status='active'`
    ).bind(now, userId).run();
  }
  return jsonRes({ ok: true, revoked: keyStr||userId, rows_affected: result.meta?.changes??0 });
}

async function handleAdminClearDevice(request, env) {
  if (!checkAdminKey(request, env)) return new Response('Unauthorized', { status: 401 });
  let body;
  try { body = await request.json(); } catch { return jsonRes({ error: 'Invalid JSON' }, 400); }

  const keyStr = (body.key     || '').trim().toUpperCase();
  const userId = (body.user_id || '').trim();
  if (!keyStr && !userId) return jsonRes({ error: 'key or user_id required' }, 400);

  if (keyStr) {
    await env.DB.prepare(`UPDATE license_keys SET device_id=NULL WHERE key=?`).bind(keyStr).run();
  } else {
    await env.DB.prepare(`UPDATE license_keys SET device_id=NULL WHERE user_id=? AND status='active'`).bind(userId).run();
  }
  return jsonRes({ ok: true, cleared: keyStr||userId });
}

function checkAdminKey(request, env) {
  if (!env.ADMIN_KEY) return false;
  const url    = new URL(request.url);
  const header = (request.headers.get('Authorization') || '').replace(/^Bearer\s+/i, '');
  const query  = url.searchParams.get('key') || '';
  return header === env.ADMIN_KEY || query === env.ADMIN_KEY;
}

// ── Dashboard ─────────────────────────────────────────────────────────────────

async function handleDashboardValidate(request, env) {
  let body;
  try { body = await request.json(); } catch { return jsonRes({ error: 'Invalid JSON' }, 400); }
  const key = (body.license_key || '').trim().toUpperCase();
  if (!key.startsWith('PT-')) return jsonRes({ error: 'Invalid key format' }, 400);

  const row = await env.DB.prepare(`
    SELECT lk.user_id, lk.status, u.license_hint, u.plan
    FROM license_keys lk JOIN users u ON u.id=lk.user_id WHERE lk.key=?
  `).bind(key).first();
  if (!row)                    return jsonRes({ error: 'Key not found' }, 401);
  if (row.status !== 'active') return jsonRes({ error: `Key is ${row.status}` }, 401);

  const user  = { id: row.user_id, license_hint: row.license_hint, plan: row.plan||'pro' };
  const token = await issueToken(env, user);
  return new Response(JSON.stringify({ ok: true }), {
    status: 200,
    headers: { ...CORS, 'Content-Type': 'application/json',
      'Set-Cookie': `pt_session=${token}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=${TOKEN_TTL_SECS}` },
  });
}

async function handleDashboardLogout(request, env) {
  const cookie = request.headers.get('Cookie') || '';
  const m      = cookie.match(/pt_session=([a-f0-9]+)/);
  if (m) await env.SESSIONS.delete('token:' + m[1]);
  return new Response(JSON.stringify({ ok: true }), {
    status: 200,
    headers: { ...CORS, 'Content-Type': 'application/json',
      'Set-Cookie': 'pt_session=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=0' },
  });
}

function dashboardLogin() {
  return new Response(`<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>ProxyKit — Dashboard</title>
<style>*{box-sizing:border-box;margin:0;padding:0}body{background:#0d1117;color:#dce8f5;font-family:'Segoe UI',system-ui,sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;padding:20px}
.card{background:#161b27;border:1px solid #242e42;border-radius:12px;padding:32px;width:100%;max-width:400px}
h1{font-size:15px;font-weight:800;letter-spacing:.14em;color:#818cf8;margin-bottom:6px}.sub{font-size:11px;color:#5d7290;margin-bottom:24px}
label{display:block;font-size:10px;font-weight:700;letter-spacing:.1em;color:#5d7290;text-transform:uppercase;margin-bottom:6px}
input{display:block;width:100%;padding:10px 14px;background:#0d1117;border:1px solid #242e42;border-radius:8px;color:#dce8f5;font-size:14px;letter-spacing:.08em;font-family:monospace;margin-bottom:16px;outline:none}
input:focus{border-color:#818cf8}button{width:100%;padding:11px;background:#818cf8;border:none;border-radius:8px;color:#0d1117;font-weight:700;font-size:13px;cursor:pointer}
button:hover{background:#9ba4f8}button:disabled{opacity:.5;cursor:not-allowed}.err{color:#ff5c5c;font-size:12px;margin-top:10px;display:none}
</style></head><body>
<div class="card"><h1>⬡ PROXYKIT</h1><p class="sub">Enter your license key to access your dashboard</p>
<label for="k">License Key</label>
<input type="text" id="k" placeholder="PT-XXXXXX-XXXXXXXX-XXXXXX" autocomplete="off" spellcheck="false">
<button id="btn" onclick="go()">Access Dashboard</button>
<div class="err" id="err"></div></div>
<script>
document.getElementById('k').addEventListener('keydown',function(e){if(e.key==='Enter')go();});
async function go(){var k=document.getElementById('k').value.trim(),err=document.getElementById('err'),btn=document.getElementById('btn');
err.style.display='none';btn.disabled=true;btn.textContent='Checking…';
try{var r=await fetch('/dashboard/validate',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({license_key:k})});
var d=await r.json();if(d.ok)location.reload();else{err.textContent=d.error||'Invalid key';err.style.display='';}}
catch(e){err.textContent='Network error';err.style.display='';}
btn.disabled=false;btn.textContent='Access Dashboard';}
</script></body></html>`, { headers: { 'Content-Type': 'text/html; charset=utf-8' } });
}

async function dashboardPage(user, env) {
  const rows = await env.DB.prepare(
    `SELECT id,list_name,total,passed,pass_rate,avg_ms,synced_at FROM synced_results WHERE user_id=? ORDER BY synced_at DESC LIMIT 50`
  ).bind(user.id).all();
  const results = rows.results || [];
  const f  = n => n != null ? Number(n).toLocaleString() : '—';
  const p  = n => n != null ? n + '%' : '—';
  const dt = s => s ? new Date(s*1000).toISOString().slice(0,16).replace('T',' ')+' UTC' : '—';
  const rows_html = results.map(r => `<tr>
    <td>${esc(r.list_name||'Unnamed')}</td><td>${f(r.total)}</td>
    <td style="color:${r.pass_rate>=50?'#4ade80':'#ff5c5c'}">${p(r.pass_rate)}</td>
    <td>${r.avg_ms!=null?r.avg_ms+'ms':'—'}</td>
    <td style="font-size:11px;color:#5d7290">${dt(r.synced_at)}</td>
    <td><button onclick="del('${r.id}')" style="background:none;border:1px solid #ff5c5c;color:#ff5c5c;padding:3px 8px;border-radius:4px;font-size:11px;cursor:pointer">×</button></td>
  </tr>`).join('') || '<tr><td colspan="6" style="color:#5d7290;padding:16px 8px">No synced results yet</td></tr>';

  return new Response(`<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>ProxyKit Dashboard</title>
<style>*{box-sizing:border-box;margin:0;padding:0}body{background:#0d1117;color:#dce8f5;font-family:'Segoe UI',system-ui,sans-serif;padding:24px}
h1{font-size:17px;font-weight:800;letter-spacing:.12em;color:#818cf8}.topbar{display:flex;justify-content:space-between;align-items:center;margin-bottom:6px}
.sub{font-size:12px;color:#5d7290;margin-bottom:24px}.badge{background:#1c2536;border:1px solid #2d3f5c;border-radius:4px;padding:2px 8px;font-size:10px;font-weight:700;color:#818cf8}
.card{background:#161b27;border:1px solid #242e42;border-radius:10px;padding:16px;overflow-x:auto}.card-h{font-size:10px;font-weight:800;letter-spacing:.12em;color:#5d7290;text-transform:uppercase;margin-bottom:12px}
table{width:100%;border-collapse:collapse;font-size:12px;min-width:500px}th{text-align:left;padding:6px 8px;font-size:10px;font-weight:700;color:#5d7290;border-bottom:1px solid #242e42;text-transform:uppercase}
td{padding:8px 8px;border-bottom:1px solid rgba(36,46,66,.5)}.btn{background:#818cf8;border:none;border-radius:6px;color:#0d1117;font-weight:700;font-size:11px;padding:5px 12px;cursor:pointer}
</style></head><body>
<div class="topbar"><h1>⬡ PROXYKIT</h1><button class="btn" onclick="logout()">Sign Out</button></div>
<div class="sub">Key ···${esc(user.license_hint||'????')} &nbsp;·&nbsp; <span class="badge">${esc(user.plan||'pro')}</span></div>
<div class="card"><div class="card-h">Synced Results</div>
<table><thead><tr><th>List</th><th>Tested</th><th>Pass Rate</th><th>Avg Speed</th><th>Synced At</th><th></th></tr></thead>
<tbody>${rows_html}</tbody></table></div>
<script>
async function logout(){await fetch('/dashboard/logout',{method:'POST'});location.reload();}
async function del(id){if(!confirm('Delete?'))return;await fetch('/results/'+id,{method:'DELETE'});location.reload();}
</script></body></html>`, { headers: { 'Content-Type': 'text/html; charset=utf-8' } });
}

// ── Hub iframe HTML ───────────────────────────────────────────────────────────

function hubHTML(keyData, hasToken, discordInvite) {
  const keyDisplay = keyData?.key || null;
  const createdAt  = keyData?.created_at
    ? new Date(keyData.created_at * 1000).toISOString().slice(0, 10) : null;

  const discordSection = (keyDisplay && discordInvite) ? `
<div class="tip" style="margin-top:16px;border-color:rgba(88,101,242,.4)">
  <strong style="color:#7289da">💬 Join the ProxyKit Discord</strong><br>
  Get real-time PX change alerts, proxy tips, and community support.<br>
  <a href="${esc(discordInvite)}" target="_blank" rel="noopener noreferrer"
     style="display:inline-block;margin-top:10px;padding:8px 18px;background:#5865f2;color:#fff;
            border-radius:6px;font-weight:700;font-size:12px;text-decoration:none">
    Join Discord →
  </a>
</div>` : '';

  return `<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>ProxyKit — License Key</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{background:#0d1117;color:#dce8f5;font-family:'Segoe UI',system-ui,sans-serif;padding:32px 24px;min-height:100vh}
h1{font-size:14px;font-weight:800;letter-spacing:.14em;color:#818cf8;margin-bottom:20px}
.label{font-size:10px;font-weight:700;letter-spacing:.1em;color:#5d7290;text-transform:uppercase;margin-bottom:8px}
.key-box{display:flex;align-items:center;gap:8px;margin-bottom:12px}
.key{flex:1;padding:12px 16px;background:#0d1117;border:1px solid #2d3f5c;border-radius:8px;font-family:monospace;font-size:15px;letter-spacing:.1em;color:#dce8f5;word-break:break-all}
.copy-btn{padding:10px 16px;background:#818cf8;border:none;border-radius:8px;color:#0d1117;font-weight:700;font-size:12px;cursor:pointer;white-space:nowrap;flex-shrink:0}
.copy-btn:hover{background:#9ba4f8}.meta{font-size:11px;color:#5d7290;margin-bottom:4px}
.tip{background:#161b27;border:1px solid #242e42;border-radius:8px;padding:14px;font-size:12px;color:#5d7290;line-height:1.6;margin-top:16px}
.tip strong{color:#dce8f5}.pending{color:#ff9f43;font-size:13px;padding:16px;background:#1a1500;border:1px solid #3d2e00;border-radius:8px}
.loading{color:#5d7290;font-size:13px}
</style></head><body>
<h1>⬡ PROXYKIT — LICENSE KEY</h1>
<div id="root">${keyDisplay ? `
<div class="label">Your License Key</div>
<div class="key-box">
  <div class="key" id="key-text">${esc(keyDisplay)}</div>
  <button class="copy-btn" onclick="copyKey()">Copy</button>
</div>
<div class="meta">Issued ${esc(createdAt||'')}</div>
<div class="tip">
  <strong>How to activate:</strong><br>
  1. Download ProxyKit from the Downloads section<br>
  2. Open the app → <strong>Settings → Sign In</strong><br>
  3. Paste your key and click <strong>Validate</strong><br>
  Results sync automatically after every test run.
</div>
${discordSection}
` : `<p class="loading" id="msg">Loading your key…</p>`}</div>
<script>
function copyKey(){
  var k=document.getElementById('key-text');
  if(!k)return;
  navigator.clipboard.writeText(k.textContent).then(function(){
    var btn=document.querySelector('.copy-btn');
    btn.textContent='Copied!';setTimeout(function(){btn.textContent='Copy';},2000);
  });
}
${!keyDisplay ? `
var token=new URLSearchParams(location.search).get('token')||'';
async function loadKey(){
  if(!token){document.getElementById('msg').textContent='No token found. Open this page from your Whop dashboard.';return;}
  try{
    var r=await fetch('/hub/verify',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({token:token})});
    var d=await r.json();
    if(d.key){
      document.getElementById('root').innerHTML=
        '<div class="label">Your License Key</div>'+
        '<div class="key-box"><div class="key" id="key-text">'+d.key+'</div>'+
        '<button class="copy-btn" onclick="copyKey()">Copy</button></div>'+
        '<div class="meta">Issued '+(d.created_at?new Date(d.created_at*1000).toISOString().slice(0,10):'')+' </div>'+
        '<div class="tip"><strong>How to activate:</strong><br>1. Download ProxyKit<br>2. Settings → Sign In<br>3. Paste key → Validate</div>'+
        ${discordInvite ? `'<div class="tip" style="margin-top:16px;border-color:rgba(88,101,242,.4)"><strong style=\\'color:#7289da\\'>💬 Join the ProxyKit Discord</strong><br>Get PX change alerts and community support.<br><a href=\\'${esc(discordInvite)}\\' target=\\'_blank\\' style=\\'display:inline-block;margin-top:10px;padding:8px 18px;background:#5865f2;color:#fff;border-radius:6px;font-weight:700;font-size:12px;text-decoration:none\\'>Join Discord →</a></div>'` : `''`};
    }else if(d.error==='no_key'){
      document.getElementById('msg').innerHTML='<span style="color:#ff9f43">⏳ Purchase processing — this page will refresh…</span>';
      setTimeout(loadKey,10000);
    }else{document.getElementById('msg').textContent=d.message||'Unable to load key.';}
  }catch(e){document.getElementById('msg').textContent='Network error — please refresh.';}
}
loadKey();
` : ''}
</script></body></html>`;
}

// ── Admin HTML ────────────────────────────────────────────────────────────────

function adminHTML(stats, key) {
  // Pre-process px_sites so body_kw / hard_codes / header_keys are real arrays
  const pxSitesJS = (stats.px_sites||[]).map(s => ({
    ...s,
    body_kw:     Array.isArray(s.body_kw)     ? s.body_kw     : (safeParseJSON(s.body_kw)     || []),
    hard_codes:  Array.isArray(s.hard_codes)  ? s.hard_codes  : (safeParseJSON(s.hard_codes)  || []),
    header_keys: Array.isArray(s.header_keys) ? s.header_keys : (safeParseJSON(s.header_keys) || []),
  }));

  const statsJS = JSON.stringify({
    users:             stats.users             || 0,
    active_keys:       stats.active_keys       || 0,
    results:           stats.results           || 0,
    total_proxies:     stats.total_proxies     || 0,
    avg_pass_rate:     stats.avg_pass_rate     || null,
    suggestions:       stats.suggestions       || 0,
    log_count:         stats.log_count         || 0,
    total_requests:    stats.total_requests    || 0,
    total_validations: stats.total_validations || 0,
    px_changes:        stats.px_changes        || 0,
    px_interval_m:     stats.px_interval_m     || 10,
    per_user:          stats.per_user          || [],
    recent_logs:       stats.recent_logs       || [],
    recent_suggs:      stats.recent_suggs      || [],
    px_sites:          pxSitesJS,
    daily_jobs:        stats.daily_jobs        || [],
    app_config:        stats.app_config        || {},
  });

  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>ProxyKit Admin</title>
<style>
:root{--bg:#0d1117;--s1:#161b27;--s2:#1c2536;--bd:#242e42;--bh:#2d3f5c;--tx:#dce8f5;--mu:#5d7290;--dm:#3d4f6a;--ac:#818cf8;--ok:#4ade80;--wn:#ff9f43;--er:#ff5c5c;--pu:#c084fc;--r:10px}
*{box-sizing:border-box;margin:0;padding:0}
body{background:var(--bg);color:var(--tx);font-family:'Segoe UI',system-ui,sans-serif;font-size:13px;min-height:100vh}
a{color:var(--ac);text-decoration:none}
/* Topbar */
.topbar{display:flex;align-items:center;gap:0;padding:0 20px;background:var(--s1);border-bottom:1px solid var(--bd);position:sticky;top:0;z-index:100;height:48px}
.logo{font-size:14px;font-weight:800;letter-spacing:.14em;color:var(--ac);flex-shrink:0;margin-right:20px}
.tabs{display:flex;gap:0;flex:1;height:100%}
.tab-btn{padding:0 16px;height:100%;border-radius:0;font-size:11px;font-weight:700;letter-spacing:.08em;cursor:pointer;color:var(--mu);background:none;border:none;border-bottom:2px solid transparent;transition:all .15s}
.tab-btn:hover{color:var(--tx)}
.tab-btn.active{color:var(--tx);border-bottom-color:var(--ac)}
.live-pill{display:flex;align-items:center;gap:5px;background:var(--s2);border:1px solid var(--bd);border-radius:20px;padding:4px 10px;font-size:10px;font-weight:700;color:var(--mu);flex-shrink:0;cursor:pointer;transition:border-color .2s}
.live-pill:hover{border-color:var(--bh)}
.live-dot{width:6px;height:6px;border-radius:50%;background:var(--ok)}
.live-dot.pulse{animation:pulse 1.8s infinite}
@keyframes pulse{0%,100%{opacity:1}50%{opacity:.3}}
/* Pages */
.page{display:none;padding:20px}
.page.active{display:block}
/* KPI */
.kpi-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(140px,1fr));gap:10px;margin-bottom:20px}
.kpi{background:var(--s1);border:1px solid var(--bd);border-radius:var(--r);padding:14px 16px;transition:border-color .2s,transform .2s}
.kpi:hover{border-color:var(--bh);transform:translateY(-1px)}
.kv{font-size:22px;font-weight:900;color:var(--ac);font-family:monospace;letter-spacing:-.02em}
.kl{font-size:10px;font-weight:700;color:var(--mu);margin-top:4px;text-transform:uppercase;letter-spacing:.08em}
.kd{font-size:10px;color:var(--dm);margin-top:2px}
/* Cards */
.card{background:var(--s1);border:1px solid var(--bd);border-radius:var(--r);padding:16px;margin-bottom:16px;overflow-x:auto}
.card-h{font-size:10px;font-weight:800;color:var(--mu);text-transform:uppercase;letter-spacing:.1em;margin-bottom:14px;display:flex;align-items:center;gap:8px}
.card-h small{font-weight:400;color:var(--dm);text-transform:none;letter-spacing:0;font-size:10px}
.two-col{display:grid;grid-template-columns:1fr 1fr;gap:16px;margin-bottom:16px}
@media(max-width:820px){.two-col{grid-template-columns:1fr}}
/* Activity chart */
.chart-wrap{height:100px;display:flex;align-items:flex-end;gap:2px}
.cbar{flex:1;border-radius:2px 2px 0 0;min-height:2px;transition:opacity .15s;cursor:default;position:relative}
.cbar:hover{opacity:.7}
.cbar:hover::after{content:attr(title);position:absolute;bottom:calc(100% + 4px);left:50%;transform:translateX(-50%);background:var(--s2);border:1px solid var(--bh);border-radius:4px;padding:3px 7px;font-size:10px;color:var(--tx);white-space:nowrap;pointer-events:none;z-index:10}
/* Heatmap */
.heatmap{display:grid;grid-template-columns:repeat(10,1fr);gap:3px}
.hm-cell{aspect-ratio:1;border-radius:2px;cursor:default;transition:opacity .1s}
.hm-cell:hover{opacity:.7}
.hm-0{background:var(--s2)} .hm-1{background:#182436} .hm-2{background:#1a3050} .hm-3{background:#244070} .hm-4{background:#3a5ea0} .hm-5{background:var(--ac)}
/* Dist bars */
.dist-row{display:flex;align-items:center;gap:8px;margin-bottom:7px}
.dist-lbl{width:52px;font-size:10px;color:var(--mu);text-align:right;flex-shrink:0}
.dist-track{flex:1;height:14px;background:var(--s2);border-radius:3px;overflow:hidden}
.dist-fill{height:100%;border-radius:3px;transition:width .4s}
.dist-cnt{width:28px;font-size:10px;color:var(--mu);text-align:right;flex-shrink:0}
/* Tables */
table{width:100%;border-collapse:collapse;font-size:12px}
th{text-align:left;padding:7px 8px;font-size:10px;font-weight:700;color:var(--mu);border-bottom:1px solid var(--bd);text-transform:uppercase;white-space:nowrap;cursor:pointer;user-select:none}
th:hover{color:var(--tx)}
th.r,td.r{text-align:right}
td{padding:7px 8px;border-bottom:1px solid rgba(36,46,66,.35);vertical-align:middle}
tr:last-child td{border-bottom:none}
tr:hover td{background:rgba(129,140,248,.04)}
/* Badges */
.badge{display:inline-block;padding:2px 7px;border-radius:4px;font-size:10px;font-weight:700}
.b-ok{background:rgba(74,222,128,.12);color:var(--ok);border:1px solid rgba(74,222,128,.3)}
.b-er{background:rgba(255,92,92,.12);color:var(--er);border:1px solid rgba(255,92,92,.3)}
.b-wn{background:rgba(255,159,67,.12);color:var(--wn);border:1px solid rgba(255,159,67,.3)}
.b-ac{background:rgba(129,140,248,.12);color:var(--ac);border:1px solid rgba(129,140,248,.3)}
.b-pu{background:rgba(192,132,252,.12);color:var(--pu);border:1px solid rgba(192,132,252,.3)}
.b-mu{background:var(--s2);color:var(--mu);border:1px solid var(--bd)}
/* Buttons */
.btn{display:inline-flex;align-items:center;justify-content:center;gap:5px;padding:6px 12px;border-radius:6px;font-size:11px;font-weight:700;cursor:pointer;border:none;transition:all .15s;white-space:nowrap}
.btn-ac{background:var(--ac);color:#0d1117}.btn-ac:hover{background:#9ba4f8}
.btn-ok{background:rgba(74,222,128,.15);color:var(--ok);border:1px solid rgba(74,222,128,.3)}.btn-ok:hover{background:rgba(74,222,128,.25)}
.btn-er{background:rgba(255,92,92,.15);color:var(--er);border:1px solid rgba(255,92,92,.3)}.btn-er:hover{background:rgba(255,92,92,.25)}
.btn-dim{background:var(--s2);color:var(--mu);border:1px solid var(--bd)}.btn-dim:hover{color:var(--tx);border-color:var(--bh)}
/* Inputs */
.inp{background:var(--bg);border:1px solid var(--bh);border-radius:6px;color:var(--tx);padding:7px 10px;font-size:12px;font-family:monospace;outline:none;transition:border-color .15s}
.inp:focus{border-color:var(--ac)}
/* Action bar */
.act-bar{background:var(--s1);border:1px solid var(--bd);border-radius:var(--r);padding:14px 16px;margin-bottom:16px}
.act-bar-h{font-size:10px;font-weight:800;color:var(--mu);text-transform:uppercase;letter-spacing:.1em;margin-bottom:10px}
.act-row{display:flex;gap:8px;flex-wrap:wrap}
.result{margin-top:8px;font-size:12px;font-family:monospace;min-height:18px}
/* PX cards */
.pxcards{display:grid;grid-template-columns:repeat(auto-fill,minmax(300px,1fr));gap:10px;margin-bottom:14px}
.pxcard{background:var(--bg);border:1px solid var(--bh);border-radius:8px;padding:13px;transition:border-color .2s,opacity .2s}
.pxcard.off{opacity:.5;border-color:var(--bd)}
.pxcard:hover{border-color:var(--ac)}
/* Tag chips */
.tag{display:inline-block;padding:1px 6px;border-radius:4px;font-size:10px;font-family:monospace;margin:1px}
.tg-b{background:#1c2030;border:1px solid #2d3f5c;color:var(--ac)}
.tg-r{background:#2a1010;border:1px solid #5c2d2d;color:var(--er)}
.tg-g{background:#1a2010;border:1px solid #2d5c2d;color:var(--ok)}
/* Toggle switch */
.sw{display:inline-flex;align-items:center;gap:8px;cursor:pointer}
.sw-track{width:34px;height:18px;border-radius:9px;background:var(--bd);position:relative;transition:background .2s;flex-shrink:0}
.sw-track.on{background:var(--ac)}
.sw-thumb{position:absolute;top:2px;left:2px;width:14px;height:14px;border-radius:50%;background:#fff;transition:transform .2s;box-shadow:0 1px 3px rgba(0,0,0,.4)}
.sw-track.on .sw-thumb{transform:translateX(16px)}
/* Form grid */
.fg{display:grid;grid-template-columns:1fr 1fr;gap:8px}
.fg .full{grid-column:1/-1}
.fl{font-size:10px;font-weight:700;color:var(--mu);letter-spacing:.08em;text-transform:uppercase;margin-bottom:4px}
/* Search row */
.search-row{display:flex;gap:8px;margin-bottom:12px;align-items:center;flex-wrap:wrap}
.search-row .inp{flex:1;min-width:160px}
/* Mono helpers */
.mono{font-family:monospace}
.uid{font-family:monospace;font-size:10px;color:var(--dm)}
.hint{font-family:monospace;font-size:11px}
/* Sections */
.sec-title{font-size:18px;font-weight:800;color:var(--tx);margin-bottom:4px;letter-spacing:-.01em}
.sec-sub{font-size:12px;color:var(--mu);margin-bottom:20px}
/* Sec kpi row */
.sec-kpis{display:grid;grid-template-columns:repeat(auto-fill,minmax(120px,1fr));gap:10px;margin-bottom:16px}
/* Sugg card */
.sugg-card{background:var(--s1);border:1px solid var(--bd);border-radius:8px;padding:14px;margin-bottom:10px}
/* Tooltip via title attr handled by browser */
</style>
</head>
<body>

<!-- Topbar -->
<nav class="topbar">
  <div class="logo">⬡ PROXYKIT</div>
  <div class="tabs">
    <button class="tab-btn active" onclick="showTab('overview')" data-tab="overview">OVERVIEW</button>
    <button class="tab-btn" onclick="showTab('users')" data-tab="users">USERS</button>
    <button class="tab-btn" onclick="showTab('px')" data-tab="px">PX MONITOR</button>
    <button class="tab-btn" onclick="showTab('security')" data-tab="security">SECURITY</button>
    <button class="tab-btn" onclick="showTab('feedback')" data-tab="feedback">FEEDBACK</button>
  </div>
  <div class="live-pill" onclick="forceRefresh()" title="Click to refresh live stats">
    <div class="live-dot pulse" id="liveDot"></div>
    <span id="liveLabel">LIVE</span>
    <span id="liveTick" style="color:var(--dm);margin-left:2px"></span>
  </div>
</nav>

<!-- ── OVERVIEW ──────────────────────────────────────────────────────────────── -->
<div class="page active" id="page-overview">
  <div class="kpi-grid" id="kpiGrid"></div>
  <div class="two-col">
    <div class="card">
      <div class="card-h">30-Day Activity <small id="actSub"></small></div>
      <div id="actChart" class="chart-wrap" style="margin-bottom:8px"></div>
      <div style="display:flex;gap:12px;font-size:10px;color:var(--mu)">
        <span><span style="display:inline-block;width:8px;height:8px;background:var(--ac);border-radius:2px;margin-right:3px;vertical-align:middle"></span>Jobs/day</span>
        <span id="actTotal" style="margin-left:auto"></span>
      </div>
    </div>
    <div class="card">
      <div class="card-h">Pass Rate Distribution</div>
      <div id="distChart" style="margin-bottom:18px"></div>
      <div class="card-h" style="margin-bottom:8px">Activity Heatmap <small>last 30 days</small></div>
      <div id="heatmap" class="heatmap" style="margin-bottom:4px"></div>
      <div style="display:flex;justify-content:space-between;font-size:9px;color:var(--dm);margin-top:2px">
        <span id="hmStart"></span><span style="color:var(--dm)">less ◀ ▶ more</span><span id="hmEnd"></span>
      </div>
    </div>
  </div>
  <div class="card">
    <div class="card-h">Top Users <small id="topUserSub"></small></div>
    <div id="topUsersTable" style="overflow-x:auto"></div>
  </div>
</div>

<!-- ── USERS ─────────────────────────────────────────────────────────────────── -->
<div class="page" id="page-users">
  <div class="act-bar">
    <div class="act-bar-h">Key &amp; Session Management</div>
    <div class="act-row">
      <input class="inp" id="uid" placeholder="user_id or PT- key" style="flex:1;min-width:200px">
      <button class="btn btn-ac" onclick="issueKey()">Issue Key</button>
      <button class="btn btn-er" onclick="revokeKey()">Revoke Key</button>
      <button class="btn btn-dim" onclick="clearDevice()">Clear Device</button>
      <button class="btn btn-er" onclick="revokeSessions()">Revoke Sessions</button>
    </div>
    <div class="result" id="action-result"></div>
  </div>
  <div class="card">
    <div class="card-h">All Users <small id="userCountLbl"></small></div>
    <div class="search-row">
      <input class="inp" id="userSearch" placeholder="Search by ID, key hint…" oninput="renderUsersTable()" style="flex:1">
      <select class="inp" id="userSortSel" onchange="renderUsersTable()" style="font-family:inherit;cursor:pointer">
        <option value="last_job_at">Last Active</option>
        <option value="jobs_synced">Most Jobs</option>
        <option value="proxies_tested">Most Proxies</option>
        <option value="avg_pass_rate">Best Pass Rate</option>
      </select>
    </div>
    <div id="usersTable" style="overflow-x:auto"></div>
  </div>
</div>

<!-- ── PX MONITOR ─────────────────────────────────────────────────────────────── -->
<div class="page" id="page-px">
  <div style="display:flex;justify-content:space-between;align-items:flex-start;flex-wrap:wrap;gap:12px;margin-bottom:20px">
    <div>
      <div class="sec-title">PX Monitor Sites</div>
      <div class="sec-sub" id="pxSubtitle"></div>
    </div>
    <div style="display:flex;gap:8px;flex-wrap:wrap;align-items:center">
      <span style="font-size:11px;color:var(--mu)">Probe every</span>
      <input class="inp" id="pxInterval" type="number" min="1" max="120" style="width:58px;text-align:center">
      <span style="font-size:11px;color:var(--mu)">min</span>
      <button class="btn btn-dim" onclick="savePxInterval()">Save</button>
      <button class="btn btn-ok" onclick="showAddForm()">+ Add Site</button>
      <button class="btn btn-ac" onclick="seedPxDefaults()">Seed Defaults</button>
    </div>
  </div>
  <div id="pxcards" class="pxcards"></div>
  <!-- Add/Edit form -->
  <div id="siteFormWrap" style="display:none;margin-bottom:16px">
    <div class="card" style="border-color:var(--ac)">
      <div class="card-h"><span id="siteFormTitle">ADD SITE</span></div>
      <div class="fg" style="margin-bottom:10px">
        <div><div class="fl">ID</div><input class="inp" id="sId" placeholder="e.g. nike" style="width:100%"></div>
        <div><div class="fl">Display Name</div><input class="inp" id="sName" placeholder="e.g. Nike / SNKRS" style="width:100%"></div>
        <div class="full"><div class="fl">URL to Probe</div><input class="inp" id="sUrl" placeholder="https://www.nike.com/launch" style="width:100%"></div>
        <div><div class="fl">Protection Type</div><input class="inp" id="sProt" placeholder="e.g. Akamai BM" style="width:100%"></div>
        <div><div class="fl">Hard HTTP Codes (comma)</div><input class="inp" id="sHard" placeholder="403,429" style="width:100%"></div>
        <div class="full"><div class="fl">Body Keywords (comma)</div><input class="inp" id="sBody" placeholder="_abck, ak_bmsc" style="width:100%"></div>
        <div><div class="fl">Response Header Keys (comma)</div><input class="inp" id="sHdr" placeholder="x-akamai-request-id" style="width:100%"></div>
        <div><div class="fl">Sort Order</div><input class="inp" id="sSort" placeholder="0" style="width:100%"></div>
      </div>
      <div style="display:flex;gap:8px">
        <button class="btn btn-ac" onclick="saveSite()" style="flex:1">Save Site</button>
        <button class="btn btn-dim" onclick="cancelSiteForm()">Cancel</button>
      </div>
    </div>
  </div>
  <div class="result" id="px-result" style="margin-bottom:8px"></div>
  <!-- App config / feature toggles -->
  <div class="card">
    <div class="card-h">Feature Toggles <small>changes propagate to app instances within the next PX poll cycle</small></div>
    <div id="appConfigPanel"></div>
  </div>
  <!-- Release management -->
  <div class="card">
    <div class="card-h">🚀 Release Management <small>publish a new version — authenticated clients poll /update/latest</small></div>
    <div style="display:grid;gap:8px;grid-template-columns:1fr 1fr">
      <div>
        <label class="lbl">Version (e.g. 1.11.0)</label>
        <input class="inp" id="relVersion" placeholder="1.11.0" value="${esc(stats.app_config.latest_version||'')}">
      </div>
      <div>
        <label class="lbl">Release Date</label>
        <input class="inp" id="relDate" readonly value="${stats.app_config.release_date ? new Date(parseInt(stats.app_config.release_date)*1000).toLocaleDateString() : 'Not published'}">
      </div>
    </div>
    <label class="lbl" style="margin-top:8px">Direct .exe Download URL</label>
    <input class="inp" id="relUrl" placeholder="https://github.com/treys5/proxykit/releases/download/v1.11.0/ProxyKit.exe" value="${esc(stats.app_config.download_url||'')}">
    <label class="lbl" style="margin-top:8px">Release Notes (shown to users)</label>
    <textarea class="inp" id="relNotes" rows="3" placeholder="Bug fixes and performance improvements…" style="resize:vertical">${esc(stats.app_config.release_notes||'')}</textarea>
    <div style="display:flex;gap:8px;margin-top:8px">
      <button class="btn btn-ac" onclick="publishRelease()" style="flex:1">Publish Release</button>
      <button class="btn btn-dim" onclick="notifyUpdate()">Notify Users via Discord</button>
    </div>
    <div class="result" id="rel-result" style="margin-top:6px"></div>
  </div>
</div>

<!-- ── SECURITY ───────────────────────────────────────────────────────────────── -->
<div class="page" id="page-security">
  <div class="sec-kpis" id="secKpis"></div>
  <div class="card">
    <div class="card-h">Security Log <small id="secCountLbl"></small></div>
    <div class="search-row">
      <input class="inp" id="secSearch" placeholder="Filter by type, IP, key hint…" oninput="renderSecTable()">
      <select class="inp" id="secFilter" onchange="renderSecTable()" style="font-family:inherit;cursor:pointer">
        <option value="">All Events</option>
        <option value="bad_key">Bad Key</option>
        <option value="wrong_device">Wrong Device</option>
        <option value="revoked_key">Revoked Key</option>
        <option value="admin_probe">Admin Probe</option>
        <option value="device_transfer">Device Transfer</option>
        <option value="sessions_revoked">Sessions Revoked</option>
      </select>
    </div>
    <div style="overflow-x:auto">
      <table>
        <thead><tr><th>Event</th><th>Key</th><th>IP</th><th>Detail</th><th>Time</th></tr></thead>
        <tbody id="secBody"></tbody>
      </table>
    </div>
  </div>
</div>

<!-- ── FEEDBACK ───────────────────────────────────────────────────────────────── -->
<div class="page" id="page-feedback">
  <div style="display:flex;gap:8px;margin-bottom:16px;flex-wrap:wrap">
    <button class="btn btn-ac" id="sf-all"     onclick="filterSugg('')">All</button>
    <button class="btn btn-dim" id="sf-feature" onclick="filterSugg('feature')">Feature</button>
    <button class="btn btn-dim" id="sf-bug"     onclick="filterSugg('bug')">Bug</button>
    <button class="btn btn-dim" id="sf-general" onclick="filterSugg('general')">General</button>
    <span style="font-size:11px;color:var(--mu);align-self:center;margin-left:4px" id="suggCountLbl"></span>
  </div>
  <div id="suggList"></div>
</div>

<script>
var S=${statsJS};
var KEY=${JSON.stringify(key)};
var _tick=30,_prevCounts={};

// ── Tab nav ───────────────────────────────────────────────────────────────────
function showTab(name){
  document.querySelectorAll('.page').forEach(function(el){el.classList.remove('active');});
  document.querySelectorAll('.tab-btn').forEach(function(el){el.classList.remove('active');});
  document.getElementById('page-'+name).classList.add('active');
  document.querySelector('[data-tab="'+name+'"]').classList.add('active');
}

// ── Utils ─────────────────────────────────────────────────────────────────────
function f(n){return n!=null&&n!==''&&!isNaN(+n)?Number(n).toLocaleString():'—';}
function fp(n){return n!=null&&n!==''&&!isNaN(+n)?Number(n).toFixed(1)+'%':'—';}
function fm(n){return n!=null&&n!==''&&!isNaN(+n)?Math.round(n)+'ms':'—';}
function dt(s){return s?new Date(s*1000).toISOString().slice(0,16).replace('T',' ')+'Z':'';}
function ago(s){
  if(!s)return '—';
  var d=Math.floor(Date.now()/1000)-s;
  if(d<60)return d+'s ago';
  if(d<3600)return Math.floor(d/60)+'m ago';
  if(d<86400)return Math.floor(d/3600)+'h ago';
  return Math.floor(d/86400)+'d ago';
}
function esc(s){return String(s||'').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');}
function pc(r){return r==null?'var(--mu)':r>=60?'var(--ok)':r>=35?'var(--wn)':'var(--er)';}
function lcol(t){return t==='bad_key'||t==='revoked_key'?'var(--er)':t==='wrong_device'?'var(--wn)':t==='admin_probe'?'var(--pu)':'var(--mu)';}
function lbadge(t){return t==='bad_key'||t==='revoked_key'?'b-er':t==='wrong_device'?'b-wn':t==='admin_probe'?'b-pu':t==='device_transfer'?'b-ok':'b-mu';}

async function api(path,body){
  return fetch(path+'?key='+KEY,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)}).then(function(r){return r.json();});
}

// ── KPI grid ──────────────────────────────────────────────────────────────────
function renderKPIs(){
  var items=[
    {v:S.users,     l:'Users',        d:S.active_keys+' active keys'},
    {v:S.active_keys,l:'Active Keys', color:'var(--ok)'},
    {v:S.results,   l:'Jobs Synced'},
    {v:S.total_proxies,l:'Proxies Tested'},
    {v:S.avg_pass_rate!=null?S.avg_pass_rate.toFixed(1)+'%':null, l:'Avg Pass Rate', raw:S.avg_pass_rate, color:pc(S.avg_pass_rate)},
    {v:S.total_requests,  l:'Total Requests'},
    {v:S.total_validations,l:'Key Validations'},
    {v:S.log_count, l:'Security Events',color:'var(--wn)'},
    {v:S.px_changes,l:'PX Changes',   color:'var(--pu)'},
    {v:S.suggestions,l:'Suggestions'},
  ];
  var html=items.map(function(k){
    var disp=(k.v!=null&&k.v!=='')?
      (typeof k.v==='string'?esc(k.v):(isNaN(+k.v)?esc(k.v):Number(k.v).toLocaleString())):'—';
    return '<div class="kpi"><div class="kv" style="color:'+(k.color||'var(--ac)')+'">'+disp+'</div>'+
      '<div class="kl">'+esc(k.l)+'</div>'+(k.d?'<div class="kd">'+esc(k.d)+'</div>':'')+'</div>';
  }).join('');
  document.getElementById('kpiGrid').innerHTML=html;
}

// ── 30-day activity chart ─────────────────────────────────────────────────────
function renderActivityChart(){
  var wrap=document.getElementById('actChart');
  var dj=S.daily_jobs||[];
  var map={};dj.forEach(function(d){map[d.day]={jobs:+d.jobs||0,proxies:+d.proxies||0};});
  var days=[],total=0;
  for(var i=29;i>=0;i--){
    var dd=new Date(Date.now()-i*86400000);
    days.push(dd.toISOString().slice(0,10));
  }
  var vals=days.map(function(k){return map[k]?map[k].jobs:0;});
  var mx=Math.max.apply(null,vals.concat([1]));
  wrap.innerHTML=days.map(function(k,i){
    var v=vals[i];total+=v;
    var h=Math.max(Math.round(v/mx*96),v?3:1);
    return '<div class="cbar" style="height:'+h+'px;background:'+(v?'var(--ac)':'var(--s2)')+'" title="'+k+': '+v+' jobs"></div>';
  }).join('');
  document.getElementById('actTotal').textContent=total+' jobs total';
  document.getElementById('actSub').textContent='last 30 days';
}

// ── Pass rate distribution ────────────────────────────────────────────────────
function renderDistChart(){
  var bkts={'0–20':0,'20–40':0,'40–60':0,'60–80':0,'80–100':0};
  var cols={'0–20':'var(--er)','20–40':'var(--wn)','40–60':'var(--wn)','60–80':'var(--ok)','80–100':'var(--ok)'};
  (S.per_user||[]).forEach(function(u){
    var r=u.avg_pass_rate;if(r==null)return;
    if(r<20)bkts['0–20']++;else if(r<40)bkts['20–40']++;else if(r<60)bkts['40–60']++;
    else if(r<80)bkts['60–80']++;else bkts['80–100']++;
  });
  var mx=Math.max.apply(null,Object.values(bkts).concat([1]));
  document.getElementById('distChart').innerHTML=Object.keys(bkts).map(function(k){
    var pct=Math.round(bkts[k]/mx*100);
    return '<div class="dist-row"><div class="dist-lbl">'+k+'</div>'+
      '<div class="dist-track"><div class="dist-fill" style="width:'+pct+'%;background:'+cols[k]+'"></div></div>'+
      '<div class="dist-cnt">'+bkts[k]+'</div></div>';
  }).join('');
}

// ── Heatmap ───────────────────────────────────────────────────────────────────
function renderHeatmap(){
  var dj=S.daily_jobs||[];var map={};
  dj.forEach(function(d){map[d.day]=+d.jobs||0;});
  var days=[],mx=1;
  for(var i=29;i>=0;i--){
    var dd=new Date(Date.now()-i*86400000);
    var k=dd.toISOString().slice(0,10);
    days.push(k);if(map[k]>mx)mx=map[k];
  }
  document.getElementById('heatmap').innerHTML=days.map(function(k){
    var v=map[k]||0;
    var lv=v===0?0:v<=1?1:v<=3?2:v<=6?3:v<=10?4:5;
    return '<div class="hm-cell hm-'+lv+'" title="'+k+': '+v+' jobs"></div>';
  }).join('');
  if(days.length){
    document.getElementById('hmStart').textContent=days[0];
    document.getElementById('hmEnd').textContent=days[days.length-1];
  }
}

// ── Top users table ───────────────────────────────────────────────────────────
function renderTopUsers(){
  var top=(S.per_user||[]).slice(0,8);
  document.getElementById('topUserSub').textContent=top.length+' of '+S.per_user.length+' users';
  if(!top.length){document.getElementById('topUsersTable').innerHTML='<p style="color:var(--mu);padding:8px">No data yet.</p>';return;}
  var h='<table><thead><tr><th>User</th><th>Plan</th><th class="r">Jobs</th><th class="r">Proxies</th><th class="r">Pass Rate</th><th class="r">Avg Speed</th><th class="r">Last Active</th></tr></thead><tbody>';
  h+=top.map(function(u){
    return '<tr>'+
      '<td><span class="hint">···'+esc(u.license_hint||'????')+'</span><br><span class="uid">'+esc(u.id)+'</span></td>'+
      '<td><span class="badge b-ac">'+esc(u.plan||'pro')+'</span></td>'+
      '<td class="r">'+f(u.jobs_synced)+'</td>'+
      '<td class="r">'+f(u.proxies_tested)+'</td>'+
      '<td class="r" style="color:'+pc(u.avg_pass_rate)+';font-weight:700">'+fp(u.avg_pass_rate)+'</td>'+
      '<td class="r" style="color:var(--mu)">'+fm(u.avg_ms)+'</td>'+
      '<td class="r" style="color:var(--mu)">'+ago(u.last_job_at)+'</td>'+
    '</tr>';
  }).join('');
  document.getElementById('topUsersTable').innerHTML=h+'</tbody></table>';
}

// ── Full users table ──────────────────────────────────────────────────────────
function renderUsersTable(){
  var q=(document.getElementById('userSearch').value||'').toLowerCase();
  var sk=document.getElementById('userSortSel').value;
  var data=(S.per_user||[]).filter(function(u){
    if(!q)return true;
    return (u.id||'').includes(q)||(u.license_hint||'').includes(q)||(u.key||'').toLowerCase().includes(q);
  });
  data.sort(function(a,b){return (+b[sk]||0)-(+a[sk]||0);});
  document.getElementById('userCountLbl').textContent=data.length+' / '+S.per_user.length;
  if(!data.length){document.getElementById('usersTable').innerHTML='<p style="color:var(--mu);padding:12px">No users match.</p>';return;}
  var h='<table><thead><tr><th>User</th><th>Key Status</th><th>Device</th><th class="r">Jobs</th><th class="r">Proxies</th><th class="r">Pass Rate</th><th class="r">Speed</th><th class="r">Last Active</th><th></th></tr></thead><tbody>';
  h+=data.map(function(u){
    var uid=JSON.stringify(u.id);
    return '<tr>'+
      '<td><span class="hint">···'+esc(u.license_hint||'????')+'</span><br><span class="uid">'+esc(u.id)+'</span></td>'+
      '<td><span class="badge '+(u.key_status==='active'?'b-ok':'b-er')+'">'+esc(u.key_status||'none')+'</span></td>'+
      '<td style="font-size:11px;color:'+(u.device_id?'var(--ok)':'var(--mu)')+'">'+
        (u.device_id?'<span title="'+esc(u.device_id)+'">🔒 Bound</span>':'—')+'</td>'+
      '<td class="r">'+f(u.jobs_synced)+'</td>'+
      '<td class="r">'+f(u.proxies_tested)+'</td>'+
      '<td class="r" style="color:'+pc(u.avg_pass_rate)+';font-weight:700">'+fp(u.avg_pass_rate)+'</td>'+
      '<td class="r" style="color:var(--mu)">'+fm(u.avg_ms)+'</td>'+
      '<td class="r" style="color:var(--mu)">'+ago(u.last_job_at)+'</td>'+
      '<td><div style="display:flex;gap:4px">'+
        '<button class="btn btn-dim" style="padding:3px 8px;font-size:10px" onclick="quickClear('+uid+')">Clear</button>'+
        '<button class="btn btn-er" style="padding:3px 8px;font-size:10px" onclick="quickRevoke('+uid+')">Revoke</button>'+
      '</div></td>'+
    '</tr>';
  }).join('');
  document.getElementById('usersTable').innerHTML=h+'</tbody></table>';
}

function out(msg,ok){var el=document.getElementById('action-result');el.style.color=ok?'var(--ok)':'var(--er)';el.textContent=msg;}
async function issueKey(){var v=document.getElementById('uid').value.trim();if(!v){out('Enter a user_id',false);return;}out('…');var d=await api('/admin/issue-key',{user_id:v});d.ok?out((d.already_had_key?'Existing: ':'✓ New key: ')+d.key,true):out(d.error||'Error',false);}
async function revokeKey(){var v=document.getElementById('uid').value.trim();if(!v){out('Enter user_id or key',false);return;}if(!confirm('Revoke for '+v+'?'))return;out('…');var d=await api('/admin/revoke-key',v.startsWith('PT-')?{key:v}:{user_id:v});d.ok?out('✓ Revoked: '+d.revoked+' ('+d.rows_affected+' rows)',true):out(d.error||'Error',false);}
async function clearDevice(){var v=document.getElementById('uid').value.trim();if(!v){out('Enter user_id or key',false);return;}out('…');var d=await api('/admin/clear-device',v.startsWith('PT-')?{key:v}:{user_id:v});d.ok?out('✓ Device cleared: '+d.cleared,true):out(d.error||'Error',false);}
async function revokeSessions(){var v=document.getElementById('uid').value.trim();if(!v){out('Enter user_id',false);return;}if(!confirm('Revoke ALL sessions for '+v+'?'))return;out('…');var d=await api('/admin/revoke-sessions',{user_id:v});d.ok?out('✓ Revoked '+d.sessions_revoked+' sessions',true):out(d.error||'Error',false);}
function quickClear(uid){document.getElementById('uid').value=uid;showTab('users');clearDevice();}
function quickRevoke(uid){document.getElementById('uid').value=uid;showTab('users');revokeKey();}

// ── PX cards ──────────────────────────────────────────────────────────────────
function renderPXCards(){
  var sites=S.px_sites||[];
  var on=sites.filter(function(s){return s.enabled;}).length;
  document.getElementById('pxSubtitle').textContent=sites.length+' sites configured · '+on+' enabled · '+S.px_interval_m+'min probe interval';
  document.getElementById('pxInterval').value=S.px_interval_m||10;
  if(!sites.length){
    document.getElementById('pxcards').innerHTML='<div style="padding:32px;text-align:center;color:var(--mu);border:1px dashed var(--bd);border-radius:8px">No sites configured — click <strong style="color:var(--ac)">Seed Defaults</strong> to populate.</div>';
    return;
  }
  document.getElementById('pxcards').innerHTML=sites.map(function(s){
    var bk=(s.body_kw||[]).map(function(k){return '<span class="tag tg-b">'+esc(k)+'</span>';}).join('');
    var hc=(s.hard_codes||[]).map(function(c){return '<span class="tag tg-r">'+esc(c)+'</span>';}).join('');
    var hk=(s.header_keys||[]).map(function(h){return '<span class="tag tg-g">'+esc(h)+'</span>';}).join('');
    var sid=JSON.stringify(s.id);
    return '<div class="pxcard'+(s.enabled?'':' off')+'" id="pxcard-'+esc(s.id)+'">' +
      '<div style="display:flex;justify-content:space-between;align-items:flex-start;margin-bottom:8px">' +
        '<div>' +
          '<div style="font-weight:700;font-size:13px">'+esc(s.name)+'</div>' +
          '<div style="font-size:10px;color:var(--ac);margin-top:2px">'+esc(s.protection||'Unknown protection')+'</div>' +
        '</div>' +
        '<div style="display:flex;gap:4px;flex-shrink:0;margin-left:8px">' +
          '<span class="badge '+(s.enabled?'b-ok':'b-er')+'" style="cursor:pointer" title="Toggle enabled" onclick="toggleSite('+sid+')">'+(s.enabled?'ON':'OFF')+'</span>'+
          '<button class="btn btn-dim" style="padding:3px 8px;font-size:10px" onclick="editSite('+sid+')">✎</button>'+
          '<button class="btn btn-er" style="padding:3px 8px;font-size:10px" onclick="deleteSite('+sid+')">✕</button>'+
        '</div>' +
      '</div>' +
      '<div style="font-size:11px;color:var(--mu);word-break:break-all;margin-bottom:7px">'+esc(s.url)+'</div>' +
      (hc?'<div style="margin-bottom:4px"><div style="font-size:9px;color:var(--dm);text-transform:uppercase;margin-bottom:2px">Hard Codes</div>'+hc+'</div>':'')+
      (bk?'<div style="margin-bottom:4px"><div style="font-size:9px;color:var(--dm);text-transform:uppercase;margin-bottom:2px">Body Keywords</div>'+bk+'</div>':'')+
      (hk?'<div><div style="font-size:9px;color:var(--dm);text-transform:uppercase;margin-bottom:2px">Header Keys</div>'+hk+'</div>':'')+
    '</div>';
  }).join('');
}

function pxOut(msg,ok){var el=document.getElementById('px-result');el.style.color=ok?'var(--ok)':'var(--er)';el.textContent=msg;}

async function savePxInterval(){var v=parseInt(document.getElementById('pxInterval').value)||10;pxOut('Saving…');var d=await api('/admin/px-config',{interval_m:v});if(d.ok){S.px_interval_m=v;renderPXCards();pxOut('✓ Saved: '+v+' min',true);}else pxOut(d.error||'Error',false);}

async function toggleSite(id){
  pxOut('Toggling…');var d=await api('/admin/px-config',{toggle_id:id});
  if(d.ok){var s=S.px_sites.find(function(x){return x.id===id;});if(s){s.enabled=!s.enabled;renderPXCards();}pxOut('✓ Toggled: '+id,true);}
  else pxOut(d.error||'Error',false);
}

async function deleteSite(id){
  if(!confirm('Delete site "'+id+'"?'))return;
  pxOut('Deleting…');var d=await api('/admin/px-config/delete',{id:id});
  if(d.ok){S.px_sites=S.px_sites.filter(function(s){return s.id!==id;});renderPXCards();pxOut('✓ Deleted: '+id,true);}
  else pxOut(d.error||'Error',false);
}

function showAddForm(){
  ['sId','sName','sUrl','sProt','sHard','sBody','sHdr','sSort'].forEach(function(i){document.getElementById(i).value='';});
  document.getElementById('siteFormTitle').textContent='ADD SITE';
  var w=document.getElementById('siteFormWrap');w.style.display='';
  w.scrollIntoView({behavior:'smooth',block:'nearest'});
}

function editSite(id){
  var s=S.px_sites.find(function(x){return x.id===id;});
  if(!s){pxOut('Site not found — reload',false);return;}
  document.getElementById('sId').value=s.id;
  document.getElementById('sName').value=s.name;
  document.getElementById('sUrl').value=s.url;
  document.getElementById('sProt').value=s.protection||'';
  document.getElementById('sHard').value=(s.hard_codes||[]).join(',');
  document.getElementById('sBody').value=(s.body_kw||[]).join(',');
  document.getElementById('sHdr').value=(s.header_keys||[]).join(',');
  document.getElementById('sSort').value=s.sort_order||0;
  document.getElementById('siteFormTitle').textContent='EDIT — '+id.toUpperCase();
  var w=document.getElementById('siteFormWrap');w.style.display='';
  w.scrollIntoView({behavior:'smooth',block:'nearest'});
}

function cancelSiteForm(){document.getElementById('siteFormWrap').style.display='none';}

async function saveSite(){
  var id=document.getElementById('sId').value.trim().toLowerCase().replace(/\W/g,'_');
  var name=document.getElementById('sName').value.trim();
  var url=document.getElementById('sUrl').value.trim();
  if(!id||!name||!url){pxOut('id, name and URL required',false);return;}
  var hard=document.getElementById('sHard').value.split(',').map(function(x){return parseInt(x.trim());}).filter(function(x){return !isNaN(x);});
  var bkw=document.getElementById('sBody').value.split(',').map(function(x){return x.trim();}).filter(Boolean);
  var hks=document.getElementById('sHdr').value.split(',').map(function(x){return x.trim();}).filter(Boolean);
  var sort=parseInt(document.getElementById('sSort').value)||0;
  pxOut('Saving…');
  var d=await api('/admin/px-config',{site:{id,name,url,protection:document.getElementById('sProt').value.trim(),hard_codes:hard,body_kw:bkw,header_keys:hks,sort_order:sort}});
  if(d.ok){
    var ns={id,name,url,protection:document.getElementById('sProt').value.trim(),hard_codes:hard,body_kw:bkw,header_keys:hks,sort_order:sort,enabled:true};
    var idx=S.px_sites.findIndex(function(s){return s.id===id;});
    if(idx>=0)S.px_sites[idx]=ns;else S.px_sites.push(ns);
    renderPXCards();cancelSiteForm();pxOut('✓ Saved: '+id,true);
  }else pxOut(d.error||'Error',false);
}

var PX_DEFAULTS=[
  {id:'nike',name:'Nike / SNKRS',url:'https://www.nike.com/launch',protection:'Akamai BM',hard_codes:[403,429],body_kw:['_abck','ak_bmsc','AkamaiGuard'],header_keys:['x-akamai-request-id']},
  {id:'adidas',name:'Adidas',url:'https://www.adidas.com/us',protection:'Akamai BM',hard_codes:[403,429],body_kw:['_abck','ak_bmsc'],header_keys:['x-akamai-request-id']},
  {id:'supreme',name:'Supreme',url:'https://www.supremenewyork.com/shop/all',protection:'Shape/F5',hard_codes:[403,429],body_kw:['shape_utmb','f5-scrutinizer'],header_keys:[]},
  {id:'footlocker',name:'Foot Locker',url:'https://www.footlocker.com/',protection:'PerimeterX',hard_codes:[403],body_kw:['_pxhd','_pxvid','_px2'],header_keys:['x-px-version']},
  {id:'jdsports',name:'JD Sports',url:'https://www.jdsports.com/',protection:'DataDome',hard_codes:[403],body_kw:['datadome','_dd_s'],header_keys:['x-datadome']},
  {id:'finishline',name:'Finish Line',url:'https://www.finishline.com/',protection:'Akamai BM',hard_codes:[403,429],body_kw:['_abck'],header_keys:[]},
  {id:'yeezysupply',name:'Yeezy Supply',url:'https://www.yeezysupply.com/',protection:'Akamai BM',hard_codes:[403,429],body_kw:['_abck'],header_keys:[]},
  {id:'jordan',name:'Jordan Brand',url:'https://www.jordan.com/',protection:'Akamai BM',hard_codes:[403,429],body_kw:['_abck'],header_keys:[]},
  {id:'shopify',name:'Shopify Checkout',url:'https://www.shopify.com/checkout',protection:'Cloudflare',hard_codes:[403,503],body_kw:['cf-challenge','__cf_bm'],header_keys:[]},
  {id:'walmart',name:'Walmart',url:'https://www.walmart.com/',protection:'Akamai BM',hard_codes:[403,429],body_kw:['_abck','ak_bmsc'],header_keys:[]},
];
async function seedPxDefaults(){
  if(!confirm('Seed '+PX_DEFAULTS.length+' default sites? Existing entries will be updated.'))return;
  pxOut('Seeding…');var errs=0;
  for(var i=0;i<PX_DEFAULTS.length;i++){
    var s=PX_DEFAULTS[i];
    var d=await api('/admin/px-config',{site:{...s,sort_order:i}});
    if(!d.ok)errs++;else{
      var ns={...s,sort_order:i,enabled:true};
      var idx=S.px_sites.findIndex(function(x){return x.id===s.id;});
      if(idx>=0)S.px_sites[idx]=ns;else S.px_sites.push(ns);
    }
  }
  renderPXCards();
  errs===0?pxOut('✓ Seeded '+PX_DEFAULTS.length+' sites',true):pxOut(errs+' error(s) — check console',false);
}

// ── App config / feature toggles ──────────────────────────────────────────────
var CFG_DEFS=[
  {key:'maintenance_mode',  label:'Maintenance Mode',        desc:'Show a maintenance banner to all app users on next startup.',            type:'bool',def:'0'},
  {key:'analytics_enabled', label:'Community Analytics',     desc:'Allow app instances to report anonymised proxy stats.',                  type:'bool',def:'1'},
  {key:'cloud_sync_enabled',label:'Cloud Sync',              desc:'Allow app instances to sync job results to the cloud.',                  type:'bool',def:'1'},
];
function renderAppConfig(){
  var cfg=S.app_config||{};
  var html=CFG_DEFS.map(function(r){
    var raw=cfg[r.key]!=null?String(cfg[r.key]):r.def;
    var on=raw==='1'||raw==='true';
    return '<div style="display:flex;align-items:center;justify-content:space-between;padding:12px 0;border-bottom:1px solid var(--bd)">'+
      '<div>'+
        '<div style="font-size:13px;font-weight:600;margin-bottom:2px">'+esc(r.label)+'</div>'+
        '<div style="font-size:11px;color:var(--mu)">'+esc(r.desc)+'</div>'+
        '<div style="font-size:10px;color:var(--dm);margin-top:1px;font-family:monospace">'+esc(r.key)+'</div>'+
      '</div>'+
      '<label class="sw" onclick="setConfigToggle('+JSON.stringify(r.key)+','+(on?'false':'true')+')">'+
        '<div class="sw-track'+(on?' on':'')+'"><div class="sw-thumb"></div></div>'+
        '<span style="font-size:11px;font-weight:700;color:'+(on?'var(--ok)':'var(--mu)')+'">'+esc(on?'ON':'OFF')+'</span>'+
      '</label>'+
    '</div>';
  }).join('');
  document.getElementById('appConfigPanel').innerHTML=html||'<p style="color:var(--mu);font-size:12px">No toggles defined.</p>';
}

async function setConfigToggle(key,enable){
  var val=enable?'1':'0';
  var d=await api('/admin/px-config',{config_key:key,config_value:val});
  if(d.ok){if(!S.app_config)S.app_config={};S.app_config[key]=val;renderAppConfig();pxOut('✓ '+key+' = '+val,true);}
  else pxOut(d.error||'Error',false);
}

// ── Release management ────────────────────────────────────────────────────────
function relOut(msg,ok){var el=document.getElementById('rel-result');if(!el)return;el.textContent=msg;el.style.color=ok?'var(--ok)':'var(--er)';}
async function publishRelease(){
  var v=document.getElementById('relVersion').value.trim();
  var u=document.getElementById('relUrl').value.trim();
  var n=document.getElementById('relNotes').value.trim();
  if(!v||!u){relOut('Version and download URL are required.',false);return;}
  relOut('Publishing…');
  var d=await api('/admin/publish-release',{version:v,download_url:u,notes:n});
  if(d.ok){
    if(!S.app_config)S.app_config={};
    S.app_config.latest_version=v; S.app_config.download_url=u; S.app_config.release_notes=n;
    S.app_config.release_date=String(Math.floor(Date.now()/1000));
    document.getElementById('relDate').value=new Date().toLocaleDateString();
    relOut('✓ Published v'+d.version,true);
  } else relOut(d.error||'Error',false);
}
async function notifyUpdate(){
  var v=document.getElementById('relVersion').value.trim();
  var u=document.getElementById('relUrl').value.trim();
  var n=document.getElementById('relNotes').value.trim();
  if(!v){relOut('Enter a version first.',false);return;}
  if(!confirm('Send Discord update notifications to all opted-in users?\n\nVersion: v'+v)){return;}
  relOut('Sending notifications…');
  var d=await api('/admin/notify-update',{version:v,download_url:u,notes:n});
  if(d.ok) relOut('✓ Sent to '+d.sent+' webhook'+(d.sent!==1?'s':'')+(d.failed?' ('+d.failed+' failed)':''),true);
  else relOut(d.error||'Error',false);
}

// ── Security ──────────────────────────────────────────────────────────────────
function renderSecKPIs(){
  var logs=S.recent_logs||[];
  var ct={bad_key:0,wrong_device:0,revoked_key:0,admin_probe:0,device_transfer:0};
  logs.forEach(function(l){if(ct[l.event_type]!=null)ct[l.event_type]++;});
  document.getElementById('secKpis').innerHTML=[
    {k:'bad_key',l:'Bad Keys',c:'b-er'},
    {k:'wrong_device',l:'Wrong Device',c:'b-wn'},
    {k:'revoked_key',l:'Revoked Keys',c:'b-er'},
    {k:'admin_probe',l:'Admin Probes',c:'b-pu'},
    {k:'device_transfer',l:'Transfers',c:'b-ok'},
  ].map(function(x){
    return '<div class="kpi"><div class="kv" style="color:'+lcol(x.k)+'">'+ct[x.k]+'</div><div class="kl">'+x.l+'</div></div>';
  }).join('');
  document.getElementById('secCountLbl').textContent=S.log_count+' total events';
}

function renderSecTable(){
  var q=(document.getElementById('secSearch').value||'').toLowerCase();
  var flt=document.getElementById('secFilter').value;
  var data=(S.recent_logs||[]).filter(function(l){
    if(flt&&l.event_type!==flt)return false;
    if(!q)return true;
    return (l.event_type||'').includes(q)||(l.ip||'').includes(q)||(l.key_hint||'').includes(q)||(l.detail||'').toLowerCase().includes(q);
  });
  document.getElementById('secBody').innerHTML=data.map(function(l){
    return '<tr>'+
      '<td><span class="badge '+lbadge(l.event_type)+'">'+esc(l.event_type)+'</span></td>'+
      '<td class="mono" style="font-size:11px">'+esc(l.key_hint||'—')+'</td>'+
      '<td style="font-size:11px;color:var(--mu)">'+esc(l.ip||'—')+'</td>'+
      '<td style="font-size:11px;max-width:280px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title="'+esc(l.detail||'')+'">'+esc(l.detail||'—')+'</td>'+
      '<td style="font-size:11px;color:var(--dm);white-space:nowrap">'+dt(l.created_at)+'</td>'+
    '</tr>';
  }).join('')||'<tr><td colspan="5" style="color:var(--mu);padding:12px">No matching events.</td></tr>';
}

// ── Suggestions ───────────────────────────────────────────────────────────────
var _sf='';
function filterSugg(cat){
  _sf=cat;
  document.querySelectorAll('[id^="sf-"]').forEach(function(b){b.className='btn btn-dim';});
  document.getElementById('sf-'+(cat||'all')).className='btn btn-ac';
  renderSuggs();
}
function renderSuggs(){
  var data=(S.recent_suggs||[]).filter(function(s){return !_sf||s.category===_sf;});
  document.getElementById('suggCountLbl').textContent=data.length+' suggestions';
  document.getElementById('suggList').innerHTML=data.map(function(s){
    var cc=s.category==='feature'?'var(--ac)':s.category==='bug'?'var(--er)':'var(--mu)';
    return '<div class="sugg-card">'+
      '<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">'+
        '<span class="badge" style="color:'+cc+';border-color:'+cc+';background:rgba(0,0,0,.2)">'+esc(s.category||'general')+'</span>'+
        '<span style="font-size:11px;color:var(--dm)">'+dt(s.submitted_at)+'</span>'+
      '</div>'+
      '<div style="font-size:13px;line-height:1.65;color:var(--tx)">'+esc(s.body)+'</div>'+
    '</div>';
  }).join('')||'<p style="color:var(--mu);padding:8px">No suggestions match.</p>';
}

// ── Live polling ──────────────────────────────────────────────────────────────
async function forceRefresh(){
  document.getElementById('liveDot').classList.remove('pulse');
  document.getElementById('liveDot').style.background='var(--wn)';
  document.getElementById('liveLabel').textContent='…';
  try{
    var r=await fetch('/admin/live-stats?key='+KEY);
    if(!r.ok)throw new Error('http '+r.status);
    var d=await r.json();
    if(d.counters){
      S.total_requests=d.counters.total_requests||S.total_requests;
      S.total_validations=d.counters.total_validations||S.total_validations;
      S.px_changes=d.counters.px_changes||S.px_changes;
      renderKPIs();
    }
    document.getElementById('liveDot').style.background='var(--ok)';
    document.getElementById('liveLabel').textContent='LIVE';
    _tick=30;
  }catch(e){
    document.getElementById('liveDot').style.background='var(--er)';
    document.getElementById('liveLabel').textContent='ERR';
  }
  document.getElementById('liveDot').classList.add('pulse');
}

setInterval(function(){
  _tick--;
  document.getElementById('liveTick').textContent='('+_tick+'s)';
  if(_tick<=0){_tick=30;forceRefresh();}
},1000);

// ── Init ──────────────────────────────────────────────────────────────────────
renderKPIs();
renderActivityChart();
renderDistChart();
renderHeatmap();
renderTopUsers();
renderUsersTable();
renderPXCards();
renderAppConfig();
renderSecKPIs();
renderSecTable();
filterSugg('');
</script>
</body>
</html>`;
}

// ── GET /update/latest — authenticated update manifest ────────────────────────

async function handleUpdateLatest(env) {
  const rows = await env.DB.prepare(
    `SELECT key,value FROM app_config WHERE key IN ('latest_version','download_url','release_notes','release_date')`
  ).all();
  const cfg = {};
  for (const row of (rows.results||[])) cfg[row.key] = row.value;
  if (!cfg.latest_version || !cfg.download_url) return jsonRes({ version: null });
  return jsonRes({
    version:      cfg.latest_version,
    download_url: cfg.download_url,
    notes:        cfg.release_notes || '',
    date:         cfg.release_date  || '',
  });
}

// ── POST /admin/publish-release — set latest version in D1 ───────────────────

async function handleAdminPublishRelease(request, env) {
  if (!checkAdminKey(request, env)) return new Response('Unauthorized', { status: 401 });
  let body;
  try { body = await request.json(); } catch { return jsonRes({ error: 'Invalid JSON' }, 400); }
  const version = (body.version      || '').trim();
  const dlUrl   = (body.download_url || '').trim();
  const notes   = (body.notes        || '').trim();
  if (!version || !dlUrl) return jsonRes({ error: 'version and download_url required' }, 400);
  const now = Math.floor(Date.now()/1000);
  const upsert = (k,v) => env.DB.prepare(
    `INSERT INTO app_config(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`
  ).bind(k,v,now).run();
  await Promise.all([
    upsert('latest_version', version),
    upsert('download_url',   dlUrl),
    upsert('release_notes',  notes),
    upsert('release_date',   String(now)),
  ]);
  return jsonRes({ ok: true, version });
}

// ── POST /admin/notify-update — send Discord update DMs to opted-in users ────

async function handleAdminNotifyUpdate(request, env) {
  if (!checkAdminKey(request, env)) return new Response('Unauthorized', { status: 401 });
  let body;
  try { body = await request.json(); } catch { return jsonRes({ error: 'Invalid JSON' }, 400); }
  const version = (body.version      || '').trim();
  const notes   = (body.notes        || '').trim();
  const dlUrl   = (body.download_url || '').trim();
  if (!version) return jsonRes({ error: 'version required' }, 400);

  // All users with personal Discord webhooks + global ProxyKit alert opt-in
  const rows = await env.DB.prepare(
    `SELECT p.discord_webhook_url FROM user_preferences p
     WHERE p.global_discord_opt=1 AND p.discord_webhook_url IS NOT NULL AND p.discord_webhook_url!=''`
  ).all();

  const globalWebhook = await env.KV?.get('GLOBAL_DISCORD_WEBHOOK').catch(()=>null) || null;
  const webhooks = new Set();
  for (const r of (rows.results||[])) webhooks.add(r.discord_webhook_url);
  if (globalWebhook) webhooks.add(globalWebhook);

  const embed = {
    title: `🚀 ProxyKit v${version} Available`,
    description: notes || 'A new version of ProxyKit is available. Open the app and click **INSTALL & RESTART** to update.',
    color: 0x818cf8,
    fields: dlUrl ? [{ name: 'Download', value: `[ProxyKit v${version}](${dlUrl})`, inline: true }] : [],
    footer: { text: 'ProxyKit Auto-Update' },
    timestamp: new Date().toISOString(),
  };

  let sent = 0, failed = 0;
  for (const wh of webhooks) {
    try {
      const r = await fetch(wh, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ embeds: [embed] }),
      });
      if (r.ok) sent++; else failed++;
    } catch { failed++; }
  }
  return jsonRes({ ok: true, sent, failed, total: webhooks.size });
}

// ── Helpers ───────────────────────────────────────────────────────────────────

function clamp(v,min,max){return Math.max(min,Math.min(max,v));}
function esc(s){return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');}
function safeParseJSON(s){try{return typeof s==='string'?JSON.parse(s):s;}catch{return {};}}
function jsonRes(data,status=200,extra={}){
  return new Response(JSON.stringify(data),{status,headers:{...CORS,'Content-Type':'application/json',...extra}});
}
