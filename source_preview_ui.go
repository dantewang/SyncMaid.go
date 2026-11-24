package main

import (
	"context"
	"fmt"
	"strings"
	"syncmaid/internal/appcore"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"

	"github.com/egoist/mygo/ui"
)

type sourcePreviewPhase uint8

const (
	previewIdle sourcePreviewPhase = iota
	previewScanning
	previewStopping
	previewStale
	previewReady
	previewFailed
)

type sourcePreviewJob struct {
	cancel     context.CancelFunc
	generation uint64
}

func (a *application) manualEdited() {
	a.sourcePreview.invalidate()
	a.refreshPreviewFromSnapshot()
	a.pauseEditingWatch(a.manual.id)
}

// Owned by the native UI thread. The worker owns only its cloned request.
type sourcePreviewSession struct {
	phase               sourcePreviewPhase
	generation          uint64
	job                 *sourcePreviewJob
	result              *appcore.RoutePreview
	err                 error
	cachedSnapshot      *appcore.SourceSnapshot
	searchQuery         string
	selectedPath        string
	selectedOwners      map[taskconfig.ID]bool
	selectedIntercepted map[taskconfig.ID]int
	open                bool
}

func (s *sourcePreviewSession) invalidate() {
	wasIdle := s.phase == previewIdle
	s.generation++
	s.result = nil
	s.err = nil
	if s.job != nil {
		s.job.cancel()
		s.phase = previewStopping
	} else if !wasIdle {
		s.phase = previewStale
	}
}

func (s *sourcePreviewSession) resetSnapshot() {
	s.cachedSnapshot = nil
	s.searchQuery = ""
	s.selectedPath = ""
	s.selectedOwners = nil
	s.selectedIntercepted = nil
	s.invalidate()
}

func (a *application) selectPreviewPath(fp string) {
	p := &a.sourcePreview
	if p.selectedPath == fp {
		p.selectedPath = ""
		p.selectedOwners = nil
		p.selectedIntercepted = nil
		return
	}
	p.selectedPath = fp
	p.selectedOwners = map[taskconfig.ID]bool{}
	p.selectedIntercepted = map[taskconfig.ID]int{}
	q, err := a.previewDraft()
	if err != nil {
		return
	}
	mini := &appcore.SourceSnapshot{
		Source: q.Source,
		Files:  []appcore.PreviewFile{{Path: fp}},
	}
	res, err := appcore.EvaluateSourceSnapshot(context.Background(), mini, q)
	if err != nil {
		return
	}
	moving := syncengine.ManualKind(q.Kind) == taskconfig.Moving
	winnerIdx := 0
	for i, d := range q.Destinations {
		if res.Targets[d.ID].Count > 0 {
			p.selectedOwners[d.ID] = true
			if winnerIdx == 0 {
				winnerIdx = i + 1
			}
		}
	}
	if moving && winnerIdx > 0 {
		// Re-evaluate individually for destinations after winner to detect intercepted Move rules.
		for i := winnerIdx; i < len(q.Destinations); i++ {
			d := q.Destinations[i]
			single := syncengine.ManualTaskRequest{
				Kind:         taskconfig.Sync,
				Source:       q.Source,
				Destinations: []syncengine.ManualDestination{d},
			}
			_ = fileinventory.File{}
			sr, err := appcore.EvaluateSourceSnapshot(context.Background(), mini, single)
			if err == nil && sr.Targets[d.ID].Count > 0 {
				p.selectedIntercepted[d.ID] = winnerIdx
			}
		}
	}
}

func (s *sourcePreviewSession) finish(job *sourcePreviewJob, result *appcore.RoutePreview, err error) {
	if s.job != job {
		return
	}
	job.cancel()
	s.job = nil
	if job.generation != s.generation {
		s.phase = previewStale
		return
	}
	s.result = result
	s.err = err
	s.phase = previewReady
	if result != nil && err == nil {
		s.cachedSnapshot = result.Snapshot
	}
	if err != nil {
		s.result = nil
		s.cachedSnapshot = nil
		s.phase = previewFailed
	}
}

