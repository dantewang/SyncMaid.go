package fileinventory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeEntry/fakeDir inject I/O faults at the directory-handle boundary only.
type fakeEntry struct {
	name    string
	mode    fs.FileMode
	infoErr error
}

func (e fakeEntry) Name() string               { return e.name }
func (e fakeEntry) IsDir() bool                { return e.mode.IsDir() }
func (e fakeEntry) Type() fs.FileMode          { return e.mode.Type() }
func (e fakeEntry) Info() (fs.FileInfo, error) { return fakeInfo(e), e.infoErr }

type fakeInfo fakeEntry

func (i fakeInfo) Name() string       { return i.name }
func (i fakeInfo) Size() int64        { return 1 }
func (i fakeInfo) Mode() fs.FileMode  { return i.mode }
func (i fakeInfo) ModTime() time.Time { return time.Unix(1, 0) }
func (i fakeInfo) IsDir() bool        { return i.mode.IsDir() }
func (i fakeInfo) Sys() any           { return nil }

type fakeTree struct {
	open      int
	children  map[string][]fakeEntry
	readErr   map[string]error // returned after the directory's entries
	openErr   map[string]error
	caseErr   map[string]error
	sensitive map[string]bool
	onRead    func()
}

type fakeDir struct {
	tree   *fakeTree
	path   string
	served bool
	closed bool
}

func (t *fakeTree) openDir(path string) (directory, error) {
	if err := t.openErr[path]; err != nil {
		return nil, err
	}
	t.open++
	return &fakeDir{tree: t, path: path}, nil
}

func (d *fakeDir) ReadDir(int) ([]fs.DirEntry, error) {
	if d.tree.onRead != nil {
		d.tree.onRead()
	}
	if d.served {
		if err := d.tree.readErr[d.path]; err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	d.served = true
	var out []fs.DirEntry
	for _, e := range d.tree.children[d.path] {
		out = append(out, e)
	}
	return out, nil
}

func (d *fakeDir) OpenChild(name string) (directory, error) {
	return d.tree.openDir(join(d.path, name))
}
func (d *fakeDir) CaseSensitive() (bool, error) {
	if err := d.tree.caseErr[d.path]; err != nil {
		return false, err
	}
	if d.tree.sensitive[d.path] {
		return true, nil
	}
	return false, errors.ErrUnsupported
}
func (d *fakeDir) Close() error {
	if d.closed {
		panic("double close")
	}
	d.closed = true
	d.tree.open--
	return nil
}

const fakeRoot = `C:\fake`

func (t *fakeTree) scan(ctx context.Context, opts Options) (*Inventory, error) {
	return scanWith(ctx, fakeRoot, opts, func(string) (directory, error) { return t.openDir("") })
}

func file(name string) fakeEntry { return fakeEntry{name: name} }
func dir(name string) fakeEntry  { return fakeEntry{name: name, mode: fs.ModeDir} }

func assertKind(t *testing.T, err error, kind ErrorKind) *ScanError {
	t.Helper()
	var se *ScanError
	if !errors.As(err, &se) || se.Kind != kind {
		t.Fatalf("want %s ScanError, got %v", kind, err)
	}
	return se
}

func TestInjectedFailuresYieldNoListing(t *testing.T) {
	boom := errors.New("injected")
	tests := []struct {
		name string
		tree fakeTree
		ctx  func() context.Context
		opts Options
		kind ErrorKind
		path string
	}{
		{name: "read error after entries", kind: Enumeration, path: "a",
			tree: fakeTree{children: map[string][]fakeEntry{"": {dir("a"), file("z")}, "a": {file("x")}},
				readErr: map[string]error{"a": boom}}},
		{name: "entry metadata error", kind: Enumeration, path: "bad",
			tree: fakeTree{children: map[string][]fakeEntry{"": {file("ok"), {name: "bad", infoErr: fs.ErrPermission}}}}},
		{name: "listed child vanished", kind: Enumeration, path: "a/b",
			tree: fakeTree{children: map[string][]fakeEntry{"": {dir("a")}, "a": {dir("b")}},
				openErr: map[string]error{"a/b": fs.ErrNotExist}}},
		{name: "child became reparse", kind: Enumeration, path: "a",
			tree: fakeTree{children: map[string][]fakeEntry{"": {dir("a")}}, openErr: map[string]error{"a": errBecameReparse}}},
		{name: "case query failure", kind: Enumeration, path: "a",
			tree: fakeTree{children: map[string][]fakeEntry{"": {dir("a")}}, caseErr: map[string]error{"a": boom}}},
		{name: "entry limit", kind: LimitExceeded, path: "c", opts: Options{MaxEntries: 2},
			tree: fakeTree{children: map[string][]fakeEntry{"": {file("a"), file("b"), file("c")}}}},
		{name: "depth limit", kind: LimitExceeded, path: "a/b", opts: Options{MaxDepth: 1},
			tree: fakeTree{children: map[string][]fakeEntry{"": {dir("a")}, "a": {dir("b")}}}},
		{name: "special file", kind: Unsupported, path: "pipe",
			tree: fakeTree{children: map[string][]fakeEntry{"": {file("ok"), {name: "pipe", mode: fs.ModeNamedPipe}}}}},
		{name: "symlink entry", kind: Unsupported, path: "link",
			tree: fakeTree{children: map[string][]fakeEntry{"": {{name: "link", mode: fs.ModeSymlink}}}}},
		{name: "case duplicate", kind: Unsupported, path: "A.txt",
			tree: fakeTree{children: map[string][]fakeEntry{"": {file("a.txt"), file("A.txt")}}}},
		{name: "separator in name", kind: Unsupported, path: `x\..\y`,
			tree: fakeTree{children: map[string][]fakeEntry{"": {file(`x\..\y`)}}}},
		{name: "dotdot name", kind: Unsupported, path: "..",
			tree: fakeTree{children: map[string][]fakeEntry{"": {dir("..")}}}},
		{name: "case-sensitive directory", kind: Unsupported, path: "a",
			tree: fakeTree{children: map[string][]fakeEntry{"": {dir("a")}, "a": {file("x"), file("X")}}, sensitive: map[string]bool{"a": true}}},
		{name: "canceled before start", kind: Canceled,
			ctx: func() context.Context { c, cancel := context.WithCancel(context.Background()); cancel(); return c }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.ctx != nil {
				ctx = tt.ctx()
			}
			inv, err := tt.tree.scan(ctx, tt.opts)
			if inv != nil {
				t.Fatal("failure returned a listing")
			}
			se := assertKind(t, err, tt.kind)
			if se.Path != tt.path {
				t.Fatalf("path %q, want %q (%v)", se.Path, tt.path, err)
			}
			if tt.tree.open != 0 {
				t.Fatalf("%d directory handles left open", tt.tree.open)
			}
		})
	}
}

