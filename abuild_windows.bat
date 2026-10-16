@echo off
setlocal
call "%~dp0scripts\build\build_windows.bat" %*
exit /b %errorlevel%
