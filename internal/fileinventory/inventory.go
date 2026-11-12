// Package fileinventory reads a local directory tree into a complete, raw
// listing for later planning. It never writes, copies, moves or deletes.
//
// Completeness has one authority: Scan returns a non-nil *Inventory only when
// every directory was read to the end and every entry was classified as a
// regular file or a regular directory. Any other outcome is a *ScanError and
// carries no listing, so a partial result can never be mistaken for the whole
// source. Success describes this traversal only. It is not an atomic snapshot,
// the tree can change during or after the scan, and it grants no permission to
// delete or run anything.
//
// Links and other reparse points are never followed: a tree containing one is
// reported as Unsupported instead of being silently skipped. Filtering is not
// applied here; an empty filtered set must not be read as an empty source.
package fileinventory

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"time"
)

// Default limits. Each depth level holds one open directory handle; the
// entry limit bounds memory. Neither value is benchmarked at full size.
const (
	DefaultMaxEntries = 2_000_000
	DefaultMaxDepth   = 1024
	maxUnsupported    = 64
	readBatch         = 256
)

// Options bounds a scan. Zero or negative values select the defaults.
type Options struct {
	MaxEntries int // directory entries seen, including unsupported ones
	MaxDepth   int // nesting below the root; the root's children are depth 1
}

// File is a regular file. Path is root-relative with '/' separators, keeping
// the name exactly as the file system returned it (case and Unicode form).
// Modified keeps the enumeration's precision; callers comparing stamps apply
// their own truncation.
type File struct {
	Path     string
	Size     int64
	Modified time.Time
}

// Directory is a regular (non-reparse) directory below the root.
type Directory struct {
	Path     string
	Modified time.Time
}

// Inventory is a complete listing produced by Scan. A zero Inventory is not a
// listing; its methods panic so it cannot stand in for an empty source.
type Inventory struct {
	root     string
	files    []File
	dirs     []Directory
	complete bool
}

func (i *Inventory) valid() {
	if i == nil || !i.complete {
		panic("fileinventory: Inventory was not produced by a successful Scan")
	}
}

// Root returns the root path exactly as passed to Scan.
func (i *Inventory) Root() string { i.valid(); return i.root }

// Files returns a copy sorted by Path (byte order).
func (i *Inventory) Files() []File { i.valid(); return slices.Clone(i.files) }

// Directories returns a copy sorted by Path (byte order).
func (i *Inventory) Directories() []Directory { i.valid(); return slices.Clone(i.dirs) }

// Empty reports an existing, fully read root with no entries at all.
func (i *Inventory) Empty() bool { i.valid(); return len(i.files) == 0 && len(i.dirs) == 0 }

// ErrorKind classifies a failed scan.
type ErrorKind string

const (
	InvalidRoot     ErrorKind = "invalid-root"     // empty or relative root
	RootUnavailable ErrorKind = "root-unavailable" // missing, not a directory, or cannot be opened
	Enumeration     ErrorKind = "enumeration"      // a read failed or the tree changed mid-scan
	Unsupported     ErrorKind = "unsupported"      // entries that cannot be listed safely
	LimitExceeded   ErrorKind = "limit-exceeded"
	Canceled        ErrorKind = "canceled"
)

// UnsupportedEntry explains why one entry prevents a complete listing.
type UnsupportedEntry struct {
	Path   string
	Reason string
}

// ScanError is the only failure type returned by Scan. Path is the
// root-relative location ("" for the root). Unsupported holds at most 64
// diagnostic samples; UnsupportedTotal counts all of them. Diagnostics never
// form a usable listing.
type ScanError struct {
	Kind             ErrorKind
	Root             string
	Path             string
	Err              error
	Unsupported      []UnsupportedEntry
	UnsupportedTotal int
}

func (e *ScanError) Error() string {
	msg := fmt.Sprintf("scan %q: %s", e.Root, e.Kind)
	if e.Path != "" {
		msg += fmt.Sprintf(" at %q", e.Path)
	}
	if e.UnsupportedTotal > 0 {
		msg += fmt.Sprintf(" (%d unsupported entries)", e.UnsupportedTotal)
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *ScanError) Unwrap() error { return e.Err }

var (
	errNotDirectory  = errors.New("not a directory")
	errBecameReparse = errors.New("directory became a link or reparse point during the scan")
)

// directory is the I/O boundary: one open directory handle. It exists so
// tests can inject real I/O faults; production uses the OS implementation.
type directory interface {
	ReadDir(n int) ([]fs.DirEntry, error)
	// OpenChild opens a direct subdirectory relative to this handle without
	// following a reparse point and fails if the opened object is one.
	OpenChild(name string) (directory, error)
	// CaseSensitive reports the per-directory case-sensitivity flag, or
	// errors.ErrUnsupported when the file system cannot answer.
	CaseSensitive() (bool, error)
	Close() error
}

// Scan lists root completely or fails. root must be an absolute path; it is
// used as given for I/O (a root that is itself a junction is followed, as the
// baseline does; identity and overlap checks belong to the caller).
// Cancellation is checked between directory reads; a single blocked OS read
// cannot be interrupted. All handles are closed before Scan returns.
func Scan(ctx context.Context, root string, opts Options) (*Inventory, error) {
	return scanWith(ctx, root, opts, openRootDirectory)
}

func scanWith(ctx context.Context, root string, opts Options, open func(string) (directory, error)) (*Inventory, error) {
	if opts.MaxEntries <= 0 {
		opts.MaxEntries = DefaultMaxEntries
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = DefaultMaxDepth
	}
	s := &scanner{ctx: ctx, opts: opts, root: root}
	if root == "" || !filepath.IsAbs(root) {
		return nil, s.fail(InvalidRoot, "", errors.New("root must be an absolute path"))
	}
	if err := ctx.Err(); err != nil {
		return nil, s.fail(Canceled, "", err)
	}
	d, err := open(root)
	if err != nil {
		return nil, s.fail(RootUnavailable, "", err)
	}
	if err := s.walk(d, "", 0); err != nil {
		return nil, err
	}
	if s.unsupportedTotal > 0 {
		e := s.fail(Unsupported, s.unsupported[0].Path, nil)
		e.Unsupported, e.UnsupportedTotal = s.unsupported, s.unsupportedTotal
		return nil, e
	}
	slices.SortFunc(s.files, func(a, b File) int { return cmp.Compare(a.Path, b.Path) })
	slices.SortFunc(s.dirs, func(a, b Directory) int { return cmp.Compare(a.Path, b.Path) })
	return &Inventory{root: root, files: s.files, dirs: s.dirs, complete: true}, nil
}
