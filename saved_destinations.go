package main

import (
	"errors"
	"fmt"
	"strings"

	"syncmaid/internal/taskconfig"
	"syncmaid/internal/taskstore"
)

// Editing/running only accepts the implemented subset, never downgrades others.
func multipleRequestFromSaved(t taskconfig.Task) (manualTaskRequest, error) {
	if t.HasUnknownFields() || t.Trigger.HasUnknownFields() {
		return manualTaskRequest{}, errors.New("任务含未支持字段")
	}
	if t.Kind != taskconfig.Sync || t.Trigger.Kind != "manual" || len(t.Destinations) == 0 {
		return manualTaskRequest{}, errors.New("仅支持有目标的手动 Sync 任务")
	}
	if strings.TrimSpace(t.Name) == "" {
		return manualTaskRequest{}, errors.New("任务名称为空")
	}
	q := manualTaskRequest{Source: t.SourcePath}
	seen := map[taskconfig.ID]bool{}
	for _, d := range t.Destinations {
		if d.ID == "" || seen[d.ID] {
			return manualTaskRequest{}, errors.New("目标 ID 缺失或重复")
		}
		seen[d.ID] = true
		if d.HasUnknownFields() || d.Target.HasUnknownFields() {
			return manualTaskRequest{}, errors.New("目标含未支持字段")
		}
		if d.Strategy != taskconfig.AddOnly || d.Target.Kind != "local" || d.FlattenStructure {
			return manualTaskRequest{}, errors.New("仅支持保留目录结构的本地 Add-only")
		}
		if len(d.Filters) != 1 {
			return manualTaskRequest{}, errors.New("当前界面不支持过滤规则")
		}
		r := d.Filters[0]
		if r.Kind != "all" || r.HasUnknownFields() || r.Prefix != nil || r.Extension != nil || r.Pattern != nil || r.Rules != nil || r.Rule != nil {
			return manualTaskRequest{}, errors.New("当前界面不支持此过滤规则")
		}
		if strings.TrimSpace(d.Name) == "" {
			return manualTaskRequest{}, errors.New("目标名称为空")
		}
		q.Destinations = append(q.Destinations, manualDestination{ID: d.ID, Name: d.Name, Path: d.Target.Path})
	}
	return q, nil
}

func (l *savedTaskLibrary) saveMultiple(id taskconfig.ID, name string, q manualTaskRequest) (*savedTaskLibrary, taskconfig.ID, error) {
	if l == nil || l.snapshot == nil {
		return nil, id, taskstore.ErrBlocked
	}
	if strings.TrimSpace(name) == "" || len(q.Destinations) == 0 {
		return nil, id, errors.New("需要任务名称及至少一个目标")
	}
	tasks, err := l.snapshot.Tasks()
	if err != nil {
		return nil, id, err
	}
	index := -1
	task := taskconfig.Task{Kind: taskconfig.Sync, Trigger: taskconfig.Trigger{Kind: "manual"}}
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
		if _, err = multipleRequestFromSaved(task); err != nil {
			return nil, id, err
		}
	} else {
		id = taskconfig.NewID()
		task.ID = id
	}
	old := map[taskconfig.ID]taskconfig.Destination{}
	for _, d := range task.Destinations {
		old[d.ID] = d
	}
	seen := map[taskconfig.ID]bool{}
	destinations := make([]taskconfig.Destination, 0, len(q.Destinations))
	for _, edit := range q.Destinations {
		if edit.ID == "" || seen[edit.ID] || strings.TrimSpace(edit.Name) == "" {
			return nil, id, errors.New("目标名称或 ID 无效/重复")
		}
		seen[edit.ID] = true
		d, ok := old[edit.ID]
		if !ok {
			d = taskconfig.Destination{ID: edit.ID, Strategy: taskconfig.AddOnly, Target: taskconfig.Target{Kind: "local"}, Filters: []taskconfig.Rule{{Kind: "all"}}, VerifyContents: true, DeleteMode: taskconfig.Recycle, CollisionPolicy: taskconfig.Skip, MassDeleteThreshold: 0.5}
		}
		d.Name = edit.Name
		d.Target.Path = edit.Path
		destinations = append(destinations, d)
	}
	task.Name = name
	task.SourcePath = q.Source
	task.Destinations = destinations
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
