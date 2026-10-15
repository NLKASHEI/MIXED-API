@echo off
chcp 65001 >nul
cd /d "%~dp0"
echo MIXED-API ^| http://127.0.0.1:8765
if not exist "MIXED-API.exe" (
  echo ERROR: MIXED-API.exe not found in this folder.
  pause
  exit /b 1
)
"%~dp0MIXED-API.exe"
if errorlevel 1 pause
