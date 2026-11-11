package taskstore

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"syncmaid/internal/taskconfig"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := New(filepath.Join(t.TempDir(), "tasks.json"))
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if e := os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
}
func read(t *testing.T, path string) []byte {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func fixture(t *testing.T) []byte {
	t.Helper()
	return read(t, "../taskconfig/testdata/nested-filters.json")
}
func loaded(t *testing.T, s *Store) *Snapshot {
	t.Helper()
	snap, e := s.Load()
	if e != nil {
		t.Fatal(e)
	}
	return snap
}
func tasks(t *testing.T, snap *Snapshot) []taskconfig.Task {
	t.Helper()
	ts, e := snap.Tasks()
	if e != nil {
		t.Fatal(e)
	}
	return ts
}
func requireBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	if got := read(t, path); !bytes.Equal(got, want) {
		t.Fatalf("%s changed:\n%s", path, got)
	}
}
func requireNoTemps(t *testing.T, s *Store) {
	t.Helper()
	files, e := os.ReadDir(filepath.Dir(s.path))
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range files {
		if strings.Contains(f.Name(), ".tmp-") {
			t.Fatalf("leaked temp %s", f.Name())
		}
	}
}

func TestCreateSaveBackupAndNoop(t *testing.T) {
	s := testStore(t)
	snap := loaded(t, s)
	if len(tasks(t, snap)) != 0 {
		t.Fatal("new store not empty")
	}
	ts, e := taskconfig.Decode(fixture(t))
	if e != nil {
		t.Fatal(e)
	}
	snap, e = snap.Save(ts)
	if e != nil {
		t.Fatal(e)
	}
	first := read(t, s.path)
	if _, e = os.Stat(s.path + ".bak"); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("first save created backup", e)
	}
	got := tasks(t, snap)
	if got[0].SourcePath != ts[0].SourcePath || got[0].Destinations[0].Target.Path != ts[0].Destinations[0].Target.Path {
		t.Fatal("paths rewritten")
	}
	if !bytes.Contains(first, []byte("9007199254740993")) {
		t.Fatal("big integer lost")
	}
	got[0].Name = "Changed"
	snap, e = snap.Save(got)
	if e != nil {
		t.Fatal(e)
	}
	requireBytes(t, s.path+".bak", first)
	second := read(t, s.path)
	// No-op does not rotate backup or touch file bytes/identity/mtime.
	before := readVersion(s.path)
	s.io.create = func(string, string) (tempFile, error) { t.Fatal("noop created a temp"); return nil, nil }
	if _, e = snap.Save(tasks(t, snap)); e != nil {
		t.Fatal(e)
	}
	if !same(before, readVersion(s.path)) {
		t.Fatal("noop changed metadata")
	}
	requireBytes(t, s.path, second)
	requireBytes(t, s.path+".bak", first)
	requireNoTemps(t, s)
}
func TestLoadDoesNotNormalizeAndNoopPreservesLegacyBytes(t *testing.T) {
	s := testStore(t)
	raw := []byte("[\r\n" + `{"Name":"Old","SourcePath":"C:\\raw\\..\\src","Trigger":{"kind":"watch"},"Destinations":[],"Extra":9007199254740993}` + "\r\n]")
	write(t, s.path, raw)
	snap := loaded(t, s)
	requireBytes(t, s.path, raw)
	if _, e := snap.Save(tasks(t, snap)); e != nil {
		t.Fatal(e)
	}
	requireBytes(t, s.path, raw)
	if _, e := os.Stat(s.path + ".bak"); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
	ts := tasks(t, snap)
	ts[0].Name = "Edited"
	if _, e := snap.Save(ts); e != nil {
		t.Fatal(e)
	}
	requireBytes(t, s.path+".bak", raw)
	if bytes.Equal(raw, read(t, s.path)) {
		t.Fatal("real edit not encoded")
	}
	requireNoTemps(t, s)
}
func TestBlockedLoadsHaveNoWritableSnapshot(t *testing.T) {
	cases := []struct {
		name            string
		primary, backup []byte
		want            State
	}{
		{"blank", []byte(" \r\n"), nil, Corrupt},
		{"bad JSON", []byte("[broken"), nil, Corrupt},
		{"bad UTF8", []byte{0xff}, nil, Corrupt},
		{"unknown type", []byte(`[{"Name":"T","SourcePath":"C:\\src","Destinations":[],"Trigger":{"kind":"future"}}]`), nil, Unsupported},
		{"backup only", nil, []byte("[]"), Missing},
		{"bad primary good backup", []byte("bad"), []byte("[]"), Corrupt},
		{"both bad", []byte("bad"), []byte("bad backup"), Corrupt},
		{"valid primary bad backup", []byte("[]"), []byte("bad backup"), Valid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := testStore(t)
			if c.primary != nil {
				write(t, s.path, c.primary)
			}
			if c.backup != nil {
				write(t, s.path+".bak", c.backup)
			}
			snap, e := s.Load()
			var le *LoadError
			if snap != nil || !errors.As(e, &le) || !errors.Is(e, ErrBlocked) || le.Primary.State != c.want {
				t.Fatal(snap, e)
			}
			if c.primary != nil {
				requireBytes(t, s.path, c.primary)
			}
			if c.backup != nil {
				requireBytes(t, s.path+".bak", c.backup)
			}
			requireNoTemps(t, s)
			if c.want == Unsupported && !errors.Is(e, taskconfig.ErrUnsupported) {
				t.Fatal("lost unsupported cause", e)
			}
		})
	}
	var zero Snapshot
	if _, e := zero.Save([]taskconfig.Task{}); !errors.Is(e, ErrBlocked) {
		t.Fatal(e)
	}
}
func TestDirectoryAndPermissionFailuresBlock(t *testing.T) {
	for _, where := range []string{"primary", "backup"} {
		t.Run(where, func(t *testing.T) {
			s := testStore(t)
			path := s.path
			if where == "backup" {
				write(t, s.path, []byte("[]"))
				path += ".bak"
			}
			if e := os.Mkdir(path, 0700); e != nil {
				t.Fatal(e)
			}
			snap, e := s.Load()
			var le *LoadError
			if snap != nil || !errors.As(e, &le) || !errors.Is(e, errors.ErrUnsupported) {
				t.Fatal(e)
			}
		})
	}
	s := testStore(t)
	write(t, s.path, []byte("[]"))
	real := s.io.read
	s.io.read = func(path string) version {
		if path == s.path {
			return version{status: Status{Path: path, State: Unreadable, Err: os.ErrPermission}}
		}
		return real(path)
	}
	snap, e := s.Load()
	var le *LoadError
	if snap != nil || !errors.As(e, &le) || le.Primary.State != Unreadable || !errors.Is(e, os.ErrPermission) {
		t.Fatal(e)
	}
	if _, e = le.Recover(); !errors.Is(e, ErrBlocked) {
		t.Fatal("unreadable primary recovered", e)
	}
}
func TestExplicitRecoveryPreservesBadBytesAndBackup(t *testing.T) {
	for _, bad := range [][]byte{[]byte("bad original"), nil} {
		t.Run(string(bad), func(t *testing.T) {
			s := testStore(t)
			good := fixture(t)
			if bad != nil {
				write(t, s.path, bad)
			}
			write(t, s.path+".bak", good)
			snap, e := s.Load()
			if snap != nil {
				t.Fatal("silent recovery")
			}
			var le *LoadError
			if !errors.As(e, &le) {
				t.Fatal(e)
			}
			r, e := le.Recover()
			if e != nil || r.Snapshot == nil {
				t.Fatal(r, e)
			}
			requireBytes(t, s.path, good)
			requireBytes(t, s.path+".bak", good)
			if bad != nil {
				if r.PreservedPath == "" {
					t.Fatal("bad source lost")
				}
				requireBytes(t, r.PreservedPath, bad)
			} else if r.PreservedPath != "" {
				t.Fatal(r)
			}
			ts := tasks(t, r.Snapshot)
			if ts[0].Destinations[0].Target.Path != `\\host\share\合成` {
				t.Fatal(ts)
			}
			requireNoTemps(t, s)
		})
	}
}
func TestRecoveryFailureStillPreservesSourceAndBackup(t *testing.T) {
	s := testStore(t)
	bad, good := []byte("bad original"), fixture(t)
	write(t, s.path, bad)
	write(t, s.path+".bak", good)
	_, e := s.Load()
	var le *LoadError
	if !errors.As(e, &le) {
		t.Fatal(e)
	}
	s.io.replace = func(string, string, bool) error { return io.ErrClosedPipe }
	r, e := le.Recover()
	if !errors.Is(e, io.ErrClosedPipe) || r.Snapshot != nil || r.PreservedPath == "" {
		t.Fatal(r, e)
	}
	requireBytes(t, r.PreservedPath, bad)
	requireBytes(t, s.path, bad)
	requireBytes(t, s.path+".bak", good)
	requireNoTemps(t, s)
}

