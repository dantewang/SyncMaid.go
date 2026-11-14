package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"syncmaid/internal/taskconfig"
)

func mirrorFixture(t *testing.T) (manualRequest, manualDestination) {
	q, p := multiFixture(t)
	manualWrite(t, filepath.Join(p.Source, "keep.txt"), "source")
	manualWrite(t, filepath.Join(p.Target, "extra.txt"), "extra")
	d := q.Destinations[0]
	d.Strategy = taskconfig.Mirror
	d.DeleteMode = taskconfig.Permanent
	return p, d
}
func acceptMirror(context.Context, mirrorPreview) error { return nil }
func noRecycle(string) error                            { return errors.New("unexpected recycle") }
func TestMirrorPermanentAndDeclined(t *testing.T) {
	for _, accept := range []bool{false, true} {
		p, d := mirrorFixture(t)
		called := 0
		progress, err := runMirrorWith(context.Background(), p, d, nil, func(_ context.Context, v mirrorPreview) error {
			called++
			if v.DestinationID != d.ID || len(v.Paths) != 1 || v.Paths[0] != "extra.txt" || v.Mode != taskconfig.Permanent {
				t.Fatal(v)
			}
			if !accept {
				return errMirrorDeclined
			}
			return nil
		}, runManualFiltered, noRecycle)
		if called != 1 {
			t.Fatal(called)
		}
		manualRead(t, filepath.Join(p.Target, "keep.txt"), "source")
		if accept {
			if err != nil || progress.Removed != 1 {
				t.Fatal(progress, err)
			}
			if _, e := os.Stat(filepath.Join(p.Target, "extra.txt")); !errors.Is(e, os.ErrNotExist) {
				t.Fatal(e)
			}
		} else {
			if !errors.Is(err, errMirrorDeclined) {
				t.Fatal(err)
			}
			manualRead(t, filepath.Join(p.Target, "extra.txt"), "extra")
		}
	}
}
func TestMirrorUnsafeSourceAndCopyFailureNeverConfirm(t *testing.T) {
	for _, kind := range []string{"empty", "missing", "link", "copy"} {
		t.Run(kind, func(t *testing.T) {
			p, d := mirrorFixture(t)
			switch kind {
			case "empty":
				if e := os.Remove(filepath.Join(p.Source, "keep.txt")); e != nil {
					t.Fatal(e)
				}
			case "missing":
				p.Source = filepath.Join(t.TempDir(), "missing")
			case "link":
				manualJunction(t, filepath.Join(p.Source, "link"), t.TempDir())
			}
			copy := mirrorCopy(runManualFiltered)
			if kind == "copy" {
				copy = func(context.Context, manualRequest, []taskconfig.Rule, func(transferProgress)) (transferProgress, error) {
					return transferProgress{}, errors.New("injected copy failure")
				}
			}
			_, err := runMirrorWith(context.Background(), p, d, nil, func(context.Context, mirrorPreview) error { t.Fatal("unsafe confirmation"); return nil }, copy, noRecycle)
			if err == nil {
				t.Fatal("unsafe success")
			}
			manualRead(t, filepath.Join(p.Target, "extra.txt"), "extra")
		})
	}
}
func TestMirrorExpiredPreviewAndCancellation(t *testing.T) {
	for _, kind := range []string{"source", "target", "replacement", "cancel", "nil"} {
		t.Run(kind, func(t *testing.T) {
			p, d := mirrorFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			confirm := mirrorConfirmation(func(context.Context, mirrorPreview) error {
				switch kind {
				case "source":
					manualWrite(t, filepath.Join(p.Source, "new.txt"), "changed")
				case "target":
					manualWrite(t, filepath.Join(p.Target, "new.txt"), "changed")
				case "replacement":
					if e := os.Remove(filepath.Join(p.Target, "extra.txt")); e != nil {
						t.Fatal(e)
					}
					manualJunction(t, filepath.Join(p.Target, "extra.txt"), t.TempDir())
				case "cancel":
					cancel()
				}
				return nil
			})
			if kind == "nil" {
				confirm = nil
			}
			progress, err := runMirrorWith(ctx, p, d, nil, confirm, runManualFiltered, noRecycle)
			if err == nil || progress.Removed != 0 {
				t.Fatal(progress, err)
			}
			if kind != "replacement" {
				manualRead(t, filepath.Join(p.Target, "extra.txt"), "extra")
			}
		})
	}
}
func TestMirrorRecycleFailureNeverFallsBack(t *testing.T) {
	p, d := mirrorFixture(t)
	d.DeleteMode = taskconfig.Recycle
	sentinel := errors.New("injected shell refusal")
	progress, err := runMirrorWith(context.Background(), p, d, nil, acceptMirror, runManualFiltered, func(string) error { return sentinel })
	if !errors.Is(err, sentinel) || progress.Removed != 0 {
		t.Fatal(progress, err)
	}
	manualRead(t, filepath.Join(p.Target, "extra.txt"), "extra")
}

