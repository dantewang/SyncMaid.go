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
	"syncmaid/internal/taskconfig"
)

func TestManualAddOnlyUpdatesAndKeepsExtra(t *testing.T) {
	q := manualFixture(t)
	manualWrite(t, filepath.Join(q.Source, "中文", "a.txt"), "new content")
	manualWrite(t, filepath.Join(q.Target, "中文", "a.txt"), "old")
	manualWrite(t, filepath.Join(q.Target, "extra"), "keep")
	manualWrite(t, filepath.Join(q.Source, "same"), "unchanged")
	manualWrite(t, filepath.Join(q.Target, "same"), "unchanged")
	stamp := time.Unix(1700000000, 0)
	for _, p := range []string{filepath.Join(q.Source, "same"), filepath.Join(q.Target, "same")} {
		if e := os.Chtimes(p, stamp, stamp); e != nil {
			t.Fatal(e)
		}
	}
	p, e := runManual(context.Background(), q, nil)
	if e != nil || p.Copied != 1 || p.Unchanged != 1 || p.Total != 2 {
		t.Fatalf("%+v %v", p, e)
	}
	manualRead(t, filepath.Join(q.Target, "中文", "a.txt"), "new content")
	manualRead(t, filepath.Join(q.Target, "extra"), "keep")
	manualRead(t, filepath.Join(q.Source, "中文", "a.txt"), "new content")
	p, e = runManual(context.Background(), q, nil)
	if e != nil || p.Copied != 0 || p.Unchanged != 2 {
		t.Fatalf("rerun %+v %v", p, e)
	}
	noTransferTemps(t, q.Target)
}
func TestManualSameSizeDifferentTimeUpdates(t *testing.T) {
	q := manualFixture(t)
	s, d := filepath.Join(q.Source, "f"), filepath.Join(q.Target, "f")
	manualWrite(t, s, "new")
	manualWrite(t, d, "old")
	a, b := time.Unix(1700000000, 0), time.Unix(1700000002, 0)
	if e := os.Chtimes(s, a, a); e != nil {
		t.Fatal(e)
	}
	if e := os.Chtimes(d, b, b); e != nil {
		t.Fatal(e)
	}
	p, e := runManual(context.Background(), q, nil)
	if e != nil || p.Copied != 1 {
		t.Fatal(p, e)
	}
	manualRead(t, d, "new")
}

