package appcore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
	"syncmaid/internal/winutil"
)

const (
	HistoryLimit      = 100
	HistoryBytes      = 4 << 20
	HistoryErrorBytes = 1024
	HistoryPathBytes  = 512
	HistoryStageBytes = 128
	HistoryGoalLimit  = 256
)

type OutcomeState string

const (
	OutcomeRunning   OutcomeState = "running"
	OutcomeSucceeded OutcomeState = "succeeded"
	OutcomeFailed    OutcomeState = "failed"
	OutcomeStopped   OutcomeState = "stopped"
)

type TriggerOrigin string

const (
	OriginManual    TriggerOrigin = "manual"
	OriginWatch     TriggerOrigin = "watch"
	OriginScheduled TriggerOrigin = "scheduled"
)

type RunGoal struct {
	Started, Ended time.Time
	ID             taskconfig.ID
	Name           string
	TargetPath     string `json:",omitempty"`
	Stage          string `json:",omitempty"`
	State          syncengine.DestinationState
	Error          string
	Counts         syncengine.TransferProgress
}

type RunRecord struct {
	ID, TaskID     taskconfig.ID
	Name           string
	Kind           taskconfig.TaskKind
	Origin         TriggerOrigin
	SourcePath     string `json:",omitempty"`
	Started, Ended time.Time
	Outcome        OutcomeState
	Error          string
	Goals          []RunGoal
}

type RunHistoryData struct {
	Version int
	Latest  map[taskconfig.ID]RunRecord
	Recent  []RunRecord
}

func HistoryText(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	s = strings.ToValidUTF8(s, "\ufffd")
	if len(s) > n {
		s = s[:n]
	}
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func NewRunRecord(id taskconfig.ID, name string, q syncengine.ManualTaskRequest, automatic bool, now time.Time) (RunRecord, error) {
	r := RunRecord{
		ID:         taskconfig.NewID(),
		TaskID:     id,
		Name:       HistoryText(name, 256),
		Kind:       syncengine.ManualKind(q.Kind),
		Origin:     OriginManual,
		SourcePath: HistoryText(winutil.PathLabel(q.Source), HistoryPathBytes),
		Started:    now,
		Outcome:    OutcomeRunning,
	}
	if automatic {
		r.Origin = TriggerOrigin(q.Trigger.Kind)
	}
	if len(q.Destinations) > HistoryGoalLimit {
		return r, fmt.Errorf("运行记录每次最多支持%d个目标，文件执行不受影响", HistoryGoalLimit)
	}
	for _, d := range q.Destinations {
		r.Goals = append(r.Goals, RunGoal{
			ID:         d.ID,
			Name:       HistoryText(d.Name, 256),
			TargetPath: HistoryText(winutil.PathLabel(d.Path), HistoryPathBytes),
			State:      syncengine.DestinationWaiting,
		})
	}
	return r, nil
}

func FinishRunRecord(r RunRecord, results syncengine.MultipleResult, err error, now time.Time) RunRecord {
	r.Ended = now
	r.Outcome = OutcomeSucceeded
	if errors.Is(err, context.Canceled) {
		r.Outcome = OutcomeStopped
	} else if err != nil {
		r.Outcome = OutcomeFailed
	}
	if err != nil {
		r.Error = HistoryText(err.Error(), HistoryErrorBytes)
	}
	r.Goals = append([]RunGoal(nil), r.Goals...)
	for i, g := range r.Goals {
		result, ok := results[g.ID]
		if !ok {
			g.State = syncengine.DestinationInterrupted
			g.Error = "没有记录此目标的最终状态，请核对文件"
			if r.Outcome == OutcomeSucceeded {
				r.Outcome = OutcomeFailed
				r.Error = "没有记录所有目标的最终状态"
			}
		} else {
			g.Started = result.Started
			g.Ended = result.Ended
			g.State = result.State
			if stage := strings.TrimSpace(result.Progress.Stage); stage != "" && stage != string(result.State) {
				g.Stage = HistoryText(stage, HistoryStageBytes)
			}
			g.Error = HistoryText(result.Error, HistoryErrorBytes)
			g.Counts = result.Progress
			g.Counts.Path = ""
			g.Counts.Stage = "" // Counts only, never a per-file trace.
			g.Counts.DeferredPaths = nil
			if result.State == syncengine.DestinationFailed && r.Outcome == OutcomeSucceeded {
				r.Outcome = OutcomeFailed
			}
		}
		r.Goals[i] = g
	}
	return r
}

// Diagnostic errors never replace the work result or cause work to run again.
func ExecuteRecordedRun(h *RunHistoryStore, start RunRecord, startErr error, active []taskconfig.ID, work func() (syncengine.MultipleResult, error)) (RunHistoryData, RunRecord, syncengine.MultipleResult, error, error) {
	var historyErr error
	if startErr == nil && h != nil {
		historyErr = h.Put(start, active)
	} else {
		historyErr = startErr
	}
	results, workErr := work()
	end := FinishRunRecord(start, results, workErr, time.Now())
	if startErr == nil && h != nil {
		historyErr = errors.Join(historyErr, h.Put(end, active))
	}
	if h == nil {
		return RunHistoryData{}, end, results, workErr, historyErr
	}
	return h.Data, end, results, workErr, historyErr
}

func RunOutcome(r RunRecord) string {
	switch r.Outcome {
	case OutcomeRunning:
		return "中断/结果未知（未记录结束）"
	case OutcomeSucceeded:
		return "完成"
	case OutcomeStopped:
		return "已停止"
	default:
		return "失败"
	}
}
