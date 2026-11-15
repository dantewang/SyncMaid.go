// Package taskstore atomically stores a caller-selected task file. The app uses
// its Go-specific file in owned Data, never the legacy Rust tasks.json.
// Successful decoding and saving never authorize task execution.
//
// A Store serializes its own operations. Snapshots detect observed changes to
// primary and backup before writing, but checks plus rename are NOT cross-process
// CAS. A stable parent and a single cooperating writer are required; other editors
// must not write during Save/Recover. This slice does not establish that protocol.
// Different Store values do not share a mutex, even for the same path.
package taskstore

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"

	"syncmaid/internal/filefilter"
	"syncmaid/internal/taskconfig"
)

var (
	ErrBlocked = errors.New("task file cannot be edited from this load")
	ErrChanged = errors.New("task file or backup changed; reload required")
)

type State string

const (
	Missing     State = "missing"
	Valid       State = "valid"
	Corrupt     State = "corrupt"
	Unsupported State = "unsupported"
	Unreadable  State = "unreadable"
)

// Status exposes the actual error (including permission errors via errors.Is).
// Unsupported includes unknown JSON types and non-regular/symlink paths.
// The state is unified, but their causes remain taskconfig.ErrUnsupported and
// errors.ErrUnsupported respectively; inspect State or the relevant cause.
type Status struct {
	Path  string
	State State
	Err   error
}
type version struct {
	status Status
	data   []byte
	info   os.FileInfo
}

type Store struct {
	versioned bool
	path      string
	mu        sync.Mutex
	io        operations
}

// New does not create directories or files. This adapter currently supports
// Windows regular files only; ACL/hardlink/network semantics are not promised.
func New(path string) (*Store, error) {
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("task store: %w", errors.ErrUnsupported)
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("task store requires an absolute path")
	}
	return &Store{path: path, io: realOperations()}, nil
}

// Snapshot is an immutable save basis issued only by a successful Load/Save or
// explicit Recover. Its zero value cannot save. One Store owns its snapshots.
// Tasks returns an independent copy, preserving generated legacy IDs.
type Snapshot struct {
	reviews         []filefilter.RegexConfirmation
	store           *Store
	primary, backup version
	canonical       []byte
}

func (s *Snapshot) Tasks() ([]taskconfig.Task, error) {
	if s == nil || s.store == nil {
		return nil, ErrBlocked
	}
	return taskconfig.Decode(s.canonical)
}

// LoadError supplies diagnostics without a writable snapshot. A good backup is
// not silently substituted; Recover is a separate explicit operation.
// An invalid/unreadable backup blocks editing even when primary is valid: a
// routine save must not silently rotate away unexamined backup data. Explicitly
// inspect and preserve/resolve that backup, then Load again. This is stricter
// than legacy Rust's primary-first fallback; Recover is for a bad/missing primary
// with a valid backup, not automatic repair of an invalid backup.
type LoadError struct {
	Primary, Backup Status
	store           *Store
	primary, backup version
}

func (e *LoadError) Error() string {
	return fmt.Sprintf("%v: primary %s (%v), backup %s (%v)", ErrBlocked, e.Primary.State, e.Primary.Err, e.Backup.State, e.Backup.Err)
}
func (e *LoadError) Unwrap() error { return errors.Join(ErrBlocked, e.Primary.Err, e.Backup.Err) }

