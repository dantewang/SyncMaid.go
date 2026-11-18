package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"syncmaid/internal/atomicfile"
	"syncmaid/internal/taskconfig"
)

const runHistoryFile = "run-history-go.json"

type runHistoryStore struct {
	path     string
	data     runHistoryData
	baseline atomicfile.Version
	err      error
	publish  func(string, string) error
}

// Only the serial execution worker mutates this store. UI receives immutable
// snapshots. The caller already owns and pins the production Data directory.
func loadRunHistory(data string) *runHistoryStore {
	h := &runHistoryStore{path: filepath.Join(data, runHistoryFile), data: runHistoryData{Version: 1, Latest: map[taskconfig.ID]runRecord{}}}
	if !filepath.IsAbs(h.path) {
		h.err = errors.New("运行记录需要绝对Data路径")
		return h
	}
	version, e := atomicfile.ReadRegular(h.path, historyBytes)
	h.baseline = version
	if e != nil {
		h.err = e
		return h
	}
	if version.Info == nil {
		return h
	}
	decoded, e := atomicfile.DecodeStrictJSONObject[runHistoryData](version.Data)
	if e == nil {
		e = validateRunHistory(*decoded)
	}
	if e != nil {
		h.err = fmt.Errorf("运行记录不可用，原文件保持不变: %w", e)
		return h
	}
	h.data = *decoded
	return h
}
func validateRunHistory(d runHistoryData) error {
	if d.Version != 1 || d.Latest == nil || len(d.Recent) > historyLimit {
		return errors.New("不支持的运行记录版本或条数")
	}
	validate := func(r runRecord) error {
		if r.ID == "" || r.TaskID == "" || len(r.ID) > 64 || len(r.TaskID) > 64 || r.Started.IsZero() || len(r.Name) > 256 || len(r.Error) > historyErrorBytes || len(r.Goals) > historyGoalLimit {
			return errors.New("运行记录字段无效或超限")
		}
		switch r.Outcome {
		case outcomeRunning:
			if !r.Ended.IsZero() {
				return errors.New("开始记录含结束时间")
			}
		case outcomeSucceeded, outcomeFailed, outcomeStopped:
			if r.Ended.IsZero() {
				return errors.New("结束记录缺失时间")
			}
		default:
			return errors.New("未知运行结果")
		}
		if r.Kind != taskconfig.Sync && r.Kind != taskconfig.Moving {
			return errors.New("未知任务类型")
		}
		if r.Origin != originManual && r.Origin != originWatch && r.Origin != originScheduled {
			return errors.New("未知触发来源")
		}
		for _, g := range r.Goals {
			if g.ID == "" || len(g.ID) > 64 || len(g.Name) > 256 || len(g.Error) > historyErrorBytes || g.Counts.Path != "" || g.Counts.Stage != "" {
				return errors.New("目标记录无效或超限")
			}
			switch g.State {
			case destinationWaiting, destinationRunning, destinationSucceeded, destinationNoMatches, destinationFailed, destinationCanceled, destinationSkipped, destinationInterrupted:
			default:
				return errors.New("未知目标结果")
			}
			p := g.Counts
			if p.Total < 0 || p.Copied < 0 || p.Unchanged < 0 || p.Bytes < 0 || p.Moved < 0 || p.Removed < 0 || p.Retained < 0 || p.Failed < 0 || p.Unmatched < 0 || p.SourceFiles < 0 {
				return errors.New("运行计数不能为负数")
			}
		}
		return nil
	}
	for id, r := range d.Latest {
		if id != r.TaskID {
			return errors.New("运行记录任务ID不符")
		}
		if e := validate(r); e != nil {
			return e
		}
	}
	for _, r := range d.Recent {
		if e := validate(r); e != nil {
			return e
		}
	}
	return nil
}
func (h *runHistoryStore) put(r runRecord, active []taskconfig.ID) error {
	next := runHistoryData{Version: 1, Latest: map[taskconfig.ID]runRecord{}, Recent: append([]runRecord(nil), h.data.Recent...)}
	keep := map[taskconfig.ID]bool{}
	for _, id := range active {
		keep[id] = true
	}
	for id, old := range h.data.Latest {
		if active == nil || keep[id] {
			next.Latest[id] = old
		}
	}
	next.Latest[r.TaskID] = r
	found := false
	for i, old := range next.Recent {
		if old.ID == r.ID {
			next.Recent[i] = r
			found = true
			break
		}
	}
	if !found {
		next.Recent = append(next.Recent, r)
	}
	if len(next.Recent) > historyLimit {
		next.Recent = next.Recent[len(next.Recent)-historyLimit:]
	}
	var body []byte
	for {
		var e error
		body, e = json.MarshalIndent(next, "", "  ")
		if e != nil {
			return e
		}
		if len(body) <= historyBytes {
			break
		}
		if len(next.Recent) == 0 {
			return errors.New("最近任务结果超过4 MiB，记录未保存，文件执行不受影响")
		}
		next.Recent = next.Recent[1:]
	}
	h.data = next // Bounded session diagnostics remain available even after disk failure.
	if h.err != nil {
		return h.err
	}
	current, e := atomicfile.ReadRegular(h.path, historyBytes)
	if e == nil && !atomicfile.SameVersion(current, h.baseline) {
		e = errors.New("运行记录被外部修改，请重新载入")
	}
	if e != nil {
		h.err = e
		return e
	}
	info, e := atomicfile.Publish(h.path, body, ".syncmaid-history-*.tmp", h.publish)
	if e != nil {
		e = fmt.Errorf("运行记录发布失败，请核对文件；不会重试同步: %w", e)
		h.err = e
		return e
	}
	h.baseline = atomicfile.Version{Data: body, Info: info}
	return nil
}
