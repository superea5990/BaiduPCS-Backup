package main

import (
	"fmt"
	"testing"
)

func TestListTasks(t *testing.T) {
	rows, err := ListTasks()
	if err != nil {
		t.Fatalf("ListTasks error: %v", err)
	}
	fmt.Printf("task count: %d\n", len(rows))
	for _, r := range rows {
		fmt.Printf("---\nSeq=%d Name=%s State=%s\nLocal=%s\nRemote=%s\nFreq=%s freqMin=%d Type=%s Mode=%s Created=%s\nConfigPath=%s Execute=%s\n",
			r.Seq, r.Name, r.State, r.LocalDir, r.RemoteDir, r.FreqText, r.FreqMinutes, r.BackupType, r.Mode, r.CreatedAt, r.ConfigPath, r.Execute)
	}
}
