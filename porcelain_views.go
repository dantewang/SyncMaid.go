package main

import (
	"fmt"
	"strings"
	"syncmaid/internal/filefilter"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
	"time"

	"github.com/egoist/mygo/ui"
)

type uiScreen uint8

const (
	screenTasks uiScreen = iota
	screenTopology
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
	case a.topologyOpen:
		return screenTopology
	case a.historyOpen:
		return screenHistory
	default:
		return screenTasks
	}
}

func (a *application) manualView(c *ui.Context) {
	c.SetTheme(&porcelain)
	e2eConfigureView(c)
	width, height := c.Size()
	s := &a.manual
	screen := a.activeScreen()
	modalLocked := screen == screenMirrorConfirm || screen == screenImport || screen == screenRegex

	ui.Row(c).Fill().Gap(0).Children(func() {
		// Level 1: Slim Global Icon Rail
		ui.Column(c).Width(64).FillHeight().Background(ribbonRailBg).Gap(0).Children(func() {
			if maidAvatar != nil {
				ui.Column(c).FillWidth().AlignItems(ui.Center).Padding(10, 0, 8, 0).Children(func() {
					ui.Image(c, maidAvatar).Size(28, 28)
				})
				ui.Box(c).Height(1).FillWidth().Background(porcelain.Border)
			}
			railBtn := func(target uiScreen, glyph, label string, click func()) {
				active := screen == target
				btn := ui.ButtonBase(c).Label(a.tr(label)).FillWidth().Height(54).Padding(6, 2).Disabled(modalLocked || a.manual.busy())
				fg := porcelain.TextMuted
				bg := ui.Transparent
				if active {
					fg = porcelain.Text
					bg = porcelain.Surface
				} else if btn.Hovered() {
					fg = porcelain.Text
					bg = porcelain.SurfaceHover
				}
				btn = btn.Background(bg).TextColor(fg)
				if active {
					btn = paintRibbonStripe(btn)
				}
				btn.Children(func() {
					ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(3).Children(func() {
						ui.Icon(c, porcelainGlyphs[glyph]).Size(16, 16)
						ui.Text(c, a.tr(label)).FontSize(12).SingleLine()
					})
				}).OnClick(click)
			}
			railBtn(screenTasks, "□", "任务", func() { a.settingsOpen = false; a.topologyOpen = false; a.historyOpen = false })
			railBtn(screenTopology, "move", "拓扑", func() { a.topologyOpen = true; a.settingsOpen = false; a.historyOpen = false })
			railBtn(screenHistory, "clock", "历史", func() { a.historyOpen = true; a.settingsOpen = false; a.topologyOpen = false })
			ui.Box(c).Grow(1)
			ui.Box(c).Height(1).FillWidth().Background(porcelain.Border)
			railBtn(screenSettings, "⚙", "设置", func() { a.settingsOpen = !a.settingsOpen; a.historyOpen = false; a.topologyOpen = false })
		})
		ui.Box(c).Width(1).FillHeight().Background(porcelain.Border)

		// Main Stage
		ui.Column(c).Grow(1).FillHeight().Gap(0).Children(func() {
			if s.phase == manualRunning || s.phase == manualChecking || s.phase == manualStopping {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Padding(8, 16).Background(ribbonBlueSoft).BorderWidth(0, 0, 1, 0).BorderColor(porcelain.Border).Children(func() {
					activity := a.render(a.watchActivity)
					if s.phase == manualStopping {
						activity = a.render(s.message)
					}
					ui.Text(c, activity).FontSize(12).Grow(1)
					if a.batch.phase == batchServing {
						ui.Text(c, a.tr("批次余下 %d 项", len(a.batch.pending))).FontSize(12).TextColor(porcelain.TextMuted)
					}
					ribbonDangerButton(c, a.tr("停止")).Padding(3, 10).FontSize(12).Disabled(s.phase == manualStopping).OnClick(a.stopManual)
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
			case screenTopology:
				a.topologyView(c)
				return
			case screenHistory:
				a.historyViewContent(c)
				return
			}
			a.tasksMasterDetailView(c, width)
		})
	})

	if a.cards.editor && screen == screenTasks {
		sheetWidth := min(float32(520), width-48)
		sheetHeight := min(float32(560), height-48)
		if a.cards.destinations {
			sheetWidth = min(float32(840), width-48)
			sheetHeight = min(float32(620), height-48)
		}
		ui.Overlay(c, func() {
			back := ui.Box(c).Absolute().Left(0).Top(0).Size(width, height).Background(ui.Color{R: 25, G: 28, B: 34, A: 84}).Modal()
			if back.OverlayShortcut(0, ui.KeyEscape) {
				a.closeTaskEditor()
			}
			ui.Column(c).Absolute().Left((width-sheetWidth)/2).Top((height-sheetHeight)/2).Width(sheetWidth).Height(sheetHeight).LineHeight(1.25).Background(porcelain.Surface).Border(1, porcelain.Text).Gap(0).Padding(14, 18).Children(func() {
				a.taskEditorView(c, sheetWidth)
			})
		})
	}
}

func (a *application) tasksMasterDetailView(c *ui.Context, width float32) {
	listW := float32(224)
	if width < 860 {
		listW = 192
	}

	selectedID := a.manual.id
	if selectedID == "" && a.tasks != nil && len(a.tasks.Tasks) > 0 {
		selectedID = a.tasks.Tasks[0].ID
	}

	ui.Row(c).Grow(1).FillWidth().Gap(0).Children(func() {
		// Level 2 Left: Task List Column
		ui.Column(c).Width(listW).FillHeight().Background(ribbonSidebarBg).Gap(0).Children(func() {
			ui.Column(c).FillWidth().Padding(10).Gap(6).Children(func() {
				ribbonInkButton(c, "+ "+a.tr("新建任务")).FillWidth().Height(32).Disabled(!a.cardActionAllowed()).OnClick(func() {
					a.newManual()
					a.cards.editor = true
					a.cards.destinations = false
				})
				ui.Row(c).FillWidth().Gap(6).Children(func() {
					porcelainButton(c, a.tr("全部运行")).Grow(1).Padding(4, 6).FontSize(12).Disabled(!a.cardActionAllowed()).Tooltip(a.tr("按已保存顺序串行运行；停止会丢弃余下任务。")).OnClick(a.startBatch)
					if a.watchPhase == watchesPaused {
						porcelainButton(c, a.tr("恢复自动")).Grow(1).Padding(4, 6).FontSize(12).Disabled(!a.cardActionAllowed()).OnClick(a.resumeWatches)
					} else {
						porcelainButton(c, a.tr("暂停自动")).Grow(1).Padding(4, 6).FontSize(12).Disabled(a.watchPhase != watchesServing).OnClick(func() { a.stopWatches(watchesPaused) })
					}
				})
			})
			ui.Box(c).Height(1).FillWidth().Background(porcelain.Border)
			ui.Scroll(c).Grow(1).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Gap(0).Children(func() {
					if a.tasks == nil {
						return
					}
					for _, t := range a.tasks.Tasks {
						id := t.ID
						active := id == selectedID
						row := ui.ButtonBase(c).Label(t.Name).Tooltip(t.Name).Justify(ui.Start).FillWidth().Padding(10, 12).BorderWidth(0, 0, 1, 0).BorderColor(porcelain.Border)
						if active {
							row = paintRibbonStripe(row.Background(porcelain.Surface))
						} else if row.Hovered() {
							row = row.Background(porcelain.SurfaceHover)
						}
						_, health := a.taskHealth(t)
						kindTag := "SYNC"
						kindColor := ribbonBlue
						if t.Kind == taskconfig.Moving {
							kindTag = "MOVE"
							kindColor = ribbonMove
						}
						triggerLabel := a.taskTriggerText(t)
						row.Children(func() {
							ui.Column(c).FillWidth().Gap(4).Children(func() {
								ui.Row(c).FillWidth().AlignItems(ui.Baseline).Gap(6).Children(func() {
									name := ui.Text(c, t.Name).FontSize(14).SingleLine().Grow(1).Shrink(1).MinWidth(0).Ellipsis("…")
									if active {
										name.FontWeight(600)
									}
									ui.Text(c, kindTag).Font("Consolas").FontSize(12).TextColor(kindColor)
								})
								porcelainPath(c, t.SourcePath)
								ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
									ui.Box(c).Size(6, 6).Background(health)
									ui.Text(c, triggerLabel).FontSize(12).TextColor(porcelain.TextMuted).Grow(1).SingleLine().Ellipsis("…")
									ui.Text(c, fmt.Sprintf("%d %s", len(t.Destinations), a.tr("目标"))).FontSize(12).TextColor(porcelain.TextMuted)
								})
							})
						}).Disabled(!a.cardActionAllowed()).OnClick(func() {
							a.selectManual(id)
						})
					}
				})
			})
		})
		ui.Box(c).Width(1).FillHeight().Background(porcelain.Border)

		// Level 2 Right: Selected Task Detail + Collapsible Source Preview
		ui.Column(c).Grow(1).FillHeight().Gap(0).Children(func() {
			a.taskStatusBanners(c)
			if a.tasks == nil || len(a.tasks.Tasks) == 0 {
				ui.Column(c).Grow(1).FillWidth().AlignItems(ui.Center).Justify(ui.Center).Gap(10).Padding(24).Children(func() {
					if maidAvatar != nil {
						ui.Image(c, maidAvatar).Size(48, 48)
					}
					ui.Text(c, a.tr("暂无同步任务")).FontSize(15).TextColor(porcelain.TextMuted)
					ribbonInkButton(c, "+ "+a.tr("新建任务")).Disabled(!a.cardActionAllowed()).OnClick(func() {
						a.newManual()
						a.cards.editor = true
						a.cards.destinations = false
					})
				})
				return
			}
			task, err := a.tasks.Find(selectedID)
			if err != nil {
				task = a.tasks.Tasks[0]
			}
			a.selectedTaskDetailView(c, task, width)
		})
	})
}

