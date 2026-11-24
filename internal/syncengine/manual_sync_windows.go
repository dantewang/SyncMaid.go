package syncengine

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"syncmaid/internal/filefilter"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/taskconfig"
	"syncmaid/internal/winutil"
)

// ManualRequest is a session task, deliberately separate from persisted legacy tasks.
type ManualRequest struct{ Source, Target string }
type TransferProgress struct {
	Stage, Path                        string
	Total, Copied, Unchanged           int
	Bytes                              int64
	Removed, RemovedDirs               int
	Deferred                           int
	DeferredPaths                      []string
	Moved, Retained, Failed, Unmatched int
	SourceFiles                        int
	SourceEmpty                        bool

	// In-memory runtime fields only; excluded from persisted run-history-go.json.
	SubStage           string `json:"-"`
	ScanFiles          int    `json:"-"`
	ScanDirs           int    `json:"-"`
	ScanBytes          int64  `json:"-"`
	TotalBytes         int64  `json:"-"`
	ProcessedBytes     int64  `json:"-"`
	PlannedCopyFiles   int    `json:"-"`
	PlannedCopyBytes   int64  `json:"-"`
	PlannedRemoveFiles int    `json:"-"`
	PlannedRemoveDirs  int    `json:"-"`
	CurrentFileSize    int64  `json:"-"`
	CurrentFileDone    int64  `json:"-"`
}

const (
	MaxConsecutiveFailures = 10
	maxDeferredSamples     = 20
)

var ErrSourceChanged = errors.New("源文件已在扫描后改变，请重新运行")

