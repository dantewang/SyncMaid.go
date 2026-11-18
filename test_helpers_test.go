package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
)

func manualFixture(t *testing.T) syncengine.ManualRequest {
	t.Helper()
	base := t.TempDir()
	s, d := filepath.Join(base, "source"), filepath.Join(base, "target")
	for _, p := range []string{s, d} {
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	return syncengine.ManualRequest{Source: s, Target: d}
}

func manualWrite(t *testing.T, path, contents string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte(contents), 0600); e != nil {
		t.Fatal(e)
	}
}

func manualRead(t *testing.T, path, want string) {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil || string(b) != want {
		t.Fatalf("%s got %q %v want %q", path, b, e, want)
	}
}

func noTransferTemps(t *testing.T, root string) {
	t.Helper()
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if strings.HasPrefix(d.Name(), ".syncmaid-") {
			t.Errorf("leaked temp %s", p)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}

func manualJunction(t *testing.T, path, target string) {
	t.Helper()
	if b, e := exec.Command("cmd", "/c", "mklink", "/J", path, target).CombinedOutput(); e != nil {
		t.Skipf("junction fixture unavailable: %v %s", e, b)
	}
}

func mirrorFixture(t *testing.T) (syncengine.ManualRequest, syncengine.ManualDestination) {
	q, p := multiFixture(t)
	manualWrite(t, filepath.Join(p.Source, "keep.txt"), "source")
	manualWrite(t, filepath.Join(p.Target, "extra.txt"), "extra")
	d := q.Destinations[0]
	d.Strategy = taskconfig.Mirror
	d.DeleteMode = taskconfig.Permanent
	return p, d
}

func moveFixture(t *testing.T) (syncengine.ManualTaskRequest, syncengine.ManualRequest) {
	q, p := multiFixture(t)
	q.Kind = taskconfig.Moving
	for i := range q.Destinations {
		q.Destinations[i].Strategy = taskconfig.Move
		q.Destinations[i].Collision = taskconfig.Skip
	}
	return q, p
}

func absentMoveFile(t *testing.T, path string) {
	t.Helper()
	if _, e := os.Stat(path); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(path, e)
	}
}

func acceptMirror(context.Context, syncengine.MirrorPreview) error { return nil }
func noRecycle(string) error                                     { return errors.New("unexpected recycle") }

func (l *savedTaskLibrary) saveMultiple(id taskconfig.ID, name string, q syncengine.ManualTaskRequest) (*savedTaskLibrary, taskconfig.ID, error) {
	tasks, id, err := l.prepareMultiple(id, name, q)
	if err != nil {
		return nil, id, err
	}
	next, err := l.snapshot.Save(tasks)
	if err != nil {
		return nil, id, fmt.Errorf("保存任务: %w", err)
	}
	library, err := savedLibrary(next)
	return library, id, err
}

func (l *savedTaskLibrary) save(id taskconfig.ID, name string, q syncengine.ManualRequest) (*savedTaskLibrary, taskconfig.ID, error) {
	d := syncengine.ManualDestination{ID: taskconfig.NewID(), Name: "目标", Path: q.Target, Filters: syncengine.AllFileRules()}
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
		if syncengine.DestinationStrategy(old.Destinations[0]) != taskconfig.AddOnly || !syncengine.IsAllFileRules(old.Destinations[0].Filters) {
			return nil, id, errors.New("无过滤的单目标保存接口不能覆盖过滤规则")
		}
		d.Filters = old.Destinations[0].Filters
	}
	return l.saveMultiple(id, name, syncengine.ManualTaskRequest{Source: q.Source, Destinations: []syncengine.ManualDestination{d}})
}

func requestFromSaved(t taskconfig.Task) (syncengine.ManualRequest, error) {
	q, err := multipleRequestFromSaved(t)
	if err != nil {
		return syncengine.ManualRequest{}, err
	}
	if len(q.Destinations) != 1 {
		return syncengine.ManualRequest{}, errors.New("需要单目标任务")
	}
	if syncengine.DestinationStrategy(q.Destinations[0]) != taskconfig.AddOnly || !syncengine.IsAllFileRules(q.Destinations[0].Filters) {
		return syncengine.ManualRequest{}, errors.New("无过滤的单目标接口不能忽略现有过滤规则")
	}
	return syncengine.ManualRequest{Source: q.Source, Target: q.Destinations[0].Path}, nil
}

func runMultiple(ctx context.Context, q syncengine.ManualTaskRequest, notify func(syncengine.DestinationResult)) (syncengine.MultipleResult, error) {
	return syncengine.RunMultipleConfirmed(ctx, q, notify, nil, nil)
}

func runManual(ctx context.Context, q syncengine.ManualRequest, notify func(syncengine.TransferProgress)) (syncengine.TransferProgress, error) {
	return runManualWith(ctx, q, notify, syncengine.TransferIO{Copy: syncengine.CopyChecked, Publish: syncengine.PublishTransfer})
}

func runManualWith(ctx context.Context, q syncengine.ManualRequest, notify func(syncengine.TransferProgress), ops syncengine.TransferIO) (syncengine.TransferProgress, error) {
	return syncengine.RunManualWithFilters(ctx, q, notify, ops, []taskconfig.Rule{{Kind: "all"}})
}

func validateManual(ctx context.Context, q syncengine.ManualRequest) (result syncengine.ManualRequest, err error) {
	r, err := syncengine.OpenTransferRoots(ctx, q)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, r.CloseProtected(ctx)) }()
	s, d := r.Paths()
	return syncengine.ManualRequest{Source: s, Target: d}, nil
}
