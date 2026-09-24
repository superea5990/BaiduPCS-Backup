package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// taskFolder 计划任务统一目录 (带尾斜杠, CIM 查询必需)
const taskFolder = `\Microsoft\BaiduPCS-Go\`

// ---- 计划任务交互 (通过隐藏的 PowerShell 调用) ----

// psTimeout 所有外部调用(PS/核心exe)的统一超时, 防止进程卡住导致界面假死
const psTimeout = 30 * time.Second

// runPS 以隐藏窗口方式执行 PowerShell 脚本, 返回 UTF-8 输出; 超时自动终止
func runPS(script string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("PowerShell 执行超时(%v)", psTimeout)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s", decodeCP(msg))
	}
	return out, nil
}

// decodeCP 将可能为系统代码页(GBK)编码的文本转为 UTF-8。
// PowerShell 错误流(stderr)按系统代码页输出, 直接当 UTF-8 显示会乱码,
// 例如"拒绝访问"在中文系统上显示为"�ܾ����ʡ�"。
func decodeCP(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	if dec, err := simplifiedchinese.GBK.NewDecoder().String(s); err == nil {
		return dec
	}
	return s
}

// psEsc 转义 PowerShell 单引号字符串字面量
func psEsc(s string) string { return strings.ReplaceAll(s, "'", "''") }

// ---- 数据模型 ----

// taskConfig 与 backup 核心一致的配置文件结构
type taskConfig struct {
	LocalDir      string   `json:"local_dir"`
	RemoteDir     string   `json:"remote_dir"`
	Exclude       []string `json:"exclude"`
	Mode          string   `json:"mode"`           // normal | stealth
	BackupType    string   `json:"backup_type"`    // incremental | full
	Concurrency   int      `json:"concurrency"`    // 同时上传文件数
	BandwidthKBps int      `json:"bandwidth_kbps"` // 带宽 KB/s, 0=不限
	LogDir        string   `json:"log_dir"`
	MaxRetry      int      `json:"max_retry"`
}

// TaskInfo 主表一行
type TaskInfo struct {
	Name        string
	Seq         int
	State       string
	Interval    string // 触发 ISO8601 重复间隔, 如 PT30M
	Description string
	Execute     string
	Arguments   string
	LastRunTime string
	NextRunTime string

	// 派生
	ConfigPath  string
	LocalDir    string
	RemoteDir   string
	BackupType  string // 显示用: 增量/全量
	Mode        string // 显示用: 普通/无感
	FreqMinutes int
	FreqText    string
	CreatedAt   string

	config *taskConfig // 供编辑页读取并发/带宽等
}

// ---- 查询 ----

const listScript = `
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$rows = @()
try {
  $tasks = Get-ScheduledTask -TaskPath '\Microsoft\BaiduPCS-Go\'
  foreach($t in $tasks){
    $info = $t | Get-ScheduledTaskInfo
    $intv = ''
    if($t.Triggers -and $t.Triggers.Count -gt 0 -and $t.Triggers[0].Repetition){ $intv = $t.Triggers[0].Repetition.Interval }
    $sb = ''
    if($t.Triggers -and $t.Triggers.Count -gt 0){ $sb = $t.Triggers[0].StartBoundary }
    $lrt = ''; if($info.LastRunTime){ $lrt = $info.LastRunTime.ToString('yyyy-MM-dd HH:mm:ss') }
    $nrt = ''; if($info.NextRunTime){ $nrt = $info.NextRunTime.ToString('yyyy-MM-dd HH:mm:ss') }
    $rows += [PSCustomObject]@{
      Name=$t.TaskName; State=$t.State.ToString(); Execute=$t.Actions[0].Execute;
      Arguments=$t.Actions[0].Arguments; Interval=$intv; StartBoundary=$sb;
      Description=$t.Description; LastRunTime=$lrt; LastTaskResult=$info.LastTaskResult; NextRunTime=$nrt
    }
  }
} catch { }
if($rows.Count -eq 0){ '[]' } else { $rows | ConvertTo-Json -Compress }
`

type psTask struct {
	Name        string `json:"Name"`
	State       string `json:"State"`
	Execute     string `json:"Execute"`
	Arguments   string `json:"Arguments"`
	Interval    string `json:"Interval"`
	Description string `json:"Description"`
	LastRunTime string `json:"LastRunTime"`
	NextRunTime string `json:"NextRunTime"`
}

// ListTasks 查询任务文件夹下所有任务并补充配置信息, 按序号升序返回
func ListTasks() ([]*TaskInfo, error) {
	out, err := runPS(listScript)
	if err != nil {
		return nil, fmt.Errorf("查询计划任务失败: %v", err)
	}
	out = bytesTrim(out)
	if len(out) > 0 && out[0] == '{' {
		// 单任务时 ConvertTo-Json 输出对象而非数组, 统一包裹
		out = append(append([]byte("["), out...), ']')
	}
	var pts []psTask
	if err := json.Unmarshal(out, &pts); err != nil {
		return nil, fmt.Errorf("解析任务数据失败: %v", err)
	}
	var rows []*TaskInfo
	for i := range pts {
		t := &TaskInfo{
			Name: pts[i].Name, State: stateText(pts[i].State), Execute: pts[i].Execute,
			Arguments: pts[i].Arguments, Interval: pts[i].Interval,
			Description: pts[i].Description, LastRunTime: pts[i].LastRunTime,
			NextRunTime: pts[i].NextRunTime,
		}
		t.Seq = parseSeq(t.Name)
		t.ConfigPath = extractConfigPath(t.Arguments)
		t.loadConfig()
		t.FreqMinutes, _ = parseIntervalMinutes(t.Interval)
		t.FreqText = freqText(t.FreqMinutes)
		t.CreatedAt = parseCreatedAt(t.Description)
		rows = append(rows, t)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Seq < rows[j].Seq })
	return rows, nil
}

// ---- 派生的解析 ----

func bytesTrim(b []byte) []byte {
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	return bytes.TrimSpace(b)
}

// parseSeq 从 "n-backup" 解析序号
func parseSeq(name string) int {
	if idx := strings.IndexByte(name, '-'); idx > 0 {
		if n, err := strconv.Atoi(name[:idx]); err == nil {
			return n
		}
	}
	return 0
}

var cfgPathRe = regexp.MustCompile(`"([^"]+\.json)"`)

