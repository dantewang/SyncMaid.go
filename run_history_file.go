package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syncmaid/internal/taskconfig"
	"unicode/utf8"
)

const runHistoryFile = "run-history-go.json"

type historyVersion struct {
	data []byte
	info os.FileInfo
}
type runHistoryStore struct {
	path     string
	data     runHistoryData
	baseline historyVersion
	err      error
	publish  func(string, string) error
}

// Only the serial execution worker mutates this store. UI receives immutable
// snapshots. The caller already owns and pins the production Data directory.
func loadRunHistory(data string) *runHistoryStore {
	h := &runHistoryStore{path: filepath.Join(data, runHistoryFile), publish: replaceRunHistory, data: runHistoryData{Version: 1, Latest: map[taskconfig.ID]runRecord{}}}
	if !filepath.IsAbs(h.path) {
		h.err = errors.New("运行记录需要绝对Data路径")
		return h
	}
	version, e := readHistoryVersion(h.path)
	h.baseline = version
	if e != nil {
		h.err = e
		return h
	}
	if version.info == nil {
		return h
	}
	if !utf8.Valid(version.data) {
		h.err = errors.New("运行记录不是有效UTF-8，原文件保持不变")
		return h
	}
	decoder := json.NewDecoder(bytes.NewReader(version.data))
	decoder.DisallowUnknownFields()
	var decoded runHistoryData
	e = decoder.Decode(&decoded)
	if e == nil {
		var extra any
		if tail := decoder.Decode(&extra); tail != io.EOF {
			e = errors.New("运行记录含多余JSON数据")
		}
	}
	if e == nil {
		e = validateRunHistory(decoded)
	}
	if e != nil {
		h.err = fmt.Errorf("运行记录不可用，原文件保持不变: %w", e)
		return h
	}
	h.data = decoded
	return h
}
func readHistoryVersion(path string) (v historyVersion, err error) {
	info, e := os.Lstat(path)
	if errors.Is(e, os.ErrNotExist) {
		return v, nil
	}
	if e != nil {
		return v, e
	}
	if !historyRegularFile(info) {
		return v, errors.New("运行记录不是受支持的普通文件")
	}
	f, e := os.Open(path)
	if e != nil {
		return v, e
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	actual, e := f.Stat()
	if e != nil {
		return v, e
	}
	if !os.SameFile(info, actual) {
		return v, errors.New("运行记录在读取时变化")
	}
	body, e := io.ReadAll(io.LimitReader(f, historyBytes+1))
	if e != nil {
		return v, e
	}
	if len(body) > historyBytes {
		return v, errors.New("运行记录超过4 MiB上限")
	}
	return historyVersion{data: body, info: actual}, nil
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
		case "running":
			if !r.Ended.IsZero() {
				return errors.New("开始记录含结束时间")
			}
		case "succeeded", "failed", "stopped":
			if r.Ended.IsZero() {
				return errors.New("结束记录缺失时间")
			}
		default:
			return errors.New("未知运行结果")
		}
		if r.Kind != taskconfig.Sync && r.Kind != taskconfig.Moving {
			return errors.New("未知任务类型")
		}
		if r.Origin != "manual" && r.Origin != "watch" && r.Origin != "scheduled" {
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
	if e := validateRunHistory(next); e != nil {
		return e
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
	current, e := readHistoryVersion(h.path)
	if e == nil {
		if (current.info == nil) != (h.baseline.info == nil) || !bytes.Equal(current.data, h.baseline.data) || (current.info != nil && !os.SameFile(current.info, h.baseline.info)) {
			e = errors.New("运行记录被外部修改，请重新载入")
		}
	}
	if e != nil {
		h.err = e
		return e
	}
	info, e := writeRunHistory(h.path, body, h.publish)
	if e != nil {
		h.err = e
		return e
	}
	h.baseline = historyVersion{data: body, info: info}
	return nil
}
func writeRunHistory(path string, body []byte, publish func(string, string) error) (info os.FileInfo, err error) {
	f, e := os.CreateTemp(filepath.Dir(path), ".syncmaid-history-*.tmp")
	if e != nil {
		return nil, e
	}
	defer f.Close()
	state := transferStaged
	defer func() {
		if state == transferStaged {
			err = errors.Join(err, os.Remove(f.Name()))
		}
	}()
	n, e := f.Write(body)
	if e == nil && n != len(body) {
		e = io.ErrShortWrite
	}
	if e != nil {
		return nil, e
	}
	if e = f.Sync(); e != nil {
		return nil, e
	}
	info, e = f.Stat()
	if e != nil {
		return nil, e
	}
	if e = f.Close(); e != nil {
		return nil, e
	}
	state = transferPublishUnknown
	if e = publish(f.Name(), path); e != nil {
		return nil, fmt.Errorf("运行记录发布失败，请核对文件；不会重试同步: %w", e)
	}
	state = transferPublished
	return info, nil
}
