package appcore

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"syncmaid/internal/filefilter"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
)

const PreviewSamples = 8

type PreviewFile struct {
	Path string
	Size int64
}

type PreviewDirectory struct {
	Prefix string
	Files  int
	Bytes  int64
}

type SourceSnapshot struct {
	Source      string
	Files       []PreviewFile
	FolderCount int
	TotalBytes  int64
	EmptyRoot   bool
	TopDirs     []PreviewDirectory
	Extensions  []PreviewExtension
	ScannedAt   time.Time
}

type PreviewSelection struct {
	Count   int
	Samples []string
}

func (s *PreviewSelection) Add(p string) {
	s.Count++
	if len(s.Samples) < PreviewSamples {
		s.Samples = append(s.Samples, p)
	}
}

type PreviewContest struct {
	Path    string
	Targets []taskconfig.ID
}

type RoutePreview struct {
	Extensions  []PreviewExtension
	SourceFiles int
	FolderCount int
	TotalBytes  int64
	TopDirs     []PreviewDirectory
	Snapshot    *SourceSnapshot
	EmptyRoot   bool
	Targets     map[taskconfig.ID]PreviewSelection
	Unmatched   PreviewSelection
	Contested   []PreviewContest
}

type PreviewExtension struct {
	Text  string
	Count int
}

func BuildTopDirs(files []fileinventory.File, limit int) []PreviewDirectory {
	if limit <= 0 {
		limit = 8
	}
	if len(files) == 0 {
		return nil
	}
	counts := map[string]PreviewDirectory{}
	for _, f := range files {
		prefix := "(root)"
		if slash := strings.IndexByte(f.Path, '/'); slash > 0 {
			prefix = f.Path[:slash+1]
		}
		d := counts[prefix]
		d.Prefix = prefix
		d.Files++
		d.Bytes += f.Size
		counts[prefix] = d
	}
	list := make([]PreviewDirectory, 0, len(counts))
	for _, d := range counts {
		list = append(list, d)
	}
	slices.SortFunc(list, func(a, b PreviewDirectory) int {
		return cmp.Or(
			cmp.Compare(b.Bytes, a.Bytes),
			cmp.Compare(b.Files, a.Files),
			cmp.Compare(strings.ToLower(a.Prefix), strings.ToLower(b.Prefix)),
			cmp.Compare(a.Prefix, b.Prefix),
		)
	})
	if len(list) > limit {
		list = list[:limit]
	}
	return list
}

func SearchSnapshotPaths(snap *SourceSnapshot, query string, limit int) (matches []PreviewFile, total int) {
	if snap == nil || len(snap.Files) == 0 {
		return nil, 0
	}
	if limit <= 0 {
		limit = 50
	}
	q := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(query)), "\\", "/")
	if q == "" {
		total = len(snap.Files)
		n := min(limit, total)
		return slices.Clone(snap.Files[:n]), total
	}
	for _, f := range snap.Files {
		if strings.Contains(strings.ToLower(f.Path), q) {
			total++
			if len(matches) < limit {
				matches = append(matches, f)
			}
		}
	}
	return matches, total
}

