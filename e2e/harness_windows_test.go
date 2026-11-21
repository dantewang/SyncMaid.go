//go:build e2e && windows

package e2e

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	e2eBinary string

	user32               = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows      = user32.NewProc("EnumWindows")
	procGetWindowPID     = user32.NewProc("GetWindowThreadProcessId")
	procGetWindowTextW   = user32.NewProc("GetWindowTextW")
	procGetWindowTextLen = user32.NewProc("GetWindowTextLengthW")
	procPostMessageW     = user32.NewProc("PostMessageW")
)

const wmClose = 0x0010

func TestMain(m *testing.M) {
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binDir, err := os.MkdirTemp("", "sm-e2e-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	e2eBinary = filepath.Join(binDir, "SyncMaid.exe")
	cmd := exec.Command("go", "build", "-tags", "e2e", "-o", e2eBinary, ".")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build e2e SyncMaid.exe failed: %v\n%s\n", err, out)
		_ = os.RemoveAll(binDir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(binDir)
	os.Exit(code)
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("could not locate repository root containing go.mod")
		}
		dir = parent
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type e2eInstall struct {
	t        *testing.T
	shortDir string
	dir      string
	dataDir  string
	exePath  string
	sockPath string
	workDir  string
}

func newE2EInstall(t *testing.T) *e2eInstall {
	t.Helper()
	shortDir, err := os.MkdirTemp("", "sm-e2e")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortDir) })

	sockPath := filepath.Join(shortDir, "a.sock")
	if len(sockPath) >= 100 {
		t.Fatalf("automation socket path %q (%d bytes) risks Windows AF_UNIX 108-byte limit", sockPath, len(sockPath))
	}

	installDir := filepath.Join(shortDir, "install")
	if err := os.MkdirAll(installDir, 0o700); err != nil {
		t.Fatal(err)
	}
	exePath := filepath.Join(installDir, "SyncMaid.exe")
	copyFileBytes(t, e2eBinary, exePath)

	workDir := filepath.Join(shortDir, "work")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return &e2eInstall{
		t:        t,
		shortDir: shortDir,
		dir:      installDir,
		dataDir:  filepath.Join(installDir, "Data"),
		exePath:  exePath,
		sockPath: sockPath,
		workDir:  workDir,
	}
}

func (in *e2eInstall) seedLocale(tag string) {
	in.t.Helper()
	if err := os.MkdirAll(in.dataDir, 0o700); err != nil {
		in.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in.dataDir, "locale"), []byte(tag+"\n"), 0o600); err != nil {
		in.t.Fatal(err)
	}
}

func (in *e2eInstall) seedSettings(raw string) {
	in.t.Helper()
	if err := os.MkdirAll(in.dataDir, 0o700); err != nil {
		in.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in.dataDir, "settings-go.json"), []byte(raw), 0o600); err != nil {
		in.t.Fatal(err)
	}
}

func (in *e2eInstall) start(extraEnv ...string) *e2eProc {
	in.t.Helper()
	_ = os.Remove(in.sockPath)
	cmd := exec.Command(in.exePath)
	cmd.Dir = in.dir
	env := append(os.Environ(),
		"MYGO_AUTOMATION="+in.sockPath,
		"SYNCMAID_E2E_NO_MSGBOX=1",
	)
	env = append(env, extraEnv...)
	cmd.Env = env
	var out syncBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		in.t.Fatal(err)
	}
	p := &e2eProc{
		t:       in.t,
		install: in,
		cmd:     cmd,
		out:     &out,
		exited:  make(chan error, 1),
	}
	go func() {
		p.exited <- cmd.Wait()
	}()
	in.t.Cleanup(func() {
		if p.client != nil && p.client.conn != nil {
			_ = p.client.conn.Close()
		}
		if !p.ended {
			_ = cmd.Process.Kill()
			<-p.exited
			p.ended = true
		}
		if in.t.Failed() {
			in.t.Logf("SyncMaid.exe output:\n%s", out.String())
		}
	})

	conn := p.dialSocket(20 * time.Second)
	p.client = &autoClient{t: in.t, proc: p, conn: conn, r: bufio.NewReader(conn)}
	return p
}

func (in *e2eInstall) runSecondInstance(extraEnv ...string) (string, error) {
	in.t.Helper()
	cmd := exec.Command(in.exePath)
	cmd.Dir = in.dir
	env := filteredEnv(os.Environ(), "MYGO_AUTOMATION")
	env = append(env, "SYNCMAID_E2E_NO_MSGBOX=1")
	env = append(env, extraEnv...)
	cmd.Env = env
	var out syncBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return out.String(), err
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		return out.String(), errors.New("second instance timed out after 15s")
	}
}

