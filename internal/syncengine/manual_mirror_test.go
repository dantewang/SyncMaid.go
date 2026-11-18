package syncengine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"syncmaid/internal/taskconfig"
)

func mirrorFixture(t *testing.T) (ManualRequest, ManualDestination) {
	q, p := multiFixture(t)
	manualWrite(t, filepath.Join(p.Source, "keep.txt"), "source")
	manualWrite(t, filepath.Join(p.Target, "extra.txt"), "extra")
	d := q.Destinations[0]
	d.Strategy = taskconfig.Mirror
	d.DeleteMode = taskconfig.Permanent
	return p, d
}
func acceptMirror(context.Context, MirrorPreview) error { return nil }
func noRecycle(string) error                            { return errors.New("unexpected recycle") }
func TestMirrorPermanentAndDeclined(t *testing.T) {
	for _, accept := range []bool{false, true} {
		p, d := mirrorFixture(t)
		called := 0
		progress, err := RunMirrorWith(context.Background(), p, d, nil, func(_ context.Context, v MirrorPreview) error {
			called++
			if v.DestinationID != d.ID || len(v.Paths) != 1 || v.Paths[0] != "extra.txt" || v.Mode != taskconfig.Permanent {
				t.Fatal(v)
			}
			if !accept {
				return ErrMirrorDeclined
			}
			return nil
		}, RunManualFiltered, noRecycle)
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
			if !errors.Is(err, ErrMirrorDeclined) {
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
			copy := MirrorCopy(RunManualFiltered)
			if kind == "copy" {
				copy = func(context.Context, ManualRequest, []taskconfig.Rule, func(TransferProgress)) (TransferProgress, error) {
					return TransferProgress{}, errors.New("injected copy failure")
				}
			}
			_, err := RunMirrorWith(context.Background(), p, d, nil, func(context.Context, MirrorPreview) error { t.Fatal("unsafe confirmation"); return nil }, copy, noRecycle)
			if err == nil {
				t.Fatal("unsafe success")
			}
			manualRead(t, filepath.Join(p.Target, "extra.txt"), "extra")
		})
	}
}
func TestMirrorExpiredPreviewAndCancellation(t *testing.T) {
	for _, kind := range []string{"source-reappeared", "source-emptied", "target-changed", "replacement", "cancel", "nil"} {
		t.Run(kind, func(t *testing.T) {
			p, d := mirrorFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			confirm := MirrorConfirmation(func(context.Context, MirrorPreview) error {
				switch kind {
				case "source-reappeared":
					manualWrite(t, filepath.Join(p.Source, "extra.txt"), "reappeared")
				case "source-emptied":
					if e := os.Remove(filepath.Join(p.Source, "keep.txt")); e != nil {
						t.Fatal(e)
					}
				case "target-changed":
					manualWrite(t, filepath.Join(p.Target, "extra.txt"), "changed")
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
			progress, err := RunMirrorWith(ctx, p, d, nil, confirm, RunManualFiltered, noRecycle)
			if err == nil || progress.Removed != 0 {
				t.Fatal(progress, err)
			}
			if kind == "target-changed" {
				manualRead(t, filepath.Join(p.Target, "extra.txt"), "changed")
			} else if kind != "replacement" {
				manualRead(t, filepath.Join(p.Target, "extra.txt"), "extra")
			}
		})
	}
}
func TestMirrorRecycleFailureNeverFallsBack(t *testing.T) {
	p, d := mirrorFixture(t)
	d.DeleteMode = taskconfig.Recycle
	sentinel := errors.New("injected shell refusal")
	progress, err := RunMirrorWith(context.Background(), p, d, nil, acceptMirror, RunManualFiltered, func(string) error { return sentinel })
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
	pgr, err := RunMirrorWith(ctx, p, d, nil, acceptMirror, RunManualFiltered, func(path string) error {
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

func TestMirrorPreviewCannotExpandPlan(t *testing.T) {
	p, d := mirrorFixture(t)
	progress, e := RunMirrorWith(context.Background(), p, d, nil, func(_ context.Context, v MirrorPreview) error { v.Paths[0] = "keep.txt"; return nil }, RunManualFiltered, noRecycle)
	if e != nil || progress.Removed != 1 {
		t.Fatal(progress, e)
	}
	manualRead(t, filepath.Join(p.Target, "keep.txt"), "source")
	if _, e := os.Stat(filepath.Join(p.Target, "extra.txt")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
}

func TestMirrorShellPathBoundary(t *testing.T) {
	p, e := MirrorShellPath(`\\?\D:\fixture\中文.txt`)
	if e != nil || p != `D:\fixture\中文.txt` {
		t.Fatal(p, e)
	}
	for _, s := range []string{`\\?\UNC\server\share\file`, `\\?\D:\` + strings.Repeat("中", 260)} {
		if _, e := MirrorShellPath(s); e == nil {
			t.Fatal("unsupported shell path accepted", s)
		}
	}
}
