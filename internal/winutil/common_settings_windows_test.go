package winutil

import (
	"golang.org/x/sys/windows"
	"os"
	"testing"
)

func TestCommonSettingsLockedSaveKeepsPreviousValue(t *testing.T) {
	s := LoadCommonSettings(t.TempDir())
	if err := s.Save(CommonSettings{}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(s.path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	saveErr := s.Save(CommonSettings{true, true})
	closeErr := windows.CloseHandle(h)
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if saveErr == nil || s.Err == nil || s.Value != (CommonSettings{}) {
		t.Fatal("failed save accepted", saveErr)
	}
	after, err := os.ReadFile(s.path)
	if err != nil || string(before) != string(after) {
		t.Fatal("old settings lost", err)
	}
}
