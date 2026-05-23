# Proxy Tester - Standalone App

## Quick Start

1. Double-click **ProxyTester.bat**
2. First launch downloads Node.js automatically (~30MB, one time only)
3. If Electron is not installed yet, the launcher runs `npm install` once
4. The Electron desktop window opens automatically

That's it.

---

## Files

```
ProxyTester.bat      <- Double-click this to launch
ProxyTester.ps1      <- Electron-first launcher script
server.js            <- Backend (replace to update)
index.html           <- Frontend (replace to update)
main.js              <- Electron main process
package.json         <- App dependencies and scripts
node_modules\        <- Created automatically when dependencies install
node\                <- Created automatically on first launch
results\             <- Saved test results (JSON)
```

---

## Updating the App

To update to a new version, replace `server.js` and/or `index.html`
next to ProxyTester.bat and restart.

---

## Custom Port

Edit `ProxyTester.bat` and change:

```
set PORT=8080
```

---

## Proxy List Formats

All of these are auto-detected:

```
host:port
host:port:user:pass
user:pass@host:port
socks5://user:pass@host:port
```

Proxies sharing the same host:port with different credentials
are tested individually.

---

## Results

- **COPY TOP 1000** - copies the best proxies in `host:port:user:pass` format
- **TXT** - downloads the current best export list
- **AI DEBUG** - downloads an AI-ready analysis prompt with the session data
