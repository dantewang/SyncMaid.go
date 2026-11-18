package main

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"syncmaid/internal/appcore"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
	"time"
)

type uiScreen uint8

const (
	screenTasks uiScreen = iota
	screenHistory
	screenSettings
	screenImport
	screenRegex
	screenMirrorConfirm
)

func (a *application) activeScreen() uiScreen {
	switch {
	case a.manual.mirrorPreview != nil:
		return screenMirrorConfirm
	case a.regexSession != nil:
		return screenRegex
	case a.importPreview != nil:
		return screenImport
	case a.settingsOpen:
		return screenSettings
	case a.historyOpen:
		return screenHistory
	default:
		return screenTasks
	}
}

func (a *application) manualView(c *ui.Context) {
	c.SetTheme(&porcelain)
	width, height := c.Size()
	s := &a.manual
	screen := a.activeScreen()
	ui.Row(c).Fill().Gap(0).Children(func() {
		ui.Column(c).Width(210).FillHeight().Background(rgb(0xD9DFE3)).Children(func() {
			ui.Text(c, a.tr("任务")).FontSize(10).TextColor(porcelain.TextMuted).Padding(14, 14, 6, 14)
			ui.Scroll(c).Grow(1).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Gap(0).Children(func() {
					if a.tasks == nil {
						return
					}
					for _, t := range a.tasks.Tasks {
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
							_, health := a.taskHealth(t)
							ui.Box(c).Size(7, 7).Background(health)
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
			ui.Box(c).Height(1).FillWidth().Background(porcelain.Border)
			ui.Row(c).FillWidth().Padding(6, 10).Gap(6).Children(func() {
				ui.Text(c, "SYNC").FontSize(13).LineHeight(1.5).Bold().Background(porcelain.Accent).TextColor(porcelain.AccentText).Padding(4, 6)
				ui.Text(c, "MAID").FontSize(13).Bold().Grow(1)
				modalLocked := screen == screenMirrorConfirm || screen == screenImport || screen == screenRegex
				gear := porcelainIcon(c, "⚙", a.tr("设置")).Border(0, ui.Transparent)
				if screen == screenSettings {
					gear = porcelainPrimaryIcon(c, "⚙", a.tr("设置")).Border(0, ui.Transparent)
				}
				gear.Disabled(modalLocked || a.manual.busy()).OnClick(func() { a.settingsOpen = !a.settingsOpen; a.historyOpen = false })
				ui.MenuButton(c, "", func(m *ui.Menu) {
					if m.Item(a.tr("运行全部")).Disabled(!a.cardActionAllowed() || modalLocked).Chosen() {
						a.startBatch()
					}
					if m.Item(a.tr("重新载入设置")).Disabled(a.commonBusy).Chosen() {
						a.reloadCommon()
					}
					if m.Item(a.tr("查看运行记录")).Disabled(modalLocked).Chosen() {
						a.toggleHistory()
					}
					if m.Item(a.tr("重新载入")).Disabled(s.busy()).Chosen() {
						a.reloadTasks()
					}
					if m.Item(a.tr("导入旧任务")).Disabled(!a.cardActionAllowed() || modalLocked).Chosen() {
						a.beginImport()
					}
					if m.Item(a.tr("旧正则迁移")).Disabled(!a.cardActionAllowed() || modalLocked || s.id == "").Chosen() {
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
				}).Label(a.tr("更多操作")).Tooltip(a.tr("更多操作")).Padding(0).Shrink(0).Size(24, 24)
			})
		})
		ui.Box(c).Width(1).FillHeight().Background(porcelain.Border)
		pane := ui.Column(c).Grow(1).FillHeight().Gap(0)
		if screen != screenSettings {
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
			switch screen {
			case screenMirrorConfirm:
				a.mirrorPreviewView(c)
				return
			case screenRegex:
				a.regexMigrationView(c)
				return
			case screenImport:
				a.importView(c)
				return
			case screenSettings:
				ui.Column(c).Fill().Children(func() { a.settingsView(c) })
				return
			case screenHistory:
				a.historyViewContent(c)
				return
			}
			a.porcelainHeader(c, width)
			if a.taskError != nil {
				ui.Text(c, a.tr("任务文件不可用，已阻止保存与运行: ")+a.taskError.Error()).TextColor(porcelain.Danger)
				if backup, ok := a.taskRecovery(); ok {
					porcelainButton(c, a.tr("恢复任务备份")).Disabled(!a.taskRecoveryAllowed()).OnClick(func() { a.confirmTaskRecovery(backup) })
				}
			}
			if a.recoveryNotice.key != "" {
				ui.Text(c, a.render(a.recoveryNotice)).FontSize(13)
			}
			if a.importError != nil {
				ui.Text(c, a.tr("导入拒绝，旧文件和任务库未修改：%s", a.importError.Error())).TextColor(porcelain.Danger)
			}
			if a.historyError != nil {
				ui.Text(c, a.tr("运行记录不可用或未保存；文件执行结果仍有效：")+appcore.HistoryText(a.historyError.Error(), appcore.HistoryErrorBytes)).TextColor(porcelain.Danger)
			}
			if a.watchPhase == watchesPaused {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, a.tr("自动触发已暂停"))
					porcelainButton(c, a.tr("恢复自动触发")).Disabled(!a.cardActionAllowed()).OnClick(a.resumeWatches)
				})
			}
			now := time.Now()
			focusID := a.cards.focus
			a.cards.focus = ""
			ui.Scroll(c).Grow(1).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Gap(12).Children(func() {
					if a.tasks != nil {
						for _, t := range a.tasks.Tasks {
							a.taskCard(c, t, now, focusID)
						}
					}
				})
			})
		})
	})
	if a.cards.editor && screen == screenTasks {
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
			ui.Column(c).Absolute().Right(0).Top(0).Width(sheetWidth).Height(height).LineHeight(1.25).Background(porcelain.Background).Gap(0).Padding(8, 16, 16, 16).Children(func() { a.taskEditorView(c) })
		})
	}
}

