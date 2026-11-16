package main

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"syncmaid/internal/taskconfig"
	"time"
)

func (a *application) manualView(c *ui.Context) {
	c.SetTheme(&porcelain)
	width, height := c.Size()
	s := &a.manual
	ui.Row(c).Fill().Gap(0).Children(func() {
		ui.Column(c).Width(210).FillHeight().Background(rgb(0xD9DFE3)).Border(1, porcelain.Border).Children(func() {
			ui.Text(c, a.tr("任务")).FontSize(10).TextColor(porcelain.TextMuted).Padding(14, 14, 6, 14)
			ui.Scroll(c).Grow(1).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Gap(0).Children(func() {
					if a.tasks == nil {
						return
					}
					for _, t := range a.tasks.tasks {
						id := t.ID
						active := a.cards.navigation == id
						nav := ui.ButtonBase(c).Label(t.Name).Tooltip(t.Name).Justify(ui.Start).FillWidth().Height(37).Gap(10).Padding(0, 14)
						if active {
							nav = nav.Background(porcelain.Background)
						} else if nav.Hovered() {
							nav = nav.Background(ui.Color{R: 248, G: 250, B: 251, A: 204})
						}
						nav.Children(func() {
							if active {
								ui.Box(c).Absolute().Left(0).Top(0).Bottom(0).Width(3).Background(porcelain.Text)
							}
							ui.Icon(c, porcelainGlyphs["□"]).Size(16, 16)
							name := ui.Text(c, t.Name).FontSize(14).SingleLine().Grow(1).Shrink(1).MinWidth(0)
							if active {
								name.FontWeight(600)
							}
							ui.Box(c).Size(7, 7).Background(a.taskHealthColor(id))
						}).Disabled(!a.cardActionAllowed()).OnClick(func() {
							a.selectManual(id)
							a.cards.navigation = id
							a.cards.focus = id
							if a.cards.collapsed != nil {
								delete(a.cards.collapsed, id)
							}
						})
					}
				})
			})
			ui.Row(c).FillWidth().Padding(6, 10).Gap(6).Border(1, porcelain.Border).Children(func() {
				ui.Text(c, "SYNC").FontSize(13).LineHeight(1.5).Bold().Background(porcelain.Accent).TextColor(porcelain.AccentText).Padding(4, 6)
				ui.Text(c, "MAID").FontSize(13).Bold().Grow(1)
				gear := porcelainIcon(c, "⚙", a.tr("设置")).Border(0, ui.Transparent)
				if a.settingsOpen {
					gear = gear.Background(porcelain.Accent).TextColor(porcelain.AccentText)
				}
				gear.Disabled(a.importPreview != nil || a.regexSession != nil || a.manual.busy()).OnClick(func() { a.settingsOpen = !a.settingsOpen; a.historyOpen = false })
				ui.MenuButton(c, "⋯", func(m *ui.Menu) {
					if m.Item(a.tr("运行全部")).Disabled(!a.cardActionAllowed()).Chosen() {
						a.startBatch()
					}
					if m.Item(a.tr("重新载入设置")).Disabled(a.commonBusy).Chosen() {
						a.reloadCommon()
					}
					if m.Item(a.tr("查看运行记录")).Disabled(a.importPreview != nil || a.regexSession != nil).Chosen() {
						a.toggleHistory()
					}
					if m.Item(a.tr("重新载入")).Disabled(s.busy()).Chosen() {
						a.reloadTasks()
					}
					if m.Item(a.tr("导入旧任务")).Disabled(!a.cardActionAllowed()).Chosen() {
						a.beginImport()
					}
					if m.Item(a.tr("旧正则迁移")).Disabled(!a.cardActionAllowed() || s.id == "").Chosen() {
						a.beginRegexMigration()
					}
					if m.Item(a.tr("暂停自动触发")).Disabled(a.watchPhase != watchesServing).Chosen() {
						a.stopWatches(watchesPaused)
					}
					if m.Item(a.tr("恢复自动触发")).Disabled(a.watchPhase != watchesPaused).Chosen() {
						a.resumeWatches()
					}
					m.Separator()
					if m.Item(a.tr("Exit")).Chosen() {
						a.requestQuit()
					}
				}).Label(a.tr("更多操作")).Tooltip(a.tr("更多操作")).Padding(3).Width(24)
			})
		})
		pane := ui.Column(c).Grow(1).FillHeight().Gap(0)
		if !a.settingsOpen {
			pane = pane.Padding(12, 16)
		}
		pane.Children(func() {
			if s.phase == manualRunning || s.phase == manualChecking || s.phase == manualStopping {
				ui.Row(c).FillWidth().Gap(8).Children(func() {
					activity := a.render(a.watchActivity)
					if s.phase == manualStopping {
						activity = a.render(s.message)
					}
					ui.Text(c, activity).FontSize(12).Grow(1)
					if a.batch.phase == batchServing {
						ui.Text(c, a.tr("批次余下 %d 项", len(a.batch.pending))).FontSize(12).TextColor(porcelain.TextMuted)
					}
					porcelainButton(c, a.tr("停止")).Disabled(s.phase == manualStopping).OnClick(a.stopManual)
				})
			}
			if s.mirrorPreview != nil {
				a.mirrorPreviewView(c)
			}
			if a.regexSession != nil {
				a.regexMigrationView(c)
				return
			}
			if a.importPreview != nil {
				a.importView(c)
				return
			}
			if a.settingsOpen {
				ui.Column(c).Fill().Children(func() { a.settingsView(c) })
				return
			}
			if a.historyOpen {
				a.historyViewContent(c)
				return
			}
			a.porcelainHeader(c, width)
			if a.taskError != nil {
				ui.Text(c, a.tr("任务文件不可用，已阻止保存与运行: ")+a.taskError.Error()).TextColor(porcelain.Danger)
			}
			if a.importError != nil {
				ui.Text(c, a.tr("导入拒绝，旧文件和任务库未修改：%s", a.importError.Error())).TextColor(porcelain.Danger)
			}
			if a.historyError != nil {
				ui.Text(c, a.tr("运行记录不可用或未保存；文件执行结果仍有效：")+historyText(a.historyError.Error(), historyErrorBytes)).TextColor(porcelain.Danger)
			}
			if a.watchPhase == watchesPaused {
				ui.Text(c, a.tr("自动触发已暂停；恢复后Watch重等静默期，Scheduled重新计算下一次。")).FontSize(12)
			}
			ui.Scroll(c).Grow(1).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Gap(12).Children(func() {
					if a.tasks != nil {
						for _, t := range a.tasks.tasks {
							a.taskCard(c, t)
						}
					}
				})
			})
		})
	})
	if a.cards.editor && s.mirrorPreview == nil {
		preferred := float32(470)
		if a.cards.destinations {
			preferred = 760
		}
		sheetWidth := min(preferred, width-48)
		ui.Overlay(c, func() {
			back := ui.Box(c).Absolute().Left(0).Top(0).Size(width, height).Background(ui.Color{R: 25, G: 28, B: 34, A: 71}).Modal()
			if back.OverlayShortcut(0, ui.KeyEscape) {
				a.closeTaskEditor()
			}
			back.Children(func() {
				ui.Column(c).Absolute().Right(0).Top(0).Width(sheetWidth).Height(height).LineHeight(1.25).Background(porcelain.Background).Gap(0).Padding(8, 16, 16, 16).Children(func() { a.taskEditorView(c) })
			})
		})
	}
}

