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

type stampProgressKey struct{}

func withMoveStampProgress(ctx context.Context, fn func(done, total int, rel string)) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, stampProgressKey{}, fn)
}

func moveStampProgressFromContext(ctx context.Context) func(done, total int, rel string) {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(stampProgressKey{}).(func(done, total int, rel string))
	return fn
}

func scanMoveFiles(ctx context.Context, root *winutil.DirectoryObject) ([]fileinventory.File, map[string]MirrorStamp, error) {
	inventory, err := fileinventory.Scan(ctx, root.Path, fileinventory.Options{
		MaxEntries: 200000,
		MaxDepth:   128,
		OnProgress: scanProgressFromContext(ctx),
	})
	if err != nil {
		return nil, nil, err
	}
	rawFiles := inventory.Files()
	onStamp := moveStampProgressFromContext(ctx)
	stamps := map[string]MirrorStamp{}
	for idx, entry := range rawFiles {
		if err = ctx.Err(); err != nil {
			return nil, nil, err
		}
		if onStamp != nil && (idx == 0 || idx%64 == 0 || idx+1 == len(rawFiles)) {
			onStamp(idx+1, len(rawFiles), entry.Path)
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
