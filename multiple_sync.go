package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"syncmaid/internal/filefilter"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/taskconfig"
)

type manualDestination struct {
	matcher        *filefilter.Matcher
	filterError    error
	ID             taskconfig.ID
	Name, Path     string
	Filters        []taskconfig.Rule
	Strategy       taskconfig.Strategy
	DeleteMode     taskconfig.DeleteMode
	VerifyContents bool
	Flatten        bool
	Collision      taskconfig.CollisionPolicy
}
type manualTaskRequest struct {
	regexTask    *taskconfig.Task
	regexReviews map[taskconfig.ID]reviewBundle
	Trigger      taskconfig.Trigger
	Kind         taskconfig.TaskKind
	Source       string
	Destinations []manualDestination
}
type destinationState string

const (
	destinationWaiting     destinationState = "未开始"
	destinationRunning     destinationState = "运行中"
	destinationSucceeded   destinationState = "完成"
	destinationNoMatches   destinationState = "无匹配"
	destinationFailed      destinationState = "失败"
	destinationCanceled    destinationState = "已停止"
	destinationSkipped     destinationState = "未执行"
	destinationInterrupted destinationState = "中断/结果未知"
)

type destinationResult struct {
	Started, Ended time.Time
	ID             taskconfig.ID
	Name           string
	State          destinationState
	Progress       transferProgress
	Error          string
}
type multipleResult map[taskconfig.ID]destinationResult

func initialDestinationResults(q manualTaskRequest) multipleResult {
	r := make(multipleResult, len(q.Destinations))
	for _, d := range q.Destinations {
		r[d.ID] = destinationResult{ID: d.ID, Name: d.Name, State: destinationWaiting}
	}
	return r
}

// Each target runs in configuration order. A failure is recorded, not turned
// into success or a reason to skip healthy targets. Cancellation stops the rest.
// Events are value copies; the final map is handed over only after work ends.
func runMultipleConfirmed(ctx context.Context, q manualTaskRequest, notify func(destinationResult), confirm mirrorConfirmation) (results multipleResult, err error) {
	if len(q.Destinations) == 0 {
		return multipleResult{}, nil
	}
	q = prepareReviewedRequest(q)
	if manualKind(q.Kind) == taskconfig.Moving {
		return runMove(ctx, q, notify)
	}
	results = initialDestinationResults(q)
	plan, err := openMultiplePlan(ctx, q)
	if err != nil {
		for id, r := range results {
			r.Ended = time.Now()
			r.State = destinationSkipped
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
			r.State = destinationSkipped
			r.Error = "已停止，未执行"
			results[d.ID] = r
			continue
		}
		if e := plan.failures[d.ID]; e != nil {
			r.State = destinationFailed
			r.Error = e.Error()
			failures = append(failures, fmt.Errorf("%s: %w", d.Name, e))
		} else {
			r.Started = time.Now()
			r.State = destinationRunning
			results[d.ID] = r
			if notify != nil {
				notify(r)
			}
			progress := func(p transferProgress) {
				r.Progress = p
				results[d.ID] = r
				if notify != nil {
					notify(r)
				}
			}
			var p transferProgress
			e := validateDestinationMode(d)
			if e == nil {
				e = d.filterError
			}
			if e == nil {
				if !sourceScanned {
					sourceScanned = true
					sourceInv, sourceScanErr = fileinventory.Scan(ctx, plan.source.Path, fileinventory.Options{MaxEntries: 200000, MaxDepth: 128})
				}
				if sourceScanErr != nil {
					e = sourceScanErr
				} else {
					roots := plan.takeTargetRoots(d.ID)
					if destinationStrategy(d) == taskconfig.Mirror {
						p, e = runMirrorSession(ctx, plan.requests[d.ID], d, roots, sourceInv, progress, confirm, nil, recycleMirrorFile)
					} else {
						p, e = runManualWithSession(ctx, plan.requests[d.ID], roots, sourceInv, progress, transferIO{copy: transferCopier(d.VerifyContents), publish: publishTransfer}, d.Filters, d.matcher)
					}
					if roots != nil {
						e = errors.Join(e, roots.close(ctx))
					}
				}
			}
			r.Progress = p
			switch {
			case e == nil:
				if p.Total == 0 {
					r.State = destinationNoMatches
				} else {
					r.State = destinationSucceeded
				}
			case errors.Is(e, context.Canceled) || ctx.Err() != nil:
				r.State = destinationCanceled
				r.Error = e.Error()
			default:
				r.State = destinationFailed
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