func (a *application) porcelainHeader(c *ui.Context, width float32) {
	count, targets := 0, 0
	if a.tasks != nil {
		count = len(a.tasks.tasks)
		for _, t := range a.tasks.tasks {
			targets += len(t.Destinations)
		}
	}
	ui.Row(c).FillWidth().Gap(8).Padding(0, 0, 12, 0).Children(func() {
		ui.Text(c, a.tr("同步任务")).FontSize(18).Margin(0, 12, 0, 0)
		if width >= 900 {
			for _, v := range []struct {
				n   int
				key string
			}{{count, "任务"}, {targets, "目标"}, {func() int {
				if a.manual.phase == manualRunning || a.manual.phase == manualChecking || a.manual.phase == manualStopping {
					return 1
				}
				return 0
			}(), "运行中"}} {
				ui.Box(c).Width(1).Height(42).Background(porcelain.Border)
				ui.Column(c).Padding(0, 4).Children(func() {
					ui.Text(c, fmt.Sprint(v.n)).FontSize(17).FontWeight(600)
					ui.Text(c, a.tr(v.key)).FontSize(9).TextColor(porcelain.TextMuted)
				})
			}
		}
		ui.Box(c).Grow(1)
		collapsed := len(a.cards.collapsed) > 0
		label := "全部折叠"
		if collapsed {
			label = "全部展开"
		}
		porcelainButton(c, a.tr(label)).OnClick(func() {
			if collapsed {
				a.cards.collapsed = nil
			} else {
				a.cards.collapsed = map[taskconfig.ID]bool{}
				if a.tasks != nil {
					for _, t := range a.tasks.tasks {
						a.cards.collapsed[t.ID] = true
					}
				}
			}
		})
		if width >= 900 {
			porcelainButton(c, "▷ "+a.tr("运行全部")).Disabled(!a.cardActionAllowed()).Tooltip(a.tr("按已保存顺序串行运行；停止会丢弃余下任务。")).OnClick(a.startBatch)
		}
		ui.PrimaryButton(c, "＋ "+a.tr("新建任务")).Padding(5, 10).Disabled(!a.cardActionAllowed()).OnClick(func() { a.newManual(); a.cards.editor = true; a.cards.destinations = false })
	})
}

