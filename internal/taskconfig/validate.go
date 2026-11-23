package taskconfig

import (
	"fmt"
	"math"
	"path"
	"strings"
)

// Check identifies an outstanding check, not a successful validation. In
// particular a nil error from Validate is never a filesystem/execute permit.
type Check struct{ Field, Requirement string }
type Validation struct{ Unverified []Check }

// Validate checks in-memory shape and known domain constraints without I/O.
// Decode deliberately accepts legacy data that is not runnable. Invalid lists
// can still be inspected/encoded without silently rewriting or losing entries.
// These domain rules are distinct from load compatibility and from outstanding
// execution conditions in Unverified. Passing them is not a runtime permit.
func Validate(tasks []Task) (Validation, error) {
	var report Validation
	if _, err := Encode(tasks); err != nil {
		return report, err
	}
	ids := map[ID]bool{}
	destIDs := map[ID]bool{}
	type location struct{ field, raw, key string }
	var sources, destinations []location
	pending := func(field, requirement string) {
		report.Unverified = append(report.Unverified, Check{field, requirement})
	}
	extensions := func(field string, e extras) {
		if len(e) > 0 {
			pending(field, "unknown-field semantics")
		}
	}
	var checkRule func(Rule, string)
	checkRule = func(r Rule, f string) {
		extensions(f, r.extra)
		if r.Kind == "regex" {
			pending(f, "Rust regex syntax and matching semantics")
		}
		if r.Rules != nil {
			for i, c := range *r.Rules {
				checkRule(c, fmt.Sprintf("%s.Rules[%d]", f, i))
			}
		}
		if r.Rule != nil {
			checkRule(*r.Rule, f+".Rule")
		}
	}
	addLocation := func(field, raw string) location {
		pending(field, "filesystem identity, aliases, accessibility and supported location")
		key := lexicalPath(raw)
		if key == "" {
			pending(field, "path layout needs platform resolution")
		}
		return location{field, raw, key}
	}
	for i, t := range tasks {
		f := fmt.Sprintf("tasks[%d]", i)
		id, _ := parseID(string(t.ID)) // Encode has already checked syntax.
		if ids[id] {
			return report, fmt.Errorf("%s.Id: duplicate task identity", f)
		}
		ids[id] = true
		if strings.TrimSpace(t.Name) == "" || strings.TrimSpace(t.SourcePath) == "" {
			return report, fmt.Errorf("%s: name and source are required", f)
		}
		extensions(f, t.extra)
		extensions(f+".Trigger", t.Trigger.extra)
		if t.Trigger.Kind == "scheduled" {
			pending(f+".Trigger", "five-field cron syntax, local time and future occurrence")
		}
		source := addLocation(f+".SourcePath", t.SourcePath)
		for _, other := range sources {
			if pathsConflict(source.raw, source.key, other.raw, other.key) {
				return report, fmt.Errorf("%s overlaps %s", source.field, other.field)
			}
		}
		sources = append(sources, source)
		for j, d := range t.Destinations {
			df := fmt.Sprintf("%s.Destinations[%d]", f, j)
			id, _ := parseID(string(d.ID))
			if destIDs[id] {
				return report, fmt.Errorf("%s.Id: duplicate destination identity", df)
			}
			destIDs[id] = true
			if strings.TrimSpace(d.Name) == "" || strings.TrimSpace(d.Target.Path) == "" {
				return report, fmt.Errorf("%s: name and target are required", df)
			}
			if (t.Kind == Moving) != (d.Strategy == Move) {
				return report, fmt.Errorf("%s.Strategy: incompatible with task Kind", df)
			}
			if math.IsNaN(d.MassDeleteThreshold) || math.IsInf(d.MassDeleteThreshold, 0) || d.MassDeleteThreshold < 0 || d.MassDeleteThreshold > 1 {
				return report, fmt.Errorf("%s.MassDeleteThreshold: expected fraction 0..1", df)
			}
			if d.Strategy == Mirror && (len(d.Filters) != 1 || d.Filters[0].Kind != "all") {
				return report, fmt.Errorf("%s.Filters: Mirror requires one all rule", df)
			}
			extensions(df, d.extra)
			extensions(df+".Target", d.Target.extra)
			pending(df+".Filters", "file filter compilation and matching compatibility")
			for k, r := range d.Filters {
				checkRule(r, fmt.Sprintf("%s.Filters[%d]", df, k))
			}
			loc := addLocation(df+".Target.Path", d.Target.Path)
			if pathsConflict(loc.raw, loc.key, source.raw, source.key) {
				return report, fmt.Errorf("%s overlaps source", loc.field)
			}
			for _, other := range destinations {
				if pathsConflict(loc.raw, loc.key, other.raw, other.key) {
					return report, fmt.Errorf("%s overlaps %s", loc.field, other.field)
				}
			}
			destinations = append(destinations, loc)
		}
	}
	return report, nil
}

// Only a lexical, ASCII Windows absolute-path comparison: never persisted and
// never a security identity. Relative, device, Unicode and alias paths require
// platform resolution. No cwd is consulted or baked into the configuration.
func cleanAbsolutePath(s string) string {
	if strings.ContainsRune(s, 0) {
		return ""
	}
	s = strings.ReplaceAll(s, `\`, "/")
	if strings.HasPrefix(s, "//?/") || strings.HasPrefix(s, "//./") {
		return ""
	}
	if len(s) >= 3 && ((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z')) && s[1:3] == ":/" {
		return strings.ToUpper(s[:2]) + path.Clean(s[2:])
	}
	if strings.HasPrefix(s, "//") {
		parts := strings.Split(strings.TrimPrefix(s, "//"), "/")
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" || parts[0] == "." || parts[0] == ".." || parts[1] == "." || parts[1] == ".." {
			return ""
		}
		return "//" + parts[0] + "/" + parts[1] + strings.TrimSuffix(path.Clean("/"+strings.Join(parts[2:], "/")), "/")
	}
	return ""
}

func lexicalPath(s string) string {
	for _, r := range s {
		if r > 127 || r == 0 {
			return ""
		}
	}
	clean := cleanAbsolutePath(s)
	if clean == "" {
		return ""
	}
	return strings.ToUpper(clean)
}

func hasPathOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, strings.TrimRight(b, "/")+"/") || strings.HasPrefix(b, strings.TrimRight(a, "/")+"/")
}

// PathsOverlap checks whether two path spellings lexically denote the same
// location or an ancestor/descendant pair. Non-ASCII paths are never case-folded
// so distinct Unicode names are left to platform resolution.
func PathsOverlap(a, b string) bool {
	if a != "" && a == b {
		return true
	}
	if ca, cb := cleanAbsolutePath(a), cleanAbsolutePath(b); ca != "" && cb != "" && hasPathOverlap(ca, cb) {
		return true
	}
	if ak, bk := lexicalPath(a), lexicalPath(b); ak != "" && bk != "" && hasPathOverlap(ak, bk) {
		return true
	}
	return false
}

func pathsConflict(a, ak, b, bk string) bool {
	if a == b {
		return true
	}
	if ak != "" && bk != "" {
		return hasPathOverlap(ak, bk)
	}
	ca, cb := cleanAbsolutePath(a), cleanAbsolutePath(b)
	return ca != "" && cb != "" && hasPathOverlap(ca, cb)
}