func (s *Store) Load() (*Snapshot, error) {
	if s == nil || s.io.read == nil {
		return nil, ErrBlocked
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}
func (s *Store) load() (*Snapshot, error) {
	p := s.io.read(s.path)
	b := s.io.read(s.path + ".bak")
	if (p.status.State == Valid && (b.status.State == Valid || b.status.State == Missing)) || (p.status.State == Missing && b.status.State == Missing) {
		tasks := []taskconfig.Task{}
		var reviews []filefilter.RegexConfirmation
		if p.status.State == Valid {
			var err error
			tasks, reviews, err = decodeDocument(p.data, s.versioned)
			if err != nil {
				return nil, err
			}
		}
		canonical, err := taskconfig.Encode(tasks)
		if err != nil {
			return nil, err
		}
		return &Snapshot{store: s, primary: p, backup: b, canonical: canonical, reviews: reviews}, nil
	}
	return nil, &LoadError{Primary: p.status, Backup: b.status, store: s, primary: p, backup: b}
}

func readVersion(path string) version {
	return readVersionFormat(path, false)
}
func readVersionFormat(path string, versioned bool) version {
	v := version{status: Status{Path: path, State: Unreadable}}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		v.status.State = Missing
		return v
	}
	if err != nil {
		v.status.Err = err
		return v
	}
	if !info.Mode().IsRegular() {
		v.status.State = Unsupported
		v.status.Err = fmt.Errorf("%s: non-regular file: %w", path, errors.ErrUnsupported)
		return v
	}
	f, err := os.Open(path)
	if err != nil {
		v.status.Err = err
		return v
	}
	opened, err := f.Stat()
	if err == nil && !os.SameFile(info, opened) {
		err = ErrChanged
	}
	if err == nil {
		if versioned {
			v.data, err = io.ReadAll(io.LimitReader(f, (8<<20)+1))
		} else {
			v.data, err = io.ReadAll(f)
		}
	}
	after, statErr := f.Stat()
	closeErr := f.Close()
	err = errors.Join(err, statErr, closeErr)
	if err == nil && (!os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime())) {
		err = ErrChanged
	}
	if err != nil {
		v.data = nil
		v.status.Err = err
		return v
	}
	v.info = after
	_, _, err = decodeDocument(v.data, versioned)
	v.status.State = Valid
	if err != nil {
		v.status.State = Corrupt
		if errors.Is(err, taskconfig.ErrUnsupported) {
			v.status.State = Unsupported
		}
		v.status.Err = err
	}
	return v
}
func same(a, b version) bool {
	if a.status.State == Missing || b.status.State == Missing {
		return a.status.State == Missing && b.status.State == Missing
	}
	if a.info == nil || b.info == nil {
		return false
	}
	return os.SameFile(a.info, b.info) && a.info.Size() == b.info.Size() && a.info.ModTime().Equal(b.info.ModTime()) && bytes.Equal(a.data, b.data)
}
func (s *Store) check(p, b version) error {
	cp, cb := s.io.read(s.path), s.io.read(s.path+".bak")
	if !same(p, cp) || !same(b, cb) {
		return errors.Join(ErrChanged, cp.status.Err, cb.status.Err)
	}
	return nil
}

// Save encodes before opening any temp. Unchanged semantic content keeps the
// original bytes (including legacy whitespace/key order/default omissions).
// A changed save uses taskconfig's semantic format, not byte-faithful Rust JSON.
// The backup may advance to the still-live primary if primary replacement fails;
// primary is never moved away, and a complete usable backup remains available.
func (snap *Snapshot) Save(tasks []taskconfig.Task) (next *Snapshot, err error) {
	if snap == nil {
		return nil, ErrBlocked
	}
	return snap.save(tasks, snap.reviews)
}
func (snap *Snapshot) save(tasks []taskconfig.Task, reviews []filefilter.RegexConfirmation) (next *Snapshot, err error) {
	if snap == nil || snap.store == nil {
		return nil, ErrBlocked
	}
	s := snap.store
	s.mu.Lock()
	defer s.mu.Unlock()
	encoded, err := taskconfig.Encode(tasks)
	if err != nil {
		return nil, err
	}
	if err = s.check(snap.primary, snap.backup); err != nil {
		return nil, err
	}
	if snap.primary.status.State == Valid && bytes.Equal(encoded, snap.canonical) && reflect.DeepEqual(reviews, snap.reviews) {
		return snap, nil
	}
	if s.versioned {
		encoded, err = encodeDocument(encoded, reviews)
		if err != nil {
			return nil, err
		}
	}
	temp, err := s.stage(encoded, ".tmp-")
	if err != nil {
		return nil, err
	}
	defer s.cleanup(temp, &err)
	return s.commit(snap, temp, encoded)
}

