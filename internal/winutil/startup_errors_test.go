package winutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestStartupClassificationPreservesCauseAndReadablePath(t *testing.T) {
	for _, tc := range []struct {
		cause          error
		fallback, want StartupKind
		text           string
	}{
		{ErrUnsupportedLocation, StartupOwnership, StartupUnsupported, "当前版本"},
		{windows.ERROR_ACCESS_DENIED, StartupOwnership, StartupPermission, "读写权限"},
		{windows.ERROR_WRITE_PROTECT, StartupOwnership, StartupPermission, "无法访问或写入"},
		{ErrUnreliableIdentity, StartupOwnership, StartupOwnership, "保护数据"},
		{windows.ERROR_ACCESS_DENIED, StartupOccupied, StartupPermission, "读写权限"},
		{windows.ERROR_ACCESS_DENIED, StartupFramework, StartupFramework, "原生界面"},
	} {
		err := StartupProblem(&LocationError{Path: `\\?\C:\isolated\Data`, Cause: tc.cause}, tc.fallback, "fallback")
		var failure *StartupFailure
		if !errors.As(err, &failure) || failure.Kind != tc.want || !errors.Is(err, tc.cause) {
			t.Fatalf("classification: %v", err)
		}
		notice := NoticeForStartup(err)
		if !strings.Contains(notice.Message, tc.text) || !strings.Contains(notice.Message, `C:\isolated\Data`) || strings.Contains(notice.Message, `\\?\`) {
			t.Fatalf("notice: %s", notice.Message)
		}
	}
}

func TestStartupPresenterRunsAfterOwnershipRollback(t *testing.T) {
	i := isolatedInstallation(t)
	if err := os.WriteFile(i.Data, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	code := RunStartup(func() error {
		_, err := AcquireOwnership(i)
		if err == nil {
			t.Fatal("invalid Data accepted")
		}
		return StartupProblem(err, StartupOwnership, i.Data)
	}, func(notice StartupNotice) error {
		calls++
		if !strings.Contains(notice.Message, PathLabel(i.Data)) {
			t.Fatal("Data error path missing")
		}
		d, err := OpenDirectory(i.Directory)
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		h, err := claimDirectory(d)
		if err != nil {
			t.Fatalf("notice displayed with ownership still held: %v", err)
		}
		windows.CloseHandle(h)
		return errors.New("presenter unavailable")
	})
	if code != 1 || calls != 1 {
		t.Fatalf("code=%d calls=%d", code, calls)
	}
	if content, err := os.ReadFile(i.Data); err != nil || string(content) != "fixture" {
		t.Fatal("Data fixture changed")
	}
}

func TestSecondLaunchActivationSuccessIsSilentAndFailureDoesNotWrite(t *testing.T) {
	i := isolatedInstallation(t)
	o, err := AcquireOwnership(i)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	shown := make(chan struct{}, 1)
	s, err := StartActivation(o.DataDirectory().Key, func() { shown <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	second := func() error {
		_, err := AcquireOwnership(i)
		if !errors.Is(err, ErrOccupied) {
			t.Fatalf("second ownership: %v", err)
		}
		if err := NotifyInstallation(i); err != nil {
			return StartupProblem(err, StartupOccupied, i.Directory)
		}
		return nil
	}
	if code := RunStartup(second, func(StartupNotice) error { t.Fatal("successful activation displayed an error"); return nil }); code != 0 {
		t.Fatal(code)
	}
	s.Close()
	calls := 0
	if code := RunStartup(second, func(n StartupNotice) error {
		calls++
		if !strings.Contains(n.Message, "不要删除锁文件") {
			t.Fatal(n.Message)
		}
		return nil
	}); code != 1 || calls != 1 {
		t.Fatalf("code=%d calls=%d", code, calls)
	}
	if _, err := AcquireOwnership(i); !errors.Is(err, ErrOccupied) {
		t.Fatalf("activation failure allowed takeover: %v", err)
	}
	entries, err := os.ReadDir(i.Data)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(".syncmaid-owner.lock") {
		t.Fatalf("second instance wrote Data: %v", entries)
	}
}

func TestSuccessfulStartupDoesNotPresentAndDiagnosticIsNotShown(t *testing.T) {
	if code := RunStartup(func() error { return nil }, func(StartupNotice) error { t.Fatal("success notice"); return nil }); code != 0 {
		t.Fatal(code)
	}
	n := NoticeForStartup(errors.New("private ABI pointer and call stack"))
	if strings.Contains(n.Message, "ABI") {
		t.Fatal("raw diagnostic leaked")
	}
}

func TestCleanupErrorIsNotSilentlyTreatedAsSuccessfulStartup(t *testing.T) {
	cleanup := errors.New("handle release failed")
	calls := 0
	code := RunStartup(func() (err error) { defer func() { err = errors.Join(err, cleanup) }(); return nil }, func(n StartupNotice) error { calls++; return nil })
	if code != 1 || calls != 1 {
		t.Fatalf("cleanup failure swallowed: %d %d", code, calls)
	}
}
