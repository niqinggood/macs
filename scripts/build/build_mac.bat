@echo off
setlocal EnableExtensions EnableDelayedExpansion
set "INTEL_FILE="
set "APPLE_FILE="
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
echo Building macs for macOS...
echo ============================================

rem Double-click to enter a version, or pass it as the first argument.
rem Fast build:    build_mac.bat 2.0.0
rem Release check: build_mac.bat 2.0.0 release
set "DEFAULT_VER=2.0.0"
set "RAW_VER=%~1"
if defined INTERACTIVE set /p RAW_VER="Version code (default: !DEFAULT_VER!): "
set "VER=!RAW_VER!"
if not defined VER set "VER=!DEFAULT_VER!"

if not "%~3"=="" goto :failed
powershell.exe -NoProfile -Command "if ($env:VER -cnotmatch '^[0-9A-Za-z][0-9A-Za-z.-]*$') { exit 2 }"
if errorlevel 1 (
    echo ERROR: Invalid version. Use letters, numbers, dots, and hyphens only.
    goto :failed
)

set "BUILD_MODE=%~2"
if not defined BUILD_MODE set "BUILD_MODE=fast"
if /i not "!BUILD_MODE!"=="fast" if /i not "!BUILD_MODE!"=="release" (
    echo ERROR: Invalid build mode "!BUILD_MODE!". Use fast or release.
    goto :failed
)

:: Inject the actual UTC build date instead of the application's fallback date.
set "BUILD_DATE="
for /f "delims=" %%I in ('powershell.exe -NoProfile -Command "[DateTime]::UtcNow.ToString('yyyy-MM-dd')"') do set "BUILD_DATE=%%I"
if not defined BUILD_DATE (
    echo ERROR: Cannot determine the UTC build date.
    goto :failed
)

where go >nul 2>&1
if errorlevel 1 (
    echo ERROR: Go was not found in PATH.
    goto :failed
)

set "GOOS=windows"
set "GOARCH=amd64"
set "CGO_ENABLED=0"
go run ./cmd/releaseguard
if errorlevel 1 goto :failed_with_current_code

echo Version: !VER!
echo Mode:    !BUILD_MODE!
go version

if /i "!BUILD_MODE!"=="release" (
    echo.
    echo Running release quality gate...
    powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%PROJECT_DIR%\scripts\quality_gate.ps1"
    if errorlevel 1 (
        echo Release quality gate failed. Build stopped.
        goto :failed
    )
)

if /i "!BUILD_MODE!"=="fast" (
    echo Fast mode: skipping the release quality gate.
)

echo.
echo [1/2] Building for Intel Mac (amd64)...
set "INTEL_FILE=openkitty-!VER!-mac-intel"
set "APPLE_FILE=openkitty-!VER!-mac-apple"
for %%F in ("!INTEL_FILE!" "!APPLE_FILE!") do (
    if exist "%%~F\" (
        echo ERROR: Output path is a directory: %%~F
        goto :failed
    )
)
for %%F in ("!INTEL_FILE!.build" "!APPLE_FILE!.build") do (
    if exist "%%~F" del /f /q "%%~F" >nul 2>&1
    if exist "%%~F" goto :failed
)
set "GOOS=darwin"
set "GOARCH=amd64"
set "CGO_ENABLED=0"
go build -buildvcs=false -ldflags="-s -w -X main.CurrentVersion=!VER! -X mac/internal/app.build=!BUILD_DATE!" -trimpath -tags netgo,osusergo -o "!INTEL_FILE!.build" ./cmd/macs

if errorlevel 1 (
    echo Build failed!
    goto :failed_with_current_code
)
echo Intel version compiled: openkitty-!VER!-mac-intel

echo.
echo [2/2] Building for Apple Silicon (arm64)...
set "GOARCH=arm64"
go build -buildvcs=false -ldflags="-s -w -X main.CurrentVersion=!VER! -X mac/internal/app.build=!BUILD_DATE!" -trimpath -tags netgo,osusergo -o "!APPLE_FILE!.build" ./cmd/macs

if errorlevel 1 (
    echo Build failed!
    goto :failed_with_current_code
)
move /y "!INTEL_FILE!.build" "!INTEL_FILE!" >nul
if errorlevel 1 goto :failed_with_current_code
move /y "!APPLE_FILE!.build" "!APPLE_FILE!" >nul
if errorlevel 1 goto :failed_with_current_code
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

:failed
set "BUILD_ERROR=1"
goto :cleanup
:failed_with_current_code
set "BUILD_ERROR=!errorlevel!"
if "!BUILD_ERROR!"=="0" set "BUILD_ERROR=1"
:cleanup
if defined INTEL_FILE if exist "!INTEL_FILE!.build" del /f /q "!INTEL_FILE!.build" >nul 2>&1
if defined APPLE_FILE if exist "!APPLE_FILE!.build" del /f /q "!APPLE_FILE!.build" >nul 2>&1
echo ERROR: Build stopped with exit code !BUILD_ERROR!.
popd
if defined INTERACTIVE pause
exit /b !BUILD_ERROR!

:usage
echo Usage: build_mac.bat [version] [fast^|release]
echo Default version: 2.0.0. Default mode: fast.
echo release: run the full quality gate before compiling both architectures.
exit /b 0
