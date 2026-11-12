package fileinventory

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// junction creates an isolated directory junction without changing any
// system or ACL setting (junctions need no privilege).
func junction(t *testing.T, path, target string) {
	t.Helper()
	shell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("PowerShell unavailable for junction fixture")
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	out, err := exec.Command(shell, "-NoProfile", "-NonInteractive", "-Command",
		"$ErrorActionPreference='Stop'; New-Item -ItemType Junction -Path "+quote(path)+" -Target "+quote(target)+" | Out-Null").CombinedOutput()
	if err != nil {
		t.Fatalf("junction fixture: %v %s", err, out)
	}
}

func scanUnsupported(t *testing.T, root string) *ScanError {
	t.Helper()
	inv, err := Scan(context.Background(), root, Options{})
	if inv != nil {
		t.Fatal("tree with a link returned a listing")
	}
	return assertKind(t, err, Unsupported)
}

func TestJunctionEscapeAndLoopAreNotFollowed(t *testing.T) {
	base := t.TempDir()
	root, outside := filepath.Join(base, "root"), filepath.Join(base, "outside")
	write(t, filepath.Join(root, "keep.txt"))
	write(t, filepath.Join(outside, "secret.txt"))
	junction(t, filepath.Join(root, "escape"), outside)
	junction(t, filepath.Join(root, "sub", "loop"), root)

	se := scanUnsupported(t, root)
	got := map[string]bool{}
	for _, u := range se.Unsupported {
		got[u.Path] = true
	}
	if se.UnsupportedTotal != 2 || !got["escape"] || !got["sub/loop"] {
		t.Fatalf("unsupported %+v", se.Unsupported)
	}
	if !strings.Contains(se.Error(), "2 unsupported") {
		t.Fatalf("message %q", se.Error())
	}
}

func TestSymlinksAreNotFollowed(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "target.txt"))
	if err := os.Symlink(filepath.Join(root, "target.txt"), filepath.Join(root, "file-link")); err != nil {
		t.Skipf("symlink creation needs privilege or Developer Mode: %v", err)
	}
	if err := os.Symlink(root, filepath.Join(root, "dir-link")); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	if se := scanUnsupported(t, root); se.UnsupportedTotal != 2 {
		t.Fatalf("unsupported %+v", se.Unsupported)
	}
}

func TestDirectoryReplacedByJunctionAfterListingIsRejected(t *testing.T) {
	base := t.TempDir()
	root, outside := filepath.Join(base, "root"), filepath.Join(base, "outside")
	write(t, filepath.Join(root, "swap", "f.txt"))
	write(t, filepath.Join(outside, "secret.txt"))
	swap := filepath.Join(root, "swap")
	_, err := scanWith(context.Background(), root, Options{}, func(p string) (directory, error) {
		d, err := openRootDirectory(p)
		if err != nil {
			return nil, err
		}
		return swapAfterRead{d, func() {
			if err := os.RemoveAll(swap); err != nil {
				t.Fatal(err)
			}
			junction(t, swap, outside)
		}}, nil
	})
	if se := assertKind(t, err, Enumeration); se.Path != "swap" || !errors.Is(se, errBecameReparse) {
		t.Fatalf("swapped dir: %v", err)
	}
}

type swapAfterRead struct {
	directory
	swap func()
}

func (s swapAfterRead) ReadDir(n int) ([]fs.DirEntry, error) {
	entries, err := s.directory.ReadDir(n)
	if errors.Is(err, io.EOF) {
		s.swap()
	}
	return entries, err
}

func TestRootJunctionIsFollowedLikeBaseline(t *testing.T) {
	base := t.TempDir()
	real, alias := filepath.Join(base, "real"), filepath.Join(base, "alias")
	write(t, filepath.Join(real, "a", "f.txt"))
	junction(t, alias, real)
	inv, err := Scan(context.Background(), alias, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if f := inv.Files(); len(f) != 1 || f[0].Path != "a/f.txt" || inv.Root() != alias {
		t.Fatalf("root junction listing %+v", f)
	}
}

func TestHiddenReadOnlyAndHardLinkedFilesAreListed(t *testing.T) {
	root := t.TempDir()
	ro := filepath.Join(root, "ro.txt")
	write(t, ro)
	if err := os.Chmod(ro, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ro, 0o644) })
	hidden := filepath.Join(root, "hidden.txt")
	write(t, hidden)
	name, err := windows.UTF16PtrFromString(hidden)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetFileAttributes(name, windows.FILE_ATTRIBUTE_HIDDEN|windows.FILE_ATTRIBUTE_SYSTEM); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(ro, filepath.Join(root, "hard.txt")); err != nil {
		t.Skipf("hard link unavailable: %v", err)
	}
	inv, err := Scan(context.Background(), root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Files()) != 3 {
		t.Fatalf("files %+v", inv.Files())
	}
}

func TestRealCaseSensitivityQueryOnTempVolume(t *testing.T) {
	d, err := openRootDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	sensitive, err := d.CaseSensitive()
	if err != nil && !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("case query: %v", err)
	}
	if sensitive {
		t.Fatal("new temp directory unexpectedly case-sensitive")
	}
}

func TestRealReservedDeviceNamesOnlyRejectCOM1to9AndLPT1to9(t *testing.T) {
	root := t.TempDir()
	legal := []string{"COM0", "com0.txt", "LPT0", "lpt0.log", "COM0.tar.gz"}
	for _, name := range legal {
		write(t, filepath.Join(root, name))
	}
	inv, err := Scan(context.Background(), root, Options{})
	if err != nil {
		t.Fatalf("COM0/LPT0 are ordinary file names: %v", err)
	}
	if got := paths(inv.Files(), func(f File) string { return f.Path }); !slices.Equal(got, slices.Sorted(slices.Values(legal))) {
		t.Fatalf("files %q", got)
	}

	// These names can be created as literal files on this build, so the scan
	// still refuses them on purpose: the refusal is a conservative policy, not
	// proof that the file cannot exist.
	reserved := []string{"COM1", "com9.txt", "LPT1", "lpt9.tar.gz", "COM¹", "LPT²"}
	for _, name := range reserved {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Skipf("reserved-name fixture %q cannot be created here: %v", name, err)
		}
	}
	se := scanUnsupported(t, root)
	if se.UnsupportedTotal != len(reserved) {
		t.Fatalf("unsupported %+v", se.Unsupported)
	}
}
