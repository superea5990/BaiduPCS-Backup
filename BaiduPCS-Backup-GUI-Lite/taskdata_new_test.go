package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestVerifyLogin(t *testing.T) {
	core := ResolveCoreExe()
	if core == "" {
		t.Fatal("ResolveCoreExe 未找到核心程序")
	}
	fmt.Println("core:", core)
	line, err := VerifyLogin(core)
	if err != nil {
		t.Fatalf("VerifyLogin: %v", err)
	}
	fmt.Println("verify:", line)
	if line == "" || !strings.Contains(line, "用户名") {
		t.Errorf("验证输出格式异常: %q", line)
	}
}

func TestExportTaskXML(t *testing.T) {
	rows, err := ListTasks()
	if err != nil || len(rows) == 0 {
		t.Skip("无任务可导出")
	}
	xml, err := ExportTaskXML(rows[0].Name)
	if err != nil {
		t.Fatalf("ExportTaskXML: %v", err)
	}
	fmt.Printf("XML 长度=%d 开头=%s\n", len(xml), xml[:min(40, len(xml))])
	if !strings.Contains(xml, "<Task") {
		t.Errorf("导出内容异常: %q", xml[:min(80, len(xml))])
	}
}

func TestAddDeleteCycle(t *testing.T) {
	// 用一个临时配置 + 临时任务名验证 添加(注册) -> 删除 全链路
	tmpCfg := "backup-task.__test__.json"
	tmpName := "9-backup"
	defer os.Remove(tmpCfg)

	cfg := &taskConfig{
		LocalDir: "C:/__test__", RemoteDir: "/__test__", Exclude: []string{},
		Mode: "normal", BackupType: "incremental", Concurrency: 1, BandwidthKBps: 0, MaxRetry: 3,
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(tmpCfg, data, 0644); err != nil {
		t.Fatalf("写临时配置: %v", err)
	}
	core := ResolveCoreExe()
	if core == "" {
		t.Fatal("无核心程序")
	}
	desc := "源：C:/__test__\n目标：/__test__\n频率：每30分钟\n类型：增量备份\n模式：普通模式\n创建时间：test"
	if err := ReRegisterTask(tmpName, core, tmpCfg, 30, desc); err != nil {
		t.Fatalf("注册临时任务: %v", err)
	}
	fmt.Println("临时任务已注册, 开始删除")
	if err := DeleteTask(tmpName); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	// 复核已删除
	rows, err := ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	for _, r := range rows {
		if r.Name == tmpName {
			t.Fatalf("任务 %s 删除后仍存在", tmpName)
		}
	}
	fmt.Println("删除验证 OK")
}

func jsonMarshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}
