//go:build e2e && windows

package e2e

import (
	"encoding/json"
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func movePointer(c *autoClient, winID int, x, y float64) {
	c.t.Helper()
	c.must("input", map[string]any{
		"window": winID,
		"events": []any{map[string]any{"type": "pointerMove", "x": x, "y": y}},
	}, nil)
	c.idle(winID)
}

func assertPixelNear(t *testing.T, imgPixel color.Color, wantR, wantG, wantB uint8, tol int, label string) {
	t.Helper()
	got := color.NRGBAModel.Convert(imgPixel).(color.NRGBA)
	diff := func(a, b uint8) int {
		if a > b {
			return int(a - b)
		}
		return int(b - a)
	}
	if diff(got.R, wantR) > tol || diff(got.G, wantG) > tol || diff(got.B, wantB) > tol {
		t.Fatalf("%s pixel = rgb(%d,%d,%d), want near rgb(%d,%d,%d) (tol=%d)", label, got.R, got.G, got.B, wantR, wantG, wantB, tol)
	}
}

func sampleNodeCorner(c *autoClient, winID int, content autoRect, b autoRect) color.Color {
	c.t.Helper()
	img, w, h := c.capture(winID)
	scaleX := float64(w) / content.Width
	scaleY := float64(h) / content.Height
	px := int(math.Round((b.X + 2) * scaleX))
	py := int(math.Round((b.Y + 2) * scaleY))
	return img.At(px, py)
}

func TestE2EStartupSingleInstanceTrayAndExit(t *testing.T) {
	inst := newE2EInstall(t)
	if _, err := os.Stat(inst.dataDir); !os.IsNotExist(err) {
		t.Fatalf("expected Data dir to be absent before first run, got %v", err)
	}

	proc := inst.start()
	c := proc.client
	win := c.waitWindow("SyncMaid", boolPtr(true), 15*time.Second)
	c.idle(win.ID)

	for _, locked := range []string{
		inst.dataDir,
		filepath.Join(inst.dir, ".syncmaid-owner.lock"),
		filepath.Join(inst.dataDir, ".syncmaid-owner.lock"),
	} {
		if _, err := os.Stat(locked); err != nil {
			t.Fatalf("expected ownership path %s: %v", locked, err)
		}
	}

	img, w, h := c.capture(win.ID)
	if w < 760 || h < 480 || img.Bounds().Dx() != w || img.Bounds().Dy() != h {
		t.Fatalf("unexpected captured window image %dx%d (%v)", w, h, img.Bounds())
	}

	gear := c.waitNode(win.ID, 5*time.Second, "settings button", func(n autoNode) bool {
		return n.Role == "button" && !n.disabled() && (n.Label == "设置" || n.Label == "Settings" || n.Label == "設定")
	})
	c.pressNode(win.ID, gear)
	c.press(win.ID, "button", "简体中文")
	c.waitText(win.ID, "界面语言", 5*time.Second)
	c.waitText(win.ID, "关闭到托盘", 5*time.Second)

	closeSwitch := c.waitNode(win.ID, 5*time.Second, "close-to-tray switch", func(n autoNode) bool {
		return n.Role == "switch" && strings.HasPrefix(n.Label, "关闭窗口：") && !n.disabled()
	})
	c.pressNode(win.ID, closeSwitch)

	settingsPath := filepath.Join(inst.dataDir, "settings-go.json")
	waitUntil(t, 5*time.Second, "CloseToTray saved", func() bool {
		b, err := os.ReadFile(settingsPath)
		return err == nil && strings.Contains(string(b), `"CloseToTray": true`) || strings.Contains(string(b), `"CloseToTray":true`)
	})

	// Verify danger button ("退出") rest and hover states.
	exitBtn := c.waitNode(win.ID, 5*time.Second, "settings exit danger button", func(n autoNode) bool {
		return n.Role == "button" && n.Label == "退出" && !n.disabled()
	})
	for _, act := range exitBtn.Actions {
		if act == "scrollIntoView" {
			c.must("act", map[string]any{"window": win.ID, "id": exitBtn.ID, "action": "scrollIntoView"}, nil)
			c.idle(win.ID)
			exitBtn = c.waitNode(win.ID, 5*time.Second, "settings exit danger button after scroll", func(n autoNode) bool {
				return n.Role == "button" && n.Label == "退出" && !n.disabled()
			})
			break
		}
	}
	movePointer(c, win.ID, 8, 8)
	assertPixelNear(t, sampleNodeCorner(c, win.ID, win.ContentBounds, exitBtn.Bounds), 251, 236, 238, 4, "danger button rest")
	movePointer(c, win.ID, exitBtn.Bounds.X+exitBtn.Bounds.Width/2, exitBtn.Bounds.Y+exitBtn.Bounds.Height/2)
	assertPixelNear(t, sampleNodeCorner(c, win.ID, win.ContentBounds, exitBtn.Bounds), 198, 47, 60, 4, "danger button hover")

	// Verify unified stacked rail nav buttons (Tasks, Topology, History, Settings): uniform 64x54 size, neutral rail/hover gray, active white surface.
	tasksNav := c.waitNode(win.ID, 5*time.Second, "tasks nav button", func(n autoNode) bool {
		return n.Role == "button" && n.Label == "任务" && !n.disabled()
	})
	topoNav := c.waitNode(win.ID, 5*time.Second, "topology nav button", func(n autoNode) bool {
		return n.Role == "button" && n.Label == "拓扑" && !n.disabled()
	})
	histNav := c.waitNode(win.ID, 5*time.Second, "history nav button", func(n autoNode) bool {
		return n.Role == "button" && n.Label == "历史" && !n.disabled()
	})
	gearZh := c.waitNode(win.ID, 5*time.Second, "settings nav button", func(n autoNode) bool {
		return n.Role == "button" && n.Label == "设置" && !n.disabled()
	})
	for _, nav := range []autoNode{tasksNav, topoNav, histNav, gearZh} {
		if math.Abs(nav.Bounds.Width-64) > 0.5 || math.Abs(nav.Bounds.Height-54) > 0.5 {
			t.Fatalf("rail nav button geometry mismatch for %q: %+v", nav.Label, nav.Bounds)
		}
	}

	movePointer(c, win.ID, 8, 8)
	activeGearSample := autoRect{X: gearZh.Bounds.X + gearZh.Bounds.Width - 8, Y: gearZh.Bounds.Y + 4, Width: 4, Height: 4}
	assertPixelNear(t, sampleNodeCorner(c, win.ID, win.ContentBounds, activeGearSample), 255, 255, 255, 4, "gear active rest")
	assertPixelNear(t, sampleNodeCorner(c, win.ID, win.ContentBounds, tasksNav.Bounds), 242, 242, 243, 4, "tasks inactive rest")

	movePointer(c, win.ID, tasksNav.Bounds.X+tasksNav.Bounds.Width/2, tasksNav.Bounds.Y+tasksNav.Bounds.Height/2)
	assertPixelNear(t, sampleNodeCorner(c, win.ID, win.ContentBounds, tasksNav.Bounds), 234, 234, 235, 4, "tasks inactive hover")

	c.pressNode(win.ID, gearZh)
	c.waitNoText(win.ID, "关闭到托盘", 5*time.Second)

	movePointer(c, win.ID, 8, 8)
	assertPixelNear(t, sampleNodeCorner(c, win.ID, win.ContentBounds, gearZh.Bounds), 242, 242, 243, 4, "gear inactive rest")
	movePointer(c, win.ID, gearZh.Bounds.X+gearZh.Bounds.Width/2, gearZh.Bounds.Y+gearZh.Bounds.Height/2)
	assertPixelNear(t, sampleNodeCorner(c, win.ID, win.ContentBounds, gearZh.Bounds), 234, 234, 235, 4, "gear inactive hover")

	// Verify Klein Blue primary button ("+ 新建任务"): #002FA7 at rest, lighter #1B4FD8 on hover.
	newBtn := c.waitNode(win.ID, 5*time.Second, "new task button", func(n autoNode) bool {
		return n.Role == "button" && n.Label == "+ 新建任务" && !n.disabled()
	})
	movePointer(c, win.ID, 8, 8)
	assertPixelNear(t, sampleNodeCorner(c, win.ID, win.ContentBounds, newBtn.Bounds), 0, 47, 167, 4, "new task Klein Blue rest")
	movePointer(c, win.ID, newBtn.Bounds.X+newBtn.Bounds.Width/2, newBtn.Bounds.Y+newBtn.Bounds.Height/2)
	assertPixelNear(t, sampleNodeCorner(c, win.ID, win.ContentBounds, newBtn.Bounds), 27, 79, 216, 4, "new task Klein Blue hover")

	// Verify Level 1 icon rail navigation across Tasks, Topology, and History.
	c.press(win.ID, "button", "拓扑")
	c.waitText(win.ID, "暂无同步任务", 5*time.Second)
	c.press(win.ID, "button", "历史")
	c.waitText(win.ID, "近期运行记录", 5*time.Second)
	c.press(win.ID, "button", "任务")
	c.waitText(win.ID, "+ 新建任务", 5*time.Second)

	if err := sendWMClose(proc.pid(), "SyncMaid"); err != nil {
		t.Fatal(err)
	}
	c.waitWindow("SyncMaid", boolPtr(false), 10*time.Second)

	out, err := inst.runSecondInstance()
	if err != nil {
		t.Fatalf("second instance activation failed: %v\noutput:\n%s", err, out)
	}
	c.waitWindow("SyncMaid", boolPtr(true), 10*time.Second)

	c.quitViaSettings(win.ID, "设置", "退出")

	// Re-launch on the same install directory to prove file locks and socket were released cleanly.
	proc2 := inst.start()
	win2 := proc2.client.waitWindow("SyncMaid", boolPtr(true), 15*time.Second)
	proc2.client.idle(win2.ID)
	proc2.client.quitViaSettings(win2.ID, "设置", "退出")
}

func TestE2ETaskLifecyclePreviewRestartSyncAndLogWindow(t *testing.T) {
	inst := newE2EInstall(t)
	inst.seedLocale("zh-Hans")

	sourceDir := filepath.Join(inst.workDir, "source")
	targetTxt := filepath.Join(inst.workDir, "target-txt")
	targetAll := filepath.Join(inst.workDir, "target-all")
	writeTestFile(t, filepath.Join(sourceDir, "notes.txt"), "hello")
	writeTestFile(t, filepath.Join(sourceDir, "sub", "keep.txt"), "nested")
	writeTestFile(t, filepath.Join(sourceDir, "photo.jpg"), "img")
	if err := os.MkdirAll(targetTxt, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(targetAll, 0o700); err != nil {
		t.Fatal(err)
	}

	proc := inst.start()
	c := proc.client
	win := c.waitWindow("SyncMaid", boolPtr(true), 15*time.Second)
	c.idle(win.ID)
	c.waitText(win.ID, "+ 新建任务", 5*time.Second)

	// Verify blank backdrop click keeps the editor open, while Escape and Cancel discard the draft.
	c.press(win.ID, "button", "+ 新建任务")
	c.setValue(win.ID, "任务名称", "Discard Draft")
	c.must("input", map[string]any{
		"window": win.ID,
		"events": []any{map[string]any{"type": "click", "x": 8, "y": 300}},
	}, nil)
	c.idle(win.ID)
	c.waitText(win.ID, "保存任务", 5*time.Second)

	c.must("input", map[string]any{
		"window": win.ID,
		"events": []any{map[string]any{"type": "key", "key": "Escape"}},
	}, nil)
	c.idle(win.ID)
	c.waitNoText(win.ID, "保存任务", 5*time.Second)
	c.waitNoText(win.ID, "Discard Draft", 5*time.Second)

	c.press(win.ID, "button", "+ 新建任务")
	c.press(win.ID, "button", "取消")
	c.waitNoText(win.ID, "保存任务", 5*time.Second)

	c.press(win.ID, "button", "+ 新建任务")
	c.setValue(win.ID, "任务名称", "E2E Sync")
	c.setValue(win.ID, "源文件夹路径", sourceDir)
	c.press(win.ID, "button", "保存任务")
	c.waitNoText(win.ID, "保存任务", 10*time.Second)
	c.waitText(win.ID, "E2E Sync", 5*time.Second)
	c.waitText(win.ID, "尚未添加目标", 5*time.Second)

	c.pressAfter(win.ID, "E2E Sync", "button", "+ 添加目标")
	c.waitText(win.ID, "返回目标列表", 5*time.Second)
	c.waitText(win.ID, "源预览", 5*time.Second)
	c.setValue(win.ID, "目标名称", "TextOnly")
	c.setValue(win.ID, "目标文件夹路径", targetTxt)
	c.press(win.ID, "button", "仅匹配")
	c.press(win.ID, "button", "扩展名")
	c.setValue(win.ID, "新规则", "txt")
	c.press(win.ID, "button", "添加")
	c.waitText(win.ID, "扩展名 txt", 5*time.Second)
	c.press(win.ID, "button", "保存")
	c.waitNoText(win.ID, "目标文件夹路径", 10*time.Second)

	c.press(win.ID, "button", "+ 添加目标")
	c.waitText(win.ID, "返回目标列表", 5*time.Second)
	c.setValue(win.ID, "目标名称", "AllFiles")
	c.setValue(win.ID, "目标文件夹路径", targetAll)

	c.press(win.ID, "button", "扫描")
	c.waitText(win.ID, "3 文件 · 1 文件夹", 10*time.Second)
	c.waitText(win.ID, "3 (100.0%)", 5*time.Second)
	c.setValue(win.ID, "搜索源路径", "notes.txt")
	c.waitText(win.ID, "✓ 命中", 5*time.Second)
	c.press(win.ID, "button", "清除")

	c.press(win.ID, "button", "保存")
	c.waitNoText(win.ID, "目标文件夹路径", 10*time.Second)
	c.waitText(win.ID, "2 (66.7%)", 5*time.Second)
	c.waitText(win.ID, "3 (100.0%)", 5*time.Second)
	c.press(win.ID, "button", "源预览")
	c.waitNoText(win.ID, "快照", 5*time.Second)
	c.waitText(win.ID, "TextOnly", 5*time.Second)
	c.waitText(win.ID, "AllFiles", 5*time.Second)

	// Full restart to verify tasks-go.json persistence.
	c.quitViaSettings(win.ID, "设置", "退出")

	proc = inst.start()
	c = proc.client
	win = c.waitWindow("SyncMaid", boolPtr(true), 15*time.Second)
	c.idle(win.ID)
	c.waitText(win.ID, "E2E Sync", 5*time.Second)
	c.waitText(win.ID, "TextOnly", 5*time.Second)
	c.waitText(win.ID, "AllFiles", 5*time.Second)

	c.pressAfter(win.ID, "E2E Sync", "button", "▶ 运行")
	c.waitText(win.ID, "全部已同步", 15*time.Second)
	c.waitText(win.ID, "刚刚同步 · 2个文件", 5*time.Second)
	c.waitText(win.ID, "刚刚同步 · 3个文件", 5*time.Second)

	if got := readTestFile(t, filepath.Join(targetTxt, "notes.txt")); got != "hello" {
		t.Fatalf("notes.txt = %q", got)
	}
	if got := readTestFile(t, filepath.Join(targetTxt, "sub", "keep.txt")); got != "nested" {
		t.Fatalf("sub/keep.txt = %q", got)
	}
	assertNoFile(t, filepath.Join(targetTxt, "photo.jpg"))
	if got := readTestFile(t, filepath.Join(targetAll, "photo.jpg")); got != "img" {
		t.Fatalf("photo.jpg = %q", got)
	}
	if _, err := os.Stat(filepath.Join(inst.dataDir, "run-history-go.json")); err != nil {
		t.Fatalf("run-history-go.json missing: %v", err)
	}

	c.pressContains(win.ID, "link", "2个文件")
	c.waitText(win.ID, "近期运行记录", 5*time.Second)
	c.waitText(win.ID, "TextOnly", 5*time.Second)
	c.waitText(win.ID, "已定位", 5*time.Second)
	c.waitText(win.ID, "完成", 5*time.Second)
	c.waitText(win.ID, "已发布 2", 5*time.Second)

	c.quitViaSettings(win.ID, "设置", "退出")
}

func TestE2EMirrorConfirmDeclineCloseAndDelete(t *testing.T) {
	inst := newE2EInstall(t)
	inst.seedLocale("zh-Hans")
	inst.seedSettings(`{"CloseToTray":true,"StartMinimized":false}`)

	sourceDir := filepath.Join(inst.workDir, "source")
	targetPerm := filepath.Join(inst.workDir, "target-perm")
	targetRecycle := filepath.Join(inst.workDir, "target-recycle")
	writeTestFile(t, filepath.Join(sourceDir, "keep.txt"), "stay")
	writeTestFile(t, filepath.Join(targetPerm, "keep.txt"), "stay")
	extraPerm := filepath.Join(targetPerm, "extra-perm.txt")
	writeTestFile(t, extraPerm, "delete-me")
	writeTestFile(t, filepath.Join(targetRecycle, "keep.txt"), "stay")
	extraRecycle := filepath.Join(targetRecycle, "syncmaid-e2e-recycle.txt")
	writeTestFile(t, extraRecycle, "trash-me")
	for i := 1; i <= 8; i++ {
		writeTestFile(t, filepath.Join(targetPerm, fmt.Sprintf("extra-guard-%d.txt", i)), "guard")
		writeTestFile(t, filepath.Join(targetRecycle, fmt.Sprintf("extra-guard-%d.txt", i)), "guard")
	}

	proc := inst.start()
	c := proc.client
	win := c.waitWindow("SyncMaid", boolPtr(true), 15*time.Second)
	c.idle(win.ID)

	c.press(win.ID, "button", "+ 新建任务")
	c.setValue(win.ID, "任务名称", "E2E Mirror")
	c.setValue(win.ID, "源文件夹路径", sourceDir)
	c.press(win.ID, "button", "保存任务")
	c.waitNoText(win.ID, "保存任务", 10*time.Second)

	c.pressAfter(win.ID, "E2E Mirror", "button", "+ 添加目标")
	c.setValue(win.ID, "目标名称", "PermDest")
	c.setValue(win.ID, "目标文件夹路径", targetPerm)
	c.press(win.ID, "button", "镜像")
	c.press(win.ID, "button", "永久删除")
	c.press(win.ID, "button", "保存")
	c.waitNoText(win.ID, "目标文件夹路径", 10*time.Second)

	// 1) Decline via button.
	c.pressAfter(win.ID, "E2E Mirror", "button", "▶ 运行")
	c.waitText(win.ID, "确认本次Mirror", 10*time.Second)
	c.waitText(win.ID, "extra-perm.txt", 5*time.Second)
	c.press(win.ID, "button", "取消本次移除")
	c.waitNoText(win.ID, "确认本次Mirror", 10*time.Second)
	if got := readTestFile(t, extraPerm); got != "delete-me" {
		t.Fatalf("declined file changed: %q", got)
	}

	// 2) Decline via WM_CLOSE before hiding to tray.
	c.pressAfter(win.ID, "E2E Mirror", "button", "▶ 运行")
	c.waitText(win.ID, "确认本次Mirror", 10*time.Second)
	if err := sendWMClose(proc.pid(), "SyncMaid"); err != nil {
		t.Fatal(err)
	}
	c.waitWindow("SyncMaid", boolPtr(false), 10*time.Second)
	if out, err := inst.runSecondInstance(); err != nil {
		t.Fatalf("activation after WM_CLOSE failed: %v\n%s", err, out)
	}
	c.waitWindow("SyncMaid", boolPtr(true), 10*time.Second)
	c.waitNoText(win.ID, "确认本次Mirror", 10*time.Second)
	c.waitText(win.ID, "+ 新建任务", 5*time.Second)
	if got := readTestFile(t, extraPerm); got != "delete-me" {
		t.Fatalf("WM_CLOSE did not decline removal: %q", got)
	}

	// 3) Confirm permanent removal.
	c.pressAfter(win.ID, "E2E Mirror", "button", "▶ 运行")
	c.waitText(win.ID, "确认本次Mirror", 10*time.Second)
	c.press(win.ID, "button", "确认本次移除")
	c.waitNoText(win.ID, "确认本次Mirror", 10*time.Second)
	c.waitText(win.ID, "全部已同步", 15*time.Second)
	if got := readTestFile(t, filepath.Join(targetPerm, "keep.txt")); got != "stay" {
		t.Fatalf("keep.txt = %q", got)
	}
	assertNoFile(t, extraPerm)

	// 4) Real Windows Shell Recycle Bin deletion.
	c.pressAfter(win.ID, "E2E Mirror", "button", "+ 添加目标")
	c.setValue(win.ID, "目标名称", "RecycleDest")
	c.setValue(win.ID, "目标文件夹路径", targetRecycle)
	c.press(win.ID, "button", "镜像")
	c.press(win.ID, "button", "保存")
	c.waitNoText(win.ID, "目标文件夹路径", 10*time.Second)

	c.pressAfter(win.ID, "E2E Mirror", "button", "▶ 运行")
	c.waitText(win.ID, "确认本次Mirror", 10*time.Second)
	c.waitText(win.ID, "移至回收站", 5*time.Second)
	c.waitText(win.ID, "syncmaid-e2e-recycle.txt", 5*time.Second)
	c.press(win.ID, "button", "确认本次移除")
	c.waitNoText(win.ID, "确认本次Mirror", 10*time.Second)
	c.waitText(win.ID, "全部已同步", 15*time.Second)
	if got := readTestFile(t, filepath.Join(targetRecycle, "keep.txt")); got != "stay" {
		t.Fatalf("recycle keep.txt = %q", got)
	}
	assertNoFile(t, extraRecycle)

	c.quitViaSettings(win.ID, "设置", "退出")
}

func TestE2EMoveRoutingSuffixAndStop(t *testing.T) {
	inst := newE2EInstall(t)
	inst.seedLocale("zh-Hans")

	sourceDir := filepath.Join(inst.workDir, "move-src")
	txtTarget := filepath.Join(inst.workDir, "move-txt")
	restTarget := filepath.Join(inst.workDir, "move-rest")
	writeTestFile(t, filepath.Join(sourceDir, "a.txt"), "from-source")
	writeTestFile(t, filepath.Join(sourceDir, "sub", "b.txt"), "nested")
	writeTestFile(t, filepath.Join(sourceDir, "other.bin"), "binary")
	writeTestFile(t, filepath.Join(txtTarget, "a.txt"), "already-here")
	if err := os.MkdirAll(restTarget, 0o700); err != nil {
		t.Fatal(err)
	}

	proc := inst.start()
	c := proc.client
	win := c.waitWindow("SyncMaid", boolPtr(true), 15*time.Second)
	c.idle(win.ID)

	c.press(win.ID, "button", "+ 新建任务")
	c.setValue(win.ID, "任务名称", "E2E Move")
	c.setValue(win.ID, "源文件夹路径", sourceDir)
	c.press(win.ID, "button", "移动")
	c.press(win.ID, "button", "保存任务")
	c.waitNoText(win.ID, "保存任务", 10*time.Second)

	c.pressAfter(win.ID, "E2E Move", "button", "+ 添加目标")
	c.setValue(win.ID, "目标名称", "Texts")
	c.setValue(win.ID, "目标文件夹路径", txtTarget)
	c.press(win.ID, "button", "加编号")
	c.press(win.ID, "button", "仅匹配")
	c.press(win.ID, "button", "扩展名")
	c.setValue(win.ID, "新规则", "txt")
	c.press(win.ID, "button", "添加")
	c.waitText(win.ID, "扩展名 txt", 5*time.Second)
	c.press(win.ID, "button", "保存")
	c.waitNoText(win.ID, "目标文件夹路径", 10*time.Second)

	c.press(win.ID, "button", "+ 添加目标")
	c.setValue(win.ID, "目标名称", "Rest")
	c.setValue(win.ID, "目标文件夹路径", restTarget)
	c.press(win.ID, "button", "保存")
	c.waitNoText(win.ID, "目标文件夹路径", 10*time.Second)

	c.pressAfter(win.ID, "E2E Move", "button", "▶ 运行")
	c.waitText(win.ID, "全部已同步", 15*time.Second)

	assertNoFile(t, filepath.Join(sourceDir, "a.txt"))
	assertNoFile(t, filepath.Join(sourceDir, "sub", "b.txt"))
	assertNoFile(t, filepath.Join(sourceDir, "other.bin"))
	if got := readTestFile(t, filepath.Join(txtTarget, "a.txt")); got != "already-here" {
		t.Fatalf("existing a.txt overwritten: %q", got)
	}
	if got := readTestFile(t, filepath.Join(txtTarget, "a (2).txt")); got != "from-source" {
		t.Fatalf("numbered suffix file = %q", got)
	}
	if got := readTestFile(t, filepath.Join(txtTarget, "sub", "b.txt")); got != "nested" {
		t.Fatalf("sub/b.txt = %q", got)
	}
	if got := readTestFile(t, filepath.Join(restTarget, "other.bin")); got != "binary" {
		t.Fatalf("other.bin = %q", got)
	}

	// Add a Mirror task + a follow-up task and run batch to test Stop mid-run & queue clearing.
	mirrorSrc := filepath.Join(inst.workDir, "mirror-src")
	mirrorDst := filepath.Join(inst.workDir, "mirror-dst")
	afterSrc := filepath.Join(inst.workDir, "after-src")
	afterDst := filepath.Join(inst.workDir, "after-dst")
	writeTestFile(t, filepath.Join(mirrorSrc, "one.txt"), "one")
	writeTestFile(t, filepath.Join(mirrorDst, "one.txt"), "one")
	extraKeep := filepath.Join(mirrorDst, "extra-keep.txt")
	writeTestFile(t, extraKeep, "keep")
	for i := 1; i <= 8; i++ {
		writeTestFile(t, filepath.Join(mirrorDst, fmt.Sprintf("extra-guard-%d.txt", i)), "guard")
	}
	writeTestFile(t, filepath.Join(afterSrc, "later.txt"), "later")
	if err := os.MkdirAll(afterDst, 0o700); err != nil {
		t.Fatal(err)
	}

	c.press(win.ID, "button", "+ 新建任务")
	c.setValue(win.ID, "任务名称", "Mirror Stop")
	c.setValue(win.ID, "源文件夹路径", mirrorSrc)
	c.press(win.ID, "button", "保存任务")
	c.waitNoText(win.ID, "保存任务", 10*time.Second)
	c.pressAfter(win.ID, "Mirror Stop", "button", "+ 添加目标")
	c.setValue(win.ID, "目标名称", "MirrorDst")
	c.setValue(win.ID, "目标文件夹路径", mirrorDst)
	c.press(win.ID, "button", "镜像")
	c.press(win.ID, "button", "永久删除")
	c.press(win.ID, "button", "保存")
	c.waitNoText(win.ID, "目标文件夹路径", 10*time.Second)

	c.press(win.ID, "button", "+ 新建任务")
	c.setValue(win.ID, "任务名称", "After Stop")
	c.setValue(win.ID, "源文件夹路径", afterSrc)
	c.press(win.ID, "button", "保存任务")
	c.waitNoText(win.ID, "保存任务", 10*time.Second)
	c.pressAfter(win.ID, "After Stop", "button", "+ 添加目标")
	c.setValue(win.ID, "目标名称", "AfterDst")
	c.setValue(win.ID, "目标文件夹路径", afterDst)
	c.press(win.ID, "button", "保存")
	c.waitNoText(win.ID, "目标文件夹路径", 10*time.Second)

	c.press(win.ID, "button", "全部运行")
	c.waitText(win.ID, "确认本次Mirror", 10*time.Second)
	c.waitText(win.ID, "批次 2/3", 5*time.Second)
	c.press(win.ID, "button", "停止")
	c.waitNoText(win.ID, "确认本次Mirror", 10*time.Second)
	c.waitText(win.ID, "+ 新建任务", 5*time.Second)
	c.waitNode(win.ID, 10*time.Second, "enabled Run all button after stop", func(n autoNode) bool {
		return n.Role == "button" && n.Label == "全部运行" && !n.disabled()
	})

	if got := readTestFile(t, extraKeep); got != "keep" {
		t.Fatalf("stopped mirror modified file: %q", got)
	}
	assertNoFile(t, filepath.Join(afterDst, "later.txt"))
	assertNoTempFiles(t, txtTarget, restTarget, mirrorDst, afterDst)

	c.quitViaSettings(win.ID, "设置", "退出")
}

func TestE2ERealWatchFilesTriggerAndPause(t *testing.T) {
	inst := newE2EInstall(t)
	inst.seedLocale("zh-Hans")

	sourceDir := filepath.Join(inst.workDir, "watch-src")
	targetDir := filepath.Join(inst.workDir, "watch-dst")
	writeTestFile(t, filepath.Join(sourceDir, "initial.txt"), "v1")
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}

	proc := inst.start()
	c := proc.client
	win := c.waitWindow("SyncMaid", boolPtr(true), 15*time.Second)
	c.idle(win.ID)

	c.press(win.ID, "button", "+ 新建任务")
	c.setValue(win.ID, "任务名称", "E2E Watch")
	c.setValue(win.ID, "源文件夹路径", sourceDir)
	c.press(win.ID, "button", "监视")
	c.setValue(win.ID, "静默秒数", "1")
	c.press(win.ID, "button", "保存任务")
	c.waitNoText(win.ID, "保存任务", 10*time.Second)

	c.pressAfter(win.ID, "E2E Watch", "button", "+ 添加目标")
	c.setValue(win.ID, "目标名称", "WatchedTarget")
	c.setValue(win.ID, "目标文件夹路径", targetDir)
	c.press(win.ID, "button", "保存")
	c.waitNoText(win.ID, "目标文件夹路径", 10*time.Second)

	waitUntil(t, 15*time.Second, "initial watch-triggered sync", func() bool {
		b, err := os.ReadFile(filepath.Join(targetDir, "initial.txt"))
		return err == nil && string(b) == "v1"
	})
	c.waitText(win.ID, "全部已同步", 10*time.Second)

	// Trigger real mygo.WatchFiles OS events after enrollment.
	writeTestFile(t, filepath.Join(sourceDir, "live.txt"), "live")
	writeTestFile(t, filepath.Join(sourceDir, "initial.txt"), "v2")
	waitUntil(t, 15*time.Second, "live WatchFiles sync", func() bool {
		b1, e1 := os.ReadFile(filepath.Join(targetDir, "live.txt"))
		b2, e2 := os.ReadFile(filepath.Join(targetDir, "initial.txt"))
		return e1 == nil && e2 == nil && string(b1) == "live" && string(b2) == "v2"
	})

	c.press(win.ID, "button", "暂停自动")
	c.waitText(win.ID, "自动触发已暂停", 5*time.Second)
	writeTestFile(t, filepath.Join(sourceDir, "paused.txt"), "paused")
	time.Sleep(2500 * time.Millisecond)
	assertNoFile(t, filepath.Join(targetDir, "paused.txt"))

	c.press(win.ID, "button", "恢复自动触发")
	waitUntil(t, 15*time.Second, "resumed WatchFiles sync", func() bool {
		b, err := os.ReadFile(filepath.Join(targetDir, "paused.txt"))
		return err == nil && string(b) == "paused"
	})

	c.quitViaSettings(win.ID, "设置", "退出")
}

func TestE2ELanguageSwitchPersistenceAndBackupRecovery(t *testing.T) {
	inst := newE2EInstall(t)
	inst.seedLocale("zh-Hans")

	sourceDir := filepath.Join(inst.workDir, "recover-src")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}

	proc := inst.start()
	c := proc.client
	win := c.waitWindow("SyncMaid", boolPtr(true), 15*time.Second)
	c.idle(win.ID)

	// Part A: Switch languages and verify Data/locale persistence across restart.
	c.press(win.ID, "button", "设置")
	c.press(win.ID, "button", "English")
	c.waitText(win.ID, "Settings", 5*time.Second)
	c.waitText(win.ID, "Open data folder", 5*time.Second)
	localePath := filepath.Join(inst.dataDir, "locale")
	waitUntil(t, 5*time.Second, "Data/locale == en", func() bool {
		return strings.TrimSpace(readTestFile(t, localePath)) == "en"
	})

	c.press(win.ID, "button", "日本語")
	c.waitText(win.ID, "設定", 5*time.Second)
	c.waitText(win.ID, "データフォルダーを開く", 5*time.Second)
	waitUntil(t, 5*time.Second, "Data/locale == ja", func() bool {
		return strings.TrimSpace(readTestFile(t, localePath)) == "ja"
	})
	c.quitViaSettings(win.ID, "設定", "終了")

	proc = inst.start()
	c = proc.client
	win = c.waitWindow("SyncMaid", boolPtr(true), 15*time.Second)
	c.idle(win.ID)
	c.waitText(win.ID, "設定", 5*time.Second)
	c.waitText(win.ID, "新規タスク", 5*time.Second)

	c.press(win.ID, "button", "設定")
	c.press(win.ID, "button", "简体中文")
	c.waitText(win.ID, "界面语言", 5*time.Second)
	c.waitText(win.ID, "打开数据目录", 5*time.Second)
	waitUntil(t, 5*time.Second, "Data/locale == zh-Hans", func() bool {
		return strings.TrimSpace(readTestFile(t, localePath)) == "zh-Hans"
	})
	gearCount := 0
	for _, n := range c.tree(win.ID) {
		if n.Role == "button" && n.Label == "设置" {
			gearCount++
		}
	}
	if gearCount != 1 {
		t.Fatalf("expected exactly 1 settings gear button while settings open, got %d", gearCount)
	}
	c.press(win.ID, "button", "设置")

	// Part B: Save twice to create tasks-go.json + tasks-go.json.bak, corrupt primary, recover via UI.
	c.press(win.ID, "button", "+ 新建任务")
	c.setValue(win.ID, "任务名称", "Recoverable")
	c.setValue(win.ID, "源文件夹路径", sourceDir)
	c.press(win.ID, "button", "保存任务")
	c.waitNoText(win.ID, "保存任务", 10*time.Second)
	c.waitText(win.ID, "Recoverable", 5*time.Second)

	c.pressAfter(win.ID, "Recoverable", "button", "编辑任务")
	c.setValue(win.ID, "任务名称", "Recoverable V2")
	c.press(win.ID, "button", "保存任务")
	c.waitNoText(win.ID, "保存任务", 10*time.Second)
	c.waitText(win.ID, "Recoverable V2", 5*time.Second)

	c.quitViaSettings(win.ID, "设置", "退出")

	tasksPath := filepath.Join(inst.dataDir, "tasks-go.json")
	if _, err := os.Stat(tasksPath + ".bak"); err != nil {
		t.Fatalf("expected backup file %s.bak: %v", tasksPath, err)
	}
	const corruptBytes = "{corrupt-json"
	writeTestFile(t, tasksPath, corruptBytes)

	proc = inst.start("SYNCMAID_E2E_DIALOG_BUTTON=1")
	c = proc.client
	win = c.waitWindow("SyncMaid", boolPtr(true), 15*time.Second)
	c.idle(win.ID)
	c.waitText(win.ID, "任务文件不可用，已阻止保存与运行", 5*time.Second)
	c.press(win.ID, "button", "恢复任务备份")
	c.waitText(win.ID, "任务备份已恢复", 10*time.Second)
	c.waitText(win.ID, "Recoverable", 5*time.Second)

	restored := readTestFile(t, tasksPath)
	var parsed any
	if err := json.Unmarshal([]byte(restored), &parsed); err != nil {
		t.Fatalf("recovered tasks-go.json invalid JSON: %v\n%s", err, restored)
	}
	if !strings.Contains(restored, "Recoverable") {
		t.Fatalf("recovered tasks-go.json missing task: %s", restored)
	}
	rejected, err := filepath.Glob(filepath.Join(inst.dataDir, "tasks-go.json.rejected-*"))
	if err != nil || len(rejected) != 1 || readTestFile(t, rejected[0]) != corruptBytes {
		t.Fatalf("expected rejected corrupt archive with original bytes, got %v err=%v", rejected, err)
	}

	c.quitViaSettings(win.ID, "设置", "退出")
}

