# dela (deplapius) CLI installer for Windows PowerShell
#
#   irm https://raw.githubusercontent.com/lapius7/dela-cli/main/install.ps1 | iex
#
$ErrorActionPreference = "Stop"

$Pkg = "github.com/lapius7/dela-cli/cli/cmd/dela"

Write-Host "dela — deploy.lapius7.com トンネルCLI installer`n" -ForegroundColor Cyan

$go = Get-Command go -ErrorAction SilentlyContinue
if (-not $go) {
    Write-Host "✗ Go が見つかりません。https://go.dev/dl/ からインストールしてから、もう一度実行してください。" -ForegroundColor Red
    exit 1
}
$goVersion = (go version)
Write-Host "✓ Go を検出しました ($goVersion)" -ForegroundColor Green

$ssh = Get-Command ssh -ErrorAction SilentlyContinue
if (-not $ssh) {
    Write-Host "! ssh コマンドが見つかりません。dela の実行にはsshが必要です(通常はWindows 10/11に標準搭載、OpenSSHクライアントの機能が無効な場合は設定から有効化してください)。" -ForegroundColor Yellow
}

Write-Host "→ dela をダウンロード・ビルド中 ($Pkg@latest)" -ForegroundColor Cyan
# GOPROXY(既定はproxy.golang.org)は、タグの無いブランチの@latest解決結果を
# キャッシュすることがあり、更新してもしばらく古いコミットが返ることがある。
# directにしてGitHubから直接取得させ、常に最新のコミットを使う。
$env:GOPROXY = "direct"
& go install "$Pkg@latest"
if ($LASTEXITCODE -ne 0) {
    Write-Host "✗ インストールに失敗しました" -ForegroundColor Red
    exit 1
}
Write-Host "✓ ビルド完了" -ForegroundColor Green

$gobin = (go env GOPATH) + "\bin"
$bin = Join-Path $gobin "dela.exe"

if (-not (Test-Path $bin)) {
    Write-Host "✗ ビルドは成功しましたが、想定の場所にバイナリが見つかりません: $bin" -ForegroundColor Red
    exit 1
}
Write-Host "✓ インストール先: $bin" -ForegroundColor Green

$pathEntries = $env:Path -split ";"
if ($pathEntries -notcontains $gobin) {
    Write-Host "! $gobin にPATHが通っていません。次のコマンドで追加できます:" -ForegroundColor Yellow
    Write-Host "    [Environment]::SetEnvironmentVariable('Path', `$env:Path + ';$gobin', 'User')"
}

Write-Host "`n✓ セットアップ完了。次のように使えます:" -ForegroundColor Green
Write-Host "  dela 3000" -ForegroundColor White