func extractConfigPath(args string) string {
	m := cfgPathRe.FindStringSubmatch(args)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

func (t *TaskInfo) loadConfig() {
	if t.ConfigPath == "" {
		return
	}
	data, err := os.ReadFile(t.ConfigPath)
	if err != nil {
		return
	}
	var cfg taskConfig
	if err := json.Unmarshal(bytesTrim(data), &cfg); err != nil {
		return
	}
	t.LocalDir = cfg.LocalDir
	t.RemoteDir = cfg.RemoteDir
	t.BackupType = typeText(cfg.BackupType)
	t.Mode = modeText(cfg.Mode)
	t.config = &cfg
}

// intervalRe ISO8601 重复间隔解析正则: PT30M / PT1H / P1D / PT1H30M 等
var intervalRe = regexp.MustCompile(`^P(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?(?:(\d+)D)?$`)

// parseIntervalMinutes 解析 ISO8601 重复间隔为分钟, 如 PT30M / PT1H / P1D
func parseIntervalMinutes(iso string) (int, error) {
	m := intervalRe.FindStringSubmatch(iso)
	if len(m) != 5 {
		return 0, fmt.Errorf("无法解析间隔: %s", iso)
	}
	var min int
	if m[4] != "" {
		d, _ := strconv.Atoi(m[4])
		min += d * 1440
	}
	if m[1] != "" {
		h, _ := strconv.Atoi(m[1])
		min += h * 60
	}
	if m[2] != "" {
		mm, _ := strconv.Atoi(m[2])
		min += mm
	}
	if min <= 0 {
		return 0, fmt.Errorf("间隔为0: %s", iso)
	}
	return min, nil
}

func freqText(min int) string {
	switch min {
	case 30:
		return "每30分钟"
	case 60:
		return "每小时"
	case 120:
		return "每2小时"
	case 360:
		return "每6小时"
	case 1440:
		return "每天"
	case 10080:
		return "每周"
	}
	return fmt.Sprintf("每%d分钟", min)
}

func typeText(t string) string {
	switch t {
	case "full":
		return "全量"
	default:
		return "增量"
	}
}

func modeText(m string) string {
	switch m {
	case "stealth":
		return "无感"
	default:
		return "普通"
	}
}

// stateText Windows 计划任务状态 -> 中文
func stateText(s string) string {
	switch s {
	case "Ready":
		return "准备就绪"
	case "Running":
		return "运行中"
	case "Disabled":
		return "已禁用"
	case "Queued":
		return "排队中"
	case "Unknown":
		return "未知"
	default:
		return s // 未知状态原样返回, 不吞掉
	}
}

// parseCreatedAt 从 Description 列表解析创建时间
func parseCreatedAt(desc string) string {
	for _, line := range strings.Split(desc, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "创建时间：") {
			return strings.TrimSpace(strings.TrimPrefix(line, "创建时间："))
		}
	}
	return ""
}

