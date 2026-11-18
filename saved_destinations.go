package main

import (
	"errors"
	"strings"
	"time"

	"syncmaid/internal/taskconfig"
	"syncmaid/internal/taskstore"
)

// Editing/running only accepts the implemented subset, never downgrades others.
func multipleRequestFromSaved(t taskconfig.Task) (manualTaskRequest, error) {
	if t.HasUnknownFields() || t.Trigger.HasUnknownFields() {
		return manualTaskRequest{}, errors.New("任务含未支持字段")
	}
	if (t.Kind != taskconfig.Sync && t.Kind != taskconfig.Moving) || (t.Trigger.Kind != "manual" && t.Trigger.Kind != "watch" && t.Trigger.Kind != "scheduled") {
		return manualTaskRequest{}, errors.New("仅支持Manual/Watch/Scheduled Sync/Move任务")
	}
	if strings.TrimSpace(t.Name) == "" {
		return manualTaskRequest{}, errors.New("任务名称为空")
	}
	if e := validateApplicationTrigger(t.Trigger); e != nil {
		return manualTaskRequest{}, e
	}
	q := manualTaskRequest{Kind: t.Kind, Trigger: t.Trigger, Source: t.SourcePath}
	seen := map[taskconfig.ID]bool{}
	for _, d := range t.Destinations {
		if d.ID == "" || seen[d.ID] {
			return manualTaskRequest{}, errors.New("目标 ID 缺失或重复")
		}
		seen[d.ID] = true
		if d.HasUnknownFields() || d.Target.HasUnknownFields() {
			return manualTaskRequest{}, errors.New("目标含未支持字段")
		}
		if d.Target.Kind != "local" || (t.Kind == taskconfig.Sync && ((d.Strategy != taskconfig.AddOnly && d.Strategy != taskconfig.Mirror) || d.FlattenStructure)) || (t.Kind == taskconfig.Moving && (d.Strategy != taskconfig.Move || (d.CollisionPolicy != "" && d.CollisionPolicy != taskconfig.Skip && d.CollisionPolicy != taskconfig.Suffix))) {
			return manualTaskRequest{}, errors.New("不支持此任务策略、目标类型、平铺或冲突配置")
		}
		if t.Kind == taskconfig.Sync && d.CollisionPolicy != taskconfig.Skip && d.CollisionPolicy != "" {
			return manualTaskRequest{}, errors.New("Sync暂不支持此冲突策略")
		}
		if strings.TrimSpace(d.Name) == "" {
			return manualTaskRequest{}, errors.New("目标名称为空")
		}
		q.Destinations = append(q.Destinations, manualDestination{ID: d.ID, Name: d.Name, Path: d.Target.Path, Filters: d.Filters, Strategy: d.Strategy, DeleteMode: d.DeleteMode, VerifyContents: d.VerifyContents, Flatten: d.FlattenStructure, Collision: d.CollisionPolicy})
	}
	return q, nil
}

func (l *savedTaskLibrary) prepareMultiple(id taskconfig.ID, name string, q manualTaskRequest) ([]taskconfig.Task, taskconfig.ID, error) {
	if l == nil || l.snapshot == nil {
		return nil, id, taskstore.ErrBlocked
	}
	if strings.TrimSpace(name) == "" {
		return nil, id, errors.New("需要任务名称")
	}
	if err := validateTaskDraft(q); err != nil {
		return nil, id, err
	}
	tasks, err := l.snapshot.Tasks()
	if err != nil {
		return nil, id, err
	}
	index := -1
	if manualKind(q.Kind) != taskconfig.Sync && manualKind(q.Kind) != taskconfig.Moving {
		return nil, id, errors.New("未知任务类型")
	}
	task := taskconfig.Task{Kind: manualKind(q.Kind), Trigger: taskconfig.Trigger{Kind: "manual"}}
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
	if task.Kind != manualKind(q.Kind) && len(task.Destinations) != 0 {
		return nil, id, errors.New("已保存任务不能改变Sync/Move类型")
	}
	task.Kind = manualKind(q.Kind)
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
			d = taskconfig.Destination{ID: edit.ID, Strategy: taskconfig.AddOnly, Target: taskconfig.Target{Kind: "local"}, Filters: []taskconfig.Rule{{Kind: "all"}}, DeleteMode: taskconfig.Recycle, CollisionPolicy: taskconfig.Skip, MassDeleteThreshold: 0.5}
		}
		d.Name = edit.Name
		d.Target.Path = edit.Path
		var modeErr error
		if task.Kind == taskconfig.Moving {
			modeErr = validateMoveDestination(edit)
		} else {
			modeErr = validateDestinationMode(edit)
			if edit.Collision != "" && edit.Collision != taskconfig.Skip {
				modeErr = errors.New("Sync暂不支持此冲突策略")
			}
			if edit.Flatten {
				modeErr = errors.New("Sync不能平铺")
			}
		}
		if err := modeErr; err != nil {
			return nil, id, err
		}
		d.Strategy = destinationStrategy(edit)
		d.DeleteMode = destinationDeleteMode(edit)
		d.FlattenStructure = edit.Flatten
		if task.Kind == taskconfig.Moving {
			d.CollisionPolicy = edit.Collision
			if d.CollisionPolicy == "" {
				d.CollisionPolicy = taskconfig.Skip
			}
		}
		if !sameFilterRules(d.Filters, edit.Filters) {
			if ok && projectNativeFilter(d.Filters).Mode == filterReadOnly {
				return nil, id, errors.New("只读过滤规则不能改写")
			}
			if form := projectNativeFilter(edit.Filters); form.Mode == filterReadOnly {
				return nil, id, errors.New("编辑器不能保存此复杂或不支持的过滤规则")
			}
			d.Filters = edit.Filters
		}
		destinations = append(destinations, d)
	}
	trigger := q.Trigger
	for oldID, d := range old {
		if !seen[oldID] && rulesHaveUnknownFields(d.Filters) {
			return nil, id, errors.New("不能移除含未支持字段的目标")
		}
	}
	if trigger.Kind == "" {
		trigger = taskconfig.Trigger{Kind: "manual"}
	}
	if trigger.Kind != "manual" && trigger.Kind != "watch" && trigger.Kind != "scheduled" {
		return nil, id, errors.New("不支持此触发方式")
	}
	if e := validateApplicationTrigger(trigger); e != nil {
		return nil, id, e
	}
	if trigger.Kind == "scheduled" {
		if _, _, e := parseScheduled(*trigger.CronExpression, time.Now()); e != nil && !errors.Is(e, errNoScheduledFuture) {
			return nil, id, e
		}
	}
	task.Trigger = trigger
	task.Name = name
	task.SourcePath = q.Source
	task.Destinations = destinations
	if index < 0 {
		tasks = append(tasks, task)
	} else {
		tasks[index] = task
	}
	if err := validateEditedTaskLayout(task, tasks); err != nil {
		return nil, id, err
	}
	return tasks, id, nil
}