func isDeferrableTransferError(err error) bool {
	return errors.Is(err, ErrSourceChanged) ||
		errors.Is(err, ErrMirrorChanged) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

func recordDeferred(p *TransferProgress, rel string) {
	p.Deferred++
	if rel != "" && len(p.DeferredPaths) < maxDeferredSamples {
		p.DeferredPaths = append(p.DeferredPaths, rel)
	}
}

type TransferRoots struct {
	source, target *winutil.DirectoryObject
	ancestors      []*winutil.DirectoryObject
	ownsSource     bool
	targetWritable bool
}

func (r *TransferRoots) Close() (err error) {
	for _, d := range r.ancestors {
		err = errors.Join(err, d.Close())
	}
	if r.target != nil {
		err = errors.Join(err, r.target.Close())
	}
	if r.ownsSource && r.source != nil {
		err = errors.Join(err, r.source.Close())
	}
	return
}

func (r *TransferRoots) close(ctx context.Context) error {
	return CloseProtected(ctx, r)
}

func (r *TransferRoots) CloseProtected(ctx context.Context) error {
	return r.close(ctx)
}

func (r *TransferRoots) Paths() (string, string) {
	return r.source.Path, r.target.Path
}

func (r *TransferRoots) ensureWritableTarget(ctx context.Context) error {
	if r.targetWritable {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	next, err := winutil.OpenDirectoryAccess(r.target.Path, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE)
	if err != nil {
		return fmt.Errorf("目标文件夹: %w", err)
	}
	if err := ordinaryDirectory(next); err != nil {
		return errors.Join(err, CloseProtected(ctx, next))
	}
	if next.Key != r.target.Key {
		return errors.Join(fmt.Errorf("%w: 目标目录句柄已变化", ErrTransferOverlap), CloseProtected(ctx, next))
	}
	old := r.target
	r.target = next
	r.targetWritable = true
	return CloseProtected(ctx, old)
}

// no-delete-sharing handles keep the resolved operation paths stable for Scan.
func OpenTransferRoots(ctx context.Context, q ManualRequest) (roots *TransferRoots, err error) {
	return OpenTransferRootsAccess(ctx, q, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE)
}

var ErrTransferOverlap = errors.New("源与同步目标不可重叠")

func openAncestors(ctx context.Context, rootPath string, againstKey string) ([]*winutil.DirectoryObject, error) {
	var ancestors []*winutil.DirectoryObject
	for child := rootPath; filepath.Dir(child) != child; {
		path := filepath.Dir(child)
		if err := ctx.Err(); err != nil {
			return ancestors, err
		}
		parent, err := winutil.OpenDirectoryAccess(path, windows.FILE_READ_ATTRIBUTES)
		if err != nil {
			return ancestors, err
		}
		ancestors = append(ancestors, parent)
		if againstKey != "" && parent.Key == againstKey {
			return ancestors, fmt.Errorf("%w: 源与目标互相包含（包括目录别名）", ErrTransferOverlap)
		}
		child = path
	}
	return ancestors, nil
}

func openDestinationRoots(ctx context.Context, source *winutil.DirectoryObject, sourceAncestors []*winutil.DirectoryObject, targetPath string, targetAccess uint32) (roots *TransferRoots, err error) {
	if strings.TrimSpace(targetPath) == "" {
		return nil, errors.New("目标文件夹未填写，请在目标编辑中浏览选择现有文件夹")
	}
	if !filepath.IsAbs(targetPath) {
		return nil, errors.New("目标文件夹：请选择现有文件夹的绝对路径")
	}
	r := &TransferRoots{
		source:         source,
		targetWritable: targetAccess&windows.FILE_GENERIC_WRITE == windows.FILE_GENERIC_WRITE,
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, r.close(ctx))
		}
	}()
	if r.target, err = winutil.OpenDirectoryAccess(targetPath, targetAccess); err != nil {
		return nil, fmt.Errorf("目标文件夹: %w", err)
	}
	if err = ordinaryDirectory(r.target); err != nil {
		return nil, err
	}
	if r.source.Key == r.target.Key {
		return nil, fmt.Errorf("%w: 源与目标指向同一文件夹", ErrTransferOverlap)
	}
	for _, parent := range sourceAncestors {
		if parent.Key == r.target.Key {
			return nil, fmt.Errorf("%w: 源与目标互相包含（包括目录别名）", ErrTransferOverlap)
		}
	}
	r.ancestors, err = openAncestors(ctx, r.target.Path, r.source.Key)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func OpenTransferRootsAccess(ctx context.Context, q ManualRequest, targetAccess uint32) (roots *TransferRoots, err error) {
	if !filepath.IsAbs(q.Source) {
		return nil, errors.New("源文件夹：请选择现有文件夹的绝对路径")
	}
	r := &TransferRoots{ownsSource: true}
	defer func() {
		if err != nil {
			err = errors.Join(err, r.close(ctx))
		}
	}()
	if r.source, err = winutil.OpenDirectoryAccess(q.Source, windows.FILE_READ_ATTRIBUTES); err != nil {
		return nil, fmt.Errorf("源文件夹: %w", err)
	}
	if err = ordinaryDirectory(r.source); err != nil {
		return nil, err
	}
	sourceAncestors, err := openAncestors(ctx, r.source.Path, "")
	defer func() {
		for _, d := range sourceAncestors {
			if closeErr := CloseProtected(ctx, d); closeErr != nil && err == nil {
				err = closeErr
			}
		}
	}()
	if err != nil {
		return nil, err
	}
	destRoots, err := openDestinationRoots(ctx, r.source, sourceAncestors, q.Target, targetAccess)
	if err != nil {
		return nil, err
	}
	r.target = destRoots.target
	r.targetWritable = destRoots.targetWritable
	r.ancestors = destRoots.ancestors
	destRoots.target = nil
	destRoots.ancestors = nil
	return r, nil
}

// Only the two fallible transfer boundaries are injectable; tests still use real files.
type TransferIO struct {
	Collision    taskconfig.CollisionPolicy
	Copy         func(context.Context, *os.File, *os.File, func(int64)) error
	Publish      func(*os.File, windows.Handle, string, bool) error
	SourceDelete func(*os.File) error
}

type TransferPublication uint8

const (
	TransferStaged TransferPublication = iota
	TransferPublishUnknown
	TransferPublished
)

var ErrTransferPublicationUnknown = errors.New("发布结果无法确认，已保留文件对象，请检查目标及临时文件")

func RunManualFiltered(ctx context.Context, q ManualRequest, rules []taskconfig.Rule, notify func(TransferProgress)) (TransferProgress, error) {
	return RunManualWithFilters(ctx, q, notify, TransferIO{Copy: CopyChecked, Publish: PublishTransfer}, rules)
}
func RunManualWithFilters(ctx context.Context, q ManualRequest, notify func(TransferProgress), ops TransferIO, rules []taskconfig.Rule, prepared ...*filefilter.Matcher) (p TransferProgress, err error) {
	return RunManualWithSession(ctx, q, nil, nil, notify, ops, rules, prepared...)
}
func RunManualWithSession(ctx context.Context, q ManualRequest, r *TransferRoots, inventory *fileinventory.Inventory, notify func(TransferProgress), ops TransferIO, rules []taskconfig.Rule, prepared ...*filefilter.Matcher) (p TransferProgress, err error) {
	return runManualWithSessionTarget(ctx, q, r, inventory, nil, notify, ops, rules, prepared...)
}

func runManualWithSessionTarget(ctx context.Context, q ManualRequest, r *TransferRoots, inventory *fileinventory.Inventory, targetInv *fileinventory.Inventory, notify func(TransferProgress), ops TransferIO, rules []taskconfig.Rule, prepared ...*filefilter.Matcher) (p TransferProgress, err error) {
	update := func() {
		if notify != nil {
			notify(p)
		}
	}
	if r == nil {
		r, err = OpenTransferRoots(ctx, q)
		if err != nil {
			return p, err
		}
		defer func() { err = errors.Join(err, r.close(ctx)) }()
	} else if err = r.ensureWritableTarget(ctx); err != nil {
		return p, err
	}
	if inventory == nil {
		p.Stage = "扫描源文件夹"
		p.SubStage = ""
		update()
		inventory, err = fileinventory.Scan(ctx, r.source.Path, fileinventory.Options{
			MaxEntries: 200000,
			MaxDepth:   128,
			OnProgress: func(sp fileinventory.ScanProgress) {
				p.Stage = "扫描源文件夹"
				p.Path = sp.CurrentPath
				p.ScanFiles = sp.Files
				p.ScanDirs = sp.Dirs
				p.ScanBytes = sp.Bytes
				update()
			},
		})
		if err != nil {
			return p, err
		} // No destination mutation before a complete source scan.
	}
	raw := inventory.Files()
	p.SourceFiles = len(raw)
	p.SourceEmpty = inventory.Empty()
	p.Stage = "准备过滤"
	p.SubStage = ""
	p.Path = ""
	update()
	var files []fileinventory.File
	var filterErr error
	if len(prepared) > 0 && prepared[0] != nil {
		files, filterErr = SelectMatchedFiles(ctx, raw, prepared[0])
	} else {
		files, filterErr = SelectFilteredFiles(ctx, raw, rules)
	}
	if filterErr != nil {
		return p, filterErr
	}
	p.Total = len(files)
	var totalBytes int64
	for _, entry := range files {
		totalBytes += entry.Size
	}
	p.TotalBytes = totalBytes
	if len(files) == 0 {
		p.Stage = "无匹配文件"
		update()
		return p, ctx.Err()
	}
	var targetFiles map[string]fileinventory.File
	if targetInv != nil {
		list := targetInv.Files()
		targetFiles = make(map[string]fileinventory.File, len(list))
		for _, tf := range list {
			targetFiles[tf.Path] = tf
		}
		var plannedFiles int
		var plannedBytes int64
		for _, entry := range files {
			if existing, ok := targetFiles[entry.Path]; ok && !TransferNeeded(entry.Size, entry.Modified, existing.Size, existing.Modified) {
				continue
			}
			plannedFiles++
			plannedBytes += entry.Size
		}
		p.PlannedCopyFiles = plannedFiles
		p.PlannedCopyBytes = plannedBytes
	}
	p.Stage = "同步中"
	update()
	var firstErr error
	consecutiveFailures := 0
	for _, entry := range files {
		if targetFiles != nil {
			if existing, ok := targetFiles[entry.Path]; ok && !TransferNeeded(entry.Size, entry.Modified, existing.Size, existing.Modified) {
				p.Unchanged++
				p.ProcessedBytes += entry.Size
				update()
				continue
			}
		}
		p.Path = entry.Path
		p.CurrentFileSize = entry.Size
		p.CurrentFileDone = 0
		p.SubStage = "写入中"
		update()
		var fileWritten int64
		var verifyDone int64
		verifyCtx := WithVerifyProgress(ctx, func(n int64) {
			verifyDone += n
			p.SubStage = "校验中"
			p.CurrentFileDone = verifyDone
			update()
		})
		var copied bool
		copied, err = transferFile(verifyCtx, r, entry, ops, func(n int64) {
			fileWritten += n
			p.Bytes += n
			p.ProcessedBytes += n
			p.CurrentFileDone = fileWritten
			p.SubStage = "写入中"
			update()
		})
		p.CurrentFileSize = 0
		p.CurrentFileDone = 0
		p.SubStage = ""
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return p, err
			}
			if entry.Size > fileWritten {
				p.ProcessedBytes += entry.Size - fileWritten
			}
			if isDeferrableTransferError(err) {
				recordDeferred(&p, entry.Path)
				consecutiveFailures = 0
				update()
				continue
			}
			p.Failed++
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", entry.Path, err)
			}
			consecutiveFailures++
			update()
			if consecutiveFailures >= MaxConsecutiveFailures {
				return p, fmt.Errorf("%w（连续失败 %d 次已中止）", firstErr, MaxConsecutiveFailures)
			}
			continue
		}
		if entry.Size > fileWritten {
			p.ProcessedBytes += entry.Size - fileWritten
		}
		consecutiveFailures = 0
		if copied {
			p.Copied++
		} else {
			p.Unchanged++
		}
		update()
	}
	if firstErr != nil {
		return p, firstErr
	}
	if err = ctx.Err(); err != nil {
		return p, err
	}
	p.Stage = "已完成"
	p.SubStage = ""
	p.Path = ""
	update()
	return p, nil
}

