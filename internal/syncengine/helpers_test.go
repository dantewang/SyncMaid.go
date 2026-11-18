package syncengine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"syncmaid/internal/taskconfig"
)

func manualFixture(t *testing.T) ManualRequest {
	t.Helper()
	base := t.TempDir()
	s, d := filepath.Join(base, "source"), filepath.Join(base, "target")
	for _, p := range []string{s, d} {
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	return ManualRequest{s, d}
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

func multiFixture(t *testing.T) (ManualTaskRequest, ManualRequest) {
	t.Helper()
	first := manualFixture(t)
	second := filepath.Join(filepath.Dir(first.Target), "second-target")
	if e := os.Mkdir(second, 0700); e != nil {
		t.Fatal(e)
	}
	q := ManualTaskRequest{
		Source: first.Source,
		Destinations: []ManualDestination{
			{ID: taskconfig.NewID(), Name: "First", Path: first.Target, Filters: AllFileRules()},
			{ID: taskconfig.NewID(), Name: "Second", Path: second, Filters: AllFileRules()},
		},
	}
	return q, first
}

func runManual(ctx context.Context, q ManualRequest, notify func(TransferProgress)) (TransferProgress, error) {
	return runManualWith(ctx, q, notify, TransferIO{Copy: CopyChecked, Publish: PublishTransfer})
}

func runManualWith(ctx context.Context, q ManualRequest, notify func(TransferProgress), ops TransferIO) (TransferProgress, error) {
	return RunManualWithFilters(ctx, q, notify, ops, []taskconfig.Rule{{Kind: "all"}})
}

func runMultiple(ctx context.Context, q ManualTaskRequest, notify func(DestinationResult)) (MultipleResult, error) {
	return RunMultipleConfirmed(ctx, q, notify, nil, nil)
}

func validateManual(ctx context.Context, q ManualRequest) (result ManualRequest, err error) {
	r, err := OpenTransferRoots(ctx, q)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, r.CloseProtected(ctx)) }()
	s, d := r.Paths()
	return ManualRequest{Source: s, Target: d}, nil
}

func filterText(s string) *string { return &s }
