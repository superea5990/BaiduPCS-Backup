//go:build !windows

package pcscommand

import (
	"os"
	"syscall"
)

// processAlive 判断指定 PID 的进程是否仍在运行 (非 Windows)
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
