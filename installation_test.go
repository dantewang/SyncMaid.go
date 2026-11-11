package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func executableIn(t *testing.T, directory, name string) string {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInstallationUsesDirectoryNotExecutableNameOrWorkingDirectory(t *testing.T) {
	directory := t.TempDir()
	first, err := installationForExecutable(executableIn(t, directory, "one.exe"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := installationForExecutable(executableIn(t, directory, "two.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first.data != filepath.Join(first.directory, "Data") {
		t.Fatalf("same installation resolved differently: %+v %+v", first, second)
	}
	// Changing cwd must have no bearing on an already absolute executable path.
	t.Chdir(t.TempDir())
	again, err := installationForExecutable(filepath.Join(directory, "one.exe"))
	if err != nil || again != first {
		t.Fatalf("cwd changed installation: %+v %v", again, err)
	}
	other, err := installationForExecutable(executableIn(t, t.TempDir(), "one.exe"))
	if err != nil || other.key == first.key || other.data == first.data {
		t.Fatalf("different installations collide: %+v %v", other, err)
	}
}

func TestInstallationRejectsUnknownLocationInsteadOfUsingCwd(t *testing.T) {
	for _, path := range []string{"", "relative.exe", filepath.Join(t.TempDir(), "missing.exe")} {
		if _, err := installationForExecutable(path); err == nil {
			t.Errorf("accepted unknown executable location %q", path)
		}
	}
}

func TestInstallationWindowsCase(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows case-insensitive identity")
	}
	path := executableIn(t, t.TempDir(), "App.exe")
	first, err := installationForExecutable(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := installationForExecutable(strings.ToUpper(path))
	if err != nil || second.key != first.key {
		t.Fatalf("path case changed identity: %+v %v", second, err)
	}
}

func TestInstallationResolvesDirectoryAlias(t *testing.T) {
	root := t.TempDir()
	path := executableIn(t, filepath.Join(root, "real"), "app.exe")
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(filepath.Dir(path), alias); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	first, err := installationForExecutable(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := installationForExecutable(filepath.Join(alias, "app.exe"))
	if err != nil || first != second {
		t.Fatalf("alias creates duplicate installation: %+v %+v %v", first, second, err)
	}
}