func filteredEnv(env []string, dropKey string) []string {
	prefix := dropKey + "="
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if len(entry) >= len(prefix) && strings.EqualFold(entry[:len(prefix)], prefix) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

type e2eProc struct {
	t       *testing.T
	install *e2eInstall
	cmd     *exec.Cmd
	out     *syncBuffer
	exited  chan error
	ended   bool
	client  *autoClient
}

func (p *e2eProc) pid() uint32 {
	return uint32(p.cmd.Process.Pid)
}

func (p *e2eProc) dialSocket(timeout time.Duration) net.Conn {
	p.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		select {
		case err := <-p.exited:
			p.ended = true
			p.t.Fatalf("SyncMaid.exe exited before opening automation socket (%v):\n%s", err, p.out.String())
		default:
		}
		conn, err := net.DialTimeout("unix", p.install.sockPath, time.Second)
		if err == nil {
			return conn
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("timed out waiting for automation socket %s: %v\noutput:\n%s", p.install.sockPath, err, p.out.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (p *e2eProc) waitExit(timeout time.Duration) {
	p.t.Helper()
	select {
	case err := <-p.exited:
		p.ended = true
		if err != nil {
			p.t.Fatalf("SyncMaid.exe exited with error: %v\noutput:\n%s", err, p.out.String())
		}
	case <-time.After(timeout):
		p.t.Fatalf("SyncMaid.exe did not exit within %v\noutput:\n%s", timeout, p.out.String())
	}
	if p.client != nil && p.client.conn != nil {
		_ = p.client.conn.Close()
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := os.Lstat(p.install.sockPath)
		if os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("automation socket %s remained after exit: %v", p.install.sockPath, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type autoError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type autoRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type autoWindow struct {
	ID            int      `json:"id"`
	Title         string   `json:"title"`
	Kind          string   `json:"kind"`
	Bounds        autoRect `json:"bounds"`
	ContentBounds autoRect `json:"contentBounds"`
	Focused       bool     `json:"focused"`
	Visible       bool     `json:"visible"`
}

type autoNode struct {
	ID      string   `json:"id"`
	Parent  string   `json:"parent"`
	Role    string   `json:"role"`
	Key     any      `json:"key"`
	Label   string   `json:"label"`
	Value   string   `json:"value"`
	Bounds  autoRect `json:"bounds"`
	States  []string `json:"states"`
	Actions []string `json:"actions"`
}

func (n autoNode) disabled() bool { return slices.Contains(n.States, "disabled") }
func (n autoNode) readOnly() bool { return slices.Contains(n.States, "readOnly") }

type autoClient struct {
	t    *testing.T
	proc *e2eProc
	conn net.Conn
	r    *bufio.Reader
	seq  int
}

func (c *autoClient) writeReq(method string, params any) int {
	c.t.Helper()
	c.seq++
	line, err := json.Marshal(map[string]any{"id": c.seq, "method": method, "params": params})
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.conn.Write(append(line, '\n')); err != nil {
		c.t.Fatalf("write %s: %v", method, err)
	}
	return c.seq
}

func (c *autoClient) call(method string, params any) (json.RawMessage, *autoError) {
	c.t.Helper()
	id := c.writeReq(method, params)
	_ = c.conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	answer, err := c.r.ReadBytes('\n')
	_ = c.conn.SetReadDeadline(time.Time{})
	if err != nil {
		c.t.Fatalf("read %s: %v", method, err)
	}
	var res struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *autoError      `json:"error"`
	}
	if err := json.Unmarshal(answer, &res); err != nil || res.ID != id {
		c.t.Fatalf("%s: bad response %s (%v)", method, answer, err)
	}
	return res.Result, res.Error
}

func (c *autoClient) must(method string, params any, out any) {
	c.t.Helper()
	res, ae := c.call(method, params)
	if ae != nil {
		c.t.Fatalf("%s %v -> %s: %s", method, params, ae.Code, ae.Message)
	}
	if out != nil {
		if err := json.Unmarshal(res, out); err != nil {
			c.t.Fatalf("unmarshal %s %s: %v", method, res, err)
		}
	}
}

func (c *autoClient) windows() []autoWindow {
	c.t.Helper()
	var res struct {
		Windows []autoWindow `json:"windows"`
	}
	c.must("windows", nil, &res)
	return res.Windows
}

func (c *autoClient) waitWindow(titleSubstr string, wantVisible *bool, timeout time.Duration) autoWindow {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		ws := c.windows()
		for _, w := range ws {
			if strings.Contains(w.Title, titleSubstr) && (wantVisible == nil || w.Visible == *wantVisible) {
				return w
			}
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("timed out waiting for window %q (wantVisible=%v) in %+v", titleSubstr, wantVisible, ws)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func (c *autoClient) waitSecondWindow(mainID int, titleSubstr string, timeout time.Duration) autoWindow {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		ws := c.windows()
		for _, w := range ws {
			if w.ID != mainID && strings.Contains(w.Title, titleSubstr) && w.Visible {
				return w
			}
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("timed out waiting for second window %q in %+v", titleSubstr, ws)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func (c *autoClient) idle(winID int) {
	c.t.Helper()
	c.must("idle", map[string]any{"window": winID, "timeout": 5000}, nil)
}

func (c *autoClient) tree(winID int) []autoNode {
	c.t.Helper()
	var res struct {
		Nodes []autoNode `json:"nodes"`
	}
	c.must("tree", map[string]any{"window": winID}, &res)
	return res.Nodes
}

func (c *autoClient) dumpNodes(winID int) string {
	nodes := c.tree(winID)
	var b strings.Builder
	for _, n := range nodes {
		fmt.Fprintf(&b, "  role=%s label=%q value=%q states=%v actions=%v\n", n.Role, n.Label, n.Value, n.States, n.Actions)
	}
	return b.String()
}

func (c *autoClient) waitNode(winID int, timeout time.Duration, desc string, pred func(autoNode) bool) autoNode {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		nodes := c.tree(winID)
		for _, n := range nodes {
			if pred(n) {
				return n
			}
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("timed out waiting for %s; current nodes:\n%s", desc, c.dumpNodes(winID))
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func (c *autoClient) waitText(winID int, substr string, timeout time.Duration) autoNode {
	c.t.Helper()
	return c.waitNode(winID, timeout, fmt.Sprintf("text containing %q", substr), func(n autoNode) bool {
		return strings.Contains(n.Label, substr) || strings.Contains(n.Value, substr)
	})
}

func (c *autoClient) waitNoText(winID int, substr string, timeout time.Duration) {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		nodes := c.tree(winID)
		found := false
		for _, n := range nodes {
			if strings.Contains(n.Label, substr) || strings.Contains(n.Value, substr) {
				found = true
				break
			}
		}
		if !found {
			return
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("timed out waiting for %q to disappear; current nodes:\n%s", substr, c.dumpNodes(winID))
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func matchesRole(got, want string) bool {
	if want != "" {
		return got == want
	}
	switch got {
	case "button", "menuButton", "menuItem", "menuItemCheckBox", "menuItemRadio", "switch", "link", "checkBox", "toggleButton":
		return true
	default:
		return false
	}
}

func (c *autoClient) pressNode(winID int, n autoNode) {
	c.t.Helper()
	if slices.Contains(n.Actions, "press") {
		c.must("act", map[string]any{"window": winID, "id": n.ID, "action": "press"}, nil)
	} else {
		c.must("input", map[string]any{
			"window": winID,
			"events": []any{map[string]any{
				"type": "click",
				"x":    n.Bounds.X + n.Bounds.Width/2,
				"y":    n.Bounds.Y + n.Bounds.Height/2,
			}},
		}, nil)
	}
	c.idle(winID)
}

func (c *autoClient) press(winID int, role, label string) {
	c.t.Helper()
	n := c.waitNode(winID, 10*time.Second, fmt.Sprintf("%s %q", role, label), func(n autoNode) bool {
		return matchesRole(n.Role, role) && n.Label == label && !n.disabled()
	})
	c.pressNode(winID, n)
}

func (c *autoClient) pressContains(winID int, role, substr string) {
	c.t.Helper()
	n := c.waitNode(winID, 10*time.Second, fmt.Sprintf("%s containing %q", role, substr), func(n autoNode) bool {
		return matchesRole(n.Role, role) && strings.Contains(n.Label, substr) && !n.disabled()
	})
	c.pressNode(winID, n)
}

func (c *autoClient) pressAfter(winID int, afterLabel, role, label string) {
	c.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		nodes := c.tree(winID)
		seenAnchor := false
		for _, n := range nodes {
			if !seenAnchor {
				if n.Role == "text" && n.Label == afterLabel {
					seenAnchor = true
				}
				continue
			}
			if matchesRole(n.Role, role) && n.Label == label && !n.disabled() {
				c.pressNode(winID, n)
				return
			}
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("timed out waiting for %s %q after %q; current nodes:\n%s", role, label, afterLabel, c.dumpNodes(winID))
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func (c *autoClient) setValue(winID int, label, value string) {
	c.t.Helper()
	n := c.waitNode(winID, 10*time.Second, fmt.Sprintf("textField %q", label), func(n autoNode) bool {
		return n.Role == "textField" && n.Label == label && !n.disabled() && !n.readOnly()
	})
	c.must("act", map[string]any{"window": winID, "id": n.ID, "action": "setValue", "text": value}, nil)
	c.idle(winID)
}

func (c *autoClient) chooseMoreMenu(winID int, moreLabel, itemLabel string, expectExit bool) {
	c.t.Helper()
	c.press(winID, "menuButton", moreLabel)
	item := c.waitNode(winID, 5*time.Second, fmt.Sprintf("menuItem %q", itemLabel), func(n autoNode) bool {
		return (n.Role == "menuItem" || n.Role == "menuItemCheckBox" || n.Role == "menuItemRadio") && n.Label == itemLabel && !n.disabled()
	})
	if expectExit {
		if slices.Contains(item.Actions, "press") {
			c.writeReq("act", map[string]any{"window": winID, "id": item.ID, "action": "press"})
		} else {
			c.writeReq("input", map[string]any{
				"window": winID,
				"events": []any{map[string]any{
					"type": "click",
					"x":    item.Bounds.X + item.Bounds.Width/2,
					"y":    item.Bounds.Y + item.Bounds.Height/2,
				}},
			})
		}
		c.proc.waitExit(15 * time.Second)
		return
	}
	c.pressNode(winID, item)
}

func (c *autoClient) quitViaSettings(winID int, settingsLabel, exitLabel string) {
	c.t.Helper()
	alreadyOnSettings := false
	for _, n := range c.tree(winID) {
		if n.Role == "button" && n.Label == exitLabel && !n.disabled() {
			alreadyOnSettings = true
			break
		}
	}
	if !alreadyOnSettings {
		c.press(winID, "button", settingsLabel)
	}
	btn := c.waitNode(winID, 5*time.Second, fmt.Sprintf("exit button %q", exitLabel), func(n autoNode) bool {
		return n.Role == "button" && n.Label == exitLabel && !n.disabled()
	})
	if slices.Contains(btn.Actions, "press") {
		c.writeReq("act", map[string]any{"window": winID, "id": btn.ID, "action": "press"})
	} else {
		c.writeReq("input", map[string]any{
			"window": winID,
			"events": []any{map[string]any{
				"type": "click",
				"x":    btn.Bounds.X + btn.Bounds.Width/2,
				"y":    btn.Bounds.Y + btn.Bounds.Height/2,
			}},
		})
	}
	c.proc.waitExit(15 * time.Second)
}

func (c *autoClient) capture(winID int) (image.Image, int, int) {
	c.t.Helper()
	var res struct {
		PNG    string `json:"png"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}
	c.must("capture", map[string]any{"window": winID}, &res)
	raw, err := base64.StdEncoding.DecodeString(res.PNG)
	if err != nil {
		c.t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		c.t.Fatal(err)
	}
	return img, res.Width, res.Height
}

func sendWMClose(pid uint32, titleExact string) error {
	var target windows.HWND
	cb := syscall.NewCallback(func(hwnd uintptr, lparam uintptr) uintptr {
		var windowPID uint32
		_, _, _ = procGetWindowPID.Call(hwnd, uintptr(unsafe.Pointer(&windowPID)))
		if windowPID != pid {
			return 1
		}
		length, _, _ := procGetWindowTextLen.Call(hwnd)
		if length == 0 {
			return 1
		}
		buf := make([]uint16, length+1)
		_, _, _ = procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if windows.UTF16ToString(buf) == titleExact {
			target = windows.HWND(hwnd)
			return 0
		}
		return 1
	})
	_, _, _ = procEnumWindows.Call(cb, 0)
	if target == 0 {
		return fmt.Errorf("no top-level window %q for pid %d", titleExact, pid)
	}
	r, _, err := procPostMessageW.Call(uintptr(target), wmClose, 0, 0)
	if r == 0 {
		return err
	}
	return nil
}

func waitUntil(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", desc)
		}
		time.Sleep(40 * time.Millisecond)
	}
}

func copyFileBytes(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertNoFile(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	if !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent, got err=%v", path, err)
	}
}

func assertNoTempFiles(t *testing.T, dirs ...string) {
	t.Helper()
	for _, root := range dirs {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasPrefix(d.Name(), ".syncmaid-") {
				t.Fatalf("leftover temporary file: %s", path)
			}
			return nil
		})
	}
}

func boolPtr(v bool) *bool { return &v }
