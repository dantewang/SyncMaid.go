package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/taskconfig"
)

// manualRequest is a session task, deliberately separate from persisted legacy tasks.
type manualRequest struct{ Source, Target string }
type transferProgress struct {
	Stage, Path              string
	Total, Copied, Unchanged int
	Bytes                    int64
	SourceFiles              int
	SourceEmpty              bool
}

type transferRoots struct {
	source, target *directoryObject
	ancestors      []*directoryObject
}

func (r *transferRoots) Close() (err error) {
	for _, d := range r.ancestors {
		err = errors.Join(err, d.Close())
	}
	if r.target != nil {
		err = errors.Join(err, r.target.Close())
	}
	if r.source != nil {
		err = errors.Join(err, r.source.Close())
	}
	return
}

// Pin both roots and their ancestors. Identity comparisons detect aliases;
// no-delete-sharing handles keep the resolved operation paths stable for Scan.
func openTransferRoots(ctx context.Context, q manualRequest) (roots *transferRoots, err error) {
	return openTransferRootsAccess(ctx, q, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE)
}

var errTransferOverlap = errors.New("源与同步目标不可重叠")

func openTransferRootsAccess(ctx context.Context, q manualRequest, targetAccess uint32) (roots *transferRoots, err error) {
	if !filepath.IsAbs(q.Source) || !filepath.IsAbs(q.Target) {
		return nil, errors.New("源与目标必须是现有文件夹的绝对路径")
	}
	r := &transferRoots{}
	defer func() {
		if err != nil {
			err = errors.Join(err, protectedTransferIO(ctx, r.Close))
		}
	}()
	if r.source, err = openDirectory(q.Source); err != nil {
		return nil, fmt.Errorf("源文件夹: %w", err)
	}
	if r.target, err = openDirectoryAccess(q.Target, targetAccess); err != nil {
		return nil, fmt.Errorf("目标文件夹: %w", err)
	}
	if r.source.key == r.target.key {
		return nil, fmt.Errorf("%w: 源与目标指向同一文件夹", errTransferOverlap)
	}
	for _, pair := range [][2]*directoryObject{{r.source, r.target}, {r.target, r.source}} {
		if err = ordinaryDirectory(pair[0].handle); err != nil {
			return nil, err
		}
		for path := filepath.Dir(pair[0].path); ; path = filepath.Dir(path) {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			var parent *directoryObject
			parent, err = openDirectory(path)
			if err != nil {
				return nil, err
			}
			r.ancestors = append(r.ancestors, parent)
			if parent.key == pair[1].key {
				return nil, fmt.Errorf("%w: 源与目标互相包含（包括目录别名）", errTransferOverlap)
			}
			if filepath.Dir(path) == path {
				break
			}
		}
	}
	return r, nil
}

func validateManual(ctx context.Context, q manualRequest) (result manualRequest, err error) {
	r, err := openTransferRoots(ctx, q)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, protectedTransferIO(ctx, r.Close)) }()
	return manualRequest{Source: r.source.path, Target: r.target.path}, nil
}

func runManual(ctx context.Context, q manualRequest, notify func(transferProgress)) (p transferProgress, err error) {
	return runManualWith(ctx, q, notify, transferIO{copy: copyChecked, publish: publishTransfer})
}

// Only the two fallible transfer boundaries are injectable; tests still use real files.
type transferIO struct {
	copy    func(context.Context, *os.File, *os.File, func(int64)) error
	publish func(*os.File, windows.Handle, string, bool) error
}

type transferPublication uint8

const (
	transferStaged transferPublication = iota
	transferPublishUnknown
	transferPublished
)

var errTransferPublicationUnknown = errors.New("发布结果无法确认，已保留文件对象，请检查目标及临时文件")

func runManualWith(ctx context.Context, q manualRequest, notify func(transferProgress), ops transferIO) (p transferProgress, err error) {
	return runManualWithFilters(ctx, q, notify, ops, []taskconfig.Rule{{Kind: "all"}})
}
func runManualFiltered(ctx context.Context, q manualRequest, rules []taskconfig.Rule, notify func(transferProgress)) (transferProgress, error) {
	return runManualWithFilters(ctx, q, notify, transferIO{copy: copyChecked, publish: publishTransfer}, rules)
}
func runManualWithFilters(ctx context.Context, q manualRequest, notify func(transferProgress), ops transferIO, rules []taskconfig.Rule) (p transferProgress, err error) {
	update := func() {
		if notify != nil {
			notify(p)
		}
	}
	r, err := openTransferRoots(ctx, q)
	if err != nil {
		return p, err
	}
	defer func() { err = errors.Join(err, protectedTransferIO(ctx, r.Close)) }()
	p.Stage = "扫描源文件夹"
	update()
	inventory, err := fileinventory.Scan(ctx, r.source.path, fileinventory.Options{MaxEntries: 200000, MaxDepth: 128})
	if err != nil {
		return p, err
	} // No destination mutation before a complete source scan.
	raw := inventory.Files()
	p.SourceFiles = len(raw)
	p.SourceEmpty = inventory.Empty()
	p.Stage = "准备过滤"
	update()
	files, filterErr := selectFilteredFiles(ctx, raw, rules)
	if filterErr != nil {
		return p, filterErr
	}
	p.Total = len(files)
	if len(files) == 0 {
		p.Stage = "无匹配文件"
		update()
		return p, ctx.Err()
	}
	p.Stage = "同步中"
	update()
	for _, entry := range files {
		p.Path = entry.Path
		update()
		var copied bool
		copied, err = transferFile(ctx, r, entry, ops, func(n int64) { p.Bytes += n; update() })
		if copied {
			p.Copied++
		}
		if err != nil {
			return p, fmt.Errorf("%s: %w", entry.Path, err)
		}
		if !copied {
			p.Unchanged++
		}
		update()
	}
	if err = ctx.Err(); err != nil {
		return p, err
	}
	p.Stage = "已完成"
	p.Path = ""
	update()
	return p, nil
}