// ---- 任务操作 ----

func opTask(name, verb string) error {
	script := fmt.Sprintf("$ErrorActionPreference='Stop'; %s-ScheduledTask -TaskPath '%s' -TaskName '%s'",
		verb, taskFolder, psEsc(name))
	_, err := runPS(script)
	return err
}

func RunTask(name string) error   { return opTask(name, "Start") }
func StopTask(name string) error  { return opTask(name, "Stop") }
func DisableTask(name string) error { return opTask(name, "Disable") }
func EnableTask(name string) error { return opTask(name, "Enable") }

// timeNow 当前时间 yyyy-MM-dd HH:mm
func timeNow() string { return time.Now().Format("2006-01-02 15:04") }

// ---- 配置保存 ----

// SaveConfig 将任务配置写回配置文件 (保留 exclude/log_dir/max_retry 等未改字段)
func (t *TaskInfo) SaveConfig() error {
	if t.ConfigPath == "" {
		return fmt.Errorf("任务 %s 没有可用的配置文件路径", t.Name)
	}
	cfg := t.config
	if cfg == nil {
		cfg = &taskConfig{}
	}
	cfg.LocalDir = t.LocalDir
	cfg.RemoteDir = t.RemoteDir
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(t.ConfigPath, data, 0644)
}

// BuildDescription 按第十二节第5条多行列表格式生成任务描述
func (t *TaskInfo) BuildDescription() string {
	created := t.CreatedAt
	if created == "" {
		created = timeNow()
	}
	return fmt.Sprintf("源：%s\n目标：%s\n频率：%s\n类型：%s备份\n模式：%s模式\n创建时间：%s",
		t.LocalDir, t.RemoteDir, t.FreqText, t.BackupType, t.Mode, created)
}

