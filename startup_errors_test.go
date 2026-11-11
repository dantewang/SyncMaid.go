package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
)

func TestStartupClassificationPreservesCauseAndReadablePath(t *testing.T) {
	for _, tc := range []struct {
		cause          error
		fallback, want startupKind
		text           string
	}{
		{errUnsupportedLocation, startupOwnership, startupUnsupported, "当前版本"},
		{windows.ERROR_ACCESS_DENIED, startupOwnership, startupPermission, "读写权限"},
		{windows.ERROR_WRITE_PROTECT, startupOwnership, startupPermission, "无法访问或写入"},
		{errUnreliableIdentity, startupOwnership, startupOwnership, "保护数据"},
		{windows.ERROR_ACCESS_DENIED, startupOccupied, startupOccupied, "已有 SyncMaid"},
		{windows.ERROR_ACCESS_DENIED, startupFramework, startupFramework, "原生界面"},
	} {
		err := startupProblem(&locationError{path: `\\?\C:\isolated\Data`, cause: tc.cause}, tc.fallback, "fallback")
		var failure *startupFailure
		if !errors.As(err, &failure) || failure.kind != tc.want || !errors.Is(err, tc.cause) {
			t.Fatalf("classification: %v", err)
		}
		notice := noticeForStartup(err)
		if !strings.Contains(notice.message, tc.text) || !strings.Contains(notice.message, `C:\isolated\Data`) || strings.Contains(notice.message, `\\?\`) {
			t.Fatalf("notice: %s", notice.message)
		}
	}
}

func TestStartupPresenterRunsAfterOwnershipRollback(t *testing.T) {
	i := isolatedInstallation(t)
	if err := os.WriteFile(i.data, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	code := runStartup(func() error {
		_, err := acquireOwnership(i)
		if err == nil {
			t.Fatal("invalid Data accepted")
		}
		return startupProblem(err, startupOwnership, i.data)
	}, func(notice startupNotice) error {
		calls++
		if !strings.Contains(notice.message, pathLabel(i.data)) {
			t.Fatal("Data error path missing")
		}
		d, err := openDirectory(i.directory)
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
	if content, err := os.ReadFile(i.data); err != nil || string(content) != "fixture" {
		t.Fatal("Data fixture changed")
	}
}

func TestSecondLaunchActivationSuccessIsSilentAndFailureDoesNotWrite(t *testing.T) {
	i := isolatedInstallation(t)
	o, err := acquireOwnership(i)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	shown := make(chan struct{}, 1)
	s, err := startActivation(o.dataDirectory().key, func() { shown <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	second := func() error {
		_, err := acquireOwnership(i)
		if !errors.Is(err, errOccupied) {
			t.Fatalf("second ownership: %v", err)
		}
		if err := notifyInstallation(i); err != nil {
			return startupProblem(err, startupOccupied, i.directory)
		}
		return nil
	}
	if code := runStartup(second, func(startupNotice) error { t.Fatal("successful activation displayed an error"); return nil }); code != 0 {
		t.Fatal(code)
	}
	s.Close()
	calls := 0
	if code := runStartup(second, func(n startupNotice) error {
		calls++
		if !strings.Contains(n.message, "不要删除锁文件") {
			t.Fatal(n.message)
		}
		return nil
	}); code != 1 || calls != 1 {
		t.Fatalf("code=%d calls=%d", code, calls)
	}
	if _, err := acquireOwnership(i); !errors.Is(err, errOccupied) {
		t.Fatalf("activation failure allowed takeover: %v", err)
	}
	entries, err := os.ReadDir(i.data)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(".syncmaid-owner.lock") {
		t.Fatalf("second instance wrote Data: %v", entries)
	}
}

func TestSuccessfulStartupDoesNotPresentAndDiagnosticIsNotShown(t *testing.T) {
	if code := runStartup(func() error { return nil }, func(startupNotice) error { t.Fatal("success notice"); return nil }); code != 0 {
		t.Fatal(code)
	}
	n := noticeForStartup(errors.New("private ABI pointer and call stack"))
	if strings.Contains(n.message, "ABI") {
		t.Fatal("raw diagnostic leaked")
	}
}

func TestWindowInitializationFailureCanQuitAndReleaseTray(t *testing.T) {
	cause := errors.New("backend initialization failed")
	_, err := createStartupWindow(func() *mygo.Window { panic(cause) })
	if !errors.Is(err, cause) {
		t.Fatalf("lost window failure: %v", err)
	}
	l := &lifecycle{}
	releases := 0
	l.destroyTray = func() { releases++ }
	l.beginQuit()
	l.finish()
	l.finish()
	if releases != 1 || l.phase != stopped {
		t.Fatal("tray leaked on failed window initialization")
	}
}

func TestCleanupErrorIsNotSilentlyTreatedAsSuccessfulStartup(t *testing.T) {
	cleanup := errors.New("handle release failed")
	calls := 0
	code := runStartup(func() (err error) { defer func() { err = errors.Join(err, cleanup) }(); return nil }, func(n startupNotice) error { calls++; return nil })
	if code != 1 || calls != 1 {
		t.Fatalf("cleanup failure swallowed: %d %d", code, calls)
	}
}
