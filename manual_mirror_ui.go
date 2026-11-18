package main

import (
	"context"
	"fmt"
	"github.com/egoist/mygo/ui"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
)

func (a *application) setDestinationMode(strategy taskconfig.Strategy, mode taskconfig.DeleteMode) {
	if !a.targetEditAllowed() || syncengine.ManualKind(a.manual.kind) == taskconfig.Moving {
		return
	}
	for i := range a.manual.targets {
		d := &a.manual.targets[i]
		if d.ID != a.manual.targetID {
			continue
		}
		if strategy == taskconfig.Mirror && !syncengine.IsAllFileRules(d.Filters) {
			a.manual.message = messagef("请先将此目标过滤设为全部文件，再选择Mirror。")
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
			ui.Text(c, a.tr("同步策略"))
			porcelainChoice(c, "sync", a.tr("镜像"), a.tr("复制新增和更改的文件；多余文件须每次确认后移除。"), syncengine.DestinationStrategy(d) == taskconfig.Mirror, locked, func() { a.setDestinationMode(taskconfig.Mirror, syncengine.DestinationDeleteMode(d)) })
			porcelainChoice(c, "＋", a.tr("仅添加"), a.tr("复制新增和更改的文件；从不删除目标中的文件。"), syncengine.DestinationStrategy(d) == taskconfig.AddOnly, locked, func() { a.setDestinationMode(taskconfig.AddOnly, syncengine.DestinationDeleteMode(d)) })
			if syncengine.DestinationStrategy(d) == taskconfig.Mirror {
				ui.Text(c, a.tr("Mirror同步全部文件；每次移除多余文件前须确认。空源禁止删除，不清理空目录。"))
				ui.Row(c).Gap(8).Children(func() {
					ui.Button(c, a.tr("回收站")).Selected(syncengine.DestinationDeleteMode(d) == taskconfig.Recycle).Disabled(locked).OnClick(func() { a.setDestinationMode(taskconfig.Mirror, taskconfig.Recycle) })
					ui.Button(c, a.tr("永久删除")).Selected(syncengine.DestinationDeleteMode(d) == taskconfig.Permanent).Disabled(locked).OnClick(func() { a.setDestinationMode(taskconfig.Mirror, taskconfig.Permanent) })
				})
				return
			}
		}
	}
}
func (a *application) confirmMirror(ctx context.Context, p syncengine.MirrorPreview) error {
	reply := make(chan error, 1)
	a.window.Update(func() {
		if ctx.Err() == nil {
			a.lifecycle.show()
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
		e = syncengine.ErrMirrorDeclined
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
	action := a.tr("移至回收站")
	if p.Mode == taskconfig.Permanent {
		action = a.tr("永久删除（不可撤销）")
	}
	ui.Text(c, fmt.Sprintf(a.tr("确认本次Mirror：%s · %s · %d个文件"), p.Name, action, len(p.Paths))).Bold()
	ui.Text(c, p.Target)
	ui.Scroll(c).Height(150).Children(func() {
		ui.Column(c).Children(func() {
			for _, path := range p.Paths {
				ui.Text(c, path)
			}
		})
	})
	ui.Row(c).Gap(8).Children(func() {
		ui.Button(c, a.tr("取消本次移除")).OnClick(func() { a.answerMirror(false) })
		ui.Button(c, a.tr("确认本次移除")).OnClick(func() { a.answerMirror(true) })
	})
}

func (a *application) selectedMirror() bool {
	for _, d := range a.manual.targets {
		if d.ID == a.manual.targetID {
			return syncengine.DestinationStrategy(d) == taskconfig.Mirror
		}
	}
	return false
}