// ReRegisterTask 重新注册任务: 先删除原任务, 再按原名称重新添加。
// 除"运行/结束/禁用"外任何修改都必须走此流程 (第十三节第4条约定)。
func ReRegisterTask(name, exe, configPath string, freqMin int, desc string) error {
	wd := filepath.Dir(exe)
	script := fmt.Sprintf(`$ErrorActionPreference='Stop'
# 确保任务文件夹存在
$svc = New-Object -ComObject Schedule.Service
$svc.Connect()
try { $null = $svc.GetFolder('\Microsoft\BaiduPCS-Go') } catch { $svc.GetFolder('\Microsoft').CreateFolder('BaiduPCS-Go') | Out-Null }
$name = '%s'
$exe = '%s'
$cfg = '%s'
$wd = '%s'
$freqMin = %d
$desc = '%s'
# 1) 先删除原任务
Unregister-ScheduledTask -TaskPath '\Microsoft\BaiduPCS-Go\' -TaskName $name -Confirm:$false -ErrorAction SilentlyContinue
# 2) 按原名称重新添加
$action = New-ScheduledTaskAction -Execute $exe -Argument ('backup run "' + $cfg + '"') -WorkingDirectory $wd
$trigger = New-ScheduledTaskTrigger -Once -At (Get-Date) -RepetitionInterval (New-TimeSpan -Minutes $freqMin) -RepetitionDuration (New-TimeSpan -Days 3650)
# S4U: 不管用户是否登录都运行, 免密码, 后台会话运行不弹出黑窗口 (需管理员权限注册)
$principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType S4U -RunLevel Limited
$settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit (New-TimeSpan -Minutes 240) -MultipleInstances IgnoreNew -StartWhenAvailable -RestartCount 2 -RestartInterval (New-TimeSpan -Minutes 1)
Register-ScheduledTask -TaskPath '\Microsoft\BaiduPCS-Go\' -TaskName $name -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
# 3) 写入描述 (多行列表格式)
[xml]$doc = Export-ScheduledTask -TaskPath '\Microsoft\BaiduPCS-Go\' -TaskName $name
$ri = $doc.Task.RegistrationInfo
if ($null -eq $ri) { $ri = $doc.CreateElement('RegistrationInfo', 'http://schemas.microsoft.com/windows/2004/02/mit/task'); $doc.Task.AppendChild($ri) | Out-Null }
$dn = $ri.SelectSingleNode("//*[local-name()='Description']")
if ($null -eq $dn) { $dn = $doc.CreateElement('Description', 'http://schemas.microsoft.com/windows/2004/02/mit/task'); $ri.AppendChild($dn) | Out-Null }
$dn.InnerText = $desc
Register-ScheduledTask -TaskPath '\Microsoft\BaiduPCS-Go\' -TaskName $name -Xml $doc.OuterXml -Force | Out-Null
Write-Output 'OK'`,
		psEsc(name), psEsc(exe), psEsc(configPath), psEsc(wd), freqMin, psEsc(desc))
	_, err := runPS(script)
	if err != nil {
		if strings.Contains(err.Error(), "拒绝访问") || strings.Contains(err.Error(), "0x80070005") {
			return fmt.Errorf("注册任务失败: 拒绝访问(0x80070005), 请以管理员身份运行本程序后重试。原始错误: %v", err)
		}
		return err
	}
	return nil
}

// EnsureTaskFolder 校验任务计划程序中的 \Microsoft\BaiduPCS-Go 文件夹是否存在,
// 不存在则尝试自动创建。返回 (是否新建, 错误)。
func EnsureTaskFolder() (bool, error) {
	const script = `$ErrorActionPreference='Stop'
$svc = New-Object -ComObject Schedule.Service
$svc.Connect()
try {
  $null = $svc.GetFolder('\Microsoft\BaiduPCS-Go')
  Write-Output 'OK'
} catch {
  try {
    $null = $svc.GetFolder('\Microsoft').CreateFolder('BaiduPCS-Go')
    Write-Output 'CREATED'
  } catch {
    Write-Error $_.Exception.Message
    exit 1
  }
}`
	out, err := runPS(script)
	if err != nil {
		if strings.Contains(err.Error(), "拒绝访问") || strings.Contains(err.Error(), "0x80070005") {
			return false, fmt.Errorf("校验失败: 拒绝访问(0x80070005), 请以管理员身份运行本程序。原始错误: %v", err)
		}
		return false, err
	}
	return strings.Contains(string(out), "CREATED"), nil
}

// ---- BaiduPCS 核心程序调用 (用户验证等) ----

// runCore 隐藏窗口方式调用 BaiduPCS 核心 exe (who/quota 等), 输出为 UTF-8; 超时自动终止
func runCore(coreExe string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, coreExe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return out, fmt.Errorf("调用核心程序超时(%v)", psTimeout)
		}
	}
	return out, err
}

var (
	coreExeOnce  sync.Once
	coreExeCache string
)

// ResolveCoreExe 定位 BaiduPCS 核心程序: 优先从任务动作取, 其次默认相对路径。
// 结果进程内缓存 (文件被删/移动后自动重新解析)。
func ResolveCoreExe() string {
	coreExeOnce.Do(func() { coreExeCache = resolveCoreExe() })
	if coreExeCache != "" {
		if fi, err := os.Stat(coreExeCache); err == nil && !fi.IsDir() {
			return coreExeCache
		}
	}
	coreExeCache = resolveCoreExe()
	return coreExeCache
}

