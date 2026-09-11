package pcscommand

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsfunctions/pcsupload"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/converter"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
)

// 退出码约定: 0=成功, 1=部分失败(有文件上传失败), 2=配置/登录/锁/未实现等错误
const (
	BackupExitOK          = 0
	BackupExitPartialFail = 1
	BackupExitConfigError = 2
)

// TaskConfig 备份任务配置 (JSON)
type TaskConfig struct {
	LocalDir      string   `json:"local_dir"`       // 待备份的本地目录
	RemoteDir     string   `json:"remote_dir"`      // 网盘目标目录, 以 / 开头
	Exclude       []string `json:"exclude"`         // 排除规则, 支持 * ? 与 ** (跨多级)
	Mode          string   `json:"mode"`            // smart | normal | stealth (智能模式阶段3实现)
	BackupType    string   `json:"backup_type"`     // incremental(默认,按大小增量) | full(覆盖上传)
	Concurrency   int      `json:"concurrency"`     // 同时上传文件数 (普通模式)
	BandwidthKBps int      `json:"bandwidth_kbps"`  // 上传带宽限制 KB/s, 0=不限 (普通/无感模式)
	LogDir        string   `json:"log_dir"`         // 日志目录, 为空时默认 <主程序所在目录>/log
	MaxRetry      int      `json:"max_retry"`       // 上传失败最大重试次数
}

func (tc *TaskConfig) normalize() {
	tc.LocalDir = filepath.Clean(tc.LocalDir)
	if tc.Mode == "" {
		tc.Mode = "normal"
	}
	if tc.BackupType == "" {
		tc.BackupType = "incremental"
	}
	if tc.MaxRetry <= 0 {
		tc.MaxRetry = DefaultUploadMaxRetry
	}
	if tc.LogDir == "" {
		// 默认日志目录 = 主程序所在目录下的 log 文件夹 (GUI 创建任务时已显式写入, 此处为命令行直接运行时的兜底)
		if exe, err := os.Executable(); err == nil {
			tc.LogDir = filepath.Join(filepath.Dir(exe), "log")
		} else {
			tc.LogDir = filepath.Join(tc.LocalDir, ".baklog")
		}
	}
	// 性能模式参数
	switch tc.Mode {
	case "stealth": // 无感模式: 默认单任务, 200KB/s
		tc.Concurrency = 1
		tc.BandwidthKBps = 200
	case "smart": // 智能模式: 阶段3实现, 此处保持占位
	case "normal": // 普通模式: 使用配置文件中的固定值
		if tc.Concurrency <= 0 {
			tc.Concurrency = 2
		}
	default:
		tc.Mode = "normal"
	}
}

// backupFile 待上传文件
type backupFile struct {
	abs string // 本地绝对路径
	rel string // 相对 local_dir 的路径, 使用 / 分隔
}