func TestE2ELegacyImportAndRegexMigration(t *testing.T) {
	inst := newE2EInstall(t)
	inst.seedLocale("zh-Hans")

	sourceDir := filepath.Join(inst.workDir, "legacy-src")
	targetDir := filepath.Join(inst.workDir, "legacy-dst")
	writeTestFile(t, filepath.Join(sourceDir, "match.txt"), "yes")
	writeTestFile(t, filepath.Join(sourceDir, "skip.bin"), "no")
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}

	legacyPath := filepath.Join(inst.workDir, "legacy-tasks.json")
	legacyPayload, err := json.Marshal([]map[string]any{{
		"Name":       "Legacy Regex Task",
		"SourcePath": sourceDir,
		"Trigger":    map[string]any{"kind": "watch", "SettleSeconds": 10},
		"Kind":       "Sync",
		"Destinations": []map[string]any{{
			"Name":     "RegexDest",
			"Target":   map[string]any{"kind": "local", "Path": targetDir},
			"Strategy": "AddOnly",
			"Filters":  []map[string]any{{"kind": "regex", "Pattern": `\.txt$`}},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, legacyPath, string(legacyPayload))

	proc := inst.start("SYNCMAID_E2E_OPEN_PATH=" + legacyPath)
	c := proc.client
	win := c.waitWindow("SyncMaid", boolPtr(true), 15*time.Second)
	c.idle(win.ID)

	c.press(win.ID, "button", "设置")
	c.press(win.ID, "button", "导入旧任务")
	c.waitText(win.ID, "旧任务导入预览", 10*time.Second)
	c.waitText(win.ID, "Legacy Regex Task", 5*time.Second)
	c.press(win.ID, "button", "确认追加并设为手动")
	c.waitText(win.ID, "+ 新建任务", 10*time.Second)
	c.waitText(win.ID, "Legacy Regex Task", 5*time.Second)
	c.waitText(win.ID, "手动", 5*time.Second)

	if got := readTestFile(t, legacyPath); got != string(legacyPayload) {
		t.Fatalf("legacy tasks.json was modified")
	}

	// Unconfirmed regex must block execution before migration.
	c.pressAfter(win.ID, "Legacy Regex Task", "button", "▶ 运行")
	c.waitText(win.ID, "1个中有1个失败", 10*time.Second)
	assertNoFile(t, filepath.Join(targetDir, "match.txt"))

	c.press(win.ID, "button", "Legacy Regex Task")
	c.press(win.ID, "button", "设置")
	c.press(win.ID, "button", "旧正则迁移")
	c.waitText(win.ID, "旧正则迁移", 5*time.Second)
	c.press(win.ID, "button", "预览新正则")
	c.waitText(win.ID, "匹配 · example.txt", 10*time.Second)
	c.press(win.ID, "button", "确认新语义并保存")
	c.waitText(win.ID, "确认已保存", 10*time.Second)
	c.press(win.ID, "button", "返回任务编辑")
	c.waitText(win.ID, "+ 新建任务", 5*time.Second)

	c.pressAfter(win.ID, "Legacy Regex Task", "button", "▶ 运行")
	c.waitText(win.ID, "全部已同步", 15*time.Second)
	if got := readTestFile(t, filepath.Join(targetDir, "match.txt")); got != "yes" {
		t.Fatalf("match.txt = %q", got)
	}
	assertNoFile(t, filepath.Join(targetDir, "skip.bin"))

	c.quitViaSettings(win.ID, "设置", "退出")
}

func TestE2ELargeSourcePreviewProfileAndTopologyCascade(t *testing.T) {
	inst := newE2EInstall(t)
	inst.seedLocale("zh-Hans")

	root := t.TempDir()
	sourceDir := filepath.Join(root, "BigSource")
	for i := 1; i <= 12; i++ {
		writeTestFile(t, filepath.Join(sourceDir, "Media", fmt.Sprintf("photo%02d.jpg", i)), "img")
	}
	for i := 1; i <= 10; i++ {
		writeTestFile(t, filepath.Join(sourceDir, "Docs", fmt.Sprintf("doc%02d.txt", i)), "doc")
	}
	for i := 1; i <= 4; i++ {
		writeTestFile(t, filepath.Join(sourceDir, fmt.Sprintf("note%02d.md", i)), "note")
	}

	mirrorDir := filepath.Join(root, "SMBMirror")
	for i := 1; i <= 15; i++ {
		writeTestFile(t, filepath.Join(mirrorDir, "OldA", fmt.Sprintf("old%02d.txt", i)), "stale-a")
	}
	for i := 1; i <= 10; i++ {
		writeTestFile(t, filepath.Join(mirrorDir, "OldB", fmt.Sprintf("bak%02d.txt", i)), "stale-b")
	}

	txtTargetDir := filepath.Join(root, "DocsOnlyTarget")
	cascadeDestDir := filepath.Join(root, "CascadeArchive")
	for _, dir := range []string{txtTargetDir, cascadeDestDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	proc := inst.start()
	c := proc.client
	win := c.waitWindow("SyncMaid", boolPtr(true), 15*time.Second)
	c.idle(win.ID)

	// 1. Create "Big Mirror" Sync task with all-files Mirror destination.
	c.press(win.ID, "button", "+ 新建任务")
	c.setValue(win.ID, "任务名称", "Big Mirror")
	c.setValue(win.ID, "源文件夹路径", sourceDir)
	c.press(win.ID, "button", "保存任务")
	c.waitNoText(win.ID, "保存任务", 10*time.Second)

	c.pressAfter(win.ID, "Big Mirror", "button", "+ 添加目标")
	c.setValue(win.ID, "目标名称", "SMB Mirror")
	c.setValue(win.ID, "目标文件夹路径", mirrorDir)
	c.press(win.ID, "button", "镜像")
	c.press(win.ID, "button", "永久删除")
	c.press(win.ID, "button", "保存")
	c.waitNoText(win.ID, "目标文件夹路径", 10*time.Second)

	// 2. Source Preview panel was opened alongside destination editing: all-files mode renders TopDirs + Extensions Profile & instant search.
	c.press(win.ID, "button", "扫描源目录")
	c.waitText(win.ID, "顶层目录分布", 10*time.Second)
	c.waitText(win.ID, "Media/", 5*time.Second)
	c.waitText(win.ID, "Docs/", 5*time.Second)
	c.waitText(win.ID, "高频扩展名", 5*time.Second)
	c.waitText(win.ID, ".jpg", 5*time.Second)

	c.setValue(win.ID, "搜索源路径", "photo01")
	c.waitText(win.ID, "命中 1 项", 5*time.Second)
	c.waitText(win.ID, "Media/photo01.jpg", 5*time.Second)
	c.press(win.ID, "button", "清除")
	c.waitText(win.ID, "顶层目录分布", 5*time.Second)

	// 3. Adding a filtered Add-only target shows live linkage in the side-by-side panel and switches to Sampled Buckets after saving.
	c.pressAfter(win.ID, "Big Mirror", "button", "+ 添加目标")
	c.waitText(win.ID, "返回目标列表", 5*time.Second)
	c.setValue(win.ID, "目标名称", "Docs Only")
	c.setValue(win.ID, "目标文件夹路径", txtTargetDir)
	c.press(win.ID, "button", "仅匹配")
	c.waitText(win.ID, "+ 点击直接加入规则", 5*time.Second)
	c.press(win.ID, "button", "扩展名")
	c.setValue(win.ID, "新规则", "txt")
	c.press(win.ID, "button", "添加")
	c.waitText(win.ID, "扩展名 txt", 5*time.Second)
	c.waitText(win.ID, "10 (38.5%)", 5*time.Second)
	c.press(win.ID, "button", "保存")
	c.waitNoText(win.ID, "目标文件夹路径", 10*time.Second)

	c.waitNoText(win.ID, "顶层目录分布", 5*time.Second)
	c.waitText(win.ID, "10 (38.5%)", 5*time.Second)
	c.press(win.ID, "button", "源预览")
	c.waitNoText(win.ID, "快照", 5*time.Second)

	// 4. Mirror confirmation with >20 extra files defaults to directory summary & toggles all paths.
	c.pressAfter(win.ID, "Big Mirror", "button", "▶ 运行")
	c.waitText(win.ID, "确认本次Mirror", 10*time.Second)
	c.waitText(win.ID, "按目录汇总", 5*time.Second)
	c.waitText(win.ID, "OldA/", 5*time.Second)
	c.waitText(win.ID, "OldB/", 5*time.Second)
	c.waitNoText(win.ID, "OldA/old01.txt", 3*time.Second)

	c.press(win.ID, "button", "全部路径")
	c.waitText(win.ID, "OldA/old01.txt", 5*time.Second)
	c.press(win.ID, "button", "取消本次移除")
	c.waitNoText(win.ID, "确认本次Mirror", 10*time.Second)

	// 5. Create downstream task sourced from mirrorDir & verify cascade link + Topology view.
	c.press(win.ID, "button", "+ 新建任务")
	c.setValue(win.ID, "任务名称", "Downstream Sync")
	c.setValue(win.ID, "源文件夹路径", mirrorDir)
	c.press(win.ID, "button", "保存任务")
	c.waitNoText(win.ID, "保存任务", 10*time.Second)

	c.pressAfter(win.ID, "Downstream Sync", "button", "+ 添加目标")
	c.setValue(win.ID, "目标名称", "Archive")
	c.setValue(win.ID, "目标文件夹路径", cascadeDestDir)
	c.press(win.ID, "button", "保存")
	c.waitNoText(win.ID, "目标文件夹路径", 10*time.Second)

	c.waitText(win.ID, "↳ 接力自 Big Mirror", 5*time.Second)

	c.press(win.ID, "button", "拓扑")
	c.waitText(win.ID, "Big Mirror", 5*time.Second)
	c.waitText(win.ID, "SMB Mirror", 5*time.Second)
	c.waitText(win.ID, "Docs Only", 5*time.Second)
	c.waitText(win.ID, "Archive", 5*time.Second)
	c.press(win.ID, "button", "Archive")
	c.waitText(win.ID, "↳ 接力自 Big Mirror", 5*time.Second)
	c.waitText(win.ID, "+ 新建任务", 5*time.Second)

	c.quitViaSettings(win.ID, "设置", "退出")
}

func TestE2EExecutionProgressBannerAndHistoryAudit(t *testing.T) {
	inst := newE2EInstall(t)
	inst.seedLocale("zh-Hans")

	sourceDir := filepath.Join(inst.workDir, "src")
	sourceDir2 := filepath.Join(inst.workDir, "src2")
	okTarget := filepath.Join(inst.workDir, "ok-target")
	missingTarget := filepath.Join(inst.workDir, "missing-target")
	writeTestFile(t, filepath.Join(sourceDir, "a.txt"), "alpha")
	writeTestFile(t, filepath.Join(sourceDir2, "b.txt"), "beta")
	if err := os.MkdirAll(okTarget, 0o700); err != nil {
		t.Fatal(err)
	}

	proc := inst.start()
	c := proc.client
	win := c.waitWindow("SyncMaid", boolPtr(true), 15*time.Second)
	c.idle(win.ID)

	// Task 1: healthy sync
	c.press(win.ID, "button", "+ 新建任务")
	c.setValue(win.ID, "任务名称", "GoodSync")
	c.setValue(win.ID, "源文件夹路径", sourceDir)
	c.press(win.ID, "button", "保存任务")
	c.waitNoText(win.ID, "保存任务", 10*time.Second)
	c.pressAfter(win.ID, "GoodSync", "button", "+ 添加目标")
	c.setValue(win.ID, "目标名称", "OkDest")
	c.setValue(win.ID, "目标文件夹路径", okTarget)
	c.press(win.ID, "button", "保存")
	c.waitNoText(win.ID, "目标文件夹路径", 10*time.Second)
	c.pressAfter(win.ID, "GoodSync", "button", "▶ 运行")
	c.waitText(win.ID, "全部已同步", 15*time.Second)

	// Task 2: target path does not exist -> fails cleanly and records error in history
	c.press(win.ID, "button", "+ 新建任务")
	c.setValue(win.ID, "任务名称", "FailSync")
	c.setValue(win.ID, "源文件夹路径", sourceDir2)
	c.press(win.ID, "button", "保存任务")
	c.waitNoText(win.ID, "保存任务", 10*time.Second)
	c.pressAfter(win.ID, "FailSync", "button", "+ 添加目标")
	c.setValue(win.ID, "目标名称", "BadDest")
	c.setValue(win.ID, "目标文件夹路径", missingTarget)
	c.press(win.ID, "button", "保存")
	c.waitNoText(win.ID, "目标文件夹路径", 10*time.Second)
	c.pressAfter(win.ID, "FailSync", "button", "▶ 运行")
	c.waitText(win.ID, "1个中有1个失败", 15*time.Second)

	// Click destination status link on FailSync -> jumps to History, filtered and located
	c.pressContains(win.ID, "link", "失败")
	c.waitText(win.ID, "近期运行记录", 5*time.Second)
	c.waitText(win.ID, "已定位", 5*time.Second)
	c.waitText(win.ID, "任务: FailSync ×", 5*time.Second)
	c.waitText(win.ID, "目录不可访问", 5*time.Second)

	// Clear task filter -> both FailSync and GoodSync appear in timeline
	c.press(win.ID, "button", "任务: FailSync ×")
	c.waitText(win.ID, "GoodSync", 5*time.Second)

	// Abnormal filter isolates FailSync
	c.press(win.ID, "button", "仅异常")
	c.waitNoText(win.ID, "GoodSync", 5*time.Second)
	c.waitText(win.ID, "FailSync", 5*time.Second)

	// All filter restores GoodSync and Diagnostics toggle reveals IDs
	c.press(win.ID, "button", "全部")
	c.waitText(win.ID, "GoodSync", 5*time.Second)
	c.press(win.ID, "button", "诊断详情")
	c.waitText(win.ID, "目标ID:", 5*time.Second)

	c.quitViaSettings(win.ID, "设置", "退出")
}
