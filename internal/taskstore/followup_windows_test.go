package taskstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"syncmaid/internal/taskconfig"
)

func TestBackupPublicationReadFailureRetainsCause(t *testing.T) {
	s := testStore(t)
	original := fixture(t)
	write(t, s.path, original)
	snap := loaded(t, s)
	ts := tasks(t, snap)
	ts[0].Name = "Edited"
	realRead, realReplace := s.io.read, s.io.replace
	backupPublished := false
	s.io.replace = func(from, to string, exists bool) error {
		err := realReplace(from, to, exists)
		if err == nil && to == s.path+".bak" {
			backupPublished = true
		}
		return err
	}
	s.io.read = func(path string) version {
		if backupPublished && path == s.path+".bak" {
			return version{status: Status{Path: path, State: Unreadable, Err: os.ErrPermission}}
		}
		return realRead(path)
	}
	next, err := snap.Save(ts)
	var committed *CommitError
	if next != nil || !errors.Is(err, ErrChanged) || !errors.Is(err, os.ErrPermission) || errors.As(err, &committed) {
		t.Fatal(next, err)
	}
	requireBytes(t, s.path, original)
	requireBytes(t, s.path+".bak", original)
	requireNoTemps(t, s)
}

func TestFutureVariantRequiresExplicitPreservingRecovery(t *testing.T) {
	// Synthetic future schema: closed trigger discriminator is unsupported, while
	// opaque members carry values that must not be truncated or re-encoded.
	future := []byte("[\r\n" + `{"Name":"Future","SourcePath":"C:\\合成\\Inbox","Trigger":{"kind":"future-v2","Generation":9007199254740993},"Destinations":[],"FutureData":{"n":18446744073709551616,"raw":"\\keep"}}` + "\r\n]")
	for _, mode := range []string{"success", "archive failure", "replace failure"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			good := fixture(t)
			write(t, s.path, future)
			write(t, s.path+".bak", good)
			snap, err := s.Load()
			var blocked *LoadError
			if snap != nil || !errors.As(err, &blocked) || blocked.Primary.State != Unsupported || !errors.Is(err, taskconfig.ErrUnsupported) {
				t.Fatal(snap, err)
			}
			// Failure is not an editable empty configuration; merely loading never
			// substitutes the backup or creates an archival/replacement file.
			if _, err = snap.Save([]taskconfig.Task{}); !errors.Is(err, ErrBlocked) {
				t.Fatal(err)
			}
			requireBytes(t, s.path, future)
			requireBytes(t, s.path+".bak", good)
			files, err := os.ReadDir(filepath.Dir(s.path))
			if err != nil || len(files) != 2 {
				t.Fatal(files, err)
			}
			realCreate, realReplace := s.io.create, s.io.replace
			if mode == "archive failure" {
				s.io.create = func(dir, pattern string) (tempFile, error) {
					f, err := realCreate(dir, pattern)
					if err == nil && strings.Contains(pattern, ".rejected-") {
						return faultFile{f, "partial"}, nil
					}
					return f, err
				}
			}
			publicationAttempted := false
			s.io.replace = func(from, to string, exists bool) error {
				publicationAttempted = true
				if to != s.path {
					t.Fatal("recovery rotated backup")
				}
				// Inspect before replacement: the full unsupported bytes must already
				// be in a closed, readable archive, not just promised for later.
				files, err := os.ReadDir(filepath.Dir(s.path))
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, f := range files {
					if strings.Contains(f.Name(), ".rejected-") {
						requireBytes(t, filepath.Join(filepath.Dir(s.path), f.Name()), future)
						count++
					}
				}
				if count != 1 {
					t.Fatal("archive not complete before replacement", count)
				}
				if mode == "replace failure" {
					return os.ErrPermission
				}
				return realReplace(from, to, exists)
			}
			recovery, err := blocked.Recover()
			if mode == "success" {
				if err != nil || recovery.Snapshot == nil || !publicationAttempted {
					t.Fatal(recovery, err)
				}
				requireBytes(t, s.path, good)
				requireBytes(t, recovery.PreservedPath, future)
			} else {
				if err == nil || recovery.Snapshot != nil {
					t.Fatal(recovery, err)
				}
				requireBytes(t, s.path, future)
				if mode == "replace failure" {
					if !errors.Is(err, os.ErrPermission) || !publicationAttempted {
						t.Fatal(err)
					}
					requireBytes(t, recovery.PreservedPath, future)
				} else {
					if publicationAttempted || recovery.PreservedPath != "" {
						t.Fatal("published after archive failure", recovery)
					}
					files, err := os.ReadDir(filepath.Dir(s.path))
					if err != nil || len(files) != 2 {
						t.Fatal(files, err)
					}
				}
			}
			requireBytes(t, s.path+".bak", good)
			requireNoTemps(t, s)
		})
	}
}

func TestPostCommitBackupReadFailureIsExplicit(t *testing.T) {
	s := testStore(t)
	original := fixture(t)
	write(t, s.path, original)
	snap := loaded(t, s)
	ts := tasks(t, snap)
	ts[0].Name = "Published edit"
	realRead, realReplace := s.io.read, s.io.replace
	primaryPublished := false
	s.io.replace = func(from, to string, exists bool) error {
		err := realReplace(from, to, exists)
		if err == nil && to == s.path {
			primaryPublished = true
		}
		return err
	}
	s.io.read = func(path string) version {
		if primaryPublished && path == s.path+".bak" {
			return version{status: Status{Path: path, State: Unreadable, Err: os.ErrPermission}}
		}
		return realRead(path)
	}
	next, err := snap.Save(ts)
	var committed *CommitError
	if next != nil || !primaryPublished || !errors.As(err, &committed) || !errors.Is(err, os.ErrPermission) {
		t.Fatal(next, err)
	}
	encoded, err := taskconfig.Encode(ts)
	if err != nil {
		t.Fatal(err)
	}
	requireBytes(t, s.path, encoded)
	requireBytes(t, s.path+".bak", original)
	requireNoTemps(t, s)
}