func (a *application) taskCard(c *ui.Context, t taskconfig.Task) {
	width, _ := c.Size()
	healthWidth, resultWidth := float32(190), float32(280)
	if width < 900 {
		healthWidth = 100
		resultWidth = 100
	}
	id := t.ID
	running := id == a.cards.running && (a.manual.phase == manualRunning || a.manual.phase == manualChecking || a.manual.phase == manualStopping)
	result, hasResult := a.results[id]
	color := porcelain.Border
	status := a.tr("未开始")
	if hasResult {
		status = a.tr("全部已同步")
		failed := 0
		complete := len(result.destinations) > 0
		for _, r := range result.destinations {
			if r.State != destinationSucceeded && r.State != destinationNoMatches {
				complete = false
			}
			if r.State == destinationFailed {
				failed++
				color = porcelain.Danger
			}
		}
		if !complete {
			status = a.render(result.message)
		}
		if failed > 0 {
			status = a.tr("%d个中有%d个失败", len(t.Destinations), failed)
		}
	}
	if id == a.manual.id && a.manual.phase == manualDraft && !a.manual.dirty && a.manual.message.key != "" {
		status = a.render(a.manual.message)
		color = porcelain.Danger
	}
	if running {
		status = a.tr("正在运行…")
		if a.manual.phase == manualStopping {
			status = a.render(a.manual.message)
		}
		color = porcelain.Accent
	}
	card := ui.Column(c).FillWidth().Gap(0).Border(1, color)
	if a.cards.focus == id {
		card = card.ScrollIntoView()
		a.cards.focus = ""
	}
	card.Children(func() {
		ui.Row(c).FillWidth().MinHeight(68).Gap(4).Padding(10).Children(func() {
			glyph := "⌄"
			if a.cards.collapsed[id] {
				glyph = "›"
			}
			porcelainIcon(c, glyph, a.tr("展开或折叠任务")+" "+t.Name).Border(0, ui.Transparent).OnClick(func() { a.toggleCard(id) })
			ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Clip().Gap(3).Children(func() {
				ui.Row(c).FillWidth().Clip().Gap(8).Children(func() {
					ui.Text(c, t.Name).FontSize(16).SingleLine().Shrink(0).Ellipsis("…").Tooltip(t.Name)
					kind := "同步"
					if t.Kind == taskconfig.Moving {
						kind = "移动"
					}
					porcelainTag(c, a.tr(kind), true)
					trigger := map[string]string{"manual": "手动", "watch": "监视", "scheduled": "定时"}[t.Trigger.Kind]
					triggerText := a.tr(trigger)
					if t.Trigger.Kind == "scheduled" && t.Trigger.CronExpression != nil {
						triggerText = a.tr("定时（%s）", *t.Trigger.CronExpression)
					}
					porcelainTag(c, triggerText, false)
					if w := a.watches[id]; w != nil && w.ticket.phase == watchFailed {
						ui.Text(c, a.tr("触发器异常")).FontSize(12).LineHeight(1).Padding(4, 10).Background(porcelain.Warning).TextColor(porcelain.AccentText).Tooltip(a.render(w.status))
					}
				})
				porcelainPath(c, t.SourcePath)
			})
			ui.Text(c, status).FontSize(16).MaxWidth(healthWidth).SingleLine().Ellipsis("…").Tooltip(status).TextColor(func() ui.Color {
				if color == porcelain.Danger {
					return color
				}
				return porcelain.Text
			}())
			if running {
				porcelainIcon(c, "■", a.tr("停止")).OnClick(a.stopManual)
			} else {
				porcelainIcon(c, "▷", a.tr("运行")).Background(porcelain.Accent).TextColor(porcelain.AccentText).Disabled(!a.cardActionAllowed()).OnClick(func() { a.runCard(id) })
			}
			porcelainIcon(c, "＋", a.tr("添加目标")).Disabled(!a.cardActionAllowed()).OnClick(func() {
				a.editCard(id, "")
				if a.manual.id == id && a.cards.editor {
					a.cards.destinations = true
					a.addManualTarget()
				}
			})
			porcelainIcon(c, "♢", a.tr("编辑任务")).Disabled(!a.cardActionAllowed()).OnClick(func() { a.editCard(id, "") })
			porcelainIcon(c, "⌫", a.tr("删除任务")).Disabled(!a.cardActionAllowed()).OnClick(func() {
				a.selectManual(id)
				if a.manual.id == id {
					a.confirmDeleteManual()
				}
			})
		})
		if a.cards.collapsed[id] {
			return
		}
		for i, d := range t.Destinations {
			did := d.ID
			r, ok := result.destinations[did]
			if running && a.manual.id == id && !a.manual.dirty {
				r, ok = a.manual.destinationResults[did]
			}
			ui.Box(c).Height(1).FillWidth().Background(porcelain.Border)
			ui.Row(c).FillWidth().MinHeight(68).Gap(8).Padding(10, 16, 10, 16).Children(func() {
				mark := porcelain.TextMuted
				if ok && r.State == destinationSucceeded {
					mark = porcelain.Text
				}
				if ok && r.State == destinationFailed {
					mark = porcelain.Danger
				}
				ui.Box(c).Size(7, 7).Background(mark)
				ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Clip().Gap(3).Children(func() {
					ui.Row(c).FillWidth().Clip().Gap(8).Children(func() {
						name := d.Name
						if t.Kind == taskconfig.Moving {
							name = fmt.Sprintf("%d. %s", i+1, name)
						}
						ui.Text(c, name).SingleLine().Shrink(0).Ellipsis("…").Tooltip(name)
						if t.Kind != taskconfig.Moving {
							key := "仅添加"
							if d.Strategy == taskconfig.Mirror {
								key = "镜像"
							}
							porcelainTag(c, a.tr(key), false)
						}
						porcelainTag(c, a.ruleSummary(d.Filters), false)
					})
					porcelainPath(c, d.Target.Path)
				})
				summary := a.destinationStatus(r, ok, time.Now())
				ui.ButtonBase(c).Role(ui.RoleLink).Label(summary).MaxWidth(resultWidth).Background(ui.Transparent).Tooltip(summary).Children(func() { ui.Text(c, summary).SingleLine().Ellipsis("…").Underline().FontSize(16).TextColor(mark) }).OnClick(func() { a.openDestinationLog(id, did) })
				porcelainIcon(c, "♢", a.tr("编辑目标")+" "+d.Name).Border(0, ui.Transparent).Disabled(!a.cardActionAllowed()).OnClick(func() { a.editCard(id, did) })
				porcelainIcon(c, "⌫", a.tr("移除目标（仅配置）")+" "+d.Name).Border(0, ui.Transparent).Disabled(!a.cardActionAllowed() || len(t.Destinations) <= 1).OnClick(func() { a.editCard(id, did); a.removeManualTarget() })
			})

		}
	})
}
