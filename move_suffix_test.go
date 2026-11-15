package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
	"syncmaid/internal/taskconfig"
)

func TestMoveSuffixNames(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"report.pdf", "report (2).pdf"}, {"archive.tar.gz", "archive.tar (2).gz"}, {"readme", "readme (2)"}, {"中文报告.txt", "中文报告 (2).txt"}, {".env", " (2).env"},
	} {
		if got := moveSuffixName(c.name, 2); got != c.want {
			t.Fatal(c, got)
		}
		if moveSuffixName(c.name, 1) != c.name {
			t.Fatal("original name changed")
		}
	}
}
func TestMoveSuffixExistingNumbersAndStructure(t *testing.T) {
	for _, name := range []string{"中文/报告.tar.gz", "无扩展名"} {
		t.Run(name, func(t *testing.T) {
			q, p := moveFixture(t)
			q.Destinations[0].Collision = taskconfig.Suffix
			manualWrite(t, filepath.Join(p.Source, filepath.FromSlash(name)), "new-content")
			parent, leaf := filepath.Split(filepath.FromSlash(name))
			for n := 1; n <= 2; n++ {
				manualWrite(t, filepath.Join(p.Target, parent, moveSuffixName(leaf, n)), "existing")
			}
			r, e := runMultiple(context.Background(), q, nil)
			if e != nil || r[q.Destinations[0].ID].Progress.Moved != 1 {
				t.Fatal(r, e)
			}
			absentMoveFile(t, filepath.Join(p.Source, filepath.FromSlash(name)))
			for n := 1; n <= 2; n++ {
				manualRead(t, filepath.Join(p.Target, parent, moveSuffixName(leaf, n)), "existing")
			}
			manualRead(t, filepath.Join(p.Target, parent, moveSuffixName(leaf, 3)), "new-content")
		})
	}
}
func TestMoveSuffixFlattenInternalCollision(t *testing.T) {
	q, p := moveFixture(t)
	q.Destinations[0].Flatten = true
	q.Destinations[0].Collision = taskconfig.Suffix
	manualWrite(t, filepath.Join(p.Source, "a/f.txt"), "first")
	manualWrite(t, filepath.Join(p.Source, "b/f.txt"), "second")
	r, e := runMultiple(context.Background(), q, nil)
	if e != nil || r[q.Destinations[0].ID].Progress.Moved != 2 {
		t.Fatal(r, e)
	}
	manualRead(t, filepath.Join(p.Target, "f.txt"), "first")
	manualRead(t, filepath.Join(p.Target, "f (2).txt"), "second")
	absentMoveFile(t, filepath.Join(p.Source, "a/f.txt"))
	absentMoveFile(t, filepath.Join(p.Source, "b/f.txt"))
}
func TestMoveSuffixPublishRaceAndOneCopy(t *testing.T) {
	_, p := moveFixture(t)
	manualWrite(t, filepath.Join(p.Source, "f.txt"), "moving")
	roots, e := openTransferRoots(context.Background(), p)
	if e != nil {
		t.Fatal(e)
	}
	defer roots.Close()
	files, stamps, e := scanMoveFiles(context.Background(), roots.source)
	if e != nil {
		t.Fatal(e)
	}
	copies, attempts := 0, 0
	ops := transferIO{collision: taskconfig.Suffix, copy: func(ctx context.Context, s, d *os.File, p func(int64)) error {
		copies++
		return copyChecked(ctx, s, d, p)
	}, publish: func(f *os.File, h windows.Handle, name string, replace bool) error {
		attempts++
		if replace {
			t.Fatal("overwrite attempted")
		}
		if attempts == 1 {
			manualWrite(t, filepath.Join(p.Target, name), "racing-writer")
		}
		return publishTransfer(f, h, name, replace)
	}}
	stamp := stamps[files[0].Path]
	published, e := transferFileTo(context.Background(), roots, files[0], "f.txt", &stamp, ops, nil)
	if e != nil || !published || copies != 1 || attempts != 2 {
		t.Fatal(published, copies, attempts, e)
	}
	manualRead(t, filepath.Join(p.Target, "f.txt"), "racing-writer")
	manualRead(t, filepath.Join(p.Target, "f (2).txt"), "moving")
	absentMoveFile(t, filepath.Join(p.Source, "f.txt"))
}
func TestMoveSuffixCancellationBoundAndAmbiguousFailure(t *testing.T) {
	for _, kind := range []string{"cancel", "limit", "error", "renamed-collision", "renamed-error"} {
		t.Run(kind, func(t *testing.T) {
			_, p := moveFixture(t)
			manualWrite(t, filepath.Join(p.Source, "f.txt"), "keep-source")
			roots, e := openTransferRoots(context.Background(), p)
			if e != nil {
				t.Fatal(e)
			}
			defer roots.Close()
			files, stamps, e := scanMoveFiles(context.Background(), roots.source)
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			attempts, copies := 0, 0
			ops := transferIO{collision: taskconfig.Suffix, copy: func(ctx context.Context, s, d *os.File, p func(int64)) error {
				copies++
				return copyChecked(ctx, s, d, p)
			}, publish: func(f *os.File, h windows.Handle, name string, replace bool) error {
				attempts++
				if replace {
					t.Fatal("overwrite attempted")
				}
				switch kind {
				case "cancel":
					cancel()
					return windows.ERROR_ALREADY_EXISTS
				case "limit":
					return windows.ERROR_ALREADY_EXISTS // Inject contention without thousands of fixture files.
				case "error":
					return windows.ERROR_ACCESS_DENIED
				default:
					// Fault injection: a completed rename followed by an inconsistent error.
					// This is not a claim that Windows has exhibited this behavior.
					if e := publishTransfer(f, h, name, false); e != nil {
						return e
					}
					if kind == "renamed-collision" {
						return windows.ERROR_ALREADY_EXISTS
					}
					return windows.ERROR_ACCESS_DENIED
				}
			}}
			stamp := stamps[files[0].Path]
			published, e := transferFileTo(ctx, roots, files[0], "f.txt", &stamp, ops, nil)
			if e == nil || published || copies != 1 {
				t.Fatal(published, copies, e)
			}
			manualRead(t, filepath.Join(p.Source, "f.txt"), "keep-source")
			if kind == "cancel" || kind == "limit" {
				entries, err := os.ReadDir(p.Target)
				if err != nil || len(entries) != 0 {
					t.Fatal(entries, err)
				}
				if kind == "cancel" && !errors.Is(e, context.Canceled) {
					t.Fatal(e)
				}
				if kind == "limit" && attempts != moveNameAttempts {
					t.Fatal(attempts)
				}
			} else {
				if !errors.Is(e, errTransferPublicationUnknown) || attempts != 1 {
					t.Fatal(attempts, e)
				}
				if kind != "error" {
					manualRead(t, filepath.Join(p.Target, "f.txt"), "keep-source")
				}
			}
		})
	}
}
func TestMoveSuffixPersistenceAndUI(t *testing.T) {
	data := t.TempDir()
	l, e := loadSavedTasks(data)
	if e != nil {
		t.Fatal(e)
	}
	q, _ := moveFixture(t)
	l, id, e := l.saveMultiple("", "suffix", q)
	if e != nil {
		t.Fatal(e)
	}
	a := application{tasks: l}
	a.selectManual(id)
	a.setMoveCollision(taskconfig.Suffix)
	l, _, e = l.saveMultiple(id, "suffix", a.manual.draftRequest())
	if e != nil {
		t.Fatal(e)
	}
	l, e = loadSavedTasks(data)
	if e != nil {
		t.Fatal(e)
	}
	task, _ := l.find(id)
	request, e := multipleRequestFromSaved(task)
	if e != nil || request.Destinations[0].Collision != taskconfig.Suffix {
		t.Fatal(request, e)
	}
	a.manual.phase = manualRunning
	a.setMoveCollision(taskconfig.Skip)
	if a.manual.targets[0].Collision != taskconfig.Suffix {
		t.Fatal("running edit")
	}
	task.Kind = taskconfig.Sync
	task.Destinations[0].Strategy = taskconfig.AddOnly
	if _, e = multipleRequestFromSaved(task); e == nil {
		t.Fatal("Sync suffix silently accepted")
	}
}

func TestSyncSuffixRejectedBeforeWrite(t *testing.T) {
	q, p := multiFixture(t)
	q.Destinations = q.Destinations[:1]
	q.Destinations[0].Collision = taskconfig.Suffix
	manualWrite(t, filepath.Join(p.Source, "keep.txt"), "source")
	results, e := runMultiple(context.Background(), q, nil)
	if e == nil || results[q.Destinations[0].ID].State != destinationFailed {
		t.Fatal(results, e)
	}
	manualRead(t, filepath.Join(p.Source, "keep.txt"), "source")
	entries, e := os.ReadDir(p.Target)
	if e != nil || len(entries) != 0 {
		t.Fatal(entries, e)
	}
}
