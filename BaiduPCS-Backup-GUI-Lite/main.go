package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

type mainUI struct {
	*walk.MainWindow
	model     *taskTableModel
	table     *walk.TableView
	statusLbl *walk.Label
	loginLbl  *walk.Label
	log       *walk.TextEdit
	chkVerify *walk.CheckBox
	refreshMu sync.Mutex    // 保护 refreshing
	refreshing bool
	runMu     sync.Mutex   // 保护 running
	running   map[string]bool // 正在手动备份中的任务名, 防止同一任务重复执行
}

// taskTableModel 主表数据模型
type taskTableModel struct {
	walk.TableModelBase
	rows []*TaskInfo
}

func (m *taskTableModel) RowCount() int { return len(m.rows) }

func (m *taskTableModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.rows) {
		return ""
	}
	t := m.rows[row]
	switch col {
	case 0:
		return t.Seq
	case 1:
		return t.Name
	case 2:
		return t.State
	case 3:
		return t.LocalDir
	case 4:
		return t.RemoteDir
	case 5:
		return t.FreqText
	case 6:
		return t.BackupType + "备份"
	case 7:
		return t.Mode + "模式"
	case 8:
		return t.CreatedAt
	}
	return ""
}

func main() {
	mw, err := walk.NewMainWindow()
	if err != nil {
		writeStartupErr(err)
		return
	}
	ui := &mainUI{MainWindow: mw, running: make(map[string]bool)}
	ui.model = &taskTableModel{}

	mw.SetTitle("BaiduPCS-Go 备份管理器")
	mw.SetSize(walk.Size{Width: 1160, Height: 620})
	mw.SetMinMaxSize(walk.Size{Width: 600, Height: 380}, walk.Size{Width: 0, Height: 0})

	// 窗口左上角图标: 优先从 exe 嵌入资源加载(rsrc 生成: manifest=ID1, 图标组=ID2),
	// 失败则回退到 exe 同目录的 netdisk_logo.ico
	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)
	var winIcon *walk.Icon
	for _, id := range []int{2, 1} {
		if ic, err := walk.NewIconFromResourceId(id); err == nil {
			winIcon = ic
			break
		}
	}
	if winIcon == nil {
		winIcon, _ = walk.NewIconFromFile(filepath.Join(exeDir, "netdisk_logo.ico"))
	}
	if winIcon != nil {
		mw.SetIcon(winIcon)
	}

	// 自定义设置源: 主界面分隔条位置等 walk 持久化状态写入 gui_config.json。
	// 只手动存取分隔条状态(vs.RestoreState/vs.SaveState), 不置窗口本身 Persistent,
	// 避免 walk 顺带持久化窗口位置/尺寸(保持主界面默认尺寸不变)。
	walk.App().SetSettings(ensureAppSettings())
	mw.SetName("main")

	root := walk.NewVBoxLayout()
	root.SetMargins(walk.Margins{HNear: 8, VNear: 8, HFar: 8, VFar: 8})
	root.SetSpacing(6)
	mw.SetLayout(root)

	// 三段时间距可调: 用垂直分隔条容纳 主表/功能区/输出区, 拖动分隔条调整高度并自动保存
	vs, _ := walk.NewVSplitter(mw)
	vs.SetName("mainSplitter")
	vs.SetPersistent(true)
	root.SetStretchFactor(vs, 1)

	// ============ 上部: 主表 (分隔条内) ============
	ui.table, _ = walk.NewTableView(vs)
	ui.table.SetModel(ui.model)
	ui.table.SetAlternatingRowBG(true)
	cols := ui.table.Columns()
	addCol := func(title string, width int, format string) {
		c := walk.NewTableViewColumn()
		c.SetName(title) // 列名用于持久化列宽
		c.SetTitle(title)
		c.SetWidth(width)
		c.SetFormat(format)
		cols.Add(c)
	}
	addCol("序号", 60, "%d")
	addCol("任务名称", 140, "%v")
	addCol("状态", 80, "%v")
	addCol("源", 240, "%v")
	addCol("目标", 180, "%v")
	addCol("频率", 90, "%v")
	addCol("类型", 60, "%v")
	addCol("模式", 60, "%v")
	addCol("创建时间", 130, "%v")

	// 主表列宽调整持久化: 命名表格并置为持久化, 分隔条 Save/RestoreState 会自动级联
	// 保存/恢复各列宽度到 gui_config.json, 打开主界面时按配置参数显示列间距。
	ui.table.SetName("mainTable")
	ui.table.SetPersistent(true)

	// 窗口缩放时自适应: 各列总宽放不下就启用横向滚动条(超出的列可左右滑动查看),
	// 放得下则只留纵向滚动条、末列自动拉伸填满右侧空白(保持原有观感)。
	// 纵向滚动条始终保留, 任务条数超出可视高度时可上下滑动。
	// 同时设主表最小高度, 过矮时下方内容溢出被窗口裁剪, 而不是把各段压扁。
	ui.table.SetScrollbarOrientation(walk.Vertical)
	ui.table.SetMinMaxSize(walk.Size{Width: 0, Height: 150}, walk.Size{Width: 0, Height: 0})
	ui.table.SizeChanged().Attach(func() { syncTableScrollbar(ui.table) })

	// ============ 中部: 功能区 (分隔条内, 合并为单个面板) ============
	mid, _ := walk.NewComposite(vs)
	mh := walk.NewVBoxLayout()
	mh.SetSpacing(6)
	mid.SetLayout(mh)

	row1, _ := walk.NewComposite(mid)
	h1 := walk.NewHBoxLayout()
	h1.SetSpacing(6)
	row1.SetLayout(h1)

	btnAdd := mkBtn(row1, "添加任务", 90)
	btnDel := mkBtn(row1, "删除选中", 90)
	btnRun := mkBtn(row1, "手动执行", 90)
	_, _ = walk.NewHSpacer(row1)
	ui.chkVerify, _ = walk.NewCheckBox(row1)
	ui.chkVerify.SetText("用户验证")
	btnSettings := mkBtn(row1, "设置", 70)

	row2, _ := walk.NewComposite(mid)
	h2 := walk.NewHBoxLayout()
	h2.SetSpacing(6)
	row2.SetLayout(h2)

	btnStartSched := mkBtn(row2, "启动任务", 90)
	btnStopSched := mkBtn(row2, "停止任务", 90)
	btnExport := mkBtn(row2, "导出计划任务", 110)
	btnOpen := mkBtn(row2, "打开计划任务", 110)
	_, _ = walk.NewHSpacer(row2)
	btnRefresh := mkBtn(row2, "刷新", 70)
	btnClearLog := mkBtn(row2, "清屏", 70)

	// ============ 下部: 输出区 (分隔条内) ============
	ui.log, _ = walk.NewTextEdit(vs)
	ui.log.SetReadOnly(true)
	ui.log.SetMinMaxSize(walk.Size{Width: 0, Height: 110}, walk.Size{Width: 0, Height: 0})
	// 关闭自动换行: 每个操作的返回信息保持单独一行, 超长内容横向滚动而非折成多行
	// 同时启用垂直滚动条(WS_VSCROLL): 日志过多时右侧出现可滑动滚动条, 可回看历史
	style := win.GetWindowLong(ui.log.Handle(), win.GWL_STYLE)
	win.SetWindowLong(ui.log.Handle(), win.GWL_STYLE, style|win.ES_AUTOHSCROLL|win.WS_VSCROLL)

	// 状态栏
	stRow, _ := walk.NewComposite(mw)
	sh := walk.NewHBoxLayout()
	sh.SetSpacing(6)
	stRow.SetLayout(sh)
	ui.statusLbl, _ = walk.NewLabel(stRow)
	ui.statusLbl.SetText("就绪")
	_, _ = walk.NewHSpacer(stRow)
	ui.loginLbl, _ = walk.NewLabel(stRow)
	ui.loginLbl.SetText("网盘未登录")

	// ============ 事件 ============
	btnAdd.Clicked().Attach(ui.addTask)
	btnDel.Clicked().Attach(ui.delTask)
	btnRun.Clicked().Attach(ui.runBackupNow)
	btnSettings.Clicked().Attach(func() { showSettingsDialog(ui) })
	btnStartSched.Clicked().Attach(func() { ui.taskOp("启动任务", RunTask) })
	btnStopSched.Clicked().Attach(func() { ui.taskOp("停止任务", StopTask) })
	btnExport.Clicked().Attach(ui.exportTask)
	btnOpen.Clicked().Attach(ui.openScheduler)
	btnRefresh.Clicked().Attach(ui.refresh)
	btnClearLog.Clicked().Attach(ui.clearLog)
	ui.table.ItemActivated().Attach(ui.editSelected)

	mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		// 关闭时保存分隔条位置到 gui_config.json (拖动后下次打开自动还原)
		vs.SaveState()
	})

	// 启动: 先恢复勾选状态(不触发事件), 再挂事件; 勾选则启动时验证登录
	cfg := loadGUIConfig()
	ui.chkVerify.SetChecked(cfg.VerifyOnStart)
	ui.chkVerify.CheckedChanged().Attach(ui.onVerifyToggled)
	if cfg.VerifyOnStart {
		ui.verifyLogin()
	}

	// 启动时按配置校验计划任务文件夹 \Microsoft\BaiduPCS-Go\ 是否存在 (默认校验)
	// 若不存在则尝试自动创建, 避免后续所有任务注册失败; 校验结果输出到日志区
	if cfg.CheckTaskFolder {
		go func() {
			created, err := EnsureTaskFolder()
			ui.Synchronize(func() {
				if err != nil {
					ui.logf("任务文件夹校验失败: %v", err)
					ui.statusLbl.SetText("任务文件夹校验失败")
				} else if created {
					ui.logf("任务计划文件夹 \\Microsoft\\BaiduPCS-Go 不存在, 已自动创建")
				} else {
					ui.logf("任务计划文件夹 \\Microsoft\\BaiduPCS-Go 校验通过")
				}
			})
		}()
	}

	// 首次运行(配置中尚无分隔条位置)时预置默认值, 保持与旧版一致的主表大/输出区小的布局。
	// key 为 walk 持久化路径: 窗口名/clientComposite/分隔条名。
	const splitKey = "main/clientComposite/mainSplitter"
	if _, ok := walk.App().Settings().Get(splitKey); !ok {
		walk.App().Settings().Put(splitKey, "350 62 116")
	}
	// 读取已保存的分隔条位置 (未保存时用上方预置默认值)
	vs.RestoreState()

	ui.refresh()
	ui.startAutoRefresh()
	mw.Show()
	mw.Run()
}

