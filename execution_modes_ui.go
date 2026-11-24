package main

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"path"
	"slices"
	"strconv"
	"strings"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"

	"github.com/egoist/mygo/ui"
)

type batchPhase uint8

const (
	batchIdle batchPhase = iota
	batchServing
)

// UI-owned order fixed at the user's click; no parallel executor or retries.
type manualBatch struct {
	phase   batchPhase
	pending []taskconfig.ID
}

func (b *manualBatch) clear() { b.phase = batchIdle; b.pending = nil }
func (b *manualBatch) begin(ids []taskconfig.ID) {
	b.phase = batchServing
	b.pending = append([]taskconfig.ID(nil), ids...)
}
func (b *manualBatch) next() (taskconfig.ID, bool) {
	if b.phase != batchServing || len(b.pending) == 0 {
		return "", false
	}
	id := b.pending[0]
	b.pending = b.pending[1:]
	return id, true
}

func (a *application) startBatch() {
	if !a.cardActionAllowed() || a.hasModalOpen() || a.manual.quitPending {
		return
	}
	a.batch.begin(taskIDs(a.tasks.Tasks))
	a.advanceBatch()
}
func (a *application) advanceBatch() { a.advanceBatchUsing(a.startTaskRun) }
func (a *application) advanceBatchUsing(start func(taskconfig.ID, string, syncengine.ManualTaskRequest, bool)) {
	if a.batch.phase != batchServing || a.manual.busy() || a.hasModalOpen() {
		return
	}
	if a.manual.quitPending || a.manual.dirty || a.taskError != nil || a.tasks == nil {
		a.batch.clear()
		return
	}
	for {
		id, ok := a.batch.next()
		if !ok {
			a.batch.clear()
			a.drainWatches()
			return
		}
		t, err := a.tasks.Find(id)
		if err == nil {
			q, e := a.tasks.Request(t)
			err = e
			if err == nil {
				a.selectManual(id)
				if len(q.Destinations) == 0 {
					a.startTaskRun(id, t.Name, q, false)
					continue
				}
				start(id, t.Name, q, false)
				return
			}
		}
		if a.results == nil {
			a.results = map[taskconfig.ID]savedTaskResult{}
		}
		goals := syncengine.MultipleResult{}
		for _, d := range t.Destinations {
			goals[d.ID] = syncengine.DestinationResult{ID: d.ID, Name: d.Name, State: syncengine.DestinationFailed, Error: err.Error()}
		}
		a.results[id] = savedTaskResult{outcome: "failed", message: messagef("准备失败，任务未执行: %s", err.Error()), destinations: goals}
	}
}

