package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"syncmaid/internal/taskconfig"
	"syncmaid/internal/taskstore"
)

const savedTaskFile = "tasks-go.json"

// The application opens this only after acquiring its installation/Data owner.
// A library is immutable once handed to the UI; workers return a new library.
type savedTaskLibrary struct {
	snapshot *taskstore.Snapshot
	tasks    []taskconfig.Task
}

func loadSavedTasks(data string) (*savedTaskLibrary, error) {
	store, err := taskstore.New(filepath.Join(data, savedTaskFile))
	if err != nil {
		return nil, err
	}
	snapshot, err := store.Load()
	if err != nil {
		return nil, err
	}
	return savedLibrary(snapshot)
}
func savedLibrary(snapshot *taskstore.Snapshot) (*savedTaskLibrary, error) {
	tasks, err := snapshot.Tasks()
	if err != nil {
		return nil, err
	}
	seen := map[taskconfig.ID]bool{}
	for _, t := range tasks {
		if seen[t.ID] {
			return nil, errors.New("任务 ID 重复，已阻止编辑与运行")
		}
		seen[t.ID] = true
	}
	return &savedTaskLibrary{snapshot: snapshot, tasks: tasks}, nil
}
func (l *savedTaskLibrary) find(id taskconfig.ID) (taskconfig.Task, error) {
	if l != nil {
		for _, t := range l.tasks {
			if t.ID == id {
				return t, nil
			}
		}
	}
	return taskconfig.Task{}, errors.New("未找到此任务，请重新载入或新建")
}

// Only the slice this UI actually implements can be run or edited. Other
// records stay intact in the library; saving another record preserves them.
func requestFromSaved(t taskconfig.Task) (manualRequest, error) {
	if t.HasUnknownFields() || t.Trigger.HasUnknownFields() {
		return manualRequest{}, errors.New("任务含未支持字段")
	}
	if t.Kind != taskconfig.Sync || t.Trigger.Kind != "manual" || len(t.Destinations) != 1 {
		return manualRequest{}, errors.New("当前仅支持手动、单目标 Sync 任务")
	}
	d := t.Destinations[0]
	if d.HasUnknownFields() || d.Target.HasUnknownFields() {
		return manualRequest{}, errors.New("目标含未支持字段")
	}
	if d.Strategy != taskconfig.AddOnly || d.Target.Kind != "local" || d.FlattenStructure {
		return manualRequest{}, errors.New("当前仅支持保留目录结构的本地 Add-only")
	}
	if len(d.Filters) != 1 {
		return manualRequest{}, errors.New("当前界面不支持过滤规则")
	}
	r := d.Filters[0]
	if r.Kind != "all" || r.HasUnknownFields() || r.Prefix != nil || r.Extension != nil || r.Pattern != nil || r.Rules != nil || r.Rule != nil {
		return manualRequest{}, errors.New("当前界面不支持此过滤规则")
	}
	if strings.TrimSpace(t.Name) == "" {
		return manualRequest{}, errors.New("任务名称为空")
	}
	return manualRequest{Source: t.SourcePath, Target: d.Target.Path}, nil
}

func (l *savedTaskLibrary) save(id taskconfig.ID, name string, q manualRequest) (*savedTaskLibrary, taskconfig.ID, error) {
	if l == nil || l.snapshot == nil {
		return nil, id, taskstore.ErrBlocked
	}
	if strings.TrimSpace(name) == "" {
		return nil, id, errors.New("请输入任务名称")
	}
	tasks, err := l.snapshot.Tasks()
	if err != nil {
		return nil, id, err
	}
	index := -1
	var task taskconfig.Task
	if id != "" {
		for i, t := range tasks {
			if t.ID == id {
				index = i
				task = t
				break
			}
		}
		if index < 0 {
			return nil, id, errors.New("任务已不存在，请新建")
		}
		if _, err = requestFromSaved(task); err != nil {
			return nil, id, err
		}
		task.Destinations = append([]taskconfig.Destination(nil), task.Destinations...)
	} else {
		id = taskconfig.NewID()
		task = taskconfig.Task{ID: id, Kind: taskconfig.Sync, Trigger: taskconfig.Trigger{Kind: "manual"}, Destinations: []taskconfig.Destination{{ID: taskconfig.NewID(), Name: "目标", Strategy: taskconfig.AddOnly, Target: taskconfig.Target{Kind: "local"}, Filters: []taskconfig.Rule{{Kind: "all"}}, VerifyContents: true, DeleteMode: taskconfig.Recycle, CollisionPolicy: taskconfig.Skip, MassDeleteThreshold: 0.5}}}
	}
	task.Name = name
	task.SourcePath = q.Source
	task.Destinations[0].Target.Path = q.Target
	if index < 0 {
		tasks = append(tasks, task)
	} else {
		tasks[index] = task
	}
	next, err := l.snapshot.Save(tasks)
	if err != nil {
		return nil, id, fmt.Errorf("保存任务: %w", err)
	}
	library, err := savedLibrary(next)
	return library, id, err
}
func (l *savedTaskLibrary) remove(id taskconfig.ID) (*savedTaskLibrary, error) {
	if l == nil || l.snapshot == nil {
		return nil, taskstore.ErrBlocked
	}
	task, err := l.find(id)
	if err != nil {
		return nil, err
	}
	if _, err = requestFromSaved(task); err != nil {
		return nil, err
	}
	tasks, err := l.snapshot.Tasks()
	if err != nil {
		return nil, err
	}
	for i, t := range tasks {
		if t.ID == id {
			tasks = append(tasks[:i], tasks[i+1:]...)
			break
		}
	}
	next, err := l.snapshot.Save(tasks)
	if err != nil {
		return nil, err
	}
	return savedLibrary(next)
}
