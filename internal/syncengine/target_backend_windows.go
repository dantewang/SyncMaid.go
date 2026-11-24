//go:build windows

package syncengine

import (
	"context"

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
	return fileinventory.Scan(ctx, b.roots.target.Path, fileinventory.Options{MaxEntries: 200000, MaxDepth: 128})
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

func (b *localWindowsBackend) DeleteExtraFile(ctx context.Context, rel string, mode taskconfig.DeleteMode) error {
	return removeMirrorCandidate(ctx, b.roots, rel, b.stamps[rel], mode, b.recycle)
}

func (b *localWindowsBackend) RestoreDirectories(ctx context.Context, sourceDirs []fileinventory.Directory, targetBefore *fileinventory.Inventory, dirty map[string]bool) error {
	return restoreMirrorDirectories(ctx, b.roots.target, sourceDirs, targetBefore, dirty)
}
