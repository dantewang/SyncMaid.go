package syncengine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"syncmaid/internal/fileinventory"
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

func TestMirrorSkipsUnchangedFilesAndMatchingDirectories(t *testing.T) {
	q, p := multiFixture(t)
	d := q.Destinations[0]
	d.Strategy = taskconfig.Mirror
	d.DeleteMode = taskconfig.Permanent

	srcUnchanged := filepath.Join(p.Source, "sub", "same.txt")
	dstUnchanged := filepath.Join(p.Target, "sub", "same.txt")
	srcChanged := filepath.Join(p.Source, "dirty", "changed.txt")
	dstChanged := filepath.Join(p.Target, "dirty", "changed.txt")
	manualWrite(t, srcUnchanged, "same-content")
	manualWrite(t, dstUnchanged, "same-content")
	manualWrite(t, srcChanged, "new-content")
	manualWrite(t, dstChanged, "old")
	manualWrite(t, filepath.Join(p.Target, "dirty", "extra.txt"), "extra")

	old := time.Date(2023, 5, 6, 7, 8, 0, 0, time.UTC)
	for _, f := range []string{srcUnchanged, dstUnchanged, filepath.Dir(srcUnchanged), filepath.Dir(dstUnchanged)} {
		if err := os.Chtimes(f, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(filepath.Dir(srcChanged), old, old); err != nil {
		t.Fatal(err)
	}

	// Verify runManualWithSessionTarget skips unchanged files in memory without copying.
	srcInv, err := fileinventory.Scan(context.Background(), p.Source, fileinventory.Options{})
	if err != nil {
		t.Fatal(err)
	}
	dstInv, err := fileinventory.Scan(context.Background(), p.Target, fileinventory.Options{})
	if err != nil {
		t.Fatal(err)
	}
	copiedCalls := 0
	progress, err := runManualWithSessionTarget(context.Background(), p, nil, srcInv, dstInv, nil, TransferIO{
		Publish: PublishTransfer,
		Copy: func(ctx context.Context, src, tmp *os.File, cb func(int64)) error {
			copiedCalls++
			return CopyUnverified(ctx, src, tmp, cb)
		},
	}, AllFileRules())
	if err != nil {
		t.Fatal(err)
	}
	if copiedCalls != 1 || progress.Copied != 1 || progress.Unchanged != 1 {
		t.Fatalf("expected 1 copy and 1 in-memory skip, got copiedCalls=%d progress=%+v", copiedCalls, progress)
	}

	// Run full Mirror session: removes extra.txt and restores dirty dir timestamp.
	progress, err = RunMirrorSession(context.Background(), p, d, nil, nil, nil, acceptMirror, nil, noRecycle)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Unchanged != 2 || progress.Copied != 0 || progress.Removed != 1 {
		t.Fatalf("unexpected second-pass mirror progress: %+v", progress)
	}
	gotDirtyDir, err := os.Stat(filepath.Dir(dstChanged))
	if err != nil || !gotDirtyDir.ModTime().Equal(old) {
		t.Fatalf("dirty dir timestamp not restored after removal: %v %v", gotDirtyDir, err)
	}
}

func TestMirrorDistinctUnicodeCaseFoldingAndRealCaseInsensitivity(t *testing.T) {
	old := time.Date(2023, 5, 6, 7, 8, 0, 0, time.UTC)

	t.Run("distinct-unicode-declined", func(t *testing.T) {
		q, p := multiFixture(t)
		d := q.Destinations[0]
		d.Strategy = taskconfig.Mirror
		d.DeleteMode = taskconfig.Permanent

		srcFile := filepath.Join(p.Source, "\u10d0.txt")
		dstOtherFile := filepath.Join(p.Target, "\u1c90.txt")
		dstSrcFile := filepath.Join(p.Target, "\u10d0.txt")
		manualWrite(t, srcFile, "AAAA")
		manualWrite(t, dstOtherFile, "BBBB")

		for _, targetPath := range []string{srcFile, dstOtherFile} {
			if err := os.Chtimes(targetPath, old, old); err != nil {
				t.Fatal(err)
			}
		}

		if _, err := os.Stat(dstSrcFile); !errors.Is(err, os.ErrNotExist) {
			t.Skipf("filesystem treats U+10D0 and U+1C90 as the same name: %v", err)
		}
		if data, err := os.ReadFile(dstOtherFile); err != nil || string(data) != "BBBB" {
			t.Fatalf("precondition failed for target file: data=%q err=%v", data, err)
		}

		var gotPreview []string
		progress, err := RunMirrorSession(context.Background(), p, d, nil, nil, nil, func(_ context.Context, v MirrorPreview) error {
			gotPreview = append([]string(nil), v.Paths...)
			return ErrMirrorDeclined
		}, nil, noRecycle)
		if !errors.Is(err, ErrMirrorDeclined) {
			t.Fatalf("expected ErrMirrorDeclined, got %v", err)
		}
		if progress.Copied != 1 || progress.Unchanged != 0 || progress.Removed != 0 {
			t.Fatalf("unexpected progress: %+v", progress)
		}
		if len(gotPreview) != 1 || gotPreview[0] != "\u1c90.txt" {
			t.Fatalf("unexpected preview paths: %v", gotPreview)
		}
		if data, err := os.ReadFile(dstSrcFile); err != nil || string(data) != "AAAA" {
			t.Fatalf("source file was not copied: data=%q err=%v", data, err)
		}
		if data, err := os.ReadFile(dstOtherFile); err != nil || string(data) != "BBBB" {
			t.Fatalf("existing target file should remain when deletion declined: data=%q err=%v", data, err)
		}

		// Re-running Mirror while both files coexist in target must also succeed.
		progress, err = RunMirrorSession(context.Background(), p, d, nil, nil, nil, acceptMirror, nil, noRecycle)
		if err != nil {
			t.Fatal(err)
		}
		if progress.Copied != 0 || progress.Unchanged != 1 || progress.Removed != 1 {
			t.Fatalf("unexpected progress on second pass: %+v", progress)
		}
		if _, err := os.Stat(dstOtherFile); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected %s to be removed, got err=%v", dstOtherFile, err)
		}
		if data, err := os.ReadFile(dstSrcFile); err != nil || string(data) != "AAAA" {
			t.Fatalf("copied file changed unexpectedly: data=%q err=%v", data, err)
		}
	})

	t.Run("distinct-unicode-accepted", func(t *testing.T) {
		q, p := multiFixture(t)
		d := q.Destinations[0]
		d.Strategy = taskconfig.Mirror
		d.DeleteMode = taskconfig.Permanent

		srcFile := filepath.Join(p.Source, "\u10d0.txt")
		dstOtherFile := filepath.Join(p.Target, "\u1c90.txt")
		dstSrcFile := filepath.Join(p.Target, "\u10d0.txt")
		manualWrite(t, srcFile, "AAAA")
		manualWrite(t, dstOtherFile, "BBBB")

		srcDir := filepath.Join(p.Source, "\u10d0")
		dstOtherDir := filepath.Join(p.Target, "\u1c90")
		dstSrcDir := filepath.Join(p.Target, "\u10d0")
		if err := os.MkdirAll(srcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dstOtherDir, 0o755); err != nil {
			t.Fatal(err)
		}

		for _, targetPath := range []string{srcFile, dstOtherFile, srcDir, dstOtherDir} {
			if err := os.Chtimes(targetPath, old, old); err != nil {
				t.Fatal(err)
			}
		}

		if _, err := os.Stat(dstSrcFile); !errors.Is(err, os.ErrNotExist) {
			t.Skipf("filesystem treats U+10D0 and U+1C90 as the same name: %v", err)
		}
		if data, err := os.ReadFile(dstOtherFile); err != nil || string(data) != "BBBB" {
			t.Fatalf("precondition failed for target file: data=%q err=%v", data, err)
		}

		progress, err := RunMirrorSession(context.Background(), p, d, nil, nil, nil, acceptMirror, nil, noRecycle)
		if err != nil {
			t.Fatal(err)
		}
		if progress.Copied != 1 || progress.Unchanged != 0 || progress.Removed != 1 {
			t.Fatalf("unexpected progress: %+v", progress)
		}
		if _, err := os.Stat(dstOtherFile); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected %s to be removed, got err=%v", dstOtherFile, err)
		}
		if data, err := os.ReadFile(dstSrcFile); err != nil || string(data) != "AAAA" {
			t.Fatalf("copied file changed unexpectedly: data=%q err=%v", data, err)
		}
		info, err := os.Stat(dstSrcDir)
		if err != nil || !info.IsDir() || !info.ModTime().Equal(old) {
			t.Fatalf("expected directory %s with mtime %v, got info=%v err=%v", dstSrcDir, old, info, err)
		}
	})

	t.Run("real-case-insensitive-match", func(t *testing.T) {
		q, p := multiFixture(t)
		d := q.Destinations[0]
		d.Strategy = taskconfig.Mirror
		d.DeleteMode = taskconfig.Permanent

		srcFile := filepath.Join(p.Source, "Report.txt")
		dstFile := filepath.Join(p.Target, "report.txt")
		manualWrite(t, srcFile, "same")
		manualWrite(t, dstFile, "same")
		for _, targetPath := range []string{srcFile, dstFile} {
			if err := os.Chtimes(targetPath, old, old); err != nil {
				t.Fatal(err)
			}
		}

		if _, err := os.Stat(filepath.Join(p.Target, "Report.txt")); err != nil {
			t.Skipf("target directory is case-sensitive: %v", err)
		}

		progress, err := RunMirrorSession(context.Background(), p, d, nil, nil, nil, func(_ context.Context, v MirrorPreview) error {
			t.Fatalf("confirm should not be called for case-only difference, got preview=%+v", v)
			return nil
		}, nil, noRecycle)
		if err != nil {
			t.Fatal(err)
		}
		if progress.Copied != 0 || progress.Unchanged != 1 || progress.Removed != 0 {
			t.Fatalf("unexpected progress for case-insensitive match: %+v", progress)
		}
	})
}