// RunBackup 执行一次备份任务, 返回退出码 (0/1/2)
func RunBackup(configPath string) int {
	// 1. 读取配置 (兼容 UTF-8 BOM)
	data, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[备份] 读取任务配置失败: %v\n", err)
		return BackupExitConfigError
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var tc TaskConfig
	if err := json.Unmarshal(data, &tc); err != nil {
		fmt.Fprintf(os.Stderr, "[备份] 解析任务配置失败: %v\n", err)
		return BackupExitConfigError
	}
	tc.normalize()

	// 2. 校验
	info, err := os.Stat(tc.LocalDir)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "[备份] 本地目录无效: %s (%v)\n", tc.LocalDir, err)
		return BackupExitConfigError
	}
	if !strings.HasPrefix(tc.RemoteDir, "/") {
		fmt.Fprintf(os.Stderr, "[备份] 网盘目录必须是以 / 开头的绝对路径: %s\n", tc.RemoteDir)
		return BackupExitConfigError
	}
	if tc.Mode == "smart" {
		fmt.Fprintf(os.Stderr, "[备份] 智能模式尚未实现(规划于阶段3), 请使用 normal 或 stealth\n")
		return BackupExitConfigError
	}

	// 3. 登录检查
	if pcsconfig.Config.NumLogins() == 0 || pcsconfig.Config.ActiveUser().BDUSS == "" {
		fmt.Fprintf(os.Stderr, "[备份] 未登录百度账号, 请先执行 login 登录后再备份\n")
		return BackupExitConfigError
	}

	// 4. 日志目录
	if err := os.MkdirAll(tc.LogDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "[备份] 创建日志目录失败: %v\n", err)
		return BackupExitConfigError
	}
	// 日志目录位于待备份目录内时, 自动排除
	if rel, err := filepath.Rel(tc.LocalDir, tc.LogDir); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		tc.Exclude = append(tc.Exclude, filepath.ToSlash(rel))
	}

	// 5. 单实例锁 (含残留锁检测: 持有进程已退出则自动接管)
	lockPath := filepath.Join(tc.LogDir, "backup.lock")
	lockF, err := acquireBackupLock(lockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[备份] 已有备份任务在运行(锁文件: %s), 本次跳过\n", lockPath)
		return BackupExitConfigError
	}
	lockF.Close()
	defer os.Remove(lockPath)

	// 6. 打开日志文件并重定向输出 (上传进度等全部写入日志)
	logName := "backup-" + time.Now().Format("2006-01-02") + ".log"
	logF, err := os.OpenFile(filepath.Join(tc.LogDir, logName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[备份] 打开日志文件失败: %v\n", err)
		return BackupExitConfigError
	}
	defer logF.Close()
	oldStdout, oldStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = logF, logF
	defer func() { os.Stdout, os.Stderr = oldStdout, oldStderr }()

	logLine := func(format string, args ...interface{}) {
		fmt.Fprintf(logF, "[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
	}

	logLine("备份任务启动: 本地=%s 网盘=%s 模式=%s 类型=%s 并发=%d 带宽=%dKB/s", tc.LocalDir, tc.RemoteDir, tc.Mode, tc.BackupType, tc.Concurrency, tc.BandwidthKBps)

	// 7. 遍历本地目录, 应用排除规则
	var files []backupFile
	err = filepath.Walk(tc.LocalDir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(tc.LocalDir, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if matchesExclude(rel, tc.Exclude) {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !fi.IsDir() {
			if fi.Size() == 0 {
				// 与上游行为一致: 跳过 0 字节文件
				logLine("跳过空文件(0字节): %s", rel)
				return nil
			}
			files = append(files, backupFile{abs: p, rel: rel})
		}
		return nil
	})
	if err != nil {
		logLine("遍历本地目录失败: %v", err)
		return BackupExitConfigError
	}
	if len(files) == 0 {
		logLine("没有需要上传的文件")
		return BackupExitOK
	}
	logLine("待上传文件数: %d", len(files))

	// 8. 确保网盘目标目录存在 (忽略已存在等错误)
	pcs := GetBaiduPCS()
	_ = pcs.Mkdir(tc.RemoteDir)

	// 9. 限速与上传策略 (限速通过全局配置临时生效, 结束后恢复)
	oldRate := pcsconfig.Config.MaxUploadRate
	defer func() { pcsconfig.Config.MaxUploadRate = oldRate }()
	if tc.BandwidthKBps > 0 {
		pcsconfig.Config.MaxUploadRate = int64(tc.BandwidthKBps) * 1024 // KB/s -> B/s
	} else {
		pcsconfig.Config.MaxUploadRate = 0
	}
	policy := baidupcs.RsyncPolicy // incremental: 仅跳过大小未变化的文件
	if tc.BackupType == "full" {
		policy = baidupcs.OverWritePolicy
	}

	// 10. 上传
	uploadDatabase, err := pcsupload.NewUploadingDatabase()
	if err != nil {
		logLine("打开上传断点数据库失败: %v", err)
		return BackupExitConfigError
	}
	defer uploadDatabase.Close()

	executor := &taskframework.TaskExecutor{IsFailedDeque: true}
	statistic := &pcsupload.UploadStatistic{}

	// 优雅退出: 第一次 Ctrl+C 停止派发新任务并等待进行中的分片完成, 第二次强制退出
	sigChan := make(chan os.Signal, 2)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n收到退出信号, 等待进行中的分片完成 (再次 Ctrl+C 强制退出)...")
		executor.Stop()
		pcsupload.GracefulStopActiveUploaders()
		<-sigChan
		fmt.Println("强制退出")
		os.Exit(130)
	}()

	statistic.StartTimer()
	load := tc.Concurrency
	if load <= 0 {
		load = 1
	}
	for _, f := range files {
		taskInfo := executor.Append(&pcsupload.UploadTaskUnit{
			LocalFileChecksum: checksum.NewLocalFileChecksum(f.abs, int(baidupcs.SliceMD5Size)),
			SavePath:          path.Join(tc.RemoteDir, f.rel),
			PCS:               pcs.CopyPCS(),
			UploadingDatabase: uploadDatabase,
			Parallel:          1,
			PrintFormat:       pcsupload.DefaultPrintFormat,
			NoRapidUpload:     false,
			NoSplitFile:       false,
			UploadStatistic:   statistic,
			Policy:            policy,
		}, tc.MaxRetry)
		logLine("加入上传队列: %s -> %s", f.rel, taskInfo.Id())
	}
	if executor.Count() == 0 {
		logLine("未检测到可上传的文件")
		return BackupExitOK
	}
	executor.SetParallel(load)
	executor.Execute()

	failedCount := executor.FailedDeque().Size()
	logLine("备份结束, 耗时: %v, 总大小: %s, 失败文件数: %d", statistic.Elapsed(), converter.ConvertFileSize(statistic.TotalSize()), failedCount)

	if failedCount > 0 {
		return BackupExitPartialFail
	}
	return BackupExitOK
}

// acquireBackupLock 尝试获取单实例锁; 锁文件已存在但持有进程已退出时视为残留并自动接管
func acquireBackupLock(lockPath string) (*os.File, error) {
	lockF, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err == nil {
		fmt.Fprintf(lockF, "%d\n", os.Getpid())
		return lockF, nil
	}
	// O_EXCL 失败: 检查是否为残留锁 (持有进程已退出)
	if data, rerr := os.ReadFile(lockPath); rerr == nil {
		var pid int
		if _, serr := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); serr == nil && !processAlive(pid) {
			_ = os.Remove(lockPath)
			lockF, err = os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
			if err == nil {
				fmt.Fprintf(lockF, "%d\n", os.Getpid())
				return lockF, nil
			}
		}
	}
	return nil, err
}