func (s *Store) commit(snap *Snapshot, temp string, encoded []byte) (next *Snapshot, err error) {
	backup := snap.backup
	if snap.primary.status.State == Valid {
		bt, stageErr := s.stage(snap.primary.data, ".tmp-")
		if stageErr != nil {
			return nil, stageErr
		}
		defer s.cleanup(bt, &err)
		if err = s.check(snap.primary, snap.backup); err != nil {
			return nil, err
		}
		if err = s.io.replace(bt, s.path+".bak", snap.backup.status.State != Missing); err != nil {
			return nil, fmt.Errorf("replace backup: %w", err)
		}
		backup = s.io.read(s.path + ".bak")
		if backup.status.State != Valid || !bytes.Equal(backup.data, snap.primary.data) {
			return nil, errors.Join(ErrChanged, backup.status.Err)
		}
	}
	if err = s.check(snap.primary, backup); err != nil {
		return nil, err
	}
	if err = s.io.replace(temp, s.path, snap.primary.status.State != Missing); err != nil {
		return nil, fmt.Errorf("replace tasks: %w", err)
	}
	next, err = s.load()
	if err != nil {
		return nil, &CommitError{Err: err}
	}
	if !bytes.Equal(next.primary.data, encoded) || !same(backup, next.backup) {
		return nil, &CommitError{Err: ErrChanged}
	}
	return next, nil
}

// CommitError means replacement succeeded but a fresh snapshot could not be
// loaded. Do not blindly retry; inspect/reload. It is not a failed pre-commit.
type CommitError struct{ Err error }

func (e *CommitError) Error() string {
	return "tasks replacement completed; reload failed: " + e.Err.Error()
}
func (e *CommitError) Unwrap() error { return e.Err }

// Recovery returns the archival path as soon as bad source bytes were preserved,
// even if replacement subsequently fails. Backup is never rotated by recovery.
type Recovery struct {
	Snapshot      *Snapshot
	PreservedPath string
}

func (e *LoadError) Recover() (result Recovery, err error) {
	if e == nil || e.store == nil || e.backup.status.State != Valid || !(e.primary.status.State == Missing || e.primary.info != nil) {
		return result, ErrBlocked
	}
	// A bounded read of an oversized document is diagnostic only. Never archive
	// that prefix and replace the full original as if all bytes were preserved.
	if e.primary.info != nil && int64(len(e.primary.data)) != e.primary.info.Size() {
		return result, ErrBlocked
	}
	s := e.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.check(e.primary, e.backup); err != nil {
		return result, err
	}
	recovered := e.backup.data
	if s.versioned {
		tasks, _, decodeErr := decodeDocument(recovered, true)
		if decodeErr != nil {
			return result, decodeErr
		}
		body, encodeErr := taskconfig.Encode(tasks)
		if encodeErr != nil {
			return result, encodeErr
		}
		recovered, err = encodeDocument(body, nil)
		if err != nil {
			return result, err
		}
	}
	temp, err := s.stage(recovered, ".tmp-")
	if err != nil {
		return result, err
	}
	defer s.cleanup(temp, &err)
	if e.primary.info != nil {
		result.PreservedPath, err = s.stage(e.primary.data, ".rejected-")
		if err != nil {
			return result, err
		}
	}
	if err = s.check(e.primary, e.backup); err != nil {
		return result, err
	}
	if err = s.io.replace(temp, s.path, e.primary.status.State != Missing); err != nil {
		return result, fmt.Errorf("recover tasks: %w", err)
	}
	result.Snapshot, err = s.load()
	if err == nil && (!bytes.Equal(result.Snapshot.primary.data, recovered) || !same(e.backup, result.Snapshot.backup)) {
		err = ErrChanged
	}
	if err != nil {
		result.Snapshot = nil
		err = &CommitError{Err: err}
	}
	return result, err
}
