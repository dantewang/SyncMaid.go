package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"syncmaid/internal/taskconfig"
)

const (
	historyLimit      = 100
	historyBytes      = 4 << 20
	historyErrorBytes = 1024
	historyGoalLimit  = 256
)

type runGoal struct {
	Started, Ended time.Time
	ID             taskconfig.ID
	Name           string
	State          destinationState
	Error          string
	Counts         transferProgress
}
type runRecord struct {
	ID, TaskID     taskconfig.ID
	Name           string
	Kind           taskconfig.TaskKind
	Origin         string
	Started, Ended time.Time
	Outcome        string
	Error          string
	Goals          []runGoal
}
type runHistoryData struct {
	Version int
	Latest  map[taskconfig.ID]runRecord
	Recent  []runRecord
}

func historyText(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	s = strings.ToValidUTF8(s, "�")
	if len(s) > n {
		s = s[:n]
	}
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
func newRunRecord(id taskconfig.ID, name string, q manualTaskRequest, automatic bool, now time.Time) (runRecord, error) {
	r := runRecord{ID: taskconfig.NewID(), TaskID: id, Name: historyText(name, 256), Kind: manualKind(q.Kind), Origin: "manual", Started: now, Outcome: "running"}
	if automatic {
		r.Origin = q.Trigger.Kind
	}
	if len(q.Destinations) > historyGoalLimit {
		return r, fmt.Errorf("运行记录每次最多支持%d个目标，文件执行不受影响", historyGoalLimit)
	}
	for _, d := range q.Destinations {
		r.Goals = append(r.Goals, runGoal{ID: d.ID, Name: historyText(d.Name, 256), State: destinationWaiting})
	}
	return r, nil
}
func finishRunRecord(r runRecord, results multipleResult, err error, now time.Time) runRecord {
	r.Ended = now
	r.Outcome = "succeeded"
	if errors.Is(err, context.Canceled) {
		r.Outcome = "stopped"
	} else if err != nil {
		r.Outcome = "failed"
	}
	if err != nil {
		r.Error = historyText(err.Error(), historyErrorBytes)
	}
	r.Goals = append([]runGoal(nil), r.Goals...)
	for i, g := range r.Goals {
		result, ok := results[g.ID]
		if !ok {
			g.State = destinationInterrupted
			g.Error = "没有记录此目标的最终状态，请核对文件"
			if r.Outcome == "succeeded" {
				r.Outcome = "failed"
				r.Error = "没有记录所有目标的最终状态"
			}
		} else {
			g.Started = result.Started
			g.Ended = result.Ended
			g.State = result.State
			g.Error = historyText(result.Error, historyErrorBytes)
			g.Counts = result.Progress
			g.Counts.Path = ""
			g.Counts.Stage = "" // Counts only, never a per-file trace.
			if result.State == destinationFailed && r.Outcome == "succeeded" {
				r.Outcome = "failed"
			}
		}
		r.Goals[i] = g
	}
	return r
}

// Diagnostic errors never replace the work result or cause work to run again.
func executeRecordedRun(h *runHistoryStore, start runRecord, startErr error, active []taskconfig.ID, work func() (multipleResult, error)) (runHistoryData, runRecord, multipleResult, error, error) {
	var historyErr error
	if startErr == nil && h != nil {
		historyErr = h.put(start, active)
	} else {
		historyErr = startErr
	}
	results, workErr := work()
	end := finishRunRecord(start, results, workErr, time.Now())
	if startErr == nil && h != nil {
		historyErr = errors.Join(historyErr, h.put(end, active))
	}
	if h == nil {
		return runHistoryData{}, end, results, workErr, historyErr
	}
	return h.data, end, results, workErr, historyErr
}
func runOutcome(r runRecord) string {
	switch r.Outcome {
	case "running":
		return "中断/结果未知（未记录结束）"
	case "succeeded":
		return "完成"
	case "stopped":
		return "已停止"
	default:
		return "失败"
	}
}
func historyResult(r runRecord) savedTaskResult {
	message := messagef("%s · %s", r.Started.Local().Format("2006-01-02 15:04:05"), messagef(runOutcome(r)))
	if r.Error != "" {
		message = messagef("%s · %s", message, r.Error)
	}
	results := multipleResult{}
	var progress transferProgress
	for _, g := range r.Goals {
		result := destinationResult{ID: g.ID, Name: g.Name, Started: g.Started, Ended: g.Ended, State: g.State, Progress: g.Counts, Error: g.Error}
		if r.Outcome == "running" {
			result.State = destinationInterrupted
			result.Error = "上次未记录结束，计数不是最终结果，请核对文件"
		}
		results[g.ID] = result
		progress = result.Progress
		progress.Stage = string(result.State)
	}
	return savedTaskResult{message: message, progress: progress, destinations: results, started: r.Started, ended: r.Ended}
}
func (a *application) restoreRunResults() {
	if a.results == nil {
		a.results = map[taskconfig.ID]savedTaskResult{}
	}
	if a.tasks == nil {
		return
	}
	known := map[taskconfig.ID]bool{}
	for _, t := range a.tasks.tasks {
		known[t.ID] = true
	}
	for id, r := range a.historyView.Latest {
		if !known[id] {
			continue
		}
		if _, current := a.results[id]; !current {
			a.results[id] = historyResult(r)
		}
	}
}