// syncTableScrollbar 按主表当前可视宽度决定横向滚动条策略:
//   - 各列总宽 <= 可视宽度: 只保留纵向滚动条, 末列自动拉伸填满右侧空白(原有观感不变)
//   - 各列总宽 >  可视宽度: 同时启用横向滚动条, 超出的列可左右滑动查看, 不再被裁剪
//
// walk 的 SetScrollbarOrientation 会控制是否剥离 WS_HSCROLL: 只给 Vertical 时
// 横向滚动条被摘掉、末列被挤压填补空白, 窗口缩小时列就看不见了。这里按实际
// 宽度动态切换, 切换后调用 StretchLastColumn 重算末列宽度, 让滚动条立即生效。
func syncTableScrollbar(tv *walk.TableView) {
	if tv == nil {
		return
	}
	cols := tv.Columns()
	total := 0
	for i := 0; i < cols.Len(); i++ {
		total += tv.IntFrom96DPI(cols.At(i).Width())
	}
	avail := tv.ClientBoundsPixels().Width

	// 注意: walk.Horizontal/Vertical 是无类型常量, 必须显式声明为 walk.Orientation
	var want walk.Orientation = walk.Vertical
	if avail > 0 && total > avail {
		want = walk.Horizontal | walk.Vertical
	}
	if tv.ScrollbarOrientation() == want {
		return
	}
	tv.SetScrollbarOrientation(want)
	_ = tv.StretchLastColumn()
}