func TestExternalChangesAndStaleSnapshots(t *testing.T) {
	for _, where := range []string{"primary", "backup", "primary identity"} {
		t.Run(where, func(t *testing.T) {
			s := testStore(t)
			original := fixture(t)
			write(t, s.path, original)
			write(t, s.path+".bak", []byte("[]"))
			snap := loaded(t, s)
			path := s.path
			if where == "backup" {
				path += ".bak"
			}
			if where == "primary identity" {
				other := path + ".other"
				write(t, other, original)
				if e := nativeReplace(other, path, true); e != nil {
					t.Fatal(e)
				}
			} else {
				write(t, path, []byte("[ ]"))
			}
			external := read(t, path)
			ts := tasks(t, snap)
			ts[0].Name = "Changed"
			if _, e := snap.Save(ts); !errors.Is(e, ErrChanged) {
				t.Fatal(e)
			}
			requireBytes(t, path, external)
			requireNoTemps(t, s)
		})
	}
	s := testStore(t)
	write(t, s.path, fixture(t))
	a, b := loaded(t, s), loaded(t, s)
	ts := tasks(t, a)
	ts[0].Name = "New"
	if _, e := a.Save(ts); e != nil {
		t.Fatal(e)
	}
	if _, e := b.Save(tasks(t, b)); !errors.Is(e, ErrChanged) {
		t.Fatal("stale snapshot saved", e)
	}
}
func TestFirstCreateRefusesLateDestination(t *testing.T) {
	s := testStore(t)
	snap := loaded(t, s)
	real := s.io.replace
	s.io.replace = func(from, to string, exists bool) error {
		if exists {
			t.Fatal("first save used overwrite")
		}
		write(t, to, []byte("external"))
		return real(from, to, exists)
	}
	if _, e := snap.Save([]taskconfig.Task{}); e == nil {
		t.Fatal("late destination overwritten")
	}
	requireBytes(t, s.path, []byte("external"))
	requireNoTemps(t, s)
}

