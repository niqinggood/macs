@echo off
setlocal EnableExtensions EnableDelayedExpansion
set "INTERACTIVE="
if "%~1"=="" set "INTERACTIVE=1"
for %%I in ("%~dp0..\..") do set "PROJECT_DIR=%%~fI"
pushd "%PROJECT_DIR%" >nul
if errorlevel 1 (
    echo ERROR: Cannot enter project directory: %PROJECT_DIR%
    if defined INTERACTIVE pause
    exit /b 1
)

echo ============================================
echo Building macs for macOS...
echo ============================================

rem Double-click to enter a version, or pass it as the first argument.
rem Fast build:    build_mac.bat 1.1.8
rem Release check: build_mac.bat 1.1.8 release
set "DEFAULT_VER=1.1.8"
set "RAW_VER=%~1"
if defined INTERACTIVE set /p RAW_VER="Version code (default: !DEFAULT_VER!): "
set "VER="
for /f "tokens=1" %%A in ("!RAW_VER!") do set "VER=%%A"
if not defined VER set "VER=!DEFAULT_VER!"

set "INVALID_VER="
for /f "delims=0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz.-" %%A in ("!VER!") do set "INVALID_VER=%%A"
if defined INVALID_VER (
    echo ERROR: Invalid version "!VER!". Use letters, numbers, dots, and hyphens only.
    popd
    if defined INTERACTIVE pause
    exit /b 1
)

set "BUILD_MODE=%~2"
if not defined BUILD_MODE set "BUILD_MODE=fast"
if /i not "!BUILD_MODE!"=="fast" if /i not "!BUILD_MODE!"=="release" (
    echo ERROR: Invalid build mode "!BUILD_MODE!". Use fast or release.
    popd
    if defined INTERACTIVE pause
    exit /b 1
)

:: Inject the actual UTC build date instead of the application's fallback date.
set "BUILD_DATE="
for /f "delims=" %%I in ('powershell.exe -NoProfile -Command "[DateTime]::UtcNow.ToString('yyyy-MM-dd')"') do set "BUILD_DATE=%%I"
if not defined BUILD_DATE (
    echo ERROR: Cannot determine the UTC build date.
    if defined INTERACTIVE pause
    popd
    exit /b 1
)

where go >nul 2>&1
if errorlevel 1 (
    echo ERROR: Go was not found in PATH.
    popd
    if defined INTERACTIVE pause
    exit /b 1
)

echo Version: !VER!
echo Mode:    !BUILD_MODE!
go version

if /i "!BUILD_MODE!"=="release" (
    echo.
    echo Running release quality gate...
    powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%PROJECT_DIR%\scripts\quality_gate.ps1"
    if errorlevel 1 (
        echo Release quality gate failed. Build stopped.
        popd
        if defined INTERACTIVE pause
        exit /b 1
    )
)

if /i "!BUILD_MODE!"=="fast" (
    echo Fast mode: skipping the release quality gate.
)

echo.
echo [1/2] Building for Intel Mac (amd64)...
set "GOOS=darwin"
set "GOARCH=amd64"
set "CGO_ENABLED=0"
go build -buildvcs=false -ldflags="-s -w -X main.CurrentVersion=!VER! -X mac/internal/app.build=!BUILD_DATE!" -trimpath -tags netgo,osusergo -o openkitty-!VER!-mac-intel ./cmd/openkitty

if errorlevel 1 (
    echo Build failed!
    popd
    if defined INTERACTIVE pause
    exit /b 1
)
echo Intel version compiled: openkitty-!VER!-mac-intel

echo.
echo [2/2] Building for Apple Silicon (arm64)...
set "GOARCH=arm64"
go build -buildvcs=false -ldflags="-s -w -X main.CurrentVersion=!VER! -X mac/internal/app.build=!BUILD_DATE!" -trimpath -tags netgo,osusergo -o openkitty-!VER!-mac-apple ./cmd/openkitty

if errorlevel 1 (
    echo Build failed!
    popd
    if defined INTERACTIVE pause
    exit /b 1
)
echo Apple Silicon compiled: openkitty-!VER!-mac-apple

echo.
echo ============================================
echo Build complete! Output:
echo   - openkitty-!VER!-mac-intel   (Intel Mac)
echo   - openkitty-!VER!-mac-apple  (Apple Silicon)
echo.
echo IMPORTANT: After copying to Mac, run: chmod +x openkitty-*-mac-*
echo ============================================
popd
if defined INTERACTIVE pause
exit /b 0
