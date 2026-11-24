package syncengine

import (
	"context"
	"errors"
	"fmt"
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
func seedSharedGuardFiles(t *testing.T, source, target string, count int) {
	t.Helper()
	now := time.Now().Add(-2 * time.Minute).Truncate(time.Second)
	for i := 0; i < count; i++ {
		name := filepath.Join("guard", "g-"+string(rune('a'+i))+".txt")
		sp := filepath.Join(source, name)
		tp := filepath.Join(target, name)
		manualWrite(t, sp, "same")
		manualWrite(t, tp, "same")
		if err := os.Chtimes(sp, now, now); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(tp, now, now); err != nil {
			t.Fatal(err)
		}
	}
}
func seedExtraTargetFiles(t *testing.T, target string, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		manualWrite(t, filepath.Join(target, "z-extra-"+string(rune('a'+i))+".txt"), "extra")
	}
}
func acceptMirror(context.Context, MirrorPreview) error { return nil }
func noRecycle(string) error                            { return errors.New("unexpected recycle") }
func TestMirrorPermanentAndDeclined(t *testing.T) {
	for _, accept := range []bool{false, true} {
		p, d := mirrorFixture(t)
		seedSharedGuardFiles(t, p.Source, p.Target, 9)
		d.MassDeleteThreshold = 0.1
		d.MassDeleteThresholdSet = true
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
			seedExtraTargetFiles(t, p.Target, 9)
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
			switch kind {
			case "source-reappeared", "target-changed":
				if err != nil || progress.Deferred != 1 || progress.Removed != 9 {
					t.Fatal(progress, err)
				}
			case "replacement":
				if err == nil || progress.Failed != 1 {
					t.Fatalf("expected hard failure on replacement, got progress=%+v err=%v", progress, err)
				}
			default:
				if err == nil || progress.Removed != 0 {
					t.Fatal(progress, err)
				}
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

		seedSharedGuardFiles(t, p.Source, p.Target, 9)
		d.MassDeleteThreshold = 0.1
		d.MassDeleteThresholdSet = true

		var gotPreview []string
		progress, err := RunMirrorSession(context.Background(), p, d, nil, nil, nil, func(_ context.Context, v MirrorPreview) error {
			gotPreview = append([]string(nil), v.Paths...)
			return ErrMirrorDeclined
		}, nil, noRecycle)
		if !errors.Is(err, ErrMirrorDeclined) {
			t.Fatalf("expected ErrMirrorDeclined, got %v", err)
		}
		if progress.Copied != 1 || progress.Unchanged != 9 || progress.Removed != 0 {
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
		if progress.Copied != 0 || progress.Unchanged != 10 || progress.Removed != 1 {
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

	t.Run("source-short-dir-vs-target-long-dir", func(t *testing.T) {
		q, p := multiFixture(t)
		d := q.Destinations[0]
		d.Strategy = taskconfig.Mirror
		d.DeleteMode = taskconfig.Permanent

		const longDirName = "LongDirName1234567890"
		dstDir := filepath.Join(p.Target, longDirName)
		dstSame := filepath.Join(dstDir, "same.txt")
		manualWrite(t, dstSame, "BBBB")
		shortDir := shortNameForFile(t, dstDir)
		if strings.EqualFold(shortDir, longDirName) {
			t.Skip("8.3 short names disabled on temp volume")
		}

		srcSame := filepath.Join(p.Source, shortDir, "same.txt")
		srcNested := filepath.Join(p.Source, shortDir, "nested", "new.txt")
		manualWrite(t, srcSame, "AAAA")
		manualWrite(t, srcNested, "CCCC")
		for _, targetPath := range []string{dstSame, srcSame, srcNested} {
			if err := os.Chtimes(targetPath, old, old); err != nil {
				t.Fatal(err)
			}
		}

		_, err := RunMirrorSession(context.Background(), p, d, nil, nil, nil, acceptMirror, nil, noRecycle)
		if err == nil {
			t.Fatalf("expected 8.3 short-name directory conflict error, got nil")
		}
		if data, readErr := os.ReadFile(dstSame); readErr != nil || string(data) != "BBBB" {
			t.Fatalf("target long dir file was altered or deleted: data=%q err=%v", data, readErr)
		}
		if _, statErr := os.Stat(filepath.Join(dstDir, "nested")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("unexpected nested directory created inside aliased target dir: err=%v", statErr)
		}
	})

	t.Run("source-long-dir-vs-target-short-dir", func(t *testing.T) {
		q, p := multiFixture(t)
		d := q.Destinations[0]
		d.Strategy = taskconfig.Mirror
		d.DeleteMode = taskconfig.Permanent

		const longDirName = "LongDirName1234567890"
		srcDir := filepath.Join(p.Source, longDirName)
		srcSame := filepath.Join(srcDir, "same.txt")
		manualWrite(t, srcSame, "AAAA")
		shortDir := shortNameForFile(t, srcDir)
		if strings.EqualFold(shortDir, longDirName) {
			t.Skip("8.3 short names disabled on temp volume")
		}

		dstSame := filepath.Join(p.Target, shortDir, "same.txt")
		manualWrite(t, dstSame, "BBBB")
		for _, targetPath := range []string{srcSame, dstSame} {
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
		if data, readErr := os.ReadFile(filepath.Join(p.Target, longDirName, "same.txt")); readErr != nil || string(data) != "AAAA" {
			t.Fatalf("expected copied file in long dir, got data=%q err=%v", data, readErr)
		}
		if _, statErr := os.Stat(dstSame); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("expected %s to be removed, got err=%v", dstSame, statErr)
		}
	})

	t.Run("empty-source-short-dir-blocks-before-delete", func(t *testing.T) {
		q, p := multiFixture(t)
		d := q.Destinations[0]
		d.Strategy = taskconfig.Mirror
		d.DeleteMode = taskconfig.Permanent

		const longDirName = "Quarterly financial reports"
		dstDir := filepath.Join(p.Target, longDirName)
		dstFile := filepath.Join(dstDir, "a.txt")
		manualWrite(t, dstFile, "BBBB")
		shortDir := shortNameForFile(t, dstDir)
		if strings.EqualFold(shortDir, longDirName) {
			t.Skip("8.3 short names disabled on temp volume")
		}

		srcKeep := filepath.Join(p.Source, "keep.txt")
		srcEmptyShortDir := filepath.Join(p.Source, shortDir)
		manualWrite(t, srcKeep, "AAAA")
		if err := os.MkdirAll(srcEmptyShortDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, targetPath := range []string{dstFile, srcKeep, srcEmptyShortDir} {
			if err := os.Chtimes(targetPath, old, old); err != nil {
				t.Fatal(err)
			}
		}

		progress, err := RunMirrorSession(context.Background(), p, d, nil, nil, nil, func(_ context.Context, v MirrorPreview) error {
			t.Fatalf("confirm should not be called when directory short-name conflict exists, got preview=%+v", v)
			return nil
		}, nil, noRecycle)
		if !errors.Is(err, errShortNameConflict) {
			t.Fatalf("expected errShortNameConflict, got %v", err)
		}
		if progress.Copied != 0 || progress.Removed != 0 {
			t.Fatalf("expected no copies or removals before conflict check, got %+v", progress)
		}
		if data, readErr := os.ReadFile(dstFile); readErr != nil || string(data) != "BBBB" {
			t.Fatalf("target file altered or deleted: data=%q err=%v", data, readErr)
		}
	})
}

func TestMirrorNameVerifyPathNotFoundNeverDeletes(t *testing.T) {
	q, p := multiFixture(t)
	d := q.Destinations[0]
	d.Strategy = taskconfig.Mirror
	d.DeleteMode = taskconfig.Permanent

	manualWrite(t, filepath.Join(p.Source, "Report.txt"), "same")
	dstFile := filepath.Join(p.Target, "report.txt")
	manualWrite(t, dstFile, "same")
	if _, err := os.Stat(filepath.Join(p.Source, "report.txt")); err != nil {
		t.Skipf("source directory is case-sensitive: %v", err)
	}

	orig := openedFinalPath
	t.Cleanup(func() { openedFinalPath = orig })
	openedFinalPath = func(windows.Handle) (string, error) {
		return "", windows.ERROR_PATH_NOT_FOUND
	}

	roots, err := OpenTransferRoots(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := roots.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}()
	exists, err := mirrorSourceExists(roots.source, "report.txt")
	if err == nil || exists {
		t.Fatalf("expected error from post-open name query failure, got exists=%v err=%v", exists, err)
	}
	if errors.Is(err, windows.ERROR_PATH_NOT_FOUND) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		t.Fatalf("post-open name query error must not unwrap as missing file: %v", err)
	}

	confirmCalled := false
	_, err = RunMirrorWith(context.Background(), p, d, nil, func(_ context.Context, v MirrorPreview) error {
		confirmCalled = true
		t.Fatalf("confirm should not be called on name query failure, got preview=%+v", v)
		return nil
	}, func(context.Context, ManualRequest, []taskconfig.Rule, func(TransferProgress)) (TransferProgress, error) {
		return TransferProgress{Stage: "已完成", Total: 1, Unchanged: 1}, nil
	}, noRecycle)
	if err == nil || confirmCalled {
		t.Fatalf("expected error without confirm, got err=%v confirmCalled=%v", err, confirmCalled)
	}
	if data, readErr := os.ReadFile(dstFile); readErr != nil || string(data) != "same" {
		t.Fatalf("target file was altered or deleted: data=%q err=%v", data, readErr)
	}
}

func TestMirrorReplacedShortTargetWithLongAliasNeverDeletes(t *testing.T) {
	old := time.Date(2023, 5, 6, 7, 8, 0, 0, time.UTC)
	const longName = "Quarterly financial report.txt"

	probeDir := t.TempDir()
	probeLong := filepath.Join(probeDir, longName)
	manualWrite(t, probeLong, "old1")
	shortName := shortNameForFile(t, probeLong)
	if strings.EqualFold(shortName, longName) {
		t.Skip("8.3 short names disabled on temp volume")
	}

	replaceTarget := func(t *testing.T, targetDir string) string {
		t.Helper()
		shortPath := filepath.Join(targetDir, shortName)
		longPath := filepath.Join(targetDir, longName)
		if err := os.Remove(shortPath); err != nil {
			t.Fatal(err)
		}
		manualWrite(t, longPath, "new2")
		if err := os.Chtimes(longPath, old, old); err != nil {
			t.Fatal(err)
		}
		if gotShort := shortNameForFile(t, longPath); !strings.EqualFold(gotShort, shortName) {
			t.Skipf("OS assigned different short name %q instead of %q", gotShort, shortName)
		}
		return longPath
	}

	t.Run("before-preview", func(t *testing.T) {
		q, p := multiFixture(t)
		d := q.Destinations[0]
		d.Strategy = taskconfig.Mirror
		d.DeleteMode = taskconfig.Permanent

		manualWrite(t, filepath.Join(p.Source, "keep.txt"), "source")
		shortPath := filepath.Join(p.Target, shortName)
		manualWrite(t, shortPath, "old1")
		if err := os.Chtimes(shortPath, old, old); err != nil {
			t.Fatal(err)
		}

		var longPath string
		_, err := RunMirrorWith(context.Background(), p, d, nil, func(_ context.Context, v MirrorPreview) error {
			t.Fatalf("confirm should not be called after short-to-long replacement, got preview=%+v", v)
			return nil
		}, func(ctx context.Context, req ManualRequest, rules []taskconfig.Rule, notify func(TransferProgress)) (TransferProgress, error) {
			prog, copyErr := RunManualFiltered(ctx, req, rules, notify)
			if copyErr != nil {
				return prog, copyErr
			}
			longPath = replaceTarget(t, p.Target)
			return prog, nil
		}, noRecycle)
		if !errors.Is(err, ErrMirrorChanged) {
			t.Fatalf("expected ErrMirrorChanged, got %v", err)
		}
		if data, readErr := os.ReadFile(longPath); readErr != nil || string(data) != "new2" {
			t.Fatalf("replaced long file was altered or deleted: data=%q err=%v", data, readErr)
		}
	})

	t.Run("during-confirm", func(t *testing.T) {
		q, p := multiFixture(t)
		d := q.Destinations[0]
		d.Strategy = taskconfig.Mirror
		d.DeleteMode = taskconfig.Permanent
		d.MassDeleteThreshold = 0.1
		d.MassDeleteThresholdSet = true

		manualWrite(t, filepath.Join(p.Source, "keep.txt"), "source")
		seedSharedGuardFiles(t, p.Source, p.Target, 9)
		shortPath := filepath.Join(p.Target, shortName)
		manualWrite(t, shortPath, "old1")
		if err := os.Chtimes(shortPath, old, old); err != nil {
			t.Fatal(err)
		}

		var longPath string
		progress, err := RunMirrorWith(context.Background(), p, d, nil, func(_ context.Context, v MirrorPreview) error {
			if len(v.Paths) != 1 || v.Paths[0] != shortName {
				t.Fatalf("unexpected preview: %+v", v)
			}
			longPath = replaceTarget(t, p.Target)
			return nil
		}, RunManualFiltered, noRecycle)
		if err != nil || progress.Deferred != 1 || progress.Removed != 0 {
			t.Fatalf("expected deferred candidate with zero deletions, got progress=%+v err=%v", progress, err)
		}
		if data, readErr := os.ReadFile(longPath); readErr != nil || string(data) != "new2" {
			t.Fatalf("replaced long file was altered or deleted: data=%q err=%v", data, readErr)
		}
	})
}

func TestEvaluateMirrorGuard(t *testing.T) {
	if got := EvaluateMirrorGuard(0, 100, true, 0.5); got != MirrorGuardAllowed {
		t.Fatalf("zero deletes should be allowed, got %v", got)
	}
	if got := EvaluateMirrorGuard(1, 100, true, 0.0); got != MirrorGuardEmptySource {
		t.Fatalf("empty source must always block, got %v", got)
	}
	if got := EvaluateMirrorGuard(9, 9, false, 0.5); got != MirrorGuardAllowed {
		t.Fatalf("below min target file floor should allow, got %v", got)
	}
	if got := EvaluateMirrorGuard(4, 10, false, 0.5); got != MirrorGuardAllowed {
		t.Fatalf("4/10 with 0.5 threshold should allow, got %v", got)
	}
	if got := EvaluateMirrorGuard(5, 10, false, 0.5); got != MirrorGuardNeedsConfirmation {
		t.Fatalf("5/10 with 0.5 threshold should need confirmation, got %v", got)
	}
	if got := EvaluateMirrorGuard(99, 100, false, 0.0); got != MirrorGuardAllowed {
		t.Fatalf("0.0 threshold should allow without confirmation, got %v", got)
	}
}

func TestMirrorRatioGuardAutoDeleteVsConfirm(t *testing.T) {
	t.Run("below-threshold-auto-deletes", func(t *testing.T) {
		p, d := mirrorFixture(t)
		// mirrorFixture already has 1 extra ("extra.txt") and 1 keep ("keep.txt").
		// Add 5 more shared and 3 more extra => 10 target files total, 4 extra (40% < 50%).
		seedSharedGuardFiles(t, p.Source, p.Target, 5)
		seedExtraTargetFiles(t, p.Target, 3)

		called := 0
		progress, err := RunMirrorWith(context.Background(), p, d, nil, func(context.Context, MirrorPreview) error {
			called++
			return nil
		}, RunManualFiltered, noRecycle)
		if err != nil {
			t.Fatal(err)
		}
		if called != 0 || progress.Removed != 4 {
			t.Fatalf("expected auto-delete of 4 files without confirm, got called=%d progress=%+v", called, progress)
		}
	})

	t.Run("at-threshold-confirms-with-dirs", func(t *testing.T) {
		p, d := mirrorFixture(t)
		seedSharedGuardFiles(t, p.Source, p.Target, 5)
		seedExtraTargetFiles(t, p.Target, 4)
		if err := os.MkdirAll(filepath.Join(p.Target, "old-dir", "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		// 5 shared + 5 extra (extra.txt + 4 z-extra) = 10 target files, 5 extra = 50% >= 0.5 threshold.
		var preview MirrorPreview
		called := 0
		progress, err := RunMirrorWith(context.Background(), p, d, nil, func(_ context.Context, v MirrorPreview) error {
			called++
			preview = v
			return nil
		}, RunManualFiltered, noRecycle)
		if err != nil {
			t.Fatal(err)
		}
		if called != 1 || len(preview.Paths) != 5 || len(preview.Dirs) != 2 {
			t.Fatalf("unexpected confirm preview: called=%d preview=%+v", called, preview)
		}
		if progress.Removed != 5 || progress.RemovedDirs != 2 {
			t.Fatalf("unexpected progress: %+v", progress)
		}
	})

	t.Run("zero-threshold-disables-ratio-confirm", func(t *testing.T) {
		p, d := mirrorFixture(t)
		seedExtraTargetFiles(t, p.Target, 9)
		d.MassDeleteThreshold = 0
		d.MassDeleteThresholdSet = true

		called := 0
		progress, err := RunMirrorWith(context.Background(), p, d, nil, func(context.Context, MirrorPreview) error {
			called++
			return nil
		}, RunManualFiltered, noRecycle)
		if err != nil || called != 0 || progress.Removed != 10 {
			t.Fatalf("expected 0%% threshold to auto-delete 10 files, called=%d progress=%+v err=%v", called, progress, err)
		}
	})
}

func TestMirrorCleansExtraEmptyDirsDeepestFirstAndPreservesNonEmpty(t *testing.T) {
	p, d := mirrorFixture(t)
	if err := os.Remove(filepath.Join(p.Target, "extra.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p.Target, "empty", "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p.Target, "late", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	seededLate := false
	progress, err := RunMirrorWith(context.Background(), p, d, func(tp TransferProgress) {
		if tp.Stage == "清理多余空目录" && !seededLate {
			seededLate = true
			manualWrite(t, filepath.Join(p.Target, "late", "sub", "keep-new.txt"), "new")
		}
	}, nil, RunManualFiltered, noRecycle)
	if err != nil {
		t.Fatal(err)
	}
	if progress.RemovedDirs != 3 {
		t.Fatalf("expected 3 empty dirs removed, got %+v", progress)
	}
	if _, e := os.Stat(filepath.Join(p.Target, "empty")); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("expected empty dir tree to be removed, stat err=%v", e)
	}
	manualRead(t, filepath.Join(p.Target, "late", "sub", "keep-new.txt"), "new")
}

func TestMirrorDefersChangedSourceAndExtraWithoutAborting(t *testing.T) {
	q, p := multiFixture(t)
	q.Destinations = q.Destinations[:1]
	d := &q.Destinations[0]
	d.Strategy = taskconfig.Mirror
	d.DeleteMode = taskconfig.Permanent
	d.MassDeleteThreshold = 0.1
	d.MassDeleteThresholdSet = true

	manualWrite(t, filepath.Join(p.Source, "defer.txt"), "initial")
	manualWrite(t, filepath.Join(p.Source, "ok.txt"), "ok")
	manualWrite(t, filepath.Join(p.Target, "defer.txt"), "old-target")
	manualWrite(t, filepath.Join(p.Target, "extra1.txt"), "e1")
	manualWrite(t, filepath.Join(p.Target, "extra2.txt"), "e2")
	seedSharedGuardFiles(t, p.Source, p.Target, 8)

	changedSource := false
	results, err := RunMultipleConfirmed(context.Background(), q, func(r DestinationResult) {
		if r.Progress.Stage == "同步中" && r.Progress.Path == "defer.txt" && !changedSource {
			changedSource = true
			manualWrite(t, filepath.Join(p.Source, "defer.txt"), "changed-mid-run")
		}
	}, func(_ context.Context, v MirrorPreview) error {
		manualWrite(t, filepath.Join(p.Target, "extra1.txt"), "changed-in-target")
		return nil
	}, noRecycle)
	if err != nil {
		t.Fatalf("expected deferred-only run to complete without error, got %v", err)
	}
	res := results[d.ID]
	if res.State != DestinationIncomplete {
		t.Fatalf("expected DestinationIncomplete, got %q", res.State)
	}
	if res.Progress.Copied != 1 || res.Progress.Removed != 1 || res.Progress.Deferred != 2 {
		t.Fatalf("unexpected counts: %+v", res.Progress)
	}
	manualRead(t, filepath.Join(p.Target, "ok.txt"), "ok")
	manualRead(t, filepath.Join(p.Target, "defer.txt"), "old-target")
	manualRead(t, filepath.Join(p.Target, "extra1.txt"), "changed-in-target")
	if _, e := os.Stat(filepath.Join(p.Target, "extra2.txt")); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("expected extra2.txt to be removed, err=%v", e)
	}
}

func TestMirrorConsecutiveFailureFuse(t *testing.T) {
	p, d := mirrorFixture(t)
	d.DeleteMode = taskconfig.Recycle
	d.MassDeleteThreshold = 0
	d.MassDeleteThresholdSet = true
	if err := os.Remove(filepath.Join(p.Target, "extra.txt")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		manualWrite(t, filepath.Join(p.Target, fmt.Sprintf("fail-%02d.txt", i)), "x")
	}

	sentinel := errors.New("recycle refused")
	attempts := 0
	progress, err := RunMirrorWith(context.Background(), p, d, nil, acceptMirror, RunManualFiltered, func(string) error {
		attempts++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel failure, got %v", err)
	}
	if attempts != MaxConsecutiveFailures || progress.Failed != MaxConsecutiveFailures {
		t.Fatalf("expected fuse at %d failures, got attempts=%d failed=%d", MaxConsecutiveFailures, attempts, progress.Failed)
	}
}
