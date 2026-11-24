package main

import (
	"strings"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"
	"time"

	"github.com/egoist/mygo/ui"
)

// Layout state is owned by the UI thread, keyed by configuration identity.
type taskCardLayout struct {
	navigation   taskconfig.ID
	targetHidden taskconfig.ID
	destinations bool
	collapsed    map[taskconfig.ID]bool
	editor       bool
	running      taskconfig.ID
	focus        taskconfig.ID
}

func (a *application) cardActionAllowed() bool {
	return a.batch.phase == batchIdle && a.sourcePreview.job == nil && !a.manual.busy() && !a.manual.dirty && !a.cards.editor && a.tasks != nil && a.taskError == nil
}

func (a *application) editCard(id, destination taskconfig.ID) {
	if !a.cardActionAllowed() {
		return
	}
	a.selectManual(id)
	if a.manual.id != id {
		return
	}
	if destination != "" {
		a.manual.selectTarget(destination)
		for _, d := range a.manual.targets {
			if d.ID == destination {
				a.manual.beginDestinationEdit(destination, &d)
			}
		}
		a.cards.targetHidden = ""
		a.sourcePreview.open = true
	}
	a.cards.destinations = destination != ""
	a.cards.editor = true
	a.scrolls.taskModal = ui.ScrollState{}
	a.scrolls.destModal = ui.ScrollState{}
	a.scrolls.previewModal = ui.ScrollState{}
}

func (a *application) openAddDestination(id taskconfig.ID) {
	a.editCard(id, "")
	if a.manual.id == id && a.cards.editor {
		a.cards.destinations = true
		a.sourcePreview.open = true
		a.addManualTarget()
	}
}

func (a *application) openCopyDestination(id, destination taskconfig.ID) {
	a.editCard(id, "")
	if a.manual.id == id && a.cards.editor {
		a.cards.destinations = true
		a.sourcePreview.open = true
		a.copyManualTarget(destination)
	}
}

func (a *application) reorderSavedDestination(id, destination taskconfig.ID, delta int) {
	if !a.cardActionAllowed() {
		return
	}
	a.selectManual(id)
	if a.manual.id != id {
		return
	}
	a.moveManualTarget(destination, delta)
	if a.manual.dirty {
		a.createManual()
	}
}

func (a *application) deleteSavedDestination(id, destination taskconfig.ID) {
	if !a.cardActionAllowed() {
		return
	}
	a.selectManual(id)
	if a.manual.id != id {
		return
	}
	a.manual.selectTarget(destination)
	a.confirmRemoveManualTarget()
}

func (a *application) runCard(id taskconfig.ID) {
	if !a.cardActionAllowed() {
		return
	}
	a.selectManual(id)
	if a.manual.id == id {
		a.startManual()
	}
}

func (a *application) closeTaskEditor() {
	if a.manual.busy() {
		return
	}
	a.sourcePreview.invalidate()
	if a.taskError != nil {
		a.manual = manualSession{onEdit: a.manualEdited, phase: manualDraft, quitPending: a.manual.quitPending}
	} else {
		a.discardManual()
		a.refreshPreviewFromSnapshot()
	}
	a.cards.editor = false
	a.scrolls.taskModal = ui.ScrollState{}
	a.scrolls.destModal = ui.ScrollState{}
	a.scrolls.previewModal = ui.ScrollState{}
}

func (a *application) toggleCard(id taskconfig.ID) {
	if a.cards.collapsed == nil {
		a.cards.collapsed = make(map[taskconfig.ID]bool)
	}
	if a.cards.collapsed[id] {
		delete(a.cards.collapsed, id)
		return
	}
	a.cards.collapsed[id] = true
}

func (a *application) hasCollapsedCards() bool {
	if a.tasks != nil {
		for _, t := range a.tasks.Tasks {
			if a.cards.collapsed[t.ID] {
				return true
			}
		}
	}
	return false
}

func (a *application) filterSummary(rules []taskconfig.Rule) string {
	if len(rules) == 0 {
		return a.tr("无规则")
	}
	if len(rules) == 1 {
		switch rules[0].Kind {
		case "all":
			return a.tr("所有文件")
		case "none":
			return a.tr("无匹配")
		}
	}
	return a.tr("%d 条过滤规则", len(rules))
}

