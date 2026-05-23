#!/usr/bin/env node
'use strict';

const http = require('http');
const net  = require('net');
const fs   = require('fs');
const path = require('path');
const url  = require('url');

const PORT        = process.env.PORT || 8080;
const APP_VERSION = '1.07';

// When packaged as an asar, __dirname is read-only.
// Use APP_DATA_DIR (set by main.js to app.getPath('userData')) for all writes.
const DATA_DIR = process.env.APP_DATA_DIR || __dirname;

const jobs      = {};
const sessions  = {};
const providers = {};

const sessionGroups = {};

// ── Proxy key includes credentials so rotating proxies aren't deduped ────────
function proxyKey(p) {
  return (p.username||'') + ':' + (p.password||'') + '@' + p.host + ':' + p.port;
}

// ── Known datacenter/hosting org patterns ────────────────────────────────────
const DATACENTER_ORGS = [
  // Hyperscalers
  'amazon','aws','azure','microsoft azure','google cloud','google llc',
  'digitalocean','linode','vultr','hetzner','ovh','leaseweb','rackspace',
  // CDN/Edge
  'akamai','cloudflare','fastly','edgecast','stackpath','cdn77',
  'keycdn','bunnycdn','g-core','gcore','incapsula','imperva',
  // Hosting/DC providers
  'choopa','choopa llc','constant','colocrossing','psychz','fdcservers',
  'quadranet','sharktech','tzulo','krypt','multacom','wowrack','nocix',
  'servermania','serverbeach','singlehop','nexcess','mochahost','performive',
  'host.net','reliablesite','wholesale internet','limestone networks',
  'staminus','colocrossing','hostwinds','datalink','fibernet','nexeon',
  'multacom','tzulo','ibm cloud','oracle cloud','sap ',
  // Transit/backbone (not retail ISP)
  'cogent','level 3','lumen','zayo','telia company','internap',
  'xo communications','tata communications','ntt communications',
  'zenlayer','twtelecom','tw telecom','he.net','hurricane electric',
  'packet','equinix','global telecom','incero','spartanhost',
  'mchost','quickpacket','volumedrive','hostdime','sectorlink',
  'voxility','path network','ddos-guard','serverius','combahton',
  'bg networks','serverius','hosthatch','racknerd','buyvm',
  'frantech','1gservers','asfastweb','as13335','as15169',
  'dedicated server','colocation','data center','datacenter',
  'server hosting','vps hosting','cloud hosting','web hosting',
];

// ── Known residential/mobile ISPs — organized by region ─────────────────────
const RESIDENTIAL_ISPS = [
  // ── United States ──
  'comcast','xfinity','spectrum','charter communications','at&t','verizon',
  'cox communications','optimum','cablevision','frontier communications',
  'windstream','consolidated communications','mediacom','wow!','altice',
  'suddenlink','astound','breezeline','cincinnati bell','metrocast',
  'armstrong','buckeye broadband','sparklight','midcontinent',
  'wave broadband','ziply fiber','rcn','service electric','surewest',
  't-mobile','tmobile','cricket wireless','boost mobile','metro pcs',
  'metropcs','sbcglobal','bellsouth','uverse','hughes network',
  'viasat','earthlink','centurytel','embarq','brightspeed',
  'lumen residential','centurylink residential','dish network','directv',
  // ── Canada ──
  'bell canada','rogers communications','telus','shaw communications',
  'cogeco','videotron','sasktel','mts allstream','eastlink',
  'tbaytel','northwestel','teksavvy','distributel','fido solutions',
  'freedom mobile','koodo','public mobile','virgin plus',
  // ── United Kingdom ──
  'bt group','british telecom','virgin media','sky broadband','talktalk',
  'plusnet','ee limited','three uk','vodafone uk','o2 uk','now broadband',
  'hyperoptic','community fibre','g.network','trooli','toob limited',
  'gigaclear','brsk','zzoomm','cityfibre','aquiss','air broadband',
  'cuckoo internet','isp solutions','hay systems',
  // ── Germany ──
  'deutsche telekom','telekom deutschland','vodafone germany','unitymedia',
  'o2 germany','1&1','freenet','congstar','klarmobil','drillisch',
  'eplus','mobilfunk','netcologne','wilhelm.tel','mnet','inexio',
  // ── France ──
  'orange france','sfr','bouygues telecom','free sas','iliad',
  'numericable','colt telecom','completel','altitude telecom',
  // ── Spain ──
  'telefonica de espana','movistar','vodafone spain','orange spain',
  'masmovil','yoigo','euskaltel','r cable','telecable','pepephone',
  // ── Italy ──
  'telecom italia','tim ','vodafone italy','fastweb','wind tre',
  'tiscali','iliad italy','sky italia','infostrada',
  // ── Netherlands ──
  'kpn','ziggo','t-mobile netherlands','xs4all','delta','kabelbedrijven',
  'online.nl','vodafone libertel','tele2 netherlands',
  // ── Belgium ──
  'proximus','telenet','base company','mobile vikings','orange belgium',
  // ── Switzerland ──
  'swisscom','sunrise','salt mobile','upc switzerland','quickline',
  // ── Austria ──
  'a1 telekom austria','magenta telekom','hutchison drei','drei austria',
  // ── Poland ──
  'orange polska','polkomtel','play','t-mobile polska','netia',
  'vectra','multimedia polska','diagnostyka','aster',
  // ── Nordics ──
  'telia','telenor','elisa','dna plc','tdc','yousee','one.dk',
  'ice norway','altibox','get as','canal digital',
  'siminn','nova iceland','vodafone iceland',
  // ── Eastern Europe ──
  'rostelecom','pjsc mts','mts russia','vimpelcom','beeline',
  'megafon','tele2 russia','ttk','er-telecom','dom.ru',
  'kyivstar','lifecell','ukrtelecom','triolan',
  'telekom srbija','a1 serbia','mts serbia',
  'digi communications','orange romania','telekom romania',
  'bulsatcom','a1 bulgaria','telenor bulgaria',
  'cosmote','ote','nova greece','wind hellas',
  'iskon','t-com hrvatska','a1 hrvatska',
  'telekomunikacja polska','netia','polpak',
  // ── Russia/CIS ──
  'kazakhtelecom','kcell','beeline kazakhstan',
  'azercell','bakcell','unitel azerbaijan',
  // ── Australia / NZ ──
  'telstra','optus','tpg telecom','aussie broadband','internode',
  'iinet','dodo','superloop','launtel','exetel',
  'vodafone australia','amaysim','catch connect',
  'spark new zealand','vodafone new zealand','2degrees',
  // ── Japan ──
  'ntt docomo','softbank','kddi','au by kddi','iij',
  'biglobe','so-net','ocn','nifty','plala',
  'jcom','uq communications','mineo','rakuten mobile',
  // ── South Korea ──
  'kt corporation','sk broadband','lg uplus','kt olleh',
  'sk telecom','lg telecom','cj hellovision',
  // ── China ──
  'china telecom','china unicom','china mobile',
  'chinanet','cnc','cernet',
  // ── India ──
  'airtel','reliance jio','bsnl','mtnl','vodafone india',
  'idea cellular','vi ','hathway','act fibernet','asianet',
  'd-vois','railnet','railwire','tikona','you broadband',
  // ── Southeast Asia ──
  'singtel','starhub','m1 limited','myrepublic',
  'maxis','celcom','digi.com','tm net','unifi',
  'ais fibre','true move','dtac','tot public',
  'viettel','vnpt','fpt telecom','vietnamobile',
  'globe telecom','pldt','smart communications','sun cellular',
  'xl axiata','indosat','telkomsel','biznet','myrepublic indonesia',
  // ── Latin America ──
  'vivo','claro brasil','tim brasil','oi internet','net virtua',
  'telmex','izzi','totalplay','axtel','telcel','movistar mexico',
  'etb colombia','une','tigo colombia','claro colombia',
  'entel chile','gtd','claro chile','movistar chile',
  'personal argentina','fibertel','claro argentina','cablevision argentina',
  'antel','claro uruguay',
  // ── Middle East ──
  'bezeq','hot telecom','partner communications',
  'du telecom','etisalat','mobily','stc','jawwy',
  'turkcell','turk telekom','vodafone turkey','superonline',
  // ── Africa ──
  'safaricom','vodacom','mtn','airtel africa','glo mobile',
  'telkom south africa','cell c','rain',
  'ethio telecom','telecom egypt','maroc telecom',
];

// ── Keyword patterns that strongly suggest retail ISP (not DC) ───────────────
// checked AFTER the DC list, so DC-named orgs won't slip through
const RESIDENTIAL_KEYWORDS = [
  'telecom','telekom','telefon','telefonica','telstra','telus',
  'cablevision','cable tv','cable network',
  'broadband','fiber internet','fibre internet',
  'dsl ','adsl','vdsl','xdsl',
  'mobile network','mobile operator','mobile service',
  'cellular','gsm network','lte network',
  'internet service provider',
  'kommunikation','kommunikations',
  'telecomunicac','telecomunikac',
  'telekomunikacja','telekomunikace',
];

// ── PerimeterX / bot-challenge response markers ───────────────────────────────
const PX_MARKERS = [
  '_pxhd', 'px-captcha', 'press & hold', '_pxde', 'pxchallenge',
  'px.init', 'perimeterx', '_px2', 'pxi/', 'human challenge',
];

// Always-on parallel test URLs (in addition to ip-api and optional target)
const HTTPBIN_URL = 'http://httpbin.org/ip';

// Known datacenter ASN numbers for -20 score penalty
const DATACENTER_ASN_NUMS = new Set([
  13335,  // Cloudflare
  15169,  // Google
  16509,  // Amazon AWS
  14618,  // Amazon AWS
  8075,   // Microsoft Azure
  36351,  // SoftLayer/IBM Cloud
  63949,  // Linode/Akamai
  14061,  // DigitalOcean
  20473,  // Choopa/Vultr
  24940,  // Hetzner
  16276,  // OVH
  12876,  // Online.net
  3223,   // Voxility
  9009,   // M247
  396982, // Google Cloud
  15133,  // Edgecast/Verizon
  20940,  // Akamai
  54113,  // Fastly
  32934,  // Facebook/Meta
  2906,   // Netflix
  46489,  // Twitch
  55256,  // StackPath
  7018,   // AT&T (backbone, not residential)
  7922,   // Comcast (backbone transit, not retail)
]);

function extractAsnNum(asStr) {
  if (!asStr) return null;
  var m = String(asStr).match(/AS(\d+)/i);
  return m ? parseInt(m[1]) : null;
}

