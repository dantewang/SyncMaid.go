package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
	"syncmaid/internal/fileinventory"
	"syncmaid/internal/taskconfig"
)

type mirrorStamp struct {
	ID struct {
		Volume uint64
		File   [16]byte
	}
	Size     int64
	Modified windows.Filetime
}
type mirrorListing struct {
	Files map[string]mirrorStamp
	Dirs  []fileinventory.Directory
}

// Parents remain pinned until the returned closer runs. Neither an intermediate
// reparse point nor a replaced final reparse file is followed.
func openMirrorFile(root windows.Handle, rel string, access uint32) (f *os.File, closeAll func() error, err error) {
	if rel == "" || filepath.IsAbs(rel) || strings.Contains(rel, "\\") {
		return nil, nil, errors.New("非法相对文件路径")
	}
	parts := strings.Split(rel, "/")
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
		if part == "" || part == "." || part == ".." || strings.Contains(part, ":") {
			return nil, closeAll, errors.New("非法相对路径组件")
		}
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
func mirrorFileStamp(f *os.File) (s mirrorStamp, err error) {
	h := windows.Handle(f.Fd())
	if err = windows.GetFileInformationByHandleEx(h, windows.FileIdInfo, (*byte)(unsafe.Pointer(&s.ID)), uint32(unsafe.Sizeof(s.ID))); err != nil {
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
func scanMirror(ctx context.Context, root *directoryObject) (out mirrorListing, err error) {
	inventory, err := fileinventory.Scan(ctx, root.path, fileinventory.Options{MaxEntries: 200000, MaxDepth: 128})
	if err != nil {
		return out, err
	}
	out = mirrorListing{Files: map[string]mirrorStamp{}, Dirs: inventory.Directories()}
	for _, entry := range inventory.Files() {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		f, close, e := openMirrorFile(root.handle, entry.Path, windows.GENERIC_READ)
		if e != nil {
			return out, e
		}
		stamp, e := mirrorFileStamp(f)
		e = errors.Join(e, close())
		if e != nil {
			return out, e
		}
		if stamp.Size != entry.Size || stamp.Modified.Nanoseconds() != entry.Modified.UnixNano() {
			return out, errMirrorChanged
		}
		out.Files[entry.Path] = stamp
	}
	return out, nil
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

func mirrorCandidates(ctx context.Context, roots *transferRoots) ([]string, error) {
	// Use the actual filesystem's case-insensitive lookup, not filter Unicode
	// comparison, to distinguish destination extras from source spellings.
	inventory, e := fileinventory.Scan(ctx, roots.target.path, fileinventory.Options{MaxEntries: 200000, MaxDepth: 128})
	if e != nil {
		return nil, e
	}
	var paths []string
	for _, entry := range inventory.Files() {
		_, close, e := openMirrorFile(roots.source.handle, entry.Path, windows.GENERIC_READ)
		if e == nil {
			if e = close(); e != nil {
				return nil, e
			}
			continue
		}
		if !errors.Is(e, windows.ERROR_FILE_NOT_FOUND) && !errors.Is(e, windows.ERROR_PATH_NOT_FOUND) {
			return nil, e
		}
		paths = append(paths, entry.Path)
	}
	return paths, nil
}
func removeMirrorCandidate(ctx context.Context, roots *transferRoots, rel string, expected mirrorStamp, mode taskconfig.DeleteMode, recycle func(string) error) error {
	f, close, e := openMirrorFile(roots.target.handle, rel, windows.GENERIC_READ|windows.DELETE)
	if e != nil {
		return e
	}
	stamp, e := mirrorFileStamp(f)
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
		return recycle(filepath.Join(roots.target.path, filepath.FromSlash(rel)))
	})
	e = errors.Join(e, close())
	return e
}
