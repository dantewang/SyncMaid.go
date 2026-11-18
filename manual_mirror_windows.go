package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syncmaid/internal/windowsfs"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/taskconfig"
	"syncmaid/internal/winutil"
)

type mirrorStamp struct {
	ID       windowsfs.Identity
	Size     int64
	Modified windows.Filetime
}

// Parents remain pinned until the returned closer runs. Neither an intermediate
// reparse point nor a replaced final reparse file is followed.
func openMirrorFile(root windows.Handle, rel string, access uint32) (f *os.File, closeAll func() error, err error) {
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
	h := root
	for i, part := range parts {
		if i == len(parts)-1 {
			f, err = openTransferChild(h, part, false, windows.FILE_OPEN, access, windows.FILE_SHARE_READ)
			return
		}
		var d *os.File
		d, err = openTransferChild(h, part, true, windows.FILE_OPEN, windows.FILE_GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
		if err != nil {
			return
		}
		parents = append(parents, d)
		h = windows.Handle(d.Fd())
	}
	return
}
func mirrorFileStamp(f *os.File, fat bool) (s mirrorStamp, err error) {
	h := windows.Handle(f.Fd())
	if s.ID, err = windowsfs.FileIdentity(h, fat); err != nil {
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

// restoreMirrorDirectories creates missing source directories and preserves their
// timestamps after file publication and any confirmed removal. Extra folders
// are left alone; this never turns a file-only approval into folder deletion.
func restoreMirrorDirectories(ctx context.Context, root *winutil.DirectoryObject, dirs []fileinventory.Directory) error {
	for i := len(dirs) - 1; i >= 0; i-- {
		d := dirs[i]
		if err := protectedTransferIO(ctx, func() error {
			return restoreMirrorDirectory(ctx, root.Handle, d)
		}); err != nil {
			return fmt.Errorf("恢复目标目录 %s: %w", d.Path, err)
		}
	}
	return nil
}

func restoreMirrorDirectory(ctx context.Context, root windows.Handle, d fileinventory.Directory) (err error) {
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
	for _, name := range parts {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir, err := openTransferChild(root, name, true, windows.FILE_OPEN_IF,
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
func runMirror(ctx context.Context, q manualRequest, d manualDestination, notify func(transferProgress), confirm mirrorConfirmation) (p transferProgress, err error) {
	return runMirrorWith(ctx, q, d, notify, confirm, runManualFiltered, recycleMirrorFile)
}

// Shell parsing rejects the NT extended prefix (0x80070057). Only convert
// short local DOS paths; do not sacrifice extended-length I/O semantics.
func mirrorShellPath(path string) (string, error) {
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
func recycleMirrorFile(path string) error {
	path, e := mirrorShellPath(path)
	if e != nil {
		return e
	}
	return mygo.Shell.TrashItem(path)
}

func mirrorSourceExists(sourceRoot windows.Handle, rel string) (bool, error) {
	_, close, e := openMirrorFile(sourceRoot, rel, windows.GENERIC_READ)
	if e == nil {
		return true, close()
	}
	if errors.Is(e, windows.ERROR_FILE_NOT_FOUND) || errors.Is(e, windows.ERROR_PATH_NOT_FOUND) {
		return false, nil
	}
	return false, e
}

func collectMirrorCandidates(ctx context.Context, roots *transferRoots, targetFiles []fileinventory.File) ([]string, map[string]mirrorStamp, error) {
	// Use the actual filesystem's case-insensitive lookup, not filter Unicode
	// comparison, to distinguish destination extras from source spellings.
	var paths []string
	stamps := map[string]mirrorStamp{}
	for _, entry := range targetFiles {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		exists, e := mirrorSourceExists(roots.source.Handle, entry.Path)
		if e != nil {
			return nil, nil, e
		}
		if exists {
			continue
		}
		f, close, e := openMirrorFile(roots.target.Handle, entry.Path, windows.GENERIC_READ)
		if e != nil {
			if errors.Is(e, windows.ERROR_FILE_NOT_FOUND) || errors.Is(e, windows.ERROR_PATH_NOT_FOUND) {
				return nil, nil, errMirrorChanged
			}
			return nil, nil, e
		}
		stamp, e := mirrorFileStamp(f, roots.target.Fat)
		e = errors.Join(e, close())
		if e != nil {
			return nil, nil, e
		}
		if stamp.Size != entry.Size || stamp.Modified.Nanoseconds() != entry.Modified.UnixNano() {
			return nil, nil, errMirrorChanged
		}
		paths = append(paths, entry.Path)
		stamps[entry.Path] = stamp
	}
	return paths, stamps, nil
}
func removeMirrorCandidate(ctx context.Context, roots *transferRoots, rel string, expected mirrorStamp, mode taskconfig.DeleteMode, recycle func(string) error) error {
	exists, e := mirrorSourceExists(roots.source.Handle, rel)
	if e != nil {
		return e
	}
	if exists {
		return errMirrorChanged
	}
	f, close, e := openMirrorFile(roots.target.Handle, rel, windows.GENERIC_READ|windows.DELETE)
	if e != nil {
		return e
	}
	stamp, e := mirrorFileStamp(f, roots.target.Fat)
	if e == nil && stamp != expected {
		e = errMirrorChanged
	}
	if e != nil {
		return errors.Join(e, close())
	}
	// The shell accepts a path, not our handle. Pin parents while releasing
	// the file, then recycle. The final external replacement race is not CAS.
	e = protectedTransferIO(ctx, func() error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if mode == taskconfig.Permanent {
			return discardTransfer(f)
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
