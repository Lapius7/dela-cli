package main

import "os"

// colorEnabledはNO_COLOR環境変数やTTY判定に基づき、色付き出力を使うかどうかを決める
// (パイプ/リダイレクト時に生のANSIコードが混ざらないようにする一般的な作法。
//  sca-cliのstyle.goと同じ考え方)。
var colorEnabled = detectColorSupport()

func detectColorSupport() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
)

func style(code, s string) string {
	if !colorEnabled {
		return s
	}
	return code + s + ansiReset
}

func bold(s string) string   { return style(ansiBold, s) }
func dim(s string) string    { return style(ansiDim, s) }
func red(s string) string    { return style(ansiRed, s) }
func green(s string) string  { return style(ansiGreen, s) }
func yellow(s string) string { return style(ansiYellow, s) }
func cyan(s string) string   { return style(ansiCyan, s) }
