//go:build windows

package pcscommand

import (
	"golang.org/x/sys/windows"
)

// processAlive 判断指定 PID 的进程是否仍在运行 (Windows)
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	// STILL_ACTIVE = 259 (Windows 文档定义值)
	return code == 259
}
