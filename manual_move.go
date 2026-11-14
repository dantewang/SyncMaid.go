package main

import (
	"context"
	"errors"
	"fmt"
	"path"
	"syncmaid/internal/filefilter"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/taskconfig"
)

func manualKind(k taskconfig.TaskKind) taskconfig.TaskKind {
	if k == "" {
		return taskconfig.Sync
	}
	return k
}
func validateMoveDestination(d manualDestination) error {
	if d.Strategy != taskconfig.Move {
		return errors.New("Move任务只能使用Move目标")
	}
	if d.Collision != "" && d.Collision != taskconfig.Skip {
		return errors.New("当前Move仅支持冲突保留源，暂不支持加编号")
	}
	return nil
}

// Compile every target before matching anything. First match owns the file,
// including when its destination later fails or skips a collision.
func prepareMoveRoutes(ctx context.Context, files []fileinventory.File, destinations []manualDestination) ([][]fileinventory.File, int, error) {
	matchers := make([]*filefilter.Matcher, len(destinations))
	for i, d := range destinations {
		if e := validateMoveDestination(d); e != nil {
			return nil, 0, e
		}
		m, e := filefilter.Compile(d.Filters)
		if e != nil {
			return nil, 0, fmt.Errorf("%s: %w", d.Name, e)
		}
		matchers[i] = m
	}
	routes := make([][]fileinventory.File, len(destinations))
	unmatched := 0
	for _, f := range files {
		if e := ctx.Err(); e != nil {
			return nil, 0, e
		}
		chosen := -1
		for i, m := range matchers {
			yes, e := m.Match(f.Path)
			if e != nil {
				return nil, 0, e
			}
			if yes && chosen < 0 {
				chosen = i
			}
		}
		if chosen < 0 {
			unmatched++
		} else {
			routes[chosen] = append(routes[chosen], f)
		}
	}
	return routes, unmatched, nil
}

func runMove(ctx context.Context, q manualTaskRequest, notify func(destinationResult)) (results multipleResult, err error) {
	results = initialDestinationResults(q)
	failAll := func(e error) (multipleResult, error) {
		for id, r := range results {
			r.State = destinationFailed
			r.Error = e.Error()
			results[id] = r
		}
		return results, e
	}
	plan, e := openMultiplePlan(ctx, q)
	if e != nil {
		return failAll(e)
	}
	defer func() { err = errors.Join(err, protectedTransferIO(ctx, plan.Close)) }()
	files, stamps, e := scanMoveFiles(ctx, plan.source)
	if e != nil {
		return failAll(e)
	}
	routes, unmatched, e := prepareMoveRoutes(ctx, files, q.Destinations)
	if e != nil {
		return failAll(e)
	}
	var failures []error
	for i, d := range q.Destinations {
		r := results[d.ID]
		r.Progress = transferProgress{SourceFiles: len(files), Total: len(routes[i]), Unmatched: unmatched}
		emit := func() {
			results[d.ID] = r
			if notify != nil {
				notify(r)
			}
		}
		if ctx.Err() != nil {
			r.State = destinationSkipped
			r.Progress.Retained = len(routes[i])
			emit()
			continue
		}
		if e := plan.failures[d.ID]; e != nil {
			r.State = destinationFailed
			r.Error = e.Error()
			r.Progress.Failed = len(routes[i])
			r.Progress.Retained = len(routes[i])
			failures = append(failures, e)
			emit()
			continue
		}
		if len(routes[i]) == 0 {
			r.State = destinationNoMatches
			emit()
			continue
		}
		roots, e := openTransferRoots(ctx, plan.requests[d.ID])
		if e != nil {
			r.State = destinationFailed
			r.Error = e.Error()
			r.Progress.Retained = len(routes[i])
			failures = append(failures, e)
			emit()
			continue
		}
		r.State = destinationRunning
		emit()
		var targetErrors []error
		for j, f := range routes[i] {
			if ctx.Err() != nil {
				r.Progress.Retained += len(routes[i]) - j
				break
			}
			r.Progress.Stage = "移动中"
			r.Progress.Path = f.Path
			emit()
			rel := f.Path
			if d.Flatten {
				rel = path.Base(rel)
			}
			published, e := moveFile(ctx, roots, f, rel, stamps[f.Path], func(n int64) { r.Progress.Bytes += n; emit() })
			if published {
				r.Progress.Copied++
			}
			switch {
			case e != nil:
				r.Progress.Failed++
				r.Progress.Retained++
				targetErrors = append(targetErrors, fmt.Errorf("%s: %w", f.Path, e))
			case published:
				r.Progress.Moved++
			default:
				r.Progress.Retained++
			}
			emit()
		}
		e = protectedTransferIO(ctx, roots.Close)
		targetErrors = append(targetErrors, e)
		combined := errors.Join(targetErrors...)
		switch {
		case ctx.Err() != nil:
			r.State = destinationCanceled
		case combined != nil:
			r.State = destinationFailed
		default:
			r.State = destinationSucceeded
		}
		if combined != nil {
			r.Error = combined.Error()
			failures = append(failures, combined)
		}
		emit()
	}
	return results, errors.Join(append(failures, ctx.Err())...)
}
