package main

import (
	"github.com/egoist/mygo/ui"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
	"time"
)

func destinationHealthColor(r syncengine.DestinationResult, exists bool) ui.Color {
	if !exists {
		return porcelain.TextMuted
	}
	switch r.State {
	case syncengine.DestinationSucceeded, syncengine.DestinationNoMatches, syncengine.DestinationRunning:
		return porcelain.Text
	case syncengine.DestinationFailed:
		return porcelain.Danger
	case syncengine.DestinationCanceled, syncengine.DestinationSkipped, syncengine.DestinationInterrupted:
		return porcelain.Warning
	default:
		return porcelain.TextMuted
	}
}

func (a *application) taskHealth(t taskconfig.Task) (string, ui.Color) {
	id := t.ID
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
	completed, matched, failed, retained, interrupted := 0, 0, 0, 0, false
	currentResult := false
	for _, d := range t.Destinations {
		goal, exists := result.destinations[d.ID]
		currentResult = currentResult || exists
		switch goal.State {
		case syncengine.DestinationSucceeded:
			completed++
			matched++
			retained += goal.Progress.Retained
		case syncengine.DestinationNoMatches:
			completed++
		case syncengine.DestinationFailed:
			failed++
		case syncengine.DestinationCanceled, syncengine.DestinationSkipped, syncengine.DestinationInterrupted:
			interrupted = true
		}
	}
	switch {
	case failed > 0:
		return a.tr("%d个中有%d个失败", len(t.Destinations), failed), porcelain.Danger
	case result.outcome == "failed" && (currentResult || len(result.destinations) == 0):
		return a.render(result.message), porcelain.Danger
	case interrupted || result.outcome == "stopped" && currentResult:
		return a.render(result.message), porcelain.Warning
	case completed == len(t.Destinations) && matched == 0:
		return a.tr(string(syncengine.DestinationNoMatches)), porcelain.Text
	case completed == len(t.Destinations) && retained > 0:
		return a.tr("完成 · 保留 %d 个源文件", retained), porcelain.Text
	case completed == len(t.Destinations):
		return a.tr("全部已同步"), porcelain.Text
	case completed > 0:
		return a.tr("部分目标已完成"), porcelain.Text
	default:
		return a.tr("未开始"), porcelain.TextMuted
	}
}
func (a *application) destinationStatus(r syncengine.DestinationResult, exists bool, now time.Time) string {
	if !exists || r.State == syncengine.DestinationWaiting {
		return a.tr("从未运行")
	}
	if r.State != syncengine.DestinationSucceeded {
		if r.Error != "" {
			return a.tr(string(r.State)) + " · " + historyText(r.Error, historyErrorBytes)
		}
		return a.tr(string(r.State))
	}
	if r.Progress.Retained > 0 {
		return a.tr("移动 %d · 保留 %d 个源文件", r.Progress.Moved, r.Progress.Retained)
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
