package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// installation identifies the executable's real directory, never the caller's
// working directory. No configuration files are read while resolving it.
type installation struct {
	directory string
	data      string
	key       string
}

func currentInstallation() (installation, error) {
	exe, err := os.Executable()
	if err != nil {
		return installation{}, fmt.Errorf("locate executable: %w", err)
	}
	return installationForExecutable(exe)
}

func installationForExecutable(exe string) (installation, error) {
	if !filepath.IsAbs(exe) {
		return installation{}, fmt.Errorf("executable path must be absolute: %q", exe)
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return installation{}, fmt.Errorf("resolve executable %q: %w", exe, err)
	}
	directory := filepath.Dir(filepath.Clean(real))
	identity := directory
	if runtime.GOOS == "windows" {
		identity = strings.ToUpper(identity)
	}
	return installation{
		directory: directory,
		data:      filepath.Join(directory, "Data"),
		key:       fmt.Sprintf("SyncMaid-%x", sha256.Sum256([]byte(identity))),
	}, nil
}
