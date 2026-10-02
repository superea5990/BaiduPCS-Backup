package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseTaskNameFromFile 验证从配置文件名反解任务名 (含边界: 全点/缺前后缀/无任务名)。
func TestParseTaskNameFromFile(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"backup-task.1-backup.json", "1-backup"},
		{"backup-task.12-backup.json", "12-backup"},
		{"backup-task.backup.json", "backup"},
		{"backup-task..json", ""},
		{"backup-task...json", ""},    // 三连点 -> 任务名为 "."
		{"backup-task....json", ""},   // 四连点 -> 任务名为 ".."
		{"1-backup.json", ""},         // 缺前缀
		{"backup-task.1-backup", ""},  // 缺后缀
		{"backup-task.json", ""},      // 前后缀之间无任务名
	}
	for _, c := range cases {
		if got := parseTaskNameFromFile(c.in); got != c.want {
			t.Errorf("parseTaskNameFromFile(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

// TestParseImportItemFile 验证单文件解析: 正常、BOM、非法 JSON、缺少 local_dir、空 JSON、全空白、文件不存在。
func TestParseImportItemFile(t *testing.T) {
	dir := t.TempDir()

	// 1) 正常配置
	good := filepath.Join(dir, "backup-task.1-backup.json")
	if err := os.WriteFile(good, []byte(`{"local_dir":"C:/src","remote_dir":"/backup"}`), 0644); err != nil {
		t.Fatalf("写临时配置: %v", err)
	}
	it := parseImportItemFile(good)
	if it.ParseErr != "" {
		t.Fatalf("正常配置被误判为失败: %s", it.ParseErr)
	}
	if it.Name != "1-backup" || it.LocalDir != "C:/src" || it.RemoteDir != "/backup" {
		t.Fatalf("正常配置字段解析错误: %+v", it)
	}

	// 2) UTF-8 BOM + 正常配置
	bom := filepath.Join(dir, "backup-task.2-backup.json")
	data := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"local_dir":"D:/x","remote_dir":"/y"}`)...)
	if err := os.WriteFile(bom, data, 0644); err != nil {
		t.Fatalf("写 BOM 配置: %v", err)
	}
	it2 := parseImportItemFile(bom)
	if it2.ParseErr != "" {
		t.Fatalf("BOM 配置被误判为失败: %s", it2.ParseErr)
	}
	if it2.LocalDir != "D:/x" {
		t.Fatalf("BOM 配置字段解析错误: %+v", it2)
	}

	// 3) 非法 JSON
	bad := filepath.Join(dir, "backup-task.3-backup.json")
	if err := os.WriteFile(bad, []byte("not-a-json"), 0644); err != nil {
		t.Fatalf("写非法配置: %v", err)
	}
	it3 := parseImportItemFile(bad)
	if it3.ParseErr == "" || !strings.Contains(it3.ParseErr, "JSON") {
		t.Fatalf("非法 JSON 未标记 ParseErr: %+v", it3)
	}

	// 4) 缺少 local_dir
	nodir := filepath.Join(dir, "backup-task.4-backup.json")
	if err := os.WriteFile(nodir, []byte(`{"remote_dir":"/z"}`), 0644); err != nil {
		t.Fatalf("写缺 local_dir 配置: %v", err)
	}
	it4 := parseImportItemFile(nodir)
	if it4.ParseErr == "" || !strings.Contains(it4.ParseErr, "local_dir") {
		t.Fatalf("缺少 local_dir 未标记 ParseErr: %+v", it4)
	}

	// 5) 空 JSON {}
	empty := filepath.Join(dir, "backup-task.5-backup.json")
	if err := os.WriteFile(empty, []byte(`{}`), 0644); err != nil {
		t.Fatalf("写空 JSON 配置: %v", err)
	}
	it5 := parseImportItemFile(empty)
	if it5.ParseErr == "" || !strings.Contains(it5.ParseErr, "local_dir") {
		t.Fatalf("空 JSON 未标记 ParseErr: %+v", it5)
	}

	// 6) local_dir 全空白
	blank := filepath.Join(dir, "backup-task.6-backup.json")
	if err := os.WriteFile(blank, []byte(`{"local_dir":"   ","remote_dir":"/x"}`), 0644); err != nil {
		t.Fatalf("写全空白 local_dir 配置: %v", err)
	}
	it6 := parseImportItemFile(blank)
	if it6.ParseErr == "" || !strings.Contains(it6.ParseErr, "local_dir") {
		t.Fatalf("全空白 local_dir 未标记 ParseErr: %+v", it6)
	}

	// 7) 文件不存在 (读取失败分支)
	missing := filepath.Join(dir, "backup-task.7-backup.json")
	it7 := parseImportItemFile(missing)
	if it7.ParseErr == "" || !strings.Contains(it7.ParseErr, "读取失败") {
		t.Fatalf("文件不存在未标记读取失败: %+v", it7)
	}
}

// TestRegisterImportItemEmptyName 验证 registerImportItem 对空任务名/nil 的防护。
func TestRegisterImportItemEmptyName(t *testing.T) {
	if err := registerImportItem(&importItem{Name: ""}, 30, "dummy.exe"); err == nil {
		t.Fatal("空任务名应返回错误")
	}
	if err := registerImportItem(nil, 30, "dummy.exe"); err == nil {
		t.Fatal("nil item 应返回错误")
	}
}
