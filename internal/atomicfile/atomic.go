package atomicfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"
)

var (
	ErrChanged  = errors.New("file changed while reading")
	ErrTooLarge = errors.New("file exceeds maximum size")
)

type Version struct {
	Data []byte
	Info os.FileInfo
}

func SameVersion(a, b Version) bool {
	if a.Info == nil || b.Info == nil {
		return a.Info == nil && b.Info == nil
	}
	return os.SameFile(a.Info, b.Info) && a.Info.Size() == b.Info.Size() && a.Info.ModTime().Equal(b.Info.ModTime()) && bytes.Equal(a.Data, b.Data)
}

// ReadRegular opens path if it exists and is an ordinary regular file (non-reparse),
// verifying file identity before and after reading. Missing files return a zero Version
// and nil error. If maxBytes > 0 and the file exceeds maxBytes, ErrTooLarge is returned.
func ReadRegular(path string, maxBytes int64) (Version, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Version{}, nil
	}
	if err != nil {
		return Version{}, err
	}
	if !IsRegularFile(info) {
		return Version{}, fmt.Errorf("%s: non-regular file: %w", path, errors.ErrUnsupported)
	}
	f, err := os.Open(path)
	if err != nil {
		return Version{}, err
	}
	opened, err := f.Stat()
	if err == nil && !os.SameFile(info, opened) {
		err = ErrChanged
	}
	var data []byte
	if err == nil {
		if maxBytes > 0 {
			data, err = io.ReadAll(io.LimitReader(f, maxBytes+1))
		} else {
			data, err = io.ReadAll(f)
		}
	}
	after, statErr := f.Stat()
	closeErr := f.Close()
	err = errors.Join(err, statErr, closeErr)
	if err == nil && (!os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime())) {
		err = ErrChanged
	}
	if err != nil {
		return Version{}, err
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return Version{Info: after}, ErrTooLarge
	}
	return Version{Data: data, Info: after}, nil
}

// WriteTemp stages body in a sibling temporary file, syncs it, stats it, and closes it.
func WriteTemp(dir, pattern string, body []byte) (name string, info os.FileInfo, err error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", nil, err
	}
	name = f.Name()
	n, writeErr := f.Write(body)
	if writeErr == nil && n != len(body) {
		writeErr = io.ErrShortWrite
	}
	var syncErr error
	if writeErr == nil {
		syncErr = f.Sync()
	}
	if writeErr == nil && syncErr == nil {
		info, err = f.Stat()
	}
	closeErr := f.Close()
	err = errors.Join(writeErr, syncErr, err, closeErr)
	if err != nil {
		if rmErr := os.Remove(name); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove temp %s: %w", name, rmErr))
		}
		return "", nil, err
	}
	return name, info, nil
}

// Publish writes body to a temp file beside path and invokes replace(temp, path)
// (defaulting to Replace(temp, path, true)). Only staging failures delete the temp;
// ambiguous replacement failures leave the staged file intact.
func Publish(path string, body []byte, tempPattern string, replace func(from, to string) error) (os.FileInfo, error) {
	temp, info, err := WriteTemp(filepath.Dir(path), tempPattern, body)
	if err != nil {
		return nil, err
	}
	if replace == nil {
		replace = func(from, to string) error { return Replace(from, to, true) }
	}
	if err := replace(temp, path); err != nil {
		return nil, err
	}
	return info, nil
}

// DecodeStrictJSONObject decodes exactly one non-null UTF-8 JSON object of type T,
// rejecting unknown fields and trailing data.
func DecodeStrictJSONObject[T any](data []byte) (*T, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("invalid UTF-8 JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value *T
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var tail any
	if value == nil || decoder.Decode(&tail) != io.EOF {
		return nil, errors.New("expected a single JSON object without trailing data")
	}
	return value, nil
}
