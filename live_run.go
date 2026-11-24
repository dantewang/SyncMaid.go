package main

import (
	"fmt"
	"math"
	"sync"
	"syncmaid/internal/appcore"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
	"syncmaid/internal/winutil"
	"time"
)

const liveEventRingCapacity = 200

type liveEventLevel uint8

const (
	liveEventInfo liveEventLevel = iota
	liveEventWarn
	liveEventError
)

type liveEvent struct {
	At       time.Time
	Level    liveEventLevel
	DestID   taskconfig.ID
	DestName string
	Message  localizedMessage
}

// liveEventRing stores bounded recent run milestones in memory without disk writes.
type liveEventRing struct {
	entries [liveEventRingCapacity]liveEvent
	head    int
	count   int
}

func (r *liveEventRing) Push(ev liveEvent) {
	if r.count < liveEventRingCapacity {
		idx := (r.head + r.count) % liveEventRingCapacity
		r.entries[idx] = ev
		r.count++
		return
	}
	r.entries[r.head] = ev
	r.head = (r.head + 1) % liveEventRingCapacity
}

func (r *liveEventRing) Snapshot() []liveEvent {
	if r.count == 0 {
		return nil
	}
	out := make([]liveEvent, r.count)
	for i := 0; i < r.count; i++ {
		out[i] = r.entries[(r.head+i)%liveEventRingCapacity]
	}
	return out
}

type liveDestSnapshot struct {
	ID         taskconfig.ID
	Name       string
	TargetPath string
	Strategy   taskconfig.Strategy
	Result     syncengine.DestinationResult
	Fraction   float64
}

type liveRunSnapshot struct {
	Active           bool
	Finished         bool
	TaskID           taskconfig.ID
	TaskName         string
	Kind             taskconfig.TaskKind
	Origin           appcore.TriggerOrigin
	BatchIndex       int
	BatchTotal       int
	Started          time.Time
	Ended            time.Time
	Stopping         bool
	WaitingConfirm   bool
	CurrentDestID    taskconfig.ID
	CurrentDestIndex int
	TotalDests       int
	CurrentDestName  string
	Stage            string
	SubStage         string
	CurrentPath      string
	Fraction         float64
	Elapsed          time.Duration
	HasETA           bool
	ETA              time.Duration
	ETAMode          string // "", "verify", "paused", "scanning"
	ByteRateBps      float64
	Stalled          bool
	Progress         syncengine.TransferProgress
	Destinations     []liveDestSnapshot
	Results          syncengine.MultipleResult
	Events           []liveEvent
	Outcome          appcore.OutcomeState
	Error            string
}

type liveETAEstimator struct {
	destID             taskconfig.ID
	started            time.Time
	paused             bool
	pauseStart         time.Time
	pausedDur          time.Duration
	lastSampleAt       time.Time
	lastCopiedBytes    int64
	lastProcessedBytes int64
	lastDoneFiles      int
	lastRemoved        int
	ewmaByteRate       float64
	ewmaProcessedRate  float64
	ewmaFileRate       float64
	ewmaRemoveRate     float64
	lastETA            time.Duration
	hasLastETA         bool
	lastByteAt         time.Time
	prevBytes          int64
	prevProcessed      int64
}

func (e *liveETAEstimator) resetForDestination(id taskconfig.ID, now time.Time) {
	e.destID = id
	e.started = now
	e.paused = false
	e.pauseStart = time.Time{}
	e.pausedDur = 0
	e.lastSampleAt = now
	e.lastCopiedBytes = 0
	e.lastProcessedBytes = 0
	e.lastDoneFiles = 0
	e.lastRemoved = 0
	e.ewmaByteRate = 0
	e.ewmaProcessedRate = 0
	e.ewmaFileRate = 0
	e.ewmaRemoveRate = 0
	e.lastETA = 0
	e.hasLastETA = false
	e.lastByteAt = now
	e.prevBytes = 0
	e.prevProcessed = 0
}

func (e *liveETAEstimator) setPaused(paused bool, now time.Time) {
	if paused == e.paused {
		return
	}
	if paused {
		e.paused = true
		e.pauseStart = now
		return
	}
	e.paused = false
	if !e.pauseStart.IsZero() && now.After(e.pauseStart) {
		e.pausedDur += now.Sub(e.pauseStart)
	}
	e.pauseStart = time.Time{}
	e.lastSampleAt = now
	e.lastByteAt = now
}