type faultFile struct {
	tempFile
	mode string
}

func (f faultFile) Write(b []byte) (int, error) {
	if f.mode == "partial" {
		n, e := f.tempFile.Write(b[:len(b)/2])
		if e != nil {
			return n, e
		}
		return n, io.ErrUnexpectedEOF
	}
	if f.mode == "short" {
		return len(b) / 2, nil
	}
	return f.tempFile.Write(b)
}
func (f faultFile) Sync() error {
	if f.mode == "sync" {
		return io.ErrUnexpectedEOF
	}
	return f.tempFile.Sync()
}
func (f faultFile) Close() error {
	e := f.tempFile.Close()
	if f.mode == "close" {
		return errors.Join(e, io.ErrUnexpectedEOF)
	}
	return e
}
func TestStageAndReplacementFaultsKeepRecoverableFiles(t *testing.T) {
	for _, step := range []int{1, 2} {
		for _, mode := range []string{"create", "partial", "short", "sync", "close"} {
			t.Run(mode+string(rune('0'+step)), func(t *testing.T) {
				s := testStore(t)
				original := fixture(t)
				oldBackup := []byte("[]")
				write(t, s.path, original)
				write(t, s.path+".bak", oldBackup)
				snap := loaded(t, s)
				ts := tasks(t, snap)
				ts[0].Name = "Changed"
				real := s.io.create
				count := 0
				s.io.create = func(d, p string) (tempFile, error) {
					count++
					if count == step && mode == "create" {
						return nil, io.ErrUnexpectedEOF
					}
					f, e := real(d, p)
					if e != nil {
						return nil, e
					}
					if count == step {
						return faultFile{f, mode}, nil
					}
					return f, nil
				}
				if _, e := snap.Save(ts); e == nil {
					t.Fatal("injected failure accepted")
				}
				requireBytes(t, s.path, original)
				requireBytes(t, s.path+".bak", oldBackup)
				requireNoTemps(t, s)
			})
		}
	}
	for _, target := range []string{"primary", "backup"} {
		t.Run("replace "+target, func(t *testing.T) {
			s := testStore(t)
			original := fixture(t)
			oldBackup := []byte("[]")
			write(t, s.path, original)
			write(t, s.path+".bak", oldBackup)
			snap := loaded(t, s)
			ts := tasks(t, snap)
			ts[0].Name = "Changed"
			real := s.io.replace
			s.io.replace = func(from, to string, exists bool) error {
				requireBytes(t, s.path, original)
				if (target == "primary" && to == s.path) || (target == "backup" && to == s.path+".bak") {
					return io.ErrClosedPipe
				}
				return real(from, to, exists)
			}
			if _, e := snap.Save(ts); !errors.Is(e, io.ErrClosedPipe) {
				t.Fatal(e)
			}
			requireBytes(t, s.path, original)
			want := oldBackup
			if target == "primary" {
				want = original
			}
			requireBytes(t, s.path+".bak", want)
			requireNoTemps(t, s)
		})
	}
}
func TestInvalidEncodingDoesNotCreateTempOrChangeFiles(t *testing.T) {
	s := testStore(t)
	write(t, s.path, fixture(t))
	snap := loaded(t, s)
	original := read(t, s.path)
	s.io.create = func(string, string) (tempFile, error) { t.Fatal("encoding failure opened temp"); return nil, nil }
	if _, e := snap.Save(nil); e == nil {
		t.Fatal("nil saved")
	}
	requireBytes(t, s.path, original)
}