// mkBtn 创建按钮
func mkBtn(parent walk.Container, text string, width int) *walk.PushButton {
	b, _ := walk.NewPushButton(parent)
	b.SetText(text)
	b.SetMinMaxSize(walk.Size{Width: width, Height: 28}, walk.Size{Width: 0, Height: 0})
	return b
}

func writeStartupErr(err error) {
	f, ferr := os.Create("startup_err.log")
	if ferr == nil {
		fmt.Fprintf(f, "%v\n", err)
		f.Close()
	}
}

// ---- 输出区 ----

// logf 追加一行带时间戳的日志并滚动到底部。
// 每个操作的返回信息单独占一行: 消息内嵌的换行(\r\n/\r/\n)一律折叠为空格;
// 行终止符用 \r\n (原生 Edit 控件的标准断行符, 裸 \n 可能不产生可见换行)。
func (ui *mainUI) logf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	msg = strings.ReplaceAll(msg, "\r\n", " ")
	msg = strings.ReplaceAll(msg, "\r", " ")
	msg = strings.ReplaceAll(msg, "\n", " ")
	line := time.Now().Format("15:04:05") + " " + msg
	ui.log.AppendText(line + "\r\n")
	ui.log.SendMessage(win.EM_SETSEL, ^uintptr(0), ^uintptr(0))
	ui.log.SendMessage(win.EM_SCROLLCARET, 0, 0)
}

