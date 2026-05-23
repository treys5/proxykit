@echo off
title Proxy Tester
cd /d "%~dp0"

set PORT=8080
set DISTEXE=%~dp0dist\Proxy Tester-win32-x64\Proxy Tester.exe
set DISTAPP=%~dp0dist\Proxy Tester-win32-x64\resources\app

if exist "%DISTEXE%" (
    if exist "%DISTAPP%" (
        copy /y "%~dp0index.html" "%DISTAPP%\index.html" >nul
        copy /y "%~dp0server.js" "%DISTAPP%\server.js" >nul
        copy /y "%~dp0main.js" "%DISTAPP%\main.js" >nul
    )
    start "" "%DISTEXE%"
    goto end
)

powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0ProxyTester.ps1" -Port %PORT%
if errorlevel 1 pause

:end
