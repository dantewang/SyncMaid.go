package main

import (
	"context"
	"slices"
	"syncmaid/internal/fileinventory"
	"time"
)

func scanMoveFiles(ctx context.Context, root *directoryObject) ([]fileinventory.File, map[string]mirrorStamp, error) {
	listing, e := scanMirror(ctx, root)
	if e != nil {
		return nil, nil, e
	}
	names := make([]string, 0, len(listing.Files))
	for name := range listing.Files {
		names = append(names, name)
	}
	slices.Sort(names)
	files := make([]fileinventory.File, 0, len(names))
	for _, name := range names {
		stamp := listing.Files[name]
		files = append(files, fileinventory.File{Path: name, Size: stamp.Size, Modified: time.Unix(0, stamp.Modified.Nanoseconds())})
	}
	return files, listing.Files, nil
}
func moveFile(ctx context.Context, roots *transferRoots, f fileinventory.File, target string, expected mirrorStamp, progress func(int64)) (bool, error) {
	return transferFileTo(ctx, roots, f, target, &expected, transferIO{copy: copyChecked, publish: publishTransfer}, progress)
}