func TestModificationDuringStagingIsRejected(t *testing.T) {
	s := testStore(t)
	write(t, s.path, fixture(t))
	write(t, s.path+".bak", []byte("[]"))
	snap := loaded(t, s)
	ts := tasks(t, snap)
	ts[0].Name = "Changed"
	real := s.io.create
	count := 0
	s.io.create = func(d, p string) (tempFile, error) {
		count++
		if count == 1 {
			write(t, s.path, []byte("external invalid JSON"))
		}
		return real(d, p)
	}
	if _, e := snap.Save(ts); !errors.Is(e, ErrChanged) {
		t.Fatal(e)
	}
	requireBytes(t, s.path, []byte("external invalid JSON"))
	requireBytes(t, s.path+".bak", []byte("[]"))
	requireNoTemps(t, s)
}
func TestRecoveryBasisChangedIsRejected(t *testing.T) {
	s := testStore(t)
	write(t, s.path, []byte("bad"))
	write(t, s.path+".bak", fixture(t))
	_, e := s.Load()
	var le *LoadError
	if !errors.As(e, &le) {
		t.Fatal(e)
	}
	write(t, s.path+".bak", []byte("[]"))
	if _, e := le.Recover(); !errors.Is(e, ErrChanged) {
		t.Fatal(e)
	}
	requireBytes(t, s.path, []byte("bad"))
	requireNoTemps(t, s)
}
func TestReadonlyPrimaryCannotBeReplaced(t *testing.T) {
	s := testStore(t)
	original := fixture(t)
	write(t, s.path, original)
	snap := loaded(t, s)
	ts := tasks(t, snap)
	ts[0].Name = "Changed"
	if e := os.Chmod(s.path, 0400); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.Chmod(s.path, 0600) })
	if _, e := snap.Save(ts); !errors.Is(e, os.ErrPermission) {
		t.Fatal("expected native readonly permission failure", e)
	}
	requireBytes(t, s.path, original)
	requireNoTemps(t, s)
}

