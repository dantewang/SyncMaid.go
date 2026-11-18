//go:build windows

package windowsfs

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

var ErrReparsePoint = errors.New("reparse point is not allowed")

// OpenChildNoReparse opens a direct child relative to parent with FILE_OPEN_REPARSE_POINT,
// failing closed if the opened object is itself a reparse point.
func OpenChildNoReparse(parent windows.Handle, name string, access, share, disposition, options uint32) (windows.Handle, windows.ByHandleFileInformation, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, windows.ByHandleFileInformation{}, err
	}
	attrs := windows.OBJECT_ATTRIBUTES{
		RootDirectory: parent,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	attrs.Length = uint32(unsafe.Sizeof(attrs))
	var h windows.Handle
	err = windows.NtCreateFile(&h, access|windows.SYNCHRONIZE, &attrs, &windows.IO_STATUS_BLOCK{},
		nil, windows.FILE_ATTRIBUTE_NORMAL, share, disposition,
		options|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT, 0, 0)
	if err != nil {
		if s, ok := err.(windows.NTStatus); ok {
			return 0, windows.ByHandleFileInformation{}, s.Errno()
		}
		return 0, windows.ByHandleFileInformation{}, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		windows.CloseHandle(h)
		return 0, windows.ByHandleFileInformation{}, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		windows.CloseHandle(h)
		return 0, windows.ByHandleFileInformation{}, ErrReparsePoint
	}
	return h, info, nil
}
