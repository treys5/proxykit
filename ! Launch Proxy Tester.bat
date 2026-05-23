@echo off
setlocal
title Proxy Tester
cd /d "%~dp0"

:: ── Case 1: packaged distribution — just run the exe ─────────────────────────
if exist "%~dp0Proxy Tester.exe" (
    start "" "%~dp0Proxy Tester.exe"
    exit /b 0
)

:: ── Case 2: source folder — delegate everything to the PowerShell launcher ───
::    ProxyTester.ps1 handles first-time setup (with progress bar) and launch.
powershell -ExecutionPolicy Bypass -NoProfile -File "%~dp0ProxyTester.ps1"
if errorlevel 1 (
    echo.
    echo   Launch failed. See error above.
    echo.
    pause
    exit /b 1
)
