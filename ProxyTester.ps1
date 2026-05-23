param(
    [int]$Port = 8080,
    [string]$UpdateUrl = ''
)

$AppDir  = Split-Path -Parent $MyInvocation.MyCommand.Path
$NodeDir = Join-Path $AppDir "node"
$NodeExe = Join-Path $NodeDir "node.exe"
$NpmCmd  = Join-Path $NodeDir "npm.cmd"
$Server  = Join-Path $AppDir "server.js"
$MainJs  = Join-Path $AppDir "main.js"
$PkgJson = Join-Path $AppDir "package.json"
$ElectronCmd = Join-Path $AppDir "node_modules\.bin\electron.cmd"
$ElectronExe = Join-Path $AppDir "node_modules\electron\dist\electron.exe"

Write-Host ""
Write-Host "  PROXY TESTER" -ForegroundColor Cyan
Write-Host "  Launching on port $Port" -ForegroundColor Cyan
Write-Host ""

if (-not (Test-Path $NodeExe)) {
    Write-Host "  Node.js not found. Downloading..." -ForegroundColor Yellow
    $ZipPath = Join-Path $AppDir "node.zip"
    $NodeUrl = "https://nodejs.org/dist/v18.20.4/node-v18.20.4-win-x64.zip"
    try {
        [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
        (New-Object System.Net.WebClient).DownloadFile($NodeUrl, $ZipPath)
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        [System.IO.Compression.ZipFile]::ExtractToDirectory($ZipPath, $AppDir)
        Rename-Item (Join-Path $AppDir "node-v18.20.4-win-x64") $NodeDir
        Remove-Item $ZipPath
        Write-Host "  Node.js installed." -ForegroundColor Green
    } catch {
        Write-Host "  ERROR: Could not download Node.js." -ForegroundColor Red
        Write-Host "  Install it manually from https://nodejs.org" -ForegroundColor Yellow
        pause
        exit 1
    }
}

if (-not (Test-Path $Server)) {
    Write-Host "  ERROR: server.js not found in $AppDir" -ForegroundColor Red
    pause
    exit 1
}

$CanLaunchElectron = (Test-Path $MainJs) -and (Test-Path $PkgJson)

$NeedInstall = $CanLaunchElectron -and (-not (Test-Path $ElectronExe))
if ($NeedInstall) {
    Write-Host "  Electron runtime not found. Installing app dependencies..." -ForegroundColor Yellow
    if (-not (Test-Path $NpmCmd)) {
        Write-Host "  ERROR: npm.cmd not found in $NodeDir" -ForegroundColor Red
        pause
        exit 1
    }
    $env:Path = $NodeDir + ";" + $env:Path
    Push-Location $AppDir
    try {
        & $NpmCmd install
        if ($LASTEXITCODE -ne 0) {
            Write-Host "  ERROR: npm install failed." -ForegroundColor Red
            pause
            exit 1
        }
    } finally {
        Pop-Location
    }
}

$env:PORT = $Port

if ($CanLaunchElectron -and (Test-Path $ElectronExe)) {
    Write-Host "  Opening Electron window..." -ForegroundColor Green
    Push-Location $AppDir
    try {
        & $ElectronExe .
        if ($LASTEXITCODE -ne 0) {
            Write-Host "  ERROR: Electron exited with code $LASTEXITCODE" -ForegroundColor Red
            pause
        }
        exit $LASTEXITCODE
    } finally {
        Pop-Location
    }
}

Write-Host "  Electron runtime unavailable. Falling back to server-only mode." -ForegroundColor Yellow
& $NodeExe $Server
