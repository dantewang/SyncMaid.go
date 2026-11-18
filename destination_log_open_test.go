package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syncmaid/internal/appcore"
	"testing"

	"github.com/egoist/mygo/ui"
	"syncmaid/internal/winutil"
)

func TestDestinationLogOpenUsesShellPathAndKeepsHistoryError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, appcore.RunHistoryFile)
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	historyErr := errors.New("existing history diagnostic")
	a := &application{
		installation:     winutil.Installation{Data: "\\\\?\\" + dir},
		logFileAvailable: true, historyError: historyErr, locale: "zh-Hans",
	}
	openErr := errors.New("no file association")
	a.openDestinationLogFile(func(got string) error {
		if got != path {
			t.Errorf("Shell got %q, want %q", got, path)
		}
		return openErr
	})
	if a.logOpenError != openErr || a.historyError != historyErr {
		t.Fatal("opening a file changed the history diagnostic")
	}
	view := ui.NewTester(a.destinationLogView, 720, 420)
	presentation := strings.Join(view.Texts(), "\n")
	for _, want := range []string{"无法打开日志文件：no file association", historyErr.Error()} {
		if !strings.Contains(presentation, want) {
			t.Errorf("missing %q in %s", want, presentation)
		}
	}
	a.openDestinationLogFile(func(string) error { return nil })
	if a.logOpenError != nil || a.historyError != historyErr {
		t.Fatal("success did not clear just the open error")
	}
}

func TestDestinationLogMissingFileNeverReachesShell(t *testing.T) {
	a := &application{installation: winutil.Installation{Data: t.TempDir()}}
	if err := inspectDestinationLogFile(a.destinationLogPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file probe: %v", err)
	}
	denied := func(string) error {
		t.Fatal("Shell called without a file")
		return nil
	}
	a.openDestinationLogFile(denied)
	// External removal after the cached probe must also disable the action.
	a.logFileAvailable = true
	a.openDestinationLogFile(denied)
	if a.logFileAvailable || !errors.Is(a.logOpenError, os.ErrNotExist) {
		t.Fatalf("stale file state: available=%t err=%v", a.logFileAvailable, a.logOpenError)
	}
}