func TestSameFileNameOrdinal(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"Report.txt", "report.txt", true},
		{"LONGFI~1.TXT", "LongFileName1234567890.txt", false},
		{"\u10d0.txt", "\u10d0.txt", true},
		{"\u10d0.txt", "\u1c90.txt", false},
	}
	for _, tc := range cases {
		got, err := sameFileNameOrdinal(tc.a, tc.b)
		if err != nil {
			t.Fatalf("sameFileNameOrdinal(%q, %q) error: %v", tc.a, tc.b, err)
		}
		if got != tc.want {
			t.Errorf("sameFileNameOrdinal(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func shortNameForFile(t *testing.T, longPath string) string {
	t.Helper()
	lp, err := windows.UTF16PtrFromString(longPath)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 32768)
	n, err := windows.GetShortPathName(lp, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) >= len(buf) {
		t.Fatalf("GetShortPathName(%q) failed: n=%d err=%v", longPath, n, err)
	}
	return filepath.Base(windows.UTF16ToString(buf[:n]))
}

func TestMirror83ShortNameAliasSafety(t *testing.T) {
	old := time.Date(2023, 5, 6, 7, 8, 0, 0, time.UTC)
	const longName = "LongFileName1234567890.txt"

	t.Run("source-short-vs-target-long", func(t *testing.T) {
		q, p := multiFixture(t)
		d := q.Destinations[0]
		d.Strategy = taskconfig.Mirror
		d.DeleteMode = taskconfig.Permanent

		dstLong := filepath.Join(p.Target, longName)
		manualWrite(t, dstLong, "BBBB")
		shortName := shortNameForFile(t, dstLong)
		if strings.EqualFold(shortName, longName) {
			t.Skip("8.3 short names disabled on temp volume")
		}
		srcShort := filepath.Join(p.Source, shortName)
		manualWrite(t, srcShort, "AAAA")
		for _, targetPath := range []string{srcShort, dstLong} {
			if err := os.Chtimes(targetPath, old, old); err != nil {
				t.Fatal(err)
			}
		}

		_, err := RunMirrorSession(context.Background(), p, d, nil, nil, nil, acceptMirror, nil, noRecycle)
		if err == nil {
			t.Fatalf("expected 8.3 short-name conflict error, got nil")
		}
		if data, readErr := os.ReadFile(dstLong); readErr != nil || string(data) != "BBBB" {
			t.Fatalf("target long file was altered or deleted: data=%q err=%v", data, readErr)
		}
	})

	t.Run("source-long-vs-target-short", func(t *testing.T) {
		q, p := multiFixture(t)
		d := q.Destinations[0]
		d.Strategy = taskconfig.Mirror
		d.DeleteMode = taskconfig.Permanent

		srcLong := filepath.Join(p.Source, longName)
		manualWrite(t, srcLong, "AAAA")
		shortName := shortNameForFile(t, srcLong)
		if strings.EqualFold(shortName, longName) {
			t.Skip("8.3 short names disabled on temp volume")
		}
		dstShort := filepath.Join(p.Target, shortName)
		manualWrite(t, dstShort, "BBBB")
		for _, targetPath := range []string{srcLong, dstShort} {
			if err := os.Chtimes(targetPath, old, old); err != nil {
				t.Fatal(err)
			}
		}

		progress, err := RunMirrorSession(context.Background(), p, d, nil, nil, nil, acceptMirror, nil, noRecycle)
		if err != nil {
			t.Fatal(err)
		}
		if progress.Copied != 1 || progress.Removed != 1 {
			t.Fatalf("unexpected progress: %+v", progress)
		}
		if data, readErr := os.ReadFile(filepath.Join(p.Target, longName)); readErr != nil || string(data) != "AAAA" {
			t.Fatalf("expected copied long file, got data=%q err=%v", data, readErr)
		}
		if _, statErr := os.Stat(dstShort); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("expected target short-named file %s to be removed, got err=%v", dstShort, statErr)
		}
	})
}
