package fileinventory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"unicode"
)

var errFoundRegularFile = errors.New("found regular file")

type scanner struct {
	ctx              context.Context
	opts             Options
	root             string
	stopOnFirstFile  bool
	entries          int
	files            []File
	dirs             []Directory
	unsupported      []UnsupportedEntry
	unsupportedTotal int
}

func (s *scanner) fail(kind ErrorKind, path string, err error) *ScanError {
	return &ScanError{Kind: kind, Root: s.root, Path: path, Err: err}
}

func (s *scanner) addUnsupported(path, reason string) {
	s.unsupportedTotal++
	if len(s.unsupported) < maxUnsupported {
		s.unsupported = append(s.unsupported, UnsupportedEntry{Path: path, Reason: reason})
	}
}

// closeFailure keeps both the original cause and the close error, so a
// cancellation or an I/O failure is still recognizable after cleanup.
func (s *scanner) closeFailure(err error, rel string, closeErr error) error {
	wrapped := fmt.Errorf("close directory: %w", closeErr)
	var se *ScanError
	if errors.As(err, &se) {
		se.Err = errors.Join(se.Err, wrapped)
		return err
	}
	if err == nil {
		return s.fail(Enumeration, rel, wrapped)
	}
	return s.fail(Enumeration, rel, errors.Join(err, wrapped))
}

func join(rel, name string) string {
	if rel == "" {
		return name
	}
	return rel + "/" + name
}

// walk reads d completely, then descends into its regular subdirectories in
// name order. d stays open while its children are walked (they are opened
// relative to it) and is always closed before walk returns.
func (s *scanner) walk(d directory, rel string, depth int) (err error) {
	defer func() {
		if closeErr := d.Close(); closeErr != nil {
			err = s.closeFailure(err, rel, closeErr)
		}
	}()
	sensitive, err := d.CaseSensitive()
	switch {
	case err == nil && sensitive:
		// Names that differ only in case are distinct here; the planner's
		// case-insensitive comparisons would merge them.
		s.addUnsupported(rel, "case-sensitive directory")
		return nil
	case err != nil && !errors.Is(err, errors.ErrUnsupported):
		return s.fail(Enumeration, rel, fmt.Errorf("query case sensitivity: %w", err))
	}

	var subdirs []string
	folded := map[string]string{}
	for {
		if err := s.ctx.Err(); err != nil {
			return s.fail(Canceled, rel, err)
		}
		batch, readErr := d.ReadDir(readBatch)
		for _, entry := range batch {
			if err := s.entry(entry, rel, folded, &subdirs); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return s.fail(Enumeration, rel, readErr)
		}
		if len(batch) == 0 {
			return s.fail(Enumeration, rel, errors.New("directory read returned no entries and no end"))
		}
	}

	slices.Sort(subdirs)
	for _, name := range subdirs {
		path := join(rel, name)
		if err := s.ctx.Err(); err != nil {
			return s.fail(Canceled, path, err)
		}
		if depth+1 > s.opts.MaxDepth {
			return s.fail(LimitExceeded, path, fmt.Errorf("directory depth exceeds %d", s.opts.MaxDepth))
		}
		child, err := d.OpenChild(name)
		if err != nil {
			// The entry was listed, so failing to open it (missing, denied,
			// replaced by a link) means this scan cannot be complete.
			return s.fail(Enumeration, path, err)
		}
		if err := s.walk(child, path, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func (s *scanner) entry(entry fs.DirEntry, rel string, folded map[string]string, subdirs *[]string) error {
	name := entry.Name()
	path := join(rel, name)
	s.entries++
	if s.entries > s.opts.MaxEntries {
		return s.fail(LimitExceeded, path, fmt.Errorf("more than %d entries", s.opts.MaxEntries))
	}
	if reason := unsupportedName(name); reason != "" {
		s.addUnsupported(path, reason)
		return nil
	}
	// Defensive only: a case-insensitive directory cannot hold these, and this
	// simple fold is not the file system's identity rule.
	key := foldKey(name)
	if other, ok := folded[key]; ok {
		s.addUnsupported(path, fmt.Sprintf("name differs from %q only by case", other))
		return nil
	}
	folded[key] = name

	info, err := entry.Info()
	if err != nil {
		return s.fail(Enumeration, path, err)
	}
	mode := info.Mode()
	reparse, known := reparseStatus(info)
	switch {
	case !known:
		// A regular Mode alone is not evidence of an ordinary file; without
		// the attributes a link or placeholder cannot be ruled out.
		s.addUnsupported(path, "file attributes unavailable; cannot rule out a link or reparse point")
	case reparse:
		s.addUnsupported(path, "link or reparse point (junction, symlink, dedup, cloud placeholder) is not followed")
	case mode.IsDir():
		s.dirs = append(s.dirs, Directory{Path: path, Modified: info.ModTime().UTC()})
		*subdirs = append(*subdirs, name)
	case mode.IsRegular():
		if s.stopOnFirstFile {
			return errFoundRegularFile
		}
		s.files = append(s.files, File{Path: path, Size: info.Size(), Modified: info.ModTime().UTC()})
	default:
		s.addUnsupported(path, "special file: "+mode.Type().String())
	}
	return nil
}

// foldKey approximates the NTFS upcase table with simple per-rune uppercase,
// so names NTFS keeps distinct (k and U+212A KELVIN SIGN, ß and SS) stay
// distinct here.
func foldKey(name string) string { return strings.Map(unicode.ToUpper, name) }

// unsupportedName rejects names that cannot be carried safely as a
// root-relative '/' path or that Win32 path APIs would resolve to a different
// object (trailing dot/space, DOS device names). The OS normally prevents the
// separator cases; they are checked so a listed path can never escape.
func unsupportedName(name string) string {
	switch {
	case name == "" || name == "." || name == "..":
		return "invalid name"
	case strings.ContainsRune(name, unicode.ReplacementChar):
		// Go decodes unpaired UTF-16 surrogates to U+FFFD, so the name may not
		// round-trip to the real file. A genuine U+FFFD is rejected too.
		return "name contains U+FFFD (possibly an unpaired surrogate)"
	case strings.ContainsAny(name, `/\:*?"<>|`):
		return "name contains a path separator or Win32-reserved character"
	case strings.IndexFunc(name, func(r rune) bool { return r < 0x20 }) >= 0:
		return "name contains a control character"
	case strings.HasSuffix(name, ".") || strings.HasSuffix(name, " "):
		return "name ends with a dot or space (ambiguous for Win32 paths)"
	case isDeviceName(name):
		return "reserved DOS device name"
	}
	return ""
}

func isDeviceName(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	// Reserved are COM1-9/LPT1-9 and the superscript 1-3 forms; COM0/LPT0
	// name ordinary files. See notes/file-inventory-device-names.md.
	if r := []rune(base); len(r) == 4 && (string(r[:3]) == "COM" || string(r[:3]) == "LPT") {
		return strings.ContainsRune("123456789¹²³", r[3])
	}
	return false
}