func TestMissingParentIsNotCreated(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "absent")
	s, e := New(filepath.Join(parent, "tasks.json"))
	if e != nil {
		t.Fatal(e)
	}
	snap := loaded(t, s)
	if _, e = snap.Save([]taskconfig.Task{}); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
	if _, e = os.Stat(parent); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("parent implicitly created", e)
	}
}
func TestRecoveryArchiveFailureLeavesBothInputs(t *testing.T) {
	s := testStore(t)
	bad, good := []byte("bad original"), fixture(t)
	write(t, s.path, bad)
	write(t, s.path+".bak", good)
	_, e := s.Load()
	var le *LoadError
	if !errors.As(e, &le) {
		t.Fatal(e)
	}
	real := s.io.create
	s.io.create = func(dir, pattern string) (tempFile, error) {
		f, e := real(dir, pattern)
		if e == nil && strings.Contains(pattern, ".rejected-") {
			return faultFile{f, "partial"}, nil
		}
		return f, e
	}
	r, e := le.Recover()
	if e == nil || r.Snapshot != nil {
		t.Fatal(r, e)
	}
	requireBytes(t, s.path, bad)
	requireBytes(t, s.path+".bak", good)
	requireNoTemps(t, s)
	files, e := os.ReadDir(filepath.Dir(s.path))
	if e != nil || len(files) != 2 {
		t.Fatal("partial archive retained", files, e)
	}
}
func TestCleanupFailureIsReportedWithoutDeletingUnownedFiles(t *testing.T) {
	s := testStore(t)
	write(t, s.path, fixture(t))
	snap := loaded(t, s)
	ts := tasks(t, snap)
	ts[0].Name = "Changed"
	unowned := s.path + ".tmp-user-owned"
	write(t, unowned, []byte("keep"))
	s.io.replace = func(string, string, bool) error { return io.ErrClosedPipe }
	cleanupErr := errors.New("injected cleanup failure")
	var retained []string
	s.io.remove = func(path string) error { retained = append(retained, path); return cleanupErr }
	_, e := snap.Save(ts)
	if !errors.Is(e, cleanupErr) || !errors.Is(e, io.ErrClosedPipe) {
		t.Fatal("cleanup swallowed original or cleanup error", e)
	}
	requireBytes(t, unowned, []byte("keep"))
	for _, path := range retained {
		if path == unowned {
			t.Fatal("removed foreign temp")
		}
		if e := os.Remove(path); e != nil && !errors.Is(e, os.ErrNotExist) {
			t.Fatal(e)
		}
	}
}
func TestPostCommitReadErrorIsExplicit(t *testing.T) {
	s := testStore(t)
	write(t, s.path, fixture(t))
	snap := loaded(t, s)
	ts := tasks(t, snap)
	ts[0].Name = "Changed"
	realRead, realReplace := s.io.read, s.io.replace
	committed := false
	s.io.replace = func(from, to string, exists bool) error {
		e := realReplace(from, to, exists)
		if e == nil && to == s.path {
			committed = true
		}
		return e
	}
	s.io.read = func(path string) version {
		if committed && path == s.path {
			return version{status: Status{Path: path, State: Unreadable, Err: os.ErrPermission}}
		}
		return realRead(path)
	}
	_, e := snap.Save(ts)
	var ce *CommitError
	if !errors.As(e, &ce) || !errors.Is(e, os.ErrPermission) {
		t.Fatal(e)
	}
	want, e := taskconfig.Encode(ts)
	if e != nil {
		t.Fatal(e)
	}
	requireBytes(t, s.path, want)
	requireNoTemps(t, s)
}
