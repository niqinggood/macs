@echo off
setlocal EnableExtensions EnableDelayedExpansion
set "TEMP_FILE="
set "TEMP_ARCHIVE="
if /i "%~1"=="--help" goto :usage
if /i "%~1"=="-h" goto :usage
set "INTERACTIVE="
if "%~1"=="" set "INTERACTIVE=1"
for %%I in ("%~dp0..\..") do set "PROJECT_DIR=%%~fI"
pushd "%PROJECT_DIR%" >nul
if errorlevel 1 exit /b 1

set "DEFAULT_VER=2.0.0"
set "VER=%~1"
if defined INTERACTIVE set /p VER="Version code (default: !DEFAULT_VER!): "
if not defined VER set "VER=!DEFAULT_VER!"
set "COMPRESSION=%~2"
if not defined COMPRESSION set "COMPRESSION=auto"
if not "%~3"=="" goto :invalid_arguments
if /i not "!COMPRESSION!"=="auto" if /i not "!COMPRESSION!"=="require" if /i not "!COMPRESSION!"=="off" goto :invalid_arguments
rem Validate the entire version, including spaces; never silently truncate it.
powershell.exe -NoProfile -Command "if ($env:VER -cnotmatch '^[0-9A-Za-z][0-9A-Za-z.-]*$') { exit 2 }"
if errorlevel 1 (
    echo ERROR: Invalid version. Use letters, numbers, dots, and hyphens only.
    goto :failed
)
where go >nul 2>&1
if errorlevel 1 (
    echo ERROR: Go was not found in PATH.
    goto :failed
)
set "BUILD_DATE="
for /f "delims=" %%I in ('powershell.exe -NoProfile -Command "[DateTime]::UtcNow.ToString('yyyy-MM-dd')"') do set "BUILD_DATE=%%I"
if not defined BUILD_DATE goto :failed
for %%F in (openKitty.exe "openKitty-!VER!-win.exe" openKitty-newicon.exe "openKitty-!VER!-win.rar") do (
    if exist "%%~F\" (
        echo ERROR: Output path is a directory: %%~F
        goto :failed
    )
)

rem Locate build tools before replacing any existing outputs.
set "WINRES=!MACS_WINRES!"
if defined WINRES if not exist "!WINRES!" (
    echo ERROR: MACS_WINRES does not point to an existing executable.
    goto :failed
)
if not defined WINRES for /f "delims=" %%I in ('go env GOBIN') do if exist "%%I\go-winres.exe" set "WINRES=%%I\go-winres.exe"
if not defined WINRES for /f "delims=" %%I in ('go env GOPATH') do if exist "%%I\bin\go-winres.exe" set "WINRES=%%I\bin\go-winres.exe"
if not defined WINRES for /f "delims=" %%I in ('where go-winres.exe 2^>nul') do if not defined WINRES set "WINRES=%%I"
if not defined WINRES (
    echo ERROR: go-winres not found. Install it with:
    echo   go install github.com/tc-hib/go-winres@latest
    goto :failed
)
set "UPX_CMD="
if /i not "!COMPRESSION!"=="off" (
    set "UPX_CMD=!MACS_UPX!"
    if defined UPX_CMD if not exist "!UPX_CMD!" (
        echo ERROR: MACS_UPX does not point to an existing executable.
        goto :failed
    )
    if not defined UPX_CMD if exist "%PROJECT_DIR%\upx.exe" set "UPX_CMD=%PROJECT_DIR%\upx.exe"
    if not defined UPX_CMD for /f "delims=" %%I in ('where upx.exe 2^>nul') do if not defined UPX_CMD set "UPX_CMD=%%I"
    if not defined UPX_CMD if /i "!COMPRESSION!"=="require" (
        echo ERROR: UPX is required. Set MACS_UPX or install upx.exe in PATH.
        goto :failed
    )
)
set "GOOS=windows"
set "GOARCH=amd64"
set "CGO_ENABLED=0"
echo Running release credential guard...
go run ./cmd/releaseguard
if errorlevel 1 goto :failed_with_current_code

echo [1/5] Cleaning old Windows resource objects...
for %%F in (resource.syso_windows_*.syso rsrc_windows_*.syso) do (
    if exist "%%F" del /f /q "%%F" >nul 2>&1
    if exist "%%F" (
        echo ERROR: Cannot remove "%%F".
        goto :failed
    )
)
echo [2/5] Generating Windows resources...
if not exist "winres\kitty.png" (
    echo ERROR: Icon not found: winres\kitty.png
    goto :failed
)
"!WINRES!" simply --icon winres\kitty.png --out rsrc --manifest cli --file-description "macs Multi-Agent Collaboration System" --product-name "macs" --original-filename "openKitty.exe" --file-version "!VER!" --product-version "!VER!"
if errorlevel 1 goto :failed_with_current_code

