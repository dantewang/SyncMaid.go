//go:build windows

package fileinventory

import (
	"io/fs"
	"syscall"

	"golang.org/x/sys/windows"
)

const attrChecksSupported = true

// attrSys builds the *syscall.Win32FileAttributeData that os.DirEntry.Info
// exposes on Windows. It carries the attribute bits but not the reparse tag.
func attrSys(mode fs.FileMode) any {
	data := &syscall.Win32FileAttributeData{}
	if mode.IsDir() {
		data.FileAttributes |= windows.FILE_ATTRIBUTE_DIRECTORY
	}
	return data
}

func attrReparse() any {
	return &syscall.Win32FileAttributeData{
		FileAttributes: windows.FILE_ATTRIBUTE_REPARSE_POINT,
	}
}
