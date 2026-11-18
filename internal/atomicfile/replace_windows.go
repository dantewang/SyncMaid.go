//go:build windows

package atomicfile

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

func IsRegularFile(info os.FileInfo) bool {
	if !info.Mode().IsRegular() {
		return false
	}
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return ok && attributes.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0
}

// Same-directory MoveFileEx does not delete the destination first. Omitting
// REPLACE_EXISTING for first creation also refuses a late-created destination.
func Replace(from, to string, replaceExisting bool) error {
	f, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)
	if replaceExisting {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	return windows.MoveFileEx(f, t, flags)
}
