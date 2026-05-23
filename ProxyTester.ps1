param(
    [int]$Port = 8080,
    [string]$UpdateUrl = ''
)

$AppDir      = Split-Path -Parent $MyInvocation.MyCommand.Path
$NodeDir     = Join-Path $AppDir "node"
$NodeExe     = Join-Path $NodeDir "node.exe"
$BundledNpm  = Join-Path $NodeDir "npm.cmd"
$Server      = Join-Path $AppDir "server.js"
$MainJs      = Join-Path $AppDir "main.js"
$PkgJson     = Join-Path $AppDir "package.json"
$ElectronExe = Join-Path $AppDir "node_modules\electron\dist\electron.exe"

Write-Host ""
Write-Host "  PROXY TESTER" -ForegroundColor Cyan
Write-Host "  ============================================" -ForegroundColor DarkGray
Write-Host ""

# Pick npm: prefer system npm over the bundled copy
$SysNpm = (Get-Command npm -ErrorAction SilentlyContinue)
if ($SysNpm) {
    $NpmExe = $SysNpm.Source
} elseif (Test-Path $BundledNpm) {
    $NpmExe = $BundledNpm
    $env:Path = $NodeDir + ";" + $env:Path
} else {
    Write-Host "  ERROR: npm not found. Install Node.js from https://nodejs.org" -ForegroundColor Red
    pause
    exit 1
}

if (-not (Test-Path $Server)) {
    Write-Host "  ERROR: server.js not found in $AppDir" -ForegroundColor Red
    pause
    exit 1
}

$CanLaunchElectron = (Test-Path $MainJs) -and (Test-Path $PkgJson)

# First-time setup: install Electron if missing
if ($CanLaunchElectron -and (-not (Test-Path $ElectronExe))) {
    Write-Host "  First-time setup -- downloading Electron runtime (~80 MB)..." -ForegroundColor Yellow
    Write-Host ""

    $StdoutLog = [System.IO.Path]::GetTempFileName()
    $StderrLog = [System.IO.Path]::GetTempFileName()

    Push-Location $AppDir
    $Proc = Start-Process -FilePath $NpmExe `
        -ArgumentList @("install", "--prefer-offline", "--no-progress") `
        -WorkingDirectory $AppDir -PassThru -NoNewWindow `
        -RedirectStandardOutput $StdoutLog -RedirectStandardError $StderrLog
    Pop-Location

    $Spinner = @('|', '/', '-', '\')
    $SpinIdx = 0
    $StartTime = [DateTime]::Now
    $EstSecs = 50

    while (-not $Proc.HasExited) {
        $Elapsed   = [int]([DateTime]::Now - $StartTime).TotalSeconds
        $Remaining = [Math]::Max(0, $EstSecs - $Elapsed)
        $Spin      = $Spinner[$SpinIdx % 4]
        $Filled    = [Math]::Min(30, [int]($Elapsed / $EstSecs * 30))
        $Bar       = ('#' * $Filled) + ('-' * (30 - $Filled))
        Write-Host ("`r  $Spin  [$Bar]  ${Elapsed}s elapsed  ~${Remaining}s left   ") -NoNewline -ForegroundColor Yellow
        Start-Sleep -Milliseconds 250
        $SpinIdx++
    }

    $TotalSecs = [int]([DateTime]::Now - $StartTime).TotalSeconds

    if ($Proc.ExitCode -ne 0) {
        Write-Host "`r  [FAIL] Setup failed after ${TotalSecs}s.                                    " -ForegroundColor Red
        Write-Host ""
        Write-Host "  Error output:" -ForegroundColor Red
        if (Test-Path $StderrLog) {
            Get-Content $StderrLog | ForEach-Object { Write-Host "  $_" -ForegroundColor DarkRed }
        }
        if (Test-Path $StdoutLog) {
            Get-Content $StdoutLog | Select-Object -Last 20 | ForEach-Object { Write-Host "  $_" -ForegroundColor DarkGray }
        }
        Remove-Item $StdoutLog, $StderrLog -ErrorAction SilentlyContinue
        pause
        exit 1
    }

    Write-Host "`r  [OK]  Setup complete in ${TotalSecs}s.                                       " -ForegroundColor Green
    Write-Host ""
    Remove-Item $StdoutLog, $StderrLog -ErrorAction SilentlyContinue
}

# Launch
$env:PORT = $Port

if ($CanLaunchElectron -and (Test-Path $ElectronExe)) {
    Write-Host "  Launching Proxy Tester..." -ForegroundColor Green
    Push-Location $AppDir
    try {
        & $ElectronExe .
        if ($LASTEXITCODE -ne 0) {
            Write-Host "  Electron exited with code $LASTEXITCODE" -ForegroundColor Red
            pause
        }
        exit $LASTEXITCODE
    } finally {
        Pop-Location
    }
}

Write-Host "  Electron unavailable -- falling back to server-only mode." -ForegroundColor Yellow
& $NodeExe $Server