rem Compile and compress a temporary exe; preserve the last good exe on failure.
set "TEMP_FILE=openKitty.build.exe"
if exist "!TEMP_FILE!" del /f /q "!TEMP_FILE!" >nul 2>&1
if exist "!TEMP_FILE!" goto :failed
echo [3/5] Building macs !VER! for windows/amd64...
go build -buildvcs=false -trimpath -tags netgo,osusergo -ldflags="-s -w -X main.CurrentVersion=!VER! -X mac/internal/app.build=!BUILD_DATE!" -o "!TEMP_FILE!" ./cmd/macs
if errorlevel 1 goto :failed_with_current_code
if not exist "!TEMP_FILE!" goto :failed
for %%A in ("!TEMP_FILE!") do echo Uncompressed size: %%~zA bytes

echo [4/5] Compression: !COMPRESSION!
if defined UPX_CMD (
    "!UPX_CMD!" --best --lzma "!TEMP_FILE!"
    if errorlevel 1 goto :failed_with_current_code
    "!UPX_CMD!" -t "!TEMP_FILE!"
    if errorlevel 1 goto :failed_with_current_code
) else if /i "!COMPRESSION!"=="auto" (
    echo WARNING: UPX not found. Output is uncompressed. Use require mode to enforce compression.
)
move /y "!TEMP_FILE!" openKitty.exe >nul
if errorlevel 1 goto :failed_with_current_code
copy /y openKitty.exe "openKitty-!VER!-win.exe" >nul
if errorlevel 1 goto :failed_with_current_code
copy /y openKitty.exe openKitty-newicon.exe >nul
if errorlevel 1 goto :failed_with_current_code

echo [5/5] Creating optional RAR archive...
set "RAR_CREATED="
if "!MACS_SKIP_RAR!"=="1" goto :complete
set "RAR_CMD=!MACS_RAR!"
if defined RAR_CMD if not exist "!RAR_CMD!" (
    echo ERROR: MACS_RAR does not point to an existing executable.
    goto :failed
)
if not defined RAR_CMD for /f "delims=" %%I in ('where Rar.exe 2^>nul') do if not defined RAR_CMD set "RAR_CMD=%%I"
for %%I in ("%ProgramFiles%\WinRAR\Rar.exe" "%ProgramFiles(x86)%\WinRAR\Rar.exe" "D:\Program Files\WinRAR\Rar.exe") do if not defined RAR_CMD if exist "%%~I" set "RAR_CMD=%%~I"
if not defined RAR_CMD (
    echo Rar.exe not found; the executable is ready without an archive.
    goto :complete
)
rem A new temporary archive avoids retaining stale entries from previous builds.
set "TEMP_ARCHIVE=openKitty-!VER!-win.build.rar"
if exist "!TEMP_ARCHIVE!" del /f /q "!TEMP_ARCHIVE!" >nul 2>&1
if exist "!TEMP_ARCHIVE!" goto :failed
rem -r- stops LICENSE/NOTICE names from matching unrelated subdirectories.
"!RAR_CMD!" a -r- -m5 "!TEMP_ARCHIVE!" openKitty.exe LICENSE NOTICE COMMERCIAL-LICENSE.md TRADEMARKS.md BRANDING.md THIRD-PARTY-NOTICES.md
if errorlevel 1 goto :failed_with_current_code
rem Recurse only the explicit license directory, never root filename patterns.
"!RAR_CMD!" a -r -m5 -x*.go -x*.py -x*.js -x*.ts -x*.mod -x*.sum -x*.bat -x*.sh "!TEMP_ARCHIVE!" third_party
if errorlevel 1 goto :failed_with_current_code
"!RAR_CMD!" t "!TEMP_ARCHIVE!"
if errorlevel 1 goto :failed_with_current_code
move /y "!TEMP_ARCHIVE!" "openKitty-!VER!-win.rar" >nul
if errorlevel 1 goto :failed_with_current_code
set "RAR_CREATED=1"

:complete
echo Build complete: macs !VER!
echo   openKitty.exe
echo   openKitty-!VER!-win.exe
echo   openKitty-newicon.exe
for %%A in (openKitty.exe) do echo Final executable size: %%~zA bytes
if defined RAR_CREATED echo   openKitty-!VER!-win.rar
if defined INTERACTIVE pause
popd
exit /b 0

:invalid_arguments
echo ERROR: Usage: abuild_windows.bat [version] [auto^|require^|off]
:failed
set "BUILD_ERROR=1"
goto :cleanup
:failed_with_current_code
set "BUILD_ERROR=!errorlevel!"
if "!BUILD_ERROR!"=="0" set "BUILD_ERROR=1"
:cleanup
echo ERROR: Build stopped with exit code !BUILD_ERROR!.
if defined TEMP_FILE if exist "!TEMP_FILE!" del /f /q "!TEMP_FILE!" >nul 2>&1
if defined TEMP_ARCHIVE if exist "!TEMP_ARCHIVE!" del /f /q "!TEMP_ARCHIVE!" >nul 2>&1
if defined INTERACTIVE pause
popd
exit /b !BUILD_ERROR!

:usage
echo Usage: abuild_windows.bat [version] [auto^|require^|off]
echo Default version: 2.0.0. No arguments: prompt for version.
echo auto: use UPX when available; require: fail without UPX; off: skip UPX.
echo UPX lookup: MACS_UPX, repository upx.exe, PATH.
echo MACS_WINRES and MACS_RAR may specify tool paths. MACS_SKIP_RAR=1 skips RAR.
exit /b 0
