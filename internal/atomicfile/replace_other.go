//go:build !windows

package atomicfile

import (
	"errors"
	"os"
)

func IsRegularFile(info os.FileInfo) bool { return info.Mode().IsRegular() }

func Replace(string, string, bool) error { return errors.ErrUnsupported }
