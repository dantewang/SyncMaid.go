package main

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

func isolatedInstallation(t *testing.T) installation {
	t.Helper()
	d := t.TempDir()
	return installation{directory: d, data: filepath.Join(d, "Data")}
}

func TestOwnershipErrorsAndRelease(t *testing.T) {
	i := isolatedInstallation(t)
	first, err := acquireOwnership(i)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireOwnership(i); !errors.Is(err, errOccupied) {
		t.Fatalf("second writer: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := acquireOwnership(i)
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
	bad := isolatedInstallation(t)
	if err := os.WriteFile(bad.data, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireOwnership(bad); err == nil {
		t.Fatal("bad Data accepted")
	}
	// A failed Data acquisition must release the installation claim too.
	d, err := openDirectory(bad.directory)
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
	o, err := acquireOwnership(installation{directory: d, data: filepath.Join(d, "Data")})
	if errors.Is(err, errOccupied) {
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
		children[n] = startLockChild(t, i.directory)
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
	next := startLockChild(t, i.directory)
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
	o, err := acquireOwnership(i)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	for _, alias := range []string{strings.ToUpper(i.directory), `\\?\` + i.directory} {
		d, err := openDirectory(alias)
		if err != nil {
			t.Fatal(err)
		}
		if d.key != o.directories[0].key {
			t.Fatal("alias changed object identity")
		}
		d.Close()
		if _, err := acquireOwnership(installation{directory: alias, data: filepath.Join(alias, "Data")}); !errors.Is(err, errOccupied) {
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
	junction(t, a.data, shared)
	junction(t, b.data, shared)
	first, err := acquireOwnership(a)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := acquireOwnership(b); !errors.Is(err, errOccupied) {
		t.Fatalf("shared Data has second writer: %v", err)
	}
	alias := filepath.Join(t.TempDir(), "install-alias")
	junction(t, alias, a.directory)
	d, err := openDirectory(alias)
	if err != nil {
		t.Fatal(err)
	}
	if d.key != first.directories[0].key {
		t.Fatal("junction changed identity")
	}
	d.Close()
	if _, err := acquireOwnership(installation{directory: alias, data: filepath.Join(alias, "Data")}); !errors.Is(err, errOccupied) {
		t.Fatalf("junction writer: %v", err)
	}
	// A nonexistent activation listener cannot give a second process ownership.
	notifyInstallation(b)
	if _, err := acquireOwnership(b); !errors.Is(err, errOccupied) {
		t.Fatalf("failed activation bypassed ownership: %v", err)
	}
	first.Close()
	next, err := acquireOwnership(b)
	if err != nil {
		t.Fatalf("failed second Data claim leaked install ownership: %v", err)
	}
	next.Close()
}

func TestShortNameIdentityWhenAvailable(t *testing.T) {
	i := isolatedInstallation(t)
	p, err := windows.UTF16PtrFromString(i.directory)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 32768)
	n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil {
		t.Skipf("short names unavailable: %v", err)
	}
	short := windows.UTF16ToString(buf[:n])
	if strings.EqualFold(short, i.directory) {
		t.Skip("8.3 alias not created on this volume")
	}
	d, err := openDirectory(i.directory)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s, err := openDirectory(short)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if d.key != s.key {
		t.Fatal("short-name identity mismatch")
	}
}

func TestCoordinationHardlinkRejected(t *testing.T) {
	i := isolatedInstallation(t)
	original := filepath.Join(i.directory, "unrelated")
	if err := os.WriteFile(original, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(original, filepath.Join(i.directory, ".syncmaid-owner.lock")); err != nil {
		t.Skipf("hardlink unavailable: %v", err)
	}
	if _, err := acquireOwnership(i); err == nil || errors.Is(err, errOccupied) {
		t.Fatalf("unsafe coordination object accepted: %v", err)
	}
}

func TestRemoteOwnershipNotYetSupported(t *testing.T) {
	t.Skip("UNC/mapped network volumes deliberately fail closed; no remote test share configured")
}
