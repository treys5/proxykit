@echo off
title ProxyKit Dev Build
cd /d "%~dp0"

echo Building ProxyKit...
go build -ldflags="-H windowsgui" -o ProxyKit.exe .
if errorlevel 1 (
    echo.
    echo Build failed. Make sure Go is installed: https://go.dev/dl/
    pause
    exit /b 1
)

start "" "ProxyKit.exe"
