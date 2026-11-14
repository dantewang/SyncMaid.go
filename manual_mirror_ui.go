package main

import (
	"context"
	"fmt"
	"github.com/egoist/mygo/ui"
	"syncmaid/internal/taskconfig"
)

func (a *application) setDestinationMode(strategy taskconfig.Strategy, mode taskconfig.DeleteMode) {
	if !a.targetEditAllowed() {
		return
	}
	for i := range a.manual.targets {
		d := &a.manual.targets[i]
		if d.ID != a.manual.targetID {
			continue
		}
		if strategy == taskconfig.Mirror && !isAllFileRules(d.Filters) {
			a.manual.message = "请先将此目标过滤设为全部文件，再选择Mirror。"
			return
		}
		d.Strategy = strategy
		d.DeleteMode = mode
		a.manual.edited()
		return
	}
}
func (a *application) destinationModeView(c *ui.Context, locked bool) {
	for _, d := range a.manual.targets {
		if d.ID == a.manual.targetID {
			ui.Row(c).Gap(8).Children(func() {
				ui.Button(c, "仅添加").Selected(destinationStrategy(d) == taskconfig.AddOnly).Disabled(locked).OnClick(func() { a.setDestinationMode(taskconfig.AddOnly, destinationDeleteMode(d)) })
				ui.Button(c, "Mirror镜像").Selected(destinationStrategy(d) == taskconfig.Mirror).Disabled(locked).OnClick(func() { a.setDestinationMode(taskconfig.Mirror, destinationDeleteMode(d)) })
			})
			if destinationStrategy(d) == taskconfig.Mirror {
				ui.Text(c, "Mirror同步全部文件；每次移除多余文件前须确认。空源禁止删除，不清理空目录。")
				ui.Row(c).Gap(8).Children(func() {
					ui.Button(c, "回收站").Selected(destinationDeleteMode(d) == taskconfig.Recycle).Disabled(locked).OnClick(func() { a.setDestinationMode(taskconfig.Mirror, taskconfig.Recycle) })
					ui.Button(c, "永久删除").Selected(destinationDeleteMode(d) == taskconfig.Permanent).Disabled(locked).OnClick(func() { a.setDestinationMode(taskconfig.Mirror, taskconfig.Permanent) })
				})
				return
			}
		}
	}
}
func (a *application) confirmMirror(ctx context.Context, p mirrorPreview) error {
	reply := make(chan error, 1)
	a.window.Update(func() {
		if ctx.Err() == nil {
			a.manual.mirrorPreview = &p
			a.manual.mirrorReply = reply
		}
	})
	defer a.window.Update(func() {
		if a.manual.mirrorReply == reply {
			a.manual.mirrorPreview = nil
			a.manual.mirrorReply = nil
		}
	})
	select {
	case e := <-reply:
		return e
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (a *application) answerMirror(accept bool) {
	s := &a.manual
	if s.mirrorReply == nil {
		return
	}
	var e error
	if !accept {
		e = errMirrorDeclined
	}
	select {
	case s.mirrorReply <- e:
	default:
	}
	s.mirrorPreview = nil
	s.mirrorReply = nil
}
func (a *application) mirrorPreviewView(c *ui.Context) {
	p := a.manual.mirrorPreview
	if p == nil {
		return
	}
	action := "移至回收站"
	if p.Mode == taskconfig.Permanent {
		action = "永久删除（不可撤销）"
	}
	ui.Text(c, fmt.Sprintf("确认本次Mirror：%s · %s · %d个文件", p.Name, action, len(p.Paths))).Bold()
	ui.Text(c, p.Target)
	ui.Scroll(c).Height(150).Children(func() {
		ui.Column(c).Children(func() {
			for _, path := range p.Paths {
				ui.Text(c, path)
			}
		})
	})
	ui.Row(c).Gap(8).Children(func() {
		ui.Button(c, "取消本次移除").OnClick(func() { a.answerMirror(false) })
		ui.Button(c, "确认本次移除").OnClick(func() { a.answerMirror(true) })
	})
}

func (a *application) selectedMirror() bool {
	for _, d := range a.manual.targets {
		if d.ID == a.manual.targetID {
			return destinationStrategy(d) == taskconfig.Mirror
		}
	}
	return false
}
