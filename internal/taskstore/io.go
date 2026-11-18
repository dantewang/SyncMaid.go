package taskstore

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"syncmaid/internal/atomicfile"
)

// Only genuine I/O boundaries are injectable, privately for fault tests.
type tempFile interface {
	io.Writer
	Sync() error
	Close() error
	Name() string
}
type operations struct {
	read    func(string) version
	create  func(string, string) (tempFile, error)
	replace func(string, string, bool) error
	remove  func(string) error
}

func realOperations() operations {
	return operations{
		read:    readVersion,
		create:  func(dir, pattern string) (tempFile, error) { return os.CreateTemp(dir, pattern) },
		replace: atomicfile.Replace, remove: os.Remove,
	}
}
func (s *Store) stage(data []byte, suffix string) (name string, err error) {
	f, err := s.io.create(filepath.Dir(s.path), filepath.Base(s.path)+suffix)
	if err != nil {
		return "", fmt.Errorf("create task temp: %w", err)
	}
	name = f.Name()
	n, writeErr := f.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = io.ErrShortWrite
	}
	var syncErr error
	if writeErr == nil {
		syncErr = f.Sync()
	}
	closeErr := f.Close()
	err = errors.Join(writeErr, syncErr, closeErr)
	if err != nil {
		s.cleanup(name, &err)
		return "", fmt.Errorf("stage tasks: %w", err)
	}
	return name, nil
}
func (s *Store) cleanup(name string, err *error) {
	if removeErr := s.io.remove(name); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		*err = errors.Join(*err, fmt.Errorf("remove owned temp %s: %w", name, removeErr))
	}
}
