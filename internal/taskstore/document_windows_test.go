package taskstore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"syncmaid/internal/filefilter"
	"syncmaid/internal/taskconfig"
)

func versionedFixture(t *testing.T) (*Store, *Snapshot, []taskconfig.Task, filefilter.RegexConfirmation) {
	t.Helper()
	s, err := NewVersioned(filepath.Join(t.TempDir(), "tasks-go.json"))
	if err != nil {
		t.Fatal(err)
	}
	ts, err := taskconfig.Decode(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	pattern := "legacy"
	ts[0].Destinations[0].Filters = []taskconfig.Rule{{Kind: "regex", Pattern: &pattern}}
	snap, err := loaded(t, s).Save(ts)
	if err != nil {
		t.Fatal(err)
	}
	specs, err := filefilter.Proposals(ts[0], ts[0].Destinations[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := filefilter.PrepareRegexPreview(specs[0], []string{"sample"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Confirm()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := r.Confirmation()
	if err != nil {
		t.Fatal(err)
	}
	return s, snap, ts, saved
}
func TestVersionedDocumentAtomicSaveAndLegacyIsolation(t *testing.T) {
	s, snap, ts, review := versionedFixture(t)
	before := read(t, s.path)
	failure := errors.New("publish denied")
	replace := s.io.replace
	s.io.replace = func(from, to string, exists bool) error {
		if to == s.path {
			return failure
		}
		return replace(from, to, exists)
	}
	if _, err := snap.SaveConfirmations(ts, []filefilter.RegexConfirmation{review}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	requireBytes(t, s.path, before)
	s.io.replace = replace
	snap = loaded(t, s)
	snap, err := snap.SaveConfirmations(ts, []filefilter.RegexConfirmation{review})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Confirmations()) != 1 {
		t.Fatal("confirmation missing")
	}
	old, err := New(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = old.Load(); err == nil {
		t.Fatal("legacy reader accepted new semantics")
	}
	// Ordinary task saves keep the receipt in the same atomic document; the
	// application will reject it as stale after any bound task change.
	ts[0].Name = "edited"
	next, err := snap.Save(ts)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Confirmations()) != 1 {
		t.Fatal("ordinary save silently discarded metadata")
	}
}
func TestVersionedRecoveryAlwaysClearsConfirmations(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "versioned", true: "old array"}[legacy], func(t *testing.T) {
			s, snap, ts, review := versionedFixture(t)
			snap, err := snap.SaveConfirmations(ts, []filefilter.RegexConfirmation{review})
			if err != nil {
				t.Fatal(err)
			}
			backup := read(t, s.path)
			if legacy {
				backup, err = taskconfig.Encode(ts)
				if err != nil {
					t.Fatal(err)
				}
			}
			write(t, s.path+".bak", backup)
			bad := []byte("{broken source}")
			write(t, s.path, bad)
			_, err = s.Load()
			var load *LoadError
			if !errors.As(err, &load) {
				t.Fatal("automatic recovery", err)
			}
			result, err := load.Recover()
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Snapshot.Confirmations()) != 0 {
				t.Fatal("backup resurrected permission")
			}
			requireBytes(t, result.PreservedPath, bad)
			requireBytes(t, s.path+".bak", backup)
			if _, err := os.Stat(result.PreservedPath); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestVersionedUnknownAndDuplicateMembersBlockWriting(t *testing.T) {
	s, _, _, _ := versionedFixture(t)
	good := read(t, s.path)
	for _, bad := range [][]byte{
		bytes.Replace(good, []byte(`"Version": 1`), []byte(`"Version": 99`), 1),
		bytes.Replace(good, []byte(`"Version": 1`), []byte(`"Version": 1,"Version": 1`), 1),
		bytes.Replace(good, []byte(`"Format":`), []byte(`"Future":true,"Format":`), 1),
	} {
		write(t, s.path, bad)
		if snap, err := s.Load(); err == nil || snap != nil {
			t.Fatal("unsupported document got writable snapshot")
		}
		requireBytes(t, s.path, bad)
	}
}

func TestVersionedOversizedSourceCannotRecoverFromTruncatedArchive(t *testing.T) {
	s, _, _, _ := versionedFixture(t)
	write(t, s.path+".bak", read(t, s.path))
	bad := bytes.Repeat([]byte("x"), (8<<20)+100)
	write(t, s.path, bad)
	_, err := s.Load()
	var load *LoadError
	if !errors.As(err, &load) {
		t.Fatal(err)
	}
	result, err := load.Recover()
	if !errors.Is(err, ErrBlocked) || result.PreservedPath != "" {
		t.Fatal("partial archive permitted replacement", result, err)
	}
	requireBytes(t, s.path, bad)
}
