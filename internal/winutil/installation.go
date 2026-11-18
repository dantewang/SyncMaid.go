package winutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// Installation identifies the executable's real directory, never the caller's
// working directory. No configuration files are read while resolving it.
type Installation struct {
	// Native operation paths; UI uses PathLabel. A future configuration
	// schema must explicitly choose its representation instead of saving these.
	Directory  string
	Data       string
	Key        string
	LegacyData string
}

func CurrentInstallation() (Installation, error) {
	exe, err := os.Executable()
	if err != nil {
		return Installation{}, fmt.Errorf("locate executable: %w", err)
	}
	return InstallationForExecutable(exe)
}

func InstallationForExecutable(exe string) (Installation, error) {
	if !filepath.IsAbs(exe) {
		return Installation{}, fmt.Errorf("executable path must be absolute: %q", exe)
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return Installation{}, fmt.Errorf("resolve executable %q: %w", exe, err)
	}
	directory := filepath.Dir(filepath.Clean(real))
	object, err := OpenDirectory(directory)
	if err != nil {
		return Installation{}, &LocationError{Path: directory, Cause: err}
	}
	defer object.Close()
	return Installation{
		Directory:  object.Path,
		Data:       filepath.Join(object.Path, "Data"),
		Key:        "SyncMaid-" + object.Key,
		LegacyData: filepath.Join(filepath.Dir(exe), "Data"),
	}, nil
}
