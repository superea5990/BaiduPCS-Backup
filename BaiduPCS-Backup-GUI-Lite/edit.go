package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lxn/walk"
)

// editResult 二级修改页的保存结果
type editResult struct {
	Name        string // 任务名 (仅修改模式有效; 新建模式为空=自动编号)
	LocalDir    string
	RemoteDir   string
	FreqMin     int
	BackupType  string // incremental | full
	Mode        string // normal | stealth
	Concurrency int
	Bandwidth   int // KB/s, 0=不限
}

type editDialog struct {
	*walk.Dialog
	nameEdit *walk.LineEdit
	curName  string // 修改模式的原任务名, 用于判断是否改名
	srcEdit  *walk.LineEdit
	dstEdit  *walk.LineEdit
	freqCb   *walk.ComboBox
	freqCust *walk.LineEdit
	typeCb   *walk.ComboBox
	modeCb   *walk.ComboBox
	concEdit *walk.LineEdit
	bandEdit *walk.LineEdit
	accepted bool
	result   *editResult
}

// 频率预设 (分钟)
var freqPresets = []struct {
	text  string
	value int
}{
	{"每30分钟", 30},
	{"每小时", 60},
	{"每2小时", 120},
	{"每6小时", 360},
	{"每天", 1440},
	{"每周", 10080},
	{"自定义(分钟)", -1},
}

func showTaskDialog(owner walk.Form, t *TaskInfo, isNew bool) (*editResult, bool) {
	dlg, err := walk.NewDialog(owner)
	if err != nil {
		return nil, false
	}
	ed := &editDialog{Dialog: dlg}
	ed.setup(t, isNew)
	if isNew {
		dlg.SetTitle("添加任务")
	} else {
		dlg.SetTitle("修改任务 - " + t.Name)
	}
	dlg.SetSize(walk.Size{Width: 520, Height: 440})
	centerOnMain(dlg, owner) // 以主界面中心弹出 (owner 即主界面)
	dlg.Run()
	return ed.result, ed.accepted
}

