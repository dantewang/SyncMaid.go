package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// installation identifies the executable's real directory, never the caller's
// working directory. No configuration files are read while resolving it.
type installation struct {
	// Native operation paths; UI uses pathLabel. A future configuration
	// schema must explicitly choose its representation instead of saving these.
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
	object, err := openDirectory(directory)
	if err != nil {
		return installation{}, &locationError{path: directory, cause: err}
	}
	defer object.Close()
	return installation{
		directory: object.path,
		data:      filepath.Join(object.path, "Data"),
		key:       "SyncMaid-" + object.key,
	}, nil
}
