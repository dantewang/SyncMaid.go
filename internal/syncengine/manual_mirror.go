package syncengine

import (
	"context"
	"errors"
	"fmt"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/taskconfig"
)

var ErrMirrorDeclined = errors.New("未确认本次删除；多余文件保持不变")
var ErrMirrorChanged = errors.New("目录或文件已变化，请重新运行并检查新预览")

type MirrorPreview struct {
	DestinationID taskconfig.ID
	Name, Target  string
	Mode          taskconfig.DeleteMode
	Paths         []string
}
type MirrorConfirmation func(context.Context, MirrorPreview) error

func DestinationStrategy(d ManualDestination) taskconfig.Strategy {
	if d.Strategy == "" {
		return taskconfig.AddOnly
	}
	return d.Strategy
}
func DestinationDeleteMode(d ManualDestination) taskconfig.DeleteMode {
	if d.DeleteMode == "" {
		return taskconfig.Recycle
	}
	return d.DeleteMode
}

func ValidateDestinationMode(d ManualDestination) error {
	if d.Collision != "" && d.Collision != taskconfig.Skip {
		return errors.New("Sync暂不支持此冲突策略")
	}
	switch DestinationStrategy(d) {
	case taskconfig.AddOnly:
	case taskconfig.Mirror:
		if !IsAllFileRules(d.Filters) {
			return errors.New("Mirror仅支持全部文件，不可使用过滤")
		}
	default:
		return errors.New("不支持的同步策略")
	}
	if mode := DestinationDeleteMode(d); mode != taskconfig.Recycle && mode != taskconfig.Permanent {
		return errors.New("不支持的删除方式")
	}
	return nil
}

type MirrorCopy func(context.Context, ManualRequest, []taskconfig.Rule, func(TransferProgress)) (TransferProgress, error)

func RunMirrorWith(ctx context.Context, q ManualRequest, d ManualDestination, notify func(TransferProgress), confirm MirrorConfirmation, copyFiles MirrorCopy, recycle func(string) error) (p TransferProgress, err error) {
	return RunMirrorSession(ctx, q, d, nil, nil, notify, confirm, copyFiles, recycle)
}

func RunMirrorSession(ctx context.Context, q ManualRequest, d ManualDestination, roots *TransferRoots, sourceInv *fileinventory.Inventory, notify func(TransferProgress), confirm MirrorConfirmation, copyFiles MirrorCopy, recycle func(string) error) (p TransferProgress, err error) {
	if err = ValidateDestinationMode(d); err != nil {
		return
	}
	if roots == nil {
		var openErr error
		roots, openErr = OpenTransferRoots(ctx, q)
		if openErr != nil {
			return p, openErr
		}
		defer func() { err = errors.Join(err, roots.close(ctx)) }()
	} else if e := roots.ensureWritableTarget(ctx); e != nil {
		return p, e
	}
	if sourceInv == nil {
		var scanErr error
		sourceInv, scanErr = fileinventory.Scan(ctx, roots.source.Path, fileinventory.Options{MaxEntries: 200000, MaxDepth: 128})
		if scanErr != nil {
			return p, scanErr
		}
	}
	if len(sourceInv.Files()) == 0 {
		return p, errors.New("源没有普通文件，Mirror禁止删除")
	}
	backend := newLocalWindowsBackend(roots, recycle)
	caps := backend.Capabilities()

	// Scan target once before copy: an incomplete/unsupported target blocks before
	// any copy, and its file list establishes pre-existing deletion candidates.
	targetBefore, e := backend.ListTree(ctx)
	if e != nil {
		return p, e
	}
	if e = backend.CheckDirectoryConflicts(ctx, sourceInv.Directories(), targetBefore); e != nil {
		return p, e
	}
	if copyFiles != nil {
		p, e = copyFiles(ctx, q, AllFileRules(), notify)
	} else {
		p, e = runManualWithSessionTarget(ctx, q, roots, sourceInv, targetBefore, notify, TransferIO{Copy: TransferCopier(d.VerifyContents), Publish: PublishTransfer}, AllFileRules())
	}
	if e != nil {
		return p, e
	}
	paths, e := backend.CollectExtraFiles(ctx, targetBefore, sourceInv)
	if e != nil {
		return p, e
	}
	preview := MirrorPreview{DestinationID: d.ID, Name: d.Name, Target: caps.TargetLabel, Mode: DestinationDeleteMode(d), Paths: paths}
	if len(preview.Paths) == 0 {
		dirty := dirtyMirrorDirs(p.Copied > 0, sourceInv, targetBefore, nil)
		return p, backend.RestoreDirectories(ctx, sourceInv.Directories(), targetBefore, dirty)
	}
	if preview.Mode == taskconfig.Recycle && !caps.SupportsRecycle {
		return p, errors.New("此目标不支持回收确认；多余文件未移除。若需要，请明确选择永久删除后重新运行。")
	}
	p.Stage = "等待删除确认"
	if notify != nil {
		notify(p)
	}
	if confirm == nil {
		return p, ErrMirrorDeclined
	}
	confirmation := preview
	confirmation.Paths = append([]string(nil), preview.Paths...)
	if e = confirm(ctx, confirmation); e != nil {
		return p, e
	}
	if e = ctx.Err(); e != nil {
		return p, e
	}
	hasRegular, e := fileinventory.HasRegularFile(ctx, roots.source.Path, fileinventory.Options{MaxEntries: 200000, MaxDepth: 128})
	if e != nil {
		return p, e
	}
	if !hasRegular {
		return p, errors.New("源没有普通文件，Mirror禁止删除")
	}
	for _, rel := range preview.Paths {
		if e = ctx.Err(); e != nil {
			return p, e
		}
		p.Stage = "移除多余文件"
		p.Path = rel
		if notify != nil {
			notify(p)
		}
		e = backend.DeleteExtraFile(ctx, rel, preview.Mode)
		if e != nil {
			return p, fmt.Errorf("%s: %w", rel, e)
		}
		p.Removed++
		if notify != nil {
			notify(p)
		}
	}
	dirty := dirtyMirrorDirs(p.Copied > 0, sourceInv, targetBefore, preview.Paths)
	return p, backend.RestoreDirectories(ctx, sourceInv.Directories(), targetBefore, dirty)
}
