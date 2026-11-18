package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"syncmaid/internal/taskconfig"
	"syncmaid/internal/windowsfs"
)

func TestReleaseTailNetworkPathClassification(t *testing.T) {
	for _, path := range []string{"\\\\server\\share\\folder", "\\\\?\\UNC\\server\\share\\folder"} {
		if !windowsfs.Network(path) {
			t.Errorf("UNC path not recognized: %s", path)
		}
	}
	if windowsfs.Network(t.TempDir()) {
		t.Fatal("local test volume mistaken for remote")
	}
}

func TestReleaseTailLegacyMutexBlocksBeforeConfigWrites(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "Data")
	ptr, err := windows.UTF16PtrFromString(legacyMutexName(data))
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := windows.CreateMutex(nil, false, ptr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = acquireOwnership(installation{directory: dir, data: data}); !errors.Is(err, errLegacyInstance) {
		windows.CloseHandle(legacy)
		t.Fatalf("same Data accepted with old mutex: %v", err)
	}
	windows.CloseHandle(legacy)
	goOwner, err := acquireOwnership(installation{directory: dir, data: data})
	if err != nil {
		t.Fatal(err)
	}
	defer goOwner.Close()
	probe, err := windows.CreateMutex(nil, false, ptr)
	if probe != 0 {
		defer windows.CloseHandle(probe)
	}
	if !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		t.Fatalf("Rust would miss Go holding the legacy mutex: %v", err)
	}
	if _, err := os.Stat(filepath.Join(data, savedTaskFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ownership claimed by writing tasks: %v", err)
	}
}

func TestReleaseTailCrossTaskAliasAndAllowedChain(t *testing.T) {
	root := t.TempDir()
	source, target, third := filepath.Join(root, "source"), filepath.Join(root, "target"), filepath.Join(root, "third")
	for _, dir := range []string{source, target, third} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "alias")
	manualJunction(t, alias, source)
	task, peer := taskconfig.NewID(), taskconfig.NewID()
	q := manualTaskRequest{Source: source, Destinations: []manualDestination{{Path: target}}}
	tasks := []taskconfig.Task{{ID: task}, {ID: peer, SourcePath: target,
		Destinations: []taskconfig.Destination{{Target: taskconfig.Target{Path: third}}}}}
	if err := validateCrossTaskPaths(context.Background(), task, q, tasks); err != nil {
		t.Fatal(err)
	}
	tasks[1].SourcePath = alias
	if err := validateCrossTaskPaths(context.Background(), task, q, tasks); err == nil {
		t.Fatal("accepted alias overlap")
	}
}

func TestReleaseTailMirrorPreservesEmptyDirectoryTime(t *testing.T) {
	p, d := mirrorFixture(t)
	sourceDir := filepath.Join(p.Source, "empty", "child")
	if err := os.MkdirAll(sourceDir, 0700); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2022, 1, 2, 3, 4, 0, 0, time.UTC)
	for _, dir := range []string{sourceDir, filepath.Dir(sourceDir)} {
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runMirrorWith(context.Background(), p, d, nil, acceptMirror, runManualFiltered, noRecycle); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Join(p.Target, "empty"), filepath.Join(p.Target, "empty", "child")} {
		got, err := os.Stat(dir)
		if err != nil || !got.IsDir() || !got.ModTime().Equal(old) {
			t.Fatalf("directory %s: %v %v", dir, got, err)
		}
	}
}

func TestReleaseTailLegacyWindowSettings(t *testing.T) {
	got, err := decodeLegacyWindowSettings([]byte(`{"CloseToTray":true,"StartMinimized":false,"Language":"ja"}`))
	if err != nil || !got.CloseToTray || got.StartMinimized {
		t.Fatalf("window settings %v %v", got, err)
	}
	if _, err := decodeLegacyWindowSettings([]byte(`{"Autostart":true}`)); err == nil {
		t.Fatal("unexpected setting accepted")
	}
}

func TestReleaseTailPollingSignalsChangeAndStops(t *testing.T) {
	path := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	ready := make(chan error, 1)
	changed, done := make(chan struct{}, 1), make(chan struct{})
	go func() {
		defer close(done)
		servePollingWatch(ctx, path, func(err error) {
			select {
			case ready <- err:
			case <-ctx.Done():
			}
		}, func() { changed <- struct{}{} })
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("poller did not enroll")
	}
	manualWrite(t, filepath.Join(path, "new.txt"), "new")
	select {
	case <-changed:
	case <-ctx.Done():
		t.Fatal("poller missed a persistent change")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("poller did not stop")
	}
}