func TestCancelMidScanReleasesHandles(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reads := 0
	tree := fakeTree{children: map[string][]fakeEntry{"": {dir("a")}, "a": {dir("b")}, "a/b": {file("f")}}}
	tree.onRead = func() {
		if reads++; reads == 3 {
			cancel()
		}
	}
	_, err := tree.scan(ctx, Options{})
	se := assertKind(t, err, Canceled)
	if !errors.Is(se, context.Canceled) || tree.open != 0 {
		t.Fatalf("err=%v open=%d", err, tree.open)
	}
}

func TestUnsupportedDiagnosticsAreBounded(t *testing.T) {
	var entries []fakeEntry
	for i := range 100 {
		entries = append(entries, fakeEntry{name: "l" + strings.Repeat("x", i), mode: fs.ModeSymlink})
	}
	tree := fakeTree{children: map[string][]fakeEntry{"": entries}}
	se := assertKind(t, func() error { _, err := tree.scan(context.Background(), Options{}); return err }(), Unsupported)
	if se.UnsupportedTotal != 100 || len(se.Unsupported) != maxUnsupported {
		t.Fatalf("total %d samples %d", se.UnsupportedTotal, len(se.Unsupported))
	}
}

func TestDistinctUnicodeNamesAreNotCaseDuplicates(t *testing.T) {
	tree := fakeTree{children: map[string][]fakeEntry{"": {file("k"), file("\u212A"), file("ß"), file("SS"), file("é"), file("e\u0301")}}}
	inv, err := tree.scan(context.Background(), Options{})
	if err != nil || len(inv.Files()) != 6 {
		t.Fatalf("distinct names rejected: %v", err)
	}
}

func TestUnsupportedNames(t *testing.T) {
	for _, name := range []string{"a.", "a ", "CON", "con.txt", "Nul", "COM1.log", "LPT¹", "CONIN$", "a\uFFFDb", "a:b", "a\x01"} {
		if unsupportedName(name) == "" {
			t.Errorf("%q accepted", name)
		}
	}
	for _, name := range []string{"COM0x", "console", "a.b", ".git", "日本語", "e\u0301", "COMA"} {
		if r := unsupportedName(name); r != "" {
			t.Errorf("%q rejected: %s", name, r)
		}
	}
}

func TestInvalidRoot(t *testing.T) {
	for _, root := range []string{"", "relative", `C:relative`} {
		_, err := Scan(context.Background(), root, Options{})
		assertKind(t, err, InvalidRoot)
	}
}

func TestZeroInventoryIsNotAListing(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("zero Inventory used as a listing")
		}
	}()
	(&Inventory{}).Empty()
}

// Real-directory tests below use only t.TempDir trees.

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func paths[T any](items []T, get func(T) string) []string {
	var out []string
	for _, i := range items {
		out = append(out, get(i))
	}
	return out
}