func (a *application) ruleSummary(rules []taskconfig.Rule) string {
	parts := make([]string, 0, len(rules))
	for _, r := range rules {
		label := map[string]string{"path": "路径前缀", "extension": "扩展名", "wildcard": "通配符", "regex": "正则表达式", "allOf": "全部满足", "anyOf": "任一满足", "not": "排除"}[r.Kind]
		if label == "" {
			label = r.Kind
		}
		if r.Kind == "all" {
			parts = append(parts, a.tr("所有文件"))
			continue
		}
		if r.Kind == "none" {
			parts = append(parts, a.tr("无匹配"))
			continue
		}
		if r.Rules != nil {
			if len(*r.Rules) == 0 {
				parts = append(parts, a.tr("无匹配"))
				continue
			}
			parts = append(parts, a.tr(label)+" ("+a.ruleSummary(*r.Rules)+")")
			continue
		}
		if r.Rule != nil {
			parts = append(parts, a.tr(label)+" ("+a.ruleSummary([]taskconfig.Rule{*r.Rule})+")")
			continue
		}
		value := ""
		switch {
		case r.Prefix != nil:
			value = *r.Prefix
		case r.Extension != nil:
			value = *r.Extension
		case r.Pattern != nil:
			value = *r.Pattern
		}
		parts = append(parts, a.tr(label)+" "+value)
	}
	return strings.Join(parts, " · ")
}

func destinationHealthColor(r syncengine.DestinationResult, exists bool) ui.Color {
	if !exists {
		return porcelain.TextMuted
	}
	switch r.State {
	case syncengine.DestinationSucceeded, syncengine.DestinationNoMatches, syncengine.DestinationRunning:
		return porcelain.Text
	case syncengine.DestinationFailed:
		return porcelain.Danger
	case syncengine.DestinationIncomplete, syncengine.DestinationCanceled, syncengine.DestinationSkipped, syncengine.DestinationInterrupted:
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
	completed, matched, failed, deferred, retained, interrupted := 0, 0, 0, 0, 0, false
	currentResult := false
	for _, d := range t.Destinations {
		goal, exists := result.destinations[d.ID]
		currentResult = currentResult || exists
		switch goal.State {
		case syncengine.DestinationSucceeded:
			completed++
			matched++
			retained += goal.Progress.Retained
		case syncengine.DestinationIncomplete:
			completed++
			matched++
			deferred += goal.Progress.Deferred
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
	case deferred > 0:
		return a.tr("部分完成 · 延迟 %d 个文件", deferred), porcelain.Warning
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
		switch r.State {
		case syncengine.DestinationCanceled, syncengine.DestinationSkipped:
			return a.tr(string(r.State))
		case syncengine.DestinationIncomplete:
			if r.Progress.Deferred > 0 {
				return a.tr("部分完成 · 延迟 %d 个", r.Progress.Deferred)
			}
			return a.tr(string(r.State))
		case syncengine.DestinationInterrupted:
			return a.tr(string(r.State)) + " · " + a.tr("查看日志")
		}
		if r.Error != "" {
			summaryKey, _ := classifySyncError(r.Error)
			return a.tr(string(r.State)) + " · " + a.tr(summaryKey)
		}
		return a.tr(string(r.State))
	}
	if r.Progress.Retained > 0 {
		return a.tr("移动 %d · 保留 %d 个源文件", r.Progress.Moved, r.Progress.Retained)
	}
	span := now.Sub(r.Ended)
	files := max(r.Progress.Copied, r.Progress.Moved)
	removed := r.Progress.Removed
	removedDirs := r.Progress.RemovedDirs
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
	if removed > 0 {
		status := a.tr("%s · %d个文件 · 移除%d个", ago, files, removed)
		if files == 0 {
			status = a.tr("%s · 移除%d个文件", ago, removed)
		}
		if removedDirs > 0 {
			return status + " · " + a.tr("清理%d个空目录", removedDirs)
		}
		return status
	}
	if removedDirs > 0 {
		if files == 0 {
			return a.tr("%s · 清理%d个空目录", ago, removedDirs)
		}
		return a.tr("%s · %d个文件 · 清理%d个空目录", ago, files, removedDirs)
	}
	return a.tr("%s · %d个文件", ago, files)
}
