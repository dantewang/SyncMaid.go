//go:build windows

package windowsfs

import (
	"fmt"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestIsUnsupportedInfoClass(t *testing.T) {
	for _, err := range []error{
		windows.ERROR_INVALID_PARAMETER,
		windows.ERROR_INVALID_FUNCTION,
		windows.ERROR_NOT_SUPPORTED,
		windows.ERROR_INVALID_LEVEL,
		fmt.Errorf("wrapped: %w", windows.ERROR_INVALID_PARAMETER),
	} {
		if !isUnsupportedInfoClass(err) {
			t.Fatalf("expected unsupported info class for %v", err)
		}
	}
	if isUnsupportedInfoClass(windows.ERROR_ACCESS_DENIED) {
		t.Fatal("access denied misclassified as unsupported info class")
	}
}

func TestFileIdentityLocalAndLegacyModes(t *testing.T) {
	dir := t.TempDir()
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)

	id, err := FileIdentity(h, false, false)
	if err != nil {
		t.Fatalf("local FileIdentity failed: %v", err)
	}
	if id.Legacy {
		t.Fatalf("expected 128-bit identity on local NTFS/ReFS dir, got legacy %+v", id)
	}

	legacy, err := FileIdentity(h, true, false)
	if err != nil {
		t.Fatalf("legacy FileIdentity failed: %v", err)
	}
	if !legacy.Legacy {
		t.Fatalf("expected legacy identity when fat=true, got %+v", legacy)
	}
}

func TestUNCPathDirTerminates(t *testing.T) {
	cur := `\\?\UNC\server\share\Eagle_backup`
	steps := 0
	for filepath.Dir(cur) != cur {
		cur = filepath.Dir(cur)
		steps++
		if steps > 10 {
			t.Fatalf("filepath.Dir did not terminate on UNC path, stuck at %q", cur)
		}
	}
	if cur != `\\?\UNC\server\share` && cur != `\\?\UNC\server\share\` {
		t.Fatalf("unexpected UNC root %q", cur)
	}
}
