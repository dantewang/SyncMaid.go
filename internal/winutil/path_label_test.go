package winutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathLabelNamespaces(t *testing.T) {
	for input, want := range map[string]string{
		`C:\App\Data`:               `C:\App\Data`,
		`\\?\C:\App\Data`:           `C:\App\Data`,
		`\\?\c:\App\name. `:         `c:\App\name. `,
		`\\?\UNC\server\share\Data`: `\\server\share\Data`,
		`\\server\share\Data`:       `\\server\share\Data`,
		`\\?\Volume{example}\Data`:  `\\?\Volume{example}\Data`,
		`\\.\device`:                `\\.\device`,
		`\\?\C:relative`:            `\\?\C:relative`,
	} {
		if got := PathLabel(input); got != want {
			t.Errorf("%q => %q want %q", input, got, want)
		}
	}
}

func TestPathRepresentationPreservesDirectoryIdentity(t *testing.T) {
	path := t.TempDir()
	d, err := OpenDirectory(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	label := PathLabel(d.Path)
	if strings.HasPrefix(label, `\\?\`) {
		t.Fatal("ordinary DOS label leaked prefix")
	}
	again, err := OpenDirectory(label)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if d.Key != again.Key {
		t.Fatal("presentation conversion changed object identity")
	}
}

func TestLongNativeOperationPathIsPreserved(t *testing.T) {
	path := t.TempDir()
	for len(path) < 300 {
		path = filepath.Join(path, strings.Repeat("long", 8))
	}
	native := `\\?\` + path
	if err := os.MkdirAll(native, 0700); err != nil {
		t.Fatal(err)
	}
	d, err := OpenDirectory(native)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if !strings.HasPrefix(d.Path, `\\?\`) || len(PathLabel(d.Path)) < 260 {
		t.Fatal("long path lost its native form")
	}
	file := filepath.Join(d.Path, "operation-check")
	if err := os.WriteFile(file, []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "ok" {
		t.Fatalf("native I/O: %q %v", b, err)
	}
}