func (a *application) setDestinationMode(strategy taskconfig.Strategy, mode taskconfig.DeleteMode) {
	if !a.targetEditAllowed() || syncengine.ManualKind(a.manual.kind) == taskconfig.Moving {
		return
	}
	for i := range a.manual.targets {
		d := &a.manual.targets[i]
		if d.ID != a.manual.targetID {
			continue
		}
		if strategy == taskconfig.Mirror && !syncengine.IsAllFileRules(d.Filters) {
			a.manual.message = messagef("请先将此目标过滤设为全部文件，再选择Mirror。")
			return
		}
		d.Strategy = strategy
		d.DeleteMode = mode
		if strategy == taskconfig.Mirror && strings.TrimSpace(a.manual.mirrorThresholdText) == "" {
			a.manual.mirrorThresholdText = formatThresholdPercent(syncengine.DestinationMassDeleteThreshold(*d))
		}
		a.manual.edited()
		if strategy == taskconfig.Mirror && syncengine.DestinationDeleteMode(*d) == taskconfig.Recycle {
			a.triggerRecycleProbe(d.ID, d.Path)
		}
		return
	}
}
func (a *application) setMirrorThreshold(pct int) {
	if !a.targetEditAllowed() || syncengine.ManualKind(a.manual.kind) == taskconfig.Moving {
		return
	}
	for i := range a.manual.targets {
		d := &a.manual.targets[i]
		if d.ID != a.manual.targetID {
			continue
		}
		d.MassDeleteThreshold = float64(pct) / 100.0
		d.MassDeleteThresholdSet = true
		a.manual.mirrorThresholdText = strconv.Itoa(pct)
		a.manual.edited()
		return
	}
}
func (a *application) destinationModeView(c *ui.Context, locked bool) {
	for _, d := range a.manual.targets {
		if d.ID == a.manual.targetID {
			ui.Text(c, a.tr("同步策略")).FontSize(13).FontWeight(600)
			porcelainChoice(c, "sync", a.tr("镜像"), a.tr("复制新增和更改的文件；清理多余文件与空目录，达到阈值时确认。"), syncengine.DestinationStrategy(d) == taskconfig.Mirror, locked, func() { a.setDestinationMode(taskconfig.Mirror, syncengine.DestinationDeleteMode(d)) }).Tooltip(a.tr("Mirror同步全部文件并清理多余空目录；删除比例达到阈值时确认。空源始终禁止删除。"))
			porcelainChoice(c, "＋", a.tr("仅添加"), a.tr("复制新增和更改的文件；从不删除目标中的文件。"), syncengine.DestinationStrategy(d) == taskconfig.AddOnly, locked, func() { a.setDestinationMode(taskconfig.AddOnly, syncengine.DestinationDeleteMode(d)) }).Tooltip(a.tr("复制新增和更改的文件；从不删除目标中的文件。"))
			if syncengine.DestinationStrategy(d) == taskconfig.Mirror {
				ui.Text(c, a.tr("镜像删除策略")).FontSize(13).FontWeight(600)
				ui.Row(c).Gap(0).Children(func() {
					porcelainSegment(c, a.tr("回收站"), syncengine.DestinationDeleteMode(d) == taskconfig.Recycle).Disabled(locked).OnClick(func() { a.setDestinationMode(taskconfig.Mirror, taskconfig.Recycle) })
					porcelainSegment(c, a.tr("永久删除"), syncengine.DestinationDeleteMode(d) == taskconfig.Permanent).Disabled(locked).OnClick(func() { a.setDestinationMode(taskconfig.Mirror, taskconfig.Permanent) })
				})
				if syncengine.DestinationDeleteMode(d) == taskconfig.Recycle && a.destinationRecycleUnsupported(d) {
					ui.Row(c).Gap(8).Children(func() {
						ui.Text(c, a.tr("此目标不支持回收站，Mirror 清理多余文件将失败。")).FontSize(12).TextColor(porcelain.Danger).Grow(1)
						porcelainButton(c, a.tr("切换为永久删除")).Disabled(locked).OnClick(func() { a.setDestinationMode(taskconfig.Mirror, taskconfig.Permanent) })
					})
				}
				currentPct := int(math.Round(syncengine.DestinationMassDeleteThreshold(d) * 100))
				validCustom := true
				if strings.TrimSpace(a.manual.mirrorThresholdText) != "" {
					if parsed, ok := parseThresholdPercent(a.manual.mirrorThresholdText); ok {
						currentPct = int(math.Round(parsed * 100))
					} else {
						validCustom = false
					}
				}
				isPreset := validCustom && (currentPct == 25 || currentPct == 50 || currentPct == 75 || currentPct == 0)

				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Text(c, a.tr("大规模删除确认阈值")).FontSize(13).FontWeight(600)
					helpBtn := porcelainIcon(c, "help", a.tr("阈值规则说明"))
					if a.manual.mirrorThresholdHelpOpen {
						helpBtn = helpBtn.Background(ribbonBlueSoft).Border(1, ribbonBlue)
					}
					helpBtn.Disabled(locked).OnClick(func() {
						a.manual.mirrorThresholdHelpOpen = !a.manual.mirrorThresholdHelpOpen
					})
				})
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Row(c).Gap(0).Children(func() {
						porcelainSegment(c, "25%", validCustom && currentPct == 25).Disabled(locked).OnClick(func() { a.setMirrorThreshold(25) })
						porcelainSegment(c, a.tr("50% 推荐"), validCustom && currentPct == 50).Disabled(locked).OnClick(func() { a.setMirrorThreshold(50) })
						porcelainSegment(c, "75%", validCustom && currentPct == 75).Disabled(locked).OnClick(func() { a.setMirrorThreshold(75) })
						porcelainSegment(c, a.tr("关闭 (0%)"), validCustom && currentPct == 0).Disabled(locked).OnClick(func() { a.setMirrorThreshold(0) })
					})
					ui.Text(c, a.tr("自定义")).FontSize(12).TextColor(porcelain.TextMuted)
					customBorder, customBg := porcelain.Border, porcelain.Surface
					if !validCustom {
						customBorder = porcelain.Danger
					} else if !isPreset {
						customBorder, customBg = ribbonBlue, ribbonBlueSoft
					}
					ui.Row(c).AlignItems(ui.Center).Gap(4).Padding(2, 6).Border(1, customBorder).Background(customBg).Children(func() {
						ui.TextInput(c, &a.manual.mirrorThresholdText).Width(46).Height(26).FontSize(13).Label(a.tr("自定义删除阈值百分比")).ReadOnly(locked).OnChange(a.manual.edited)
						ui.Text(c, "%").FontSize(12).TextColor(porcelain.TextMuted)
					})
				})
				if a.manual.mirrorThresholdHelpOpen {
					ui.Column(c).FillWidth().Border(1, porcelain.Border).Background(porcelain.SurfaceHover).Padding(8, 10).Children(func() {
						if validCustom && currentPct == 0 {
							ui.Text(c, a.tr("已关闭比例确认：多余文件将自动按所选策略移除。源目录为空时始终禁止删除。")).FontSize(12).TextColor(porcelain.TextMuted)
						} else {
							ui.Text(c, a.tr("当目标原有文件数 ≥ 10，且本次计划删除比例 ≥ %d%% 时弹窗确认；低于阈值自动移除。源目录为空时始终禁止删除。", currentPct)).FontSize(12).TextColor(porcelain.TextMuted)
						}
					})
				}
				return
			}
		}
	}
}

