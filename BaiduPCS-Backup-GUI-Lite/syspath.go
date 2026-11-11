package main

// 系统程序定位 (解决 "exec: xxx: executable file not found in %PATH%")
//
// 背景: 本程序被某些启动器/脚本拉起时, 进程环境里的 PATH 保留了未展开的环境变量
// 字面量, 例如 "%SystemRoot%\system32;%SystemRoot%;..."。Go 的 exec.LookPath 只是拿
// PATH 里的每个条目直接拼上文件名去做 os.Stat, 并不会展开 %VAR%, 于是拼出来的
// "%SystemRoot%\system32\mmc.exe" 永远不存在, 最终报
// `exec: "mmc.exe": executable file not found in %PATH%` —— 而文件其实是好的。
//
// 对策: 系统自带程序一律用 %SystemRoot% 拼绝对路径定位, 只有候选路径全部不存在时
// 才把原始命令名交回 exec.LookPath, 保证极端环境下程序不会彻底失去能力。
//
// 关于 32/64 位文件系统重定向: 本项目编译为 amd64, System32 不会被重定向到 SysWOW64;
// 即使将来出 386 版本, mmc.exe / rundll32.exe / powershell.exe 在 SysWOW64 下同样存在,
// 逻辑仍然成立。
//
// 为什么不用 ShellExecute(shell32): ShellExecute 会把参数交给 shell 重新解析, 转义规则
// 与 exec.Command 不同, 也无法复用 runPS 现有的 SysProcAttr(HideWindow + CREATE_NO_WINDOW)。
// 这里要做的只是"把程序的绝对路径找对", 保持 exec.Command 语义最清晰, 因此不混用两种方式。

import (
	"os"
	"path/filepath"
	"strings"
)

// defaultSystemRoot 系统目录的最终兜底值 (SystemRoot / windir 都读不到时使用)
const defaultSystemRoot = `C:\Windows`

// systemRoot 返回 Windows 系统目录 (通常为 C:\Windows)。
// 取值顺序: %SystemRoot% -> %windir% -> "C:\Windows"。
func systemRoot() string {
	if r := strings.TrimSpace(os.Getenv("SystemRoot")); r != "" {
		return r
	}
	if r := strings.TrimSpace(os.Getenv("windir")); r != "" {
		return r
	}
	return defaultSystemRoot
}

// systemExe 定位 Windows 系统可执行文件的绝对路径。
// 背景：PATH 可能包含未展开的环境变量字面量（如 "%SystemRoot%\system32"），
// 此时 exec.LookPath 无法解析，导致明明存在的系统程序报 "executable file not found"。
// 因此优先用 %SystemRoot% 拼绝对路径，全部候选都不存在时才回退到 name（交回 PATH 查找）。
//
// name 为命令名或文件名 (如 "mmc.exe"), 仅在兜底时原样返回;
// candidates 为相对 %SystemRoot% 的候选路径, 按顺序取第一个真实存在的文件。
// 纯函数、无副作用, 便于单元测试。
func systemExe(name string, candidates ...string) string {
	root := systemRoot()
	for _, cand := range candidates {
		cand = strings.TrimSpace(cand)
		if cand == "" {
			continue
		}
		p := filepath.Join(root, cand)
		// 用 Stat 而非 Lstat: 系统程序都是普通文件, 跟随符号链接更符合预期
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	// 全部候选都不存在: 交回 PATH 查找, 保持原有行为作为最后兜底
	return name
}
