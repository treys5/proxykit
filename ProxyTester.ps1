param(
    [int]$Port = 8080,
    [string]$UpdateUrl = '',
    [switch]$SkipUpdateCheck
)

$AppDir      = Split-Path -Parent $MyInvocation.MyCommand.Path
$NodeDir     = Join-Path $AppDir "node"
$NodeExe     = Join-Path $NodeDir "node.exe"
$BundledNpm  = Join-Path $NodeDir "npm.cmd"
$Server      = Join-Path $AppDir "server.js"
$MainJs      = Join-Path $AppDir "main.js"
$PkgJson     = Join-Path $AppDir "package.json"
$VersionFile = Join-Path $AppDir "version.json"
$ElectronExe = Join-Path $AppDir "node_modules\electron\dist\electron.exe"

$GithubRepo  = "treys5/proxykit"
$GithubApi   = "https://api.github.com/repos/$GithubRepo/releases/latest"

Write-Host ""
Write-Host "  PROXY TESTER" -ForegroundColor Cyan
Write-Host "  ============================================" -ForegroundColor DarkGray
Write-Host ""

# ── Pick npm ──────────────────────────────────────────────────────────────────
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

# ── Read local version ────────────────────────────────────────────────────────
function Get-LocalVersion {
    if (Test-Path $VersionFile) {
        try {
            $raw = Get-Content $VersionFile -Raw | ConvertFrom-Json
            return [string]$raw.version
        } catch {}
    }
    # Fallback: parse APP_VERSION from server.js
    try {
        $line = Select-String -Path $Server -Pattern "APP_VERSION\s*=\s*'([^']+)'" | Select-Object -First 1
        if ($line) { return $line.Matches[0].Groups[1].Value }
    } catch {}
    return "0.0.0"
}

# ── Compare semantic versions (returns 1 if $a > $b, -1 if $a < $b, 0 equal) ─
function Compare-SemVer([string]$a, [string]$b) {
    $pa = ($a -replace '[^0-9.]','').Split('.') | ForEach-Object { [int]$_ }
    $pb = ($b -replace '[^0-9.]','').Split('.') | ForEach-Object { [int]$_ }
    for ($i = 0; $i -lt [Math]::Max($pa.Count, $pb.Count); $i++) {
        $va = if ($i -lt $pa.Count) { $pa[$i] } else { 0 }
        $vb = if ($i -lt $pb.Count) { $pb[$i] } else { 0 }
        if ($va -gt $vb) { return 1 }
        if ($va -lt $vb) { return -1 }
    }
    return 0
}

# ── Download file with progress ───────────────────────────────────────────────
function Get-FileWithProgress([string]$Uri, [string]$OutFile) {
    $wc = New-Object System.Net.WebClient
    $wc.Headers.Add("User-Agent", "ProxyTester-Updater")
    $done = $false
    $lastPct = -1
    $wc.DownloadProgressChanged += {
        $pct = $_.ProgressPercentage
        if ($pct -ne $lastPct) {
            $lastPct = $pct
            $filled = [Math]::Min(30, [int]($pct / 100 * 30))
            $bar = ('#' * $filled) + ('-' * (30 - $filled))
            Write-Host ("`r  [$bar] $pct%   ") -NoNewline -ForegroundColor Yellow
        }
    }
    $wc.DownloadFileCompleted += { $done = $true }
    $wc.DownloadFileAsync([Uri]$Uri, $OutFile)
    while (-not $done) { Start-Sleep -Milliseconds 200 }
    Write-Host ""
}

# ── Apply update: extract zip and copy source files over ─────────────────────
function Install-Update([string]$ZipPath, [string]$TargetDir) {
    $TmpDir = Join-Path $env:TEMP "ProxyTesterUpdate_$(Get-Random)"
    try {
        Write-Host "  Extracting update..." -ForegroundColor Yellow
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        [System.IO.Compression.ZipFile]::ExtractToDirectory($ZipPath, $TmpDir)

        # The zip may have a single top-level folder (GitHub archive style)
        # Find the actual content root
        $children = Get-ChildItem $TmpDir
        $SrcDir = $TmpDir
        if ($children.Count -eq 1 -and $children[0].PSIsContainer) {
            $SrcDir = $children[0].FullName
        }

        # Files we update (everything except node_modules and user data)
        $UpdateFiles = @("server.js", "index.html", "main.js", "package.json",
                         "ProxyTester.ps1", "version.json")

        $copied = 0
        foreach ($f in $UpdateFiles) {
            $src = Join-Path $SrcDir $f
            $dst = Join-Path $TargetDir $f
            if (Test-Path $src) {
                Copy-Item -Path $src -Destination $dst -Force
                $copied++
                Write-Host "    Updated: $f" -ForegroundColor DarkGray
            }
        }
        Write-Host "  [OK] $copied file(s) updated." -ForegroundColor Green
        return $true
    } catch {
        Write-Host "  [ERROR] Failed to apply update: $_" -ForegroundColor Red
        return $false
    } finally {
        if (Test-Path $TmpDir) { Remove-Item $TmpDir -Recurse -Force -ErrorAction SilentlyContinue }
        if (Test-Path $ZipPath) { Remove-Item $ZipPath -Force -ErrorAction SilentlyContinue }
    }
}