func SourceExtensions(files []fileinventory.File) []PreviewExtension {
	counts := map[string]PreviewExtension{}
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
	list := make([]PreviewExtension, 0, len(counts))
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

func preparePreviewMatchers(q syncengine.ManualTaskRequest) (syncengine.ManualTaskRequest, []*filefilter.Matcher, error) {
	q.Destinations = append([]syncengine.ManualDestination(nil), q.Destinations...)
	for i := range q.Destinations {
		q.Destinations[i].Matcher = nil
	}
	q = syncengine.PrepareReviewedRequest(q)
	matchers := make([]*filefilter.Matcher, len(q.Destinations))
	seen := map[taskconfig.ID]bool{}
	for i, d := range q.Destinations {
		if d.ID == "" || seen[d.ID] {
			return q, nil, fmt.Errorf("duplicate or missing destination ID")
		}
		seen[d.ID] = true
		if d.FilterError != nil {
			return q, nil, fmt.Errorf("%s: %w", d.Name, d.FilterError)
		}
		m := d.Matcher
		var err error
		if m == nil {
			m, err = filefilter.Compile(d.Filters)
		}
		if err != nil {
			return q, nil, fmt.Errorf("%s: %w", d.Name, err)
		}
		matchers[i] = m
		q.Destinations[i].Matcher = m
	}
	if syncengine.ManualKind(q.Kind) != taskconfig.Sync && syncengine.ManualKind(q.Kind) != taskconfig.Moving {
		return q, nil, fmt.Errorf("unsupported task kind")
	}
	return q, matchers, nil
}

// EvaluateSourceSnapshot re-evaluates destination rules and Move routing
// against a previously scanned read-only SourceSnapshot without touching disk.
func EvaluateSourceSnapshot(ctx context.Context, snap *SourceSnapshot, q syncengine.ManualTaskRequest) (*RoutePreview, error) {
	if snap == nil {
		return nil, errors.New("source snapshot missing")
	}
	q, matchers, err := preparePreviewMatchers(q)
	if err != nil {
		return nil, err
	}
	files := make([]fileinventory.File, len(snap.Files))
	for i, f := range snap.Files {
		files[i] = fileinventory.File{Path: f.Path, Size: f.Size}
	}
	return evaluatePreviewFiles(ctx, snap, files, q, matchers)
}

func evaluatePreviewFiles(ctx context.Context, snap *SourceSnapshot, files []fileinventory.File, q syncengine.ManualTaskRequest, matchers []*filefilter.Matcher) (*RoutePreview, error) {
	result := &RoutePreview{
		Extensions:  slices.Clone(snap.Extensions),
		SourceFiles: len(files),
		FolderCount: snap.FolderCount,
		TotalBytes:  snap.TotalBytes,
		TopDirs:     slices.Clone(snap.TopDirs),
		Snapshot:    snap,
		EmptyRoot:   snap.EmptyRoot,
		Targets:     map[taskconfig.ID]PreviewSelection{},
	}
	for _, d := range q.Destinations {
		result.Targets[d.ID] = PreviewSelection{}
	}
	moving := syncengine.ManualKind(q.Kind) == taskconfig.Moving
	if moving {
		routes, unmatched, err := syncengine.PrepareMoveRoutes(ctx, files, q.Destinations)
		if err != nil {
			return nil, err
		}
		result.Unmatched.Count = unmatched
		for i, fs := range routes {
			s := PreviewSelection{Count: len(fs)}
			for j, f := range fs {
				if j == PreviewSamples {
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
					s.Add(f.Path)
					result.Targets[q.Destinations[i].ID] = s
				}
			}
		}
		if len(matches) == 0 {
			if !moving {
				result.Unmatched.Add(f.Path)
			} else if len(result.Unmatched.Samples) < PreviewSamples {
				result.Unmatched.Samples = append(result.Unmatched.Samples, f.Path)
			}
		}
		if moving && len(matches) > 1 && len(result.Contested) < PreviewSamples {
			result.Contested = append(result.Contested, PreviewContest{f.Path, matches})
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// Preview reads only the source. These counts say which rules claim a file,
// not whether a target needs updating, and never authorize a file operation.
func ScanSourcePreview(ctx context.Context, q syncengine.ManualTaskRequest) (*RoutePreview, error) {
	q, matchers, err := preparePreviewMatchers(q)
	if err != nil {
		return nil, err
	}
	inv, err := fileinventory.Scan(ctx, q.Source, fileinventory.Options{MaxEntries: 200_000, MaxDepth: 128})
	if err != nil {
		return nil, err
	}
	files := inv.Files()
	previewFiles := make([]PreviewFile, len(files))
	var totalBytes int64
	for i, f := range files {
		previewFiles[i] = PreviewFile{Path: f.Path, Size: f.Size}
		totalBytes += f.Size
	}
	snap := &SourceSnapshot{
		Source:      q.Source,
		Files:       previewFiles,
		FolderCount: len(inv.Directories()),
		TotalBytes:  totalBytes,
		EmptyRoot:   inv.Empty(),
		TopDirs:     BuildTopDirs(files, 8),
		Extensions:  SourceExtensions(files),
		ScannedAt:   time.Now(),
	}
	return evaluatePreviewFiles(ctx, snap, files, q, matchers)
}

// MatchPreviewPaths evaluates which of the given preview files route to targetID
// under the current task request, respecting Move first-match routing and regex gates.
func MatchPreviewPaths(ctx context.Context, files []PreviewFile, q syncengine.ManualTaskRequest, targetID taskconfig.ID) (map[string]bool, error) {
	hits := make(map[string]bool, len(files))
	if len(files) == 0 || targetID == "" {
		return hits, nil
	}
	q, matchers, err := preparePreviewMatchers(q)
	if err != nil {
		return nil, err
	}
	targetIdx := -1
	for i, d := range q.Destinations {
		if d.ID == targetID {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		return hits, nil
	}
	moving := syncengine.ManualKind(q.Kind) == taskconfig.Moving
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if moving {
			for i, m := range matchers {
				yes, err := m.Match(f.Path)
				if err != nil {
					return nil, err
				}
				if yes {
					if i == targetIdx {
						hits[f.Path] = true
					}
					break
				}
			}
			continue
		}
		yes, err := matchers[targetIdx].Match(f.Path)
		if err != nil {
			return nil, err
		}
		if yes {
			hits[f.Path] = true
		}
	}
	return hits, nil
}
