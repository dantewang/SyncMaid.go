package syncengine

import (
	"context"

	"syncmaid/internal/fileinventory"
	"syncmaid/internal/taskconfig"
)

// TargetCapabilities describes the runtime capabilities and path matching
// semantics of a synchronization target backend.
type TargetCapabilities struct {
	SupportsRecycle     bool
	SupportsDirectories bool
	CaseSensitivePaths  bool
	TargetLabel         string
}

// TargetBackend abstracts destination inspection, diffing helpers, and
// mutation primitives so planning/orchestration can support future remote
// backends (e.g. S3) without sacrificing Windows local safety guarantees.
type TargetBackend interface {
	Capabilities() TargetCapabilities
	ListTree(ctx context.Context) (*fileinventory.Inventory, error)
	CheckDirectoryConflicts(ctx context.Context, sourceDirs []fileinventory.Directory, targetBefore *fileinventory.Inventory) error
	CollectExtraFiles(ctx context.Context, targetBefore, sourceInv *fileinventory.Inventory) ([]string, error)
	CollectExtraDirs(ctx context.Context, targetBefore, sourceInv *fileinventory.Inventory) ([]string, error)
	DeleteExtraFile(ctx context.Context, rel string, mode taskconfig.DeleteMode) error
	DeleteExtraEmptyDir(ctx context.Context, rel string, mode taskconfig.DeleteMode) (removed bool, err error)
	RestoreDirectories(ctx context.Context, sourceDirs []fileinventory.Directory, targetBefore *fileinventory.Inventory, dirty map[string]bool) error
}
