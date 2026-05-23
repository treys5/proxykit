'use strict';

const { app, BrowserWindow, Menu, shell } = require('electron');
const http = require('http');
const path = require('path');
const fs   = require('fs');

const PORT = parseInt(process.env.PORT || '8080', 10);
let mainWindow = null;

// ── Startup log (written to startup.log next to main.js) ─────────────────
function log(msg) {
  var line = new Date().toISOString() + '  ' + String(msg) + '\n';
  process.stdout.write(line);
  try {
    // app.getPath() not available yet at this point, use env var set later
    // or fall back to os.homedir so writes always succeed in packaged builds
    var logDir = process.env.APP_DATA_DIR || app.getPath('userData');
    fs.appendFileSync(path.join(logDir, 'startup.log'), line);
  } catch(e) {}
}

log('main.js start — PORT=' + PORT + ' electron=' + process.versions.electron + ' platform=' + process.platform);

// ── Windows Server compatibility ──────────────────────────────────────────
// Server OS environments (2016/2019/2022) often have no GPU or run over RDP.
// Electron's GPU process silently fails in those cases, preventing the window
// from appearing. Detect Server and disable hardware acceleration up front.
(function() {
  if (process.platform !== 'win32') return;
  try {
    var reg = require('child_process').execSync(
      'reg query "HKLM\\SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion" /v ProductName',
      { encoding: 'utf8', windowsHide: true, timeout: 3000 }
    );
    if (/Server/i.test(reg)) {
      log('Windows Server detected — disabling GPU acceleration for compatibility');
      app.disableHardwareAcceleration();
    } else {
      log('Windows desktop detected — GPU acceleration enabled');
    }
  } catch(e) {
    log('OS detection failed, continuing with defaults: ' + e.message);
  }
})();

// ── Single-instance lock ──────────────────────────────────────────────────
const gotLock = app.requestSingleInstanceLock();
log('requestSingleInstanceLock → ' + gotLock);

if (!gotLock) {
  log('Another instance holds the lock — quitting immediately');
  app.quit();
} else {
  app.on('second-instance', function() {
    log('second-instance fired — bringing window to front');
    if (mainWindow) {
      if (mainWindow.isMinimized()) mainWindow.restore();
      mainWindow.show();
      mainWindow.focus();
    }
  });
}

// ── Wait for the HTTP server to be ready ──────────────────────────────────
function waitForServer(maxMs) {
  maxMs = maxMs || 10000;
  return new Promise(function(resolve) {
    var deadline = Date.now() + maxMs;
    function attempt() {
      var req = http.get('http://127.0.0.1:' + PORT + '/api/version', function() {
        log('server is up');
        resolve();
      });
      req.on('error', function(e) {
        if (Date.now() < deadline) { setTimeout(attempt, 300); }
        else { log('waitForServer timed out: ' + e.message); resolve(); }
      });
      req.setTimeout(500, function() { req.destroy(); });
    }
    setTimeout(attempt, 300);
  });
}

// ── Create the main window ────────────────────────────────────────────────
function createWindow() {
  log('createWindow');
  mainWindow = new BrowserWindow({
    width: 1440,
    height: 900,
    minWidth: 1000,
    minHeight: 680,
    title: 'PROXY TESTER',
    backgroundColor: '#0a0a0c',
    webPreferences: {
      nodeIntegration: false,
      contextIsolation: true,
      sandbox: false,
    },
  });

  Menu.setApplicationMenu(null);

  mainWindow.webContents.on('did-fail-load', function(e, code, desc) {
    log('did-fail-load code=' + code + ' desc=' + desc);
  });

  mainWindow.webContents.setWindowOpenHandler(function(details) {
    shell.openExternal(details.url);
    return { action: 'deny' };
  });

  mainWindow.on('closed', function() { log('window closed'); mainWindow = null; });

  log('loadURL http://127.0.0.1:' + PORT);
  mainWindow.loadURL('http://127.0.0.1:' + PORT);
}

// ── App ready ─────────────────────────────────────────────────────────────
app.whenReady().then(function() {
  log('app ready');
  if (!gotLock) { log('no lock — skipping startup'); return; }

  process.env.APP_DATA_DIR = app.getPath('userData');
  process.env.PORT = String(PORT);
  log('userData=' + process.env.APP_DATA_DIR);

  try {
    require('./server');
    log('server module loaded');
  } catch(e) {
    log('ERROR loading server: ' + e.message);
    // Continue anyway — maybe a previous instance's server is already up
  }

  waitForServer().then(function() {
    log('opening window');
    createWindow();
  });

}).catch(function(err) {
  log('app.whenReady rejected: ' + err.message);
});

app.on('window-all-closed', function() { log('window-all-closed → quit'); app.quit(); });
app.on('activate', function() { if (!mainWindow) createWindow(); });