func (a *application) refreshPreviewFromSnapshot() {
	p := &a.sourcePreview
	if p.cachedSnapshot == nil || p.job != nil || p.cachedSnapshot.Source != a.manual.source {
		return
	}
	q, err := a.previewDraft()
	if err != nil {
		return
	}
	res, err := appcore.EvaluateSourceSnapshot(context.Background(), p.cachedSnapshot, q)
	if err != nil {
		return
	}
	p.result = res
	p.err = nil
	p.phase = previewReady
}

func (a *application) previewDraft() (syncengine.ManualTaskRequest, error) {
	s := &a.manual
	if a.tasks != nil && s.id != "" {
		if err := a.selectedTaskIssue(); err != nil {
			return syncengine.ManualTaskRequest{}, err
		}
	}
	s.flushTarget()
	s.keepFilterDraft()
	if err := s.filterDraftError(); err != nil {
		return syncengine.ManualTaskRequest{}, err
	}
	q := s.draftRequest()
	for i := range q.Destinations {
		d := &q.Destinations[i]
		rs := make([]taskconfig.Rule, len(d.Filters))
		for j, r := range d.Filters {
			rs[j] = r.Clone()
		}
		d.Filters = rs
		d.Matcher = nil
		d.FilterError = nil
	}
	// Editing invalidates whole-task confirmations. A preview cannot renew them.
	if !s.dirty && samePreviewRequest(q, s.request) {
		q = q.WithPreservedRegex(s.request, s.id, s.name)
	}
	return q, nil
}

func samePreviewRequest(a, b syncengine.ManualTaskRequest) bool {
	if a.Source != b.Source || syncengine.ManualKind(a.Kind) != syncengine.ManualKind(b.Kind) || !reflectEqualTrigger(a.Trigger, b.Trigger) || len(a.Destinations) != len(b.Destinations) {
		return false
	}
	for i, d := range a.Destinations {
		o := b.Destinations[i]
		if d.ID != o.ID || d.Name != o.Name || d.Path != o.Path || d.Strategy != o.Strategy || d.DeleteMode != o.DeleteMode || d.Flatten != o.Flatten || d.Collision != o.Collision || !syncengine.SameFilterRules(d.Filters, o.Filters) {
			return false
		}
	}
	return true
}

func reflectEqualTrigger(a, b taskconfig.Trigger) bool {
	if a.Kind != b.Kind {
		return false
	}
	if (a.CronExpression == nil) != (b.CronExpression == nil) {
		return false
	}
	if a.CronExpression != nil && *a.CronExpression != *b.CronExpression {
		return false
	}
	if (a.SettleSeconds == nil) != (b.SettleSeconds == nil) {
		return false
	}
	if a.SettleSeconds != nil && *a.SettleSeconds != *b.SettleSeconds {
		return false
	}
	return true
}

