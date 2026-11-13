package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type manualPhase uint8

const (
	manualDraft manualPhase = iota
	manualChoosing
	manualChecking
	manualReady
	manualRunning
	manualStopping
	manualFinished
)

// All session state belongs to the native main thread. Workers only receive
// immutable requests and post value copies; no task configuration is saved.
type manualSession struct {
	phase          manualPhase
	source, target string
	request        manualRequest
	cancel         context.CancelFunc
	progress       transferProgress
	message        string
	quitPending    bool
}

func (s *manualSession) busy() bool {
	return s.phase == manualChecking || s.phase == manualRunning || s.phase == manualStopping || s.phase == manualChoosing
}
func (s *manualSession) edited() {
	if s.busy() {
		return
	}
	s.phase = manualDraft
	s.message = "路径已更改，请创建本次任务"
}

func (a *application) manualView(c *ui.Context) {
	s := &a.manual
	ui.Column(c).Fill().Gap(12).Padding(24).Children(func() {
		ui.Text(c, "SyncMaid").FontSize(26).Bold()
		ui.Text(c, "手动仅添加 · 本次会话任务，不保存配置")
		ui.Text(c, "源文件夹")
		ui.Row(c).Gap(8).Children(func() {
			ui.TextInput(c, &s.source).Label("源文件夹路径").Grow(1).Placeholder("源文件夹绝对路径").ReadOnly(s.busy()).OnChange(s.edited)
			ui.Button(c, "选择源").Disabled(s.busy()).OnClick(func() { a.chooseFolder(true) })
		})
		ui.Text(c, "目标文件夹（须已存在）")
		ui.Row(c).Gap(8).Children(func() {
			ui.TextInput(c, &s.target).Label("目标文件夹路径").Grow(1).Placeholder("目标文件夹绝对路径").ReadOnly(s.busy()).OnChange(s.edited)
			ui.Button(c, "选择目标").Disabled(s.busy()).OnClick(func() { a.chooseFolder(false) })
		})
		ui.Text(c, "更新大小或修改时间不同的文件；不删除目标额外文件。")
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, "创建本次任务").Disabled(s.busy()).OnClick(a.createManual)
			ui.Button(c, "运行").Disabled(s.phase != manualReady && s.phase != manualFinished).OnClick(a.startManual)
			ui.Button(c, "停止").Disabled(s.phase != manualRunning && s.phase != manualChecking).OnClick(a.stopManual)
			ui.Button(c, "Exit").OnClick(a.requestQuit)
		})
		ui.Text(c, s.message)
		if s.phase == manualReady || s.phase == manualFinished {
			ui.Text(c, "本次任务: "+pathLabel(s.request.Source)+" → "+pathLabel(s.request.Target)).FontSize(12)
		}
		p := s.progress
		ui.Text(c, fmt.Sprintf("%s · 已发布 %d / %d · 未变化 %d · 已传输 %d 字节", p.Stage, p.Copied, p.Total, p.Unchanged, p.Bytes))
		ui.Text(c, p.Path).FontSize(12)
		ui.Text(c, "关闭窗口隐藏到托盘，不停止任务。停止保留已发布文件。")
		ui.Text(c, "Data: "+pathLabel(a.installation.data)).FontSize(12)
	})
}
func (a *application) chooseFolder(source bool) {
	s := &a.manual
	if s.busy() {
		return
	}
	s.phase = manualChoosing
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.window, Title: "选择文件夹", Directory: true})
		a.window.Update(func() {
			s.phase = manualDraft
			if err != nil {
				s.message = "选择失败: " + err.Error()
			} else if len(paths) > 0 {
				if source {
					s.source = paths[0]
				} else {
					s.target = paths[0]
				}
				s.edited()
			}
			if s.quitPending {
				a.requestQuit()
			}
		})
	}()
}
func (a *application) createManual() {
	s := &a.manual
	if s.busy() {
		return
	}
	q := manualRequest{Source: s.source, Target: s.target}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.phase = manualChecking
	s.message = "检查源与目标…"
	s.progress = transferProgress{}
	go func() {
		var request manualRequest
		err := interruptibleIO(ctx, func(ctx context.Context) error { var e error; request, e = validateManual(ctx, q); return e })
		a.window.Update(func() {
			cancel()
			s.cancel = nil
			if err != nil {
				s.phase = manualDraft
				s.message = "创建失败: " + err.Error()
			} else {
				s.request = request
				s.phase = manualReady
				s.message = "任务已创建，可以运行（仅本次会话）"
			}
			if s.quitPending {
				a.requestQuit()
			}
		})
	}()
}
func (a *application) startManual() {
	s := &a.manual
	if s.phase != manualReady && s.phase != manualFinished {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.phase = manualRunning
	s.message = "正在运行…"
	s.progress = transferProgress{}
	q := s.request
	go func() {
		last := time.Time{}
		notify := func(p transferProgress) {
			if time.Since(last) < 100*time.Millisecond && p.Stage != "已完成" {
				return
			}
			last = time.Now()
			a.window.Update(func() { s.progress = p })
		}
		var p transferProgress
		err := interruptibleIO(ctx, func(ctx context.Context) error { var e error; p, e = runManual(ctx, q, notify); return e })
		a.window.Update(func() {
			cancel()
			s.cancel = nil
			s.progress = p
			s.phase = manualFinished
			switch {
			case err == nil:
				s.message = "同步完成"
			case errors.Is(err, context.Canceled):
				s.message = "已停止；已发布文件保留。详情: " + err.Error()
				s.progress.Stage = "已停止"
			default:
				s.message = "同步失败（可能已有部分文件完成）: " + err.Error()
				s.progress.Stage = "失败"
			}
			if err != nil {
				slog.Warn("manual-sync-stopped", "error", err, "copied", p.Copied, "unchanged", p.Unchanged)
			}
			if s.quitPending {
				a.requestQuit()
			}
		})
	}()
}
func (a *application) stopManual() {
	s := &a.manual
	if s.phase != manualRunning && s.phase != manualChecking {
		return
	}
	s.phase = manualStopping
	s.message = "正在停止并清理临时文件…"
	s.cancel()
}
func (a *application) requestQuit() {
	s := &a.manual
	if s.busy() {
		s.quitPending = true
		if s.phase == manualChoosing {
			s.message = "请关闭文件夹选择窗口以退出。"
		}
		a.stopManual()
		return
	}
	a.lifecycle.exit()
}
func (a *application) beforeQuit(e *mygo.QuitEvent) {
	if a.manual.busy() {
		e.PreventDefault()
		a.manual.quitPending = true
		a.stopManual()
		return
	}
	a.lifecycle.beginQuit()
}
