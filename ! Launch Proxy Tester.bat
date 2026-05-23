@echo off
setlocal
title Proxy Tester
cd /d "%~dp0"

echo.
echo   PROXY TESTER
echo   ============================================
echo.

:: ?? Case 1: Running from the built distribution ??????????????????????????
::    The self-contained Proxy Tester.exe is right here ? just run it.
if exist "%~dp0Proxy Tester.exe" (
    echo   Launching...
    start "" "%~dp0Proxy Tester.exe"
    exit /b 0
)

:: ?? Case 2: Running from the source folder ???????????????????????????????
set "NODE=%~dp0node\node.exe"
set "NPM=%~dp0node\npm.cmd"
set "ELECTRON=%~dp0node_modules\electron\dist\electron.exe"

if not exist "%NODE%" (
    echo   ERROR: Node.js runtime not found in .\node\
    echo.
    echo   The .\node\ folder is missing or incomplete.
    echo   Please re-download the full package.
    echo.
    pause
    exit /b 1
)

if not exist "%ELECTRON%" (
    echo   First-time setup: downloading Electron runtime...
    echo   This is about 80 MB and only happens once.
    echo.
    set "PATH=%~dp0node;%PATH%"
    "%NPM%" install --prefer-offline
    if errorlevel 1 (
        echo.
        echo   Setup failed. Check your internet connection and try again.
        echo.
        pause
        exit /b 1
    )
    echo.
    echo   Setup complete!
    echo.
)

echo   Launching...
set "PATH=%~dp0node;%PATH%"
start "" "%ELECTRON%" "%~dp0."
exit /b 0
