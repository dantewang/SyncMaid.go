package main

import (
	"github.com/egoist/mygo/ui"
	"syncmaid/internal/taskconfig"
	"time"
)

func destinationHealthColor(r destinationResult, exists bool) ui.Color {
	if !exists {
		return porcelain.TextMuted
	}
	switch r.State {
	case destinationSucceeded, destinationNoMatches, destinationRunning:
		return porcelain.Text
	case destinationFailed:
		return porcelain.Danger
	case destinationCanceled, destinationSkipped, destinationInterrupted:
		return porcelain.Warning
	default:
		return porcelain.TextMuted
	}
}

func (a *application) taskHealth(t taskconfig.Task) (string, ui.Color) {
	id := t.ID
	if id == a.manual.id && a.manual.phase == manualDraft && !a.manual.dirty && a.manual.message.key != "" {
		return a.render(a.manual.message), porcelain.Danger
	}
	if a.cards.running == id && (a.manual.phase == manualRunning || a.manual.phase == manualChecking || a.manual.phase == manualStopping) {
		if a.manual.phase == manualStopping {
			return a.render(a.manual.message), porcelain.Warning
		}
		return a.tr("正在运行…"), porcelain.Text
	}
	if len(t.Destinations) == 0 {
		return a.tr("尚未添加目标"), porcelain.TextMuted
	}
	result := a.results[id]
	completed, matched, failed, interrupted := 0, 0, 0, false
	for _, d := range t.Destinations {
		switch result.destinations[d.ID].State {
		case destinationSucceeded:
			completed++
			matched++
		case destinationNoMatches:
			completed++
		case destinationFailed:
			failed++
		case destinationCanceled, destinationSkipped, destinationInterrupted:
			interrupted = true
		}
	}
	switch {
	case failed > 0:
		return a.tr("%d个中有%d个失败", len(t.Destinations), failed), porcelain.Danger
	case interrupted:
		return a.render(result.message), porcelain.Warning
	case completed == len(t.Destinations) && matched == 0:
		return a.tr(string(destinationNoMatches)), porcelain.Text
	case completed == len(t.Destinations):
		return a.tr("全部已同步"), porcelain.Text
	case completed > 0:
		return a.tr("部分目标已完成"), porcelain.Warning
	default:
		if len(result.destinations) == 0 && result.message.key != "" {
			return a.render(result.message), porcelain.Warning
		}
		return a.tr("未开始"), porcelain.TextMuted
	}
}
func (a *application) destinationStatus(r destinationResult, exists bool, now time.Time) string {
	if !exists || r.State == destinationWaiting {
		return a.tr("从未运行")
	}
	if r.State != destinationSucceeded {
		if r.Error != "" {
			return a.tr(string(r.State)) + " · " + historyText(r.Error, historyErrorBytes)
		}
		return a.tr(string(r.State))
	}
	span := now.Sub(r.Ended)
	files := max(r.Progress.Copied, r.Progress.Moved)
	ago := a.tr("刚刚同步")
	if span >= 24*time.Hour {
		ago = a.tr("%d天前同步", int(span.Hours()/24))
	} else if span >= time.Hour {
		ago = a.tr("%d小时前同步", int(span.Hours()))
	} else if span >= time.Minute {
		ago = a.tr("%d分钟前同步", int(span.Minutes()))
	}
	if r.Ended.IsZero() {
		ago = a.tr(string(r.State))
	}
	return a.tr("%s · %d个文件", ago, files)
}
