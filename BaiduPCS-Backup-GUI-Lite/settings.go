package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

// ==================== 账号管理 ====================

// accountItem 账号列表一项, index 为 su 命令使用的 0 基序号 (与核心 BaiduUserList 下标一致)
type accountItem struct {
	index   int
	uid     string
	name    string
	current bool
}

// pcsAccount 核心配置文件中的单个百度账号
type pcsAccount struct {
	UID     uint64  `json:"uid"`
	Name    string  `json:"name"`
	Sex     string  `json:"sex"`
	Age     float64 `json:"age"`
	Workdir string  `json:"workdir"`
}

// pcsConfigJSON 核心配置文件中账号相关的字段
type pcsConfigJSON struct {
	BaiduUserList  []pcsAccount `json:"baidu_user_list"`
	BaiduActiveUID uint64       `json:"baidu_active_uid"`
}

// accountConfigPath 定位核心配置文件路径, 与核心 GetConfigDir 解析逻辑保持一致:
// BAIDUPCS_GO_CONFIG_DIR 环境变量 → 核心exe同级(旧版) → %APPDATA%\BaiduPCS-Go
func accountConfigPath() string {
	if d := os.Getenv("BAIDUPCS_GO_CONFIG_DIR"); d != "" {
		if !filepath.IsAbs(d) {
			exe, _ := os.Executable()
			d = filepath.Join(filepath.Dir(exe), d)
		}
		return filepath.Join(d, "pcs_config.json")
	}
	exe, _ := os.Executable()
	exeDir := filepath.Dir(exe)
	if _, err := os.Stat(filepath.Join(exeDir, "pcs_config.json")); err == nil {
		return filepath.Join(exeDir, "pcs_config.json")
	}
	if ap := os.Getenv("APPDATA"); ap != "" {
		return filepath.Join(ap, "BaiduPCS-Go", "pcs_config.json")
	}
	return filepath.Join(exeDir, "pcs_config.json")
}

