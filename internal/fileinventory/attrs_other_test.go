//go:build !windows

package fileinventory

import "io/fs"

// Attribute checks only exist on Windows; tests needing them skip elsewhere.
const attrChecksSupported = false

func attrSys(fs.FileMode) any { return nil }
func attrReparse() any        { return nil }
