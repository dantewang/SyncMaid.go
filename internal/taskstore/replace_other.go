//go:build !windows

package taskstore

import "errors"

func nativeReplace(string, string, bool) error { return errors.ErrUnsupported }
