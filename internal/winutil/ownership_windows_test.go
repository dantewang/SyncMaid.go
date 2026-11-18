package winutil

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func isolatedInstallation(t *testing.T) Installation {
	t.Helper()
	d := t.TempDir()
	return Installation{Directory: d, Data: filepath.Join(d, "Data")}
}

func TestOwnershipErrorsAndRelease(t *testing.T) {
	i := isolatedInstallation(t)
	first, err := AcquireOwnership(i)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireOwnership(i); !errors.Is(err, ErrOccupied) {
		t.Fatalf("second writer: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := AcquireOwnership(i)
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
	bad := isolatedInstallation(t)
	if err := os.WriteFile(bad.Data, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireOwnership(bad); err == nil {
		t.Fatal("bad Data accepted")
	}
	// A failed Data acquisition must release the Installation claim too.
	d, err := OpenDirectory(bad.Directory)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	h, err := claimDirectory(d)
	if err != nil {
		t.Fatalf("rollback leaked install claim: %v", err)
	}
	windows.CloseHandle(h)
}

func TestOwnershipProcess(t *testing.T) {
	if os.Getenv("SYNCMAID_LOCK_HELPER") != "1" {
		return
	}
	d := os.Getenv("SYNCMAID_LOCK_DIRECTORY")
	o, err := AcquireOwnership(Installation{Directory: d, Data: filepath.Join(d, "Data")})
	if errors.Is(err, ErrOccupied) {
		fmt.Println("occupied")
		return
	}
	if err != nil {
		fmt.Println("error:", err)
		os.Exit(24)
	}
	fmt.Println("owned")
	io.Copy(io.Discard, os.Stdin)
	if err := o.Close(); err != nil {
		os.Exit(25)
	}
}

type lockChild struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *bufio.Scanner
	stderr bytes.Buffer
}

func startLockChild(t *testing.T, directory string) *lockChild {
	t.Helper()
	c := &lockChild{cmd: exec.Command(os.Args[0], "-test.run=^TestOwnershipProcess$")}
	c.cmd.Env = append(os.Environ(), "SYNCMAID_LOCK_HELPER=1", "SYNCMAID_LOCK_DIRECTORY="+directory)
	var err error
	c.input, err = c.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c.output = bufio.NewScanner(out)
	c.cmd.Stderr = &c.stderr
	if err := c.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.input.Close()
		if c.cmd.ProcessState == nil {
			c.cmd.Process.Kill()
			c.cmd.Wait()
		}
	})
	return c
}

func (c *lockChild) result(t *testing.T) string {
	t.Helper()
	ready := make(chan string, 1)
	go func() {
		if c.output.Scan() {
			ready <- c.output.Text()
		} else {
			ready <- "no readiness"
		}
	}()
	select {
	case line := <-ready:
		return line
	case <-time.After(15 * time.Second):
		t.Fatal("ownership helper did not report a state")
		return ""
	}
}