function classifyIP(ipInfo) {
  if (!ipInfo || !ipInfo.query) return 'unknown';

  // 1 — RFC1918 / link-local
  const ip = ipInfo.query;
  if (/^10\./.test(ip) || /^172\.(1[6-9]|2\d|3[01])\./.test(ip) || /^192\.168\./.test(ip)) return 'private';
  if (/^127\./.test(ip) || /^169\.254\./.test(ip)) return 'private';

  // 2 — ip-api boolean flags (present when ?fields= includes them)
  if (ipInfo.hosting === true) return 'datacenter';
  if (ipInfo.proxy  === true) return 'flagged_proxy';

  const org     = (ipInfo.org     || '').toLowerCase();
  const isp     = (ipInfo.isp     || '').toLowerCase();
  const asname  = (ipInfo.asname  || '').toLowerCase();
  const combined = org + ' ' + isp + ' ' + asname;

  // 3 — Datacenter org list (checked before mobile so DC mobile ASNs don't sneak through)
  for (var i = 0; i < DATACENTER_ORGS.length; i++) {
    if (combined.includes(DATACENTER_ORGS[i])) return 'datacenter';
  }

  // 4 — Mobile flag
  if (ipInfo.mobile === true) return 'mobile';

  // 5 — Named residential ISPs (comprehensive international list)
  for (var j = 0; j < RESIDENTIAL_ISPS.length; j++) {
    if (combined.includes(RESIDENTIAL_ISPS[j])) return 'residential';
  }

  // 6 — Keyword heuristics: telecom/cable/broadband patterns not explicitly listed
  for (var k = 0; k < RESIDENTIAL_KEYWORDS.length; k++) {
    if (combined.includes(RESIDENTIAL_KEYWORDS[k])) return 'residential';
  }

  // 7 — If ip-api explicitly says NOT hosting, and it's not a known DC, treat as likely residential
  //     hosting:false means ip-api's own database confirms it's not a datacenter/hosting IP
  if (ipInfo.hosting === false) return 'residential';

  return 'unknown';
}

// ── Absolute speed tier scorer (not relative to batch) ───────────────────────
// Sub-150ms = excellent (1.0), scaling down to >1200ms = very poor (0.03)
// This prevents slow batches from all scoring well via relative normalization.
function speedTierScore(ms) {
  if (!ms || ms <= 0) return 0;
  if (ms <= 150)  return 1.00;
  if (ms <= 300)  return 0.80;
  if (ms <= 500)  return 0.55;
  if (ms <= 800)  return 0.30;
  if (ms <= 1200) return 0.12;
  return 0.03;
}