func (a *application) startSourcePreview() {
	if a.manual.busy() || a.manual.quitPending || a.batch.phase != batchIdle || a.tasks == nil || a.taskError != nil || a.sourcePreview.job != nil {
		return
	}
	s := &a.sourcePreview
	s.invalidate()
	q, err := a.previewDraft()
	if err != nil {
		s.phase = previewFailed
		s.err = err
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &sourcePreviewJob{cancel: cancel, generation: s.generation}
	s.job = job
	s.phase = previewScanning
	go func() {
		result, err := appcore.ScanSourcePreview(ctx, q)
		a.window.Update(func() {
			s.finish(job, result, err)
			a.finishAsyncStep(true)
		})
	}()
}

func (a *application) addPreviewExtension(group int, extension string) {
	p := &a.sourcePreview
	e := &a.manual.filterEditor
	if p.phase != previewReady || p.result == nil || !a.targetEditAllowed() || a.selectedMirror() || e.Mode != appcore.FilterGrouped || group < 0 || group >= len(e.Groups) || len(e.Groups[group].Rows) >= 128 {
		return
	}
	found := false
	for _, x := range p.result.Extensions {
		if x.Text == extension {
			found = true
			break
		}
	}
	if !found {
		return
	}
	e.Groups[group].Rows = append(e.Groups[group].Rows, taskconfig.ExtensionFilter(extension))
	a.filterEdited()
}

func (a *application) previewExtensionSelected(group int, extension string) bool {
	e := &a.manual.filterEditor
	if e.Mode != appcore.FilterGrouped || group < 0 || group >= len(e.Groups) {
		return false
	}
	for _, r := range e.Groups[group].Rows {
		if r.Kind == "extension" && r.Extension != nil && strings.EqualFold(*r.Extension, extension) {
			return true
		}
	}
	return false
}

func (a *application) togglePreviewExtension(group int, extension string) {
	p := &a.sourcePreview
	e := &a.manual.filterEditor
	if p.phase != previewReady || p.result == nil || !a.targetEditAllowed() || a.selectedMirror() || e.Mode != appcore.FilterGrouped || group < 0 || group >= len(e.Groups) {
		return
	}
	rows := e.Groups[group].Rows
	for j, r := range rows {
		if r.Kind == "extension" && r.Extension != nil && strings.EqualFold(*r.Extension, extension) {
			e.Groups[group].Rows = append(rows[:j], rows[j+1:]...)
			a.filterEdited()
			return
		}
	}
	a.addPreviewExtension(group, extension)
}

func formatPreviewBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func (a *application) sourcePreviewStatusText(c *ui.Context) {
	p := &a.sourcePreview
	switch p.phase {
	case previewScanning:
		ui.Text(c, a.tr("正在读取源文件…")).FontSize(13).TextColor(porcelain.TextMuted)
	case previewStopping:
		ui.Text(c, a.tr("正在取消预览…")).FontSize(13).TextColor(porcelain.TextMuted)
	case previewStale:
		ui.Text(c, a.tr("预览已失效，请刷新。")).FontSize(13).TextColor(porcelain.TextMuted)
	case previewFailed:
		ui.Text(c, a.tr("预览失败: %s", p.err.Error())).FontSize(13).TextColor(porcelain.Danger)
	case previewReady:
		r := p.result
		if r.EmptyRoot {
			ui.Text(c, a.tr("源目录为空。")).FontSize(13)
		} else if r.SourceFiles == 0 {
			ui.Text(c, a.tr("源目录存在，但没有普通文件。")).FontSize(13)
		} else {
			ui.Text(c, a.tr("源文件: %d", r.SourceFiles)).FontSize(13).FontWeight(600)
			if r.TotalBytes > 0 || r.FolderCount > 0 {
				ui.Text(c, a.tr("%d 个文件夹 · %s", r.FolderCount, formatPreviewBytes(r.TotalBytes))).FontSize(12).TextColor(porcelain.TextMuted)
			}
		}
	}
}

func (a *application) sourcePreviewView(c *ui.Context) {
	p := &a.sourcePreview
	ui.Column(c).FillWidth().Gap(8).Padding(12, 0).Children(func() {
		ui.Row(c).Wrap().Gap(8).Children(func() {
			label := "源预览"
			if p.phase != previewIdle {
				label = "刷新预览"
			}
			porcelainButton(c, a.tr(label)).Disabled(a.manual.busy() || a.manual.quitPending || p.job != nil || a.taskError != nil).OnClick(a.startSourcePreview)
			if p.job != nil {
				porcelainButton(c, a.tr("取消预览")).Disabled(p.phase == previewStopping).OnClick(p.invalidate)
			}
		})
		a.sourcePreviewStatusText(c)
		if p.phase == previewReady && p.result != nil {
			r := p.result
			ui.Text(c, a.tr("未匹配: %d · %s", r.Unmatched.Count, strings.Join(r.Unmatched.Samples, ", "))).FontSize(12).MaxLines(3)
			for _, item := range r.Contested {
				names := make([]string, 0, len(item.Targets))
				for _, id := range item.Targets {
					for _, d := range a.manual.targets {
						if d.ID == id {
							names = append(names, d.Name)
							break
						}
					}
				}
				text := a.tr("多规则匹配: %s → %s", item.Path, strings.Join(names, " → "))
				ui.Text(c, text).FontSize(12).MaxLines(2).Tooltip(text)
			}
		}
	})
}

func (a *application) sourcePreviewTargetView(c *ui.Context, id taskconfig.ID) {
	if a.sourcePreview.phase != previewReady || a.sourcePreview.result == nil {
		return
	}
	r, ok := a.sourcePreview.result.Targets[id]
	if !ok {
		return
	}
	text := a.tr("预览匹配: %d · %s", r.Count, strings.Join(r.Samples, ", "))
	ui.Text(c, text).FontSize(12).TextColor(porcelain.TextMuted).MaxLines(2).Tooltip(text)
}

func (a *application) sourcePreviewTaskPanel(c *ui.Context) {
	p := &a.sourcePreview
	ui.Column(c).FillHeight().FillWidth().Background(ribbonSidebarBg).Gap(0).Children(func() {
		// Header
		ui.Column(c).FillWidth().Padding(10, 14).Gap(3).Background(ribbonSidebarBg).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, a.tr("源预览")).FontSize(13).FontWeight(600).Grow(1)
				if p.job != nil {
					porcelainCompactButton(c, a.tr("取消")).Disabled(p.phase == previewStopping).OnClick(p.invalidate)
				} else {
					btnLabel := a.tr("扫描")
					if p.phase != previewIdle {
						btnLabel = a.tr("刷新")
					}
					porcelainCompactButton(c, btnLabel).Disabled(a.manual.busy() || a.manual.quitPending || a.taskError != nil).OnClick(a.startSourcePreview)
				}
			})
			if p.phase == previewReady && p.result != nil && !p.result.EmptyRoot && p.result.SourceFiles > 0 {
				r := p.result
				ui.Text(c, a.tr("%d 文件 · %d 文件夹 · %s", r.SourceFiles, r.FolderCount, formatPreviewBytes(r.TotalBytes))).FontSize(13).FontWeight(600)
			} else if p.phase != previewIdle && p.phase != previewScanning && p.phase != previewStopping {
				a.sourcePreviewStatusText(c)
			}
			sub := a.tr("仅扫描源端")
			switch {
			case p.phase == previewIdle:
				sub = a.tr("仅扫描源端 · 未扫描")
			case p.phase == previewScanning || p.phase == previewStopping:
				sub = a.tr("仅扫描源端 · 扫描中")
			case p.phase == previewReady && p.result != nil && p.result.Snapshot != nil && !p.result.Snapshot.ScannedAt.IsZero():
				sub = a.tr("仅扫描源端 · %s 快照", p.result.Snapshot.ScannedAt.Local().Format("15:04"))
			}
			ui.Text(c, sub).FontSize(12).TextColor(porcelain.TextMuted)
		})
		ui.Box(c).Height(1).FillWidth().Background(porcelain.Border)

		if p.phase == previewIdle {
			ui.Column(c).Grow(1).FillWidth().AlignItems(ui.Center).Justify(ui.Center).Padding(16).Gap(8).Children(func() {
				ui.Text(c, a.tr("按需只读扫描源目录")).FontSize(12).TextColor(porcelain.TextMuted)
				ribbonInkButton(c, a.tr("扫描源目录")).Disabled(a.manual.busy() || a.manual.quitPending || a.taskError != nil).OnClick(a.startSourcePreview)
			})
			return
		}
		if p.phase != previewReady || p.result == nil {
			return
		}
		r := p.result

		// Instant Search Box
		if r.Snapshot != nil && r.SourceFiles > 0 {
			ui.Row(c).FillWidth().Padding(8, 12).Gap(6).Background(porcelain.Surface).Children(func() {
				ui.TextInput(c, &p.searchQuery).Height(28).Grow(1).FontSize(12).Label(a.tr("搜索源路径")).Placeholder(a.tr("筛选路径 / 扩展名…"))
				if strings.TrimSpace(p.searchQuery) != "" {
					porcelainCompactButton(c, a.tr("清除")).OnClick(func() {
						p.searchQuery = ""
						p.selectedPath = ""
						p.selectedOwners = nil
						p.selectedIntercepted = nil
					})
				}
			})
			ui.Box(c).Height(1).FillWidth().Background(porcelain.Border)
		}

		editingDest := a.cards.editor && a.cards.destinations && a.manual.targetID != ""
		canToggleExt := editingDest && !a.selectedMirror() && a.manual.filterEditor.Mode == appcore.FilterGrouped && len(a.manual.filterEditor.Groups) > 0 && a.targetEditAllowed()

		porcelainScroll(c, &a.scrolls.previewPanel, func() {
			ui.Column(c).FillWidth().Padding(0, 0, 8, 0).Gap(0).Children(func() {
				q := strings.TrimSpace(p.searchQuery)
				if q != "" && r.Snapshot != nil {
					matches, total := appcore.SearchSnapshotPaths(r.Snapshot, q, 50)
					var hits map[string]bool
					if editingDest {
						if qDraft, err := a.previewDraft(); err == nil {
							hits, _ = appcore.MatchPreviewPaths(context.Background(), matches, qDraft, a.manual.targetID)
						}
					}
					summary := a.tr("命中 %d 项", total)
					if total > len(matches) {
						summary = a.tr("仅显示前 %d / 共 %d 项", len(matches), total)
					}
					ui.Text(c, summary).FontSize(12).TextColor(porcelain.TextMuted).Padding(6, 12)
					if len(matches) == 0 {
						ui.Text(c, a.tr("无匹配文件")).FontSize(12).TextColor(porcelain.TextMuted).Padding(12)
						return
					}
					for _, f := range matches {
						fp := f.Path
						selected := p.selectedPath == fp
						if hits != nil {
							rowBg := ui.Transparent
							if selected {
								rowBg = ribbonBlueSoft
							}
							ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Padding(6, 12).Background(rowBg).BorderWidth(0, 0, 1, 0).BorderColor(porcelain.Border).Children(func() {
								ui.ButtonBase(c).Label(fp).Grow(1).Shrink(1).MinWidth(0).Justify(ui.Start).Background(ui.Transparent).Children(func() {
									ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(2).Children(func() {
										ui.Text(c, fp).Font("Consolas").FontSize(12).SingleLine().Ellipsis("…").Tooltip(fp)
										ui.Text(c, formatPreviewBytes(f.Size)).FontSize(12).TextColor(porcelain.TextMuted)
									})
								}).OnClick(func() {
									a.selectPreviewPath(fp)
								})
								if hits[fp] {
									ui.Text(c, a.tr("✓ 命中")).FontSize(12).FontWeight(600).TextColor(ribbonBlue).Padding(1, 5).Background(ribbonBlueSoft).Border(1, ribbonBlue).Shrink(0)
								} else {
									ui.Text(c, a.tr("✕ 未命中")).FontSize(12).TextColor(porcelain.TextMuted).Padding(1, 5).Background(porcelain.Surface).Border(1, porcelain.Border).Shrink(0)
								}
							})
							continue
						}
						row := ui.ButtonBase(c).Label(fp).FillWidth().Justify(ui.Start).Padding(6, 12).BorderWidth(0, 0, 1, 0).BorderColor(porcelain.Border)
						if selected {
							row = row.Background(ribbonBlueSoft)
						}
						row.Children(func() {
							ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(2).Children(func() {
								ui.Text(c, fp).Font("Consolas").FontSize(12).SingleLine().Ellipsis("…").Tooltip(fp)
								ui.Text(c, formatPreviewBytes(f.Size)).FontSize(12).TextColor(porcelain.TextMuted)
							})
						}).OnClick(func() {
							a.selectPreviewPath(fp)
						})
					}
					return
				}

				if editingDest {
					sel := r.Targets[a.manual.targetID]
					pct := 0.0
					if r.SourceFiles > 0 {
						pct = float64(sel.Count) * 100 / float64(r.SourceFiles)
					}
					destTitle := strings.TrimSpace(a.manual.targetName)
					if destTitle == "" {
						destTitle = a.tr("目标")
					}
					ui.Column(c).FillWidth().Background(ribbonBlueSoft).BorderWidth(0, 0, 1, 0).BorderColor(porcelain.Border).Padding(8, 12).Gap(3).Children(func() {
						ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
							ui.Text(c, a.tr("正在编辑：%s", destTitle)).FontSize(12).FontWeight(600).TextColor(ribbonBlue).Grow(1).Shrink(1).MinWidth(0).SingleLine().Ellipsis("…")
							ui.Text(c, fmt.Sprintf("%d (%.1f%%)", sel.Count, pct)).Font("Consolas").FontSize(12).FontWeight(600).TextColor(ribbonBlue).Shrink(0)
						})
						subHint := a.tr("当前为所有文件，点击下方扩展名可按扩展名筛选搜索")
						if canToggleExt {
							subHint = a.tr("点击下方扩展名可直接增删当前目标过滤规则")
						}
						ui.Text(c, subHint).FontSize(12).TextColor(porcelain.TextMuted)
					})

					if len(r.Extensions) > 0 {
						ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Padding(8, 12, 4, 12).Children(func() {
							ui.Text(c, a.tr("高频扩展名")).FontSize(12).FontWeight(600).Grow(1)
							if canToggleExt {
								ui.Text(c, a.tr("+ 点击直接加入规则")).FontSize(12).TextColor(ribbonBlue).Shrink(0)
							}
						})
						ui.Row(c).Wrap().Gap(6).Padding(4, 12, 10, 12).Children(func() {
							for _, ext := range r.Extensions {
								extText := ext.Text
								bg, border, fg := porcelain.Surface, porcelain.Border, porcelain.Text
								if canToggleExt && a.previewExtensionSelected(0, extText) {
									bg, border, fg = ribbonBlue, ribbonBlue, porcelain.AccentText
								}
								ui.ButtonBase(c).Label("."+extText).Padding(2, 6).Border(1, border).Background(bg).Children(func() {
									ui.Text(c, fmt.Sprintf(".%s %d", extText, ext.Count)).Font("Consolas").FontSize(12).TextColor(fg)
								}).OnClick(func() {
									if canToggleExt {
										a.togglePreviewExtension(0, extText)
									} else {
										p.searchQuery = "." + extText
									}
								})
							}
						})
					}

					ui.Row(c).FillWidth().Padding(6, 12).Background(ribbonRailBg).BorderWidth(1, 0, 1, 0).BorderColor(porcelain.Border).Children(func() {
						ui.Text(c, a.tr("按目标实时分桶统计")).FontSize(12).FontWeight(600).TextColor(porcelain.TextMuted)
					})
					a.renderPreviewBuckets(c, r, a.manual.targetID)
					return
				}

				allTargetsAll := len(a.manual.targets) > 0
				for _, d := range a.manual.targets {
					if !syncengine.IsAllFileRules(d.Filters) {
						allTargetsAll = false
						break
					}
				}

				if allTargetsAll && len(r.TopDirs) > 0 {
					ui.Text(c, a.tr("顶层目录分布")).FontSize(12).FontWeight(600).Padding(10, 12, 4, 12)
					for _, dir := range r.TopDirs {
						prefix := dir.Prefix
						label := prefix
						if prefix == "(root)" {
							label = a.tr("(根目录)")
						}
						ratio := float32(0)
						if r.TotalBytes > 0 {
							ratio = float32(dir.Bytes) / float32(r.TotalBytes)
						} else if r.SourceFiles > 0 {
							ratio = float32(dir.Files) / float32(r.SourceFiles)
						}
						btn := ui.ButtonBase(c).Label(label).FillWidth().Padding(6, 12).Background(ui.Transparent).Children(func() {
							ui.Column(c).FillWidth().Gap(4).Children(func() {
								ui.Row(c).FillWidth().Gap(6).Children(func() {
									ui.Text(c, label).Font("Consolas").FontSize(12).Grow(1).SingleLine().Ellipsis("…")
									ui.Text(c, fmt.Sprintf("%d · %s", dir.Files, formatPreviewBytes(dir.Bytes))).FontSize(12).TextColor(porcelain.TextMuted)
								})
								ui.Box(c).Height(3).FillWidth().Background(porcelain.Border).DrawOver(func(pt *ui.Painter, rc ui.Rect) {
									w := rc.W * ratio
									if w > 0 && w < 3 {
										w = 3
									}
									pt.Fill(ui.Rect{X: rc.X, Y: rc.Y, W: w, H: rc.H}, ribbonBlue, 0)
								})
							})
						})
						if prefix != "(root)" {
							btn.OnClick(func() { p.searchQuery = prefix })
						}
					}
					if len(r.Extensions) > 0 {
						ui.Box(c).Height(1).FillWidth().Background(porcelain.Border).Margin(6, 0)
						ui.Text(c, a.tr("高频扩展名")).FontSize(12).FontWeight(600).Padding(4, 12)
						ui.Row(c).Wrap().Gap(8).Padding(4, 12, 10, 12).Children(func() {
							for _, ext := range r.Extensions {
								extText := ext.Text
								ui.ButtonBase(c).Label("."+extText).Padding(2, 4).Background(ui.Transparent).Children(func() {
									ui.Text(c, fmt.Sprintf(".%s %d", extText, ext.Count)).Font("Consolas").FontSize(12).TextColor(porcelain.Text)
								}).OnClick(func() { p.searchQuery = "." + extText })
							}
						})
					}
					return
				}

				a.renderPreviewBuckets(c, r, "")
			})
		}).Grow(1).Shrink(1).MinHeight(0).FillWidth()
	})
}

