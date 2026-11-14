package main

import (
	"github.com/egoist/mygo/ui"
	"strconv"
	"syncmaid/internal/taskconfig"
)

func (a *application) setTrigger(kind string) {
	if !a.targetEditAllowed() {
		return
	}
	s := &a.manual
	s.triggerError = ""
	if kind == "watch" {
		n := int32(10)
		s.trigger = taskconfig.Trigger{Kind: "watch", SettleSeconds: &n}
		s.quietText = "10"
	} else {
		s.trigger = taskconfig.Trigger{Kind: "manual"}
	}
	s.edited()
}
func (a *application) quietChanged() {
	if !a.targetEditAllowed() {
		return
	}
	s := &a.manual
	n, e := strconv.ParseInt(s.quietText, 10, 32)
	s.triggerError = ""
	if e != nil || n < 1 || n > 600 {
		s.triggerError = "静默期必须是1–600秒的整数"
	} else {
		v := int32(n)
		s.trigger.SettleSeconds = &v
	}
	s.edited()
}
func (a *application) triggerView(c *ui.Context, locked bool) {
	s := &a.manual
	ui.Row(c).Gap(8).Children(func() {
		ui.Button(c, "Manual手动").Selected(s.trigger.Kind != "watch").Disabled(locked).OnClick(func() { a.setTrigger("manual") })
		ui.Button(c, "Watch监视").Selected(s.trigger.Kind == "watch").Disabled(locked).OnClick(func() { a.setTrigger("watch") })
	})
	if s.trigger.Kind == "watch" {
		ui.Text(c, "源变化后等待静默秒数（1–600，默认10）")
		ui.TextInput(c, &s.quietText).Label("静默秒数").ReadOnly(locked).OnChange(a.quietChanged)
	}
	if s.triggerError != "" {
		ui.Text(c, s.triggerError)
	}
}
func (a *application) watchView(c *ui.Context) {
	ui.Text(c, a.watchActivity).FontSize(12)
	ui.Row(c).Gap(8).Children(func() {
		ui.Button(c, "暂停全部监视").Disabled(a.watchPhase != watchesServing).OnClick(func() { a.stopWatches(watchesPaused) })
		ui.Button(c, "恢复监视").Disabled(a.watchPhase != watchesPaused).OnClick(a.resumeWatches)
	})
	if a.watchPhase == watchesPaused {
		ui.Text(c, "监视已暂停；停止不会被旧事件立即重启。恢复后重新等待静默期。")
	}
}
