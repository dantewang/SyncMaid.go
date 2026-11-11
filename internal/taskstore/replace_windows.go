package taskstore

import "golang.org/x/sys/windows"

// Same-directory MoveFileEx does not delete the destination first. Omitting
// REPLACE_EXISTING for first creation also refuses a late-created destination.
// WRITE_THROUGH is not a claim of full power-loss/directory durability.
func nativeReplace(from, to string, exists bool) error {
	f, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)
	if exists {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	return windows.MoveFileEx(f, t, flags)
}
