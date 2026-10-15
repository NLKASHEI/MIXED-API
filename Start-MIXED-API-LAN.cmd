@echo off
chcp 65001 >nul
cd /d "%~dp0"
echo MIXED-API LAN Gateway for Android ^| Windows admin only: http://127.0.0.1:8765
echo LAN API port: 8766 ^| Requires your Windows Firewall permission on PRIVATE networks.
echo Find PC LAN IPv4 using: ipconfig
if not exist "MIXED-API.exe" (
  echo ERROR: MIXED-API.exe missing.
  pause
  exit /b 1
)
"%~dp0MIXED-API.exe" --lan
if errorlevel 1 pause