func TestManualMissingFile(t *testing.T) {
	q := manualFixture(t)
	manualWrite(t, filepath.Join(q.Source, "file.txt"), "new")
	p, e := runManual(context.Background(), q, nil)
	if e != nil || p.Copied != 1 {
		t.Fatal(p, e)
	}
	manualRead(t, filepath.Join(q.Target, "file.txt"), "new")
}
func TestManualFailureAndCancellationPreserveDestination(t *testing.T) {
	for _, failure := range []string{"partial-copy", "publish", "cancel-before-publish"} {
		t.Run(failure, func(t *testing.T) {
			q := manualFixture(t)
			manualWrite(t, filepath.Join(q.Source, "f"), strings.Repeat("new", 100000))
			manualWrite(t, filepath.Join(q.Target, "f"), "original")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sentinel := errors.New("injected transfer failure")
			ops := TransferIO{Copy: CopyChecked, Publish: PublishTransfer}
			switch failure {
			case "partial-copy":
				ops.Copy = func(ctx context.Context, s, d *os.File, p func(int64)) error {
					_, e := d.Write([]byte("partial"))
					return errors.Join(sentinel, e)
				}
			case "publish":
				ops.Publish = func(*os.File, windows.Handle, string, bool) error { return sentinel }
			case "cancel-before-publish":
				ops.Copy = func(ctx context.Context, s, d *os.File, p func(int64)) error {
					e := CopyChecked(ctx, s, d, p)
					cancel()
					return e
				}
				sentinel = context.Canceled
			}
			p, e := runManualWith(ctx, q, nil, ops)
			if !errors.Is(e, sentinel) || p.Copied != 0 {
				t.Fatalf("%+v %v", p, e)
			}
			manualRead(t, filepath.Join(q.Target, "f"), "original")
			if failure == "publish" {
				if !errors.Is(e, ErrTransferPublicationUnknown) {
					t.Fatal(e)
				}
				files, err := filepath.Glob(filepath.Join(q.Target, ".syncmaid-*.tmp"))
				if err != nil || len(files) != 1 {
					t.Fatal(files, err)
				}
				manualRead(t, files[0], strings.Repeat("new", 100000))
			} else {
				noTransferTemps(t, q.Target)
			}
		})
	}
}
func TestManualCancelMidCopyCleansTemp(t *testing.T) {
	q := manualFixture(t)
	manualWrite(t, filepath.Join(q.Source, "f"), strings.Repeat("x", 1<<20))
	manualWrite(t, filepath.Join(q.Target, "f"), "old")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, e := runManual(ctx, q, func(p TransferProgress) {
		if p.Bytes > 0 {
			cancel()
		}
	})
	if !errors.Is(e, context.Canceled) || p.Copied != 0 {
		t.Fatal(p, e)
	}
	manualRead(t, filepath.Join(q.Target, "f"), "old")
	noTransferTemps(t, q.Target)
}
func TestManualOverlapAndAliases(t *testing.T) {
	q := manualFixture(t)
	nested := filepath.Join(q.Source, "nested")
	if e := os.Mkdir(nested, 0700); e != nil {
		t.Fatal(e)
	}
	for _, r := range []ManualRequest{{q.Source, q.Source}, {q.Source, nested}, {nested, q.Source}, {"relative", q.Target}} {
		if _, e := validateManual(context.Background(), r); e == nil {
			t.Fatal("overlap accepted", r)
		}
	}
	alias := filepath.Join(filepath.Dir(q.Source), "alias")
	manualJunction(t, alias, q.Source)
	if _, e := validateManual(context.Background(), ManualRequest{q.Source, alias}); e == nil {
		t.Fatal("alias accepted")
	}
}

