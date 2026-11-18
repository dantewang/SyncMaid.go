package appcore

import (
	"context"
	"slices"
	"syncmaid/internal/syncengine"
	"time"

	"golang.org/x/sys/windows"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/windowsfs"
	"syncmaid/internal/winutil"
)

func ServeSourceWatch(ctx context.Context, path string, status func(error), changed func(), native func(context.Context, string, func(error), func())) {
	network := windowsfs.Network(path)
	if !network {
		if root, err := winutil.OpenDirectoryAccess(path, windows.FILE_READ_ATTRIBUTES); err == nil {
			network = root.Network
			_ = root.Close()
		}
	}
	if network {
		ServePollingWatch(ctx, path, status, changed)
		return
	}
	if native != nil {
		native(ctx, path, status, changed)
	}
}

func ServePollingWatch(ctx context.Context, path string, status func(error), changed func()) {
	var files []fileinventory.File
	var dirs []fileinventory.Directory
	ready := false
	for ctx.Err() == nil {
		delay := 5 * time.Second
		var inventory *fileinventory.Inventory
		err := syncengine.InterruptibleIO(ctx, func(ctx context.Context) error {
			var err error
			inventory, err = fileinventory.Scan(ctx, path, fileinventory.Options{MaxEntries: 200000, MaxDepth: 128})
			return err
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			ready = false
			status(err)
			delay = 30 * time.Second
		} else {
			nextFiles, nextDirs := inventory.Files(), inventory.Directories()
			if !ready {
				status(nil)
			} else if !slices.Equal(files, nextFiles) || !slices.Equal(dirs, nextDirs) {
				changed()
			}
			ready, files, dirs = true, nextFiles, nextDirs
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