func (ed *editDialog) setup(t *TaskInfo, isNew bool) {
	if t == nil { // 新建模式: 使用默认值
		t = &TaskInfo{}
	}
	root := walk.NewVBoxLayout()
	root.SetSpacing(8)
	ed.SetLayout(root)

	form, _ := walk.NewComposite(ed)
	fv := walk.NewVBoxLayout()
	fv.SetSpacing(8)
	form.SetLayout(fv)

	// 任务名 (仅修改模式显示; 新建模式由程序自动编号 n-backup)
	if !isNew {
		ed.curName = t.Name
		_, nameRow := newRow(form, "任务名：", 80)
		ed.nameEdit, _ = walk.NewLineEdit(nameRow)
		ed.nameEdit.SetText(t.Name)
	}

	// 源 (只读, 只能通过 … 按钮选择)
	_, srcRow := newRow(form, "源：", 80)
	ed.srcEdit, _ = walk.NewLineEdit(srcRow)
	ed.srcEdit.SetText(t.LocalDir)
	ed.srcEdit.SetReadOnly(true)
	browse, _ := walk.NewPushButton(srcRow)
	browse.SetText("…")
	browse.SetMinMaxSize(walk.Size{Width: 30, Height: 26}, walk.Size{Width: 30, Height: 26})
	browse.Clicked().Attach(func() {
		fd := new(walk.FileDialog)
		fd.Title = "选择本地备份文件夹"
		if ok, err := fd.ShowBrowseFolder(ed); err == nil && ok {
			ed.srcEdit.SetText(fd.FilePath)
		}
	})

	// 目标
	_, dstRow := newRow(form, "目标：", 80)
	ed.dstEdit, _ = walk.NewLineEdit(dstRow)
	ed.dstEdit.SetText(t.RemoteDir)

	// 频率
	_, freqRow := newRow(form, "频率：", 80)
	ed.freqCb, _ = walk.NewComboBox(freqRow)
	var items []string
	sel := 0
	matched := false
	for i, p := range freqPresets {
		items = append(items, p.text)
		if p.value == t.FreqMinutes {
			sel = i
			matched = true
		}
	}
	if !matched && t.FreqMinutes > 0 {
		sel = len(freqPresets) - 1 // 自定义
	}
	ed.freqCb.SetModel(items)
	ed.freqCb.SetCurrentIndex(sel)
	ed.freqCust, _ = walk.NewLineEdit(freqRow)
	ed.freqCust.SetText(strconv.Itoa(t.FreqMinutes))
	ed.freqCust.SetVisible(sel == len(freqPresets)-1)
	ed.freqCb.CurrentIndexChanged().Attach(func() {
		ed.freqCust.SetVisible(ed.freqCb.CurrentIndex() == len(freqPresets)-1)
	})

	// 类型
	_, typeRow := newRow(form, "类型：", 80)
	ed.typeCb, _ = walk.NewComboBox(typeRow)
	ed.typeCb.SetModel([]string{"增量", "全量"})
	if t.BackupType == "全量" {
		ed.typeCb.SetCurrentIndex(1)
	} else {
		ed.typeCb.SetCurrentIndex(0)
	}

	// 模式
	_, modeRow := newRow(form, "模式：", 80)
	ed.modeCb, _ = walk.NewComboBox(modeRow)
	ed.modeCb.SetModel([]string{"普通", "无感"})
	if t.Mode == "无感" {
		ed.modeCb.SetCurrentIndex(1)
	} else {
		ed.modeCb.SetCurrentIndex(0)
	}

	// 普通模式的并发/带宽
	conc := 1
	band := 0
	if t.config != nil {
		conc = t.config.Concurrency
		band = t.config.BandwidthKBps
	}
	_, concRow := newRow(form, "并发：", 80)
	ed.concEdit, _ = walk.NewLineEdit(concRow)
	ed.concEdit.SetText(strconv.Itoa(conc))
	_, bandRow := newRow(form, "带宽KB/s：", 80)
	ed.bandEdit, _ = walk.NewLineEdit(bandRow)
	ed.bandEdit.SetText(strconv.Itoa(band))

	// 新建任务: 继承全局默认模板 (mode_config.json 的当前性能模式及其参数)
	if isNew {
		p := currentModeParams()
		switch loadModeConfig().ActiveMode {
		case "stealth":
			ed.modeCb.SetCurrentIndex(1)
		default:
			ed.modeCb.SetCurrentIndex(0)
		}
		ed.concEdit.SetText(strconv.Itoa(p.Concurrency))
		ed.bandEdit.SetText(strconv.Itoa(p.BandwidthKBps))
	}

	ed.modeCb.CurrentIndexChanged().Attach(ed.updateModeFields)
	ed.updateModeFields()

	// 按钮区
	btnRow, _ := walk.NewComposite(ed)
	bb := walk.NewHBoxLayout()
	bb.SetAlignment(walk.AlignHFarVNear)
	btnRow.SetLayout(bb)
	okBtn, _ := walk.NewPushButton(btnRow)
	okBtn.SetText("确定")
	cancelBtn, _ := walk.NewPushButton(btnRow)
	cancelBtn.SetText("取消")
	okBtn.Clicked().Attach(ed.onOK)
	cancelBtn.Clicked().Attach(func() { ed.Cancel() })
	ed.SetDefaultButton(okBtn)
	ed.SetCancelButton(cancelBtn)
}

func (ed *editDialog) updateModeFields() {
	switch ed.modeCb.CurrentIndex() {
	case 0: // 普通
		ed.concEdit.SetVisible(true)
		ed.bandEdit.SetVisible(true)
		ed.concEdit.SetEnabled(true)
		ed.bandEdit.SetEnabled(true)
	case 1: // 无感
		ed.concEdit.SetVisible(false)
		ed.bandEdit.SetVisible(false)
	}
}

