package main

import (
	"fmt"
	"testing"
)

func TestReRegister(t *testing.T) {
	rows, err := ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("没有任务可测试")
	}
	tk := rows[0]
	fmt.Printf("重新注册前: name=%s freqMin=%d\n", tk.Name, tk.FreqMinutes)
	fmt.Printf("描述:\n%s\n", tk.Description)

	err = ReRegisterTask(tk.Name, tk.Execute, tk.ConfigPath, tk.FreqMinutes, tk.Description)
	if err != nil {
		t.Fatalf("ReRegisterTask: %v", err)
	}
	fmt.Println("ReRegisterTask OK")

	rows2, err := ListTasks()
	if err != nil {
		t.Fatalf("ListTasks2: %v", err)
	}
	found := false
	for _, r := range rows2 {
		if r.Name == tk.Name {
			found = true
			fmt.Printf("重新注册后: state=%s freqMin=%d\n", r.State, r.FreqMinutes)
			if r.Description != tk.Description {
				t.Errorf("描述不一致!\n期望:\n%s\n实际:\n%s", tk.Description, r.Description)
			} else {
				fmt.Println("描述一致 OK")
			}
			if r.FreqMinutes != tk.FreqMinutes {
				t.Errorf("频率不一致: %d != %d", r.FreqMinutes, tk.FreqMinutes)
			}
			if r.State != "准备就绪" {
				t.Errorf("状态应为 准备就绪, 实际 %s", r.State)
			}
		}
	}
	if !found {
		t.Fatalf("重新注册后找不到任务 %s", tk.Name)
	}
}
