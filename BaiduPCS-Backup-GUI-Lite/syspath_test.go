package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSystemExeMMC systemExe 应返回 mmc.exe 的绝对路径, 且该路径真实存在。
// 本机 C:\Windows\System32\mmc.exe 实测存在。
func TestSystemExeMMC(t *testing.T) {
	got := systemExe("mmc.exe", `System32\mmc.exe`)
	if !filepath.IsAbs(got) {
		t.Fatalf("期望返回绝对路径, 实际得到 %q", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("返回路径不可用: %v (path=%s)", err, got)
	}
	want := filepath.Join(systemRoot(), `System32\mmc.exe`)
	if !strings.EqualFold(got, want) {
		t.Fatalf("期望 %q, 实际 %q", want, got)
	}
}

// TestSystemExeFallbackToName 候选路径全部不存在时, 应原样返回 name 交回 PATH 查找。
func TestSystemExeFallbackToName(t *testing.T) {
	got := systemExe("nonexist.exe", `NoSuchDir\nonexist.exe`)
	if got != "nonexist.exe" {
		t.Fatalf("期望回退为 %q, 实际 %q", "nonexist.exe", got)
	}
}

// TestSystemExeSecondCandidate 多个候选中只有第二个存在时, 应按顺序返回第二个。
func TestSystemExeSecondCandidate(t *testing.T) {
	const second = `System32\kernel32.dll`
	want := filepath.Join(systemRoot(), second)
	if _, err := os.Stat(want); err != nil {
		t.Skipf("本机不存在 %s, 跳过该用例: %v", want, err)
	}
	got := systemExe("never.exe", `NoSuchDir\never.exe`, second)
	if got == "never.exe" {
		t.Fatalf("不应回退到 name, 实际返回 %q", got)
	}
	if !strings.EqualFold(got, want) {
		t.Fatalf("期望 %q, 实际 %q", want, got)
	}
}

// TestSystemExeEmptySystemRoot SystemRoot 为空时应回退 windir / C:\Windows,
// 不 panic, 且仍能定位到真实的系统程序。
func TestSystemExeEmptySystemRoot(t *testing.T) {
	t.Setenv("SystemRoot", "")
	got := systemExe("cmd.exe", `System32\cmd.exe`)
	if got == "" {
		t.Fatal("返回值为空")
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("期望绝对路径, 实际 %q", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("返回路径不可用: %v (path=%s)", err, got)
	}
}

// TestSystemRootFallback SystemRoot 与 windir 都为空时, 应兜底为 C:\Windows。
func TestSystemRootFallback(t *testing.T) {
	t.Setenv("SystemRoot", "")
	t.Setenv("windir", "")
	if got := systemRoot(); got != defaultSystemRoot {
		t.Fatalf("期望 %q, 实际 %q", defaultSystemRoot, got)
	}
}

// TestSystemRootPreferSystemRoot SystemRoot 有值时优先于 windir。
func TestSystemRootPreferSystemRoot(t *testing.T) {
	t.Setenv("SystemRoot", `D:\FakeRoot`)
	t.Setenv("windir", `E:\OtherRoot`)
	if got := systemRoot(); got != `D:\FakeRoot` {
		t.Fatalf("期望 %q, 实际 %q", `D:\FakeRoot`, got)
	}
}

// TestSystemExeWithBrokenPath 回归用例(本次 Bug 的核心场景):
// PATH 为未展开的字面量 "%SystemRoot%\system32;..." 时, exec.LookPath 找不到 mmc.exe,
// 但 systemExe 仍应返回真实存在的绝对路径。
func TestSystemExeWithBrokenPath(t *testing.T) {
	t.Setenv("PATH", `%SystemRoot%\system32;%SystemRoot%;%SYSTEMROOT%\System32\WindowsPowerShell\v1.0\`)

	// 先确认环境确实复现了故障 (若某天环境 PATH 被修复, 这里只记录不失败)
	if p, err := exec.LookPath("mmc.exe"); err != nil {
		t.Logf("已复现故障: exec.LookPath(\"mmc.exe\") 失败: %v", err)
	} else {
		t.Logf("注意: 当前环境 exec.LookPath 仍能找到 mmc.exe (%s), 本机未复现", p)
	}

	got := systemExe("mmc.exe", `System32\mmc.exe`)
	if !filepath.IsAbs(got) {
		t.Fatalf("PATH 异常时仍应返回绝对路径, 实际 %q", got)
	}
	fi, err := os.Stat(got)
	if err != nil {
		t.Fatalf("PATH 异常时返回路径不可用: %v (path=%s)", err, got)
	}
	if fi.IsDir() {
		t.Fatalf("返回的是目录而非可执行文件: %s", got)
	}
	t.Logf("PATH 异常时 systemExe 返回: %s", got)
}

// TestSystemExeEmptyCandidate 空候选串必须被跳过。
// 陷阱: filepath.Join(root, "") 返回 root 本身, 而 root 目录真实存在,
// 若不做空串判断就会把系统目录误判为命中结果返回。
func TestSystemExeEmptyCandidate(t *testing.T) {
	root := systemRoot()

	// 只有一个空候选: 应跳过并回退返回 name, 不能返回 root 目录
	if got := systemExe("mmc.exe", ""); got != "mmc.exe" {
		t.Fatalf("空候选应被跳过并回退为 %q, 实际 %q", "mmc.exe", got)
	}
	// 多个空/纯空白候选: 同样应全部跳过并回退返回 name
	if got := systemExe("mmc.exe", "", "  ", "\t"); got != "mmc.exe" {
		t.Fatalf("空/空白候选应全部跳过并回退为 %q, 实际 %q", "mmc.exe", got)
	}
	// 空串在前、有效候选在后: 应跳过空串继续命中后面的真实文件
	want := filepath.Join(root, `System32\mmc.exe`)
	if _, err := os.Stat(want); err != nil {
		t.Skipf("本机不存在 %s, 跳过后续断言: %v", want, err)
	}
	got := systemExe("mmc.exe", "", `System32\mmc.exe`)
	if !strings.EqualFold(got, want) {
		t.Fatalf("应跳过空候选并命中 %q, 实际 %q", want, got)
	}
}

// TestSystemExeDirectoryCandidate 候选指向真实存在的目录时必须被拒绝,
// 不能把目录当成可执行文件返回。
func TestSystemExeDirectoryCandidate(t *testing.T) {
	root := systemRoot()
	dirCand := `System32`
	if fi, err := os.Stat(filepath.Join(root, dirCand)); err != nil || !fi.IsDir() {
		t.Skipf("%s 不是本机存在的目录, 跳过该用例", filepath.Join(root, dirCand))
	}

	// 只有目录候选: 应被 !IsDir() 拒绝, 回退返回 name
	if got := systemExe("dir-not-exe", dirCand); got != "dir-not-exe" {
		t.Fatalf("目录候选应被拒绝并回退为 %q, 实际 %q", "dir-not-exe", got)
	}
	// 目录候选在前、文件候选在后: 应跳过目录继续命中后面的真实文件
	want := filepath.Join(root, `System32\mmc.exe`)
	if _, err := os.Stat(want); err != nil {
		t.Skipf("本机不存在 %s, 跳过后续断言: %v", want, err)
	}
	got := systemExe("dir-not-exe", dirCand, `System32\mmc.exe`)
	if !strings.EqualFold(got, want) {
		t.Fatalf("应跳过目录候选并命中 %q, 实际 %q", want, got)
	}
}

// TestSystemExeBlankEnv SystemRoot 与 windir 都是纯空白时, TrimSpace 后应判空,
// 逐级回退到 C:\Windows, 不 panic 且返回非空的可用绝对路径。
func TestSystemExeBlankEnv(t *testing.T) {
	t.Setenv("SystemRoot", "   ")
	t.Setenv("windir", "\t ")

	if got := systemRoot(); got != defaultSystemRoot {
		t.Fatalf("纯空白环境变量应回退为 %q, 实际 %q", defaultSystemRoot, got)
	}
	got := systemExe("cmd.exe", `System32\cmd.exe`)
	if got == "" {
		t.Fatal("返回值为空")
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("期望绝对路径, 实际 %q", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("返回路径不可用: %v (path=%s)", err, got)
	}
}