func (ed *editDialog) onOK() {
	// 任务名 (仅修改模式): 非空校验 + 重名校验
	newName := ""
	if ed.nameEdit != nil {
		newName = strings.TrimSpace(ed.nameEdit.Text())
		if newName == "" {
			walk.MsgBox(ed, "提示", "任务名不能为空", walk.MsgBoxIconInformation)
			return
		}
		if !strings.EqualFold(newName, ed.curName) && TaskExists(newName) {
			walk.MsgBox(ed, "提示", fmt.Sprintf("任务名 \"%s\" 已存在，请更换", newName), walk.MsgBoxIconInformation)
			return // 停止一切操作, 保留对话框供修改
		}
	}

	src := strings.TrimSpace(ed.srcEdit.Text())
	dst := strings.TrimSpace(ed.dstEdit.Text())
	if src == "" || dst == "" {
		walk.MsgBox(ed, "提示", "源文件夹和目标网盘路径不能为空", walk.MsgBoxIconInformation)
		return
	}
	if !strings.HasPrefix(dst, "/") {
		walk.MsgBox(ed, "提示", "目标网盘路径必须以 / 开头", walk.MsgBoxIconInformation)
		return
	}

	freqMin := 0
	idx := ed.freqCb.CurrentIndex()
	if idx >= 0 && idx < len(freqPresets) {
		if freqPresets[idx].value > 0 {
			freqMin = freqPresets[idx].value
		} else {
			fm, err := strconv.Atoi(strings.TrimSpace(ed.freqCust.Text()))
			if err != nil || fm <= 0 {
				walk.MsgBox(ed, "提示", "请输入有效的自定义频率(分钟)", walk.MsgBoxIconInformation)
				return
			}
			freqMin = fm
		}
	}
	if freqMin <= 0 {
		walk.MsgBox(ed, "提示", "频率无效", walk.MsgBoxIconInformation)
		return
	}

	res := &editResult{
		Name:       newName,
		LocalDir:   src,
		RemoteDir:  dst,
		FreqMin:    freqMin,
		BackupType: "incremental",
		Mode:       "normal",
	}
	if ed.typeCb.CurrentIndex() == 1 {
		res.BackupType = "full"
	}
	switch ed.modeCb.CurrentIndex() {
	case 1:
		res.Mode = "stealth"
		st := loadModeConfig().Stealth
		res.Concurrency = st.Concurrency
		res.Bandwidth = st.BandwidthKBps
	default:
		res.Mode = "normal"
		conc, err1 := strconv.Atoi(strings.TrimSpace(ed.concEdit.Text()))
		if err1 != nil || conc <= 0 {
			walk.MsgBox(ed, "提示", "并发必须为正整数", walk.MsgBoxIconInformation)
			return
		}
		band, err2 := strconv.Atoi(strings.TrimSpace(ed.bandEdit.Text()))
		if err2 != nil || band < 0 {
			walk.MsgBox(ed, "提示", "带宽必须为非负整数(KB/s, 0=不限)", walk.MsgBoxIconInformation)
			return
		}
		res.Concurrency = conc
		res.Bandwidth = band
	}

	ed.result = res
	ed.accepted = true
	ed.Accept()
}

// newRow 创建"标签+控件"行, 返回标签与容器
func newRow(parent walk.Container, label string, w int) (*walk.Label, *walk.Composite) {
	c, _ := walk.NewComposite(parent)
	hb := walk.NewHBoxLayout()
	hb.SetSpacing(6)
	c.SetLayout(hb)
	lbl, _ := walk.NewLabel(c)
	lbl.SetText(label)
	lbl.SetMinMaxSize(walk.Size{Width: w}, walk.Size{Width: 0, Height: 0})
	return lbl, c
}
