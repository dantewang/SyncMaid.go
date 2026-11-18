package syncengine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"syncmaid/internal/taskconfig"
)

func moveFixture(t *testing.T) (ManualTaskRequest, ManualRequest) {
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
func TestMoveRoutingAndFlattenCollisions(t *testing.T) {
	q, p := moveFixture(t)
	for name, value := range map[string]string{"a/f.txt": "first", "b/f.txt": "second", "loose.bin": "bin", "unmatched.dat": "stay"} {
		manualWrite(t, filepath.Join(p.Source, filepath.FromSlash(name)), value)
	}
	q.Destinations[0].Flatten = true
	q.Destinations[0].Filters = []taskconfig.Rule{taskconfig.ExtensionFilter("txt")}
	q.Destinations[1].Filters = []taskconfig.Rule{taskconfig.ExtensionFilter("txt"), taskconfig.ExtensionFilter("bin")}
	r, e := runMultiple(context.Background(), q, nil)
	if e != nil {
		t.Fatal(r, e)
	}
	first, second := r[q.Destinations[0].ID], r[q.Destinations[1].ID]
	if first.Progress.Moved != 1 || first.Progress.Retained != 1 || second.Progress.Moved != 1 || second.Progress.Unmatched != 1 {
		t.Fatal(r)
	}
	manualRead(t, filepath.Join(p.Target, "f.txt"), "first")
	absentMoveFile(t, filepath.Join(p.Source, "a/f.txt"))
	manualRead(t, filepath.Join(p.Source, "b/f.txt"), "second")
	absentMoveFile(t, filepath.Join(q.Destinations[1].Path, "b/f.txt"))
	manualRead(t, filepath.Join(q.Destinations[1].Path, "loose.bin"), "bin")
	manualRead(t, filepath.Join(p.Source, "unmatched.dat"), "stay")
}
func TestMoveSkipNeverUsesAddOnlyStampAsVerification(t *testing.T) {
	q, p := moveFixture(t)
	q.Destinations = q.Destinations[:1]
	manualWrite(t, filepath.Join(p.Source, "f.txt"), "AAAA")
	manualWrite(t, filepath.Join(p.Target, "f.txt"), "BBBB")
	stamp := time.Unix(1700000000, 0)
	for _, root := range []string{p.Source, p.Target} {
		if e := os.Chtimes(filepath.Join(root, "f.txt"), stamp, stamp); e != nil {
			t.Fatal(e)
		}
	}
	r, e := runMultiple(context.Background(), q, nil)
	if e != nil || r[q.Destinations[0].ID].Progress.Retained != 1 {
		t.Fatal(r, e)
	}
	manualRead(t, filepath.Join(p.Source, "f.txt"), "AAAA")
	manualRead(t, filepath.Join(p.Target, "f.txt"), "BBBB")
}

func TestMoveStopPreservesRemainingFiles(t *testing.T) {
	q, p := moveFixture(t)
	manualWrite(t, filepath.Join(p.Source, "a.txt"), "first")
	manualWrite(t, filepath.Join(p.Source, "z.txt"), "remaining")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, e := runMultiple(ctx, q, func(r DestinationResult) {
		if r.Progress.Moved == 1 {
			cancel()
		}
	})
	if !errors.Is(e, context.Canceled) || r[q.Destinations[0].ID].Progress.Moved != 1 {
		t.Fatal(r, e)
	}
	absentMoveFile(t, filepath.Join(p.Source, "a.txt"))
	manualRead(t, filepath.Join(p.Source, "z.txt"), "remaining")
}

func TestMoveFailedOwnerDoesNotStopHealthyRoute(t *testing.T) {
	q, p := moveFixture(t)
	manualWrite(t, filepath.Join(p.Source, "a.txt"), "keep")
	manualWrite(t, filepath.Join(p.Source, "b.bin"), "move")
	q.Destinations[0].Path = filepath.Join(t.TempDir(), "missing")
	q.Destinations[0].Filters = []taskconfig.Rule{taskconfig.ExtensionFilter("txt")}
	r, e := runMultiple(context.Background(), q, nil)
	if e == nil || r[q.Destinations[1].ID].Progress.Moved != 1 {
		t.Fatal(r, e)
	}
	manualRead(t, filepath.Join(p.Source, "a.txt"), "keep")
	absentMoveFile(t, filepath.Join(p.Source, "b.bin"))
	manualRead(t, filepath.Join(q.Destinations[1].Path, "b.bin"), "move")
}
func TestMovePreparationErrorsBlockCatchall(t *testing.T) {
	for _, kind := range []string{"regex", "unknown", "source-link"} {
		t.Run(kind, func(t *testing.T) {
			q, p := moveFixture(t)
			manualWrite(t, filepath.Join(p.Source, "f.txt"), "keep")
			switch kind {
			case "regex":
				q.Destinations[1].Filters = []taskconfig.Rule{{Kind: "regex", Pattern: filterText(".*")}}
			case "unknown":
				q.Destinations[1].Filters = []taskconfig.Rule{{Kind: "future"}}
			case "source-link":
				manualJunction(t, filepath.Join(p.Source, "link"), t.TempDir())
			}
			r, e := runMultiple(context.Background(), q, nil)
			if e == nil {
				t.Fatal(r)
			}
			manualRead(t, filepath.Join(p.Source, "f.txt"), "keep")
			for _, d := range q.Destinations {
				entries, e := os.ReadDir(d.Path)
				if e != nil || len(entries) != 0 {
					t.Fatal(entries, e)
				}
			}
		})
	}
	q, p := moveFixture(t)
	manualWrite(t, filepath.Join(p.Source, "f.txt"), "keep")
	q.Destinations[0].Path = filepath.Join(t.TempDir(), "missing")
	r, e := runMultiple(context.Background(), q, nil)
	if e == nil || r[q.Destinations[1].ID].Progress.Moved != 0 {
		t.Fatal(r, e)
	}
	manualRead(t, filepath.Join(p.Source, "f.txt"), "keep")
	absentMoveFile(t, filepath.Join(q.Destinations[1].Path, "f.txt"))
}
func TestMoveTransferFailuresKeepSource(t *testing.T) {
	for _, kind := range []string{"copy", "publish", "cancel", "source-delete", "source-replaced"} {
		t.Run(kind, func(t *testing.T) {
			_, p := moveFixture(t)
			manualWrite(t, filepath.Join(p.Source, "f.txt"), "original")
			roots, e := OpenTransferRoots(context.Background(), p)
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
			ops := TransferIO{Copy: CopyChecked, Publish: PublishTransfer}
			switch kind {
			case "copy":
				ops.Copy = func(_ context.Context, _, temp *os.File, _ func(int64)) error {
					_, _ = temp.Write([]byte("partial"))
					return errors.New("injected copy failure")
				}
			case "publish":
				ops.Publish = func(*os.File, windows.Handle, string, bool) error { return errors.New("injected publication failure") }
			case "cancel":
				ops.Publish = func(f *os.File, h windows.Handle, name string, replace bool) error {
					e := PublishTransfer(f, h, name, replace)
					cancel()
					return e
				}
			case "source-delete":
				ops.SourceDelete = func(*os.File) error { return windows.ERROR_ACCESS_DENIED }
			case "source-replaced":
				if e := os.Remove(filepath.Join(p.Source, "f.txt")); e != nil {
					t.Fatal(e)
				}
				manualWrite(t, filepath.Join(p.Source, "f.txt"), "replaced")
			}
			stamp := stamps[files[0].Path]
			published, e := transferFileTo(ctx, roots, files[0], files[0].Path, &stamp, ops, nil)
			if e == nil {
				t.Fatal("failure reported success", kind)
			}
			want := "original"
			if kind == "source-replaced" {
				want = "replaced"
			}
			manualRead(t, filepath.Join(p.Source, "f.txt"), want)
			if kind == "cancel" || kind == "source-delete" {
				if !published {
					t.Fatal("published target not reported")
				}
				manualRead(t, filepath.Join(p.Target, "f.txt"), "original")
			}
			if kind == "copy" {
				entries, e := os.ReadDir(p.Target)
				if e != nil || len(entries) != 0 {
					t.Fatal(entries, e)
				}
			}
		})
	}
}
