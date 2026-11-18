package appcore

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"syncmaid/internal/filefilter"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
)

const PreviewSamples = 8

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
	EmptyRoot   bool
	Targets     map[taskconfig.ID]PreviewSelection
	Unmatched   PreviewSelection
	Contested   []PreviewContest
}

type PreviewExtension struct {
	Text  string
	Count int
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

// Preview reads only the source. These counts say which rules claim a file,
// not whether a target needs updating, and never authorize a file operation.
func ScanSourcePreview(ctx context.Context, q syncengine.ManualTaskRequest) (*RoutePreview, error) {
	q.Destinations = append([]syncengine.ManualDestination(nil), q.Destinations...)
	for i := range q.Destinations {
		q.Destinations[i].Matcher = nil
	}
	q = syncengine.PrepareReviewedRequest(q)
	matchers := make([]*filefilter.Matcher, len(q.Destinations))
	seen := map[taskconfig.ID]bool{}
	for i, d := range q.Destinations {
		if d.ID == "" || seen[d.ID] {
			return nil, fmt.Errorf("duplicate or missing destination ID")
		}
		seen[d.ID] = true
		if d.FilterError != nil {
			return nil, fmt.Errorf("%s: %w", d.Name, d.FilterError)
		}
		m := d.Matcher
		var err error
		if m == nil {
			m, err = filefilter.Compile(d.Filters)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.Name, err)
		}
		matchers[i] = m
		q.Destinations[i].Matcher = m
	}
	if syncengine.ManualKind(q.Kind) != taskconfig.Sync && syncengine.ManualKind(q.Kind) != taskconfig.Moving {
		return nil, fmt.Errorf("unsupported task kind")
	}
	inv, err := fileinventory.Scan(ctx, q.Source, fileinventory.Options{MaxEntries: 200_000, MaxDepth: 128})
	if err != nil {
		return nil, err
	}
	files := inv.Files()
	result := &RoutePreview{SourceFiles: len(files), EmptyRoot: inv.Empty(), Targets: map[taskconfig.ID]PreviewSelection{}}
	result.Extensions = SourceExtensions(files)
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
