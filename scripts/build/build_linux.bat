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
echo Building macs for Linux
echo ============================================

:: Double-click: prompts for version and architecture.
:: Command line (version first): build_linux.bat 1.1.8 [amd64^|arm64]
:: Legacy order is also accepted: build_linux.bat [amd64^|arm64] 1.1.8
set "DEFAULT_VER=1.1.8"
set "DEFAULT_ARCH=amd64"
set "ARG1=%~1"
set "ARG2=%~2"
set "INTERACTIVE="
set "RAW_VER="
set "ARCH="

if not defined ARG1 (
    set "INTERACTIVE=1"
    set /p RAW_VER="Version code (default: !DEFAULT_VER!): "
    set /p ARCH="Architecture (amd64/arm64, default: !DEFAULT_ARCH!): "
) else if /i "!ARG1!"=="amd64" (
    set "ARCH=amd64"
    set "RAW_VER=!ARG2!"
) else if /i "!ARG1!"=="arm64" (
    set "ARCH=arm64"
    set "RAW_VER=!ARG2!"
) else (
    set "RAW_VER=!ARG1!"
    set "ARCH=!ARG2!"
)

set "VER="
for /f "tokens=1" %%A in ("!RAW_VER!") do set "VER=%%A"
if not defined VER set "VER=!DEFAULT_VER!"
if not defined ARCH set "ARCH=!DEFAULT_ARCH!"

if /i not "!ARCH!"=="amd64" if /i not "!ARCH!"=="arm64" (
    echo ERROR: Unsupported architecture "!ARCH!". Use amd64 or arm64.
    goto :failed
)

set "INVALID_VER="
for /f "delims=0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz.-" %%A in ("!VER!") do set "INVALID_VER=%%A"
if defined INVALID_VER (
    echo ERROR: Invalid version "!VER!". Use letters, numbers, dots, and hyphens only.
    goto :failed
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
    goto :failed
)

set "OUTPUT_DIR=%PROJECT_DIR%\dist"
set "OUTPUT_FILE=!OUTPUT_DIR!\openkitty_linux_!ARCH!"
set "TEMP_FILE=!OUTPUT_FILE!.build"
if not exist "!OUTPUT_DIR!" mkdir "!OUTPUT_DIR!"
if not exist "!OUTPUT_DIR!" (
    echo ERROR: Cannot create output directory: !OUTPUT_DIR!
    goto :failed
)
if exist "!TEMP_FILE!" del /f /q "!TEMP_FILE!" >nul 2>&1

echo.
echo Version:      !VER!
echo Architecture: linux/!ARCH!
go version
echo.
echo Building...

set "GOOS=linux"
set "GOARCH=!ARCH!"
set "CGO_ENABLED=0"
go build -buildvcs=false -trimpath -tags netgo,osusergo -ldflags="-s -w -X main.CurrentVersion=!VER! -X mac/internal/app.build=!BUILD_DATE!" -o "!TEMP_FILE!" ./cmd/macs
set "BUILD_ERROR=!errorlevel!"
if not "!BUILD_ERROR!"=="0" (
    echo.
    echo ERROR: Go build failed with exit code !BUILD_ERROR!.
    if exist "!TEMP_FILE!" del /f /q "!TEMP_FILE!" >nul 2>&1
    goto :failed_with_code
)

move /y "!TEMP_FILE!" "!OUTPUT_FILE!" >nul
if errorlevel 1 (
    echo ERROR: Cannot replace output file: !OUTPUT_FILE!
    goto :failed
)

echo.
echo ============================================
echo Build complete
echo   !OUTPUT_FILE!
for %%A in ("!OUTPUT_FILE!") do echo   %%~zA bytes
echo   Version: !VER!
echo ============================================
if defined INTERACTIVE pause
popd
exit /b 0

:failed
set "BUILD_ERROR=1"

:failed_with_code
if defined INTERACTIVE pause
popd
exit /b !BUILD_ERROR!
