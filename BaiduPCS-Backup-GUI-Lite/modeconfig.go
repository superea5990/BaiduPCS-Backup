package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// modeParams 一种性能模式的固定运行参数 (普通/无感)
type modeParams struct {
	Concurrency   int `json:"concurrency"`    // 同时上传文件数
	BandwidthKBps int `json:"bandwidth_kbps"` // 上传带宽 KB/s, 0=不限
}

// modeConfig 全局双模式默认参数 (全局默认模板: 新建任务时继承, 已有任务不受影响)
type modeConfig struct {
	ActiveMode string     `json:"active_mode"` // normal | stealth 当前全局默认性能模式
	Normal     modeParams `json:"normal"`      // 普通模式固定参数
	Stealth    modeParams `json:"stealth"`     // 无感模式固定参数
}

func defaultModeConfig() modeConfig {
	return modeConfig{
		ActiveMode: "normal",
		Normal:     modeParams{Concurrency: 2, BandwidthKBps: 0},
		Stealth:    modeParams{Concurrency: 1, BandwidthKBps: 200},
	}
}

var (
	modeCfg   modeConfig
	modeCfgMu sync.Mutex
)

// modeConfigFilePath 全局模式参数文件路径 (与主程序同目录)
func modeConfigFilePath() string {
	exe, _ := os.Executable()
	return filepath.Join(filepath.Dir(exe), "mode_config.json")
}

// loadModeConfig 读取全局模式参数; 文件缺失或字段缺失时用默认值补齐
func loadModeConfig() modeConfig {
	modeCfgMu.Lock()
	defer modeCfgMu.Unlock()
	if modeCfg.ActiveMode == "" { // 尚未初始化
		modeCfg = defaultModeConfig()
		data, err := os.ReadFile(modeConfigFilePath())
		if err == nil {
			data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // 兼容 UTF-8 BOM
			json.Unmarshal(data, &modeCfg)                        // 仅覆盖文件中存在的字段, 缺失保持默认
		}
	}
	return modeCfg
}

// saveModeConfig 保存全局模式参数到 mode_config.json
func saveModeConfig(mc modeConfig) error {
	modeCfgMu.Lock()
	defer modeCfgMu.Unlock()
	data, err := json.MarshalIndent(mc, "", "  ")
	if err != nil {
		return err
	}
	modeCfg = mc
	return os.WriteFile(modeConfigFilePath(), data, 0644)
}

// currentModeParams 取当前全局默认性能模式的参数
func currentModeParams() modeParams {
	mc := loadModeConfig()
	switch mc.ActiveMode {
	case "stealth":
		return mc.Stealth
	default:
		return mc.Normal
	}
}
