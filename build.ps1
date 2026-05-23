<#
.SYNOPSIS
  One-click Windows build for Proxy Tester.
  Produces a self-contained ZIP in the dist\ folder — no installer needed.
  Run from the project root:  .\build.ps1
#>
param(
    [switch]$SkipInstall,
    [switch]$NoZip
)

$ErrorActionPreference = 'Stop'
$AppDir  = Split-Path -Parent $MyInvocation.MyCommand.Path
$NodeDir = Join-Path $AppDir "node"
$NodeExe = Join-Path $NodeDir "node.exe"
$NpmCmd  = Join-Path $NodeDir "npm.cmd"

Write-Host ""
Write-Host "  PROXY TESTER -- BUILD SCRIPT (Windows)" -ForegroundColor Cyan
Write-Host ""

# 1. Ensure Node.js is available
if (-not (Test-Path $NodeExe)) {
    Write-Host "  Bundled Node.js not found; trying system Node.js..." -ForegroundColor Yellow
    $sysNode = Get-Command node -ErrorAction SilentlyContinue
    $sysNpm  = Get-Command npm  -ErrorAction SilentlyContinue
    if (-not $sysNode) {
        Write-Host ""
        Write-Host "  ERROR: Node.js is not installed." -ForegroundColor Red
        Write-Host "  Install it from https://nodejs.org then re-run this script." -ForegroundColor Yellow
        pause; exit 1
    }
    $NodeExe = $sysNode.Source
    $NpmCmd  = $sysNpm.Source
}

# Add bundled node to PATH so npm.cmd can find it
$env:Path = "$NodeDir;$env:Path"
Write-Host "  Node: $($NodeExe | Split-Path -Leaf)" -ForegroundColor DarkGray

# 2. Install / update npm dependencies
if (-not $SkipInstall) {
    Write-Host "  Installing dependencies (may take a minute on first run)..." -ForegroundColor Yellow
    Push-Location $AppDir
    try {
        & $NpmCmd install 2>&1 | Where-Object { $_ -notmatch '^npm warn' } | ForEach-Object { Write-Host "    $_" -ForegroundColor DarkGray }
        if ($LASTEXITCODE -ne 0) { throw "npm install failed (exit $LASTEXITCODE)" }
    } finally { Pop-Location }
    Write-Host "  Dependencies ready." -ForegroundColor Green
}

# 3. Clear previous build output so files aren't locked
$prevBuild = Join-Path $AppDir "dist\Proxy Tester-win32-x64"
if (Test-Path $prevBuild) {
    Write-Host "  Stopping any running Proxy Tester processes..." -ForegroundColor DarkGray
    Get-Process -Name "Proxy Tester" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 500
    Write-Host "  Removing previous build folder..." -ForegroundColor DarkGray
    try {
        Remove-Item -Recurse -Force $prevBuild -ErrorAction Stop
    } catch {
        Write-Host ""
        Write-Host "  ERROR: Could not delete $prevBuild" -ForegroundColor Red
        Write-Host "  Close 'Proxy Tester.exe' if it is running, then try again." -ForegroundColor Yellow
        pause; exit 1
    }
}

# 4. Build the app with electron-packager
Write-Host ""
Write-Host "  Packaging app (downloads ~100 MB Electron on first run)..." -ForegroundColor Cyan
Push-Location $AppDir
try {
    & $NpmCmd run build:win 2>&1 | ForEach-Object { Write-Host "    $_" -ForegroundColor DarkGray }
    if ($LASTEXITCODE -ne 0) { throw "Build failed (exit $LASTEXITCODE)" }
} finally { Pop-Location }

# 5. Drop the launcher bat into the built folder so it sorts first
$appFolder  = Join-Path $AppDir "dist\Proxy Tester-win32-x64"
$launcherSrc = Join-Path $AppDir "! Launch Proxy Tester.bat"
$launcherDst = Join-Path $appFolder "! Launch Proxy Tester.bat"
if ((Test-Path $appFolder) -and (Test-Path $launcherSrc)) {
    Copy-Item $launcherSrc $launcherDst -Force
    Write-Host "  Launcher bat added to dist folder." -ForegroundColor DarkGray
}

# 6. Zip the output folder for easy sharing
$zipPath = Join-Path $AppDir "dist\Proxy Tester-win32-x64.zip"

if ((Test-Path $appFolder) -and (-not $NoZip)) {
    Write-Host "  Creating ZIP archive..." -ForegroundColor Cyan
    if (Test-Path $zipPath) { Remove-Item $zipPath -Force }
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    [System.IO.Compression.ZipFile]::CreateFromDirectory($appFolder, $zipPath)
    $sizeMB = [math]::Round((Get-Item $zipPath).Length / 1MB, 1)
    Write-Host "  ZIP created: $sizeMB MB" -ForegroundColor Green
}

# 7. Report output
$distDir = Join-Path $AppDir "dist"
Write-Host ""
Write-Host "  BUILD COMPLETE!" -ForegroundColor Green
Write-Host "  Output in: $distDir" -ForegroundColor Cyan
Write-Host ""
if (Test-Path $zipPath) {
    Write-Host "  >> Proxy Tester-win32-x64.zip  (share this)" -ForegroundColor White
    Write-Host "     Users unzip the folder, then double-click:" -ForegroundColor DarkGray
    Write-Host "     '! Launch Proxy Tester.bat'" -ForegroundColor White
}
Write-Host ""