func (a *application) taskStatusBanners(c *ui.Context) {
	if a.taskError != nil {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Padding(8, 16).Background(ribbonWarnSoft).BorderWidth(0, 0, 1, 0).BorderColor(porcelain.Border).Children(func() {
			ui.Text(c, a.tr("任务文件不可用，已阻止保存与运行: ")+a.taskError.Error()).FontSize(13).TextColor(porcelain.Danger).Grow(1)
			if backup, ok := a.taskRecovery(); ok {
				porcelainButton(c, a.tr("恢复任务备份")).Disabled(!a.taskRecoveryAllowed()).OnClick(func() { a.confirmTaskRecovery(backup) })
			}
		})
	}
	if a.recoveryNotice.key != "" {
		ui.Text(c, a.render(a.recoveryNotice)).FontSize(13).Padding(6, 16)
	}
	if a.importError != nil {
		ui.Text(c, a.tr("导入拒绝，旧文件和任务库未修改：%s", a.importError.Error())).FontSize(13).TextColor(porcelain.Danger).Padding(6, 16)
	}
	if a.historyError != nil {
		ui.Text(c, a.tr("运行记录不可用或未保存；文件执行结果仍有效：")+a.historyError.Error()).FontSize(13).TextColor(porcelain.Danger).Padding(6, 16)
	}
	if a.watchPhase == watchesPaused {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Padding(6, 16).Background(ribbonWarnSoft).BorderWidth(0, 0, 1, 0).BorderColor(porcelain.Border).Children(func() {
			ui.Text(c, a.tr("自动触发已暂停")).FontSize(13).Grow(1)
			porcelainButton(c, a.tr("恢复自动触发")).Padding(3, 10).FontSize(12).Disabled(!a.cardActionAllowed()).OnClick(a.resumeWatches)
		})
	}
}

