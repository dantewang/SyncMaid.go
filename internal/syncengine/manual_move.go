package syncengine

import (
	"context"
	"errors"
	"fmt"
	"path"
	"syncmaid/internal/filefilter"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/taskconfig"
	"time"
)

func ManualKind(k taskconfig.TaskKind) taskconfig.TaskKind {
	if k == "" {
		return taskconfig.Sync
	}
	return k
}
func ValidateMoveDestination(d ManualDestination) error {
	if d.Strategy != taskconfig.Move {
		return errors.New("Move任务只能使用Move目标")
	}
	if d.Collision != "" && d.Collision != taskconfig.Skip && d.Collision != taskconfig.Suffix {
		return errors.New("Move仅支持Skip或Suffix冲突策略")
	}
	return nil
}

// Compile every target before matching anything. First match owns the file,
// including when its destination later fails or skips a collision.
func PrepareMoveRoutes(ctx context.Context, files []fileinventory.File, destinations []ManualDestination) ([][]fileinventory.File, int, error) {
	matchers := make([]*filefilter.Matcher, len(destinations))
	for i, d := range destinations {
		if d.FilterError != nil {
			return nil, 0, d.FilterError
		}
		if e := ValidateMoveDestination(d); e != nil {
			return nil, 0, e
		}
		m := d.Matcher
		var e error
		if m == nil {
			m, e = filefilter.Compile(d.Filters)
		}
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

func RunMove(ctx context.Context, q ManualTaskRequest, notify func(DestinationResult)) (results MultipleResult, err error) {
	if len(q.Destinations) == 0 {
		return MultipleResult{}, nil
	}
	results = InitialDestinationResults(q)
	failAll := func(e error) (MultipleResult, error) {
		for id, r := range results {
			r.Ended = time.Now()
			r.State = DestinationFailed
			r.Error = e.Error()
			results[id] = r
		}
		return results, e
	}
	plan, e := OpenMultiplePlan(ctx, q)
	if e != nil {
		return failAll(e)
	}
	defer func() { err = errors.Join(err, plan.close(ctx)) }()
	firstID := q.Destinations[0].ID
	scanCtx := WithScanProgress(ctx, func(sp fileinventory.ScanProgress) {
		r := results[firstID]
		r.Progress.Stage = "扫描源文件夹"
		r.Progress.Path = sp.CurrentPath
		r.Progress.ScanFiles = sp.Files
		r.Progress.ScanDirs = sp.Dirs
		r.Progress.ScanBytes = sp.Bytes
		results[firstID] = r
		if notify != nil {
			notify(r)
		}
	})
	scanCtx = withMoveStampProgress(scanCtx, func(done, total int, rel string) {
		r := results[firstID]
		r.Progress.Stage = "校验源文件身份"
		r.Progress.Path = rel
		r.Progress.SourceFiles = total
		r.Progress.Total = total
		r.Progress.Copied = done
		results[firstID] = r
		if notify != nil {
			notify(r)
		}
	})
	files, stamps, e := scanMoveFiles(scanCtx, plan.source)
	if e != nil {
		return failAll(e)
	}
	routes, unmatched, e := PrepareMoveRoutes(ctx, files, q.Destinations)
	if e != nil {
		return failAll(e)
	}
	var failures []error
	for i, d := range q.Destinations {
		r := results[d.ID]
		var totalBytes int64
		for _, f := range routes[i] {
			totalBytes += f.Size
		}
		r.Progress = TransferProgress{SourceFiles: len(files), Total: len(routes[i]), TotalBytes: totalBytes, Unmatched: unmatched}
		emit := func() {
			if r.State != DestinationRunning && r.State != DestinationWaiting && r.Ended.IsZero() {
				r.Ended = time.Now()
			}
			results[d.ID] = r
			if notify != nil {
				notify(r)
			}
		}
		if ctx.Err() != nil {
			r.State = DestinationSkipped
			r.Progress.Retained = len(routes[i])
			emit()
			continue
		}
		if e := plan.failures[d.ID]; e != nil {
			r.Ended = time.Now()
			r.State = DestinationFailed
			r.Error = e.Error()
			r.Progress.Failed = len(routes[i])
			r.Progress.Retained = len(routes[i])
			failures = append(failures, e)
			emit()
			continue
		}
		if len(routes[i]) == 0 {
			r.State = DestinationNoMatches
			emit()
			continue
		}
		roots := plan.takeTargetRoots(d.ID)
		var e error
		if roots == nil {
			roots, e = OpenTransferRoots(ctx, plan.requests[d.ID])
		} else {
			e = roots.ensureWritableTarget(ctx)
		}
		if e != nil {
			if roots != nil {
				e = errors.Join(e, roots.close(ctx))
			}
			r.Ended = time.Now()
			r.State = DestinationFailed
			r.Error = e.Error()
			r.Progress.Retained = len(routes[i])
			failures = append(failures, e)
			emit()
			continue
		}
		r.Started = time.Now()
		r.State = DestinationRunning
		emit()
		var targetErrors []error
		for j, f := range routes[i] {
			if ctx.Err() != nil {
				r.Progress.Retained += len(routes[i]) - j
				break
			}
			r.Progress.Stage = "移动中"
			r.Progress.SubStage = "写入中"
			r.Progress.Path = f.Path
			r.Progress.CurrentFileSize = f.Size
			r.Progress.CurrentFileDone = 0
			emit()
			rel := f.Path
			if d.Flatten {
				rel = path.Base(rel)
			}
			var fileWritten int64
			var verifyDone int64
			moveCtx := WithVerifyProgress(ctx, func(n int64) {
				verifyDone += n
				r.Progress.SubStage = "校验中"
				r.Progress.CurrentFileDone = verifyDone
				emit()
			})
			published, e := moveFile(moveCtx, roots, f, rel, stamps[f.Path], d.Collision, d.VerifyContents, func(n int64) {
				fileWritten += n
				r.Progress.Bytes += n
				r.Progress.ProcessedBytes += n
				r.Progress.CurrentFileDone = fileWritten
				r.Progress.SubStage = "写入中"
				emit()
			})
			if ctx.Err() == nil && f.Size > fileWritten {
				r.Progress.ProcessedBytes += f.Size - fileWritten
			}
			r.Progress.CurrentFileSize = 0
			r.Progress.CurrentFileDone = 0
			r.Progress.SubStage = ""
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
		e = roots.close(ctx)
		targetErrors = append(targetErrors, e)
		combined := errors.Join(targetErrors...)
		switch {
		case ctx.Err() != nil:
			r.State = DestinationCanceled
		case combined != nil:
			r.State = DestinationFailed
		default:
			r.State = DestinationSucceeded
		}
		if combined != nil {
			r.Error = combined.Error()
			failures = append(failures, combined)
		}
		emit()
	}
	return results, errors.Join(append(failures, ctx.Err())...)
}