func TestOpenMultiplePlanDistinctUnicodeDirectories(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "\u10d0")
	dst := filepath.Join(root, "\u1c90")
	if err := os.Mkdir(src, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dst, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			t.Skipf("filesystem treats U+10D0 and U+1C90 as the same directory: %v", err)
		}
		t.Fatal(err)
	}
	if ObviousPathOverlap(src, dst) {
		t.Fatalf("ObviousPathOverlap falsely flagged distinct Unicode dirs %q and %q", src, dst)
	}
	plan, err := OpenMultiplePlan(context.Background(), ManualTaskRequest{
		Source: src,
		Destinations: []ManualDestination{
			{ID: "dst-1", Name: "Dst", Path: dst, Strategy: taskconfig.Mirror, DeleteMode: taskconfig.Permanent},
		},
	})
	if err != nil {
		t.Fatalf("OpenMultiplePlan rejected distinct Unicode dirs: %v", err)
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestManualScanFailureAndChangedSourceBlockWrites(t *testing.T) {
	q := manualFixture(t)
	manualWrite(t, filepath.Join(q.Source, "a"), "source")
	manualWrite(t, filepath.Join(q.Target, "a"), "old")
	// Source changes after enumeration, before any file open.
	changed := false
	p, e := runManual(context.Background(), q, func(p TransferProgress) {
		if p.Stage == "同步中" && !changed {
			changed = true
			manualWrite(t, filepath.Join(q.Source, "a"), "changed size")
		}
	})
	if e != nil || p.Copied != 0 || p.Deferred != 1 {
		t.Fatal(p, e)
	}
	manualRead(t, filepath.Join(q.Target, "a"), "old")
	outside := t.TempDir()
	manualWrite(t, filepath.Join(outside, "private"), "outside")
	manualJunction(t, filepath.Join(q.Source, "escape"), outside)
	p, e = runManual(context.Background(), q, nil)
	if e == nil || p.Copied != 0 {
		t.Fatal(p, e)
	}
	noTransferTemps(t, q.Target)
}
func TestManualReplacedSourceDirectoryAndTargetReparse(t *testing.T) {
	for _, side := range []string{"source", "target"} {
		t.Run(side, func(t *testing.T) {
			q := manualFixture(t)
			manualWrite(t, filepath.Join(q.Source, "sub", "f"), "new")
			outside := t.TempDir()
			manualWrite(t, filepath.Join(outside, "f"), "outside-original")
			changed := false
			_, e := runManual(context.Background(), q, func(p TransferProgress) {
				if p.Stage != "同步中" || changed {
					return
				}
				changed = true
				if side == "source" {
					if e := os.Rename(filepath.Join(q.Source, "sub"), filepath.Join(q.Source, "saved")); e != nil {
						t.Fatal(e)
					}
					manualJunction(t, filepath.Join(q.Source, "sub"), outside)
				} else {
					manualJunction(t, filepath.Join(q.Target, "sub"), outside)
				}
			})
			if e == nil {
				t.Fatal("reparse accepted")
			}
			manualRead(t, filepath.Join(outside, "f"), "outside-original")
			noTransferTemps(t, q.Target)
		})
	}
}
func TestManualDestinationFileSymlinkRejected(t *testing.T) {
	q := manualFixture(t)
	manualWrite(t, filepath.Join(q.Source, "f"), "new")
	outside := filepath.Join(t.TempDir(), "f")
	manualWrite(t, outside, "outside")
	if e := os.Symlink(outside, filepath.Join(q.Target, "f")); e != nil {
		t.Skipf("symlink permission: %v", e)
	}
	if _, e := runManual(context.Background(), q, nil); e == nil {
		t.Fatal("link accepted")
	}
	manualRead(t, outside, "outside")
}
func TestManualPinsOpenFilesAndDirectory(t *testing.T) {
	q := manualFixture(t)
	s, d := filepath.Join(q.Source, "sub", "f"), filepath.Join(q.Target, "sub", "f")
	manualWrite(t, s, "new-content")
	manualWrite(t, d, "old")
	ops := TransferIO{Publish: PublishTransfer, Copy: func(ctx context.Context, source, temp *os.File, p func(int64)) error {
		if e := os.WriteFile(s, []byte("bad"), 0600); e == nil {
			t.Fatal("open source was writable")
		}
		if e := os.WriteFile(d, []byte("bad"), 0600); e == nil {
			t.Fatal("open existing target was writable")
		}
		if e := os.Rename(filepath.Dir(s), filepath.Join(q.Source, "moved")); e == nil {
			t.Fatal("pinned source parent was renamed")
		}
		return CopyChecked(ctx, source, temp, p)
	}}
	if _, e := runManualWith(context.Background(), q, nil, ops); e != nil {
		t.Fatal(e)
	}
	manualRead(t, d, "new-content")
}

func TestManualHardLinkRefused(t *testing.T) {
	q := manualFixture(t)
	s := filepath.Join(q.Source, "f")
	manualWrite(t, s, "input")
	if e := os.Link(s, filepath.Join(q.Source, "alias")); e != nil {
		t.Skip(e)
	}
	if _, e := runManual(context.Background(), q, nil); e == nil {
		t.Fatal("hard link accepted")
	}
	noTransferTemps(t, q.Target)
}

func TestManualInterruptibleCancellationCleansTemp(t *testing.T) {
	q := manualFixture(t)
	manualWrite(t, filepath.Join(q.Source, "f"), strings.Repeat("x", 1<<20))
	manualWrite(t, filepath.Join(q.Target, "f"), "old")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := InterruptibleIO(ctx, func(ctx context.Context) error {
		_, e := runManual(ctx, q, func(p TransferProgress) {
			if p.Bytes > 0 {
				cancel()
			}
		})
		return e
	})
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	noTransferTemps(t, q.Target)
	manualRead(t, filepath.Join(q.Target, "f"), "old")
}
func TestManualCleanupDisablesIOInterruption(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	g := &transferIOGate{enabled: true}
	ctx = context.WithValue(ctx, transferIOKey{}, g)
	cancel()
	sentinel := errors.New("cleanup fault")
	e := ProtectedTransferIO(ctx, func() error {
		if g.enabled {
			t.Fatal("cleanup interruption enabled")
		}
		return sentinel
	})
	if !errors.Is(e, sentinel) || g.enabled {
		t.Fatal(e, g.enabled)
	}
}

func TestCheckOrdinaryDirectoryPolicy(t *testing.T) {
	if err := checkOrdinaryDirectory(false, nil); err != nil {
		t.Fatalf("expected case-insensitive dir to pass, got %v", err)
	}
	if err := checkOrdinaryDirectory(true, nil); err == nil {
		t.Fatal("expected case-sensitive dir to be rejected")
	}
	unsupported := fmt.Errorf("%w: %w", errors.ErrUnsupported, windows.ERROR_NOT_SUPPORTED)
	if err := checkOrdinaryDirectory(false, unsupported); err != nil {
		t.Fatalf("expected ErrUnsupported to be tolerated like fileinventory.walk, got %v", err)
	}
	if err := checkOrdinaryDirectory(false, windows.ERROR_ACCESS_DENIED); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("expected access denied to propagate, got %v", err)
	}
}

