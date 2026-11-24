//go:build windows

package syncengine

import (
	"cmp"
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/windows"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/taskconfig"
	"syncmaid/internal/winutil"
)

type localWindowsBackend struct {
	roots   *TransferRoots
	recycle func(string) error
	stamps  map[string]MirrorStamp
}

func newLocalWindowsBackend(roots *TransferRoots, recycle func(string) error) *localWindowsBackend {
	return &localWindowsBackend{
		roots:   roots,
		recycle: recycle,
	}
}

func (b *localWindowsBackend) Capabilities() TargetCapabilities {
	return TargetCapabilities{
		SupportsRecycle:     !(b.roots.target.Network || b.roots.target.Removable || b.roots.target.Fat),
		SupportsDirectories: true,
		CaseSensitivePaths:  false,
		TargetLabel:         winutil.PathLabel(b.roots.target.Path),
	}
}

func (b *localWindowsBackend) ListTree(ctx context.Context) (*fileinventory.Inventory, error) {
	return fileinventory.Scan(ctx, b.roots.target.Path, fileinventory.Options{
		MaxEntries: 200000,
		MaxDepth:   128,
		OnProgress: scanProgressFromContext(ctx),
	})
}

func (b *localWindowsBackend) CheckDirectoryConflicts(ctx context.Context, sourceDirs []fileinventory.Directory, targetBefore *fileinventory.Inventory) error {
	return checkMirrorDirectoryConflicts(ctx, b.roots.target, sourceDirs, targetBefore)
}

func (b *localWindowsBackend) CollectExtraFiles(ctx context.Context, targetBefore, sourceInv *fileinventory.Inventory) ([]string, error) {
	paths, stamps, err := collectMirrorCandidates(ctx, b.roots, targetBefore.Files(), sourceInv)
	if err != nil {
		return nil, err
	}
	b.stamps = stamps
	return paths, nil
}

func mirrorSourceDirExists(sourceRoot *winutil.DirectoryObject, rel string) (bool, error) {
	_, closeAll, e := openMirrorPath(sourceRoot, rel, true, true, windows.FILE_GENERIC_READ)
	if e == nil {
		return true, closeAll()
	}
	if errors.Is(e, windows.ERROR_FILE_NOT_FOUND) ||
		errors.Is(e, windows.ERROR_PATH_NOT_FOUND) ||
		errors.Is(e, windows.ERROR_DIRECTORY) ||
		errors.Is(e, errShortNameConflict) {
		return false, nil
	}
	return false, e
}

func (b *localWindowsBackend) CollectExtraDirs(ctx context.Context, targetBefore, sourceInv *fileinventory.Inventory) ([]string, error) {
	var sourceSet map[string]bool
	if sourceInv != nil {
		list := sourceInv.Directories()
		sourceSet = make(map[string]bool, len(list))
		for _, sd := range list {
			sourceSet[sd.Path] = true
		}
	}
	var extra []string
	for _, entry := range targetBefore.Directories() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if sourceSet != nil && sourceSet[entry.Path] {
			continue
		}
		exists, err := mirrorSourceDirExists(b.roots.source, entry.Path)
		if err != nil {
			return nil, err
		}
		if exists {
			continue
		}
		extra = append(extra, entry.Path)
	}
	// Deepest first so empty child directories are removed before their parents.
	slices.SortFunc(extra, func(a, b string) int {
		da := strings.Count(a, "/")
		db := strings.Count(b, "/")
		return cmp.Or(cmp.Compare(db, da), cmp.Compare(b, a))
	})
	return extra, nil
}

func (b *localWindowsBackend) DeleteExtraFile(ctx context.Context, rel string, mode taskconfig.DeleteMode) error {
	return removeMirrorCandidate(ctx, b.roots, rel, b.stamps[rel], mode, b.recycle)
}

func (b *localWindowsBackend) DeleteExtraEmptyDir(ctx context.Context, rel string, mode taskconfig.DeleteMode) (bool, error) {
	exists, err := mirrorSourceDirExists(b.roots.source, rel)
	if err != nil {
		return false, err
	}
	if exists {
		return false, ErrMirrorChanged
	}
	f, closeAll, err := openMirrorPath(b.roots.target, rel, true, true, windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.DELETE)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return false, nil
		}
		if errors.Is(err, errShortNameConflict) {
			return false, ErrMirrorChanged
		}
		return false, err
	}
	names, readErr := f.Readdirnames(1)
	if len(names) > 0 {
		return false, closeAll()
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return false, errors.Join(readErr, closeAll())
	}
	var removed bool
	opErr := ProtectedTransferIO(ctx, func() error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if mode == taskconfig.Permanent {
			if e := discardTransfer(f); e != nil {
				if errors.Is(e, windows.ERROR_DIR_NOT_EMPTY) || errors.Is(e, windows.ERROR_FILE_NOT_FOUND) {
					return nil
				}
				return e
			}
			removed = true
			return nil
		}
		if b.recycle == nil {
			return errors.New("回收站接口不可用；未删除目录")
		}
		if e := f.Close(); e != nil {
			return e
		}
		f = nil
		if e := b.recycle(filepath.Join(b.roots.target.Path, filepath.FromSlash(rel))); e != nil {
			return e
		}
		removed = true
		return nil
	})
	opErr = errors.Join(opErr, closeAll())
	return removed, opErr
}

func (b *localWindowsBackend) RestoreDirectories(ctx context.Context, sourceDirs []fileinventory.Directory, targetBefore *fileinventory.Inventory, dirty map[string]bool) error {
	return restoreMirrorDirectories(ctx, b.roots.target, sourceDirs, targetBefore, dirty)
}
