package main

import (
	"context"
	"fmt"
	"path"
	"reflect"
	"sort"
	"strings"
	"syncmaid/internal/filefilter"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/taskconfig"
)

const previewSamples = 8

type previewSelection struct {
	Count   int
	Samples []string
}

func (s *previewSelection) add(path string) {
	s.Count++
	if len(s.Samples) < previewSamples {
		s.Samples = append(s.Samples, path)
	}
}

type previewContest struct {
	Path    string
	Targets []taskconfig.ID
}
type routePreview struct {
	Extensions  []previewExtension
	SourceFiles int
	EmptyRoot   bool
	Targets     map[taskconfig.ID]previewSelection
	Unmatched   previewSelection
	Contested   []previewContest
}

type previewExtension struct {
	Text  string
	Count int
}

func sourceExtensions(files []fileinventory.File) []previewExtension {
	counts := map[string]previewExtension{}
	for _, f := range files {
		ext := strings.TrimPrefix(path.Ext(f.Path), ".")
		if ext == "" {
			continue
		}
		key := strings.ToLower(ext)
		v := counts[key]
		if v.Text == "" {
			v.Text = ext
		}
		v.Count++
		counts[key] = v
	}
	list := make([]previewExtension, 0, len(counts))
	for _, v := range counts {
		list = append(list, v)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Count != list[j].Count {
			return list[i].Count > list[j].Count
		}
		return strings.ToLower(list[i].Text) < strings.ToLower(list[j].Text)
	})
	if len(list) > 12 {
		list = list[:12]
	}
	return list
}

// Preview reads only the source. These counts say which rules claim a file,
// not whether a target needs updating, and never authorize a file operation.
func scanSourcePreview(ctx context.Context, q manualTaskRequest) (*routePreview, error) {
	q.Destinations = append([]manualDestination(nil), q.Destinations...)
	for i := range q.Destinations {
		q.Destinations[i].matcher = nil
	}
	q = prepareReviewedRequest(q)
	matchers := make([]*filefilter.Matcher, len(q.Destinations))
	seen := map[taskconfig.ID]bool{}
	for i, d := range q.Destinations {
		if d.ID == "" || seen[d.ID] {
			return nil, fmt.Errorf("duplicate or missing destination ID")
		}
		seen[d.ID] = true
		if d.filterError != nil {
			return nil, fmt.Errorf("%s: %w", d.Name, d.filterError)
		}
		m := d.matcher
		var err error
		if m == nil {
			m, err = filefilter.Compile(d.Filters)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.Name, err)
		}
		matchers[i] = m
		q.Destinations[i].matcher = m
	}
	if manualKind(q.Kind) != taskconfig.Sync && manualKind(q.Kind) != taskconfig.Moving {
		return nil, fmt.Errorf("unsupported task kind")
	}
	inv, err := fileinventory.Scan(ctx, q.Source, fileinventory.Options{MaxEntries: 200_000, MaxDepth: 128})
	if err != nil {
		return nil, err
	}
	files := inv.Files()
	result := &routePreview{SourceFiles: len(files), EmptyRoot: inv.Empty(), Targets: map[taskconfig.ID]previewSelection{}}
	result.Extensions = sourceExtensions(files)
	for _, d := range q.Destinations {
		result.Targets[d.ID] = previewSelection{}
	}
	moving := manualKind(q.Kind) == taskconfig.Moving
	if moving {
		routes, unmatched, err := prepareMoveRoutes(ctx, files, q.Destinations)
		if err != nil {
			return nil, err
		}
		result.Unmatched.Count = unmatched
		for i, fs := range routes {
			s := previewSelection{Count: len(fs)}
			for j, f := range fs {
				if j == previewSamples {
					break
				}
				s.Samples = append(s.Samples, f.Path)
			}
			result.Targets[q.Destinations[i].ID] = s
		}
	}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var matches []taskconfig.ID
		for i, m := range matchers {
			yes, err := m.Match(f.Path)
			if err != nil {
				return nil, err
			}
			if yes {
				matches = append(matches, q.Destinations[i].ID)
				if !moving {
					s := result.Targets[q.Destinations[i].ID]
					s.add(f.Path)
					result.Targets[q.Destinations[i].ID] = s
				}
			}
		}
		if len(matches) == 0 {
			if !moving {
				result.Unmatched.add(f.Path)
			} else if len(result.Unmatched.Samples) < previewSamples {
				result.Unmatched.Samples = append(result.Unmatched.Samples, f.Path)
			}
		}
		if moving && len(matches) > 1 && len(result.Contested) < previewSamples {
			result.Contested = append(result.Contested, previewContest{f.Path, matches})
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

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
	result     *routePreview
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
func (s *sourcePreviewSession) finish(job *sourcePreviewJob, result *routePreview, err error) {
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
func (a *application) previewDraft() (manualTaskRequest, error) {
	s := &a.manual
	if a.tasks != nil && s.id != "" {
		if err := a.selectedTaskIssue(); err != nil {
			return manualTaskRequest{}, err
		}
	}
	s.flushTarget()
	s.keepFilterDraft()
	if err := s.filterDraftError(); err != nil {
		return manualTaskRequest{}, err
	}
	q := s.draftRequest()
	for i := range q.Destinations {
		d := &q.Destinations[i]
		rs := make([]taskconfig.Rule, len(d.Filters))
		for j, r := range d.Filters {
			rs[j] = r.Clone()
		}
		d.Filters = rs
		d.matcher = nil
		d.filterError = nil
	}
	// Editing invalidates whole-task confirmations. A preview cannot renew them.
	if !s.dirty && samePreviewRequest(q, s.request) && (s.request.regexTask == nil || (s.name == s.request.regexTask.Name && s.id == s.request.regexTask.ID)) {
		q.regexTask = s.request.regexTask
		q.regexReviews = s.request.regexReviews
	}
	return q, nil
}

func samePreviewRequest(a, b manualTaskRequest) bool {
	if a.Source != b.Source || manualKind(a.Kind) != manualKind(b.Kind) || !reflect.DeepEqual(a.Trigger, b.Trigger) || len(a.Destinations) != len(b.Destinations) {
		return false
	}
	for i, d := range a.Destinations {
		o := b.Destinations[i]
		if d.ID != o.ID || d.Name != o.Name || d.Path != o.Path || d.Strategy != o.Strategy || d.DeleteMode != o.DeleteMode || d.Flatten != o.Flatten || d.Collision != o.Collision || !sameFilterRules(d.Filters, o.Filters) {
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
		result, err := scanSourcePreview(ctx, q)
		a.window.Update(func() {
			s.finish(job, result, err)
			a.finishAsyncStep(true)
		})
	}()
}

func (a *application) addPreviewExtension(group int, extension string) {
	p := &a.sourcePreview
	e := &a.manual.filterEditor
	if p.phase != previewReady || p.result == nil || !a.targetEditAllowed() || a.selectedMirror() || e.Mode != filterGrouped || group < 0 || group >= len(e.Groups) || len(e.Groups[group].Rows) >= 128 {
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