func resolveCoreExe() string {
	if rows, err := ListTasks(); err == nil {
		for _, r := range rows {
			if r.Execute != "" {
				if fi, err := os.Stat(r.Execute); err == nil && !fi.IsDir() {
					return r.Execute
				}
			}
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	// 两种布局都兼容:
	//   1) 绿色版: 核心在 GUI 同级子目录  BaiduPCS-Go\
	//   2) 开发/正式: 核心在 GUI 上一级目录  ..\BaiduPCS-Go\
	for _, cand := range []string{
		filepath.Join(dir, "BaiduPCS-Go", "BaiduPCS-Backup.exe"),
		filepath.Join(dir, "..", "BaiduPCS-Go", "BaiduPCS-Backup.exe"),
		filepath.Join(dir, "BaiduPCS-Go", "BaiduPCS-Go.exe"),
		filepath.Join(dir, "..", "BaiduPCS-Go", "BaiduPCS-Go.exe"),
	} {
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand
		}
	}
	return ""
}

// VerifyLogin 执行 who + quota, 返回 "用户名: X, 总空间: A, 已用空间: B, 比率: C%"
func VerifyLogin(coreExe string) (string, error) {
	whoOut, err1 := runCore(coreExe, "who")
	quotaOut, err2 := runCore(coreExe, "quota")
	if err1 != nil && err2 != nil {
		return "", fmt.Errorf("who: %v; quota: %v", err1, err2)
	}
	user := extractKV(whoOut, "用户名: ")
	if user == "" {
		user = extractKV(quotaOut, "用户名: ")
	}
	total := extractKV(quotaOut, "总空间: ")
	used := extractKV(quotaOut, "已用空间: ")
	ratio := extractKV(quotaOut, "比率: ")
	return strings.TrimSpace(fmt.Sprintf("用户名: %s, 总空间: %s, 已用空间: %s, 比率: %s", user, total, used, ratio)), nil
}

// extractKV 从输出中取 "key" 之后到逗号/换行的字段值
func extractKV(b []byte, key string) string {
	s := string(b)
	idx := strings.Index(s, key)
	if idx < 0 {
		return ""
	}
	rest := s[idx+len(key):]
	end := strings.IndexAny(rest, ",\n\r")
	if end < 0 {
		end = len(rest)
	}
	return strings.TrimSpace(rest[:end])
}

// ---- 任务管理 (删除/重命名/导出) ----

// RenameTask 重命名计划任务: 导出XML->按新名注册->删除原名 (保留触发/动作/设置/描述)
func RenameTask(oldName, newName string) error {
	script := fmt.Sprintf(`$ErrorActionPreference='Stop'
[xml]$doc = Export-ScheduledTask -TaskPath '\Microsoft\BaiduPCS-Go\' -TaskName '%s'
Register-ScheduledTask -TaskPath '\Microsoft\BaiduPCS-Go\' -TaskName '%s' -Xml $doc.OuterXml -Force | Out-Null
Unregister-ScheduledTask -TaskPath '\Microsoft\BaiduPCS-Go\' -TaskName '%s' -Confirm:$false
Write-Output 'OK'`, psEsc(oldName), psEsc(newName), psEsc(oldName))
	_, err := runPS(script)
	return err
}

// DeleteTask 删除计划任务 (不删配置文件, 由调用方决定)
func DeleteTask(name string) error {
	script := fmt.Sprintf(`$ErrorActionPreference='Stop'
Unregister-ScheduledTask -TaskPath '\Microsoft\BaiduPCS-Go\' -TaskName '%s' -Confirm:$false
Write-Output 'OK'`, psEsc(name))
	_, err := runPS(script)
	return err
}

// TaskExists 判断指定名称的计划任务是否已存在 (任务名不区分大小写)
func TaskExists(name string) bool {
	rows, err := ListTasks()
	if err != nil {
		return false
	}
	for _, r := range rows {
		if strings.EqualFold(r.Name, name) {
			return true
		}
	}
	return false
}

// ExportTaskXML 导出计划任务XML文本
func ExportTaskXML(name string) (string, error) {
	script := fmt.Sprintf(`[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
Export-ScheduledTask -TaskPath '\Microsoft\BaiduPCS-Go\' -TaskName '%s'`, psEsc(name))
	out, err := runPS(script)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ---- 新任务配置 ----

// sanitizePath 本地路径 -> 安全文件名片段, 如 E:/BaiduPCS-Go -> E-BaiduPCS-Go
func sanitizePath(p string) string {
	p = strings.ReplaceAll(p, ":", "")
	p = strings.ReplaceAll(p, "/", "-")
	p = strings.ReplaceAll(p, "\\", "-")
	return p
}

// TaskConfigDir 任务配置目录: 优先已有任务配置所在目录, 否则 GUI 所在目录
func TaskConfigDir() string {
	if rows, err := ListTasks(); err == nil {
		for _, r := range rows {
			if r.ConfigPath != "" {
				return filepath.Dir(r.ConfigPath)
			}
		}
	}
	exe, _ := os.Executable()
	return filepath.Dir(exe)
}

// defaultLogDir 默认日志目录 = 主程序(GUI)所在目录下的 log 文件夹
func defaultLogDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "log")
}

