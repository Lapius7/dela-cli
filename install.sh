#!/usr/bin/env bash
# dela (deplapius) CLI のワンライナーインストーラー。
#
#   curl -fsSL https://raw.githubusercontent.com/lapius7/dela-cli/main/install.sh | bash
#
set -euo pipefail

PKG="github.com/lapius7/dela-cli/cli/cmd/dela"

if [ -t 1 ]; then
  BOLD=$'\033[1m'; DIM=$'\033[2m'; RESET=$'\033[0m'
  RED=$'\033[31m'; GREEN=$'\033[32m'; CYAN=$'\033[36m'; YELLOW=$'\033[33m'
else
  BOLD=""; DIM=""; RESET=""; RED=""; GREEN=""; CYAN=""; YELLOW=""
fi

info() { printf "%s→%s %s\n" "$CYAN" "$RESET" "$1"; }
ok()   { printf "%s✓%s %s\n" "$GREEN" "$RESET" "$1"; }
warn() { printf "%s!%s %s\n" "$YELLOW" "$RESET" "$1"; }
err()  { printf "%s✗%s %s\n" "$RED" "$RESET" "$1" >&2; }

printf "%sdela%s — deploy.lapius7.com トンネルCLI installer\n\n" "$BOLD" "$RESET"

if ! command -v go >/dev/null 2>&1; then
  err "Go が見つかりません。https://go.dev/dl/ からインストールしてから、もう一度実行してください。"
  exit 1
fi
GO_VERSION="$(go version | awk '{print $3}')"
ok "Go を検出しました ${DIM}${GO_VERSION}${RESET}"

if ! command -v ssh >/dev/null 2>&1; then
  warn "ssh コマンドが見つかりません。dela の実行にはsshが必要です(Windows 10/11・macOS・Linuxに標準搭載)。"
fi

info "dela をダウンロード・ビルド中 (${PKG}@latest)"
LOG="$(mktemp)"
if go install "${PKG}@latest" >"$LOG" 2>&1; then
  ok "ビルド完了"
else
  err "インストールに失敗しました"
  cat "$LOG" >&2
  rm -f "$LOG"
  exit 1
fi
rm -f "$LOG"

GOBIN="$(go env GOPATH)/bin"
BIN="${GOBIN}/dela"

if [ ! -x "$BIN" ]; then
  err "ビルドは成功しましたが、想定の場所にバイナリが見つかりません: $BIN"
  exit 1
fi
ok "インストール先: ${DIM}${BIN}${RESET}"

case ":$PATH:" in
  *":$GOBIN:"*) ;;
  *) warn "${GOBIN} にPATHが通っていません。シェルの設定ファイルに追加してください: export PATH=\"\$PATH:${GOBIN}\"" ;;
esac

printf "\n"
ok "セットアップ完了。次のように使えます:"
printf "  %sdela 3000%s\n" "$BOLD" "$RESET"