func TestProgressTracksBytesSubStageAndMirrorPlan(t *testing.T) {
	q := manualFixture(t)
	manualWrite(t, filepath.Join(q.Source, "copy.bin"), strings.Repeat("a", 128))
	manualWrite(t, filepath.Join(q.Source, "same.bin"), strings.Repeat("b", 64))
	manualWrite(t, filepath.Join(q.Target, "same.bin"), strings.Repeat("b", 64))
	stamp := time.Unix(1700000000, 0)
	for _, p := range []string{filepath.Join(q.Source, "same.bin"), filepath.Join(q.Target, "same.bin")} {
		if e := os.Chtimes(p, stamp, stamp); e != nil {
			t.Fatal(e)
		}
	}

	var sawVerify bool
	var prevProcessed int64
	p, err := RunManualWithFilters(context.Background(), q, func(tp TransferProgress) {
		if tp.SubStage == "校验中" {
			sawVerify = true
		}
		if tp.ProcessedBytes < prevProcessed {
			t.Fatalf("ProcessedBytes decreased: %d -> %d", prevProcessed, tp.ProcessedBytes)
		}
		prevProcessed = tp.ProcessedBytes
	}, TransferIO{Copy: CopyChecked, Publish: PublishTransfer}, AllFileRules())
	if err != nil {
		t.Fatal(err)
	}
	if !sawVerify {
		t.Fatal("expected verify substage callback during CopyChecked")
	}
	if p.TotalBytes != 192 || p.ProcessedBytes != 192 || p.Bytes != 128 || p.Copied != 1 || p.Unchanged != 1 {
		t.Fatalf("unexpected Add-only progress: %+v", p)
	}

	// Mirror mode should compute exact planned copy and removal counts.
	manualWrite(t, filepath.Join(q.Target, "extra1.txt"), "x")
	manualWrite(t, filepath.Join(q.Target, "extra2.txt"), "y")
	manualWrite(t, filepath.Join(q.Source, "copy2.bin"), strings.Repeat("c", 50))
	d := ManualDestination{
		ID:             taskconfig.NewID(),
		Name:           "Mirror",
		Path:           q.Target,
		Strategy:       taskconfig.Mirror,
		DeleteMode:     taskconfig.Permanent,
		Filters:        AllFileRules(),
		VerifyContents: false,
	}
	mp, err := RunMirrorSession(context.Background(), q, d, nil, nil, nil, func(context.Context, MirrorPreview) error { return nil }, nil, noRecycle)
	if err != nil {
		t.Fatal(err)
	}
	if mp.PlannedCopyFiles != 1 || mp.PlannedCopyBytes != 50 || mp.PlannedRemoveFiles != 2 || mp.Removed != 2 {
		t.Fatalf("unexpected Mirror plan progress: %+v", mp)
	}
}
