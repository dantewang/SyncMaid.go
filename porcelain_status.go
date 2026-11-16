package main

import (
	"github.com/egoist/mygo/ui"
	"syncmaid/internal/taskconfig"
	"time"
)

func (a *application) taskHealthColor(id taskconfig.ID) ui.Color {
	if id == a.manual.id && a.manual.phase == manualDraft && !a.manual.dirty && a.manual.message.key != "" {
		return porcelain.Danger
	}
	if a.cards.running == id && (a.manual.phase == manualRunning || a.manual.phase == manualChecking || a.manual.phase == manualStopping) {
		return porcelain.Text
	}
	result, ok := a.results[id]
	if !ok {
		return porcelain.TextMuted
	}
	color := porcelain.Text
	for _, r := range result.destinations {
		if r.State == destinationFailed {
			return porcelain.Danger
		}
		if r.State != destinationSucceeded && r.State != destinationNoMatches {
			color = porcelain.Warning
		}
	}
	return color
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
