package syncengine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// This deliberately returns a fabricated error AFTER a real successful rename.
// It tests the hypothetical failure boundary, not a reproduced Windows defect.
func TestManualPublishAmbiguousErrorDoesNotDiscardTarget(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "replace"}[existing], func(t *testing.T) {
			q := manualFixture(t)
			manualWrite(t, filepath.Join(q.Source, "f"), "complete-new-content")
			if existing {
				manualWrite(t, filepath.Join(q.Target, "f"), "old")
			}
			fault := errors.New("injected error after successful rename")
			ops := TransferIO{Copy: CopyChecked, Publish: func(f *os.File, parent windows.Handle, name string, replace bool) error {
				if e := PublishTransfer(f, parent, name, replace); e != nil {
					return e
				}
				return fault
			}}
			p, e := runManualWith(context.Background(), q, nil, ops)
			if !errors.Is(e, fault) || !errors.Is(e, ErrTransferPublicationUnknown) || p.Copied != 0 {
				t.Fatal(p, e)
			}
			manualRead(t, filepath.Join(q.Target, "f"), "complete-new-content")
			noTransferTemps(t, q.Target)
		})
	}
}

func TestManualPublishMasksCancellationAndKeepsCommittedFile(t *testing.T) {
	q := manualFixture(t)
	manualWrite(t, filepath.Join(q.Source, "f"), "new-content")
	manualWrite(t, filepath.Join(q.Target, "f"), "old")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var result TransferProgress
	err := InterruptibleIO(ctx, func(ctx context.Context) error {
		g := ctx.Value(transferIOKey{}).(*transferIOGate)
		ops := TransferIO{Copy: CopyChecked, Publish: func(f *os.File, parent windows.Handle, name string, replace bool) error {
			g.mu.Lock()
			enabled := g.enabled
			g.mu.Unlock()
			if enabled {
				t.Fatal("publish permits new synchronous cancellation calls")
			}
			cancel() // deterministic cancellation at the publication boundary
			return PublishTransfer(f, parent, name, replace)
		}}
		var e error
		result, e = runManualWith(ctx, q, nil, ops)
		return e
	})
	if !errors.Is(err, context.Canceled) || result.Copied != 1 {
		t.Fatal(result, err)
	}
	manualRead(t, filepath.Join(q.Target, "f"), "new-content")
	noTransferTemps(t, q.Target)
}