func (a *application) taskTriggerText(t taskconfig.Task) string {
	switch t.Trigger.Kind {
	case "watch":
		return a.tr("监视")
	case "scheduled":
		if t.Trigger.CronExpression != nil {
			return a.tr("定时（%s）", *t.Trigger.CronExpression)
		}
		return a.tr("定时")
	default:
		return a.tr("手动")
	}
}

func hasLegacyRegexRules(t taskconfig.Task) bool {
	for _, d := range t.Destinations {
		specs, err := filefilter.Proposals(t, d.ID)
		if err == nil && len(specs) > 0 {
			return true
		}
	}
	return false
}

func (a *application) selectedTaskDetailView(c *ui.Context, t taskconfig.Task, width float32) {
	id := t.ID
	now := time.Now()
	running := id == a.cards.running && (a.manual.phase == manualRunning || a.manual.phase == manualChecking || a.manual.phase == manualStopping)
	status, healthColor := a.taskHealth(t)
	result := a.results[id]
	kindTag := "SYNC"
	kindColor := ribbonBlue
	if t.Kind == taskconfig.Moving {
		kindTag = "MOVE"
		kindColor = ribbonMove
	}

	centerCol := func() {
		// Task Header
		ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(12).Padding(12, 16).Background(porcelain.Surface).Children(func() {
			ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(4).Children(func() {
				ui.Row(c).FillWidth().Wrap().AlignItems(ui.Baseline).Gap(10).Children(func() {
					ui.Text(c, t.Name).FontSize(18).FontWeight(600).SingleLine().Ellipsis("…").Tooltip(t.Name)
					ui.Text(c, kindTag).Font("Consolas").FontSize(12).TextColor(kindColor)
					if parent, hasParent := a.findCascadeParent(t); hasParent {
						parentID := parent.ID
						ui.ButtonBase(c).Role(ui.RoleLink).Label("↳ "+a.tr("接力自 %s", parent.Name)).Background(ui.Transparent).Children(func() {
							ui.Text(c, "↳ "+a.tr("接力自 %s", parent.Name)).FontSize(12).TextColor(ribbonBlue)
						}).Disabled(!a.cardActionAllowed()).OnClick(func() { a.selectManual(parentID) })
					}
				})
				porcelainPath(c, t.SourcePath)
				ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Text(c, a.taskTriggerText(t)).FontSize(12).TextColor(porcelain.TextMuted)
					ui.Text(c, "·").FontSize(12).TextColor(porcelain.TextMuted)
					statusColor := porcelain.TextMuted
					if healthColor == porcelain.Danger || healthColor == porcelain.Warning {
						statusColor = healthColor
					} else if healthColor == porcelain.Text {
						statusColor = porcelain.Success
					}
					ui.Text(c, status).FontSize(12).TextColor(statusColor)
					if w := a.watches[id]; w != nil && w.ticket.phase == watchFailed {
						ui.Text(c, "· "+a.tr("触发器异常")).FontSize(12).TextColor(porcelain.Warning).Tooltip(a.render(w.status))
					}
				})
			})

			ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(6).Children(func() {
				porcelainButton(c, a.tr("编辑任务")).Height(32).Disabled(!a.cardActionAllowed()).OnClick(func() { a.editCard(id, "") })
				previewBtn := porcelainButton(c, a.tr("源预览")).Height(32)
				if a.sourcePreview.open {
					previewBtn = previewBtn.Background(ribbonBlueSoft).Border(1, ribbonBlue).TextColor(ribbonBlue)
				}
				previewBtn.Disabled(!a.cardActionAllowed() && !a.sourcePreview.open).OnClick(func() {
					if a.manual.id != id && a.cardActionAllowed() {
						a.selectManual(id)
					}
					a.sourcePreview.open = !a.sourcePreview.open
				})
				if running {
					ribbonDangerButton(c, "■ "+a.tr("停止")).Height(32).OnClick(a.stopManual)
				} else {
					porcelainPrimaryButton(c, "▶ "+a.tr("运行")).Height(32).Disabled(!a.cardActionAllowed() || len(t.Destinations) == 0).OnClick(func() { a.runCard(id) })
				}
			})
		})
		ui.Box(c).Height(1).FillWidth().Background(porcelain.Border)

		// Unconfirmed regex / request issue strip
		if _, reqErr := a.tasks.Request(t); reqErr != nil {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Padding(8, 16).Background(ribbonWarnSoft).BorderWidth(0, 0, 1, 0).BorderColor(porcelain.Border).DrawOver(func(p *ui.Painter, r ui.Rect) {
				p.Fill(ui.Rect{X: r.X, Y: r.Y, W: 3, H: r.H}, porcelain.Warning, 0)
			}).Children(func() {
				ui.Text(c, a.tr("此任务不可运行或编辑: ")+reqErr.Error()).FontSize(12).TextColor(porcelain.Warning).Grow(1)
				if hasLegacyRegexRules(t) {
					porcelainButton(c, a.tr("旧正则迁移")).Padding(3, 10).FontSize(12).Disabled(!a.cardActionAllowed()).OnClick(func() {
						a.selectManual(id)
						a.beginRegexMigration()
					})
				}
			})
		}

		ui.Scroll(c).Grow(1).Shrink(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Padding(14, 16).Gap(8).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Text(c, fmt.Sprintf("%s (%d)", a.tr("目标"), len(t.Destinations))).FontSize(13).FontWeight(600).TextColor(porcelain.TextMuted).Grow(1)
					porcelainButton(c, "+ "+a.tr("添加目标")).Disabled(!a.cardActionAllowed()).OnClick(func() { a.openAddDestination(id) })
				})
				if len(t.Destinations) == 0 {
					ui.Column(c).FillWidth().Padding(28, 16).AlignItems(ui.Center).Gap(10).Background(porcelain.Surface).Border(1, porcelain.Border).Children(func() {
						if maidAvatar != nil {
							ui.Image(c, maidAvatar).Size(36, 36)
						}
						ui.Text(c, a.tr("尚未添加目标")).FontSize(14).TextColor(porcelain.TextMuted)
						ribbonInkButton(c, "+ "+a.tr("添加目标")).Disabled(!a.cardActionAllowed()).OnClick(func() { a.openAddDestination(id) })
					})
					return
				}
				for i, d := range t.Destinations {
					a.destinationRowCard(c, t, d, i, running, result, now)
				}
			})
		})
	}

	if a.sourcePreview.open {
		if width >= 920 {
			ui.Row(c).Grow(1).FillWidth().Gap(0).Children(func() {
				ui.Column(c).Grow(1).Shrink(1).MinWidth(0).FillHeight().Children(centerCol)
				ui.Box(c).Width(1).FillHeight().Background(porcelain.Border)
				ui.Box(c).Width(296).FillHeight().Children(func() { a.sourcePreviewTaskPanel(c) })
			})
			return
		}
		ui.Column(c).Grow(1).FillWidth().Gap(0).Children(func() {
			ui.Column(c).Grow(1).FillWidth().Children(centerCol)
			ui.Box(c).Height(1).FillWidth().Background(porcelain.Border)
			ui.Box(c).Height(230).FillWidth().Children(func() { a.sourcePreviewTaskPanel(c) })
		})
		return
	}
	centerCol()
}