// matchesExclude 判断相对路径(使用 / 分隔)是否命中任一排除规则
func matchesExclude(rel string, patterns []string) bool {
	for _, p := range patterns {
		if p == "" {
			continue
		}
		p = strings.Trim(filepath.ToSlash(p), "/")
		if matchPattern(rel, p) {
			return true
		}
		// 单段规则同时按"任意层级下的同名文件/目录"匹配
		if !strings.Contains(p, "/") {
			if filepath.Base(rel) == p {
				return true
			}
			if ok, _ := filepath.Match(p, filepath.Base(rel)); ok {
				return true
			}
		}
	}
	return false
}

// matchPattern 将模式按 / 分段匹配, 支持 ** (跨任意多级) 与 * ? 等单段通配
func matchPattern(rel, pattern string) bool {
	return matchSegs(strings.Split(rel, "/"), strings.Split(pattern, "/"))
}

func matchSegs(rel, pat []string) bool {
	if len(pat) == 0 {
		return len(rel) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(rel); i++ {
			if matchSegs(rel[i:], pat[1:]) {
				return true
			}
		}
		return false
	}
	if len(rel) == 0 {
		return false
	}
	ok, _ := filepath.Match(pat[0], rel[0])
	if !ok {
		return false
	}
	return matchSegs(rel[1:], pat[1:])
}