func TestConcurrentProcessesHaveExactlyOneWriterAndCrashReleases(t *testing.T) {
	i := isolatedInstallation(t)
	children := make([]*lockChild, 6)
	for n := range children {
		children[n] = startLockChild(t, i.Directory)
	}
	var winner *lockChild
	for _, c := range children {
		switch state := c.result(t); state {
		case "owned":
			if winner != nil {
				t.Fatal("multiple writers")
			}
			winner = c
		case "occupied":
			c.input.Close()
			if err := c.cmd.Wait(); err != nil {
				t.Fatal(err, c.stderr.String())
			}
		default:
			t.Fatal(state)
		}
	}
	if winner == nil {
		t.Fatal("no writer")
	}
	if err := winner.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	winner.cmd.Wait()
	next := startLockChild(t, i.Directory)
	if state := next.result(t); state != "owned" {
		t.Fatal("crash left ownership:", state)
	}
	next.input.Close()
	if err := next.cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestOwnershipLongPrefixAndCaseAliases(t *testing.T) {
	i := isolatedInstallation(t)
	o, err := AcquireOwnership(i)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	for _, alias := range []string{strings.ToUpper(i.Directory), `\\?\` + i.Directory} {
		d, err := OpenDirectory(alias)
		if err != nil {
			t.Fatal(err)
		}
		if d.Key != o.directories[0].Key {
			t.Fatal("alias changed object identity")
		}
		d.Close()
		if _, err := AcquireOwnership(Installation{Directory: alias, Data: filepath.Join(alias, "Data")}); !errors.Is(err, ErrOccupied) {
			t.Fatalf("alias writer: %v", err)
		}
	}
}

func junction(t *testing.T, path, target string) {
	t.Helper()
	shell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("PowerShell unavailable for isolated junction fixture")
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	cmd := exec.Command(shell, "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; New-Item -ItemType Junction -Path "+quote(path)+" -Target "+quote(target)+" | Out-Null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("junction fixture: %v %s", err, out)
	}
}

func TestSharedDataJunctionAndInstallationJunction(t *testing.T) {
	a, b := isolatedInstallation(t), isolatedInstallation(t)
	shared := t.TempDir()
	junction(t, a.Data, shared)
	junction(t, b.Data, shared)
	first, err := AcquireOwnership(a)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := AcquireOwnership(b); !errors.Is(err, ErrOccupied) {
		t.Fatalf("shared Data has second writer: %v", err)
	}
	alias := filepath.Join(t.TempDir(), "install-alias")
	junction(t, alias, a.Directory)
	d, err := OpenDirectory(alias)
	if err != nil {
		t.Fatal(err)
	}
	if d.Key != first.directories[0].Key {
		t.Fatal("junction changed identity")
	}
	d.Close()
	if _, err := AcquireOwnership(Installation{Directory: alias, Data: filepath.Join(alias, "Data")}); !errors.Is(err, ErrOccupied) {
		t.Fatalf("junction writer: %v", err)
	}
	// A nonexistent activation listener cannot give a second process ownership.
	NotifyInstallation(b)
	if _, err := AcquireOwnership(b); !errors.Is(err, ErrOccupied) {
		t.Fatalf("failed activation bypassed ownership: %v", err)
	}
	first.Close()
	next, err := AcquireOwnership(b)
	if err != nil {
		t.Fatalf("failed second Data claim leaked install ownership: %v", err)
	}
	next.Close()
}

func TestShortNameIdentityWhenAvailable(t *testing.T) {
	i := isolatedInstallation(t)
	p, err := windows.UTF16PtrFromString(i.Directory)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 32768)
	n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil {
		t.Skipf("short names unavailable: %v", err)
	}
	short := windows.UTF16ToString(buf[:n])
	if strings.EqualFold(short, i.Directory) {
		t.Skip("8.3 alias not created on this volume")
	}
	d, err := OpenDirectory(i.Directory)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s, err := OpenDirectory(short)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if d.Key != s.Key {
		t.Fatal("short-name identity mismatch")
	}
}

func TestCoordinationHardlinkRejected(t *testing.T) {
	i := isolatedInstallation(t)
	original := filepath.Join(i.Directory, "unrelated")
	if err := os.WriteFile(original, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(original, filepath.Join(i.Directory, ".syncmaid-owner.lock")); err != nil {
		t.Skipf("hardlink unavailable: %v", err)
	}
	if _, err := AcquireOwnership(i); err == nil || errors.Is(err, ErrOccupied) {
		t.Fatalf("unsafe coordination object accepted: %v", err)
	}
}

func TestRemoteOwnershipNotYetSupported(t *testing.T) {
	t.Skip("UNC/mapped network volumes deliberately fail closed; no remote test share configured")
}

func TestDifferentInstallationsOwnIndependentDataSimultaneously(t *testing.T) {
	a, b := isolatedInstallation(t), isolatedInstallation(t)
	first, err := AcquireOwnership(a)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := AcquireOwnership(b)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if first.DataDirectory().Key == second.DataDirectory().Key {
		t.Fatal("distinct Data objects collided")
	}
	if _, err := AcquireOwnership(a); !errors.Is(err, ErrOccupied) {
		t.Fatalf("first not protected: %v", err)
	}
	if _, err := AcquireOwnership(b); !errors.Is(err, ErrOccupied) {
		t.Fatalf("second not protected: %v", err)
	}
}

func TestCoordinationReparseRejected(t *testing.T) {
	i := isolatedInstallation(t)
	// A directory junction needs no symlink privilege on this test host.
	junction(t, filepath.Join(i.Directory, ".syncmaid-owner.lock"), t.TempDir())
	if _, err := AcquireOwnership(i); err == nil || errors.Is(err, ErrOccupied) {
		t.Fatalf("reparse coordination object accepted: %v", err)
	}
}

func TestCoordinationFileSymlinkRejectedWhenPermitted(t *testing.T) {
	i := isolatedInstallation(t)
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(i.Directory, ".syncmaid-owner.lock")); err != nil {
		t.Skipf("file symlink unavailable: %v", err)
	}
	if _, err := AcquireOwnership(i); err == nil || errors.Is(err, ErrOccupied) {
		t.Fatalf("file symlink accepted: %v", err)
	}
}
