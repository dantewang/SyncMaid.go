package main

import (
	"context"
	"errors"
	"fmt"

	"syncmaid/internal/taskconfig"
)

type manualDestination struct {
	ID         taskconfig.ID
	Name, Path string
	Filters    []taskconfig.Rule
}
type manualTaskRequest struct {
	Source       string
	Destinations []manualDestination
}
type destinationState string

const (
	destinationWaiting   destinationState = "未开始"
	destinationRunning   destinationState = "运行中"
	destinationSucceeded destinationState = "完成"
	destinationNoMatches destinationState = "无匹配"
	destinationFailed    destinationState = "失败"
	destinationCanceled  destinationState = "已停止"
	destinationSkipped   destinationState = "未执行"
)

type destinationResult struct {
	ID       taskconfig.ID
	Name     string
	State    destinationState
	Progress transferProgress
	Error    string
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
func runMultiple(ctx context.Context, q manualTaskRequest, notify func(destinationResult)) (results multipleResult, err error) {
	results = initialDestinationResults(q)
	plan, err := openMultiplePlan(ctx, q)
	if err != nil {
		for id, r := range results {
			r.State = destinationSkipped
			r.Error = err.Error()
			results[id] = r
		}
		return results, err
	}
	defer func() { err = errors.Join(err, protectedTransferIO(ctx, plan.Close)) }()
	var failures []error
	for _, d := range q.Destinations {
		r := results[d.ID]
		if e := ctx.Err(); e != nil {
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
			r.State = destinationRunning
			results[d.ID] = r
			if notify != nil {
				notify(r)
			}
			p, e := runManualFiltered(ctx, plan.requests[d.ID], d.Filters, func(p transferProgress) {
				r.Progress = p
				results[d.ID] = r
				if notify != nil {
					notify(r)
				}
			})
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
		results[d.ID] = r
		if notify != nil {
			notify(r)
		}
	}
	return results, errors.Join(append(failures, ctx.Err())...)
}
