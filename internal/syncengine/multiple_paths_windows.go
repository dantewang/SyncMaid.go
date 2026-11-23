package syncengine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"syncmaid/internal/taskconfig"
	"syncmaid/internal/winutil"
)

type MultiplePlan struct {
	source          *winutil.DirectoryObject
	sourceAncestors []*winutil.DirectoryObject
	roots           []*TransferRoots
	targetRoots     map[taskconfig.ID]*TransferRoots
	requests        map[taskconfig.ID]ManualRequest
	failures        map[taskconfig.ID]error
}

func (p *MultiplePlan) Close() (err error) {
	for _, r := range p.roots {
		if r != nil {
			err = errors.Join(err, r.Close())
		}
	}
	for _, d := range p.sourceAncestors {
		err = errors.Join(err, d.Close())
	}
	if p.source != nil {
		err = errors.Join(err, p.source.Close())
	}
	return
}

func (p *MultiplePlan) takeTargetRoots(id taskconfig.ID) *TransferRoots {
	r := p.targetRoots[id]
	if r == nil {
		return nil
	}
	delete(p.targetRoots, id)
	for i, existing := range p.roots {
		if existing == r {
			p.roots[i] = nil
			break
		}
	}
	return r
}

func (p *MultiplePlan) close(ctx context.Context) error {
	return CloseProtected(ctx, p)
}

// Usable targets additionally require pinned object/ancestor identity checks.
func ObviousPathOverlap(a, b string) bool {
	if !filepath.IsAbs(a) || !filepath.IsAbs(b) {
		return false
	}
	return taskconfig.PathsOverlap(a, b)
}
func targetObjectsOverlap(a, b *TransferRoots) bool {
	if a.target.Key == b.target.Key {
		return true
	}
	for _, d := range a.ancestors {
		if d.Key == b.target.Key {
			return true
		}
	}
	for _, d := range b.ancestors {
		if d.Key == a.target.Key {
			return true
		}
	}
	return false
}

func OpenMultiplePlan(ctx context.Context, q ManualTaskRequest) (plan *MultiplePlan, err error) {
	if !filepath.IsAbs(q.Source) {
		return nil, errors.New("源文件夹：请选择现有文件夹的绝对路径")
	}
	if len(q.Destinations) == 0 {
		return nil, errors.New("请添加至少一个目标文件夹")
	}
	p := &MultiplePlan{
		targetRoots: map[taskconfig.ID]*TransferRoots{},
		requests:    map[taskconfig.ID]ManualRequest{},
		failures:    map[taskconfig.ID]error{},
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, p.close(ctx))
		}
	}()
	if p.source, err = winutil.OpenDirectoryAccess(q.Source, windows.FILE_READ_ATTRIBUTES); err != nil {
		return nil, fmt.Errorf("源文件夹: %w", err)
	}
	if err = ordinaryDirectory(p.source); err != nil {
		return nil, err
	}
	if p.sourceAncestors, err = openAncestors(ctx, p.source.Path, ""); err != nil {
		return nil, err
	}
	seen := map[taskconfig.ID]bool{}
	for i, d := range q.Destinations {
		if strings.TrimSpace(d.Name) == "" {
			return nil, errors.New("目标名称不能为空")
		}
		if d.ID == "" || seen[d.ID] {
			return nil, errors.New("目标 ID 缺失或重复")
		}
		seen[d.ID] = true
		if ObviousPathOverlap(q.Source, d.Path) || ObviousPathOverlap(p.source.Path, d.Path) {
			return nil, fmt.Errorf("%w: %s", ErrTransferOverlap, d.Name)
		}
		for _, other := range q.Destinations[:i] {
			if ObviousPathOverlap(other.Path, d.Path) {
				return nil, fmt.Errorf("%w: %s / %s", ErrTransferOverlap, other.Name, d.Name)
			}
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		// Read-only preflight allows an unwritable target to get its own run
		// failure, while still checking its identity before any target writes.
		r, e := openDestinationRoots(ctx, p.source, p.sourceAncestors, d.Path, windows.FILE_READ_ATTRIBUTES)
		if e != nil {
			if errors.Is(e, ErrTransferOverlap) {
				return nil, e
			}
			p.failures[d.ID] = e
			continue // never authorize this unresolved target
		}
		p.roots = append(p.roots, r)
		p.targetRoots[d.ID] = r
		for _, other := range p.roots[:len(p.roots)-1] {
			if targetObjectsOverlap(r, other) {
				return nil, fmt.Errorf("%w: 目标目录相同或互相包含（包括别名）", ErrTransferOverlap)
			}
		}
		// Keep every validated object and ancestor pinned for the entire job.
		p.requests[d.ID] = ManualRequest{Source: r.source.Path, Target: r.target.Path}
	}
	return p, nil
}
