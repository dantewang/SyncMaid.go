package main

import (
	"errors"
	"syncmaid/internal/appcore"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
	"testing"
	"time"
)

func TestLiveEventRingWrapAround(t *testing.T) {
	var ring liveEventRing
	for i := 0; i < liveEventRingCapacity+25; i++ {
		ring.Push(liveEvent{
			At:      time.Unix(int64(i), 0),
			Level:   liveEventInfo,
			Message: messagef("事件 %d", i),
		})
	}
	snap := ring.Snapshot()
	if len(snap) != liveEventRingCapacity {
		t.Fatalf("len = %d, want %d", len(snap), liveEventRingCapacity)
	}
	if snap[0].At.Unix() != 25 || snap[len(snap)-1].At.Unix() != liveEventRingCapacity+24 {
		t.Fatalf("unexpected ring bounds: first=%d last=%d", snap[0].At.Unix(), snap[len(snap)-1].At.Unix())
	}
}

func TestComputeProgressFractionMonotonicAcrossModes(t *testing.T) {
	id := taskconfig.NewID()

	// Indeterminate stages return < 0.
	for _, st := range []string{"", "检查目录", "扫描源文件夹", "扫描目标目录", "准备过滤"} {
		r := syncengine.DestinationResult{ID: id, State: syncengine.DestinationRunning, Progress: syncengine.TransferProgress{Stage: st}}
		if frac := computeProgressFraction(r, false); frac >= 0 {
			t.Fatalf("stage %q frac = %v, want < 0", st, frac)
		}
	}

	// Stopping always returns indeterminate.
	if frac := computeProgressFraction(syncengine.DestinationResult{
		ID: id, State: syncengine.DestinationRunning,
		Progress: syncengine.TransferProgress{Stage: "同步中", Total: 10, Copied: 5, TotalBytes: 100, ProcessedBytes: 50},
	}, true); frac >= 0 {
		t.Fatalf("stopping frac = %v, want < 0", frac)
	}

	// Add-only write -> verify -> completed transition stays monotonic.
	steps := []syncengine.TransferProgress{
		{Stage: "同步中", Total: 2, TotalBytes: 100, ProcessedBytes: 25, CurrentFileSize: 50, CurrentFileDone: 25, SubStage: "写入中"},
		{Stage: "同步中", Total: 2, TotalBytes: 100, ProcessedBytes: 50, CurrentFileSize: 50, CurrentFileDone: 25, SubStage: "校验中"},
		{Stage: "同步中", Total: 2, TotalBytes: 100, ProcessedBytes: 50, Copied: 1},
		{Stage: "同步中", Total: 2, TotalBytes: 100, ProcessedBytes: 100, Copied: 2},
	}
	prev := 0.0
	for i, p := range steps {
		frac := computeProgressFraction(syncengine.DestinationResult{ID: id, State: syncengine.DestinationRunning, Progress: p}, false)
		if frac < prev || frac > 1 {
			t.Fatalf("step %d frac=%v < prev=%v", i, frac, prev)
		}
		prev = frac
	}
	if prev != 1 {
		t.Fatalf("final frac = %v, want 1", prev)
	}

	// Mirror copy uses PlannedCopyBytes and cleanup uses PlannedRemove*.
	mirrorCopy := syncengine.DestinationResult{
		ID: id, State: syncengine.DestinationRunning,
		Progress: syncengine.TransferProgress{Stage: "同步中", PlannedCopyFiles: 2, PlannedCopyBytes: 1000, Bytes: 400, TotalBytes: 5000, ProcessedBytes: 4400},
	}
	if got := computeProgressFraction(mirrorCopy, false); got != 0.4 {
		t.Fatalf("mirror copy frac = %v, want 0.4", got)
	}
	mirrorClean := syncengine.DestinationResult{
		ID: id, State: syncengine.DestinationRunning,
		Progress: syncengine.TransferProgress{Stage: "移除多余文件", PlannedRemoveFiles: 8, PlannedRemoveDirs: 2, Removed: 4, RemovedDirs: 1},
	}
	if got := computeProgressFraction(mirrorClean, false); got != 0.5 {
		t.Fatalf("mirror clean frac = %v, want 0.5", got)
	}
}

