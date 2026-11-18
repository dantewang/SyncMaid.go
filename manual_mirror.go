package main

import (
	"context"
	"errors"
	"fmt"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/taskconfig"
)

var errMirrorDeclined = errors.New("未确认本次删除；多余文件保持不变")
var errMirrorChanged = errors.New("目录或文件已变化，请重新运行并检查新预览")

type mirrorPreview struct {
	DestinationID taskconfig.ID
	Name, Target  string
	Mode          taskconfig.DeleteMode
	Paths         []string
}
type mirrorConfirmation func(context.Context, mirrorPreview) error

func destinationStrategy(d manualDestination) taskconfig.Strategy {
	if d.Strategy == "" {
		return taskconfig.AddOnly
	}
	return d.Strategy
}
func destinationDeleteMode(d manualDestination) taskconfig.DeleteMode {
	if d.DeleteMode == "" {
		return taskconfig.Recycle
	}
	return d.DeleteMode
}

func validateDestinationMode(d manualDestination) error {
	if d.Collision != "" && d.Collision != taskconfig.Skip {
		return errors.New("Sync暂不支持此冲突策略")
	}
	switch destinationStrategy(d) {
	case taskconfig.AddOnly:
	case taskconfig.Mirror:
		if !isAllFileRules(d.Filters) {
			return errors.New("Mirror仅支持全部文件，不可使用过滤")
		}
	default:
		return errors.New("不支持的同步策略")
	}
	if mode := destinationDeleteMode(d); mode != taskconfig.Recycle && mode != taskconfig.Permanent {
		return errors.New("不支持的删除方式")
	}
	return nil
}

type mirrorCopy func(context.Context, manualRequest, []taskconfig.Rule, func(transferProgress)) (transferProgress, error)

func runMirrorWith(ctx context.Context, q manualRequest, d manualDestination, notify func(transferProgress), confirm mirrorConfirmation, copyFiles mirrorCopy, recycle func(string) error) (p transferProgress, err error) {
	return runMirrorSession(ctx, q, d, nil, nil, notify, confirm, copyFiles, recycle)
}

func runMirrorSession(ctx context.Context, q manualRequest, d manualDestination, roots *transferRoots, sourceInv *fileinventory.Inventory, notify func(transferProgress), confirm mirrorConfirmation, copyFiles mirrorCopy, recycle func(string) error) (p transferProgress, err error) {
	if err = validateDestinationMode(d); err != nil {
		return
	}
	if roots == nil {
		var openErr error
		roots, openErr = openTransferRoots(ctx, q)
		if openErr != nil {
			return p, openErr
		}
		defer func() { err = errors.Join(err, roots.close(ctx)) }()
	} else if e := roots.ensureWritableTarget(ctx); e != nil {
		return p, e
	}
	if sourceInv == nil {
		var scanErr error
		sourceInv, scanErr = fileinventory.Scan(ctx, roots.source.path, fileinventory.Options{MaxEntries: 200000, MaxDepth: 128})
		if scanErr != nil {
			return p, scanErr
		}
	}
	if len(sourceInv.Files()) == 0 {
		return p, errors.New("源没有普通文件，Mirror禁止删除")
	}
	// Scan target once before copy: an incomplete/unsupported target blocks before
	// any copy, and its file list establishes pre-existing deletion candidates.
	targetBefore, e := fileinventory.Scan(ctx, roots.target.path, fileinventory.Options{MaxEntries: 200000, MaxDepth: 128})
	if e != nil {
		return p, e
	}
	if copyFiles != nil {
		p, e = copyFiles(ctx, q, allFileRules(), notify)
	} else {
		p, e = runManualWithSession(ctx, q, roots, sourceInv, notify, transferIO{copy: transferCopier(d.VerifyContents), publish: publishTransfer}, allFileRules())
	}
	if e != nil {
		return p, e
	}
	paths, targetStamps, e := collectMirrorCandidates(ctx, roots, targetBefore.Files())
	if e != nil {
		return p, e
	}
	preview := mirrorPreview{DestinationID: d.ID, Name: d.Name, Target: pathLabel(roots.target.path), Mode: destinationDeleteMode(d), Paths: paths}
	if len(preview.Paths) == 0 {
		return p, restoreMirrorDirectories(ctx, roots.target, sourceInv.Directories())
	}
	if preview.Mode == taskconfig.Recycle && (roots.target.network || roots.target.removable || roots.target.fat) {
		return p, errors.New("此目标不支持回收确认；多余文件未移除。若需要，请明确选择永久删除后重新运行。")
	}
	p.Stage = "等待删除确认"
	if notify != nil {
		notify(p)
	}
	if confirm == nil {
		return p, errMirrorDeclined
	}
	confirmation := preview
	confirmation.Paths = append([]string(nil), preview.Paths...)
	if e = confirm(ctx, confirmation); e != nil {
		return p, e
	}
	if e = ctx.Err(); e != nil {
		return p, e
	}
	hasRegular, e := fileinventory.HasRegularFile(ctx, roots.source.path, fileinventory.Options{MaxEntries: 200000, MaxDepth: 128})
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
		e = removeMirrorCandidate(ctx, roots, rel, targetStamps[rel], preview.Mode, recycle)
		if e != nil {
			return p, fmt.Errorf("%s: %w", rel, e)
		}
		p.Removed++
		if notify != nil {
			notify(p)
		}
	}
	return p, restoreMirrorDirectories(ctx, roots.target, sourceInv.Directories())
}
