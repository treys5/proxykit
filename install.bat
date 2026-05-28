@echo off
setlocal
title ProxyKit Installer

set "DEST=%LOCALAPPDATA%\ProxyKit"
set "EXE_DST=%DEST%\ProxyKit.exe"
set "EXE_SRC=%~dp0ProxyKit.exe"

echo.
echo   PROXYKIT INSTALLER
echo   ==========================================
echo.

:: ── Verify the exe is present ──────────────────────────────────────────────
if not exist "%EXE_SRC%" (
    echo   [!] ProxyKit.exe not found next to this installer.
    echo       Download both files from:
    echo       https://github.com/treys5/proxykit/releases/latest
    echo.
    pause
    exit /b 1
)

:: ── Stop any running instance ──────────────────────────────────────────────
taskkill /F /IM ProxyKit.exe >nul 2>&1
timeout /t 1 /nobreak >nul 2>&1

:: ── Copy to install dir ────────────────────────────────────────────────────
echo   [>>] Installing to %DEST% ...
if not exist "%DEST%" mkdir "%DEST%"

copy /y "%EXE_SRC%" "%EXE_DST%" >nul
if errorlevel 1 (
    echo   [!] Could not copy ProxyKit.exe — try running as Administrator.
    echo.
    pause
    exit /b 1
)
echo   [OK] Installed ProxyKit.exe

:: ── Desktop shortcut (via PowerShell — works on all Windows 10/11 machines) ─
echo   [>>] Creating desktop shortcut ...
powershell -NoProfile -ExecutionPolicy Bypass -Command "$wsh = New-Object -ComObject WScript.Shell; $lnk = $wsh.CreateShortcut([IO.Path]::Combine([Environment]::GetFolderPath('Desktop'), 'ProxyKit.lnk')); $lnk.TargetPath = $env:EXE_DST; $lnk.WorkingDirectory = $env:DEST; $lnk.Description = 'ProxyKit — Enterprise Proxy Testing Suite'; $lnk.IconLocation = $env:EXE_DST + ',0'; $lnk.Save()"

if errorlevel 1 (
    echo   [!] Shortcut skipped ^(non-critical^)
) else (
    echo   [OK] Desktop shortcut created
)

:: ── Launch ─────────────────────────────────────────────────────────────────
echo   [>>] Launching ProxyKit ...
start "" "%EXE_DST%"

echo.
echo   ==========================================
echo   [OK] ProxyKit installed and running!
echo   [OK] Use the desktop shortcut from now on.
echo.
echo       Location: %DEST%
echo.
timeout /t 4 /nobreak >nul
