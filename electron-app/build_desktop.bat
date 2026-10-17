@echo off
setlocal EnableExtensions EnableDelayedExpansion
if /i "%~1"=="--help" goto :usage
if /i "%~1"=="-h" goto :usage
set "INTERACTIVE="
if "%~1"=="" set "INTERACTIVE=1"
for %%I in ("%~dp0..") do set "PROJECT_DIR=%%~fI"
pushd "%PROJECT_DIR%" >nul
if errorlevel 1 exit /b 1
set "VER=%~1"
if defined INTERACTIVE set /p VER="Version code (default: 2.0.0): "
if not defined VER set "VER=2.0.0"
set "BUILD_MODE=%~2"
if not defined BUILD_MODE set "BUILD_MODE=release"
if not "%~3"=="" goto :failed
if /i not "!BUILD_MODE!"=="release" if /i not "!BUILD_MODE!"=="fast" goto :failed
powershell.exe -NoProfile -Command "if ($env:VER -cnotmatch '^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$') { exit 2 }"
if errorlevel 1 goto :failed
where node >nul 2>&1
if errorlevel 1 (
    echo ERROR: Node.js was not found in PATH.
    goto :failed
)
where npm >nul 2>&1
if errorlevel 1 (
    echo ERROR: npm was not found in PATH.
    goto :failed
)
if /i "!BUILD_MODE!"=="release" (
    powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%PROJECT_DIR%\scripts\quality_gate.ps1"
    if errorlevel 1 goto :failed_with_current_code
) else (
    echo Fast mode: full release quality gate skipped; credential guard still runs.
)
echo [1/3] Building macs !VER! backend...
set "MACS_SKIP_RAR=1"
call "%PROJECT_DIR%\abuild_windows.bat" "!VER!" auto
if errorlevel 1 goto :failed_with_current_code
cd /d "%PROJECT_DIR%\electron-app"
if errorlevel 1 goto :failed_with_current_code

echo [2/3] Installing npm dependencies if needed...
if not exist node_modules (
    if exist package-lock.json (
        call npm ci
    ) else (
        call npm install
    )
    if errorlevel 1 goto :failed_with_current_code
)
echo [3/3] Packaging macs Desktop...
rem Override the packaged app version without rewriting package.json or its lockfile.
call npm run build:win -- --config.extraMetadata.version=!VER! --config.buildVersion=!VER!
if errorlevel 1 goto :failed_with_current_code
echo Build complete: portable app in electron-app\dist.
echo For an installer, run npm run build:win:installer with the same version options.
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
echo ERROR: Desktop build stopped with exit code !BUILD_ERROR!.
popd
if defined INTERACTIVE pause
exit /b !BUILD_ERROR!

:usage
echo Usage: build_desktop.bat [version] [release^|fast]
echo Defaults: 2.0.0, release. No arguments: prompt for version.
echo release runs the full quality gate; fast is for local builds only.
echo Requires Go, go-winres, Node.js and npm. UPX is optional.
exit /b 0