func (a *application) porcelainHeader(c *ui.Context, width float32) {
	count, targets := 0, 0
	if a.tasks != nil {
		count = len(a.tasks.Tasks)
		for _, t := range a.tasks.Tasks {
			targets += len(t.Destinations)
		}
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Padding(0, 0, 12, 0).Children(func() {
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
		collapsed := a.hasCollapsedCards()
		label := "全部折叠"
		if collapsed {
			label = "全部展开"
		}
		porcelainButton(c, a.tr(label)).Height(32).OnClick(func() {
			if collapsed {
				a.cards.collapsed = nil
			} else {
				a.cards.collapsed = map[taskconfig.ID]bool{}
				if a.tasks != nil {
					for _, t := range a.tasks.Tasks {
						a.cards.collapsed[t.ID] = true
					}
				}
			}
		})
		porcelainButton(c, "▷ "+a.tr("运行全部")).Height(32).Disabled(!a.cardActionAllowed()).Tooltip(a.tr("按已保存顺序串行运行；停止会丢弃余下任务。")).OnClick(a.startBatch)
		ui.PrimaryButton(c, "＋ "+a.tr("新建任务")).Radius(0).Padding(5, 10).FontSize(16).Height(32).Disabled(!a.cardActionAllowed()).OnClick(func() { a.newManual(); a.cards.editor = true; a.cards.destinations = false })
	})
}

func (a *application) taskCard(c *ui.Context, t taskconfig.Task, now time.Time, focusID taskconfig.ID) {
	width, _ := c.Size()
	healthWidth, resultWidth := float32(190), float32(280)
	if width < 900 {
		healthWidth = 100
		resultWidth = 100
	}
	id := t.ID
	running := id == a.cards.running && (a.manual.phase == manualRunning || a.manual.phase == manualChecking || a.manual.phase == manualStopping)
	result := a.results[id]
	status, color := a.taskHealth(t)
	card := ui.Column(c).FillWidth().Gap(0).Border(1, color)
	if focusID == id {
		card = card.ScrollIntoView()
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
				if color == porcelain.Danger || color == porcelain.Warning {
					return color
				}
				return porcelain.Text
			}())
			if running {
				porcelainIcon(c, "■", a.tr("停止")).OnClick(a.stopManual)
			} else {
				porcelainPrimaryIcon(c, "▷", a.tr("运行")).Disabled(!a.cardActionAllowed() || len(t.Destinations) == 0).OnClick(func() { a.runCard(id) })
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
				mark := destinationHealthColor(r, ok)
				if ok && r.State == syncengine.DestinationSkipped && result.outcome == "failed" {
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
						porcelainTag(c, a.filterSummary(d.Filters), false)
					})
					porcelainPath(c, d.Target.Path)
				})
				summary := a.destinationStatus(r, ok, now)
				ui.ButtonBase(c).Role(ui.RoleLink).Label(summary).MaxWidth(resultWidth).Background(ui.Transparent).Tooltip(summary).Children(func() { ui.Text(c, summary).SingleLine().Ellipsis("…").Underline().FontSize(16).TextColor(mark) }).OnClick(func() { a.openDestinationLog(id, did) })
				porcelainIcon(c, "♢", a.tr("编辑目标")+" "+d.Name).Disabled(!a.cardActionAllowed()).OnClick(func() { a.editCard(id, did) })
				porcelainIcon(c, "⌫", a.tr("移除目标（仅配置）")+" "+d.Name).Disabled(!a.cardActionAllowed()).OnClick(func() { a.editCard(id, did); a.confirmRemoveManualTarget() })
			})

		}
	})
}

