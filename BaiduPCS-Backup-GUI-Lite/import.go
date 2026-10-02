package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ---- 导入逻辑层 ----
// 扫描 GUI 所在目录下的 backup-task.*.json 配置文件, 逐个解析后注册为计划任务。
// 本文件只做纯逻辑, 不触碰任何 UI, 便于单元测试。

const (
	importCfgPrefix      = "backup-task."
	importCfgSuffix      = ".json"
	defaultImportFreqMin = 30
)

// importItem 导入候选列表的一行
type importItem struct {
	FileName   string // backup-task.1-backup.json
	Name       string // 1-backup（空=无法解析）
	ConfigPath string // 扫描文件绝对路径, 注册时直接指向它
	LocalDir   string
	RemoteDir  string
	Exists     bool   // 计划任务已存在同名
	ParseErr   string // 解析失败原因, 空=正常
}

// importFreqPreset 导入用频率预设 (独立于手动添加任务的 freqPresets, 粒度更细)
type importFreqPreset struct {
	text string
	min  int
}

// importFreqPresets 导入弹窗的频率下拉选项
var importFreqPresets = []importFreqPreset{
	{"每5分钟", 5},
	{"每10分钟", 10},
	{"每15分钟", 15},
	{"每30分钟", 30},
	{"每1小时", 60},
	{"每2小时", 120},
	{"每4小时", 240},
	{"每6小时", 360},
	{"每12小时", 720},
	{"每天", 1440},
}

// parseTaskNameFromFile 从配置文件名反解任务名: backup-task.1-backup.json -> 1-backup。
// 空结果返回 "" (表示无法解析)。前后缀必须同时存在, 且前后缀之间去空白、去点后仍为空
// (如 backup-task...json / backup-task.json) 也视为无法解析, 防止注册非法任务名。
func parseTaskNameFromFile(fileName string) string {
	if !strings.HasPrefix(fileName, importCfgPrefix) || !strings.HasSuffix(fileName, importCfgSuffix) {
		return ""
	}
	// 中间部分 = 去掉前缀与后缀后剩下的任务名 (长度校验拦截 backup-task.json 这类无任务名)
	if len(fileName) <= len(importCfgPrefix)+len(importCfgSuffix) {
		return ""
	}
	name := fileName[len(importCfgPrefix) : len(fileName)-len(importCfgSuffix)]
	name = strings.TrimSpace(name)
	name = strings.Trim(name, ".")
	if name == "" {
		return ""
	}
	return name
}

// parseImportItemFile 读取并解析单个配置文件, 返回 importItem。
// 解析/读取失败不向上抛 error, 而是把原因写入 ParseErr, 保证扫描具备容错性,
// 单个坏文件不会中断整个导入流程。
func parseImportItemFile(path string) *importItem {
	it := &importItem{
		FileName:   filepath.Base(path),
		ConfigPath: path,
	}
	it.Name = parseTaskNameFromFile(it.FileName)
	if it.Name == "" {
		it.ParseErr = "无法从文件名解析任务名"
		return it
	}

	data, err := os.ReadFile(path)
	if err != nil {
		it.ParseErr = fmt.Sprintf("读取失败: %v", err)
		return it
	}
	var cfg taskConfig
	if err := json.Unmarshal(bytesTrim(data), &cfg); err != nil {
		it.ParseErr = fmt.Sprintf("JSON 解析失败: %v", err)
		return it
	}
	it.LocalDir = cfg.LocalDir
	it.RemoteDir = cfg.RemoteDir
	if strings.TrimSpace(it.LocalDir) == "" {
		it.ParseErr = "配置缺少 local_dir"
		return it
	}
	return it
}

// scanImportCandidates 扫描程序所在目录下的 backup-task.*.json 文件并解析为候选列表。
// 最后调用 existingTaskNameSet 建判重集合, 标记每个候选是否已存在同名计划任务。
func scanImportCandidates() ([]*importItem, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("获取程序路径失败: %v", err)
	}
	pattern := filepath.Join(filepath.Dir(exe), importCfgPrefix+"*"+importCfgSuffix)
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("扫描配置文件失败: %v", err)
	}

	items := make([]*importItem, 0, len(files))
	for _, f := range files {
		items = append(items, parseImportItemFile(f))
	}

	// 判重: 与已有计划任务同名 (大小写不敏感)
	set, err := existingTaskNameSet()
	if err != nil {
		return nil, fmt.Errorf("查询已有任务失败: %v", err)
	}
	for _, it := range items {
		if it.Name != "" {
			it.Exists = set[strings.ToLower(it.Name)]
		}
	}
	return items, nil
}

// existingTaskNameSet 查询一次计划任务列表, 返回小写任务名 -> 是否存在 的集合。
func existingTaskNameSet() (map[string]bool, error) {
	rows, err := ListTasks()
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(rows))
	for _, r := range rows {
		set[strings.ToLower(r.Name)] = true
	}
	return set, nil
}

// registerImportItem 将一个导入候选注册为计划任务。
// 任务描述中的类型/模式字段尽量从配置文件还原, 保证导入后的任务属性准确。
func registerImportItem(it *importItem, freqMin int, coreExe string) error {
	if it == nil || it.Name == "" {
		return fmt.Errorf("任务名为空, 无法注册")
	}

	// 从配置还原备份类型/模式, 供描述生成使用 (失败则按默认增量/普通处理)
	backupType := "incremental"
	mode := "normal"
	if data, err := os.ReadFile(it.ConfigPath); err == nil {
		var cfg taskConfig
		if json.Unmarshal(bytesTrim(data), &cfg) == nil {
			backupType = cfg.BackupType
			mode = cfg.Mode
		}
	}

	ti := &TaskInfo{
		Name:        it.Name,
		LocalDir:    it.LocalDir,
		RemoteDir:   it.RemoteDir,
		FreqMinutes: freqMin,
		FreqText:    freqText(freqMin),
		BackupType:  typeText(backupType),
		Mode:        modeText(mode),
		CreatedAt:   timeNow(),
		Execute:     coreExe,
		ConfigPath:  it.ConfigPath,
	}
	return ReRegisterTask(it.Name, coreExe, it.ConfigPath, freqMin, ti.BuildDescription())
}

// defaultImportFreqIndex 返回默认频率"每30分钟"在 importFreqPresets 中的下标。
func defaultImportFreqIndex() int {
	for i, p := range importFreqPresets {
		if p.min == defaultImportFreqMin {
			return i
		}
	}
	return 0
}