func TestRealTreeIsCompleteSortedAndRootRelative(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "照片", "2024", "图.jpg"))
	write(t, filepath.Join(root, "B.txt"))
	write(t, filepath.Join(root, "a.txt"))
	write(t, filepath.Join(root, "e\u0301.txt")) // NFD kept as is
	if err := os.MkdirAll(filepath.Join(root, "empty", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 8, 9, 2, 0, 12, 418_000_000, time.UTC)
	if err := os.Chtimes(filepath.Join(root, "a.txt"), when, when); err != nil {
		t.Fatal(err)
	}
	inv, err := Scan(context.Background(), root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	files := inv.Files()
	wantFiles := []string{"B.txt", "a.txt", "e\u0301.txt", "照片/2024/图.jpg"}
	if got := paths(files, func(f File) string { return f.Path }); !slices.Equal(got, wantFiles) {
		t.Fatalf("files %q", got)
	}
	wantDirs := []string{"empty", "empty/nested", "照片", "照片/2024"}
	if got := paths(inv.Directories(), func(d Directory) string { return d.Path }); !slices.Equal(got, wantDirs) {
		t.Fatalf("dirs %q", got)
	}
	if files[1].Size != 5 || !files[1].Modified.Equal(when) || inv.Root() != root || inv.Empty() {
		t.Fatalf("metadata %+v", files[1])
	}
	again, err := Scan(context.Background(), root, Options{})
	if err != nil || !slices.Equal(again.Files(), files) {
		t.Fatalf("non-deterministic: %v", err)
	}
	files[0].Path = "mutated"
	if inv.Files()[0].Path == "mutated" {
		t.Fatal("Files exposes internal storage")
	}
}

func TestEmptyRootDiffersFromMissingRoot(t *testing.T) {
	root := t.TempDir()
	inv, err := Scan(context.Background(), root, Options{})
	if err != nil || !inv.Empty() {
		t.Fatalf("empty root: %v", err)
	}
	_, err = Scan(context.Background(), filepath.Join(root, "gone"), Options{})
	if se := assertKind(t, err, RootUnavailable); !errors.Is(se, fs.ErrNotExist) {
		t.Fatalf("missing root not ErrNotExist: %v", err)
	}
	write(t, filepath.Join(root, "file.txt"))
	_, err = Scan(context.Background(), filepath.Join(root, "file.txt"), Options{})
	assertKind(t, err, RootUnavailable)
}

func TestLongPathTree(t *testing.T) {
	root := t.TempDir()
	segment := strings.Repeat("长", 60)
	deep := root
	for range 6 {
		deep = filepath.Join(deep, segment)
	}
	if len(deep) <= 260 {
		t.Fatal("fixture not long")
	}
	write(t, filepath.Join(deep, "f.txt"))
	inv, err := Scan(context.Background(), root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := inv.Files(); len(got) != 1 || !strings.HasSuffix(got[0].Path, segment+"/f.txt") || len(inv.Directories()) != 6 {
		t.Fatalf("long path listing %v", got)
	}
}

// countingOpen wraps the real boundary to prove every handle is closed.
type counting struct {
	directory
	open *int
}

func (c counting) OpenChild(name string) (directory, error) {
	d, err := c.directory.OpenChild(name)
	if err != nil {
		return nil, err
	}
	*c.open++
	return counting{d, c.open}, nil
}
func (c counting) Close() error { *c.open--; return c.directory.Close() }

func TestRealCancelAndChangeReleaseHandles(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"a/b/c", "a/d", "e"} {
		write(t, filepath.Join(root, filepath.FromSlash(d), "f"))
	}
	open := 0
	opener := func(p string) (directory, error) {
		d, err := openRootDirectory(p)
		if err != nil {
			return nil, err
		}
		open++
		return counting{d, &open}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := scanWith(ctx, root, Options{}, opener)
	assertKind(t, err, Canceled)

	if open != 0 {
		t.Fatalf("%d handles open after cancel", open)
	}

	// The root listing is read in full first; "e" is then removed before it is
	// opened, so the scan must fail instead of dropping it.
	_, err = scanWith(context.Background(), root, Options{}, func(p string) (directory, error) {
		d, err := opener(p)
		if err != nil {
			return nil, err
		}
		return removeAfterRead{d, filepath.Join(root, "e"), t}, nil
	})
	if se := assertKind(t, err, Enumeration); se.Path != "e" || !errors.Is(se, fs.ErrNotExist) {
		t.Fatalf("vanished dir: %v", err)
	}
	if open != 0 {
		t.Fatalf("%d handles open", open)
	}
}

type removeAfterRead struct {
	directory
	victim string
	t      *testing.T
}

func (r removeAfterRead) ReadDir(n int) ([]fs.DirEntry, error) {
	entries, err := r.directory.ReadDir(n)
	if errors.Is(err, io.EOF) {
		if rmErr := os.RemoveAll(r.victim); rmErr != nil {
			r.t.Fatal(rmErr)
		}
	}
	return entries, err
}

func TestRealDirectoryAcrossReadBatches(t *testing.T) {
	root := t.TempDir()
	for i := range readBatch*2 + 7 {
		write(t, filepath.Join(root, fmt.Sprintf("f%04d", i)))
	}
	inv, err := Scan(context.Background(), root, Options{})
	if err != nil || len(inv.Files()) != readBatch*2+7 {
		t.Fatalf("batches: %v", err)
	}
	_, err = Scan(context.Background(), root, Options{MaxEntries: readBatch*2 + 6})
	assertKind(t, err, LimitExceeded)
}
