package main

import (
	"github.com/lxn/walk"
)

// ---- 导入列表弹窗 ----

// importTableModel 导入候选列表数据模型。
// 内嵌 walk.TableModelBase, 并实现 walk.ItemChecker 以支持首列勾选框。
type importTableModel struct {
	walk.TableModelBase
	items   []*importItem
	checked []bool
}

func (m *importTableModel) RowCount() int { return len(m.items) }

func (m *importTableModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.items) {
		return ""
	}
	it := m.items[row]
	switch col {
	case 0:
		return it.FileName
	case 1:
		return it.Name
	case 2:
		return it.LocalDir
	case 3:
		return it.RemoteDir
	case 4:
		return importStatusText(it)
	}
	return ""
}

// Checked 返回指定行是否勾选 (walk.ItemChecker 接口)。
func (m *importTableModel) Checked(row int) bool {
	if row < 0 || row >= len(m.checked) {
		return false
	}
	return m.checked[row]
}

// SetChecked 记录用户对指定行的勾选状态 (walk.ItemChecker 接口)。
func (m *importTableModel) SetChecked(row int, checked bool) error {
	if row < 0 || row >= len(m.checked) {
		return nil
	}
	m.checked[row] = checked
	return nil
}

// importStatusText 状态列显示文本: 已存在 > 解析失败 > 可导入。
func importStatusText(it *importItem) string {
	if it.Exists {
		return "已存在"
	}
	if it.ParseErr != "" {
		return "解析失败"
	}
	return "可导入"
}

// showImportDialog 弹出导入任务列表, 供用户勾选待导入项并选择频率。
// 返回勾选中的候选、所选频率(分钟)以及是否点击确定。
func showImportDialog(owner walk.Form, items []*importItem) (checked []*importItem, freqMin int, ok bool) {
	dlg, err := walk.NewDialog(owner)
	if err != nil {
		return nil, 0, false
	}
	dlg.SetTitle("导入任务")
	dlg.SetSize(walk.Size{Width: 760, Height: 440})
	centerOnMain(dlg, owner)

	root := walk.NewVBoxLayout()
	root.SetMargins(walk.Margins{HNear: 8, VNear: 8, HFar: 8, VFar: 8})
	root.SetSpacing(8)
	dlg.SetLayout(root)

	// 候选列表 (勾选框落在第一列)
	tv, _ := walk.NewTableView(dlg)
	tv.SetCheckBoxes(true)
	tv.SetAlternatingRowBG(true)
	tv.SetMinMaxSize(walk.Size{Width: 0, Height: 260}, walk.Size{Width: 0, Height: 0})
	model := &importTableModel{items: items, checked: make([]bool, len(items))}
	// 默认勾选 = 未存在同名任务且解析正常
	for i, it := range items {
		model.checked[i] = !it.Exists && it.ParseErr == ""
	}
	tv.SetModel(model)
	cols := tv.Columns()
	addCol := func(title string, width int) {
		c := walk.NewTableViewColumn()
		c.SetName(title)
		c.SetTitle(title)
		c.SetWidth(width)
		c.SetFormat("%v")
		cols.Add(c)
	}
	addCol("文件名", 200)
	addCol("任务名", 110)
	addCol("源", 210)
	addCol("目标", 140)
	addCol("状态", 80)

	// 频率行
	freqRow, _ := walk.NewComposite(dlg)
	fh := walk.NewHBoxLayout()
	fh.SetSpacing(6)
	freqRow.SetLayout(fh)
	freqLbl, _ := walk.NewLabel(freqRow)
	freqLbl.SetText("频率：")
	freqLbl.SetMinMaxSize(walk.Size{Width: 0, Height: 0}, walk.Size{Width: 0, Height: 0})
	freqCb, _ := walk.NewComboBox(freqRow)
	var texts []string
	for _, p := range importFreqPresets {
		texts = append(texts, p.text)
	}
	freqCb.SetModel(texts)
	freqCb.SetCurrentIndex(defaultImportFreqIndex()) // 默认"每30分钟"
	freqCb.SetMinMaxSize(walk.Size{Width: 120, Height: 0}, walk.Size{Width: 0, Height: 0})
	_, _ = walk.NewHSpacer(freqRow)

	// 按钮区
	btnRow, _ := walk.NewComposite(dlg)
	bb := walk.NewHBoxLayout()
	bb.SetAlignment(walk.AlignHFarVNear)
	btnRow.SetLayout(bb)
	okBtn, _ := walk.NewPushButton(btnRow)
	okBtn.SetText("确定")
	cancelBtn, _ := walk.NewPushButton(btnRow)
	cancelBtn.SetText("取消")
	okBtn.Clicked().Attach(func() {
		idx := freqCb.CurrentIndex()
		if idx < 0 || idx >= len(importFreqPresets) {
			idx = defaultImportFreqIndex()
		}
		freqMin = importFreqPresets[idx].min
		for i := range model.items {
			if model.checked[i] {
				checked = append(checked, model.items[i])
			}
		}
		ok = true
		dlg.Accept()
	})
	cancelBtn.Clicked().Attach(func() { dlg.Cancel() })
	dlg.SetDefaultButton(okBtn)
	dlg.SetCancelButton(cancelBtn)

	dlg.Run()
	return checked, freqMin, ok
}
