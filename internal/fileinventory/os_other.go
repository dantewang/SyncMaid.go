//go:build !windows

package fileinventory

import (
	"errors"
	"io/fs"
)

// Only Windows local volumes are in scope; other platforms fail closed.
func openRootDirectory(string) (directory, error) { return nil, errors.ErrUnsupported }

func reparseStatus(info fs.FileInfo) (reparse, known bool) {
	return info.Mode()&fs.ModeSymlink != 0, true
}