func TestETAEstimatorWarmupPauseAndClamping(t *testing.T) {
	id := taskconfig.NewID()
	t0 := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	var est liveETAEstimator
	est.resetForDestination(id, t0)

	// Before warm-up (< 2s and < 5 files): no ETA yet.
	r := syncengine.DestinationResult{
		ID: id, State: syncengine.DestinationRunning,
		Progress: syncengine.TransferProgress{Stage: "同步中", Total: 100, TotalBytes: 100 << 20, Copied: 1, Bytes: 1 << 20, ProcessedBytes: 1 << 20},
	}
	has, _, _, _, _ := est.estimate(r, 0.01, false, t0.Add(1*time.Second))
	if has {
		t.Fatal("expected ETA to wait for warm-up gate")
	}

	// Pause for 30s during Mirror confirm: effective elapsed should exclude pause time.
	est.setPaused(true, t0.Add(2*time.Second))
	hasPaused, _, modePaused, _, _ := est.estimate(r, 0.01, false, t0.Add(15*time.Second))
	if hasPaused || modePaused != "paused" {
		t.Fatalf("paused estimate = (%t, %q), want (false, paused)", hasPaused, modePaused)
	}
	est.setPaused(false, t0.Add(32*time.Second))
	if got := est.effectiveElapsed(t0.Add(34 * time.Second)); got != 4*time.Second {
		t.Fatalf("effectiveElapsed = %v, want 4s", got)
	}

	// Simulate steady progress to warm up and yield finite ETA.
	r.Progress.Copied = 10
	r.Progress.Bytes = 10 << 20
	r.Progress.ProcessedBytes = 10 << 20
	est.observeBytes(r.Progress, t0.Add(34*time.Second))
	has, eta1, _, rate1, stalled := est.estimate(r, 0.10, false, t0.Add(34*time.Second))
	if !has || eta1 <= 0 || rate1 <= 0 || stalled {
		t.Fatalf("expected valid ETA after warm-up: has=%t eta=%v rate=%v stalled=%t", has, eta1, rate1, stalled)
	}

	// Verify stall detection after >3s without byte updates.
	_, _, _, _, stalledLater := est.estimate(r, 0.10, false, t0.Add(38*time.Second))
	if !stalledLater {
		t.Fatal("expected stalled=true after >3s without byte updates")
	}
}

func TestLiveRunTrackerMilestonesAndSnapshot(t *testing.T) {
	taskID := taskconfig.NewID()
	d1, d2 := taskconfig.NewID(), taskconfig.NewID()
	q := syncengine.ManualTaskRequest{
		Kind:   taskconfig.Sync,
		Source: `C:\src`,
		Destinations: []syncengine.ManualDestination{
			{ID: d1, Name: "LocalD", Path: `D:\dst`, Strategy: taskconfig.AddOnly},
			{ID: d2, Name: "NasE", Path: `E:\dst`, Strategy: taskconfig.Mirror},
		},
	}
	t0 := time.Date(2026, 11, 25, 1, 0, 0, 0, time.UTC)
	tr := newLiveRunTracker(taskID, "Docs", q, false, 2, 4, t0)

	// Pure byte delta within the same running stage is not a milestone.
	if !tr.UpdateDestination(syncengine.DestinationResult{ID: d1, Name: "LocalD", State: syncengine.DestinationRunning, Progress: syncengine.TransferProgress{Stage: "同步中", SourceFiles: 10, Total: 4, TotalBytes: 400}}) {
		t.Fatal("expected stage transition to be a milestone")
	}
	if tr.UpdateDestination(syncengine.DestinationResult{ID: d1, Name: "LocalD", State: syncengine.DestinationRunning, Progress: syncengine.TransferProgress{Stage: "同步中", SourceFiles: 10, Total: 4, TotalBytes: 400, Bytes: 50, ProcessedBytes: 50}}) {
		t.Fatal("byte-only update should not trigger milestone")
	}
	if !tr.UpdateDestination(syncengine.DestinationResult{ID: d1, Name: "LocalD", State: syncengine.DestinationRunning, Progress: syncengine.TransferProgress{Stage: "同步中", SourceFiles: 10, Total: 4, TotalBytes: 400, Deferred: 1, DeferredPaths: []string{"locked.db"}}}) {
		t.Fatal("deferred increment should trigger milestone")
	}
	tr.MarkStopping(t0.Add(5 * time.Second))
	snap := tr.Snapshot(t0.Add(6 * time.Second))
	if !snap.Stopping || snap.Stage != "正在停止" || snap.Fraction >= 0 || snap.BatchIndex != 2 || snap.BatchTotal != 4 {
		t.Fatalf("unexpected stopping snapshot: %+v", snap)
	}
	tr.Finish(syncengine.MultipleResult{d1: {ID: d1, Name: "LocalD", State: syncengine.DestinationCanceled}}, errors.New("canceled"), appcore.OutcomeStopped, t0.Add(7*time.Second))
	final := tr.Snapshot(t0.Add(8 * time.Second))
	if final.Active || !final.Finished || final.Outcome != appcore.OutcomeStopped || len(final.Events) < 5 {
		t.Fatalf("unexpected final snapshot: %+v", final)
	}
}
