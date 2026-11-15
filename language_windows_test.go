package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestLanguageLockedFilePreserved(t *testing.T) {
	data := t.TempDir()
	path := filepath.Join(data, "locale")
	if err := os.WriteFile(path, []byte("en\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = prepareLanguageChange(data, "ja")
	closeErr := windows.CloseHandle(h)
	if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("lost actual read failure: %v", err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "en\n" {
		t.Fatalf("previous language lost: %q %v", got, err)
	}
	if err := prepareLanguageChange(data, "ja"); err != nil {
		t.Fatal("cannot retry after release:", err)
	}
}
