#Requires -Version 5.1
<#
.SYNOPSIS
  ProxyKit one-click installer.
  Run this once to install ProxyKit, create a desktop shortcut, and launch.
  After that, the app updates itself — just use the desktop shortcut.

.USAGE
  Right-click install.ps1 → "Run with PowerShell"
    OR
  powershell -ExecutionPolicy Bypass -File install.ps1
#>

$ErrorActionPreference = 'Stop'

$AppName     = 'ProxyKit'
$ExeName     = 'ProxyKit.exe'
$InstallDir  = Join-Path $env:LOCALAPPDATA $AppName
$Desktop     = [Environment]::GetFolderPath('Desktop')
$ShortcutPath = Join-Path $Desktop "$AppName.lnk"

# ── Helpers ────────────────────────────────────────────────────────────────────
function Write-Step([string]$msg) { Write-Host "  > $msg" -ForegroundColor Cyan }
function Write-OK([string]$msg)   { Write-Host "  + $msg" -ForegroundColor Green }
function Write-Err([string]$msg)  { Write-Host "  ! $msg" -ForegroundColor Red }

Clear-Host
Write-Host ""
Write-Host "  PROXYKIT INSTALLER" -ForegroundColor Magenta
Write-Host "  ──────────────────────────────────────────" -ForegroundColor DarkGray
Write-Host ""

# ── Locate the exe to install ──────────────────────────────────────────────────
# The exe might be sitting right next to this script (downloaded as a zip),
# or we need to fetch the latest release from GitHub.

$scriptDir  = Split-Path $MyInvocation.MyCommand.Path -Parent
$localExe   = Join-Path $scriptDir $ExeName
$exeToInstall = $null

if (Test-Path $localExe) {
    Write-OK "Found $ExeName alongside installer — using it."
    $exeToInstall = $localExe
    $version = 'local'
} else {
    # Fetch latest from GitHub
    Write-Step "Fetching latest release from GitHub..."
    try {
        $headers = @{ 'User-Agent' = 'ProxyKit-Installer/1.0' }
        $rel     = Invoke-RestMethod -Uri 'https://api.github.com/repos/treys5/proxykit/releases/latest' `
                                     -Headers $headers -TimeoutSec 15
        $asset   = $rel.assets | Where-Object { $_.name -eq $ExeName } | Select-Object -First 1

        if (-not $asset) {
            Write-Err "No $ExeName asset found in the latest release."
            Write-Host "  Please download ProxyKit.exe manually from:" -ForegroundColor Yellow
            Write-Host "  https://github.com/treys5/proxykit/releases/latest" -ForegroundColor Yellow
            Read-Host "`n  Press Enter to exit"
            exit 1
        }

        $version  = $rel.tag_name -replace '^v', ''
        $tmpExe   = Join-Path $env:TEMP $ExeName
        Write-Step "Downloading v$version..."

        $wc = New-Object System.Net.WebClient
        $wc.Headers.Add('User-Agent', 'ProxyKit-Installer/1.0')
        $wc.DownloadFile($asset.browser_download_url, $tmpExe)
        $exeToInstall = $tmpExe
        Write-OK "Downloaded v$version"
    } catch {
        Write-Err "Download failed: $_"
        Write-Host ""
        Write-Host "  Please download ProxyKit.exe manually from:" -ForegroundColor Yellow
        Write-Host "  https://github.com/treys5/proxykit/releases/latest" -ForegroundColor Yellow
        Read-Host "`n  Press Enter to exit"
        exit 1
    }
}

# ── Stop running instance ──────────────────────────────────────────────────────
$running = Get-Process -Name ($ExeName -replace '\.exe$','') -ErrorAction SilentlyContinue
if ($running) {
    Write-Step "Stopping running ProxyKit instance..."
    $running | Stop-Process -Force
    Start-Sleep -Milliseconds 800
}

# ── Install ────────────────────────────────────────────────────────────────────
Write-Step "Installing to $InstallDir ..."
if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

$destExe = Join-Path $InstallDir $ExeName
Copy-Item -Path $exeToInstall -Destination $destExe -Force
Write-OK "Installed $ExeName"

# ── Desktop shortcut ───────────────────────────────────────────────────────────
Write-Step "Creating desktop shortcut..."
try {
    $wsh = New-Object -ComObject WScript.Shell
    $sc  = $wsh.CreateShortcut($ShortcutPath)
    $sc.TargetPath       = $destExe
    $sc.WorkingDirectory = $InstallDir
    $sc.Description      = 'ProxyKit — Enterprise Proxy Testing Suite'
    $sc.IconLocation     = "$destExe,0"
    $sc.Save()
    Write-OK "Desktop shortcut created"
} catch {
    Write-Host "  (Shortcut skipped: $_)" -ForegroundColor DarkGray
}

# ── Launch ─────────────────────────────────────────────────────────────────────
Write-Step "Launching ProxyKit..."
Start-Process $destExe -WorkingDirectory $InstallDir

Write-Host ""
Write-Host "  ──────────────────────────────────────────" -ForegroundColor DarkGray
Write-OK   "ProxyKit installed successfully!"
Write-OK   "Desktop shortcut ready — use it from now on."
Write-Host ""
Write-Host "  Location: $InstallDir" -ForegroundColor DarkGray
Write-Host ""
Start-Sleep -Seconds 3
