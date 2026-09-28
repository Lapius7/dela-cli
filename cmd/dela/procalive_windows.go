//go:build windows

package main

import "syscall"

// os.FindProcessのWindows時の「存在しないPIDでエラーになるか」はドキュメントに
// 明記が無く信頼できないため、OpenProcess + GetExitCodeProcessで直接確認する
// (標準ライブラリのsyscallパッケージのみで完結)。
func processAlive(pid int) bool {
	const processQueryLimitedInformation = 0x1000
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)

	var exitCode uint32
	if err := syscall.GetExitCodeProcess(h, &exitCode); err != nil {
		return false
	}
	const stillActive = 259
	return exitCode == stillActive
}
