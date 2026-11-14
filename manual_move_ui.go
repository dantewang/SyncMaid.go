package main

import (
	"github.com/egoist/mygo/ui"
	"syncmaid/internal/taskconfig"
)

func (a *application) setManualKind(kind taskconfig.TaskKind) {
	s := &a.manual
	if !a.targetEditAllowed() || s.id != "" || manualKind(s.kind) == kind {
		return
	}
	s.kind = kind
	for i := range s.targets {
		if kind == taskconfig.Moving {
			s.targets[i].Strategy = taskconfig.Move
			s.targets[i].Collision = taskconfig.Skip
		} else {
			s.targets[i].Strategy = taskconfig.AddOnly
			s.targets[i].Flatten = false
		}
	}
	s.edited()
}
func (a *application) manualKindView(c *ui.Context, locked bool) {
	s := &a.manual
	ui.Row(c).Gap(8).Children(func() {
		ui.Button(c, "Sync同步任务").Selected(manualKind(s.kind) == taskconfig.Sync).Disabled(locked || s.id != "").OnClick(func() { a.setManualKind(taskconfig.Sync) })
		ui.Button(c, "Move移动任务").Selected(manualKind(s.kind) == taskconfig.Moving).Disabled(locked || s.id != "").OnClick(func() { a.setManualKind(taskconfig.Moving) })
	})
	if manualKind(s.kind) == taskconfig.Moving {
		ui.Text(c, "首个匹配目标拥有文件；冲突或失败保留源，不转投后续目标。已保存任务不能切换类型。")
	}
}
func (a *application) setMoveFlatten(flatten bool) {
	if !a.targetEditAllowed() || manualKind(a.manual.kind) != taskconfig.Moving {
		return
	}
	for i := range a.manual.targets {
		if a.manual.targets[i].ID == a.manual.targetID {
			a.manual.targets[i].Flatten = flatten
			a.manual.edited()
			return
		}
	}
}
func (a *application) moveOptionsView(c *ui.Context, locked bool) {
	for _, d := range a.manual.targets {
		if d.ID == a.manual.targetID {
			ui.Text(c, "Move冲突处理：保留源，不覆盖已有文件（加编号暂不支持）。")
			ui.Row(c).Gap(8).Children(func() {
				ui.Button(c, "保留目录结构").Selected(!d.Flatten).Disabled(locked).OnClick(func() { a.setMoveFlatten(false) })
				ui.Button(c, "平铺到目标根目录").Selected(d.Flatten).Disabled(locked).OnClick(func() { a.setMoveFlatten(true) })
			})
		}
	}
}
