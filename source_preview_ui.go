package main

import (
	"context"
	"github.com/egoist/mygo/ui"
	"reflect"
	"strings"
	"syncmaid/internal/appcore"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
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

func (a *application) manualEdited() { a.sourcePreview.invalidate(); a.pauseEditingWatch(a.manual.id) }

// Owned by the native UI thread. The worker owns only its cloned request.
type sourcePreviewSession struct {
	phase      sourcePreviewPhase
	generation uint64
	job        *sourcePreviewJob
	result     *appcore.RoutePreview
	err        error
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
	if err != nil {
		s.result = nil
		s.phase = previewFailed
	}
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
	if a.Source != b.Source || syncengine.ManualKind(a.Kind) != syncengine.ManualKind(b.Kind) || !reflect.DeepEqual(a.Trigger, b.Trigger) || len(a.Destinations) != len(b.Destinations) {
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

func (a *application) sourcePreviewView(c *ui.Context) {
	p := &a.sourcePreview
	ui.Column(c).FillWidth().Gap(8).Padding(12, 0).Children(func() {
		ui.Row(c).Wrap().Gap(10).Children(func() {
			label := "源预览"
			if p.phase != previewIdle {
				label = "刷新预览"
			}
			ui.ButtonBase(c).Label(a.tr(label)).Radius(0).Padding(5, 10).Gap(8).Border(1, porcelain.Border).Background(porcelain.Background).TextColor(porcelain.Text).Children(func() { ui.Icon(c, porcelainGlyphs["eye"]).Size(16, 16); ui.Text(c, a.tr(label)).FontSize(16) }).Disabled(a.manual.busy() || a.manual.quitPending || p.job != nil || a.taskError != nil).OnClick(a.startSourcePreview)
			if p.job != nil {
				porcelainButton(c, a.tr("取消预览")).Disabled(p.phase == previewStopping).OnClick(p.invalidate)
			}
		})

		switch p.phase {
		case previewScanning:
			ui.Text(c, a.tr("正在读取源文件…"))
		case previewStopping:
			ui.Text(c, a.tr("正在取消预览…"))
		case previewStale:
			ui.Text(c, a.tr("预览已失效，请刷新。"))
		case previewFailed:
			ui.Text(c, a.tr("预览失败: %s", p.err.Error())).TextColor(porcelain.Danger)
		case previewReady:
			r := p.result
			if r.EmptyRoot {
				ui.Text(c, a.tr("源目录为空。"))
			} else if r.SourceFiles == 0 {
				ui.Text(c, a.tr("源目录存在，但没有普通文件。"))
			} else {
				ui.Text(c, a.tr("源文件: %d", r.SourceFiles))
			}
			ui.Text(c, a.tr("未匹配: %d · %s", r.Unmatched.Count, strings.Join(r.Unmatched.Samples, ", "))).MaxLines(3)
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
