package syncengine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"syncmaid/internal/filefilter"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/taskconfig"
)

type ManualDestination struct {
	Matcher                *filefilter.Matcher
	FilterError            error
	ID                     taskconfig.ID
	Name, Path             string
	Filters                []taskconfig.Rule
	Strategy               taskconfig.Strategy
	DeleteMode             taskconfig.DeleteMode
	VerifyContents         bool
	Flatten                bool
	Collision              taskconfig.CollisionPolicy
	MassDeleteThreshold    float64
	MassDeleteThresholdSet bool
}
type ManualTaskRequest struct {
	regexTask    *taskconfig.Task
	regexReviews map[taskconfig.ID]ReviewBundle
	Trigger      taskconfig.Trigger
	Kind         taskconfig.TaskKind
	Source       string
	Destinations []ManualDestination
}

func (q ManualTaskRequest) WithReviewedRegex(t *taskconfig.Task, reviews map[taskconfig.ID]ReviewBundle) ManualTaskRequest {
	q.regexTask = t
	q.regexReviews = reviews
	return q
}

type DestinationState string

const (
	DestinationWaiting     DestinationState = "未开始"
	DestinationRunning     DestinationState = "运行中"
	DestinationSucceeded   DestinationState = "完成"
	DestinationIncomplete  DestinationState = "部分完成"
	DestinationNoMatches   DestinationState = "无匹配"
	DestinationFailed      DestinationState = "失败"
	DestinationCanceled    DestinationState = "已停止"
	DestinationSkipped     DestinationState = "未执行"
	DestinationInterrupted DestinationState = "中断/结果未知"
)

type DestinationResult struct {
	Started, Ended time.Time
	ID             taskconfig.ID
	Name           string
	State          DestinationState
	Progress       TransferProgress
	Error          string
}
type MultipleResult map[taskconfig.ID]DestinationResult

func InitialDestinationResults(q ManualTaskRequest) MultipleResult {
	r := make(MultipleResult, len(q.Destinations))
	for _, d := range q.Destinations {
		r[d.ID] = DestinationResult{ID: d.ID, Name: d.Name, State: DestinationWaiting}
	}
	return r
}

type scanProgressKey struct{}

// WithScanProgress attaches an optional callback invoked during directory inventory scans.
func WithScanProgress(ctx context.Context, fn func(fileinventory.ScanProgress)) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, scanProgressKey{}, fn)
}

func scanProgressFromContext(ctx context.Context) func(fileinventory.ScanProgress) {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(scanProgressKey{}).(func(fileinventory.ScanProgress))
	return fn
}

// Each target runs in configuration order. A failure is recorded, not turned
// into success or a reason to skip healthy targets. Cancellation stops the rest.
// Events are value copies; the final map is handed over only after work ends.
func RunMultipleConfirmed(ctx context.Context, q ManualTaskRequest, notify func(DestinationResult), confirm MirrorConfirmation, recycle func(string) error) (results MultipleResult, err error) {
	if len(q.Destinations) == 0 {
		return MultipleResult{}, nil
	}
	q = PrepareReviewedRequest(q)
	if ManualKind(q.Kind) == taskconfig.Moving {
		return RunMove(ctx, q, notify)
	}
	results = InitialDestinationResults(q)
	plan, err := OpenMultiplePlan(ctx, q)
	if err != nil {
		for id, r := range results {
			r.Ended = time.Now()
			r.State = DestinationSkipped
			r.Progress.Stage = "检查目录"
			r.Error = err.Error()
			results[id] = r
		}
		return results, err
	}
	defer func() { err = errors.Join(err, plan.close(ctx)) }()
	var (
		failures      []error
		sourceScanned bool
		sourceInv     *fileinventory.Inventory
		sourceScanErr error
	)
	for _, d := range q.Destinations {
		r := results[d.ID]
		if e := ctx.Err(); e != nil {
			r.Ended = time.Now()
			r.State = DestinationSkipped
			r.Error = "已停止，未执行"
			results[d.ID] = r
			continue
		}
		if e := plan.failures[d.ID]; e != nil {
			r.State = DestinationFailed
			r.Progress.Stage = "检查目标目录"
			r.Error = e.Error()
			failures = append(failures, fmt.Errorf("%s: %w", d.Name, e))
		} else {
			r.Started = time.Now()
			r.State = DestinationRunning
			results[d.ID] = r
			if notify != nil {
				notify(r)
			}
			progress := func(p TransferProgress) {
				r.Progress = p
				results[d.ID] = r
				if notify != nil {
					notify(r)
				}
			}
			var p TransferProgress
			e := ValidateDestinationMode(d)
			if e == nil {
				e = d.FilterError
			}
			if e == nil {
				if !sourceScanned {
					sourceScanned = true
					r.Progress.Stage = "扫描源文件夹"
					if notify != nil {
						notify(r)
					}
					sourceInv, sourceScanErr = fileinventory.Scan(ctx, plan.source.Path, fileinventory.Options{
						MaxEntries: 200000,
						MaxDepth:   128,
						OnProgress: func(sp fileinventory.ScanProgress) {
							r.Progress.Stage = "扫描源文件夹"
							r.Progress.Path = sp.CurrentPath
							r.Progress.ScanFiles = sp.Files
							r.Progress.ScanDirs = sp.Dirs
							r.Progress.ScanBytes = sp.Bytes
							results[d.ID] = r
							if notify != nil {
								notify(r)
							}
						},
					})
				}
				if sourceScanErr != nil {
					e = sourceScanErr
				} else {
					roots := plan.takeTargetRoots(d.ID)
					if DestinationStrategy(d) == taskconfig.Mirror {
						p, e = RunMirrorSession(ctx, plan.requests[d.ID], d, roots, sourceInv, progress, confirm, nil, recycle)
					} else {
						p, e = RunManualWithSession(ctx, plan.requests[d.ID], roots, sourceInv, progress, TransferIO{Copy: TransferCopier(d.VerifyContents), Publish: PublishTransfer}, d.Filters, d.Matcher)
					}
					if roots != nil {
						e = errors.Join(e, roots.close(ctx))
					}
				}
			}
			r.Progress = p
			switch {
			case e == nil:
				if p.Deferred > 0 {
					r.State = DestinationIncomplete
				} else if p.Total == 0 && p.Removed == 0 && p.RemovedDirs == 0 {
					r.State = DestinationNoMatches
				} else {
					r.State = DestinationSucceeded
				}
			case errors.Is(e, context.Canceled) || ctx.Err() != nil:
				r.State = DestinationCanceled
				r.Error = e.Error()
			default:
				r.State = DestinationFailed
				r.Error = e.Error()
			}
			if e != nil {
				failures = append(failures, fmt.Errorf("%s: %w", d.Name, e))
			}
		}
		r.Ended = time.Now()
		results[d.ID] = r
		if notify != nil {
			notify(r)
		}
	}
	return results, errors.Join(append(failures, ctx.Err())...)
}
