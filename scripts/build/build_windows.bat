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
echo Building macs for Windows...
echo ============================================

:: Double-click to enter a version, or pass it as the first argument.
set "DEFAULT_VER=1.1.8"
set "VER=%~1"
if defined INTERACTIVE set /p VER="Version code (default: %DEFAULT_VER%): "
set RAW_VER=!VER!
set VER=
for /f "tokens=* delims= " %%A in ("!RAW_VER!") do set VER=%%A
if not defined VER set VER=%DEFAULT_VER%

set "INVALID_VER="
for /f "delims=0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz.-" %%A in ("!VER!") do set "INVALID_VER=%%A"
if defined INVALID_VER (
    echo ERROR: Invalid version "!VER!". Use letters, numbers, dots, and hyphens only.
    if defined INTERACTIVE pause
    popd
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
    if defined INTERACTIVE pause
    popd
    exit /b 1
)

set "WINRES="
for /f "delims=" %%I in ('go env GOBIN') do set "WINRES=%%I\go-winres.exe"
if not defined WINRES for /f "delims=" %%I in ('go env GOPATH') do set "WINRES=%%I\bin\go-winres.exe"
if not exist "%WINRES%" (
    where go-winres >nul 2>&1 && set WINRES=go-winres
)
if not exist "%WINRES%" if /i not "%WINRES%"=="go-winres" (
    echo ERROR: go-winres not found. Install it with:
    echo   go install github.com/tc-hib/go-winres@latest
    if defined INTERACTIVE pause
    popd
    exit /b 1
)

echo.
echo [1/5] Cleaning old Windows resource objects...
del /f /q resource.syso_windows_*.syso 2>nul
del /f /q rsrc_windows_*.syso 2>nul
for %%F in (resource.syso_windows_*.syso rsrc_windows_*.syso) do (
    if exist "%%F" (
        echo ERROR: Cannot remove "%%F". Close programs using it and try again.
        if defined INTERACTIVE pause
        popd
        exit /b 1
    )
)

echo.
echo [2/5] Generating Windows resources...
if not exist "winres\kitty.png" (
    echo ERROR: Icon not found: "%PROJECT_DIR%\winres\kitty.png"
    if defined INTERACTIVE pause
    popd
    exit /b 1
)
"%WINRES%" simply --icon winres\kitty.png --out rsrc --manifest cli --file-description "macs Multi-Agent Collaboration System" --product-name "macs" --original-filename "openKitty.exe" --file-version "!VER!" --product-version "!VER!"
if errorlevel 1 (
    set "BUILD_ERROR=!errorlevel!"
    echo Resource generation failed.
    if defined INTERACTIVE pause
    popd
    exit /b !BUILD_ERROR!
)

echo.
echo [3/5] Building openKitty.exe...
set "GOOS=windows"
set "GOARCH=amd64"
set "CGO_ENABLED=0"
go build -buildvcs=false -ldflags="-s -w -X main.CurrentVersion=!VER! -X mac/internal/app.build=!BUILD_DATE!" -trimpath -tags netgo,osusergo -o openKitty.exe ./cmd/openkitty

if errorlevel 1 (
    set "BUILD_ERROR=!errorlevel!"
    echo Build failed.
    if defined INTERACTIVE pause
    popd
    exit /b !BUILD_ERROR!
)

echo Build successful. File size:
for %%A in (openKitty.exe) do echo   %%~zA bytes

echo.
echo [4/5] Compressing with UPX...
if exist upx.exe (
    upx.exe --best --lzma openKitty.exe
    if !errorlevel! equ 0 (
        echo Compression successful. Compressed size:
        for %%A in (openKitty.exe) do echo   %%~zA bytes
    ) else (
        echo Compression failed.
    )
) else (
    echo WARNING: upx.exe not found, skipping compression
)

echo.
echo [5/5] Creating versioned exe and RAR archive...
copy /y openKitty.exe "openKitty-!VER!-win.exe" >nul
copy /y openKitty.exe "openKitty-newicon.exe" >nul
echo   - openKitty-!VER!-win.exe
echo   - openKitty-newicon.exe
for %%A in ("openKitty-!VER!-win.exe") do echo     Size: %%~zA bytes

:: Find Rar.exe
set RAR_CMD=
for %%p in (
    "D:\Program Files\WinRAR\Rar.exe"
    "C:\Program Files\WinRAR\Rar.exe"
    "C:\Program Files (x86)\WinRAR\Rar.exe"
) do (
    if not defined RAR_CMD if exist %%p set RAR_CMD=%%p
)
if not defined RAR_CMD (
    where Rar >nul 2>&1 && set RAR_CMD=Rar
)
if not defined RAR_CMD (
    where WinRAR >nul 2>&1 && set RAR_CMD=WinRAR
)

if defined RAR_CMD (
    echo Found: !RAR_CMD!
    echo Creating openKitty-!VER!-win.rar...
    !RAR_CMD! a -r -m5 "openKitty-!VER!-win.rar" openKitty.exe LICENSE NOTICE COMMERCIAL-LICENSE.md TRADEMARKS.md BRANDING.md THIRD-PARTY-NOTICES.md third_party
    if !errorlevel! equ 0 (
        echo RAR archive created.
        echo   - openKitty-!VER!-win.rar
        for %%A in ("openKitty-!VER!-win.rar") do echo     Size: %%~zA bytes
    ) else (
        echo RAR creation failed.
    )
) else (
    echo WARNING: WinRAR/Rar not found, skipping RAR
)

echo.
echo ============================================
echo Build complete! Output:
echo   - openKitty.exe
echo   - openKitty-!VER!-win.exe
echo   - openKitty-newicon.exe
if exist "openKitty-!VER!-win.rar" echo   - openKitty-!VER!-win.rar
echo ============================================
if defined INTERACTIVE pause
popd
exit /b 0