// clearLog 清空日志区
func (ui *mainUI) clearLog() {
	ui.log.SetText("")
}

// ---- 主表刷新 ----

// refresh 异步刷新任务列表 (refreshing 标志加锁保护, 可被自动刷新与手动刷新并发调用)
func (ui *mainUI) refresh() {
	ui.refreshMu.Lock()
	if ui.refreshing {
		ui.refreshMu.Unlock()
		return
	}
	ui.refreshing = true
	ui.refreshMu.Unlock()
	go func() {
		rows, err := ListTasks()
		ui.Synchronize(func() {
			ui.refreshMu.Lock()
			ui.refreshing = false
			ui.refreshMu.Unlock()
			if err != nil {
				ui.statusLbl.SetText("刷新失败: " + err.Error())
				return
			}
			ui.model.rows = rows
			ui.model.PublishRowsReset()
			ui.statusLbl.SetText(fmt.Sprintf("共 %d 个任务", len(rows)))
		})
	}()
}

// startAutoRefresh 每 15s 自动刷新
func (ui *mainUI) startAutoRefresh() {
	go func() {
		t := time.NewTicker(15 * time.Second)
		for range t.C {
			ui.refresh()
		}
	}()
}

// selected 当前选中任务
func (ui *mainUI) selected() *TaskInfo {
	idxs := ui.table.SelectedIndexes()
	if len(idxs) == 0 || idxs[0] < 0 || idxs[0] >= len(ui.model.rows) {
		return nil
	}
	return ui.model.rows[idxs[0]]
}

// ---- 用户验证 ----

// onVerifyToggled 勾选状态变化: 持久化; 勾选则立即验证
func (ui *mainUI) onVerifyToggled() {
	cfg := loadGUIConfig()
	cfg.VerifyOnStart = ui.chkVerify.Checked()
	saveGUIConfig(cfg)
	if ui.chkVerify.Checked() {
		ui.verifyLogin()
	} else {
		ui.loginLbl.SetText("网盘未登录")
	}
}

// verifyLogin 调 BaiduPCS who + quota 并输出结果
func (ui *mainUI) verifyLogin() {
	go func() {
		core := ResolveCoreExe()
		if core == "" {
			ui.Synchronize(func() {
				ui.logf("[用户验证] 未找到 BaiduPCS 核心程序")
				ui.loginLbl.SetText("核心程序未找到")
			})
			return
		}
		line, err := VerifyLogin(core)
		ui.Synchronize(func() {
			if err != nil {
				ui.logf("[用户验证] 失败: %v", err)
				ui.loginLbl.SetText("网盘未登录")
			} else {
				ui.logf("[用户验证] %s", line)
				ui.loginLbl.SetText("网盘已登录")
			}
		})
	}()
}

// ---- 任务操作 (手动执行/启动任务/停止任务) ----

func (ui *mainUI) taskOp(opName string, fn func(name string) error) {
	t := ui.selected()
	if t == nil {
		ui.logf("[%s] 请先选择一个任务", opName)
		return
	}
	go func() {
		err := fn(t.Name)
		ui.Synchronize(func() {
			if err != nil {
				ui.logf("[%s] %s 失败: %v", opName, t.Name, err)
			} else {
				ui.logf("[%s] %s 成功", opName, t.Name)
			}
			ui.refresh()
		})
	}()
}

