package main

import (
	"github.com/egoist/mygo/ui"
	"syncmaid/internal/taskconfig"
)

func (a *application) setManualKind(kind taskconfig.TaskKind) {
	s := &a.manual
	if !a.targetEditAllowed() || len(s.targets) != 0 || manualKind(s.kind) == kind {
		return
	}
	s.kind = kind
	s.edited()
}
func (a *application) manualKindView(c *ui.Context, locked bool) {
	s := &a.manual
	ui.Text(c, a.tr("此任务做什么"))
	for _, kind := range []taskconfig.TaskKind{taskconfig.Sync, taskconfig.Moving} {
		key, description := "同步", "把源复制到各个目标，源保持原样。每个目标各自挑选文件。"
		if kind == taskconfig.Moving {
			key = "移动"
			description = "把源里的文件分拣走：每个文件进入第一条匹配它的规则，并从源中移除。"
		}
		glyph := "sync"
		if kind == taskconfig.Moving {
			glyph = "move"
		}
		porcelainChoice(c, glyph, a.tr(key), a.tr(description), manualKind(s.kind) == kind, locked || len(s.targets) != 0, func() { a.setManualKind(kind) }).MinHeight(94)
	}
	if manualKind(s.kind) == taskconfig.Moving {
		ui.Text(c, a.tr("首个匹配目标拥有文件；不覆盖目标，失败不转投后续目标。已保存任务不能切换类型。"))
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
			ui.Text(c, a.tr("Move冲突处理：保留源，或在扩展名前加编号；都不覆盖已有文件。"))
			ui.Row(c).Gap(8).Children(func() {
				ui.Button(c, a.tr("Skip保留源")).Selected(d.Collision == "" || d.Collision == taskconfig.Skip).Disabled(locked).OnClick(func() { a.setMoveCollision(taskconfig.Skip) })
				ui.Button(c, a.tr("Suffix加编号")).Selected(d.Collision == taskconfig.Suffix).Disabled(locked).OnClick(func() { a.setMoveCollision(taskconfig.Suffix) })
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.Button(c, a.tr("保留目录结构")).Selected(!d.Flatten).Disabled(locked).OnClick(func() { a.setMoveFlatten(false) })
				ui.Button(c, a.tr("平铺到目标根目录")).Selected(d.Flatten).Disabled(locked).OnClick(func() { a.setMoveFlatten(true) })
			})
		}
	}
}

func (a *application) setMoveCollision(policy taskconfig.CollisionPolicy) {
	if !a.targetEditAllowed() || manualKind(a.manual.kind) != taskconfig.Moving {
		return
	}
	if policy != taskconfig.Skip && policy != taskconfig.Suffix {
		return
	}
	for i := range a.manual.targets {
		if a.manual.targets[i].ID == a.manual.targetID {
			a.manual.targets[i].Collision = policy
			a.manual.edited()
			return
		}
	}
}