func destinationHealthColor(r syncengine.DestinationResult, exists bool) ui.Color {
	if !exists {
		return porcelain.TextMuted
	}
	switch r.State {
	case syncengine.DestinationSucceeded, syncengine.DestinationNoMatches, syncengine.DestinationRunning:
		return porcelain.Text
	case syncengine.DestinationFailed:
		return porcelain.Danger
	case syncengine.DestinationCanceled, syncengine.DestinationSkipped, syncengine.DestinationInterrupted:
		return porcelain.Warning
	default:
		return porcelain.TextMuted
	}
}

func (a *application) taskHealth(t taskconfig.Task) (string, ui.Color) {
	id := t.ID
	if a.cards.running == id && (a.manual.phase == manualRunning || a.manual.phase == manualChecking || a.manual.phase == manualStopping) {
		if a.manual.phase == manualStopping {
			return a.render(a.manual.message), porcelain.Warning
		}
		return a.tr("正在运行…"), porcelain.Text
	}
	if len(t.Destinations) == 0 {
		return a.tr("尚未添加目标"), porcelain.TextMuted
	}
	result := a.results[id]
	completed, matched, failed, retained, interrupted := 0, 0, 0, 0, false
	currentResult := false
	for _, d := range t.Destinations {
		goal, exists := result.destinations[d.ID]
		currentResult = currentResult || exists
		switch goal.State {
		case syncengine.DestinationSucceeded:
			completed++
			matched++
			retained += goal.Progress.Retained
		case syncengine.DestinationNoMatches:
			completed++
		case syncengine.DestinationFailed:
			failed++
		case syncengine.DestinationCanceled, syncengine.DestinationSkipped, syncengine.DestinationInterrupted:
			interrupted = true
		}
	}
	switch {
	case failed > 0:
		return a.tr("%d个中有%d个失败", len(t.Destinations), failed), porcelain.Danger
	case result.outcome == "failed" && (currentResult || len(result.destinations) == 0):
		return a.render(result.message), porcelain.Danger
	case interrupted || result.outcome == "stopped" && currentResult:
		return a.render(result.message), porcelain.Warning
	case completed == len(t.Destinations) && matched == 0:
		return a.tr(string(syncengine.DestinationNoMatches)), porcelain.Text
	case completed == len(t.Destinations) && retained > 0:
		return a.tr("完成 · 保留 %d 个源文件", retained), porcelain.Text
	case completed == len(t.Destinations):
		return a.tr("全部已同步"), porcelain.Text
	case completed > 0:
		return a.tr("部分目标已完成"), porcelain.Text
	default:
		return a.tr("未开始"), porcelain.TextMuted
	}
}
func (a *application) destinationStatus(r syncengine.DestinationResult, exists bool, now time.Time) string {
	if !exists || r.State == syncengine.DestinationWaiting {
		return a.tr("从未运行")
	}
	if r.State != syncengine.DestinationSucceeded {
		if r.Error != "" {
			return a.tr(string(r.State)) + " · " + appcore.HistoryText(r.Error, appcore.HistoryErrorBytes)
		}
		return a.tr(string(r.State))
	}
	if r.Progress.Retained > 0 {
		return a.tr("移动 %d · 保留 %d 个源文件", r.Progress.Moved, r.Progress.Retained)
	}
	span := now.Sub(r.Ended)
	files := max(r.Progress.Copied, r.Progress.Moved)
	ago := a.tr("刚刚同步")
	if span >= 24*time.Hour {
		ago = a.tr("%d天前同步", int(span.Hours()/24))
	} else if span >= time.Hour {
		ago = a.tr("%d小时前同步", int(span.Hours()))
	} else if span >= time.Minute {
		ago = a.tr("%d分钟前同步", int(span.Minutes()))
	}
	if r.Ended.IsZero() {
		ago = a.tr(string(r.State))
	}
	return a.tr("%s · %d个文件", ago, files)
}
