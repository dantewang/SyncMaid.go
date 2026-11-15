package main

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"strings"
	"syncmaid/internal/taskconfig"
)

// Layout state is owned by the UI thread, keyed by configuration identity.
type taskCardLayout struct {
	collapsed map[taskconfig.ID]bool
	editor    bool
	running   taskconfig.ID
	focus     taskconfig.ID
}

func (a *application) cardActionAllowed() bool {
	return !a.manual.busy() && !a.manual.dirty && a.tasks != nil && a.taskError == nil
}

func (a *application) editCard(id, destination taskconfig.ID) {
	if !a.cardActionAllowed() {
		return
	}
	a.selectManual(id)
	if a.manual.id != id {
		return
	}
	if destination != "" {
		a.manual.selectTarget(destination)
	}
	a.cards.editor = true
}

func (a *application) runCard(id taskconfig.ID) {
	if !a.cardActionAllowed() {
		return
	}
	a.selectManual(id)
	if a.manual.id == id {
		a.startManual()
	}
}

func (a *application) closeTaskEditor() {
	if a.manual.dirty {
		a.manual.message = messagef("编辑尚未保存；请保存或明确放弃编辑后再切换任务。")
		return
	}
	a.cards.editor = false
}

func (a *application) toggleCard(id taskconfig.ID) {
	if a.cards.collapsed == nil {
		a.cards.collapsed = make(map[taskconfig.ID]bool)
	}
	a.cards.collapsed[id] = !a.cards.collapsed[id]
}

func (a *application) manualView(c *ui.Context) {
	width, _ := c.Size()
	s := &a.manual
	ui.Row(c).Fill().Children(func() {
		if width >= 900 {
			ui.Column(c).Width(180).FillHeight().Gap(8).Padding(12).Children(func() {
				ui.Text(c, a.tr("任务列表")).Bold()
				ui.Scroll(c).Grow(1).Children(func() {
					ui.Column(c).FillWidth().Gap(6).Children(func() {
						if a.tasks == nil {
							return
						}
						for _, t := range a.tasks.tasks {
							id := t.ID
							ui.Button(c, t.Name).FillWidth().Selected(s.id == id).Disabled(!a.cardActionAllowed()).OnClick(func() {
								a.selectManual(id)
								a.cards.focus = id
								if a.cards.collapsed != nil {
									delete(a.cards.collapsed, id)
								}
							})
						}
					})
				})
			})
		}
		ui.Column(c).Grow(1).FillHeight().Gap(8).Padding(12).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, "SyncMaid").FontSize(24).Bold()
				ui.Button(c, a.tr("新建任务")).Disabled(!a.cardActionAllowed()).OnClick(func() { a.newManual(); a.cards.editor = true })
				ui.Button(c, a.tr("设置")).Disabled(a.importPreview != nil || a.regexSession != nil).OnClick(func() { a.settingsOpen = !a.settingsOpen; a.historyOpen = false })
				ui.Button(c, a.tr("查看运行记录")).Disabled(a.importPreview != nil || a.regexSession != nil).OnClick(a.toggleHistory)
				ui.Button(c, a.tr("Exit")).OnClick(a.requestQuit)
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.Button(c, a.tr("重新载入")).Disabled(s.busy()).OnClick(a.reloadTasks)
				ui.Button(c, a.tr("导入旧任务")).Disabled(!a.cardActionAllowed()).OnClick(a.beginImport)
				ui.Button(c, a.tr("旧正则迁移")).Disabled(!a.cardActionAllowed() || s.id == "").OnClick(a.beginRegexMigration)
				ui.Button(c, a.tr("放弃编辑")).Disabled(s.busy() || !s.dirty).OnClick(a.discardManual)
				ui.Button(c, a.tr("停止")).Disabled(s.phase != manualRunning && s.phase != manualChecking).OnClick(a.stopManual)
			})
			a.watchView(c)
			a.mirrorPreviewView(c)
			if a.taskError != nil {
				ui.Text(c, a.tr("任务文件不可用，已阻止保存与运行: ")+a.taskError.Error())
			}
			if a.importError != nil && a.importPreview == nil {
				ui.Text(c, a.tr("导入拒绝，旧文件和任务库未修改：%s", a.importError.Error()))
			}
			if a.historyError != nil {
				ui.Text(c, a.tr("运行记录不可用或未保存；文件执行结果仍有效：")+historyText(a.historyError.Error(), historyErrorBytes))
			}
			if s.dirty {
				ui.Text(c, a.render(s.message))
			}
			switch {
			case a.regexSession != nil:
				a.regexMigrationView(c)
			case a.importPreview != nil:
				a.importView(c)
			case a.settingsOpen:
				a.settingsView(c)
			case a.historyOpen:
				a.historyViewContent(c)
			default:
				ui.Row(c).Grow(1).FillWidth().Gap(12).Children(func() {
					if !a.cards.editor || width >= 1350 {
						a.taskCardsView(c)
					}
					if a.cards.editor {
						col := ui.Column(c).FillHeight().Gap(8)
						if width >= 1350 {
							col = col.Width(540)
						} else {
							col = col.Grow(1)
						}
						col.Children(func() { a.taskEditorView(c) })
					}
				})
			}
		})
	})
}