# ── Auto-update check ─────────────────────────────────────────────────────────
if (-not $SkipUpdateCheck) {
    $LocalVersion = Get-LocalVersion
    Write-Host "  Version: $LocalVersion" -ForegroundColor DarkGray

    try {
        Write-Host "  Checking for updates..." -ForegroundColor DarkGray
        $wc = New-Object System.Net.WebClient
        $wc.Headers.Add("User-Agent", "ProxyTester-Updater")
        $json = $wc.DownloadString($GithubApi) | ConvertFrom-Json
        $LatestVersion = ($json.tag_name -replace '^v','')
        $LatestTag     = $json.tag_name

        if ((Compare-SemVer $LatestVersion $LocalVersion) -gt 0) {
            Write-Host ""
            Write-Host "  UPDATE AVAILABLE: v$LocalVersion  →  v$LatestVersion" -ForegroundColor Cyan
            Write-Host "  $($json.name)" -ForegroundColor White
            Write-Host ""

            # Find the update zip asset
            $Asset = $json.assets | Where-Object { $_.name -like "*update*" -or $_.name -like "*.zip" } | Select-Object -First 1
            if (-not $Asset) {
                # Fall back to GitHub's auto-generated source zip
                $Asset = @{ browser_download_url = "https://github.com/$GithubRepo/archive/refs/tags/$LatestTag.zip"; name = "source.zip" }
            }

            $response = Read-Host "  Install update now? [Y/N]"
            if ($response -match '^[Yy]') {
                Write-Host ""
                Write-Host "  Downloading $($Asset.name)..." -ForegroundColor Yellow
                $TmpZip = Join-Path $env:TEMP "ProxyTester-update-$(Get-Random).zip"
                Get-FileWithProgress -Uri $Asset.browser_download_url -OutFile $TmpZip

                $ok = Install-Update -ZipPath $TmpZip -TargetDir $AppDir
                if ($ok) {
                    Write-Host ""
                    Write-Host "  Relaunching with updated version..." -ForegroundColor Green
                    Start-Sleep -Seconds 1
                    # Relaunch this same script (now updated) and exit
                    $newScript = Join-Path $AppDir "ProxyTester.ps1"
                    Start-Process powershell.exe -ArgumentList "-NoProfile -ExecutionPolicy Bypass -File `"$newScript`" -Port $Port -SkipUpdateCheck"
                    exit 0
                }
            } else {
                Write-Host "  Skipping update — launching current version." -ForegroundColor DarkGray
            }
            Write-Host ""
        } else {
            Write-Host "  Up to date." -ForegroundColor DarkGray
        }
    } catch {
        # Network or parse error — just continue launching
        Write-Host "  Update check failed (offline?): $($_.Exception.Message)" -ForegroundColor DarkGray
    }
}

# ── First-time Electron setup ─────────────────────────────────────────────────
$CanLaunchElectron = (Test-Path $MainJs) -and (Test-Path $PkgJson)

if ($CanLaunchElectron -and (-not (Test-Path $ElectronExe))) {
    Write-Host ""
    Write-Host "  First-time setup -- downloading Electron runtime (~80 MB)..." -ForegroundColor Yellow
    Write-Host ""

    $StartTime = [DateTime]::Now
    $Job = Start-Job -ScriptBlock {
        param($npmExe, $workDir)
        Set-Location $workDir
        try {
            if ($npmExe -match '\.(cmd|bat)$') {
                & cmd.exe /c "`"$npmExe`" install --prefer-offline --no-progress" 2>&1
            } else {
                & $npmExe install --prefer-offline --no-progress 2>&1
            }
            exit $LASTEXITCODE
        } catch {
            Write-Output "ERROR: $_"
            exit 1
        }
    } -ArgumentList $NpmExe, $AppDir

    $Spinner = @('|', '/', '-', '\')
    $SpinIdx = 0
    $EstSecs = 90

    while ($Job.State -eq 'Running') {
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
    $JobOutput = Receive-Job -Job $Job
    $ExitOk    = $Job.State -eq 'Completed'
    Remove-Job -Job $Job -Force

    if (-not $ExitOk) {
        Write-Host "`r  [FAIL] Setup failed after ${TotalSecs}s.                                    " -ForegroundColor Red
        Write-Host ""
        Write-Host "  Error output:" -ForegroundColor Red
        $JobOutput | Select-Object -Last 20 | ForEach-Object { Write-Host "  $_" -ForegroundColor DarkRed }
        pause
        exit 1
    }

    Write-Host "`r  [OK]  Setup complete in ${TotalSecs}s.                                       " -ForegroundColor Green
    Write-Host ""
}

# ── Launch ────────────────────────────────────────────────────────────────────
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
