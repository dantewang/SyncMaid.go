package main

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

var cancelSyncIO = windows.NewLazySystemDLL("kernel32.dll").NewProc("CancelSynchronousIo")

type transferIOKey struct{}
type transferIOGate struct {
	mu      sync.Mutex
	enabled bool
}

// Exclude new CancelSynchronousIo calls from cleanup and the publish/result
// interval. The same mutex covers the cancellation syscall, so a call already
// issuing finishes before masking. An OS cancellation request already issued
// cannot be withdrawn; this is not a hard guarantee about driver completion.
func protectedTransferIO(ctx context.Context, action func() error) error {
	g, ok := ctx.Value(transferIOKey{}).(*transferIOGate)
	if !ok {
		return action()
	}
	g.mu.Lock()
	g.enabled = false
	g.mu.Unlock()
	defer func() { g.mu.Lock(); g.enabled = ctx.Err() == nil; g.mu.Unlock() }()
	return action()
}

// Keep synchronous file operations on a known thread so stopping also asks
// Windows to interrupt outstanding I/O. Retries cover cancellation racing the
// next syscall; they are bounded and stop as soon as the worker releases files.
// A broken driver can still delay OS completion; never quit before cleanup.
func interruptibleIO(ctx context.Context, work func(context.Context) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var thread windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.CurrentThread(), windows.CurrentProcess(), &thread, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return err
	}
	defer windows.CloseHandle(thread)
	g := &transferIOGate{enabled: true}
	ctx = context.WithValue(ctx, transferIOKey{}, g)
	done := make(chan struct{})
	cancelDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(cancelDone)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for {
			g.mu.Lock()
			if g.enabled {
				cancelSyncIO.Call(uintptr(thread))
			}
			g.mu.Unlock()
			select {
			case <-done:
				return
			case <-deadline.C:
				return
			case <-ticker.C:
			}
		}
	})
	err := work(ctx)
	close(done)
	if !stop() {
		<-cancelDone
	}
	return errors.Join(err, ctx.Err())
}