// runBackupNow 手动执行: 用选中任务的源/目标/类型/模式, 直接调用备份核心做一次备份 (绕过计划任务)
func (ui *mainUI) runBackupNow() {
	t := ui.selected()
	if t == nil {
		ui.logf("[手动执行] 请先选择一个任务")
		return
	}
	if t.Execute == "" || t.ConfigPath == "" {
		ui.logf("[手动执行] %s 缺少执行程序或配置文件，无法执行", t.Name)
		return
	}
	ui.runMu.Lock()
	if ui.running[t.Name] {
		ui.runMu.Unlock()
		ui.logf("[手动执行] %s 正在备份中，请稍候", t.Name)
		return
	}
	ui.running[t.Name] = true
	ui.runMu.Unlock()

	ui.logf("[手动执行] %s 开始备份: %s -> %s（%s，%s）", t.Name, t.LocalDir, t.RemoteDir, t.BackupType, t.Mode)
	go func() {
		defer func() {
			ui.runMu.Lock()
			delete(ui.running, t.Name)
			ui.runMu.Unlock()
		}()
		cmd := exec.Command(t.Execute, "backup", "run", t.ConfigPath)
		cmd.Dir = filepath.Dir(t.Execute)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
		out, err := cmd.CombinedOutput()
		ui.Synchronize(func() {
			if err != nil {
				ui.logf("[手动执行] %s 失败: %v\n%s", t.Name, err, tailText(out, 1500))
			} else {
				ui.logf("[手动执行] %s 完成\n%s", t.Name, tailText(out, 1500))
			}
			ui.refresh()
		})
	}()
}

// tailText 截取输出尾部, 避免大量进度行刷屏
func tailText(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) <= n {
		return s
	}
	return "...(省略 " + fmt.Sprint(len(s)-n) + " 字符)\n" + s[len(s)-n:]
}

// ---- 添加 / 删除 / 上移 / 下移 ----

func (ui *mainUI) addTask() {
	res, ok := showTaskDialog(ui, nil, true)
	if !ok {
		return
	}
	rows := ui.model.rows
	seq := nextSeq(rows)
	name := fmt.Sprintf("%d-backup", seq)
	core := ResolveCoreExe()
	if core == "" {
		ui.logf("[添加任务] 未找到 BaiduPCS 核心程序")
		return
	}
	cfgPath := NewTaskConfigPath(name)
	cfg := &taskConfig{
		LocalDir:      res.LocalDir,
		RemoteDir:     res.RemoteDir,
		Exclude:       []string{},
		Mode:          res.Mode,
		BackupType:    res.BackupType,
		Concurrency:   res.Concurrency,
		BandwidthKBps: res.Bandwidth,
		LogDir:        defaultLogDir(),
		MaxRetry:      3,
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		ui.logf("[添加任务] 配置序列化失败: %v", err)
		return
	}
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		ui.logf("[添加任务] 写配置失败: %v", err)
		return
	}
	ti := &TaskInfo{
		Name: name, LocalDir: res.LocalDir, RemoteDir: res.RemoteDir,
		FreqMinutes: res.FreqMin, FreqText: freqText(res.FreqMin),
		BackupType: typeText(res.BackupType), Mode: modeText(res.Mode),
		CreatedAt: timeNow(), Execute: core, ConfigPath: cfgPath,
	}
	if err := ReRegisterTask(name, core, cfgPath, res.FreqMin, ti.BuildDescription()); err != nil {
		ui.logf("[添加任务] 注册失败: %v", err)
		return
	}
	ui.logf("[添加任务] 已创建 %s: %s -> %s", name, res.LocalDir, res.RemoteDir)
	ui.refresh()
}

func (ui *mainUI) delTask() {
	t := ui.selected()
	if t == nil {
		ui.logf("[删除选中] 请先选择一个任务")
		return
	}
	r := walk.MsgBox(ui, "确认删除", fmt.Sprintf("确定删除任务 %s 吗？\n其配置文件也将被删除。", t.Name),
		walk.MsgBoxIconQuestion|walk.MsgBoxYesNo)
	if r != walk.DlgCmdYes {
		return
	}
	go func() {
		if err := DeleteTask(t.Name); err != nil {
			ui.Synchronize(func() { ui.logf("[删除选中] %s 失败: %v", t.Name, err) })
			return
		}
		if t.ConfigPath != "" {
			os.Remove(t.ConfigPath)
		}
		ui.Synchronize(func() {
			ui.logf("[删除选中] 已删除 %s", t.Name)
			ui.refresh()
		})
	}()
}

// ---- 导出 / 查看 / 打开计划任务 ----