func (a *application) renderPreviewBuckets(c *ui.Context, r *appcore.RoutePreview, activeTargetID taskconfig.ID) {
	for i, d := range a.manual.targets {
		sel := r.Targets[d.ID]
		pct := "0.0%"
		if r.SourceFiles > 0 {
			pct = fmt.Sprintf("%.1f%%", float64(sel.Count)*100/float64(r.SourceFiles))
		}
		title := d.Name
		if syncengine.ManualKind(a.manual.kind) == taskconfig.Moving {
			title = fmt.Sprintf("%02d %s", i+1, d.Name)
		}
		rowBg := porcelain.Surface
		if activeTargetID != "" && d.ID == activeTargetID {
			rowBg = ribbonBlueSoft
			title = "▸ " + title
		}
		ui.Row(c).FillWidth().Padding(7, 12).Background(rowBg).BorderWidth(0, 0, 1, 0).BorderColor(porcelain.Border).Children(func() {
			ui.Text(c, title).FontSize(12).FontWeight(600).Grow(1).SingleLine().Ellipsis("…")
			ui.Text(c, fmt.Sprintf("%d (%s)", sel.Count, pct)).FontSize(12).TextColor(porcelain.TextMuted)
		})
		for _, sp := range sel.Samples {
			samplePath := sp
			ui.ButtonBase(c).Label(samplePath).FillWidth().Justify(ui.Start).Padding(5, 12).BorderWidth(0, 0, 1, 0).BorderColor(porcelain.Border).Children(func() {
				ui.Text(c, samplePath).Font("Consolas").FontSize(12).SingleLine().Ellipsis("…").Tooltip(samplePath)
			}).OnClick(func() { a.selectPreviewPath(samplePath) })
		}
	}
	if r.Unmatched.Count > 0 {
		ui.Row(c).FillWidth().Padding(7, 12).Background(porcelain.Surface).BorderWidth(0, 0, 1, 0).BorderColor(porcelain.Border).Children(func() {
			ui.Text(c, a.tr("未匹配")).FontSize(12).TextColor(porcelain.TextMuted).Grow(1)
			ui.Text(c, fmt.Sprint(r.Unmatched.Count)).FontSize(12).TextColor(porcelain.TextMuted)
		})
		for _, sp := range r.Unmatched.Samples {
			samplePath := sp
			ui.Text(c, samplePath).Font("Consolas").FontSize(12).TextColor(porcelain.TextMuted).Padding(4, 12).SingleLine().Ellipsis("…")
		}
	}
	for _, item := range r.Contested {
		ui.Text(c, a.tr("多规则匹配: %s", item.Path)).FontSize(12).TextColor(porcelain.Warning).Padding(4, 12).SingleLine().Ellipsis("…")
	}
}
