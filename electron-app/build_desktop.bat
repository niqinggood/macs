@echo off
echo ============================================
echo Building MACS Desktop App
echo ============================================

:: 1. Ensure Go backend is built
echo.
echo [1/3] Building Go backend...
cd ..
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%CD%\scripts\quality_gate.ps1"
if %errorlevel% neq 0 (
    echo Release quality gate failed. Build stopped.
    pause
    exit /b %errorlevel%
)
go build -ldflags="-s -w -X main.CurrentVersion=1.0.0" -trimpath -o openkitty.exe ./cmd/macs
if %errorlevel% neq 0 (
    echo Go build failed!
    pause
    exit /b %errorlevel%
)
echo   openkitty.exe ready
cd electron-app

:: 2. Install npm dependencies if needed
echo.
echo [2/3] Installing npm dependencies...
if not exist node_modules (
    call npm install
)

:: 3. Package Electron app
echo.
echo [3/3] Packaging Electron app...
call npm run build:win

echo.
echo ============================================
echo Build complete!
echo   dist/MACS Setup *.exe  (installer)
echo   dist/MACS *.exe        (portable)
echo ============================================
pause