func (ui *mainUI) exportTask() {
	t := ui.selected()
	if t == nil {
		ui.logf("[导出计划任务] 请先选择一个任务")
		return
	}
	go func() {
		xml, err := ExportTaskXML(t.Name)
		if err != nil {
			ui.Synchronize(func() { ui.logf("[导出计划任务] 失败: %v", err) })
			return
		}
		exe, _ := os.Executable()
		dir := filepath.Join(filepath.Dir(exe), "export")
		os.MkdirAll(dir, 0755)
		fp := filepath.Join(dir, t.Name+".xml")
		if err := os.WriteFile(fp, []byte(xml), 0644); err != nil {
			ui.Synchronize(func() { ui.logf("[导出计划任务] 写文件失败: %v", err) })
			return
		}
		exec.Command("explorer.exe", dir).Start()
		ui.Synchronize(func() { ui.logf("[导出计划任务] 已导出到 %s", fp) })
	}()
}

// openScheduler 打开任务计划程序 (简化: 跨电脑树结构不可靠, 不再自动定位文件夹)
func (ui *mainUI) openScheduler() {
	go func() {
		if err := exec.Command("mmc.exe", "taskschd.msc").Start(); err != nil {
			ui.Synchronize(func() { ui.logf("[打开计划任务] 打开任务计划程序失败: %v", err) })
			return
		}
		ui.Synchronize(func() {
			ui.logf("[打开计划任务] 请逐级查看当前任务：任务计划程序 (本地)>任务计划程序库>Microsoft>BaiduPCS-Go")
		})
	}()
}

// ---- 双击修改 ----

func (ui *mainUI) editSelected() {
	t := ui.selected()
	if t == nil {
		return
	}
	res, ok := showTaskDialog(ui, t, false)
	if !ok {
		return
	}
	// 任务名可能被修改 (对话框内已做重名校验, 未重名才走到这里)
	oldName := t.Name
	newName := oldName
	if res.Name != "" && !strings.EqualFold(res.Name, oldName) {
		newName = res.Name
	}
	// 应用修改
	t.LocalDir = res.LocalDir
	t.RemoteDir = res.RemoteDir
	t.FreqMinutes = res.FreqMin
	t.FreqText = freqText(res.FreqMin)
	t.BackupType = typeText(res.BackupType)
	t.Mode = modeText(res.Mode)
	if t.config == nil {
		t.config = &taskConfig{}
	}
	t.config.LocalDir = res.LocalDir
	t.config.RemoteDir = res.RemoteDir
	t.config.BackupType = res.BackupType
	t.config.Mode = res.Mode
	t.config.Concurrency = res.Concurrency
	t.config.BandwidthKBps = res.Bandwidth

	if err := t.SaveConfig(); err != nil {
		ui.logf("[修改任务] 配置保存失败: %v", err)
		return
	}
	newCfgPath := t.ConfigPath
	if newName != oldName {
		// 配置随任务名迁移 (配置文件按任务名命名, 避免同源共用/误删)
		newCfgPath = NewTaskConfigPath(newName)
		if !strings.EqualFold(newCfgPath, t.ConfigPath) {
			data, rerr := os.ReadFile(t.ConfigPath)
			if rerr != nil {
				ui.logf("[修改任务] 读取原配置失败: %v", rerr)
				ui.refresh()
				return
			}
			if werr := os.WriteFile(newCfgPath, data, 0644); werr != nil {
				ui.logf("[修改任务] 迁移配置文件到 %s 失败: %v", newCfgPath, werr)
				ui.refresh()
				return
			}
		}
	}
	// 用新名(或原名)重新注册: ReRegisterTask 内部会先删同名再重建
	if err := ReRegisterTask(newName, t.Execute, newCfgPath, res.FreqMin, t.BuildDescription()); err != nil {
		ui.logf("[修改任务] 重新注册失败: %v（配置已更新，可手动检查任务）", err)
		ui.refresh()
		return
	}
	if newName != oldName {
		// 改名: 删除原任务名的任务
		if err := DeleteTask(oldName); err != nil {
			ui.logf("[修改任务] %s 已注册，但删除原任务 %s 失败: %v", newName, oldName, err)
			ui.refresh()
			return
		}
		// 删除旧配置文件 (仅当迁移到不同路径时)
		if !strings.EqualFold(newCfgPath, t.ConfigPath) {
			os.Remove(t.ConfigPath)
		}
		t.ConfigPath = newCfgPath
		t.Name = newName
		ui.logf("[修改任务] %s 已重命名为 %s 并重新注册", oldName, newName)
	} else {
		ui.logf("[修改任务] %s 已修改并重新注册", t.Name)
	}
	ui.refresh()
}