// ── Proxy parser ──────────────────────────────────────────────────────────────
function parseProxies(raw) {
  const seen = new Set(), out = [];
  for (let line of raw.split(/\r?\n/)) {
    line = line.trim();
    if (!line || line.startsWith('#')) continue;
    let protocol = 'http', username, password, host, port;
    const pm = line.match(/^(https?|socks[45]):\/\//i);
    if (pm) { protocol = pm[1].toLowerCase(); line = line.slice(pm[0].length); }
    if (line.includes('@')) {
      const ai = line.lastIndexOf('@'), creds = line.slice(0, ai), hp = line.slice(ai + 1);
      const ci = creds.indexOf(':');
      username = ci >= 0 ? creds.slice(0, ci) : creds;
      password = ci >= 0 ? creds.slice(ci + 1) : '';
      const hi = hp.lastIndexOf(':');
      host = hp.slice(0, hi); port = parseInt(hp.slice(hi + 1));
    } else {
      const p = line.split(':');
      if (p.length === 4) { host=p[0]; port=parseInt(p[1]); username=p[2]; password=p[3]; }
      else if (p.length === 2) { host=p[0]; port=parseInt(p[1]); }
      else continue;
    }
    if (!host || !port || isNaN(port)) continue;
    const key = proxyKey({ host, port, username, password });
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({ host, port, protocol, username, password });
  }
  return out;
}

// ── Chunked transfer-encoding decoder ────────────────────────────────────────
function decodeChunked(body) {
  var result = '', pos = 0;
  while (pos < body.length) {
    var lineEnd = body.indexOf('\r\n', pos);
    if (lineEnd === -1) break;
    var size = parseInt(body.slice(pos, lineEnd), 16);
    if (isNaN(size) || size === 0) break;
    pos = lineEnd + 2;
    result += body.slice(pos, pos + size);
    pos += size + 2;
  }
  return result || body;
}

// ── TCP CONNECT timing — measures round-trip time through the proxy to target ──
function tcpPingThrough(proxy, host, port, timeoutMs) {
  return new Promise(function(resolve) {
    var start = Date.now();
    var settled = false;
    function done(rtt) { if (!settled) { settled = true; resolve(rtt); } }
    var sock = net.createConnection({ host: proxy.host, port: proxy.port });
    var timer = setTimeout(function() { sock.destroy(); done(null); }, timeoutMs);
    sock.setTimeout(timeoutMs);
    sock.on('timeout', function() { sock.destroy(); clearTimeout(timer); done(null); });
    sock.on('error',   function() { sock.destroy(); clearTimeout(timer); done(null); });
    sock.on('connect', function() {
      var auth = proxy.username
        ? ('Proxy-Authorization: Basic ' + Buffer.from(proxy.username + ':' + proxy.password).toString('base64') + '\r\n')
        : '';
      var req = 'CONNECT ' + host + ':' + port + ' HTTP/1.1\r\nHost: ' + host + ':' + port + '\r\n' + auth + '\r\n';
      sock.write(req);
      var buf = '';
      sock.on('data', function(c) {
        buf += c.toString('binary');
        if (buf.indexOf('\r\n\r\n') === -1) return;
        clearTimeout(timer);
        sock.destroy();
        done(/^HTTP\/1\.[01] 200/.test(buf) ? Date.now() - start : null);
      });
    });
  });
}

// ── Core proxy test — captures response body, bytes used, and PX markers ──────
function testProxyOnce(proxy, testUrl, timeoutMs) {
  return new Promise(function(resolve) {
    const start = Date.now();
    let settled = false;
    function finish(ok, body, bytesSent, bytesReceived) {
      if (settled) return;
      settled = true;
      var px = false;
      if (body) {
        var bl = body.toLowerCase();
        for (var pi = 0; pi < PX_MARKERS.length; pi++) {
          if (bl.includes(PX_MARKERS[pi])) { px = true; break; }
        }
      }
      resolve({ ok, ms: Date.now() - start, body: body || null,
                bytes_sent: bytesSent || 0, bytes_received: bytesReceived || 0,
                px_challenge: px });
    }
    const parsed     = new URL(testUrl);
    const isHttps    = parsed.protocol === 'https:';
    const targetHost = parsed.hostname;
    const targetPort = parseInt(parsed.port) || (isHttps ? 443 : 80);
    const timer = setTimeout(function() { sock.destroy(); finish(false, null, 0, 0); }, timeoutMs);
    const sock  = net.createConnection({ host: proxy.host, port: proxy.port });
    sock.setTimeout(timeoutMs);
    sock.on('timeout', function() { sock.destroy(); clearTimeout(timer); finish(false, null, 0, 0); });
    sock.on('error',   function() { sock.destroy(); clearTimeout(timer); finish(false, null, 0, 0); });
    sock.on('connect', function() {
      var auth = proxy.username
        ? ('Proxy-Authorization: Basic ' + Buffer.from(proxy.username + ':' + proxy.password).toString('base64') + '\r\n')
        : '';
      if (isHttps) {
        var connectReq = 'CONNECT ' + targetHost + ':' + targetPort + ' HTTP/1.1\r\nHost: ' + targetHost + ':' + targetPort + '\r\n' + auth + '\r\n';
        sock.write(connectReq);
        var bytesSentH = Buffer.byteLength(connectReq);
        var bytesReceivedH = 0;
        var buf = '';
        sock.on('data', function(c) {
          buf += c.toString('binary');
          bytesReceivedH += c.length;
          if (buf.indexOf('\r\n\r\n') === -1) return;
          clearTimeout(timer); sock.destroy(); finish(/^HTTP\/1\.[01] 200/.test(buf), null, bytesSentH, bytesReceivedH);
        });
      } else {
        // Use HTTP/1.0 to avoid chunked encoding from the origin server.
        // Proxies that only speak 1.1 will still work — they upgrade internally.
        var getReq = 'GET ' + testUrl + ' HTTP/1.0\r\nHost: ' + targetHost
          + '\r\nUser-Agent: Mozilla/5.0\r\nConnection: close\r\n' + auth + '\r\n';
        sock.write(getReq);
        var bytesSentG = Buffer.byteLength(getReq, 'latin1');
        var chunks = [];
        var bytesReceivedG = 0;
        sock.on('data', function(c) { chunks.push(c); bytesReceivedG += c.length; });
        sock.on('end', function() {
          clearTimeout(timer);
          var raw = Buffer.concat(chunks).toString('latin1');
          var headerEnd = raw.indexOf('\r\n\r\n');
          var statusLine = raw.split('\r\n')[0] || '';
          var m = statusLine.match(/HTTP\/1\.[01] (\d+)/);
          var status = m ? parseInt(m[1]) : 0;
          var headers = headerEnd >= 0 ? raw.slice(0, headerEnd) : '';
          var body    = headerEnd >= 0 ? raw.slice(headerEnd + 4) : '';
          if (/transfer-encoding:\s*chunked/i.test(headers)) body = decodeChunked(body);
          finish(status >= 200 && status < 500, body, bytesSentG, bytesReceivedG);
        });
        sock.on('error', function() { clearTimeout(timer); finish(false, null, bytesSentG, 0); });
      }
    });
  });
}

// ── Parse ip-api response from proxy body ─────────────────────────────────────
function parseIpApiResponse(body) {
  if (!body) return null;
  try {
    var jsonStart = body.indexOf('{');
    var jsonEnd   = body.lastIndexOf('}');
    if (jsonStart === -1 || jsonEnd === -1) return null;
    var data = JSON.parse(body.slice(jsonStart, jsonEnd + 1));
    if (!data || !data.query) return null;
    return data;
  } catch (e) { return null; }
}

async function testProxy(proxy, testUrl, timeoutMs, retries, targetUrl) {
  var ipapiAttempts = [], httpbinAttempts = [], targetAttempts = [];
  var totalBytesSent = 0, totalBytesRecv = 0;

  for (var i = 0; i < retries; i++) {
    // Always run ip-api + httpbin in parallel; optionally add target
    var tasks = [
      testProxyOnce(proxy, testUrl, timeoutMs),      // ip-api
      testProxyOnce(proxy, HTTPBIN_URL, timeoutMs),  // httpbin
    ];
    if (targetUrl) tasks.push(testProxyOnce(proxy, targetUrl, timeoutMs));

    var results = await Promise.all(tasks);
    var ipapi   = results[0];
    var httpbin = results[1];
    var tgt     = targetUrl ? results[2] : null;

    ipapiAttempts.push(ipapi);
    httpbinAttempts.push(httpbin);
    if (tgt) targetAttempts.push(tgt);

    totalBytesSent += (ipapi.bytes_sent||0) + (httpbin.bytes_sent||0) + (tgt ? (tgt.bytes_sent||0) : 0);
    totalBytesRecv += (ipapi.bytes_received||0) + (httpbin.bytes_received||0) + (tgt ? (tgt.bytes_received||0) : 0);
  }

  var goodIpapi   = ipapiAttempts.filter(function(a){ return a.ok; });
  var goodHttpbin = httpbinAttempts.filter(function(a){ return a.ok; });
  var goodTarget  = targetAttempts.filter(function(a){ return a.ok; });

  var ipapiPass   = goodIpapi.length > 0;
  var httpbinPass = goodHttpbin.length > 0;
  var targetTested = targetAttempts.length > 0;
  var targetPass   = targetTested ? goodTarget.length > 0 : null;

  var pxChallenge = ipapiAttempts.concat(httpbinAttempts).concat(targetAttempts)
    .some(function(a){ return a.px_challenge; });
  var targetPx = targetAttempts.some(function(a){ return a.px_challenge; });

  var targetMs = (targetTested && goodTarget.length)
    ? Math.round(goodTarget.reduce(function(s,a){return s+a.ms;},0) / goodTarget.length) : null;

  // Use ip-api latency as the primary timing signal
  var allGood = goodIpapi.concat(goodHttpbin);
  if (!allGood.length) {
    return { host:proxy.host, port:proxy.port, protocol:proxy.protocol,
             username:proxy.username, password:proxy.password,
             status:'fail', avg_ms:null, min_ms:null, success_rate:0, score:0,
             egress_ip:null, ip_info:null, ip_type:'unknown',
             httpbin_pass:false, target_pass:targetPass, target_ms:targetMs, target_px:targetPx,
             bytes_sent:totalBytesSent, bytes_received:totalBytesRecv,
             px_challenge:pxChallenge, edge_rtt:null };
  }

  var lats = goodIpapi.length ? goodIpapi.map(function(a){return a.ms;}) : goodHttpbin.map(function(a){return a.ms;});
  var avg  = lats.reduce(function(a,b){return a+b;},0) / lats.length;
  var min  = Math.min.apply(null, lats);
  var rate = goodIpapi.length / retries;

  var ipInfo = null;
  for (var j = goodIpapi.length - 1; j >= 0; j--) {
    ipInfo = parseIpApiResponse(goodIpapi[j].body);
    if (ipInfo) break;
  }
  var ipType = classifyIP(ipInfo);
  var asnNum = ipInfo ? extractAsnNum(ipInfo.as) : null;
  var isDcAsn = asnNum !== null && DATACENTER_ASN_NUMS.has(asnNum);

  // TCP edge RTT (async, non-blocking — fires after main tests)
  var edgeRtt = null;
  if (targetUrl) {
    try {
      var parsed = new URL(targetUrl);
      var edgeHost = parsed.hostname;
      var edgePort = parsed.port ? parseInt(parsed.port) : (parsed.protocol === 'https:' ? 443 : 80);
      edgeRtt = await tcpPingThrough(proxy, edgeHost, edgePort, Math.min(timeoutMs, 5000));
    } catch(e) { edgeRtt = null; }
  }

  // Fork additive scoring formula
  var score = 0;
  if (targetTested && targetPass)  score += 50;
  if (ipapiPass)                   score += 15;
  if (httpbinPass)                 score += 10;
  if (pxChallenge)                 score -= 30;
  if (ipType === 'datacenter')     score -= 25;
  if (isDcAsn)                     score -= 20;
  // Latency bonus: +10 under 300ms, -10 over 800ms
  if (avg <= 300)      score += 10;
  else if (avg <= 500) score += 5;
  else if (avg > 800)  score -= 10;
  // Consistency bonus
  if (rate >= 1.0)     score += 5;
  else if (rate < 0.5) score -= 10;
  // Clamp to 0..100
  score = Math.max(0, Math.min(100, score));

  return {
    host:proxy.host, port:proxy.port, protocol:proxy.protocol,
    username:proxy.username, password:proxy.password,
    status:'pass', avg_ms:Math.round(avg), min_ms:Math.round(min),
    success_rate:Math.round(rate*1000)/1000,
    score,
    egress_ip:  ipInfo ? ipInfo.query : null,
    ip_info:    ipInfo ? { isp:ipInfo.isp, org:ipInfo.org, as:ipInfo.as,
                           asname:ipInfo.asname,
                           city:ipInfo.city, region:ipInfo.regionName,
                           country:ipInfo.country, countryCode:ipInfo.countryCode,
                           hosting:ipInfo.hosting,
                           proxy:ipInfo.proxy, mobile:ipInfo.mobile } : null,
    ip_type: ipType,
    httpbin_pass: httpbinPass,
    target_pass: targetPass, target_ms: targetMs, target_px: targetPx,
    bytes_sent: totalBytesSent, bytes_received: totalBytesRecv,
    px_challenge: pxChallenge, edge_rtt: edgeRtt,
  };
}

// ── Worker pool ───────────────────────────────────────────────────────────────
async function runJob(jobId, proxies, config) {
  const job = jobs[jobId];
  job.status = 'running'; job.startTime = Date.now();
  const total = proxies.length, results = new Array(total);
  const conc  = Math.min(config.concurrency, total);
  let queue = 0, completed = 0;
  async function worker() {
    while (true) {
      const i = queue++;
      if (i >= total) break;
      const r = await testProxy(proxies[i], config.test_url, config.timeout * 1000, config.retries, config.target_url || '');
      results[i] = r; completed++;
      job.tested = completed;
      if (r.status === 'pass') job.passed++; else job.failed++;
      if (r.px_challenge) job.px_challenge_count = (job.px_challenge_count || 0) + 1;
      if (r.httpbin_pass !== undefined) {
        job.httpbin_tested = (job.httpbin_tested || 0) + 1;
        if (r.httpbin_pass) job.httpbin_passed = (job.httpbin_passed || 0) + 1;
      }
      if (r.target_pass !== null && r.target_pass !== undefined) {
        job.target_tested = (job.target_tested || 0) + 1;
        if (r.target_pass) job.target_passed = (job.target_passed || 0) + 1;
      }
      job.bytes_sent     = (job.bytes_sent     || 0) + (r.bytes_sent     || 0);
      job.bytes_received = (job.bytes_received || 0) + (r.bytes_received || 0);
      job.progress_pct = Math.round(completed/total*1000)/10;
      const el = (Date.now()-job.startTime)/1000;
      job.elapsed_sec = Math.round(el*10)/10;
      job.eta_sec = Math.round((total-completed)/(completed/(el||0.001)));
    }
  }
  await Promise.all(Array.from({ length: conc }, function() { return worker(); }));

  var totalBytes = (job.bytes_sent || 0) + (job.bytes_received || 0);
  job.data_usage = {
    bytes_sent:          job.bytes_sent || 0,
    bytes_received:      job.bytes_received || 0,
    total_bytes:         totalBytes,
    avg_bytes_per_proxy: completed > 0 ? Math.round(totalBytes / completed) : 0,
    px_challenge_count:  job.px_challenge_count || 0,
    px_challenge_pct:    completed > 0 ? Math.round((job.px_challenge_count || 0) / completed * 1000) / 10 : 0,
    httpbin_tested:      job.httpbin_tested  || 0,
    httpbin_passed:      job.httpbin_passed  || 0,
    httpbin_pass_pct:    job.httpbin_tested  > 0 ? Math.round((job.httpbin_passed || 0) / job.httpbin_tested * 1000) / 10 : null,
    target_tested:       job.target_tested  || 0,
    target_passed:       job.target_passed  || 0,
    target_pass_pct:     job.target_tested  > 0 ? Math.round((job.target_passed || 0) / job.target_tested * 1000) / 10 : null,
  };

  const passed = results.filter(function(r){ return r && r.status==='pass'; });

  // Build IP analysis for this run
  job.ip_analysis = analyzeEgressIPs(passed);

  // Sort: residential first, then by speed. Datacenter/flagged go to bottom.
  passed.sort(function(a, b) {
    var typeOrder = { residential:0, mobile:1, unknown:2, flagged_proxy:3, datacenter:4, private:5 };
    var ao = typeOrder[a.ip_type] !== undefined ? typeOrder[a.ip_type] : 2;
    var bo = typeOrder[b.ip_type] !== undefined ? typeOrder[b.ip_type] : 2;
    if (ao !== bo) return ao - bo;
    return a.avg_ms - b.avg_ms;
  });

  job.top_proxies = passed.slice(0, config.top_n);
  job.status = 'done'; job.progress_pct = 100; job.eta_sec = 0;
  if (job.session_id && sessions[job.session_id]) mergeRunIntoSession(job.session_id, jobId, results);
  fs.mkdirSync(path.join(DATA_DIR,'results'),{recursive:true});
  fs.writeFileSync(path.join(__dirname,'results',jobId+'.json'),
    JSON.stringify({job_id:jobId,config,completed_at:new Date().toISOString(),
      stats:{total,passed:job.passed,failed:job.failed},
      data_usage:job.data_usage,
      ip_analysis:job.ip_analysis,top_proxies:job.top_proxies},null,2));
}

// ── Egress IP analysis ────────────────────────────────────────────────────────
function analyzeEgressIPs(passed) {
  var ipCounts = {}, asnCounts = {}, ispCounts = {}, typeCounts = {};
  var totalWithIP = 0, datacenterCount = 0, privateCount = 0, flaggedCount = 0,
      residentialCount = 0, mobileCount = 0, unknownCount = 0;

  passed.forEach(function(r) {
    var t = r.ip_type || 'unknown';
    typeCounts[t] = (typeCounts[t] || 0) + 1;
    if (t === 'datacenter') datacenterCount++;
    else if (t === 'private') privateCount++;
    else if (t === 'flagged_proxy') flaggedCount++;
    else if (t === 'residential') residentialCount++;
    else if (t === 'mobile') mobileCount++;
    else unknownCount++;

    if (r.egress_ip) {
      totalWithIP++;
      ipCounts[r.egress_ip] = (ipCounts[r.egress_ip] || 0) + 1;
      if (r.ip_info) {
        var asn = r.ip_info.as || 'Unknown';
        asnCounts[asn] = (asnCounts[asn] || 0) + 1;
        var isp = r.ip_info.isp || 'Unknown';
        ispCounts[isp] = (ispCounts[isp] || 0) + 1;
      }
    }
  });

  // Top reused IPs
  var reusedIPs = Object.keys(ipCounts)
    .filter(function(ip){ return ipCounts[ip] > 1; })
    .sort(function(a,b){ return ipCounts[b] - ipCounts[a]; })
    .slice(0, 20)
    .map(function(ip){ return { ip, count: ipCounts[ip] }; });

  // Top ASNs
  var topASNs = Object.keys(asnCounts)
    .sort(function(a,b){ return asnCounts[b] - asnCounts[a]; })
    .slice(0, 10)
    .map(function(asn){ return { asn, count: asnCounts[asn] }; });

  // Top ISPs
  var topISPs = Object.keys(ispCounts)
    .sort(function(a,b){ return ispCounts[b] - ispCounts[a]; })
    .slice(0, 10)
    .map(function(isp){ return { isp, count: ispCounts[isp] }; });

  var uniqueIPs    = Object.keys(ipCounts).length;
  var reuseRate    = totalWithIP > 0 ? Math.round((1 - uniqueIPs / totalWithIP) * 1000) / 10 : 0;
  var resRate      = passed.length > 0 ? Math.round(residentialCount / passed.length * 1000) / 10 : 0;
  var dcRate       = passed.length > 0 ? Math.round(datacenterCount  / passed.length * 1000) / 10 : 0;

  // Pool quality score 0-100
  var qualityScore = Math.round(
    resRate * 0.4 +                              // residential % (max 40)
    Math.max(0, (100 - reuseRate)) * 0.3 +       // uniqueness (max 30)
    Math.max(0, (100 - dcRate * 5)) * 0.2 +      // low datacenter % (max 20)
    (topASNs.length >= 5 ? 10 : topASNs.length * 2) // ASN diversity (max 10)
  );

  return {
    total_passed:      passed.length,
    total_with_ip:     totalWithIP,
    unique_ips:        uniqueIPs,
    isp_diversity:     Object.keys(ispCounts).length,
    reuse_rate_pct:    reuseRate,
    residential_count: residentialCount,
    residential_pct:   resRate,
    datacenter_count:  datacenterCount,
    datacenter_pct:    dcRate,
    mobile_count:      mobileCount,
    flagged_count:     flaggedCount,
    private_count:     privateCount,
    unknown_count:     unknownCount,
    quality_score:     qualityScore,
    top_reused_ips:    reusedIPs,
    top_asns:          topASNs,
    top_isps:          topISPs,
  };
}

// ── Session merge ─────────────────────────────────────────────────────────────
function mergeRunIntoSession(sessionId, jobId, results) {
  const s = sessions[sessionId];
  if (!s) return;
  s.run_ids.push(jobId);
  s.run_count = s.run_ids.length;

  results.forEach(function(r) {
    if (!r) return;
    const k = proxyKey(r);
    if (!s.proxy_history[k]) s.proxy_history[k] = { proxy: r, runs: [] };
    s.proxy_history[k].runs.push({
      run_num: s.run_count, job_id: jobId,
      status: r.status, avg_ms: r.avg_ms, min_ms: r.min_ms,
      success_rate: r.success_rate, score: r.score,
      egress_ip: r.egress_ip, ip_type: r.ip_type,
      ip_info: r.ip_info,
    });
  });

  const analyzed = [];
  Object.keys(s.proxy_history).forEach(function(k) {
    const entry = s.proxy_history[k];
    const runs  = entry.runs;
    if (runs.length < s.run_count) return;
    const passes = runs.filter(function(r){ return r.status === 'pass'; }).length;
    const required = s.config.min_pass_runs !== undefined ? s.config.min_pass_runs : s.run_count;
    if (passes < required) return;

    const passRuns   = runs.filter(function(r){ return r.status === 'pass'; });
    const lats       = passRuns.map(function(r){ return r.avg_ms || 9999; });
    const scores     = passRuns.map(function(r){ return r.score  || 0; });
    const mean_lat   = Math.round(lats.reduce(function(a,b){return a+b;},0) / lats.length);
    const min_lat    = Math.min.apply(null, passRuns.filter(function(r){return r.min_ms!=null;}).map(function(r){return r.min_ms;}));
    const mean_score = Math.round(scores.reduce(function(a,b){return a+b;},0) / scores.length * 10000) / 10000;

    // Determine dominant IP type across runs
    var typeCounts = {};
    passRuns.forEach(function(r){ var t=r.ip_type||'unknown'; typeCounts[t]=(typeCounts[t]||0)+1; });
    var ip_type = Object.keys(typeCounts).sort(function(a,b){return typeCounts[b]-typeCounts[a];})[0] || 'unknown';

    analyzed.push({
      proxy: entry.proxy,
      run_count: s.run_count,
      pass_count: passes,
      pass_pct: Math.round(passes / s.run_count * 100),
      mean_lat,
      min_lat: isFinite(min_lat) ? min_lat : null,
      mean_score,
      ip_type,
      // Latest egress IP seen
      egress_ip: passRuns[passRuns.length-1].egress_ip || null,
      ip_info:   passRuns[passRuns.length-1].ip_info || null,
      lat_history:   runs.map(function(r){ return r.avg_ms; }),
      score_history: runs.map(function(r){ return r.score ? Math.round(r.score*100)/100 : 0; }),
    });
  });

  // Sort: residential first, then by latency
  analyzed.sort(function(a, b) {
    var typeOrder = { residential:0, mobile:1, unknown:2, flagged_proxy:3, datacenter:4, private:5 };
    var ao = typeOrder[a.ip_type] !== undefined ? typeOrder[a.ip_type] : 2;
    var bo = typeOrder[b.ip_type] !== undefined ? typeOrder[b.ip_type] : 2;
    if (ao !== bo) return ao - bo;
    if (b.pass_count !== a.pass_count) return b.pass_count - a.pass_count;
    return a.mean_lat - b.mean_lat;
  });

  s.analyzed     = analyzed;
  s.best_proxies = analyzed.slice(0, s.config.top_n);
  s.status       = 'idle';
  s.updated_at   = new Date().toISOString();
  s.analytics    = computeSessionAnalytics(s);
  // Aggregate IP analysis across all runs
  s.ip_analysis  = analyzeEgressIPs(
    analyzed.map(function(a){ return { ip_type:a.ip_type, egress_ip:a.egress_ip, ip_info:a.ip_info, avg_ms:a.mean_lat }; })
  );

  fs.mkdirSync(path.join(DATA_DIR,'results'),{recursive:true});
  fs.writeFileSync(path.join(__dirname,'results','session_'+sessionId+'.json'),
    JSON.stringify({session_id:sessionId,config:s.config,run_count:s.run_count,
      run_ids:s.run_ids,updated_at:s.updated_at,ip_analysis:s.ip_analysis,
      best_proxies:s.best_proxies},null,2));
}

// ── Session analytics — auto-computed after every run ────────────────────────
function computeSessionAnalytics(s) {
  var history  = s.proxy_history || {};
  var analyzed = s.analyzed || [];
  var runCount = s.run_count || 1;

  // Build egress IP -> credential count map from ALL pass runs in history.
  // Multiple credentials resolving to the same egress IP = crowded/thin pool.
  var ipToCredCount = {};
  Object.keys(history).forEach(function(k) {
    history[k].runs.forEach(function(r) {
      if (r.status === 'pass' && r.egress_ip) {
        ipToCredCount[r.egress_ip] = (ipToCredCount[r.egress_ip] || 0) + 1;
      }
    });
  });

  // ISP speed profile (from proxies that passed)
  var ispMap = {};
  analyzed.forEach(function(a) {
    var isp = (a.ip_info && a.ip_info.isp) ? a.ip_info.isp : 'Unknown';
    if (!ispMap[isp]) ispMap[isp] = { count:0, total_ms:0, pass_total:0, type_counts:{} };
    ispMap[isp].count++;
    ispMap[isp].total_ms += a.mean_lat || 0;
    ispMap[isp].pass_total += a.pass_count;
    var t = a.ip_type || 'unknown';
    ispMap[isp].type_counts[t] = (ispMap[isp].type_counts[t] || 0) + 1;
  });
  var total = analyzed.length || 1;
  var ispSpeeds = Object.keys(ispMap).map(function(isp) {
    var m = ispMap[isp];
    var domType = Object.keys(m.type_counts).sort(function(a,b){return m.type_counts[b]-m.type_counts[a];})[0]||'unknown';
    return { isp:isp, count:m.count, pool_pct:Math.round(m.count/total*100),
             avg_ms:Math.round(m.total_ms/m.count),
             consistency_pct:Math.round(m.pass_total/(m.count*runCount)*100),
             dominant_type:domType };
  }).sort(function(a,b){return a.avg_ms-b.avg_ms;}).slice(0,25);

  // Composite score per proxy — absolute speed tiers, tougher consistency curve.
  // Mobile and DC are excluded (they shouldn't appear in the best-proxy list).
  // Formula: speed 40% (absolute tiers) + consistency 30% (exponential) + uniqueness 20% + type 10%
  var TYPE_SCORE = { residential:1.0, unknown:0.5, flagged_proxy:0.1, datacenter:0, private:0 };

  var compositeRanked = analyzed
    .filter(function(a){
      return a.ip_type!=='datacenter' && a.ip_type!=='private' &&
             a.ip_type!=='mobile'     && a.ip_type!=='flagged_proxy';
    })
    .map(function(a) {
      var consistency = a.pass_count / Math.max(a.run_count, 1);
      var spd         = speedTierScore(a.mean_lat);
      var crowding    = a.egress_ip ? (ipToCredCount[a.egress_ip]||1) : 1;
      var uniqueScore = crowding===1?1.0:crowding<=2?0.7:crowding<=5?0.4:0.1;
      var tScore      = TYPE_SCORE[a.ip_type]!==undefined ? TYPE_SCORE[a.ip_type] : 0.5;
      // Math.pow(consistency, 2.2): 100%→1.0, 80%→0.61, 67%→0.40, 50%→0.22
      var composite   = Math.round(Math.pow(consistency,2.2)*30 + spd*40 + uniqueScore*20 + tScore*10);
      return { proxy:a.proxy, egress_ip:a.egress_ip, ip_type:a.ip_type,
               isp:a.ip_info?a.ip_info.isp:null,
               asn:a.ip_info?a.ip_info.as:null,
               city:a.ip_info?a.ip_info.city:null,
               country:a.ip_info?a.ip_info.country:null,
               mean_lat:a.mean_lat,
               pass_count:a.pass_count, run_count:a.run_count,
               consistency_pct:Math.round(consistency*100),
               shared_by:crowding, composite_score:composite };
    })
    .sort(function(a,b){return b.composite_score-a.composite_score;});

  // Crowded IPs report (IPs shared by 2+ credentials)
  var crowdedIPs = Object.keys(ipToCredCount)
    .filter(function(ip){return ipToCredCount[ip]>1;})
    .map(function(ip) {
      var match = analyzed.find(function(a){return a.egress_ip===ip;});
      return { ip:ip, shared_by:ipToCredCount[ip],
               isp:match&&match.ip_info?match.ip_info.isp:null,
               avg_ms:match?match.mean_lat:null,
               ip_type:match?match.ip_type:null };
    })
    .sort(function(a,b){return b.shared_by-a.shared_by;})
    .slice(0,40);

  var uniqueCount = Object.keys(ipToCredCount).filter(function(ip){return ipToCredCount[ip]===1;}).length;

  return {
    computed_at:        new Date().toISOString(),
    total_analyzed:     analyzed.length,
    unique_egress_ips:  Object.keys(ipToCredCount).length,
    uncrowded_count:    uniqueCount,
    crowded_count:      crowdedIPs.length,
    isp_speeds:         ispSpeeds,
    crowded_ips:        crowdedIPs,
    composite_ranked:   compositeRanked.slice(0, 1000),
  };
}

// ── Diversity caps — limit how many proxies come from any single ASN or city ──
// Works on both raw job results (p.ip_info) and composite_ranked entries (p.asn/p.city)
function applyDiversityCaps(list, maxPerAsn, maxPerCity) {
  if (!maxPerAsn && !maxPerCity) return list;
  var asnCounts = {}, cityCounts = {};
  return list.filter(function(p) {
    var info = p.ip_info || null;
    var asn  = info ? (info.as  || '__uk_asn')  : (p.asn   || '__uk_asn');
    var city = info
      ? ((info.city && info.country) ? (info.city + ',' + info.country) : '__uk_city')
      : ((p.city    && p.country)    ? (p.city   + ',' + p.country)    : '__uk_city');
    if (maxPerAsn)  { asnCounts[asn]   = (asnCounts[asn]   || 0) + 1; if (asnCounts[asn]   > maxPerAsn)  return false; }
    if (maxPerCity) { cityCounts[city] = (cityCounts[city] || 0) + 1; if (cityCounts[city] > maxPerCity) return false; }
    return true;
  });
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────
function readBody(req) {
  return new Promise(function(resolve,reject){
    const c=[];
    req.on('data',function(d){c.push(d);}); req.on('end',function(){resolve(Buffer.concat(c));}); req.on('error',reject);
  });
}

function parseMultipart(buf, boundary) {
  const fields={}, bnd=Buffer.from('--'+boundary); let pos=0;
  while (pos<buf.length) {
    const start=buf.indexOf(bnd,pos); if(start===-1)break;
    pos=start+bnd.length+2;
    const he=buf.indexOf('\r\n\r\n',pos); if(he===-1)break;
    const hdr=buf.slice(pos,he).toString(); pos=he+4;
    const nb=buf.indexOf(bnd,pos), de=nb===-1?buf.length:nb-2;
    const data=buf.slice(pos,de); pos=de+2;
    const nm=hdr.match(/name="([^"]+)"/), fn=hdr.match(/filename="([^"]+)"/);
    if(!nm)continue;
    fields[nm[1]]=fn?{filename:fn[1],data}:data.toString().trim();
  }
  return fields;
}

function genId() { return Math.random().toString(36).slice(2,10); }
function jsonRes(res,data,status){ res.writeHead(status||200,{'Content-Type':'application/json'}); res.end(JSON.stringify(data)); }
function proxyLine(p){ return p.username?(p.host+':'+p.port+':'+p.username+':'+p.password):(p.host+':'+p.port); }
function providerNameFromInfo(ipInfo) {
  if (!ipInfo) return 'Unknown ISP';
  return ipInfo.isp || ipInfo.org || ipInfo.as || 'Unknown ISP';
}

function providerNameFromEntry(entry) {
  return providerNameFromInfo(entry && entry.ip_info ? entry.ip_info : null);
}

function buildProviderBreakdown(entries, options) {
  options = options || {};
  var items = Array.isArray(entries) ? entries : [];
  var runCount = options.run_count || 1;
  var typeScore = { residential:1.0, mobile:0.8, unknown:0.5, flagged_proxy:0.15, datacenter:0, private:0 };
  var withLatency = items.map(function(item) { return item.mean_lat != null ? item.mean_lat : item.avg_ms; }).filter(function(ms) {
    return typeof ms === 'number' && isFinite(ms);
  });
  var minMs = withLatency.length ? Math.min.apply(null, withLatency) : 0;
  var maxMs = withLatency.length ? Math.max.apply(null, withLatency) : 5000;
  var msRange = Math.max(1, maxMs - minMs);
  var grouped = {};

  items.forEach(function(item) {
    var provider = providerNameFromEntry(item);
    if (!grouped[provider]) grouped[provider] = [];
    grouped[provider].push(item);
  });

  return Object.keys(grouped).map(function(provider) {
    var group = grouped[provider];
    var count = group.length;
    var latencies = group.map(function(item) { return item.mean_lat != null ? item.mean_lat : item.avg_ms; }).filter(function(ms) {
      return typeof ms === 'number' && isFinite(ms);
    });
    var avgMs = latencies.length
      ? Math.round(latencies.reduce(function(sum, ms) { return sum + ms; }, 0) / latencies.length)
      : null;
    var consistencyList = group.map(function(item) {
      if (item.pass_count != null && item.run_count != null) return item.pass_count / Math.max(item.run_count, 1);
      if (item.success_rate != null) return item.success_rate;
      return item.status === 'pass' ? 1 : 0;
    });
    var consistencyPct = Math.round(consistencyList.reduce(function(sum, value) { return sum + value; }, 0) / Math.max(consistencyList.length, 1) * 100);
    var ipCounts = {}, typeCounts = {}, residential = 0, datacenter = 0;

    group.forEach(function(item) {
      var ip = item.egress_ip || '';
      var type = item.ip_type || 'unknown';
      typeCounts[type] = (typeCounts[type] || 0) + 1;
      if (type === 'residential') residential++;
      if (type === 'datacenter') datacenter++;
      if (ip) ipCounts[ip] = (ipCounts[ip] || 0) + 1;
    });

    var withIp = Object.keys(ipCounts).reduce(function(sum, ip) { return sum + ipCounts[ip]; }, 0);
    var uniqueIps = Object.keys(ipCounts).length;
    var uniquenessPct = withIp > 0 ? Math.round(uniqueIps / withIp * 100) : 0;
    var dominantType = Object.keys(typeCounts).sort(function(a, b) { return typeCounts[b] - typeCounts[a]; })[0] || 'unknown';
    var speedScore = avgMs == null ? 0 : Math.max(0, 1 - ((avgMs - minMs) / msRange));
    var residentialPct = Math.round(residential / Math.max(count, 1) * 100);
    var datacenterPct = Math.round(datacenter / Math.max(count, 1) * 100);
    var compositeScore = Math.round(
      consistencyPct * 0.35 +
      speedScore * 30 +
      uniquenessPct * 0.15 +
      residentialPct * 0.15 +
      Math.max(0, 100 - datacenterPct * 2) * 0.05
    );

    return {
      provider: provider,
      count: count,
      avg_ms: avgMs,
      consistency_pct: consistencyPct,
      unique_ips: uniqueIps,
      uniqueness_pct: uniquenessPct,
      residential_pct: residentialPct,
      datacenter_pct: datacenterPct,
      dominant_type: dominantType,
      provider_score: Math.max(0, Math.min(100, compositeScore)),
      run_count: runCount,
      top_proxies: group.slice().sort(function(a, b) {
        var aConsistency = a.pass_count != null && a.run_count != null ? a.pass_count / Math.max(a.run_count, 1) : (a.success_rate != null ? a.success_rate : 0);
        var bConsistency = b.pass_count != null && b.run_count != null ? b.pass_count / Math.max(b.run_count, 1) : (b.success_rate != null ? b.success_rate : 0);
        if (bConsistency !== aConsistency) return bConsistency - aConsistency;
        var aTypeScore = typeScore[a.ip_type] != null ? typeScore[a.ip_type] : 0.5;
        var bTypeScore = typeScore[b.ip_type] != null ? typeScore[b.ip_type] : 0.5;
        if (bTypeScore !== aTypeScore) return bTypeScore - aTypeScore;
        var aMs = a.mean_lat != null ? a.mean_lat : (a.avg_ms != null ? a.avg_ms : 999999);
        var bMs = b.mean_lat != null ? b.mean_lat : (b.avg_ms != null ? b.avg_ms : 999999);
        return aMs - bMs;
      }).slice(0, 25).map(function(item) {
        return {
          proxy: item.proxy || {
            host: item.host,
            port: item.port,
            protocol: item.protocol,
            username: item.username,
            password: item.password
          },
          egress_ip: item.egress_ip || null,
          ip_type: item.ip_type || 'unknown',
          avg_ms: item.mean_lat != null ? item.mean_lat : item.avg_ms,
          consistency_pct: item.pass_count != null && item.run_count != null
            ? Math.round(item.pass_count / Math.max(item.run_count, 1) * 100)
            : Math.round((item.success_rate || 0) * 100),
          pass_count: item.pass_count != null ? item.pass_count : null,
          run_count: item.run_count != null ? item.run_count : null
        };
      })
    };
  }).sort(function(a, b) {
    if (b.provider_score !== a.provider_score) return b.provider_score - a.provider_score;
    if (b.count !== a.count) return b.count - a.count;
    var aMs = a.avg_ms != null ? a.avg_ms : 999999;
    var bMs = b.avg_ms != null ? b.avg_ms : 999999;
    return aMs - bMs;
  });
}

function buildSessionDebugPrompt(sessionId, session) {
  var analytics = session.analytics || computeSessionAnalytics(session);
  var providerBreakdown = buildProviderBreakdown(session.analyzed || [], { run_count: session.run_count || 1 }).slice(0, 25);
  return [
    'You are analyzing a residential proxy test session.',
    '',
    'Goal:',
    'Identify which ISP/provider groups and which individual proxy credentials are best for high-quality, fast, and consistent egress IPs. Prefer residential or mobile IPs, low latency, high pass consistency across runs, unique egress IPs, and low crowding. Penalize datacenter, private, or flagged-proxy outcomes and heavily reused egress IPs.',
    '',
    'How to interpret the metrics:',
    '- pass_count / run_count and consistency_pct: reliability across repeated runs.',
    '- mean_lat and min_lat: speed, where lower is better.',
    '- ip_type: residential and mobile are preferred; datacenter, private, and flagged_proxy are risky.',
    '- egress_ip and crowded/shared_by indicators: unique IPs are better than crowded pools.',
    '- provider_score: aggregate provider score derived from consistency, speed, uniqueness, residential share, and datacenter avoidance.',
    '- composite_ranked: best individual credentials after consistency, speed, uniqueness, and IP type weighting.',
    '',
    'Tasks:',
    '1. Rank the best providers for scaling campaigns that need clean, stable residential IPs.',
    '2. Identify the strongest individual proxies or proxy cohorts to keep.',
    '3. Flag risky providers, crowded pools, suspicious datacenter contamination, or inconsistent results.',
    '4. Recommend a practical export strategy for the best 1000 proxies.',
    '5. Call out any anomalies that deserve retesting.',
    '',
    'SESSION SUMMARY',
    JSON.stringify({
      session_id: sessionId,
      name: session.name,
      run_count: session.run_count,
      proxy_count: session.proxies ? session.proxies.length : 0,
      analyzed_count: session.analyzed ? session.analyzed.length : 0,
      updated_at: session.updated_at
    }, null, 2),
    '',
    'IP ANALYSIS',
    JSON.stringify(session.ip_analysis || {}, null, 2),
    '',
    'SESSION ANALYTICS',
    JSON.stringify(analytics, null, 2),
    '',
    'PROVIDER BREAKDOWN',
    JSON.stringify(providerBreakdown, null, 2),
    '',
    'ALL ANALYZED PROXIES',
    JSON.stringify(session.analyzed || [], null, 2)
  ].join('\n');
}

function buildJobDebugPrompt(jobId, job) {
  var providerBreakdown = buildProviderBreakdown(job.top_proxies || [], { run_count: 1 }).slice(0, 25);
  return [
    'You are analyzing a single-run proxy test batch.',
    '',
    'Goal:',
    'Rank ISP/provider quality and identify the best proxy credentials to keep based on pass rate, low latency, clean IP type, and unique egress IP behavior. Residential and mobile IPs are preferred. Datacenter, private, and flagged proxy outcomes should be treated as lower quality.',
    '',
    'How to interpret the metrics:',
    '- success_rate: reliability in the current batch.',
    '- avg_ms and min_ms: speed, where lower is better.',
    '- score: proxy-level batch score from success rate and speed.',
    '- ip_type and ip_info: classify the quality and likely source of the egress IP.',
    '- provider_score: aggregate provider score derived from consistency, speed, uniqueness, residential share, and datacenter avoidance.',
    '',
    'Tasks:',
    '1. Rank the best providers for clean residential proxy use.',
    '2. Identify the best individual proxies to export first.',
    '3. Flag suspicious providers, crowding, or datacenter leakage.',
    '4. Recommend whether this pool is good enough to keep or should be retested.',
    '',
    'JOB SUMMARY',
    JSON.stringify({
      job_id: jobId,
      list_name: job.list_name,
      total_tested: job.total,
      passed: job.passed,
      failed: job.failed,
      completed: job.status === 'done'
    }, null, 2),
    '',
    'IP ANALYSIS',
    JSON.stringify(job.ip_analysis || {}, null, 2),
    '',
    'PROVIDER BREAKDOWN',
    JSON.stringify(providerBreakdown, null, 2),
    '',
    'TOP PROXIES',
    JSON.stringify(job.top_proxies || [], null, 2)
  ].join('\n');
}

// ── Router ────────────────────────────────────────────────────────────────────
const server = http.createServer(async function(req,res){
  const parsed=url.parse(req.url,true), pathname=parsed.pathname, method=req.method;
  res.setHeader('Access-Control-Allow-Origin','*');
  res.setHeader('Access-Control-Allow-Methods','GET,POST,PUT,DELETE,OPTIONS');
  res.setHeader('Access-Control-Allow-Headers','Content-Type');
  if(method==='OPTIONS'){res.writeHead(204);res.end();return;}

  if(pathname==='/api/version') return jsonRes(res,{version:APP_VERSION});

  // SSE real-time progress stream
  if(pathname==='/api/events'&&method==='GET'){
    res.writeHead(200,{
      'Content-Type':'text/event-stream',
      'Cache-Control':'no-cache',
      'Connection':'keep-alive',
      'Access-Control-Allow-Origin':'*',
    });
    function sendEvent(){
      var snapshot=Object.values(jobs).map(function(j){
        return{job_id:j.job_id,status:j.status,total:j.total,tested:j.tested,
               passed:j.passed,failed:j.failed,progress_pct:j.progress_pct,
               elapsed_sec:j.elapsed_sec,eta_sec:j.eta_sec,
               list_name:j.list_name,session_id:j.session_id||null};
      });
      res.write('data: '+JSON.stringify(snapshot)+'\n\n');
    }
    sendEvent();
    var iv=setInterval(sendEvent,500);
    req.on('close',function(){clearInterval(iv);});
    return;
  }

  // Data usage estimator — calibrated from recent completed jobs
  if(pathname==='/api/estimate'&&method==='GET'){
    var estN=parseInt(parsed.query.proxies||'1000');
    if(isNaN(estN)||estN<1) estN=1000;
    var recentJobs=Object.values(jobs)
      .filter(function(j){return j.status==='done'&&j.data_usage&&j.data_usage.avg_bytes_per_proxy>0;})
      .sort(function(a,b){return (b.startTime||0)-(a.startTime||0);})
      .slice(0,5);
    if(!recentJobs.length) return jsonRes(res,{error:'No completed tests yet — run at least one test to calibrate the estimate.'},400);
    var avgBytesPerProxy=Math.round(
      recentJobs.reduce(function(s,j){return s+j.data_usage.avg_bytes_per_proxy;},0)/recentJobs.length
    );
    var estimatedBytes=avgBytesPerProxy*estN;
    return jsonRes(res,{
      proxy_count:          estN,
      avg_bytes_per_proxy:  avgBytesPerProxy,
      estimated_bytes:      estimatedBytes,
      estimated_kb:         Math.round(estimatedBytes/1024),
      estimated_mb:         Math.round(estimatedBytes/1024/1024*100)/100,
      calibrated_from:      recentJobs.length,
    });
  }

  // Jobs list
  if(pathname==='/api/jobs'&&method==='GET')
    return jsonRes(res,Object.values(jobs).map(function(j){
      return{job_id:j.job_id,status:j.status,total:j.total,tested:j.tested,
             passed:j.passed,failed:j.failed,progress_pct:j.progress_pct,
             elapsed_sec:j.elapsed_sec,eta_sec:j.eta_sec,
             list_name:j.list_name,session_id:j.session_id||null,
             px_challenge_count:j.px_challenge_count||0,
             httpbin_tested:j.httpbin_tested||0,httpbin_passed:j.httpbin_passed||0,
             target_tested:j.target_tested||0,target_passed:j.target_passed||0,
             data_usage:j.data_usage||null};}));

  // Create job
  if(pathname==='/api/jobs'&&method==='POST'){
    const body=await readBody(req), ct=req.headers['content-type']||'', bm=ct.match(/boundary=(.+)/);
    if(!bm) return jsonRes(res,{error:'Bad content-type'},400);
    const fields=parseMultipart(body,bm[1].trim());
    if(!fields.file) return jsonRes(res,{error:'No file field'},400);
    const proxies=parseProxies(fields.file.data.toString('utf-8'));
    if(!proxies.length) return jsonRes(res,{error:'No valid proxies found'},400);
    const config={test_url:fields.test_url||'http://ip-api.com/json',
                  target_url:(fields.target_url||'').trim(),
                  concurrency:parseInt(fields.concurrency||'150'),
                  timeout:parseFloat(fields.timeout||'10'),
                  retries:parseInt(fields.retries||'1'),
                  top_n:parseInt(fields.top_n||'1000')};
    const jobId=genId(), sid=fields.session_id||null;
    jobs[jobId]={job_id:jobId,status:'queued',session_id:sid,
      total:proxies.length,tested:0,passed:0,failed:0,
      progress_pct:0,elapsed_sec:0,eta_sec:null,
      top_proxies:[],ip_analysis:null,config,list_name:fields.file.filename||'proxies.txt'};
    runJob(jobId,proxies,config).catch(function(e){console.error(e);if(jobs[jobId])jobs[jobId].status='error';});
    return jsonRes(res,{job_id:jobId,total_proxies:proxies.length,session_id:sid});
  }

  // Job sub-routes
  const jobMatch=pathname.match(/^\/api\/jobs\/([^/]+)(\/.*)?$/);
  if(jobMatch){
    const jobId=jobMatch[1],sub=jobMatch[2]||'',job=jobs[jobId];
    if(!job) return jsonRes(res,{error:'Not found'},404);
    if(sub==='/results'&&method==='GET'){
      if(job.status!=='done') return jsonRes(res,{error:'Not complete'},400);
      const lim=parseInt(parsed.query.limit||'1000'),off=parseInt(parsed.query.offset||'0'),top=job.top_proxies;
      return jsonRes(res,{total:top.length,offset:off,limit:lim,proxies:top.slice(off,off+lim),ip_analysis:job.ip_analysis});
    }
    if(sub==='/analysis'&&method==='GET'){
      if(job.status!=='done') return jsonRes(res,{error:'Not complete'},400);
      return jsonRes(res,job.ip_analysis||{});
    }
    if(sub==='/copy'&&method==='GET'){
      if(job.status!=='done') return jsonRes(res,{error:'Not complete'},400);
      var excludeDC  = parsed.query.exclude_dc !== 'false';
      var provider   = (parsed.query.provider || '').trim().toLowerCase();
      var maxPerAsn  = parseInt(parsed.query.max_per_asn  || '0');
      var maxPerCity = parseInt(parsed.query.max_per_city || '0');
      var list = job.top_proxies.filter(function(p){
        if (provider && providerNameFromEntry(p).toLowerCase() !== provider) return false;
        if (!excludeDC) return true;
        return p.ip_type !== 'datacenter' && p.ip_type !== 'private';
      });
      list = applyDiversityCaps(list, maxPerAsn, maxPerCity);
      res.writeHead(200,{'Content-Type':'text/plain; charset=utf-8'});
      return res.end(list.map(proxyLine).join('\n'));
    }
    if(sub==='/elite'&&method==='GET'){
      if(job.status!=='done') return jsonRes(res,{error:'Not complete'},400);
      var minScore   = parseInt(parsed.query.min_score  || '60');
      var maxMs      = parseInt(parsed.query.max_ms     || '800');
      var excludeDC  = parsed.query.exclude_dc  !== 'false';
      var excludePX  = parsed.query.exclude_px  !== 'false';
      var dedupeIP   = parsed.query.dedupe_ip   !== 'false';
      var maxPerAsn  = parseInt(parsed.query.max_per_asn  || '5');
      var maxPerCity = parseInt(parsed.query.max_per_city || '3');
      var seenIPs    = new Set();
      var list = job.top_proxies.filter(function(p){
        if (p.score < minScore) return false;
        if (p.avg_ms != null && p.avg_ms > maxMs) return false;
        if (excludeDC && (p.ip_type==='datacenter'||p.ip_type==='private')) return false;
        if (excludePX && p.px_challenge) return false;
        if (p.target_pass === false) return false; // always drop target failures
        if (dedupeIP && p.egress_ip) {
          if (seenIPs.has(p.egress_ip)) return false;
          seenIPs.add(p.egress_ip);
        }
        return true;
      });
      list = applyDiversityCaps(list, maxPerAsn, maxPerCity);
      res.writeHead(200,{'Content-Type':'text/plain; charset=utf-8'});
      return res.end(list.map(proxyLine).join('\n'));
    }
    if(sub==='/debug-prompt'&&method==='GET'){
      if(job.status!=='done') return jsonRes(res,{error:'Not complete'},400);
      res.writeHead(200,{
        'Content-Type':'text/plain; charset=utf-8',
        'Content-Disposition':'attachment; filename="proxy_job_analysis_'+jobId+'.txt"'
      });
      return res.end(buildJobDebugPrompt(jobId, job));
    }
    if(sub==='/export'&&method==='GET'){
      if(job.status!=='done') return jsonRes(res,{error:'Not complete'},400);
      const p=path.join(__dirname,'results',jobId+'.json');
      if(fs.existsSync(p)){res.writeHead(200,{'Content-Type':'application/json','Content-Disposition':'attachment; filename="proxies_'+jobId+'.json"'});return fs.createReadStream(p).pipe(res);}
      res.writeHead(404); return res.end('File not found');
    }
    if(method==='DELETE'){
      delete jobs[jobId];
      const p=path.join(__dirname,'results',jobId+'.json');
      if(fs.existsSync(p))fs.unlinkSync(p);
      return jsonRes(res,{deleted:jobId});
    }
    return jsonRes(res,{job_id:job.job_id,status:job.status,session_id:job.session_id||null,
      total:job.total,tested:job.tested,passed:job.passed,failed:job.failed,
      progress_pct:job.progress_pct,elapsed_sec:job.elapsed_sec,eta_sec:job.eta_sec,
      config:job.config,list_name:job.list_name,top_proxies_count:job.top_proxies.length,
      ip_analysis:job.ip_analysis});
  }

  // ── Session Groups ─────────────────────────────────────────────────────────
  if(pathname==='/api/session-groups'&&method==='GET'){
    var sgList=Object.values(sessionGroups).sort(function(a,b){
      if(b.favorited!==a.favorited) return b.favorited?1:-1;
      return (a.order||0)-(b.order||0)||(a.name<b.name?-1:1);
    });
    return jsonRes(res,sgList);
  }
  if(pathname==='/api/session-groups'&&method==='POST'){
    const body=await readBody(req); let ov={};
    try{ov=JSON.parse(body.toString()||'{}');}catch(e){}
    if(!ov.name||!String(ov.name).trim()) return jsonRes(res,{error:'Name required'},400);
    var sgId=genId();
    sessionGroups[sgId]={id:sgId,name:String(ov.name).trim(),order:Object.keys(sessionGroups).length,
      favorited:false,session_ids:[],created_at:new Date().toISOString()};
    return jsonRes(res,sessionGroups[sgId]);
  }
  const sgMatch=pathname.match(/^\/api\/session-groups\/([^/]+)(\/.*)?$/);
  if(sgMatch){
    const sgId=sgMatch[1],sgSub=sgMatch[2]||'',sg=sessionGroups[sgId];
    if(!sg) return jsonRes(res,{error:'Group not found'},404);
    if(method==='PUT'&&sgSub===''){
      const body=await readBody(req); let ov={};
      try{ov=JSON.parse(body.toString()||'{}');}catch(e){}
      if(ov.name!=null&&String(ov.name).trim()) sg.name=String(ov.name).trim();
      if(ov.favorited!=null) sg.favorited=!!ov.favorited;
      if(ov.order!=null) sg.order=parseInt(ov.order)||0;
      return jsonRes(res,sg);
    }
    if(method==='DELETE'&&sgSub===''){
      delete sessionGroups[sgId];
      return jsonRes(res,{ok:true});
    }
    if(method==='POST'&&sgSub==='/add'){
      const body=await readBody(req); let ov={};
      try{ov=JSON.parse(body.toString()||'{}');}catch(e){}
      if(ov.session_id&&sg.session_ids.indexOf(ov.session_id)<0) sg.session_ids.push(ov.session_id);
      return jsonRes(res,sg);
    }
    if(method==='POST'&&sgSub==='/remove'){
      const body=await readBody(req); let ov={};
      try{ov=JSON.parse(body.toString()||'{}');}catch(e){}
      sg.session_ids=sg.session_ids.filter(function(id){return id!==ov.session_id;});
      return jsonRes(res,sg);
    }
    return jsonRes(res,{error:'Not found'},404);
  }

  // ── Providers ────────────────────────────────────────────────────────────────

  // List providers
  if(pathname==='/api/providers'&&method==='GET'){
    return jsonRes(res, Object.values(providers).map(function(p){
      var sess=(p.session_ids||[]).map(function(sid){return sessions[sid];}).filter(Boolean);
      var last=sess[sess.length-1];
      var avgMs=null;
      if(last&&last.analytics&&last.analytics.isp_speeds&&last.analytics.isp_speeds.length){
        var tot=last.analytics.isp_speeds.reduce(function(acc,x){return acc+x.avg_ms*x.count;},0);
        var cnt=last.analytics.isp_speeds.reduce(function(acc,x){return acc+x.count;},0);
        if(cnt>0) avgMs=Math.round(tot/cnt);
      }
      return {id:p.id,name:p.name,
        proxy_count:p.proxies?p.proxies.length:0,
        session_count:p.session_ids.length,
        last_run_count:last?last.run_count:0,
        last_quality_score:last&&last.ip_analysis?last.ip_analysis.quality_score:null,
        last_residential_pct:last&&last.ip_analysis?last.ip_analysis.residential_pct:null,
        last_avg_ms:avgMs,
        created_at:p.created_at,updated_at:p.updated_at};
    }));
  }

  // Auto-pick best proxies across ALL provider sessions + standalone tester sessions
  if(pathname==='/api/providers/autopick'&&method==='GET'){
    var topN=parseInt(parsed.query.top_n||'1000');
    var minScore=parseInt(parsed.query.min_score||'0');
    var byEgress={};
    function mergeRanked(analytics){
      if(!analytics||!analytics.composite_ranked) return;
      analytics.composite_ranked.forEach(function(item){
        if(item.composite_score<minScore) return;
        var key=item.egress_ip||('__'+item.proxy.host+':'+item.proxy.port);
        if(!byEgress[key]||byEgress[key].composite_score<item.composite_score)
          byEgress[key]=item;
      });
    }
    // Provider-linked sessions
    var processedSids=new Set();
    Object.values(providers).forEach(function(p){
      (p.session_ids||[]).forEach(function(sid){
        processedSids.add(sid);
        var s=sessions[sid];
        if(!s) return;
        mergeRanked(s.analytics||computeSessionAnalytics(s));
      });
    });
    // Standalone tester sessions (not linked to any provider)
    Object.values(sessions).forEach(function(s){
      if(processedSids.has(s.session_id)) return;
      mergeRanked(s.analytics||computeSessionAnalytics(s));
    });
    var sorted=Object.values(byEgress)
      .sort(function(a,b){return b.composite_score-a.composite_score;})
      .slice(0,topN);
    res.writeHead(200,{'Content-Type':'text/plain; charset=utf-8'});
    return res.end(sorted.map(function(p){return proxyLine(p.proxy);}).join('\n'));
  }

  // Create provider
  if(pathname==='/api/providers'&&method==='POST'){
    const body=await readBody(req), ct=req.headers['content-type']||'', bm=ct.match(/boundary=(.+)/);
    if(!bm) return jsonRes(res,{error:'Bad content-type'},400);
    const fields=parseMultipart(body,bm[1].trim());
    if(!fields.file) return jsonRes(res,{error:'No file field'},400);
    const proxies=parseProxies(fields.file.data.toString('utf-8'));
    if(!proxies.length) return jsonRes(res,{error:'No valid proxies found'},400);
    const pid=genId();
    providers[pid]={
      id:pid,
      name:fields.provider_name||fields.file.filename||'Provider '+pid,
      input_text:fields.file.data.toString('utf-8'),
      proxies:proxies,
      session_ids:[],
      created_at:new Date().toISOString(),
      updated_at:new Date().toISOString()
    };
    return jsonRes(res,{id:pid,name:providers[pid].name,proxy_count:proxies.length});
  }

  // Provider sub-routes (/api/providers/:id)
  const provMatch=pathname.match(/^\/api\/providers\/([^/]+)(\/.*)?$/);
  if(provMatch){
    const pid=provMatch[1],psub=provMatch[2]||'',prov=providers[pid];
    if(!prov) return jsonRes(res,{error:'Provider not found'},404);

    if(method==='GET'&&psub===''){
      var sess=(prov.session_ids||[]).map(function(sid){return sessions[sid];}).filter(Boolean);
      return jsonRes(res,{
        id:prov.id,name:prov.name,
        proxy_count:prov.proxies?prov.proxies.length:0,
        session_ids:prov.session_ids,
        sessions:sess.map(function(s){return {
          session_id:s.session_id,name:s.name,run_count:s.run_count,
          status:s.status,updated_at:s.updated_at,
          analyzed_count:s.analyzed?s.analyzed.length:0,
          quality_score:s.ip_analysis?s.ip_analysis.quality_score:null,
          residential_pct:s.ip_analysis?s.ip_analysis.residential_pct:null
        };}),
        created_at:prov.created_at,updated_at:prov.updated_at
      });
    }

    if(method==='PUT'&&psub===''){
      const body=await readBody(req); let ov={};
      try{ov=JSON.parse(body.toString()||'{}');}catch(e){}
      if(ov.name&&String(ov.name).trim()) prov.name=String(ov.name).trim();
      prov.updated_at=new Date().toISOString();
      return jsonRes(res,{id:prov.id,name:prov.name});
    }

    if(method==='POST'&&psub==='/run'){
      const body=await readBody(req); let ov={};
      try{ov=JSON.parse(body.toString()||'{}');}catch(e){}
      const config={
        test_url:ov.test_url||'http://ip-api.com/json?fields=status,message,query,country,countryCode,isp,org,as,asname,mobile,proxy,hosting',
        target_url:(ov.target_url||'').trim(),
        concurrency:parseInt(ov.concurrency||'150'),
        timeout:parseFloat(ov.timeout||'10'),
        retries:parseInt(ov.retries||'1'),
        top_n:parseInt(ov.top_n||'1000')
      };
      const sid=genId();
      sessions[sid]={
        session_id:sid,
        name:prov.name+' — Test '+(prov.session_ids.length+1),
        status:'idle',run_count:0,run_ids:[],
        proxies:prov.proxies,config:config,
        proxy_history:{},analyzed:[],best_proxies:[],ip_analysis:null,
        created_at:new Date().toISOString(),updated_at:new Date().toISOString()
      };
      prov.session_ids.push(sid);
      prov.updated_at=new Date().toISOString();
      const jobId=genId();
      jobs[jobId]={job_id:jobId,status:'queued',session_id:sid,
        total:prov.proxies.length,tested:0,passed:0,failed:0,
        progress_pct:0,elapsed_sec:0,eta_sec:null,top_proxies:[],config:config,
        list_name:sessions[sid].name+' — Run 1'};
      sessions[sid].status='running';
      runJob(jobId,prov.proxies,config).catch(function(e){console.error(e);sessions[sid].status='idle';});
      return jsonRes(res,{session_id:sid,job_id:jobId,proxy_count:prov.proxies.length});
    }

    // Link an existing tester session to this provider
    if(method==='POST'&&psub==='/link-session'){
      const body=await readBody(req); let ov={};
      try{ov=JSON.parse(body.toString()||'{}');}catch(e){}
      var linkSid=ov.session_id;
      if(!linkSid||!sessions[linkSid]) return jsonRes(res,{error:'Session not found'},404);
      if(prov.session_ids.indexOf(linkSid)<0){
        prov.session_ids.push(linkSid);
        prov.updated_at=new Date().toISOString();
      }
      return jsonRes(res,{linked:linkSid,total_sessions:prov.session_ids.length});
    }

    if(method==='DELETE'){
      (prov.session_ids||[]).forEach(function(sid){
        if(sessions[sid]){
          sessions[sid].run_ids.forEach(function(jid){delete jobs[jid];});
          delete sessions[sid];
        }
      });
      delete providers[pid];
      return jsonRes(res,{deleted:pid});
    }
  }

  // Cross-session comparison
  if(pathname==='/api/sessions/compare'&&method==='GET')
    return jsonRes(res,Object.values(sessions).map(function(s){
      var a=s.analytics||{};
      return{session_id:s.session_id,name:s.name,run_count:s.run_count,
             proxy_count:s.proxies?s.proxies.length:0,
             analyzed_count:s.analyzed?s.analyzed.length:0,
             quality_score:s.ip_analysis?s.ip_analysis.quality_score:null,
             avg_ms:a.isp_speeds&&a.isp_speeds.length?Math.round(a.isp_speeds.reduce(function(acc,x){return acc+x.avg_ms*x.count;},0)/a.isp_speeds.reduce(function(acc,x){return acc+x.count;},1)):null,
             unique_ips:a.unique_egress_ips||0,
             uncrowded_count:a.uncrowded_count||0,
             isp_count:s.ip_analysis?s.ip_analysis.isp_diversity||0:0,
             residential_pct:s.ip_analysis?s.ip_analysis.residential_pct||0:0,
             datacenter_pct:s.ip_analysis?s.ip_analysis.datacenter_pct||0:0,
             updated_at:s.updated_at};}));

  // Sessions list
  if(pathname==='/api/sessions'&&method==='GET')
    return jsonRes(res,Object.values(sessions).map(function(s){
      return{session_id:s.session_id,name:s.name,status:s.status,
             run_count:s.run_count,proxy_count:s.proxies?s.proxies.length:0,
             proxy_type:s.proxy_type||'residential',
             updated_at:s.updated_at};}));

  // Create session
  if(pathname==='/api/sessions'&&method==='POST'){
    const body=await readBody(req), ct=req.headers['content-type']||'', bm=ct.match(/boundary=(.+)/);
    if(!bm) return jsonRes(res,{error:'Bad content-type'},400);
    const fields=parseMultipart(body,bm[1].trim());
    if(!fields.file) return jsonRes(res,{error:'No file field'},400);
    const proxies=parseProxies(fields.file.data.toString('utf-8'));
    if(!proxies.length) return jsonRes(res,{error:'No valid proxies found'},400);
    const config={test_url:fields.test_url||'http://ip-api.com/json',
                  target_url:(fields.target_url||'').trim(),
                  concurrency:parseInt(fields.concurrency||'150'),
                  timeout:parseFloat(fields.timeout||'10'),
                  retries:parseInt(fields.retries||'1'),
                  top_n:parseInt(fields.top_n||'1000')};
    const sid=genId();
    sessions[sid]={session_id:sid,name:fields.session_name||fields.file.filename||'Session',
      status:'idle',run_count:0,run_ids:[],proxies,config,
      proxy_type:fields.proxy_type||'residential',
      proxy_history:{},analyzed:[],best_proxies:[],ip_analysis:null,
      created_at:new Date().toISOString(),updated_at:new Date().toISOString()};
    return jsonRes(res,{session_id:sid,proxy_count:proxies.length,name:sessions[sid].name});
  }

  // Session sub-routes
  const sessMatch=pathname.match(/^\/api\/sessions\/([^/]+)(\/.*)?$/);
  if(sessMatch){
    const sid=sessMatch[1],sub=sessMatch[2]||'',s=sessions[sid];
    if(!s) return jsonRes(res,{error:'Session not found'},404);

    if(method==='GET'&&sub==='')
      return jsonRes(res,{session_id:s.session_id,name:s.name,status:s.status,
        run_count:s.run_count,run_ids:s.run_ids,
        proxy_count:s.proxies?s.proxies.length:0,config:s.config,
        updated_at:s.updated_at,best_count:s.best_proxies.length,
        passed_all:s.analyzed.length,ip_analysis:s.ip_analysis});

    if(method==='POST'&&sub==='/run'){
      if(s.status==='running') return jsonRes(res,{error:'Run already in progress'},400);
      if(!s.proxies||!s.proxies.length) return jsonRes(res,{error:'No proxies in session'},400);
      const body=await readBody(req); let ov={};
      try{ov=JSON.parse(body.toString()||'{}');}catch(e){}
      const rc=Object.assign({},s.config,ov);
      const jobId=genId();
      jobs[jobId]={job_id:jobId,status:'queued',session_id:sid,
        total:s.proxies.length,tested:0,passed:0,failed:0,
        progress_pct:0,elapsed_sec:0,eta_sec:null,top_proxies:[],config:rc,
        list_name:s.name+' — Run '+(s.run_count+1)};
      s.status='running';
      runJob(jobId,s.proxies,rc).catch(function(e){console.error(e);s.status='idle';});
      return jsonRes(res,{job_id:jobId,run_number:s.run_count+1,total_proxies:s.proxies.length});
    }

    if(method==='GET'&&sub==='/results'){
      const lim=parseInt(parsed.query.limit||'2000'),off=parseInt(parsed.query.offset||'0');
      return jsonRes(res,{session_id:sid,run_count:s.run_count,
        total:s.analyzed.length,offset:off,limit:lim,
        proxies:s.analyzed.slice(off,off+lim),ip_analysis:s.ip_analysis});
    }

    if(method==='GET'&&sub==='/analysis')
      return jsonRes(res,s.ip_analysis||{});

    if(method==='GET'&&sub==='/analytics')
      return jsonRes(res,s.analytics||computeSessionAnalytics(s));

    if(method==='GET'&&sub==='/copy-composite'){
      var topN=parseInt(parsed.query.top_n||'1000');
      var minScore=parseInt(parsed.query.min_score||'0');
      var provider=(parsed.query.provider||'').trim().toLowerCase();
      var maxPerAsn=parseInt(parsed.query.max_per_asn||'0');
      var maxPerCity=parseInt(parsed.query.max_per_city||'0');
      var analytics=s.analytics||computeSessionAnalytics(s);
      var best=analytics.composite_ranked
        .filter(function(p){return p.composite_score>=minScore;})
        .filter(function(p){return !provider || String(p.isp || 'Unknown ISP').toLowerCase() === provider;});
      best = applyDiversityCaps(best, maxPerAsn, maxPerCity).slice(0,topN);
      res.writeHead(200,{'Content-Type':'text/plain; charset=utf-8'});
      return res.end(best.map(function(p){return proxyLine(p.proxy);}).join('\n'));
    }

    if(method==='GET'&&sub==='/copy'){
      const topN=parseInt(parsed.query.top_n||'1000');
      const minPasses=parseInt(parsed.query.min_passes||s.run_count||'1');
      var excludeDC  = parsed.query.exclude_dc !== 'false';
      var provider   = (parsed.query.provider || '').trim().toLowerCase();
      var maxPerAsn  = parseInt(parsed.query.max_per_asn  || '0');
      var maxPerCity = parseInt(parsed.query.max_per_city || '0');
      var best=s.analyzed
        .filter(function(a){ return a.pass_count>=minPasses; })
        .filter(function(a){ return !provider || providerNameFromEntry(a).toLowerCase() === provider; })
        .filter(function(a){ if(!excludeDC)return true; return a.ip_type!=='datacenter'&&a.ip_type!=='private'; });
      best = applyDiversityCaps(best, maxPerAsn, maxPerCity).slice(0,topN);
      res.writeHead(200,{'Content-Type':'text/plain; charset=utf-8'});
      return res.end(best.map(function(a){return proxyLine(a.proxy);}).join('\n'));
    }

    if(method==='GET'&&sub==='/list'){
      // Return the raw input proxy list as newline-separated text
      var rawLines=(s.proxies||[]).map(function(p){return proxyLine(p);});
      res.writeHead(200,{'Content-Type':'text/plain; charset=utf-8'});
      return res.end(rawLines.join('\n'));
    }

    if(method==='GET'&&sub==='/debug-prompt'){
      res.writeHead(200,{
        'Content-Type':'text/plain; charset=utf-8',
        'Content-Disposition':'attachment; filename="proxy_session_analysis_'+sid+'.txt"'
      });
      return res.end(buildSessionDebugPrompt(sid, s));
    }

    if(method==='PUT'&&sub===''){
      const body=await readBody(req); let ov={};
      try{ov=JSON.parse(body.toString()||'{}');}catch(e){}
      if(ov.name&&String(ov.name).trim()) s.name=String(ov.name).trim();
      return jsonRes(res,{session_id:s.session_id,name:s.name});
    }

    if(method==='DELETE'){
      s.run_ids.forEach(function(jid){delete jobs[jid];});
      delete sessions[sid];
      const fp=path.join(__dirname,'results','session_'+sid+'.json');
      if(fs.existsSync(fp))fs.unlinkSync(fp);
      return jsonRes(res,{deleted:sid});
    }
  }

  if(pathname==='/'||pathname==='/index.html'){
    res.writeHead(200,{'Content-Type':'text/html; charset=utf-8'});
    var htmlPath=path.join(__dirname,'index.html');
    return res.end(fs.existsSync(htmlPath)?fs.readFileSync(htmlPath,'utf8'):'<h1>index.html not found</h1>');
  }
  res.writeHead(404); res.end('Not found');
});

// ── Boot ──────────────────────────────────────────────────────────────────────

fs.mkdirSync(path.join(DATA_DIR,'results'),{recursive:true});

server.on('error', function(err) {
  if (err.code === 'EADDRINUSE') {
    console.log('PROXY TESTER — port ' + PORT + ' already in use (another instance may be running). Continuing — UI will connect to existing server.');
    // Don't crash — waitForServer() in main.js will connect to the existing server
    // and createWindow() will still be called.
  } else {
    console.error('PROXY TESTER — server error:', err);
  }
});

server.listen(PORT,'127.0.0.1',function(){
  console.log('PROXY TESTER — listening on port '+PORT);
});