func transferFile(ctx context.Context, roots *TransferRoots, entry fileinventory.File, ops TransferIO, progress func(int64)) (bool, error) {
	return transferFileTo(ctx, roots, entry, entry.Path, nil, ops, progress)
}
func transferFileTo(ctx context.Context, roots *TransferRoots, entry fileinventory.File, targetRel string, expected *MirrorStamp, ops TransferIO, progress func(int64)) (copied bool, err error) {
	if err = ctx.Err(); err != nil {
		return false, err
	}
	parts, err := splitRelativePath(entry.Path)
	if err != nil {
		return false, errors.New("非法相对路径")
	}
	parents := []*os.File{}
	defer func() {
		err = errors.Join(err, CloseFilesProtected(ctx, parents))
	}()
	// Open every actual directory relative to a pinned handle; never follow a
	// replaced junction. Held no-delete-sharing handles also pin its spelling.
	openParents := func(root windows.Handle, create, checkCase bool, parts []string) (windows.Handle, error) {
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
			f, e := openTransferChildChecked(parent, part, true, checkCase, create, mode, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
			if e != nil {
				return 0, e
			}
			parents = append(parents, f)
			parent = windows.Handle(f.Fd())
		}
		return parent, nil
	}
	sp, e := openParents(roots.source.Handle, false, needsCaseCheck(roots.source), parts)
	if e != nil {
		return false, e
	}
	name := parts[len(parts)-1]
	access := uint32(windows.GENERIC_READ)
	if expected != nil {
		access |= windows.DELETE
	}
	source, e := retryTransferOpen(ctx, func() (*os.File, error) {
		return openTransferChild(sp, name, false, windows.FILE_OPEN, access, windows.FILE_SHARE_READ)
	})
	if e != nil {
		return false, e
	}
	defer func() { err = errors.Join(err, CloseProtected(ctx, source)) }()
	stamp, e := source.Stat()
	if e != nil {
		return false, e
	}
	if stamp.Size() != entry.Size || !stamp.ModTime().Equal(entry.Modified) {
		return false, ErrSourceChanged
	}
	if expected != nil {
		actual, e := mirrorFileStamp(source, roots.source.Fat, roots.source.Network)
		if e != nil {
			return false, e
		}
		if actual != *expected {
			return false, errors.New("源对象已变化，请重新运行")
		}
	}
	targetParts, e := splitRelativePath(targetRel)
	if e != nil {
		return false, errors.New("非法目标相对路径")
	}
	name = targetParts[len(targetParts)-1]
	dp, e := openParents(roots.target.Handle, true, needsCaseCheck(roots.target), targetParts)
	if e != nil {
		return false, e
	}
	var existing *os.File
	replace := false
	suffix := expected != nil && ops.Collision == taskconfig.Suffix
	if !suffix {
		existing, e = retryTransferOpen(ctx, func() (*os.File, error) {
			return openTransferChildChecked(dp, name, false, true, true, windows.FILE_OPEN, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE)
		})
		if e == nil {
			if expected != nil {
				return false, existing.Close()
			}
			replace = true
			defer func() {
				if existing != nil {
					err = errors.Join(err, CloseProtected(ctx, existing))
				}
			}()
			destStamp, se := existing.Stat()
			if se != nil {
				return false, se
			}
			if !TransferNeeded(stamp.Size(), stamp.ModTime(), destStamp.Size(), destStamp.ModTime()) {
				return false, nil
			}
		} else if !errors.Is(e, windows.ERROR_FILE_NOT_FOUND) {
			return false, e
		}

	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	temp, e := openTransferChild(dp, ".syncmaid-"+rand.Text()+".tmp", false, windows.FILE_CREATE,
		windows.GENERIC_READ|windows.GENERIC_WRITE|windows.DELETE, 0)
	if e != nil {
		return false, e
	}
	publication := TransferStaged
	defer func() {
		err = errors.Join(err, finalizeTempTransfer(ctx, temp, &publication))
	}()
	if e = ops.Copy(ctx, source, temp, progress); e != nil {
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
	if suffix {
		publication, e = publishMoveSuffix(ctx, temp, dp, name, ops.Publish)
		copied = publication == TransferPublished
	} else {
		e = publishTransferChecked(ctx, temp, dp, name, replace, &publication, ops.Publish)
		copied = publication == TransferPublished
	}
	if e == nil && copied && expected != nil {
		e = deleteVerifiedSource(ctx, source, *expected, roots.source.Fat, roots.source.Network, ops.SourceDelete)
	}
	return copied, e
}
