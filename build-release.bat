@echo off
title ProxyKit Release Build
cd /d "%~dp0"

:: ── Read version from config.go ───────────────────────────────────────────────
for /f "tokens=3 delims= " %%v in ('findstr /C:"AppVersion" config.go') do (
    set RAW=%%v
)
:: Strip quotes
set VERSION=%RAW:"=%

echo.
echo  ██████╗ ██████╗  ██████╗ ██╗  ██╗██╗   ██╗██╗  ██╗██╗████████╗
echo  ██╔══██╗██╔══██╗██╔═══██╗╚██╗██╔╝╚██╗ ██╔╝██║ ██╔╝██║╚══██╔══╝
echo  ██████╔╝██████╔╝██║   ██║ ╚███╔╝  ╚████╔╝ █████╔╝ ██║   ██║
echo  ██╔═══╝ ██╔══██╗██║   ██║ ██╔██╗   ╚██╔╝  ██╔═██╗ ██║   ██║
echo  ██║     ██║  ██║╚██████╔╝██╔╝ ██╗   ██║   ██║  ██╗██║   ██║
echo  ╚═╝     ╚═╝  ╚═╝ ╚═════╝ ╚═╝  ╚═╝   ╚═╝   ╚═╝  ╚═╝╚═╝   ╚═╝
echo.
echo  Release Build  v%VERSION%
echo  ────────────────────────────────────────────────────────────────
echo.

:: ── Embed icon + manifest via goversioninfo ───────────────────────────────────
echo  [1/4]  Generating resource.syso (icon + version info) ...
go generate ./...
if errorlevel 1 (
    echo.
    echo  WARNING: go generate failed. Icon may not be embedded.
    echo  Install goversioninfo: go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest
    echo  Continuing build without embedded icon...
    echo.
)

:: ── Build exe ─────────────────────────────────────────────────────────────────
echo  [2/4]  Compiling ProxyKit.exe ...
go build -ldflags="-H windowsgui -s -w" -trimpath -o ProxyKit.exe .
if errorlevel 1 (
    echo.
    echo  BUILD FAILED. Check errors above.
    pause
    exit /b 1
)
echo         Done  (%~dp0ProxyKit.exe)

:: ── Create release folder ─────────────────────────────────────────────────────
echo  [3/4]  Assembling release package ...
set RELDIR=%~dp0release\ProxyKit-v%VERSION%
if exist "%RELDIR%" rmdir /s /q "%RELDIR%"
mkdir "%RELDIR%"

copy /y "ProxyKit.exe"  "%RELDIR%\ProxyKit.exe"  >nul
copy /y "install.bat"   "%RELDIR%\install.bat"   >nul

:: ── Zip the release folder ────────────────────────────────────────────────────
echo  [4/4]  Zipping ...
set ZIPOUT=%~dp0release\ProxyKit-v%VERSION%.zip
if exist "%ZIPOUT%" del "%ZIPOUT%"

powershell -NoProfile -Command "Compress-Archive -Path '%RELDIR%\*' -DestinationPath '%ZIPOUT%'"

echo.
echo  ────────────────────────────────────────────────────────────────
echo   Release ready:
echo     %ZIPOUT%
echo.
echo   Contents of release package:
dir /b "%RELDIR%"
echo.
echo   Upload ProxyKit.exe as a GitHub release asset, then publish
echo   the release through your admin panel.
echo  ────────────────────────────────────────────────────────────────
echo.
pause
