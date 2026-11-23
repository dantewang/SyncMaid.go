package syncengine

import (
	"context"
	"errors"
	"slices"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/taskconfig"
	"syncmaid/internal/winutil"
	"time"

	"golang.org/x/sys/windows"
)

func scanMoveFiles(ctx context.Context, root *winutil.DirectoryObject) ([]fileinventory.File, map[string]MirrorStamp, error) {
	inventory, err := fileinventory.Scan(ctx, root.Path, fileinventory.Options{MaxEntries: 200000, MaxDepth: 128})
	if err != nil {
		return nil, nil, err
	}
	stamps := map[string]MirrorStamp{}
	for _, entry := range inventory.Files() {
		if err = ctx.Err(); err != nil {
			return nil, nil, err
		}
		f, close, e := openMirrorPath(root, entry.Path, false, false, windows.GENERIC_READ)
		if e != nil {
			return nil, nil, e
		}
		stamp, e := mirrorFileStamp(f, root.Fat, root.Network)
		e = errors.Join(e, close())
		if e != nil {
			return nil, nil, e
		}
		if stamp.Size != entry.Size || stamp.Modified.Nanoseconds() != entry.Modified.UnixNano() {
			return nil, nil, ErrMirrorChanged
		}
		stamps[entry.Path] = stamp
	}
	names := make([]string, 0, len(stamps))
	for name := range stamps {
		names = append(names, name)
	}
	slices.Sort(names)
	files := make([]fileinventory.File, 0, len(names))
	for _, name := range names {
		stamp := stamps[name]
		files = append(files, fileinventory.File{Path: name, Size: stamp.Size, Modified: time.Unix(0, stamp.Modified.Nanoseconds())})
	}
	return files, stamps, nil
}
func moveFile(ctx context.Context, roots *TransferRoots, f fileinventory.File, target string, expected MirrorStamp, collision taskconfig.CollisionPolicy, verifyContents bool, progress func(int64)) (bool, error) {
	return transferFileTo(ctx, roots, f, target, &expected, TransferIO{Copy: TransferCopier(verifyContents), Publish: PublishTransfer, Collision: collision}, progress)
}
