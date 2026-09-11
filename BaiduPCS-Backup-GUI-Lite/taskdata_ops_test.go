package main

import (
	"fmt"
	"testing"
)

func TestOps(t *testing.T) {
	name := "1-backup"
	// 记录初始状态
	initial := getState(t, name)
	fmt.Printf("initial state: %s\n", initial)

	// 禁用 -> 应变为 已禁用
	if err := DisableTask(name); err != nil {
		t.Fatalf("DisableTask: %v", err)
	}
	if st := getState(t, name); st != "已禁用" {
		t.Errorf("after disable want 已禁用, got %s", st)
	} else {
		fmt.Println("disable OK -> 已禁用")
	}

	// 启用 -> 应恢复
	if err := EnableTask(name); err != nil {
		t.Fatalf("EnableTask: %v", err)
	}
	if st := getState(t, name); st != "准备就绪" {
		t.Errorf("after enable want 准备就绪, got %s", st)
	} else {
		fmt.Println("enable OK -> 准备就绪")
	}

	// 运行 (触发备份, 幂等)
	if err := RunTask(name); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	fmt.Println("run OK (已触发)")

	// 结束 (任务计划 Stop, 未运行时报错属正常)
	err := StopTask(name)
	fmt.Printf("stop result: %v\n", err)
}

func getState(t *testing.T, name string) string {
	rows, err := ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	for _, r := range rows {
		if r.Name == name {
			return r.State
		}
	}
	return ""
}