func (a *application) destinationRowCard(c *ui.Context, t taskconfig.Task, d taskconfig.Destination, idx int, running bool, result savedTaskResult, now time.Time) {
	id := t.ID
	did := d.ID
	r, ok := result.destinations[did]
	if running && a.manual.id == id && !a.manual.dirty {
		r, ok = a.manual.destinationResults[did]
	}
	mark := destinationHealthColor(r, ok)
	if ok && r.State == syncengine.DestinationSkipped && result.outcome == "failed" {
		mark = porcelain.Danger
	}

	strategyLabel := "ADD-ONLY"
	strategyColor := ribbonBlue
	stripeColor := ribbonBlue
	if t.Kind == taskconfig.Moving {
		strategyLabel = "MOVE"
		strategyColor = ribbonMove
		stripeColor = ribbonMove
	} else if d.Strategy == taskconfig.Mirror {
		strategyLabel = "MIRROR"
		strategyColor = porcelain.Warning
		stripeColor = porcelain.Warning
	}

	modeText := a.tr("只增不删")
	if t.Kind == taskconfig.Moving {
		dirMode := "保留目录结构"
		if d.FlattenStructure {
			dirMode = "平铺到目标根目录"
		}
		colMode := "保留源"
		if d.CollisionPolicy == taskconfig.Suffix {
			colMode = "加编号"
		}
		modeText = a.tr(dirMode) + " · " + a.tr(colMode)
	} else if d.Strategy == taskconfig.Mirror {
		delMode := "回收站"
		if d.DeleteMode == taskconfig.Permanent {
			delMode = "永久删除"
		}
		modeText = a.tr(delMode)
	}

	metaParts := []string{a.filterSummary(d.Filters), modeText}
	if a.sourcePreview.phase == previewReady && a.sourcePreview.result != nil {
		if sel, exists := a.sourcePreview.result.Targets[did]; exists {
			metaParts = append(metaParts, a.tr("命中 %d", sel.Count))
		}
	}

	isOwner := a.sourcePreview.selectedPath != "" && a.sourcePreview.selectedOwners[did]
	interceptedBy := 0
	if a.sourcePreview.selectedPath != "" {
		interceptedBy = a.sourcePreview.selectedIntercepted[did]
	}

	cardBg := porcelain.Surface
	cardBorder := porcelain.Border
	if isOwner {
		cardBg = ribbonBlueSoft
		cardBorder = ribbonBlue
	} else if interceptedBy > 0 {
		cardBg = ribbonWarnSoft
		cardBorder = porcelain.Warning
	}

	card := ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(10).Padding(12, 14).Background(cardBg).Border(1, cardBorder)
	if t.Kind != taskconfig.Moving {
		card = card.DrawOver(func(p *ui.Painter, rc ui.Rect) {
			p.Fill(ui.Rect{X: rc.X, Y: rc.Y, W: 3, H: rc.H}, stripeColor, 0)
		})
	}
	card.Children(func() {
		if t.Kind == taskconfig.Moving {
			ui.Column(c).Width(24).AlignItems(ui.Center).Padding(2, 0).Children(func() {
				ui.Text(c, fmt.Sprintf("%02d", idx+1)).Font("Consolas").FontSize(14).FontWeight(700)
			})
		}
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(4).Children(func() {
			ui.Row(c).FillWidth().Wrap().AlignItems(ui.Baseline).Gap(8).Children(func() {
				ui.Text(c, d.Name).FontSize(14).FontWeight(600).SingleLine().Ellipsis("…").Tooltip(d.Name)
				ui.Text(c, strategyLabel).Font("Consolas").FontSize(12).TextColor(strategyColor)
				if isOwner {
					ui.Text(c, "● "+a.tr("命中")).FontSize(12).FontWeight(600).TextColor(ribbonBlue)
				} else if interceptedBy > 0 {
					ui.Text(c, a.tr("已被 %02d 拦截", interceptedBy)).FontSize(12).TextColor(porcelain.Warning)
				}
				children := a.findCascadeChildren(t, d)
				if len(children) > 0 {
					childID := children[0].ID
					ui.ButtonBase(c).Role(ui.RoleLink).Label("↳ "+a.tr("级联至 %s", children[0].Name)).Background(ui.Transparent).Children(func() {
						ui.Text(c, "↳ "+a.tr("级联至 %s", children[0].Name)).FontSize(12).TextColor(ribbonBlue)
					}).Disabled(!a.cardActionAllowed()).OnClick(func() { a.selectManual(childID) })
				}
			})
			porcelainPath(c, d.Target.Path)
			ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, strings.Join(metaParts, "  |  ")).FontSize(12).TextColor(porcelain.TextMuted).Tooltip(a.ruleSummary(d.Filters))
				ui.Text(c, "·").FontSize(12).TextColor(porcelain.TextMuted)
				summary := a.destinationStatus(r, ok, now)
				ui.ButtonBase(c).Role(ui.RoleLink).Label(summary).Background(ui.Transparent).Tooltip(summary).Children(func() {
					ui.Text(c, summary).FontSize(12).SingleLine().Ellipsis("…").Underline().TextColor(mark)
				}).OnClick(func() { a.openDestinationLog(id, did) })
			})
		})

		ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(6).Children(func() {
			if t.Kind == taskconfig.Moving {
				canUp := idx > 0 && !syncengine.IsAllFileRules(d.Filters) && !syncengine.IsAllFileRules(t.Destinations[idx-1].Filters)
				canDown := idx+1 < len(t.Destinations) && !syncengine.IsAllFileRules(d.Filters) && !syncengine.IsAllFileRules(t.Destinations[idx+1].Filters)
				porcelainCompactButton(c, "↑").Padding(0, 8).Tooltip(a.tr("上移")).Disabled(!a.cardActionAllowed() || !canUp).OnClick(func() { a.reorderSavedDestination(id, did, -1) })
				porcelainCompactButton(c, "↓").Padding(0, 8).Tooltip(a.tr("下移")).Disabled(!a.cardActionAllowed() || !canDown).OnClick(func() { a.reorderSavedDestination(id, did, 1) })
			}
			porcelainCompactButton(c, a.tr("编辑")).Tooltip(a.tr("编辑目标")+" "+d.Name).Disabled(!a.cardActionAllowed()).OnClick(func() { a.editCard(id, did) })
			porcelainCompactButton(c, a.tr("删除")).Tooltip(a.tr("移除目标（仅配置）")+" "+d.Name).Disabled(!a.cardActionAllowed()).OnClick(func() { a.deleteSavedDestination(id, did) })
		})
	})
}