// NewTaskConfigPath 为新任务生成配置文件路径。
// 按"任务名"命名 (backup-task.<任务名>.json), 避免同源路径多任务共用/误删配置。
func NewTaskConfigPath(name string) string {
	return filepath.Join(TaskConfigDir(), "backup-task."+sanitizePath(name)+".json")
}

// nextSeq 计算下一个任务序号 = 现有最大序号 + 1
func nextSeq(rows []*TaskInfo) int {
	max := 0
	for _, r := range rows {
		if r.Seq > max {
			max = r.Seq
		}
	}
	return max + 1
}

// ---- GUI 配置 (用户验证勾选、主界面分隔条位置等) ----

type guiConfig struct {
	VerifyOnStart  bool              `json:"verify_on_start"`
	CheckTaskFolder bool             `json:"check_task_folder"` // 启动时是否校验计划任务文件夹, 默认 true
	Settings       map[string]string `json:"settings"`          // walk 持久化状态(如分隔条位置), key=控件路径
}

func guiConfigFilePath() string {
	exe, _ := os.Executable()
	return filepath.Join(filepath.Dir(exe), "gui_config.json")
}

// fileSettings 把 walk 的持久化状态(主界面分隔条位置等)读写到 gui_config.json 的 settings 字段。
// 实现 walk.Settings 接口, 通过 walk.App().SetSettings() 接入: walk 在窗口 Show 时 RestoreState、
// 关闭时 SaveState, 从而自动完成"拖动分隔条→写配置"和"打开主界面→读配置"。
type fileSettings struct {
	mu   sync.Mutex
	path string
	cfg  guiConfig
}

var appSettings *fileSettings

func ensureAppSettings() *fileSettings {
	if appSettings == nil {
		appSettings = &fileSettings{path: guiConfigFilePath()}
		appSettings.cfg.Settings = make(map[string]string)
		appSettings.load()
	}
	return appSettings
}

func (fs *fileSettings) load() {
	data, err := os.ReadFile(fs.path)
	if err != nil {
		return
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // 兼容 UTF-8 BOM
	json.Unmarshal(data, &fs.cfg)
	if fs.cfg.Settings == nil {
		fs.cfg.Settings = make(map[string]string)
	}
}

func (fs *fileSettings) save() error {
	data, err := json.MarshalIndent(fs.cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(fs.path, data, 0644)
}

// ---- walk.Settings 接口实现 ----

func (fs *fileSettings) Get(key string) (string, bool) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	v, ok := fs.cfg.Settings[key]
	return v, ok
}

func (fs *fileSettings) Put(key, value string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.cfg.Settings[key] = value
	return fs.save()
}

func (fs *fileSettings) PutExpiring(key, value string) error { return fs.Put(key, value) }

func (fs *fileSettings) Remove(key string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	delete(fs.cfg.Settings, key)
	return fs.save()
}

func (fs *fileSettings) Timestamp(key string) (time.Time, bool) { return time.Time{}, false }
func (fs *fileSettings) ExpireDuration() time.Duration          { return 0 }
func (fs *fileSettings) SetExpireDuration(time.Duration)        {}

func (fs *fileSettings) Load() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.load()
	return nil
}

func (fs *fileSettings) Save() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.save()
}

func loadGUIConfig() guiConfig {
	fs := ensureAppSettings()
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.cfg
}

func saveGUIConfig(c guiConfig) error {
	fs := ensureAppSettings()
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.cfg = c
	return fs.save()
}