func transferFile(ctx context.Context, roots *transferRoots, entry fileinventory.File, ops transferIO, progress func(int64)) (copied bool, err error) {
	if err = ctx.Err(); err != nil {
		return false, err
	}
	parts := strings.Split(entry.Path, "/")
	for _, s := range parts {
		if s == "" || s == "." || s == ".." || strings.ContainsAny(s, `\:`) {
			return false, errors.New("非法相对路径")
		}
	}
	parents := []*os.File{}
	defer func() {
		err = errors.Join(err, protectedTransferIO(ctx, func() (e error) {
			for i := len(parents) - 1; i >= 0; i-- {
				e = errors.Join(e, parents[i].Close())
			}
			return
		}))
	}()
	// Open every actual directory relative to a pinned handle; never follow a
	// replaced junction. Held no-delete-sharing handles also pin its spelling.
	openParents := func(root windows.Handle, create bool) (windows.Handle, error) {
		parent := root
		for _, part := range parts[:len(parts)-1] {
			mode := uint32(windows.FILE_OPEN)
			if create {
				mode = windows.FILE_OPEN_IF
			}
			access := uint32(windows.FILE_GENERIC_READ)
			if create {
				access |= windows.FILE_GENERIC_WRITE
			}
			f, e := openTransferChild(parent, part, true, mode, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
			if e != nil {
				return 0, e
			}
			parents = append(parents, f)
			parent = windows.Handle(f.Fd())
		}
		return parent, nil
	}
	sp, e := openParents(roots.source.handle, false)
	if e != nil {
		return false, e
	}
	name := parts[len(parts)-1]
	source, e := openTransferChild(sp, name, false, windows.FILE_OPEN, windows.GENERIC_READ, windows.FILE_SHARE_READ)
	if e != nil {
		return false, e
	}
	defer func() { err = errors.Join(err, protectedTransferIO(ctx, source.Close)) }()
	stamp, e := source.Stat()
	if e != nil {
		return false, e
	}
	if stamp.Size() != entry.Size || !stamp.ModTime().Equal(entry.Modified) {
		return false, errors.New("源文件已在扫描后改变，请重新运行")
	}
	dp, e := openParents(roots.target.handle, true)
	if e != nil {
		return false, e
	}
	existing, e := openTransferChild(dp, name, false, windows.FILE_OPEN, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE)
	replace := false
	if e == nil {
		replace = true
		defer func() {
			if existing != nil {
				err = errors.Join(err, protectedTransferIO(ctx, existing.Close))
			}
		}()
		destStamp, se := existing.Stat()
		if se != nil {
			return false, se
		}
		if !transferNeeded(stamp.Size(), stamp.ModTime(), destStamp.Size(), destStamp.ModTime()) {
			return false, nil
		}
	} else if !errors.Is(e, windows.ERROR_FILE_NOT_FOUND) {
		return false, e
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	temp, e := openTransferChild(dp, ".syncmaid-"+rand.Text()+".tmp", false, windows.FILE_CREATE,
		windows.GENERIC_READ|windows.GENERIC_WRITE|windows.DELETE, 0)
	if e != nil {
		return false, e
	}
	publication := transferStaged
	defer func() {
		err = errors.Join(err, protectedTransferIO(ctx, func() (e error) {
			if publication == transferStaged {
				e = discardTransfer(temp)
			}
			return errors.Join(e, temp.Close())
		}))
	}()
	if e = ops.copy(ctx, source, temp, progress); e != nil {
		return false, e
	}
	mtime := windows.NsecToFiletime(stamp.ModTime().UnixNano())
	if e = windows.SetFileTime(windows.Handle(temp.Fd()), nil, nil, &mtime); e != nil {
		return false, e
	}
	if e = temp.Sync(); e != nil {
		return false, e
	}
	if e = ctx.Err(); e != nil {
		return false, e
	}
	// Nt's ordinary replace requires the old data handle closed. Publication
	// replaces a leaf relative to the still-pinned parent, never follows it.
	// This is atomic publication, not compare-and-swap against outside editors.
	if existing != nil {
		e = existing.Close()
		existing = nil
		if e != nil {
			return false, e
		}
	}
	e = protectedTransferIO(ctx, func() error {
		if e := ctx.Err(); e != nil {
			return e
		}
		// Once publish is attempted, an error alone is insufficient evidence
		// that this handle still names our temp. Never delete that object on
		// an uncertain result: it might already be the new target. Do not infer
		// success from a missing path, either. No Windows bug is assumed here.
		publication = transferPublishUnknown
		if e := ops.publish(temp, dp, name, replace); e != nil {
			return fmt.Errorf("%w (临时名称 %q): %w", errTransferPublicationUnknown, temp.Name(), e)
		}
		publication = transferPublished
		copied = true
		return nil
	})
	return copied, e
}
