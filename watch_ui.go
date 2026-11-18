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
	} else if kind == "scheduled" {
		expr := "* * * * *"
		s.trigger = taskconfig.Trigger{Kind: "scheduled", CronExpression: &expr}
		s.cronText = expr
		s.refreshCron()
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
	ui.Row(c).Gap(0).Children(func() {
		porcelainSegment(c, a.tr("手动"), s.trigger.Kind != "watch" && s.trigger.Kind != "scheduled", "manual").Disabled(locked).OnClick(func() { a.setTrigger("manual") })
		porcelainSegment(c, a.tr("定时"), s.trigger.Kind == "scheduled", "clock").Disabled(locked).OnClick(func() { a.setTrigger("scheduled") })
		porcelainSegment(c, a.tr("监视"), s.trigger.Kind == "watch", "eye").Disabled(locked).OnClick(func() { a.setTrigger("watch") })
	})
	if s.trigger.Kind == "watch" {
		ui.Text(c, a.tr("源变化后等待静默秒数（1–600，默认10）"))
		ui.TextInput(c, &s.quietText).Label(a.tr("静默秒数")).ReadOnly(locked).OnChange(a.quietChanged)
	}
	if s.trigger.Kind == "scheduled" {
		ui.Text(c, a.tr("本地时间 · 分 时 日 月 星期(0–6，0周日) · 不补跑历史"))
		ui.TextInput(c, &s.cronText).Label(a.tr("cron表达式")).MaxLength(4096).ReadOnly(locked).OnChange(a.cronChanged)
		if !s.dirty && a.watchPhase != watchesServing {
			ui.Text(c, a.tr("自动触发已暂停，恢复后重新计算下一次"))
		} else if w := a.watches[s.id]; !s.dirty && w != nil && w.schedule != nil {
			ui.Text(c, a.render(w.status))
		} else {
			ui.Text(c, a.tr("下次运行: ")+a.tr(s.cronNext))
		}
	}
	if s.triggerError != "" {
		ui.Text(c, a.tr("触发设置错误: ")+a.tr(s.triggerError))
	}
}
func (a *application) watchView(c *ui.Context) {
	ui.Text(c, a.render(a.watchActivity)).FontSize(12)
	ui.Row(c).Gap(8).Children(func() {
		ui.Button(c, a.tr("暂停自动触发")).Disabled(a.watchPhase != watchesServing).OnClick(func() { a.stopWatches(watchesPaused) })
		ui.Button(c, a.tr("恢复自动触发")).Disabled(a.watchPhase != watchesPaused).OnClick(a.resumeWatches)
	})
	if a.watchPhase == watchesPaused {
		ui.Text(c, a.tr("自动触发已暂停"))
	}
}
