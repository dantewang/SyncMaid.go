//go:build windows

package fileinventory

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
	"unsafe"

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
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	attrs := windows.OBJECT_ATTRIBUTES{RootDirectory: parent, ObjectName: objectName}
	attrs.Length = uint32(unsafe.Sizeof(attrs))
	var h windows.Handle
	err = windows.NtCreateFile(&h,
		windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		&attrs, &windows.IO_STATUS_BLOCK{}, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|
			windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_OPEN_REPARSE_POINT,
		0, 0)
	if err != nil {
		if s, ok := err.(windows.NTStatus); ok {
			return 0, s.Errno() // keeps errors.Is(fs.ErrNotExist/ErrPermission)
		}
		return 0, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		windows.CloseHandle(h)
		return 0, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		windows.CloseHandle(h)
		return 0, errBecameReparse
	}
	return h, nil
}

type fileCaseSensitiveInfo struct{ Flags uint32 }

func (d *osDirectory) CaseSensitive() (bool, error) {
	var info fileCaseSensitiveInfo
	err := windows.GetFileInformationByHandleEx(windows.Handle(d.file.Fd()), windows.FileCaseSensitiveInfo,
		(*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	switch {
	case err == nil:
		return info.Flags&windows.FILE_CS_FLAG_CASE_SENSITIVE_DIR != 0, nil
	case errors.Is(err, windows.ERROR_INVALID_PARAMETER), errors.Is(err, windows.ERROR_INVALID_FUNCTION),
		errors.Is(err, windows.ERROR_NOT_SUPPORTED), errors.Is(err, windows.ERROR_INVALID_LEVEL):
		// FAT/exFAT and some redirectors cannot answer.
		return false, fmt.Errorf("%w: %v", errors.ErrUnsupported, err)
	}
	return false, err
}

// isReparse reports entries carrying FILE_ATTRIBUTE_REPARSE_POINT, except
// deduplicated regular files, which Go 1.27.3 maps to a regular mode (all
// other reparse tags become ModeSymlink, ModeIrregular or a non-directory).
func isReparse(info fs.FileInfo) bool {
	if info.Mode()&fs.ModeSymlink != 0 {
		return true
	}
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok || data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		return false
	}
	return !(info.Mode().Type() == 0 && data.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0)
}
