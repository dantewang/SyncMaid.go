package main

import (
	"context"
	"errors"
	"github.com/egoist/mygo"
	"io"
	"time"
)

// Native notifications are hints only. Every run still scans and validates all
// actual objects. A failed enrollment retries slowly, never runs an empty list.
func serveNativeWatch(ctx context.Context, path string, status func(error), changed func()) {
	for ctx.Err() == nil {
		w, e := mygo.WatchFiles(ctx, path, mygo.FileWatchOptions{Recursive: true, HintsOnly: true, Debounce: 50 * time.Millisecond, MaxDelay: 500 * time.Millisecond, MaxEntries: 200000})
		if e == nil {
			status(nil)
			for ctx.Err() == nil {
				events, err := w.Next(ctx)
				if err != nil {
					e = err
					break
				}
				if len(events) > 0 {
					changed()
				}
			}
			_ = w.Close()
			<-w.Done()
		}
		if ctx.Err() != nil {
			return
		}
		if e == nil || errors.Is(e, io.EOF) {
			e = errors.New("源监视已结束或源目录不可用")
		}
		status(e)
		retry := time.NewTimer(30 * time.Second)
		select {
		case <-ctx.Done():
			retry.Stop()
			return
		case <-retry.C:
		}
	}
}
