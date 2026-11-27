package buildinfo

import (
	"runtime/debug"
	"strings"
)

// Version, Commit, and BuildDate can be overridden at link time via -ldflags -X.
var (
	Version   string
	Commit    string
	BuildDate string
)

// Info describes the resolved application version and build metadata.
type Info struct {
	Version   string
	Commit    string
	BuildDate string
	Dirty     bool
	Stamped   bool
}

// Resolve returns build metadata combining link-time ldflags, a fallback version
// (such as from mygo.json), and Go VCS build settings.
func Resolve(fallbackVersion string) Info {
	bi, _ := debug.ReadBuildInfo()
	return resolve(Info{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
	}, fallbackVersion, bi)
}

func resolve(injected Info, fallbackVersion string, bi *debug.BuildInfo) Info {
	ver := normalizeVersion(injected.Version)
	stamped := ver != ""
	if !stamped {
		ver = normalizeVersion(fallbackVersion)
	}
	if ver == "" {
		ver = "0.0.0-dev"
	}

	commit := strings.TrimSpace(injected.Commit)
	buildDate := strings.TrimSpace(injected.BuildDate)
	var dirty bool

	if bi != nil {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if commit == "" {
					commit = strings.TrimSpace(s.Value)
				}
			case "vcs.time":
				if buildDate == "" {
					buildDate = strings.TrimSpace(s.Value)
				}
			case "vcs.modified":
				dirty = strings.EqualFold(strings.TrimSpace(s.Value), "true")
			}
		}
	}

	if len(commit) > 7 {
		commit = commit[:7]
	}

	return Info{
		Version:   ver,
		Commit:    commit,
		BuildDate: buildDate,
		Dirty:     dirty,
		Stamped:   stamped,
	}
}

func normalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

func (i Info) baseTag() string {
	if i.Stamped || strings.HasSuffix(i.Version, "-dev") {
		return "v" + i.Version
	}
	return "v" + i.Version + "-dev"
}

// Tag returns the SemVer Git tag name corresponding to Version.
func (i Info) Tag() string {
	return "v" + i.Version
}

// Display returns the human-readable version string for UI presentation.
func (i Info) Display() string {
	if i.Stamped {
		return i.Tag()
	}
	base := i.baseTag()
	if i.Commit != "" {
		if i.Dirty {
			return base + " (" + i.Commit + "-dirty)"
		}
		return base + " (" + i.Commit + ")"
	}
	if i.Dirty {
		return base + " (dirty)"
	}
	return base
}

// Summary returns a compact single-line diagnostic representation for logs.
func (i Info) Summary() string {
	parts := []string{i.baseTag()}
	if i.Commit != "" {
		parts = append(parts, "commit="+i.Commit)
	}
	if i.BuildDate != "" {
		parts = append(parts, "built="+i.BuildDate)
	}
	if i.Dirty {
		parts = append(parts, "dirty=true")
	}
	return strings.Join(parts, " ")
}
