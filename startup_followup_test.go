package main

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestOwnershipFailuresIdentifyActualDirectoryBoundary(t *testing.T) {
	for _, stage := range []string{"install-open", "install-claim", "data-create", "data-claim"} {
		t.Run(stage, func(t *testing.T) {
			i := isolatedInstallation(t)
			want := i.directory
			switch stage {
			case "install-open":
				i.directory = filepath.Join(i.directory, "not-a-directory")
				if err := os.WriteFile(i.directory, nil, 0600); err != nil {
					t.Fatal(err)
				}
				want = i.directory
			case "install-claim":
				if err := os.Mkdir(filepath.Join(i.directory, ".syncmaid-owner.lock"), 0700); err != nil {
					t.Fatal(err)
				}
			case "data-create":
				want = i.data
				if err := os.WriteFile(i.data, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "data-claim":
				want = i.data
				if err := os.MkdirAll(filepath.Join(i.data, ".syncmaid-owner.lock"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			_, err := acquireOwnership(i)
			var location *locationError
			if err == nil || !errors.As(err, &location) || location.path != want || !errors.Is(err, location.cause) {
				t.Fatalf("wrong location/cause: %v", err)
			}
			problem := startupProblem(err, startupOwnership, i.data)
			var failure *startupFailure
			if !errors.As(problem, &failure) || failure.path != want {
				t.Fatalf("fallback overrode actual location: %v", problem)
			}
			if !strings.HasSuffix(noticeForStartup(problem).message, "位置："+pathLabel(want)) {
				t.Fatal("incorrect displayed location")
			}
		})
	}
}

func TestActivationClassifiesOnlyProvenDirectoryAccessFailures(t *testing.T) {
	for _, cause := range []error{windows.ERROR_ACCESS_DENIED, errUnsupportedLocation} {
		directory := startupProblem(&locationError{path: `C:\Data`, cause: cause}, startupOccupied, `C:\Install`)
		var failure *startupFailure
		if !errors.As(directory, &failure) || failure.kind == startupOccupied {
			t.Fatalf("directory evidence ignored: %v", directory)
		}
		transport := startupProblem(&net.OpError{Op: "dial", Net: "unix", Err: cause}, startupOccupied, `C:\Install`)
		if !errors.As(transport, &failure) || failure.kind != startupOccupied || failure.path != `C:\Install` {
			t.Fatalf("socket failure mislabeled as Data access: %v", transport)
		}
	}
}

func TestUnconfirmedActivationReceiptsRemainBoundedAndNeverBecomeWriter(t *testing.T) {
	for _, mode := range []string{"eof", "timeout", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			i := isolatedInstallation(t)
			owner, err := acquireOwnership(i)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			marker := filepath.Join(i.data, "preserved-fixture")
			if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("unix", activationPath(owner.dataDirectory().key))
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(4 * time.Second))
				var request [1]byte
				if _, err := io.ReadFull(conn, request[:]); err != nil {
					return
				}
				switch mode {
				case "eof":
					return // Mixed-version peer may already have restored its UI.
				case "invalid":
					conn.Write([]byte{7})
				case "timeout":
					conn.Read(request[:]) // No sleep: wait for client deadline/close.
				}
			}()
			calls := 0
			var activationError error
			start := time.Now()
			code := runStartup(func() error {
				if _, err := acquireOwnership(i); !errors.Is(err, errOccupied) {
					t.Fatalf("second became writer: %v", err)
				}
				activationError = notifyInstallation(i)
				if activationError == nil {
					t.Fatal("unconfirmed receipt treated as success")
				}
				return startupProblem(activationError, startupOccupied, i.directory)
			}, func(n startupNotice) error {
				calls++
				if !strings.Contains(n.message, "未能确认窗口已恢复") {
					t.Fatal(n.message)
				}
				return nil
			})
			if code != 1 || calls != 1 || time.Since(start) > 5*time.Second {
				t.Fatalf("unbounded or incorrect failure: %d %d %s", code, calls, time.Since(start))
			}
			switch mode {
			case "eof":
				if !errors.Is(activationError, io.EOF) {
					t.Fatal(activationError)
				}
			case "invalid":
				if !errors.Is(activationError, errInvalidActivationReceipt) {
					t.Fatal(activationError)
				}
			case "timeout":
				var timeout net.Error
				if !errors.As(activationError, &timeout) || !timeout.Timeout() {
					t.Fatal(activationError)
				}
			}
			if _, err := acquireOwnership(i); !errors.Is(err, errOccupied) {
				t.Fatalf("failed activation allowed writer: %v", err)
			}
			content, err := os.ReadFile(marker)
			if err != nil || string(content) != "preserve" {
				t.Fatal("fixture changed")
			}
			entries, err := os.ReadDir(i.data)
			if err != nil || len(entries) != 2 {
				t.Fatalf("unexpected Data write: %v %v", entries, err)
			}
			listener.Close()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("peer did not stop")
			}
		})
	}
}