func TestMirrorStopsAfterCommittedRemoval(t *testing.T) {
	p, d := mirrorFixture(t)
	d.DeleteMode = taskconfig.Recycle
	manualWrite(t, filepath.Join(p.Target, "z.txt"), "remaining")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pgr, err := runMirrorWith(ctx, p, d, nil, acceptMirror, runManualFiltered, func(path string) error {
		// Inject the shell boundary, not a claim of real recycling.
		e := os.Remove(path)
		cancel()
		return e
	})
	if !errors.Is(err, context.Canceled) || pgr.Removed != 1 {
		t.Fatal(pgr, err)
	}
	manualRead(t, filepath.Join(p.Target, "z.txt"), "remaining")
}

func TestMirrorConfirmationReplyIsSingleUse(t *testing.T) {
	reply := make(chan error, 1)
	a := application{manual: manualSession{mirrorReply: reply, mirrorPreview: &mirrorPreview{}}}
	a.answerMirror(false)
	a.answerMirror(true)
	if !errors.Is(<-reply, errMirrorDeclined) || a.manual.mirrorReply != nil || a.manual.mirrorPreview != nil {
		t.Fatal("stale confirmation retained")
	}
}

func TestMirrorPreviewCannotExpandPlan(t *testing.T) {
	p, d := mirrorFixture(t)
	progress, e := runMirrorWith(context.Background(), p, d, nil, func(_ context.Context, v mirrorPreview) error { v.Paths[0] = "keep.txt"; return nil }, runManualFiltered, noRecycle)
	if e != nil || progress.Removed != 1 {
		t.Fatal(progress, e)
	}
	manualRead(t, filepath.Join(p.Target, "keep.txt"), "source")
	if _, e := os.Stat(filepath.Join(p.Target, "extra.txt")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
}

func TestMirrorShellPathBoundary(t *testing.T) {
	p, e := mirrorShellPath(`\\?\D:\fixture\中文.txt`)
	if e != nil || p != `D:\fixture\中文.txt` {
		t.Fatal(p, e)
	}
	for _, s := range []string{`\\?\UNC\server\share\file`, `\\?\D:\` + strings.Repeat("中", 260)} {
		if _, e := mirrorShellPath(s); e == nil {
			t.Fatal("unsupported shell path accepted", s)
		}
	}
}
func TestMirrorFailureIsolatedAndPersistence(t *testing.T) {
	q, p := multiFixture(t)
	q.Destinations[0].Strategy = taskconfig.Mirror
	q.Destinations[0].DeleteMode = taskconfig.Permanent
	manualWrite(t, filepath.Join(p.Source, "f.txt"), "input")
	manualWrite(t, filepath.Join(p.Target, "extra.txt"), "keep")
	r, err := runMultipleConfirmed(context.Background(), q, nil, func(context.Context, mirrorPreview) error { return errMirrorDeclined })
	if !errors.Is(err, errMirrorDeclined) || r[q.Destinations[0].ID].State != destinationFailed || r[q.Destinations[1].ID].State != destinationSucceeded {
		t.Fatal(r, err)
	}
	data := t.TempDir()
	l, e := loadSavedTasks(data)
	if e != nil {
		t.Fatal(e)
	}
	l, id, e := l.saveMultiple("", "Mirror", q)
	if e != nil {
		t.Fatal(e)
	}
	l, e = loadSavedTasks(data)
	if e != nil {
		t.Fatal(e)
	}
	record, e := l.find(id)
	if e != nil {
		t.Fatal(e)
	}
	saved, e := multipleRequestFromSaved(record)
	if e != nil || saved.Destinations[0].Strategy != taskconfig.Mirror || saved.Destinations[0].DeleteMode != taskconfig.Permanent {
		t.Fatal(saved, e)
	}
	q.Destinations[0].Filters = []taskconfig.Rule{}
	if _, _, e = l.saveMultiple(id, "invalid", q); e == nil {
		t.Fatal("filtered mirror saved")
	}
}
