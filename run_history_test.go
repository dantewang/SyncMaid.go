package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syncmaid/internal/appcore"
	"syncmaid/internal/syncengine"
	"testing"
	"time"

	"syncmaid/internal/taskconfig"
)

func historyFixture(t *testing.T) (appcore.RunRecord, syncengine.ManualTaskRequest) {
	q, _ := multiFixture(t)
	r, e := appcore.NewRunRecord(taskconfig.NewID(), "history", q, false, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	return r, q
}
func historySuccess(r appcore.RunRecord) syncengine.MultipleResult {
	result := syncengine.MultipleResult{}
	for _, g := range r.Goals {
		result[g.ID] = syncengine.DestinationResult{ID: g.ID, Name: g.Name, State: syncengine.DestinationSucceeded, Progress: syncengine.TransferProgress{Total: 2, Copied: 1, Unchanged: 1, SourceFiles: 2, Bytes: 42}}
	}
	return result
}
func TestRunHistorySuccessMixedAndStoppedRestore(t *testing.T) {
	for _, outcome := range []appcore.OutcomeState{appcore.OutcomeSucceeded, appcore.OutcomeFailed, appcore.OutcomeStopped} {
		t.Run(string(outcome), func(t *testing.T) {
			data := t.TempDir()
			h := appcore.LoadRunHistory(data)
			r, _ := historyFixture(t)
			results := historySuccess(r)
			var workErr error
			if outcome == "failed" {
				workErr = errors.New("target failed")
				target := results[r.Goals[1].ID]
				target.State = syncengine.DestinationFailed
				target.Error = "target failed"
				results[target.ID] = target
			}
			if outcome == "stopped" {
				workErr = context.Canceled
				target := results[r.Goals[1].ID]
				target.State = syncengine.DestinationCanceled
				results[target.ID] = target
			}
			calls := 0
			_, end, actual, e, diagnostic := appcore.ExecuteRecordedRun(h, r, nil, []taskconfig.ID{r.TaskID}, func() (syncengine.MultipleResult, error) { calls++; return results, workErr })
			if calls != 1 || e != workErr || diagnostic != nil || end.Outcome != outcome || len(actual) != 2 {
				t.Fatal(calls, end, e, diagnostic)
			}
			loaded := appcore.LoadRunHistory(data)
			if loaded.Err != nil {
				t.Fatal(loaded.Err)
			}
			stored := loaded.Data.Latest[r.TaskID]
			if stored.ID != r.ID || stored.Outcome != outcome || stored.Ended.IsZero() || stored.Goals[0].ID != r.Goals[0].ID || stored.Goals[0].Counts.Bytes != 42 {
				t.Fatal(stored)
			}
			if outcome == "failed" && stored.Goals[1].Error != "target failed" {
				t.Fatal(stored)
			}
			restored := historyResult(stored)
			if restored.started.IsZero() || restored.ended.IsZero() || restored.destinations[r.Goals[0].ID].Progress.Copied != 1 {
				t.Fatal(restored)
			}
		})
	}
}
func TestRunHistoryInterruptedIsNotSuccessOrReplay(t *testing.T) {
	data := t.TempDir()
	h := appcore.LoadRunHistory(data)
	r, _ := historyFixture(t)
	if e := h.Put(r, nil); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(filepath.Join(data, appcore.RunHistoryFile))
	if e != nil {
		t.Fatal(e)
	}
	loaded := appcore.LoadRunHistory(data)
	if loaded.Err != nil {
		t.Fatal(loaded.Err)
	}
	a := application{tasks: &appcore.SavedTaskLibrary{Tasks: []taskconfig.Task{{ID: r.TaskID}}}, historyView: loaded.Data}
	a.restoreRunResults()
	result := a.results[r.TaskID]
	if !strings.Contains(result.message.render("zh-Hans"), "中断") || result.destinations[r.Goals[0].ID].State != syncengine.DestinationInterrupted || a.isRunning() || a.manual.phase != manualDraft {
		t.Fatal(result)
	}
	after, _ := os.ReadFile(filepath.Join(data, appcore.RunHistoryFile))
	if !bytes.Equal(before, after) {
		t.Fatal("loading rewrote incomplete evidence")
	}
}
func TestRunHistoryCorruptFileNeverOverwritten(t *testing.T) {
	for _, raw := range [][]byte{[]byte("{broken"), []byte(`{"Version":99,"Latest":{},"Recent":[]}`), []byte("null"), {0xff, 0xfe}} {
		data := t.TempDir()
		path := filepath.Join(data, appcore.RunHistoryFile)
		if e := os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
		h := appcore.LoadRunHistory(data)
		if h.Err == nil {
			t.Fatal("bad history accepted", string(raw))
		}
		r, _ := historyFixture(t)
		calls := 0
		view, end, _, workErr, recordErr := appcore.ExecuteRecordedRun(h, r, nil, nil, func() (syncengine.MultipleResult, error) { calls++; return historySuccess(r), nil })
		if calls != 1 || workErr != nil || recordErr == nil || end.Outcome != "succeeded" || view.Latest[r.TaskID].Outcome != "succeeded" {
			t.Fatal(calls, end, workErr, recordErr)
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(raw, after) {
			t.Fatal("bad original overwritten")
		}
	}
}
func TestRunHistoryPublishFailureDoesNotRetryWorkOrOverwrite(t *testing.T) {
	data := t.TempDir()
	h := appcore.LoadRunHistory(data)
	old, _ := historyFixture(t)
	old = appcore.FinishRunRecord(old, historySuccess(old), nil, time.Now())
	if e := h.Put(old, nil); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(filepath.Join(data, appcore.RunHistoryFile))
	sentinel := errors.New("injected publish refusal")
	publishes := 0
	h.Publish = func(string, string) error { publishes++; return sentinel }
	r, _ := historyFixture(t)
	calls := 0
	view, end, _, workErr, recordErr := appcore.ExecuteRecordedRun(h, r, nil, nil, func() (syncengine.MultipleResult, error) { calls++; return historySuccess(r), nil })
	if calls != 1 || publishes != 1 || workErr != nil || !errors.Is(recordErr, sentinel) || end.Outcome != "succeeded" || view.Latest[r.TaskID].Outcome != "succeeded" {
		t.Fatal(calls, publishes, workErr, recordErr)
	}
	after, _ := os.ReadFile(filepath.Join(data, appcore.RunHistoryFile))
	if !bytes.Equal(before, after) {
		t.Fatal("prior history lost")
	}
}
func TestRunHistoryExternalChangeAndDirectoryProtection(t *testing.T) {
	data := t.TempDir()
	h := appcore.LoadRunHistory(data)
	r, _ := historyFixture(t)
	r = appcore.FinishRunRecord(r, historySuccess(r), nil, time.Now())
	if e := h.Put(r, nil); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(data, appcore.RunHistoryFile)
	raw, _ := os.ReadFile(path)
	raw = append(raw, '\n')
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	r.ID = taskconfig.NewID()
	if e := h.Put(r, nil); e == nil {
		t.Fatal("external change overwritten")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(raw, after) {
		t.Fatal("external bytes lost")
	}
	dirData := t.TempDir()
	dir := filepath.Join(dirData, appcore.RunHistoryFile)
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	blocked := appcore.LoadRunHistory(dirData)
	if blocked.Err == nil {
		t.Fatal("directory accepted")
	}
	if e := blocked.Put(r, nil); e == nil {
		t.Fatal("directory overwritten")
	}
}
func TestRunHistoryRetentionKeepsLatestPerTask(t *testing.T) {
	data := t.TempDir()
	h := appcore.LoadRunHistory(data)
	old, _ := historyFixture(t)
	old = appcore.FinishRunRecord(old, historySuccess(old), nil, time.Now())
	if e := h.Put(old, nil); e != nil {
		t.Fatal(e)
	}
	current, _ := historyFixture(t)
	current = appcore.FinishRunRecord(current, historySuccess(current), nil, time.Now())
	active := []taskconfig.ID{old.TaskID, current.TaskID}
	for i := 0; i < appcore.HistoryLimit+3; i++ {
		current.ID = taskconfig.NewID()
		if e := h.Put(current, active); e != nil {
			t.Fatal(e)
		}
	}
	loaded := appcore.LoadRunHistory(data)
	if loaded.Err != nil || len(loaded.Data.Recent) != appcore.HistoryLimit || loaded.Data.Latest[old.TaskID].ID != old.ID || loaded.Data.Latest[current.TaskID].ID != current.ID {
		t.Fatal(loaded.Err, len(loaded.Data.Recent))
	}
}
func TestRunHistoryBoundsAndOrigins(t *testing.T) {
	r, q := historyFixture(t)
	for _, kind := range []string{"watch", "scheduled"} {
		q.Trigger.Kind = kind
		record, e := appcore.NewRunRecord(r.TaskID, "name", q, true, time.Now())
		if e != nil || string(record.Origin) != kind {
			t.Fatal(record, e)
		}
		manual, _ := appcore.NewRunRecord(r.TaskID, "name", q, false, time.Now())
		if manual.Origin != "manual" {
			t.Fatal(manual)
		}
	}
	result := historySuccess(r)
	for id, g := range result {
		g.Error = strings.Repeat("错", 1000)
		g.Progress.Path = "not-stored.txt"
		result[id] = g
	}
	end := appcore.FinishRunRecord(r, result, errors.New(strings.Repeat("坏", 1000)), time.Now())
	if len(end.Error) > appcore.HistoryErrorBytes || len(end.Goals[0].Error) > appcore.HistoryErrorBytes || end.Goals[0].Counts.Path != "" {
		t.Fatal(end)
	}
	data := t.TempDir()
	large := bytes.Repeat([]byte("x"), appcore.HistoryBytes+1)
	if e := os.WriteFile(filepath.Join(data, appcore.RunHistoryFile), large, 0600); e != nil {
		t.Fatal(e)
	}
	if appcore.LoadRunHistory(data).Err == nil {
		t.Fatal("oversize file accepted")
	}
	h := appcore.LoadRunHistory(t.TempDir())
	h.Err = errors.New("block disk writes for byte-retention test")
	base := end.Goals[0]
	end.Goals = nil
	for i := 0; i < appcore.HistoryGoalLimit; i++ {
		g := base
		g.ID = taskconfig.NewID()
		end.Goals = append(end.Goals, g)
	}
	for i := 0; i < 20; i++ {
		end.ID = taskconfig.NewID()
		_ = h.Put(end, nil)
	}
	body, e := json.MarshalIndent(h.Data, "", "  ")
	if e != nil || len(body) > appcore.HistoryBytes || len(h.Data.Recent) >= 20 {
		t.Fatal(len(body), len(h.Data.Recent), e)
	}
}

func TestRunHistoryMetadataBackwardCompatibilityAndBounds(t *testing.T) {
	data := t.TempDir()
	legacyJSON := `{
  "Version": 1,
  "Latest": {
    "c4384fbe-ac5f-4973-82dc-1d764ff3f8ee": {
      "ID": "0e6671b4-874c-490a-8010-57b5f236552a",
      "TaskID": "c4384fbe-ac5f-4973-82dc-1d764ff3f8ee",
      "Name": "Eagle",
      "Kind": "Sync",
      "Origin": "manual",
      "Started": "2026-11-22T17:47:25.6660106+08:00",
      "Ended": "2026-11-22T17:47:25.6732326+08:00",
      "Outcome": "failed",
      "Error": "NAS-Eagle_backup: 目标文件夹: directory identity cannot be verified: The parameter is incorrect.",
      "Goals": [
        {
          "Started": "0001-01-01T00:00:00Z",
          "Ended": "2026-11-22T17:47:25.6732326+08:00",
          "ID": "91fbf41d-0f7b-4b0c-a8d1-ad2b30b23a3d",
          "Name": "NAS-Eagle_backup",
          "State": "失败",
          "Error": "目标文件夹: directory identity cannot be verified: The parameter is incorrect.",
          "Counts": {
            "Stage": "",
            "Path": "",
            "Total": 0,
            "Copied": 0,
            "Unchanged": 0,
            "Bytes": 0,
            "Removed": 0,
            "Moved": 0,
            "Retained": 0,
            "Failed": 0,
            "Unmatched": 0,
            "SourceFiles": 0,
            "SourceEmpty": false
          }
        }
      ]
    }
  },
  "Recent": []
}`
	if err := os.WriteFile(filepath.Join(data, appcore.RunHistoryFile), []byte(legacyJSON), 0600); err != nil {
		t.Fatal(err)
	}
	loaded := appcore.LoadRunHistory(data)
	if loaded.Err != nil {
		t.Fatalf("failed to load v1 run-history without new metadata: %v", loaded.Err)
	}

	r, q := historyFixture(t)
	start, err := appcore.NewRunRecord(r.TaskID, "Meta", q, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if start.SourcePath == "" || start.Goals[0].TargetPath == "" {
		t.Fatalf("expected SourcePath and TargetPath to be populated: %+v", start)
	}
	results := historySuccess(start)
	g := results[start.Goals[0].ID]
	g.Progress.Stage = "检查目标目录"
	results[start.Goals[0].ID] = g
	end := appcore.FinishRunRecord(start, results, nil, time.Now())
	if end.Goals[0].Stage != "检查目标目录" || end.Goals[0].Counts.Stage != "" {
		t.Fatalf("unexpected goal stage persistence: %+v", end.Goals[0])
	}
	if err := loaded.Put(end, []taskconfig.ID{start.TaskID}); err != nil {
		t.Fatal(err)
	}
	reloaded := appcore.LoadRunHistory(data)
	if reloaded.Err != nil {
		t.Fatal(reloaded.Err)
	}
	got := reloaded.Data.Latest[start.TaskID]
	if got.SourcePath == "" || got.Goals[0].TargetPath == "" || got.Goals[0].Stage != "检查目标目录" {
		t.Fatalf("new metadata lost after reload: %+v", got)
	}
	badSource := reloaded.Data
	rCopy := got
	rCopy.SourcePath = strings.Repeat("a", appcore.HistoryPathBytes+1)
	badSource.Latest = map[taskconfig.ID]appcore.RunRecord{start.TaskID: rCopy}
	if err := appcore.ValidateRunHistory(badSource); err == nil {
		t.Fatal("oversize SourcePath accepted")
	}
	badStage := reloaded.Data
	rCopy = got
	rCopy.Goals = append([]appcore.RunGoal(nil), got.Goals...)
	rCopy.Goals[0].Stage = strings.Repeat("s", appcore.HistoryStageBytes+1)
	badStage.Latest = map[taskconfig.ID]appcore.RunRecord{start.TaskID: rCopy}
	if err := appcore.ValidateRunHistory(badStage); err == nil {
		t.Fatal("oversize Goal.Stage accepted")
	}
}
