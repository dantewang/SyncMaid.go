package appcore

import (
	"errors"
	"path/filepath"
	"syncmaid/internal/syncengine"

	"syncmaid/internal/filefilter"
	"syncmaid/internal/taskconfig"
	"syncmaid/internal/taskstore"
)

const SavedTaskFile = "tasks-go.json"

// The application opens this only after acquiring its installation/Data owner.
// A library is immutable once handed to the UI; workers return a new library.
type SavedTaskLibrary struct {
	Reviews  []filefilter.RegexConfirmation
	Restored map[taskconfig.ID]map[taskconfig.ID]syncengine.ReviewBundle
	Snapshot *taskstore.Snapshot
	Tasks    []taskconfig.Task
}

func LoadSavedTasks(data string) (*SavedTaskLibrary, error) {
	store, err := taskstore.NewVersioned(filepath.Join(data, SavedTaskFile))
	if err != nil {
		return nil, err
	}
	snapshot, err := store.Load()
	if err != nil {
		return nil, err
	}
	return SavedLibrary(snapshot)
}
func SavedLibrary(snapshot *taskstore.Snapshot) (*SavedTaskLibrary, error) {
	tasks, err := snapshot.Tasks()
	if err != nil {
		return nil, err
	}
	if err := UniqueSavedTasks(tasks); err != nil {
		return nil, err
	}
	l := &SavedTaskLibrary{Snapshot: snapshot, Tasks: tasks, Reviews: snapshot.Confirmations()}
	l.RestoreReviews()
	return l, nil
}

func UniqueSavedTasks(tasks []taskconfig.Task) error {
	seen := map[taskconfig.ID]bool{}
	for _, t := range tasks {
		if seen[t.ID] {
			return errors.New("任务 ID 重复，已阻止编辑与运行")
		}
		seen[t.ID] = true
	}
	return nil
}
func (l *SavedTaskLibrary) Find(id taskconfig.ID) (taskconfig.Task, error) {
	if l != nil {
		for _, t := range l.Tasks {
			if t.ID == id {
				return t, nil
			}
		}
	}
	return taskconfig.Task{}, errors.New("未找到此任务，请重新载入或新建")
}

func (l *SavedTaskLibrary) Remove(id taskconfig.ID) (*SavedTaskLibrary, error) {
	if l == nil || l.Snapshot == nil {
		return nil, taskstore.ErrBlocked
	}
	task, err := l.Find(id)
	if err != nil {
		return nil, err
	}
	if err = TaskConfigurationDeletionIssue(task); err != nil {
		return nil, err
	}
	tasks, err := l.Snapshot.Tasks()
	if err != nil {
		return nil, err
	}
	for i, t := range tasks {
		if t.ID == id {
			tasks = append(tasks[:i], tasks[i+1:]...)
			break
		}
	}
	next, err := l.Snapshot.Save(tasks)
	if err != nil {
		return nil, err
	}
	return SavedLibrary(next)
}
