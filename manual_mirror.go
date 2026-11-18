package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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
	if err = validateDestinationMode(d); err != nil {
		return
	}
	roots, e := openTransferRoots(ctx, q)
	if e != nil {
		return p, e
	}
	defer func() { err = errors.Join(err, roots.close(ctx)) }()
	source, e := scanMirror(ctx, roots.source)
	if e != nil {
		return p, e
	}
	if len(source.Files) == 0 {
		return p, errors.New("源没有普通文件，Mirror禁止删除")
	}
	// An incomplete/unsupported target blocks this target before copying too.
	if _, e = scanMirror(ctx, roots.target); e != nil {
		return p, e
	}
	p, e = copyFiles(ctx, q, allFileRules(), notify)
	if e != nil {
		return p, e
	}
	current, e := scanMirror(ctx, roots.source)
	if e != nil {
		return p, e
	}
	if !reflect.DeepEqual(source, current) {
		return p, errMirrorChanged
	}
	target, e := scanMirror(ctx, roots.target)
	if e != nil {
		return p, e
	}
	paths, e := mirrorCandidates(ctx, roots)
	if e != nil {
		return p, e
	}
	preview := mirrorPreview{DestinationID: d.ID, Name: d.Name, Target: pathLabel(roots.target.path), Mode: destinationDeleteMode(d), Paths: paths}
	if len(preview.Paths) == 0 {
		return p, restoreMirrorDirectories(ctx, roots.target, source.Dirs)
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
	checkSource, e := scanMirror(ctx, roots.source)
	if e != nil {
		return p, e
	}
	checkTarget, e := scanMirror(ctx, roots.target)
	if e != nil {
		return p, e
	}
	if !reflect.DeepEqual(source, checkSource) || !reflect.DeepEqual(target, checkTarget) {
		return p, errMirrorChanged
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
		e = removeMirrorCandidate(ctx, roots, rel, target.Files[rel], preview.Mode, recycle)
		if e != nil {
			return p, fmt.Errorf("%s: %w", rel, e)
		}
		p.Removed++
		if notify != nil {
			notify(p)
		}
	}
	return p, restoreMirrorDirectories(ctx, roots.target, source.Dirs)
}