type mirrorDirGroup struct {
	Dir   string
	Count int
}

func groupMirrorPaths(paths []string) []mirrorDirGroup {
	counts := map[string]int{}
	for _, p := range paths {
		norm := strings.ReplaceAll(p, "\\", "/")
		dir := path.Dir(norm)
		if dir == "." || dir == "" {
			dir = "(root)"
		} else {
			dir += "/"
		}
		counts[dir]++
	}
	out := make([]mirrorDirGroup, 0, len(counts))
	for d, cnt := range counts {
		out = append(out, mirrorDirGroup{Dir: d, Count: cnt})
	}
	slices.SortFunc(out, func(a, b mirrorDirGroup) int {
		return cmp.Or(
			cmp.Compare(b.Count, a.Count),
			cmp.Compare(strings.ToLower(a.Dir), strings.ToLower(b.Dir)),
			cmp.Compare(a.Dir, b.Dir),
		)
	})
	return out
}

func (a *application) confirmMirror(ctx context.Context, p syncengine.MirrorPreview) error {
	reply := make(chan error, 1)
	a.window.Update(func() {
		if ctx.Err() == nil {
			a.lifecycle.show()
			a.manual.mirrorPreview = &p
			a.manual.mirrorReply = reply
			a.manual.mirrorShowAll = false
			a.manual.mirrorPreviewTabDirs = false
		}
	})
	defer a.window.Update(func() {
		if a.manual.mirrorReply == reply {
			a.manual.mirrorPreview = nil
			a.manual.mirrorReply = nil
			a.manual.mirrorShowAll = false
			a.manual.mirrorPreviewTabDirs = false
		}
	})
	select {
	case e := <-reply:
		return e
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (a *application) answerMirror(accept bool) {
	s := &a.manual
	if s.mirrorReply == nil {
		return
	}
	var e error
	if !accept {
		e = syncengine.ErrMirrorDeclined
	}
	select {
	case s.mirrorReply <- e:
	default:
	}
	s.mirrorPreview = nil
	s.mirrorReply = nil
	s.mirrorShowAll = false
	s.mirrorPreviewTabDirs = false
}
func (a *application) mirrorPreviewView(c *ui.Context) {
	p := a.manual.mirrorPreview
	if p == nil {
		return
	}
	action := a.tr("移至回收站")
	if p.Mode == taskconfig.Permanent {
		action = a.tr("永久删除（不可撤销）")
	}
	hasDirs := len(p.Dirs) > 0
	showDirs := hasDirs && a.manual.mirrorPreviewTabDirs
	ui.Column(c).Grow(1).FillWidth().Gap(12).Padding(16).Children(func() {
		if hasDirs {
			ui.Text(c, a.tr("确认本次Mirror：%s · %s · %d个文件 · %d个空目录", p.Name, action, len(p.Paths), len(p.Dirs))).FontSize(17).Bold()
		} else {
			ui.Text(c, fmt.Sprintf(a.tr("确认本次Mirror：%s · %s · %d个文件"), p.Name, action, len(p.Paths))).FontSize(17).Bold()
		}
		porcelainPath(c, p.Target)
		large := len(p.Paths) > 20
		if hasDirs || large {
			ui.Row(c).Gap(6).Children(func() {
				if hasDirs {
					porcelainSegment(c, a.tr("多余文件 (%d)", len(p.Paths)), !showDirs).OnClick(func() { a.manual.mirrorPreviewTabDirs = false })
					porcelainSegment(c, a.tr("多余空目录 (%d)", len(p.Dirs)), showDirs).OnClick(func() { a.manual.mirrorPreviewTabDirs = true })
				}
				if !showDirs && large {
					porcelainSegment(c, a.tr("按目录汇总"), !a.manual.mirrorShowAll).OnClick(func() { a.manual.mirrorShowAll = false })
					porcelainSegment(c, a.tr("全部路径"), a.manual.mirrorShowAll).OnClick(func() { a.manual.mirrorShowAll = true })
				}
			})
		}
		porcelainScroll(c, &a.scrolls.mirrorConfirm, func() {
			ui.Column(c).FillWidth().Padding(10).Gap(4).Children(func() {
				if showDirs {
					limit := len(p.Dirs)
					if limit > 200 {
						limit = 200
					}
					for _, dirPath := range p.Dirs[:limit] {
						ui.Text(c, dirPath).Font("Consolas").FontSize(13)
					}
					if limit < len(p.Dirs) {
						ui.Text(c, a.tr("仅显示前 %d / 共 %d 项", limit, len(p.Dirs))).FontSize(12).TextColor(porcelain.TextMuted)
					}
					return
				}
				if large && !a.manual.mirrorShowAll {
					for _, g := range groupMirrorPaths(p.Paths) {
						dirLabel := g.Dir
						if dirLabel == "(root)" {
							dirLabel = a.tr("(根目录)")
						}
						ui.Row(c).FillWidth().Padding(4, 0).BorderWidth(0, 0, 1, 0).BorderColor(porcelain.Border).Children(func() {
							ui.Text(c, dirLabel).Font("Consolas").FontSize(13).Grow(1)
							ui.Text(c, fmt.Sprintf("%d", g.Count)).FontSize(13).TextColor(porcelain.TextMuted)
						})
					}
					return
				}
				limit := len(p.Paths)
				if large && limit > 200 {
					limit = 200
				}
				for _, itemPath := range p.Paths[:limit] {
					ui.Text(c, itemPath).Font("Consolas").FontSize(13)
				}
				if limit < len(p.Paths) {
					ui.Text(c, a.tr("仅显示前 %d / 共 %d 项", limit, len(p.Paths))).FontSize(12).TextColor(porcelain.TextMuted)
				}
			})
		}).Grow(1).FillWidth().Border(1, porcelain.Border).Background(porcelain.Surface)
		ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Children(func() {
			porcelainButton(c, a.tr("取消本次移除")).OnClick(func() { a.answerMirror(false) })
			ribbonDangerButton(c, a.tr("确认本次移除")).OnClick(func() { a.answerMirror(true) })
		})
	})
}

func (a *application) selectedMirror() bool {
	for _, d := range a.manual.targets {
		if d.ID == a.manual.targetID {
			return syncengine.DestinationStrategy(d) == taskconfig.Mirror
		}
	}
	return false
}

func (a *application) setManualKind(kind taskconfig.TaskKind) {
	s := &a.manual
	if !a.targetEditAllowed() || len(s.targets) != 0 || syncengine.ManualKind(s.kind) == kind {
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
		porcelainChoice(c, glyph, a.tr(key), a.tr(description), syncengine.ManualKind(s.kind) == kind, locked || len(s.targets) != 0, func() { a.setManualKind(kind) }).MinHeight(94)
	}
	if syncengine.ManualKind(s.kind) == taskconfig.Moving {
		ui.Text(c, a.tr("首个匹配目标拥有文件；不覆盖目标，失败不转投后续目标。已保存任务不能切换类型。"))
	}
}
func (a *application) setMoveFlatten(flatten bool) {
	if !a.targetEditAllowed() || syncengine.ManualKind(a.manual.kind) != taskconfig.Moving {
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
			ui.Text(c, a.tr("Move冲突处理：保留源，或在扩展名前加编号；都不覆盖已有文件。")).FontSize(13).FontWeight(600)
			ui.Row(c).Gap(8).Children(func() {
				ui.Button(c, a.tr("保留源")).Selected(d.Collision == "" || d.Collision == taskconfig.Skip).Disabled(locked).OnClick(func() { a.setMoveCollision(taskconfig.Skip) })
				ui.Button(c, a.tr("加编号")).Selected(d.Collision == taskconfig.Suffix).Disabled(locked).OnClick(func() { a.setMoveCollision(taskconfig.Suffix) })
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.Button(c, a.tr("保留目录结构")).Selected(!d.Flatten).Disabled(locked).OnClick(func() { a.setMoveFlatten(false) })
				ui.Button(c, a.tr("平铺到目标根目录")).Selected(d.Flatten).Disabled(locked).OnClick(func() { a.setMoveFlatten(true) })
			})
		}
	}
}

func (a *application) setMoveCollision(policy taskconfig.CollisionPolicy) {
	if !a.targetEditAllowed() || syncengine.ManualKind(a.manual.kind) != taskconfig.Moving {
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
