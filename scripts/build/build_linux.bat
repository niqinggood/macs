@echo off
setlocal EnableExtensions EnableDelayedExpansion
set "TEMP_FILE="
if /i "%~1"=="--help" goto :usage
if /i "%~1"=="-h" goto :usage
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
:: Command line (version first): build_linux.bat 2.0.0 [amd64^|arm64]
:: Legacy order is also accepted: build_linux.bat [amd64^|arm64] 2.0.0
set "DEFAULT_VER=2.0.0"
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

set "VER=!RAW_VER!"
if not defined VER set "VER=!DEFAULT_VER!"
if not defined ARCH set "ARCH=!DEFAULT_ARCH!"
if not "%~3"=="" goto :failed
if /i "!ARCH!"=="AMD64" set "ARCH=amd64"
if /i "!ARCH!"=="ARM64" set "ARCH=arm64"

if /i not "!ARCH!"=="amd64" if /i not "!ARCH!"=="arm64" (
    echo ERROR: Unsupported architecture "!ARCH!". Use amd64 or arm64.
    goto :failed
)

powershell.exe -NoProfile -Command "if ($env:VER -cnotmatch '^[0-9A-Za-z][0-9A-Za-z.-]*$') { exit 2 }"
if errorlevel 1 (
    echo ERROR: Invalid version. Use letters, numbers, dots, and hyphens only.
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

rem Credential scanning runs on the host before switching to the target platform.
set "GOOS=windows"
set "GOARCH=amd64"
set "CGO_ENABLED=0"
go run ./cmd/releaseguard
if errorlevel 1 (
    set "BUILD_ERROR=!errorlevel!"
    goto :failed_with_code
)

set "OUTPUT_DIR=%PROJECT_DIR%\dist"
set "OUTPUT_FILE=!OUTPUT_DIR!\openkitty_linux_!ARCH!"
set "TEMP_FILE=!OUTPUT_FILE!.build"
if exist "!OUTPUT_FILE!\" (
    echo ERROR: Output path is a directory: !OUTPUT_FILE!
    goto :failed
)
if not exist "!OUTPUT_DIR!" mkdir "!OUTPUT_DIR!"
if not exist "!OUTPUT_DIR!" (
    echo ERROR: Cannot create output directory: !OUTPUT_DIR!
    goto :failed
)
if exist "!TEMP_FILE!" del /f /q "!TEMP_FILE!" >nul 2>&1
if exist "!TEMP_FILE!" (
    echo ERROR: Cannot remove temporary output: !TEMP_FILE!
    goto :failed
)

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
if defined TEMP_FILE if exist "!TEMP_FILE!" del /f /q "!TEMP_FILE!" >nul 2>&1
if defined INTERACTIVE pause
popd
exit /b !BUILD_ERROR!

:usage
echo Usage: build_linux.bat [version] [amd64^|arm64]
echo Legacy order: build_linux.bat [amd64^|arm64] [version]
echo Defaults: 2.0.0, amd64. No arguments: prompt for both.
exit /b 0
