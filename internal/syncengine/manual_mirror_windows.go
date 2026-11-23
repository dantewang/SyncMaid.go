package syncengine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syncmaid/internal/windowsfs"
	"time"

	"golang.org/x/sys/windows"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/taskconfig"
	"syncmaid/internal/winutil"
)

type MirrorStamp struct {
	ID       windowsfs.Identity
	Size     int64
	Modified windows.Filetime
}

// Parents remain pinned until the returned closer runs. Neither an intermediate
// reparse point nor a replaced final reparse file is followed.
func openMirrorFile(root *winutil.DirectoryObject, rel string, access uint32) (f *os.File, closeAll func() error, err error) {
	parts, err := splitRelativePath(rel)
	if err != nil {
		return nil, nil, err
	}
	var parents []*os.File
	closeAll = func() (e error) {
		if f != nil && f.Fd() != ^uintptr(0) {
			e = errors.Join(e, f.Close())
		}
		for i := len(parents) - 1; i >= 0; i-- {
			e = errors.Join(e, parents[i].Close())
		}
		return
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, closeAll())
		}
	}()
	h := root.Handle
	checkCase := needsCaseCheck(root)
	for i, part := range parts {
		if i == len(parts)-1 {
			f, err = openTransferChildChecked(h, part, false, checkCase, windows.FILE_OPEN, access, windows.FILE_SHARE_READ)
			return
		}
		var d *os.File
		d, err = openTransferChildChecked(h, part, true, checkCase, windows.FILE_OPEN, windows.FILE_GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
		if err != nil {
			return
		}
		parents = append(parents, d)
		h = windows.Handle(d.Fd())
	}
	return
}
func mirrorFileStamp(f *os.File, fat, network bool) (s MirrorStamp, err error) {
	h := windows.Handle(f.Fd())
	if s.ID, err = windowsfs.FileIdentity(h, fat, network); err != nil {
		return
	}
	var b windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &b); err != nil {
		return
	}
	s.Size = int64(uint64(b.FileSizeHigh)<<32 | uint64(b.FileSizeLow))
	s.Modified = b.LastWriteTime
	return
}

func markAncestorDirs(dirty map[string]bool, rel string) {
	for dir := path.Dir(rel); dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
		dirty[dir] = true
	}
}

func dirtyMirrorDirs(hasCopies bool, sourceInv, targetBefore *fileinventory.Inventory, removed []string) map[string]bool {
	if !hasCopies && len(removed) == 0 {
		return nil
	}
	dirty := map[string]bool{}
	for _, rel := range removed {
		markAncestorDirs(dirty, rel)
	}
	if hasCopies && sourceInv != nil {
		var targetFiles map[string]fileinventory.File
		if targetBefore != nil {
			list := targetBefore.Files()
			targetFiles = make(map[string]fileinventory.File, len(list))
			for _, tf := range list {
				targetFiles[tf.Path] = tf
			}
		}
		for _, sf := range sourceInv.Files() {
			if targetFiles != nil {
				if existing, ok := targetFiles[sf.Path]; ok && !TransferNeeded(sf.Size, sf.Modified, existing.Size, existing.Modified) {
					continue
				}
			}
			markAncestorDirs(dirty, sf.Path)
		}
	}
	return dirty
}

