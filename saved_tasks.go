package main

import (
	"errors"
	"path/filepath"

	"syncmaid/internal/filefilter"
	"syncmaid/internal/taskconfig"
	"syncmaid/internal/taskstore"
)

const savedTaskFile = "tasks-go.json"

// The application opens this only after acquiring its installation/Data owner.
// A library is immutable once handed to the UI; workers return a new library.
type savedTaskLibrary struct {
	reviews  []filefilter.RegexConfirmation
	restored map[taskconfig.ID]map[taskconfig.ID]reviewBundle
	snapshot *taskstore.Snapshot
	tasks    []taskconfig.Task
}

func loadSavedTasks(data string) (*savedTaskLibrary, error) {
	store, err := taskstore.NewVersioned(filepath.Join(data, savedTaskFile))
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
	l := &savedTaskLibrary{snapshot: snapshot, tasks: tasks, reviews: snapshot.Confirmations()}
	l.restoreReviews()
	return l, nil
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

// The single-target accessor remains for callers that explicitly require a pair.
func requestFromSaved(t taskconfig.Task) (manualRequest, error) {
	q, err := multipleRequestFromSaved(t)
	if err != nil {
		return manualRequest{}, err
	}
	if len(q.Destinations) != 1 {
		return manualRequest{}, errors.New("需要单目标任务")
	}
	if destinationStrategy(q.Destinations[0]) != taskconfig.AddOnly || !isAllFileRules(q.Destinations[0].Filters) {
		return manualRequest{}, errors.New("无过滤的单目标接口不能忽略现有过滤规则")
	}
	return manualRequest{Source: q.Source, Target: q.Destinations[0].Path}, nil
}
func (l *savedTaskLibrary) save(id taskconfig.ID, name string, q manualRequest) (*savedTaskLibrary, taskconfig.ID, error) {
	d := manualDestination{ID: taskconfig.NewID(), Name: "目标", Path: q.Target, Filters: allFileRules()}
	if id != "" {
		t, err := l.find(id)
		if err != nil {
			return nil, id, err
		}
		old, err := multipleRequestFromSaved(t)
		if err != nil {
			return nil, id, err
		}
		if len(old.Destinations) != 1 {
			return nil, id, errors.New("单目标保存不能缩减多目标任务")
		}
		d.ID = old.Destinations[0].ID
		d.Name = old.Destinations[0].Name
		if destinationStrategy(old.Destinations[0]) != taskconfig.AddOnly || !isAllFileRules(old.Destinations[0].Filters) {
			return nil, id, errors.New("无过滤的单目标保存接口不能覆盖过滤规则")
		}
		d.Filters = old.Destinations[0].Filters
	}
	return l.saveMultiple(id, name, manualTaskRequest{Source: q.Source, Destinations: []manualDestination{d}})
}
func (l *savedTaskLibrary) remove(id taskconfig.ID) (*savedTaskLibrary, error) {
	if l == nil || l.snapshot == nil {
		return nil, taskstore.ErrBlocked
	}
	task, err := l.find(id)
	if err != nil {
		return nil, err
	}
	if err = taskConfigurationDeletionIssue(task); err != nil {
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
