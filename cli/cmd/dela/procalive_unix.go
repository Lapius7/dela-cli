//go:build !windows

package main

import (
	"os"
	"syscall"
)

// UnixではFindProcessは常に成功するので、シグナル0を送って生死を確認する
// (実際にシグナルを配送せず、存在・権限チェックだけが行われる)。
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
