package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syncmaid/internal/syncengine"
	"testing"
	"time"

	"syncmaid/internal/taskconfig"
)

func historyFixture(t *testing.T) (runRecord, syncengine.ManualTaskRequest) {
	q, _ := multiFixture(t)
	r, e := newRunRecord(taskconfig.NewID(), "history", q, false, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	return r, q
}
func historySuccess(r runRecord) syncengine.MultipleResult {
	result := syncengine.MultipleResult{}
	for _, g := range r.Goals {
		result[g.ID] = syncengine.DestinationResult{ID: g.ID, Name: g.Name, State: syncengine.DestinationSucceeded, Progress: syncengine.TransferProgress{Total: 2, Copied: 1, Unchanged: 1, SourceFiles: 2, Bytes: 42}}
	}
	return result
}
func TestRunHistorySuccessMixedAndStoppedRestore(t *testing.T) {
	for _, outcome := range []outcomeState{outcomeSucceeded, outcomeFailed, outcomeStopped} {
		t.Run(string(outcome), func(t *testing.T) {
			data := t.TempDir()
			h := loadRunHistory(data)
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
			_, end, actual, e, diagnostic := executeRecordedRun(h, r, nil, []taskconfig.ID{r.TaskID}, func() (syncengine.MultipleResult, error) { calls++; return results, workErr })
			if calls != 1 || e != workErr || diagnostic != nil || end.Outcome != outcome || len(actual) != 2 {
				t.Fatal(calls, end, e, diagnostic)
			}
			loaded := loadRunHistory(data)
			if loaded.err != nil {
				t.Fatal(loaded.err)
			}
			stored := loaded.data.Latest[r.TaskID]
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
	h := loadRunHistory(data)
	r, _ := historyFixture(t)
	if e := h.put(r, nil); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(filepath.Join(data, runHistoryFile))
	if e != nil {
		t.Fatal(e)
	}
	loaded := loadRunHistory(data)
	if loaded.err != nil {
		t.Fatal(loaded.err)
	}
	a := application{tasks: &savedTaskLibrary{tasks: []taskconfig.Task{{ID: r.TaskID}}}, historyView: loaded.data}
	a.restoreRunResults()
	result := a.results[r.TaskID]
	if !strings.Contains(result.message.render("zh-Hans"), "中断") || result.destinations[r.Goals[0].ID].State != syncengine.DestinationInterrupted || a.manual.cancel != nil || a.manual.phase != manualDraft {
		t.Fatal(result)
	}
	after, _ := os.ReadFile(filepath.Join(data, runHistoryFile))
	if !bytes.Equal(before, after) {
		t.Fatal("loading rewrote incomplete evidence")
	}
}
func TestRunHistoryCorruptFileNeverOverwritten(t *testing.T) {
	for _, raw := range [][]byte{[]byte("{broken"), []byte(`{"Version":99,"Latest":{},"Recent":[]}`), []byte("null"), {0xff, 0xfe}} {
		data := t.TempDir()
		path := filepath.Join(data, runHistoryFile)
		if e := os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
		h := loadRunHistory(data)
		if h.err == nil {
			t.Fatal("bad history accepted", string(raw))
		}
		r, _ := historyFixture(t)
		calls := 0
		view, end, _, workErr, recordErr := executeRecordedRun(h, r, nil, nil, func() (syncengine.MultipleResult, error) { calls++; return historySuccess(r), nil })
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
	h := loadRunHistory(data)
	old, _ := historyFixture(t)
	old = finishRunRecord(old, historySuccess(old), nil, time.Now())
	if e := h.put(old, nil); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(filepath.Join(data, runHistoryFile))
	sentinel := errors.New("injected publish refusal")
	publishes := 0
	h.publish = func(string, string) error { publishes++; return sentinel }
	r, _ := historyFixture(t)
	calls := 0
	view, end, _, workErr, recordErr := executeRecordedRun(h, r, nil, nil, func() (syncengine.MultipleResult, error) { calls++; return historySuccess(r), nil })
	if calls != 1 || publishes != 1 || workErr != nil || !errors.Is(recordErr, sentinel) || end.Outcome != "succeeded" || view.Latest[r.TaskID].Outcome != "succeeded" {
		t.Fatal(calls, publishes, workErr, recordErr)
	}
	after, _ := os.ReadFile(filepath.Join(data, runHistoryFile))
	if !bytes.Equal(before, after) {
		t.Fatal("prior history lost")
	}
}
func TestRunHistoryExternalChangeAndDirectoryProtection(t *testing.T) {
	data := t.TempDir()
	h := loadRunHistory(data)
	r, _ := historyFixture(t)
	r = finishRunRecord(r, historySuccess(r), nil, time.Now())
	if e := h.put(r, nil); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(data, runHistoryFile)
	raw, _ := os.ReadFile(path)
	raw = append(raw, '\n')
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	r.ID = taskconfig.NewID()
	if e := h.put(r, nil); e == nil {
		t.Fatal("external change overwritten")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(raw, after) {
		t.Fatal("external bytes lost")
	}
	dirData := t.TempDir()
	dir := filepath.Join(dirData, runHistoryFile)
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	blocked := loadRunHistory(dirData)
	if blocked.err == nil {
		t.Fatal("directory accepted")
	}
	if e := blocked.put(r, nil); e == nil {
		t.Fatal("directory overwritten")
	}
}
func TestRunHistoryRetentionKeepsLatestPerTask(t *testing.T) {
	data := t.TempDir()
	h := loadRunHistory(data)
	old, _ := historyFixture(t)
	old = finishRunRecord(old, historySuccess(old), nil, time.Now())
	if e := h.put(old, nil); e != nil {
		t.Fatal(e)
	}
	current, _ := historyFixture(t)
	current = finishRunRecord(current, historySuccess(current), nil, time.Now())
	active := []taskconfig.ID{old.TaskID, current.TaskID}
	for i := 0; i < historyLimit+3; i++ {
		current.ID = taskconfig.NewID()
		if e := h.put(current, active); e != nil {
			t.Fatal(e)
		}
	}
	loaded := loadRunHistory(data)
	if loaded.err != nil || len(loaded.data.Recent) != historyLimit || loaded.data.Latest[old.TaskID].ID != old.ID || loaded.data.Latest[current.TaskID].ID != current.ID {
		t.Fatal(loaded.err, len(loaded.data.Recent))
	}
}
func TestRunHistoryBoundsAndOrigins(t *testing.T) {
	r, q := historyFixture(t)
	for _, kind := range []string{"watch", "scheduled"} {
		q.Trigger.Kind = kind
		record, e := newRunRecord(r.TaskID, "name", q, true, time.Now())
		if e != nil || string(record.Origin) != kind {
			t.Fatal(record, e)
		}
		manual, _ := newRunRecord(r.TaskID, "name", q, false, time.Now())
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
	end := finishRunRecord(r, result, errors.New(strings.Repeat("坏", 1000)), time.Now())
	if len(end.Error) > historyErrorBytes || len(end.Goals[0].Error) > historyErrorBytes || end.Goals[0].Counts.Path != "" {
		t.Fatal(end)
	}
	data := t.TempDir()
	large := bytes.Repeat([]byte("x"), historyBytes+1)
	if e := os.WriteFile(filepath.Join(data, runHistoryFile), large, 0600); e != nil {
		t.Fatal(e)
	}
	if loadRunHistory(data).err == nil {
		t.Fatal("oversize file accepted")
	}
	h := loadRunHistory(t.TempDir())
	h.err = errors.New("block disk writes for byte-retention test")
	base := end.Goals[0]
	end.Goals = nil
	for i := 0; i < historyGoalLimit; i++ {
		g := base
		g.ID = taskconfig.NewID()
		end.Goals = append(end.Goals, g)
	}
	for i := 0; i < 20; i++ {
		end.ID = taskconfig.NewID()
		_ = h.put(end, nil)
	}
	body, e := json.MarshalIndent(h.data, "", "  ")
	if e != nil || len(body) > historyBytes || len(h.data.Recent) >= 20 {
		t.Fatal(len(body), len(h.data.Recent), e)
	}
}
