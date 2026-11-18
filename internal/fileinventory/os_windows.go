//go:build windows

package fileinventory

import (
	"errors"

	"io/fs"
	"os"
	"syncmaid/internal/windowsfs"
	"syscall"

	"golang.org/x/sys/windows"
)

// osDirectory is an open directory handle. Children are opened relative to it
// with FILE_OPEN_REPARSE_POINT, so a directory swapped for a junction after it
// was listed is opened as the link itself and then rejected, never followed.
type osDirectory struct {
	file *os.File
	path string // for diagnostics only
}

func openRootDirectory(root string) (directory, error) {
	// The root is opened by path and may itself be a junction (followed, as in
	// the baseline). Only its descendants are guarded.
	f, err := os.Open(root)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && !info.IsDir() {
		err = &fs.PathError{Op: "open", Path: root, Err: errNotDirectory}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return &osDirectory{file: f, path: root}, nil
}

func (d *osDirectory) ReadDir(n int) ([]fs.DirEntry, error) { return d.file.ReadDir(n) }
func (d *osDirectory) Close() error                         { return d.file.Close() }

func (d *osDirectory) OpenChild(name string) (directory, error) {
	h, err := openChildDirectory(windows.Handle(d.file.Fd()), name)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: d.path + `\` + name, Err: err}
	}
	f := os.NewFile(uintptr(h), d.path+`\`+name)
	if f == nil {
		windows.CloseHandle(h)
		return nil, errors.New("invalid directory handle")
	}
	return &osDirectory{file: f, path: f.Name()}, nil
}

func openChildDirectory(parent windows.Handle, name string) (windows.Handle, error) {
	h, _, err := windowsfs.OpenChildNoReparse(parent, name,
		windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_FOR_BACKUP_INTENT)
	if errors.Is(err, windowsfs.ErrReparsePoint) {
		return 0, errBecameReparse
	}
	return h, err
}

func (d *osDirectory) CaseSensitive() (bool, error) {
	return windowsfs.CaseSensitive(windows.Handle(d.file.Fd()))
}

// reparseStatus reports the FILE_ATTRIBUTE_REPARSE_POINT bit and whether the
// attributes could be read at all. Every reparse tag is refused: dedup files
// and cloud placeholders do exist, but no tag set is verified here and
// DirEntry.Info exposes attributes (Sys) without the tag, so a regular Mode
// alone is not evidence of an ordinary file.
func reparseStatus(info fs.FileInfo) (reparse, known bool) {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return false, false
	}
	return data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0, true
}