func (a *application) taskCardsView(c *ui.Context) {
	ui.Column(c).Grow(1).FillHeight().Gap(8).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, a.tr("任务概览")).FontSize(22).Bold()
			ui.Button(c, a.tr("全部展开")).OnClick(func() { a.cards.collapsed = nil })
			ui.Button(c, a.tr("全部折叠")).OnClick(func() {
				a.cards.collapsed = make(map[taskconfig.ID]bool)
				if a.tasks != nil {
					for _, t := range a.tasks.tasks {
						a.cards.collapsed[t.ID] = true
					}
				}
			})
		})
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Gap(20).Children(func() {
				if a.tasks == nil {
					return
				}
				for _, t := range a.tasks.tasks {
					a.taskCard(c, t)
				}
			})
		})
	})
}

func (a *application) taskCard(c *ui.Context, t taskconfig.Task) {
	id := t.ID
	running := id == a.cards.running && (a.manual.phase == manualRunning || a.manual.phase == manualChecking || a.manual.phase == manualStopping)
	card := ui.Column(c).FillWidth().Padding(12).Gap(8).Border(1, c.Theme().Border).Radius(6)
	if a.cards.focus == id {
		card = card.ScrollIntoView()
		a.cards.focus = ""
	}
	card.Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			marker := "▾"
			if a.cards.collapsed[id] {
				marker = "▸"
			}
			ui.Button(c, marker+" "+t.Name).Label(a.tr("展开或折叠任务") + " " + t.Name).Grow(1).OnClick(func() { a.toggleCard(id) })
			if id == a.cards.running && (a.manual.phase == manualRunning || a.manual.phase == manualChecking) {
				ui.Button(c, a.tr("停止")).OnClick(a.stopManual)
			} else {
				ui.Button(c, a.tr("运行")).Disabled(!a.cardActionAllowed()).OnClick(func() { a.runCard(id) })
			}
			ui.Button(c, a.tr("编辑任务")).Disabled(!a.cardActionAllowed()).OnClick(func() { a.editCard(id, "") })
		})
		kind := "Sync同步任务"
		if t.Kind == taskconfig.Moving {
			kind = "Move移动任务"
		}
		trigger := "Manual手动"
		if t.Trigger.Kind == "watch" {
			trigger = "Watch监视"
		} else if t.Trigger.Kind == "scheduled" {
			trigger = "Scheduled定时"
		}
		ui.Text(c, a.tr(kind)+" · "+a.tr(trigger)).FontSize(12)
		ui.Text(c, a.tr("源文件夹")+": "+t.SourcePath)
		if w := a.watches[id]; w != nil {
			ui.Text(c, a.render(w.status)).FontSize(12)
		}
		result, hasResult := a.results[id]
		if running && id == a.manual.id && !a.manual.dirty {
			ui.Text(c, a.render(a.manual.message))
			ui.Text(c, fmt.Sprintf(a.tr("当前/最后目标 %s · 已发布 %d / %d · 未变化 %d · 已传输 %d 字节"), a.tr(a.manual.progress.Stage), a.manual.progress.Copied, a.manual.progress.Total, a.manual.progress.Unchanged, a.manual.progress.Bytes))
		} else if running {
			ui.Text(c, a.render(a.watchActivity))
		} else if hasResult {
			ui.Text(c, a.render(result.message))
		} else {
			ui.Text(c, a.tr("未开始"))
		}
		if a.cards.collapsed[id] {
			return
		}
		if t.Kind == taskconfig.Moving {
			ui.Text(c, a.tr("按目标顺序首个匹配；失败不转投后续目标。")).FontSize(12)
		}
		for i, d := range t.Destinations {
			did := d.ID
			ui.Column(c).FillWidth().Padding(8).Gap(4).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, fmt.Sprintf("%d. %s", i+1, d.Name)).Grow(1).Bold()
					ui.Button(c, a.tr("编辑目标")).Label(a.tr("编辑目标") + " " + d.Name).Disabled(!a.cardActionAllowed()).OnClick(func() { a.editCard(id, did) })
				})
				ui.Text(c, d.Target.Path)
				strategy := string(d.Strategy)
				switch strategy {
				case "AddOnly":
					strategy = "仅添加"
				case "Mirror":
					strategy = "Mirror镜像"
				case "Move":
					strategy = "Move移动任务"
				}
				ui.Text(c, a.tr(strategy)+" · "+a.ruleSummary(d.Filters)).FontSize(12)
				r, ok := result.destinations[did]
				if running && id == a.manual.id && !a.manual.dirty {
					r, ok = a.manual.destinationResults[did]
				}
				if ok {
					ui.Text(c, fmt.Sprintf(a.tr("%s: %s · 原始文件 %d · 匹配 %d · 已发布 %d · 未变化 %d · 已移除 %d · 移动 %d · 保留 %d · 失败 %d · 源未匹配 %d"), d.Name, a.tr(string(r.State)), r.Progress.SourceFiles, r.Progress.Total, r.Progress.Copied, r.Progress.Unchanged, r.Progress.Removed, r.Progress.Moved, r.Progress.Retained, r.Progress.Failed, r.Progress.Unmatched)).FontSize(12)
					if r.Error != "" {
						ui.Text(c, historyText(r.Error, historyErrorBytes)).FontSize(12)
					}
				}
			})
		}
	})
}

func (a *application) ruleSummary(rules []taskconfig.Rule) string {
	parts := make([]string, 0, len(rules))
	for _, r := range rules {
		if r.Kind == "all" {
			parts = append(parts, a.tr("全部文件"))
			continue
		}
		if r.Kind == "none" {
			parts = append(parts, a.tr("无匹配"))
			continue
		}
		if r.Rules != nil {
			parts = append(parts, a.tr(r.Kind)+" ("+a.ruleSummary(*r.Rules)+")")
			continue
		}
		if r.Rule != nil {
			parts = append(parts, a.tr(r.Kind)+" ("+a.ruleSummary([]taskconfig.Rule{*r.Rule})+")")
			continue
		}
		value := ""
		switch {
		case r.Prefix != nil:
			value = *r.Prefix
		case r.Extension != nil:
			value = *r.Extension
		case r.Pattern != nil:
			value = *r.Pattern
		}
		parts = append(parts, a.tr(r.Kind)+" "+value)
	}
	return strings.Join(parts, " · ")
}
