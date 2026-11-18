package winutil

import (
	"errors"
	"fmt"
	"hash/fnv"

	"golang.org/x/sys/windows"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

var ErrLegacyInstance = errors.New("Rust 旧版 SyncMaid 正在使用此 Data 目录")

// Rust derives this Local mutex name from the spelling of its Data path.
// The Go file claims remain the authority for Go-to-Go aliases and sessions.
func LegacyMutexName(path string) string {
	caser := cases.Upper(language.Und)
	h := fnv.New64a()
	for _, r := range path {
		_, _ = h.Write([]byte(caser.String(string(r))))
	}
	return fmt.Sprintf("Local\\SyncMaid-%016x", h.Sum64())
}

func (o *Ownership) claimLegacyPaths(paths ...string) error {
	claimed := map[string]bool{}
	for _, path := range paths {
		if path == "" {
			continue
		}
		for _, spelling := range []string{path, PathLabel(path)} {
			name := LegacyMutexName(spelling)
			if claimed[name] {
				continue
			}
			ptr, err := windows.UTF16PtrFromString(name)
			if err != nil {
				return err
			}
			h, err := windows.CreateMutex(nil, false, ptr)
			if h != 0 {
				o.claims = append(o.claims, h)
			}
			if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
				return ErrLegacyInstance
			}
			if err != nil {
				return err
			}
			claimed[name] = true
		}
	}
	return nil
}
