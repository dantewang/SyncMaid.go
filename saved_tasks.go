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
	if err := uniqueSavedTasks(tasks); err != nil {
		return nil, err
	}
	l := &savedTaskLibrary{snapshot: snapshot, tasks: tasks, reviews: snapshot.Confirmations()}
	l.restoreReviews()
	return l, nil
}

func uniqueSavedTasks(tasks []taskconfig.Task) error {
	seen := map[taskconfig.ID]bool{}
	for _, t := range tasks {
		if seen[t.ID] {
			return errors.New("任务 ID 重复，已阻止编辑与运行")
		}
		seen[t.ID] = true
	}
	return nil
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