// restoreMirrorDirectories creates missing source directories and preserves their
// timestamps after file publication and any confirmed removal. Extra folders
// are left alone; this never turns a file-only approval into folder deletion.
func restoreMirrorDirectories(ctx context.Context, root *winutil.DirectoryObject, dirs []fileinventory.Directory, targetBefore *fileinventory.Inventory, dirty map[string]bool) error {
	var existing map[string]time.Time
	if targetBefore != nil {
		targetDirs := targetBefore.Directories()
		existing = make(map[string]time.Time, len(targetDirs))
		for _, td := range targetDirs {
			existing[td.Path] = td.Modified
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		d := dirs[i]
		if existing != nil && !dirty[d.Path] {
			if mtime, ok := existing[d.Path]; ok && mtime.Unix() == d.Modified.Unix() {
				continue
			}
		}
		if err := ProtectedTransferIO(ctx, func() error {
			return restoreMirrorDirectory(ctx, root, d)
		}); err != nil {
			return fmt.Errorf("恢复目标目录 %s: %w", d.Path, err)
		}
	}
	return nil
}

func restoreMirrorDirectory(ctx context.Context, rootDir *winutil.DirectoryObject, d fileinventory.Directory) (err error) {
	parts, err := splitRelativePath(d.Path)
	if err != nil {
		return errors.New("非法相对目录路径")
	}
	var parents []*os.File
	defer func() {
		for i := len(parents) - 1; i >= 0; i-- {
			err = errors.Join(err, parents[i].Close())
		}
	}()
	root := rootDir.Handle
	checkCase := needsCaseCheck(rootDir)
	for _, name := range parts {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir, err := openTransferChildChecked(root, name, true, checkCase, windows.FILE_OPEN_IF,
			windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
		if err != nil {
			return err
		}
		parents = append(parents, dir)
		root = windows.Handle(dir.Fd())
	}
	mtime := windows.NsecToFiletime(d.Modified.UnixNano())
	return windows.SetFileTime(root, nil, nil, &mtime)
}

// The reusable copy service finishes first: any copy failure prevents planning
// deletion. Confirmation authorizes exactly this local call's candidate list.
func RunMirror(ctx context.Context, q ManualRequest, d ManualDestination, notify func(TransferProgress), confirm MirrorConfirmation, recycle func(string) error) (p TransferProgress, err error) {
	return RunMirrorWith(ctx, q, d, notify, confirm, RunManualFiltered, recycle)
}

// Shell parsing rejects the NT extended prefix (0x80070057). Only convert
// short local DOS paths; do not sacrifice extended-length I/O semantics.
func MirrorShellPath(path string) (string, error) {
	if strings.HasPrefix(path, `\\?\`) {
		path = strings.TrimPrefix(path, `\\?\`)
		if len(path) < 3 || path[1] != ':' || path[2] != '\\' {
			return "", errors.New("回收站暂不支持此路径")
		}
	}
	wide, e := windows.UTF16FromString(path)
	if e != nil {
		return "", e
	}
	if len(wide) > 260 {
		return "", errors.New("回收站暂不支持此长路径；未删除文件")
	}
	return path, nil
}
func RecycleWithShell(trash func(string) error) func(string) error {
	return func(path string) error {
		path, e := MirrorShellPath(path)
		if e != nil {
			return e
		}
		if trash == nil {
			return errors.New("回收站接口不可用；未删除文件")
		}
		return trash(path)
	}
}

func mirrorSourceExists(sourceRoot *winutil.DirectoryObject, rel string) (bool, error) {
	_, close, e := openMirrorFile(sourceRoot, rel, windows.GENERIC_READ)
	if e == nil {
		return true, close()
	}
	if errors.Is(e, windows.ERROR_FILE_NOT_FOUND) || errors.Is(e, windows.ERROR_PATH_NOT_FOUND) {
		return false, nil
	}
	return false, e
}

func collectMirrorCandidates(ctx context.Context, roots *TransferRoots, targetFiles []fileinventory.File, sourceInv *fileinventory.Inventory) ([]string, map[string]MirrorStamp, error) {
	// Use the in-memory source inventory first, falling back to the filesystem's
	// case-insensitive lookup only for items absent from the source listing.
	var sourceSet map[string]bool
	if sourceInv != nil {
		list := sourceInv.Files()
		sourceSet = make(map[string]bool, len(list))
		for _, sf := range list {
			sourceSet[sf.Path] = true
		}
	}
	var paths []string
	stamps := map[string]MirrorStamp{}
	for _, entry := range targetFiles {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if sourceSet != nil && sourceSet[entry.Path] {
			continue
		}
		exists, e := mirrorSourceExists(roots.source, entry.Path)
		if e != nil {
			return nil, nil, e
		}
		if exists {
			continue
		}
		f, close, e := openMirrorFile(roots.target, entry.Path, windows.GENERIC_READ)
		if e != nil {
			if errors.Is(e, windows.ERROR_FILE_NOT_FOUND) || errors.Is(e, windows.ERROR_PATH_NOT_FOUND) {
				return nil, nil, ErrMirrorChanged
			}
			return nil, nil, e
		}
		stamp, e := mirrorFileStamp(f, roots.target.Fat, roots.target.Network)
		e = errors.Join(e, close())
		if e != nil {
			return nil, nil, e
		}
		if stamp.Size != entry.Size || stamp.Modified.Nanoseconds() != entry.Modified.UnixNano() {
			return nil, nil, ErrMirrorChanged
		}
		paths = append(paths, entry.Path)
		stamps[entry.Path] = stamp
	}
	return paths, stamps, nil
}
func removeMirrorCandidate(ctx context.Context, roots *TransferRoots, rel string, expected MirrorStamp, mode taskconfig.DeleteMode, recycle func(string) error) error {
	exists, e := mirrorSourceExists(roots.source, rel)
	if e != nil {
		return e
	}
	if exists {
		return ErrMirrorChanged
	}
	f, close, e := openMirrorFile(roots.target, rel, windows.GENERIC_READ|windows.DELETE)
	if e != nil {
		return e
	}
	stamp, e := mirrorFileStamp(f, roots.target.Fat, roots.target.Network)
	if e == nil && stamp != expected {
		e = ErrMirrorChanged
	}
	if e != nil {
		return errors.Join(e, close())
	}
	// The shell accepts a path, not our handle. Pin parents while releasing
	// the file, then recycle. The final external replacement race is not CAS.
	e = ProtectedTransferIO(ctx, func() error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if mode == taskconfig.Permanent {
			return discardTransfer(f)
		}
		if recycle == nil {
			return errors.New("回收站接口不可用；未删除文件")
		}
		if e := f.Close(); e != nil {
			return e
		}
		f = nil
		return recycle(filepath.Join(roots.target.Path, filepath.FromSlash(rel)))
	})
	e = errors.Join(e, close())
	return e
}
