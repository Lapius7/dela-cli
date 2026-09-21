@echo off
setlocal enabledelayedexpansion
rem dela (deplapius) CLI installer for Windows CMD
rem
rem   curl -fsSL https://raw.githubusercontent.com/lapius7/dela-cli/main/install.bat -o install.bat && install.bat
rem
rem (CMDはirm/iexのようなワンライナー実行に対応していないため、一度ファイルとして
rem  保存してから実行する。PowerShellが使える場合はinstall.ps1の方が手順が短い)

echo dela - deploy.lapius7.com tunnel CLI installer
echo.

where go >nul 2>nul
if errorlevel 1 (
    echo [ERROR] Go not found. Install it from https://go.dev/dl/ and run this again.
    exit /b 1
)
for /f "tokens=3" %%v in ('go version') do set GOVER=%%v
echo [OK] Go detected (%GOVER%)

where ssh >nul 2>nul
if errorlevel 1 (
    echo [WARN] ssh command not found. dela needs ssh to run (normally bundled with Windows 10/11; enable the OpenSSH Client optional feature if missing).
)

echo [..] Downloading and building dela (github.com/lapius7/dela-cli/cli/cmd/dela@latest^)
go install github.com/lapius7/dela-cli/cli/cmd/dela@latest
if errorlevel 1 (
    echo [ERROR] Install failed.
    exit /b 1
)
echo [OK] Build complete

for /f "delims=" %%g in ('go env GOPATH') do set GOBIN=%%g\bin
set BIN=%GOBIN%\dela.exe

if not exist "%BIN%" (
    echo [ERROR] Build succeeded but the binary was not found at: %BIN%
    exit /b 1
)
echo [OK] Installed to: %BIN%

echo %PATH% | find /i "%GOBIN%" >nul
if errorlevel 1 (
    echo [WARN] %GOBIN% is not on PATH. Add it with:
    echo     setx PATH "%%PATH%%;%GOBIN%"
    echo   (open a new Command Prompt window afterwards for it to take effect^)
)

echo.
echo [OK] Setup complete. Usage:
echo   dela 3000