// listAccounts 读取核心配置文件获取账号列表并标记当前账号
// (直接读配置比解析 loglist 空格对齐表格更可靠; 列表与核心 BaiduUserList 顺序一致)
func listAccounts() ([]accountItem, string, error) {
	data, err := os.ReadFile(accountConfigPath())
	if err != nil {
		return nil, "", fmt.Errorf("读取核心配置失败: %v", err)
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var pc pcsConfigJSON
	if err := json.Unmarshal(data, &pc); err != nil {
		return nil, "", fmt.Errorf("解析核心配置失败: %v", err)
	}
	var items []accountItem
	curName := ""
	for i, u := range pc.BaiduUserList {
		item := accountItem{index: i, uid: fmt.Sprint(u.UID), name: u.Name, current: u.UID == pc.BaiduActiveUID}
		if item.current {
			curName = u.Name
		}
		items = append(items, item)
	}
	return items, curName, nil
}

// ==================== 设置对话框 ====================

type settingsDialog struct {
	*walk.Dialog
	ui *mainUI
	mc modeConfig

	// 账号管理
	acctList *walk.ListBox
	acctCur  *walk.Label
	acctItems []accountItem

	// 基础设置
	normalConc, normalBand   *walk.LineEdit
	stealthConc, stealthBand *walk.LineEdit

	// 帮助
	helpCoreVer *walk.Label // 核心程序版本号, 打开设置时异步查询后填充
}

// 帮助页使用的外部地址
const (
	releaseUrl = "https://github.com/superea5990/BaiduPCS-Backup/releases"
	cookUrl    = "https://jingyan.baidu.com/article/5553fa829a6a9e65a23934b0.html"
)

// showSettingsDialog 打开设置对话框 (账号管理/基础设置/帮助)
func showSettingsDialog(ui *mainUI) {
	dlg, err := walk.NewDialog(ui)
	if err != nil {
		return
	}
	sd := &settingsDialog{Dialog: dlg, ui: ui, mc: loadModeConfig()}
	sd.setup()
	dlg.SetTitle("设置")
	dlg.SetSize(walk.Size{Width: 760, Height: 500})
	centerOnMain(dlg, ui) // 以主界面中心弹出
	dlg.Run()
}

func (sd *settingsDialog) setup() {
	root := walk.NewVBoxLayout()
	root.SetSpacing(8)
	sd.SetLayout(root)

	tabs, _ := walk.NewTabWidget(sd)
	root.SetStretchFactor(tabs, 1)

	// ---- 账号管理 ----
	pageAcct, _ := walk.NewTabPage()
	pageAcct.SetTitle("账号管理")
	sd.setupAccountPage(pageAcct)
	tabs.Pages().Add(pageAcct)

	// ---- 基础设置 ----
	pageBasic, _ := walk.NewTabPage()
	pageBasic.SetTitle("基础设置")
	sd.setupBasicPage(pageBasic)
	tabs.Pages().Add(pageBasic)

	// ---- 帮助 ----
	pageHelp, _ := walk.NewTabPage()
	pageHelp.SetTitle("帮助")
	sd.setupHelpPage(pageHelp)
	tabs.Pages().Add(pageHelp)

	// 按钮区
	btnRow, _ := walk.NewComposite(sd)
	bb := walk.NewHBoxLayout()
	bb.SetAlignment(walk.AlignHFarVNear)
	btnRow.SetLayout(bb)
	okBtn, _ := walk.NewPushButton(btnRow)
	okBtn.SetText("确定")
	cancelBtn, _ := walk.NewPushButton(btnRow)
	cancelBtn.SetText("取消")
	okBtn.Clicked().Attach(sd.onOK)
	cancelBtn.Clicked().Attach(func() { sd.Cancel() })
	sd.SetDefaultButton(okBtn)
	sd.SetCancelButton(cancelBtn)

	// 打开时刷新账号列表
	sd.acctRefresh()
}

// setupHelpPage 帮助页: 说明文字 + 核心版本 + 新版发布地址 + Cookies 教程
// 采用两列网格(标签列 + 内容列), 各行左侧标签同宽, 内容左对齐整齐排列
func (sd *settingsDialog) setupHelpPage(page *walk.TabPage) {
	grid := walk.NewGridLayout()
	grid.SetSpacing(8)
	page.SetLayout(grid)
	grid.SetColumnStretchFactor(0, 0) // 标签列: 固定宽度, 不参与拉伸
	grid.SetColumnStretchFactor(1, 1) // 内容列: 占满剩余宽度

	// 说明: 跨两列
	tip, _ := walk.NewLabel(page)
	tip.SetText("账号登录支持粘贴百度 Cookies 或 BDUSS。\n若不清楚如何获取，请参考下方教程。")
	grid.SetRange(tip, walk.Rectangle{X: 0, Y: 0, Width: 2, Height: 1})

	// 核心版本: 异步查询核心程序, 未找到时给出提示
	_, verCell := helpGridRow(page, grid, "核心版本：", 1)
	sd.helpCoreVer, _ = walk.NewLabel(verCell)
	sd.helpCoreVer.SetText("检测中…")
	go func() {
		core := ResolveCoreExe()
		ver := ""
		if core != "" {
			ver = CoreVersion(core)
		}
		sd.ui.Synchronize(func() {
			if ver == "" {
				sd.helpCoreVer.SetText("未检测到核心程序")
			} else {
				sd.helpCoreVer.SetText("BaiduPCS-Go " + ver)
			}
		})
	}()

	// 新版发布地址
	_, relCell := helpGridRow(page, grid, "新版发布：", 2)
	newHelpLink(relCell, releaseUrl)

	// Cookies 教程
	_, cookCell := helpGridRow(page, grid, "Cookies 教程：", 3)
	newHelpLink(cookCell, cookUrl)

	// 底部占位: 吸收多余高度, 避免上方各行被垂直拉伸
	sp, _ := walk.NewLabel(page)
	grid.SetRange(sp, walk.Rectangle{X: 0, Y: 4, Width: 2, Height: 1})
	grid.SetRowStretchFactor(4, 1)
}

// helpGridRow 在 grid 指定行创建"标签列 + 内容列"两列, 返回内容单元格容器
// 标签列固定宽度, 保证多行之间的内容列左边缘对齐
func helpGridRow(parent walk.Container, grid *walk.GridLayout, label string, row int) (*walk.Label, *walk.Composite) {
	lbl, _ := walk.NewLabel(parent)
	lbl.SetText(label)
	lbl.SetMinMaxSize(walk.Size{Width: 96}, walk.Size{Width: 0, Height: 0})
	grid.SetRange(lbl, walk.Rectangle{X: 0, Y: row, Width: 1, Height: 1})

	cell, _ := walk.NewComposite(parent)
	hb := walk.NewHBoxLayout()
	hb.SetSpacing(4)
	cell.SetLayout(hb)
	grid.SetRange(cell, walk.Rectangle{X: 1, Y: row, Width: 1, Height: 1})
	return lbl, cell
}

// newHelpLink 创建一个以完整地址作为显示文字的超链接 (点击用默认浏览器打开)
func newHelpLink(parent walk.Container, url string) *walk.LinkLabel {
	ll, _ := walk.NewLinkLabel(parent)
	ll.SetText(`<a href="` + url + `">` + url + `</a>`)
	ll.LinkActivated().Attach(func(link *walk.LinkLabelLink) {
		u := link.URL()
		if u == "" {
			u = url
		}
		exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Run()
	})
	return ll
}

// ---- 账号管理页 ----

func (sd *settingsDialog) setupAccountPage(page *walk.TabPage) {
	pv := walk.NewVBoxLayout()
	pv.SetSpacing(6)
	page.SetLayout(pv)

	sd.acctCur, _ = walk.NewLabel(page)
	sd.acctCur.SetText("当前账号: -")

	sd.acctList, _ = walk.NewListBox(page)
	pv.SetStretchFactor(sd.acctList, 1)

	btnRow, _ := walk.NewComposite(page)
	hb := walk.NewHBoxLayout()
	hb.SetSpacing(6)
	btnRow.SetLayout(hb)
	mkBtn(btnRow, "登录", 80).Clicked().Attach(sd.acctLogin)
	mkBtn(btnRow, "切换", 80).Clicked().Attach(sd.acctSwitch)
	mkBtn(btnRow, "退出", 80).Clicked().Attach(sd.acctLogout)
	_, _ = walk.NewHSpacer(btnRow)
	mkBtn(btnRow, "刷新", 80).Clicked().Attach(sd.acctRefresh)
}

// acctRefresh 读取账号列表并刷新列表控件
func (sd *settingsDialog) acctRefresh() {
	go func() {
		items, cur, err := listAccounts()
		sd.ui.Synchronize(func() {
			if err != nil {
				sd.ui.logf("[账号管理] 获取账号列表失败: %v", err)
				return
			}
			sd.acctItems = items
			var strs []string
			for _, it := range items {
				mark := "  "
				if it.current {
					mark = "★"
				}
				strs = append(strs, fmt.Sprintf("%s #%d  %s (uid:%s)", mark, it.index, it.name, it.uid))
			}
			sd.acctList.SetModel(strs)
			sd.acctCur.SetText(fmt.Sprintf("当前账号: %s", cur))
		})
	}()
}

// acctLogin 打开登录对话框: 方式为方案B (粘贴 BDUSS / Cookies)
func (sd *settingsDialog) acctLogin() {
	dialog := newLoginDialog(sd, sd.ui)
	if dialog == nil {
		return
	}
	kind, cookies, bduss, stoken := dialog.Result()
	if kind == "" {
		return // 用户取消
	}
	go func() {
		core := ResolveCoreExe()
		if core == "" {
			sd.ui.Synchronize(func() { sd.ui.logf("[账号管理] 未找到 BaiduPCS 核心程序") })
			return
		}
		var args []string
		args = append(args, "login")
		if kind == "cookies" {
			args = append(args, "-cookies="+cookies)
		} else {
			args = append(args, "-bduss="+bduss)
			if stoken != "" {
				args = append(args, "-stoken="+stoken)
			}
		}
		out, err := runCore(core, args...)
		sd.ui.Synchronize(func() {
			msg := strings.TrimSpace(string(out))
			if err != nil {
				sd.ui.logf("[账号管理] 登录失败: %v %s", err, msg)
			} else {
				sd.ui.logf("[账号管理] 登录结果: %s", msg)
			}
			sd.acctRefresh()
		})
	}()
}

func (sd *settingsDialog) acctSwitch() {
	it, ok := sd.selectedAccount()
	if !ok {
		return
	}
	go func() {
		core := ResolveCoreExe()
		if core == "" {
			sd.ui.Synchronize(func() { sd.ui.logf("[账号管理] 未找到 BaiduPCS 核心程序") })
			return
		}
		out, err := runCore(core, "su", it.uid) // su <uid> 直接按 UID 切换
		sd.ui.Synchronize(func() {
			if err != nil {
				sd.ui.logf("[账号管理] 切换账号失败: %v", err)
			} else {
				sd.ui.logf("[账号管理] 切换账号: %s", strings.TrimSpace(string(out)))
			}
			sd.acctRefresh()
		})
	}()
}

func (sd *settingsDialog) acctLogout() {
	_, ok := sd.selectedAccount()
	if !ok {
		return
	}
	r := walk.MsgBox(sd, "确认退出", "确定退出当前选中的百度账号吗？", walk.MsgBoxIconQuestion|walk.MsgBoxYesNo)
	if r != walk.DlgCmdYes {
		return
	}
	go func() {
		core := ResolveCoreExe()
		if core == "" {
			sd.ui.Synchronize(func() { sd.ui.logf("[账号管理] 未找到 BaiduPCS 核心程序") })
			return
		}
		out, err := runCore(core, "logout", "-y") // -y: 默认输入Y确认退出 (GUI 已弹确认框, 无需核心再询问)
		sd.ui.Synchronize(func() {
			if err != nil {
				sd.ui.logf("[账号管理] 退出账号失败: %v", err)
			} else {
				sd.ui.logf("[账号管理] 退出账号: %s", strings.TrimSpace(string(out)))
			}
			sd.acctRefresh()
		})
	}()
}

func (sd *settingsDialog) selectedAccount() (accountItem, bool) {
	idx := sd.acctList.CurrentIndex()
	if idx < 0 || idx >= len(sd.acctItems) {
		walk.MsgBox(sd, "提示", "请先选择一个账号", walk.MsgBoxIconInformation)
		return accountItem{}, false
	}
	return sd.acctItems[idx], true
}

// ---- 基础设置页 ----

func (sd *settingsDialog) setupBasicPage(page *walk.TabPage) {
	grid := walk.NewGridLayout()
	grid.SetSpacing(6)
	page.SetLayout(grid)
	// 两列等宽, 保证列对齐
	for c := 0; c < 2; c++ {
		grid.SetColumnStretchFactor(c, 1)
	}

	// 提示: 跨两列
	tip, _ := walk.NewLabel(page)
	tip.SetText("全局默认模板：新建任务时继承以下参数，已有任务不受影响。")
	grid.SetRange(tip, walk.Rectangle{X: 0, Y: 0, Width: 2, Height: 1})

	// 标题行 (Y=1): 普通 / 无感 两列左对齐
	for i, t := range []string{"普通模式", "无感模式"} {
		lbl, _ := walk.NewLabel(page)
		lbl.SetText(t)
		grid.SetRange(lbl, walk.Rectangle{X: i, Y: 1, Width: 1, Height: 1})
	}

	// 并发行 (Y=2)
	conc := mkGridFieldCell(page, grid, "并发：", 2, []int{
		sd.mc.Normal.Concurrency, sd.mc.Stealth.Concurrency,
	})
	sd.normalConc, sd.stealthConc = conc[0], conc[1]

	// 带宽行 (Y=3)
	band := mkGridFieldCell(page, grid, "带宽KB/s：", 3, []int{
		sd.mc.Normal.BandwidthKBps, sd.mc.Stealth.BandwidthKBps,
	})
	sd.normalBand, sd.stealthBand = band[0], band[1]
}

// mkGridFieldCell 在 grid 的指定行创建三列"标签+输入框", 返回输入框列表
func mkGridFieldCell(parent walk.Container, grid *walk.GridLayout, label string, row int, vals []int) []*walk.LineEdit {
	var edits []*walk.LineEdit
	for i, v := range vals {
		cell, _ := walk.NewComposite(parent)
		ch := walk.NewHBoxLayout()
		ch.SetSpacing(6)
		cell.SetLayout(ch)
		lbl, _ := walk.NewLabel(cell)
		lbl.SetText(label)
		lbl.SetMinMaxSize(walk.Size{Width: 70}, walk.Size{Width: 0, Height: 0})
		e, _ := walk.NewLineEdit(cell)
		e.SetText(strconv.Itoa(v))
		edits = append(edits, e)
		grid.SetRange(cell, walk.Rectangle{X: i, Y: row, Width: 1, Height: 1})
	}
	return edits
}

// onOK 保存基础设置: 普通/无感模式的固定参数直接写入全局配置
func (sd *settingsDialog) onOK() {
	nc, ok1 := parsePosInt(sd.normalConc.Text())
	nb, ok2 := parseNonNeg(sd.normalBand.Text())
	stc, ok3 := parsePosInt(sd.stealthConc.Text())
	stb, ok4 := parseNonNeg(sd.stealthBand.Text())
	if !ok1 || !ok2 || !ok3 || !ok4 {
		walk.MsgBox(sd, "提示", "并发必须为正整数，带宽必须为非负整数（KB/s，0=不限）", walk.MsgBoxIconInformation)
		return
	}

	mc := sd.mc
	mc.Normal.Concurrency = nc
	mc.Normal.BandwidthKBps = nb
	mc.Stealth.Concurrency = stc
	mc.Stealth.BandwidthKBps = stb

	if err := saveModeConfig(mc); err != nil {
		walk.MsgBox(sd, "错误", "保存模式参数失败: "+err.Error(), walk.MsgBoxIconError)
		return
	}
	sd.ui.logf("[设置] 基础设置已保存")
	sd.Accept()
}

// centerOnMain 将对话框定位到主界面窗口正中央 (所有弹出窗口统一以主界面中心为准)
func centerOnMain(dlg *walk.Dialog, main walk.Form) {
	if main == nil || dlg == nil {
		return
	}
	ob := main.BoundsPixels()
	sz := dlg.SizePixels()
	dlg.SetBoundsPixels(walk.Rectangle{
		X:      ob.X + (ob.Width - sz.Width) / 2,
		Y:      ob.Y + (ob.Height - sz.Height) / 2,
		Width:  sz.Width,
		Height: sz.Height,
	})
}

// ---- 工具 ----

func parsePosInt(s string) (int, bool) {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	return v, err == nil && v > 0
}

func parseNonNeg(s string) (int, bool) {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	return v, err == nil && v >= 0
}

// ---- 登录对话框 (方案B: BDUSS / Cookies) ----

type loginDialog struct {
	*walk.Dialog
	methodCb *walk.ComboBox
	cRow, bRow, sRow *walk.Composite // 各输入行容器(隐藏时连标签一起隐藏)
	cookies  *walk.TextEdit          // Cookies 多行大输入框
	bduss    *walk.LineEdit
	stoken   *walk.LineEdit
	kind     string
	c, bd, st string
	accepted bool
}

func newLoginDialog(owner walk.Form, main walk.Form) *loginDialog {
	dlg, err := walk.NewDialog(owner)
	if err != nil {
		return nil
	}
	ld := &loginDialog{Dialog: dlg}
	ld.setup()
	dlg.SetTitle("账号登录")
	dlg.SetSize(walk.Size{Width: 460, Height: 210})
	ld.updateFields() // 按当前模式(默认BDUSS)确定最终窗口高度
	centerOnMain(dlg, main)
	dlg.Run()
	if !ld.accepted {
		return nil
	}
	return ld
}

func (ld *loginDialog) setup() {
	root := walk.NewVBoxLayout()
	root.SetSpacing(8)
	ld.SetLayout(root)

	_, mRow := newRow(ld, "登录方式：", 80)
	ld.methodCb, _ = walk.NewComboBox(mRow)
	ld.methodCb.SetModel([]string{"Cookies", "BDUSS"})
	ld.methodCb.SetCurrentIndex(1) // 默认 BDUSS

	_, cRow := newRow(ld, "Cookies：", 80)
	ld.cRow = cRow
	ld.cookies, _ = walk.NewTextEditWithStyle(cRow, win.WS_VSCROLL|win.WS_HSCROLL)
	ld.cookies.SetMaxLength(100000) // 放宽字符上限, 容纳整段长 cookies
	ld.cookies.SetMinMaxSize(walk.Size{Width: 0}, walk.Size{Width: 0, Height: 200})

	_, bRow := newRow(ld, "BDUSS：", 80)
	ld.bRow = bRow
	ld.bduss, _ = walk.NewLineEdit(bRow)
	_, sRow := newRow(ld, "STOKEN：", 80)
	ld.sRow = sRow
	ld.stoken, _ = walk.NewLineEdit(sRow)

	ld.updateFields()
	ld.methodCb.CurrentIndexChanged().Attach(ld.updateFields)

	btnRow, _ := walk.NewComposite(ld)
	bb := walk.NewHBoxLayout()
	bb.SetAlignment(walk.AlignHFarVNear)
	btnRow.SetLayout(bb)
	okBtn, _ := walk.NewPushButton(btnRow)
	okBtn.SetText("登录")
	cancelBtn, _ := walk.NewPushButton(btnRow)
	cancelBtn.SetText("取消")
	okBtn.Clicked().Attach(ld.onOK)
	cancelBtn.Clicked().Attach(func() { ld.Cancel() })
	ld.SetDefaultButton(okBtn)
	ld.SetCancelButton(cancelBtn)
}

func (ld *loginDialog) updateFields() {
	cookiesMode := ld.methodCb.CurrentIndex() == 0
	ld.cRow.SetVisible(cookiesMode)
	ld.bRow.SetVisible(!cookiesMode)
	ld.sRow.SetVisible(!cookiesMode)
	// 动态窗口高度: Cookies 模式需容纳大输入框
	h := 190
	if cookiesMode {
		h = 390
	}
	ld.SetSize(walk.Size{Width: 460, Height: h})
}

func (ld *loginDialog) onOK() {
	if ld.methodCb.CurrentIndex() == 0 {
		c := strings.TrimSpace(ld.cookies.Text())
		if c == "" {
			walk.MsgBox(ld, "提示", "请粘贴 Cookies", walk.MsgBoxIconInformation)
			return
		}
		ld.kind, ld.c = "cookies", c
	} else {
		b := strings.TrimSpace(ld.bduss.Text())
		if b == "" {
			walk.MsgBox(ld, "提示", "请粘贴 BDUSS", walk.MsgBoxIconInformation)
			return
		}
		ld.kind, ld.bd = "bduss", b
		ld.st = strings.TrimSpace(ld.stoken.Text())
	}
	ld.accepted = true
	ld.Accept()
}

// Result 返回登录参数; kind 为空表示取消
func (ld *loginDialog) Result() (kind, cookies, bduss, stoken string) {
	if ld == nil || !ld.accepted {
		return "", "", "", ""
	}
	return ld.kind, ld.c, ld.bd, ld.st
}