func (e *liveETAEstimator) effectiveElapsed(now time.Time) time.Duration {
	if e.started.IsZero() {
		return 0
	}
	end := now
	if e.paused && !e.pauseStart.IsZero() {
		end = e.pauseStart
	}
	d := end.Sub(e.started) - e.pausedDur
	if d < 0 {
		return 0
	}
	return d
}

func (e *liveETAEstimator) observeBytes(p syncengine.TransferProgress, now time.Time) {
	if p.Bytes != e.prevBytes || p.ProcessedBytes != e.prevProcessed {
		e.prevBytes = p.Bytes
		e.prevProcessed = p.ProcessedBytes
		e.lastByteAt = now
	}
}

func destinationDoneFiles(p syncengine.TransferProgress) int {
	return p.Copied + p.Unchanged + p.Retained + p.Deferred + p.Failed
}

func isIndeterminateStage(stage string) bool {
	switch stage {
	case "", "检查目录", "检查目标目录", "扫描源文件夹", "扫描目标目录", "准备过滤":
		return true
	default:
		return false
	}
}

func computeProgressFraction(r syncengine.DestinationResult, stopping bool) float64 {
	if stopping {
		return -1
	}
	if r.State == syncengine.DestinationWaiting {
		return 0
	}
	if r.State != syncengine.DestinationRunning {
		return 1
	}
	p := r.Progress
	if isIndeterminateStage(p.Stage) {
		return -1
	}
	if p.Stage == "校验源文件身份" {
		if p.Total <= 0 {
			return -1
		}
		return clamp01(float64(p.Copied) / float64(p.Total))
	}
	if p.Stage == "等待删除确认" {
		return 1
	}
	if p.Stage == "移除多余文件" || p.Stage == "清理多余空目录" {
		totalRem := p.PlannedRemoveFiles + p.PlannedRemoveDirs
		if totalRem <= 0 {
			return -1
		}
		return clamp01(float64(p.Removed+p.RemovedDirs) / float64(totalRem))
	}
	// Mirror copy phase with exact planned bytes/files.
	if p.PlannedCopyFiles > 0 || p.PlannedCopyBytes > 0 {
		if p.PlannedCopyBytes > 0 {
			return clamp01(float64(p.Bytes) / float64(p.PlannedCopyBytes))
		}
		return clamp01(float64(p.Copied) / float64(p.PlannedCopyFiles))
	}
	// Add-only or Move phase.
	doneFiles := destinationDoneFiles(p)
	if p.Total <= 0 {
		if p.Stage == "无匹配文件" || p.Stage == "已完成" {
			return 1
		}
		return -1
	}
	fileSub := 0.0
	if p.CurrentFileSize > 0 {
		curRatio := clamp01(float64(p.CurrentFileDone) / float64(p.CurrentFileSize))
		if p.SubStage == "校验中" {
			fileSub = 0.70 + 0.30*curRatio
		} else if p.SubStage == "写入中" {
			fileSub = 0.70 * curRatio
		}
	}
	fileFrac := clamp01((float64(doneFiles) + fileSub) / float64(p.Total))
	if p.TotalBytes > 0 {
		byteFrac := clamp01(float64(p.ProcessedBytes) / float64(p.TotalBytes))
		return clamp01(0.85*byteFrac + 0.15*fileFrac)
	}
	return fileFrac
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func smoothEWMA(prev, sample, alpha float64) float64 {
	if sample <= 0 {
		return prev * (1 - alpha*0.5)
	}
	if prev <= 0 {
		return sample
	}
	return alpha*sample + (1-alpha)*prev
}

func roundDisplayETA(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	switch {
	case d >= 10*time.Minute:
		return d.Round(time.Minute)
	case d >= time.Minute:
		return d.Round(10 * time.Second)
	default:
		return d.Round(time.Second)
	}
}

func (e *liveETAEstimator) estimate(r syncengine.DestinationResult, frac float64, stopping bool, now time.Time) (hasETA bool, eta time.Duration, mode string, byteRate float64, stalled bool) {
	if stopping || r.State != syncengine.DestinationRunning {
		return false, 0, "", 0, false
	}
	p := r.Progress
	if e.paused || p.Stage == "等待删除确认" {
		return false, 0, "paused", 0, false
	}
	if isIndeterminateStage(p.Stage) || p.Stage == "校验源文件身份" {
		return false, 0, "scanning", 0, false
	}

	dt := now.Sub(e.lastSampleAt)
	doneFiles := destinationDoneFiles(p)
	removedTotal := p.Removed + p.RemovedDirs
	if dt >= 500*time.Millisecond {
		secs := dt.Seconds()
		e.ewmaByteRate = smoothEWMA(e.ewmaByteRate, float64(p.Bytes-e.lastCopiedBytes)/secs, 0.30)
		e.ewmaProcessedRate = smoothEWMA(e.ewmaProcessedRate, float64(p.ProcessedBytes-e.lastProcessedBytes)/secs, 0.30)
		e.ewmaFileRate = smoothEWMA(e.ewmaFileRate, float64(doneFiles-e.lastDoneFiles)/secs, 0.25)
		e.ewmaRemoveRate = smoothEWMA(e.ewmaRemoveRate, float64(removedTotal-e.lastRemoved)/secs, 0.25)
		e.lastSampleAt = now
		e.lastCopiedBytes = p.Bytes
		e.lastProcessedBytes = p.ProcessedBytes
		e.lastDoneFiles = doneFiles
		e.lastRemoved = removedTotal
	}

	if p.Stage == "同步中" || p.Stage == "移动中" {
		if !e.lastByteAt.IsZero() && now.Sub(e.lastByteAt) > 3*time.Second {
			stalled = true
		}
	}

	elapsed := e.effectiveElapsed(now)
	byteRate = e.ewmaByteRate

	// Warm-up gate prevents wild early ETA jumps.
	warmed := elapsed >= 2*time.Second && (doneFiles >= 5 || removedTotal >= 5 || p.Bytes >= 4<<20 || frac >= 0.02)
	if !warmed {
		return false, 0, "", byteRate, stalled
	}

	var rawSecs float64
	switch {
	case p.Stage == "移除多余文件" || p.Stage == "清理多余空目录":
		rem := (p.PlannedRemoveFiles + p.PlannedRemoveDirs) - removedTotal
		if rem <= 0 || e.ewmaRemoveRate <= 0 {
			return false, 0, "", byteRate, false
		}
		rawSecs = float64(rem) / e.ewmaRemoveRate

	case p.PlannedCopyFiles > 0 || p.PlannedCopyBytes > 0:
		remBytes := max(0, p.PlannedCopyBytes-p.Bytes)
		remFiles := max(0, p.PlannedCopyFiles-p.Copied)
		if remBytes == 0 && remFiles == 0 {
			return false, 0, "", byteRate, stalled
		}
		var byteEta, fileEta float64
		if remBytes > 0 && e.ewmaByteRate > 0 {
			byteEta = float64(remBytes) / e.ewmaByteRate
		}
		if remFiles > 0 && e.ewmaFileRate > 0 {
			fileEta = float64(remFiles) / e.ewmaFileRate
		}
		avgSize := float64(remBytes) / float64(max(remFiles, 1))
		if avgSize >= 64*1024 && byteEta > 0 {
			rawSecs = byteEta
		} else {
			rawSecs = math.Max(byteEta, fileEta)
		}

	default:
		// Add-only or Move
		remBytes := max(0, p.TotalBytes-p.ProcessedBytes)
		remFiles := max(0, p.Total-doneFiles)
		if remBytes == 0 && remFiles == 0 {
			return false, 0, "", byteRate, stalled
		}
		if p.Copied == 0 && p.Moved == 0 {
			mode = "verify"
			if remFiles > 0 && e.ewmaFileRate > 0 {
				rawSecs = float64(remFiles) / e.ewmaFileRate
			}
		} else {
			var byteEta, fileEta float64
			if remBytes > 0 && e.ewmaProcessedRate > 0 {
				byteEta = float64(remBytes) / e.ewmaProcessedRate
			}
			if remFiles > 0 && e.ewmaFileRate > 0 {
				fileEta = float64(remFiles) / e.ewmaFileRate
			}
			rawSecs = math.Max(byteEta, fileEta)
		}
	}

	if rawSecs <= 0 || math.IsNaN(rawSecs) || math.IsInf(rawSecs, 0) {
		return false, 0, mode, byteRate, stalled
	}
	candidate := time.Duration(rawSecs * float64(time.Second))
	if e.hasLastETA && e.lastETA > 0 {
		low := time.Duration(float64(e.lastETA) * 0.75)
		high := time.Duration(float64(e.lastETA) * 1.25)
		if candidate < low {
			candidate = low
		} else if candidate > high {
			candidate = high
		}
	}
	e.lastETA = candidate
	e.hasLastETA = true
	return true, roundDisplayETA(candidate), mode, byteRate, stalled
}

type liveRunTracker struct {
	mu             sync.Mutex
	taskID         taskconfig.ID
	taskName       string
	kind           taskconfig.TaskKind
	origin         appcore.TriggerOrigin
	batchIndex     int
	batchTotal     int
	started        time.Time
	ended          time.Time
	active         bool
	finished       bool
	stopping       bool
	waitingConfirm bool
	outcome        appcore.OutcomeState
	errText        string
	destOrder      []taskconfig.ID
	destMeta       map[taskconfig.ID]syncengine.ManualDestination
	results        syncengine.MultipleResult
	currentDestID  taskconfig.ID
	lastStage      map[taskconfig.ID]string
	lastState      map[taskconfig.ID]syncengine.DestinationState
	lastDeferred   map[taskconfig.ID]int
	lastFailed     map[taskconfig.ID]int
	lastScanLogged bool
	ring           liveEventRing
	eta            liveETAEstimator
}

func newLiveRunTracker(id taskconfig.ID, name string, q syncengine.ManualTaskRequest, automatic bool, batchIndex, batchTotal int, now time.Time) *liveRunTracker {
	origin := appcore.OriginManual
	if automatic {
		origin = appcore.TriggerOrigin(q.Trigger.Kind)
		if origin == "" {
			origin = appcore.OriginWatch
		}
	}
	order := make([]taskconfig.ID, 0, len(q.Destinations))
	meta := make(map[taskconfig.ID]syncengine.ManualDestination, len(q.Destinations))
	for _, d := range q.Destinations {
		order = append(order, d.ID)
		meta[d.ID] = d
	}
	t := &liveRunTracker{
		taskID:       id,
		taskName:     name,
		kind:         syncengine.ManualKind(q.Kind),
		origin:       origin,
		batchIndex:   batchIndex,
		batchTotal:   batchTotal,
		started:      now,
		active:       true,
		destOrder:    order,
		destMeta:     meta,
		results:      syncengine.InitialDestinationResults(q),
		lastStage:    make(map[taskconfig.ID]string, len(q.Destinations)),
		lastState:    make(map[taskconfig.ID]syncengine.DestinationState, len(q.Destinations)),
		lastDeferred: make(map[taskconfig.ID]int, len(q.Destinations)),
		lastFailed:   make(map[taskconfig.ID]int, len(q.Destinations)),
	}
	if len(order) > 0 {
		t.currentDestID = order[0]
		t.eta.resetForDestination(order[0], now)
	}
	for did, res := range t.results {
		t.lastState[did] = res.State
	}
	t.ring.Push(liveEvent{
		At:      now,
		Level:   liveEventInfo,
		Message: messagef("开始运行任务：%s", name),
	})
	return t
}

func (t *liveRunTracker) UpdateDestination(r syncengine.DestinationResult) (milestone bool) {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()

	t.results[r.ID] = r
	if r.State == syncengine.DestinationRunning || r.Progress.Stage != "" {
		if t.currentDestID != r.ID {
			t.currentDestID = r.ID
			t.eta.resetForDestination(r.ID, now)
			milestone = true
		}
	}
	t.eta.observeBytes(r.Progress, now)

	prevState := t.lastState[r.ID]
	prevStage := t.lastStage[r.ID]
	prevDef := t.lastDeferred[r.ID]
	prevFail := t.lastFailed[r.ID]

	if r.Progress.Stage == "等待删除确认" {
		if !t.waitingConfirm {
			t.waitingConfirm = true
			t.eta.setPaused(true, now)
			milestone = true
		}
	} else if t.waitingConfirm {
		t.waitingConfirm = false
		t.eta.setPaused(false, now)
		milestone = true
	}

	if prevState != r.State {
		t.lastState[r.ID] = r.State
		milestone = true
		switch r.State {
		case syncengine.DestinationRunning:
			t.ring.Push(liveEvent{
				At:       now,
				Level:    liveEventInfo,
				DestID:   r.ID,
				DestName: r.Name,
				Message:  messagef("开始目标：%s", r.Name),
			})
		case syncengine.DestinationSucceeded, syncengine.DestinationNoMatches:
			t.ring.Push(liveEvent{
				At:       now,
				Level:    liveEventInfo,
				DestID:   r.ID,
				DestName: r.Name,
				Message:  messagef("完成目标：%s", r.Name),
			})
		case syncengine.DestinationIncomplete:
			t.ring.Push(liveEvent{
				At:       now,
				Level:    liveEventWarn,
				DestID:   r.ID,
				DestName: r.Name,
				Message:  messagef("部分完成目标 %s（延迟 %d 个）", r.Name, r.Progress.Deferred),
			})
		case syncengine.DestinationFailed:
			t.ring.Push(liveEvent{
				At:       now,
				Level:    liveEventError,
				DestID:   r.ID,
				DestName: r.Name,
				Message:  messagef("目标失败 %s: %s", r.Name, r.Error),
			})
		case syncengine.DestinationCanceled, syncengine.DestinationSkipped:
			t.ring.Push(liveEvent{
				At:       now,
				Level:    liveEventWarn,
				DestID:   r.ID,
				DestName: r.Name,
				Message:  messagef("目标已停止或跳过：%s", r.Name),
			})
		}
	}

	if prevStage != r.Progress.Stage && r.Progress.Stage != "" {
		t.lastStage[r.ID] = r.Progress.Stage
		milestone = true
		switch r.Progress.Stage {
		case "同步中":
			if !t.lastScanLogged && r.Progress.SourceFiles > 0 {
				t.lastScanLogged = true
				t.ring.Push(liveEvent{
					At:       now,
					Level:    liveEventInfo,
					DestID:   r.ID,
					DestName: r.Name,
					Message:  messagef("源扫描完成：%d 个文件", r.Progress.SourceFiles),
				})
			}
			t.ring.Push(liveEvent{
				At:       now,
				Level:    liveEventInfo,
				DestID:   r.ID,
				DestName: r.Name,
				Message:  messagef("%s：匹配 %d 个文件 (%s)", r.Name, r.Progress.Total, formatBytesHuman(r.Progress.TotalBytes)),
			})
		case "等待删除确认":
			t.ring.Push(liveEvent{
				At:       now,
				Level:    liveEventWarn,
				DestID:   r.ID,
				DestName: r.Name,
				Message:  messagef("%s：等待删除确认（待移除 %d 个文件）", r.Name, r.Progress.PlannedRemoveFiles),
			})
		case "移除多余文件":
			t.ring.Push(liveEvent{
				At:       now,
				Level:    liveEventInfo,
				DestID:   r.ID,
				DestName: r.Name,
				Message:  messagef("%s：开始移除 %d 个多余文件", r.Name, r.Progress.PlannedRemoveFiles),
			})
		}
	}

	if r.Progress.Deferred > prevDef {
		t.lastDeferred[r.ID] = r.Progress.Deferred
		milestone = true
		pathSample := r.Progress.Path
		if n := len(r.Progress.DeferredPaths); n > 0 {
			pathSample = r.Progress.DeferredPaths[n-1]
		}
		t.ring.Push(liveEvent{
			At:       now,
			Level:    liveEventWarn,
			DestID:   r.ID,
			DestName: r.Name,
			Message:  messagef("延迟文件（被占用或变化）：%s", pathSample),
		})
	}
	if r.Progress.Failed > prevFail {
		t.lastFailed[r.ID] = r.Progress.Failed
		milestone = true
		t.ring.Push(liveEvent{
			At:       now,
			Level:    liveEventError,
			DestID:   r.ID,
			DestName: r.Name,
			Message:  messagef("文件失败：%s", r.Progress.Path),
		})
	}
	return milestone
}

func (t *liveRunTracker) SetPaused(paused bool, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.waitingConfirm = paused
	t.eta.setPaused(paused, now)
}

func (t *liveRunTracker) MarkStopping(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopping {
		return
	}
	t.stopping = true
	t.waitingConfirm = false
	t.eta.setPaused(false, now)
	t.ring.Push(liveEvent{
		At:      now,
		Level:   liveEventWarn,
		Message: messagef("用户请求停止，正在安全清理临时文件…"),
	})
}

func (t *liveRunTracker) Finish(results syncengine.MultipleResult, err error, outcome appcore.OutcomeState, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.active = false
	t.finished = true
	t.stopping = false
	t.waitingConfirm = false
	t.ended = now
	t.outcome = outcome
	if err != nil {
		t.errText = err.Error()
	}
	for id, res := range results {
		t.results[id] = res
	}
	switch outcome {
	case appcore.OutcomeSucceeded:
		t.ring.Push(liveEvent{At: now, Level: liveEventInfo, Message: messagef("全部目标同步完成")})
	case appcore.OutcomeStopped:
		t.ring.Push(liveEvent{At: now, Level: liveEventWarn, Message: messagef("任务已停止；已完成目标不回滚")})
	default:
		t.ring.Push(liveEvent{At: now, Level: liveEventError, Message: messagef("运行未完成: %s", t.errText)})
	}
}

func (t *liveRunTracker) Snapshot(now time.Time) liveRunSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()

	elapsed := now.Sub(t.started)
	if t.finished && !t.ended.IsZero() {
		elapsed = t.ended.Sub(t.started)
	}
	if elapsed < 0 {
		elapsed = 0
	}

	dests := make([]liveDestSnapshot, 0, len(t.destOrder))
	resultsCopy := make(syncengine.MultipleResult, len(t.results))
	curIdx := 0
	for idx, id := range t.destOrder {
		meta := t.destMeta[id]
		res := t.results[id]
		if res.ID == "" {
			res.ID = id
			res.Name = meta.Name
		}
		resultsCopy[id] = res
		if id == t.currentDestID {
			curIdx = idx
		}
		dests = append(dests, liveDestSnapshot{
			ID:         id,
			Name:       meta.Name,
			TargetPath: winutil.PathLabel(meta.Path),
			Strategy:   meta.Strategy,
			Result:     res,
			Fraction:   computeProgressFraction(res, t.stopping),
		})
	}

	curRes := t.results[t.currentDestID]
	curName := t.destMeta[t.currentDestID].Name
	if curRes.Name != "" {
		curName = curRes.Name
	}

	frac := computeProgressFraction(curRes, t.stopping)
	hasETA, eta, etaMode, byteRate, stalled := t.eta.estimate(curRes, frac, t.stopping, now)
	if t.waitingConfirm {
		etaMode = "paused"
		hasETA = false
	}
	stage := curRes.Progress.Stage
	if t.stopping {
		stage = "正在停止"
	} else if t.waitingConfirm {
		stage = "等待删除确认"
	} else if stage == "" && t.active {
		stage = "检查目录"
	}

	return liveRunSnapshot{
		Active:           t.active,
		Finished:         t.finished,
		TaskID:           t.taskID,
		TaskName:         t.taskName,
		Kind:             t.kind,
		Origin:           t.origin,
		BatchIndex:       t.batchIndex,
		BatchTotal:       t.batchTotal,
		Started:          t.started,
		Ended:            t.ended,
		Stopping:         t.stopping,
		WaitingConfirm:   t.waitingConfirm,
		CurrentDestID:    t.currentDestID,
		CurrentDestIndex: curIdx,
		TotalDests:       len(t.destOrder),
		CurrentDestName:  curName,
		Stage:            stage,
		SubStage:         curRes.Progress.SubStage,
		CurrentPath:      curRes.Progress.Path,
		Fraction:         frac,
		Elapsed:          elapsed,
		HasETA:           hasETA,
		ETA:              eta,
		ETAMode:          etaMode,
		ByteRateBps:      byteRate,
		Stalled:          stalled,
		Progress:         curRes.Progress,
		Destinations:     dests,
		Results:          resultsCopy,
		Events:           t.ring.Snapshot(),
		Outcome:          t.outcome,
		Error:            t.errText,
	}
}

func formatElapsedMMSS(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func formatETADuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("~%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("~%02d:%02d", m, s)
}

func formatBytesHuman(b int64) string {
	if b < 0 {
		b = 0
	}
	const (
		kb = 1024.0
		mb = 1024.0 * kb
		gb = 1024.0 * mb
	)
	fb := float64(b)
	switch {
	case fb >= gb:
		return fmt.Sprintf("%.2f GB", fb/gb)
	case fb >= mb:
		return fmt.Sprintf("%.1f MB", fb/mb)
	case fb >= kb:
		return fmt.Sprintf("%.1f KB", fb/kb)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func formatRateHuman(bps float64) string {
	if bps <= 0 {
		return "0 KB/s"
	}
	const (
		kb = 1024.0
		mb = 1024.0 * kb
	)
	if bps >= mb {
		return fmt.Sprintf("%.1f MB/s", bps/mb)
	}
	return fmt.Sprintf("%.0f KB/s", bps/kb)
}
