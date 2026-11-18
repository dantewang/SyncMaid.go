package main

import (
	"context"
	"reflect"
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
