package main

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"path/filepath"
	"syncmaid/internal/taskconfig"
)

func (a *application) toggleHistory() {
	a.settingsOpen = false
	a.historyOpen = !a.historyOpen
	if r, ok := a.historyView.Latest[a.manual.id]; ok {
		a.historySelected = r.ID
	}
}
func (a *application) selectedHistory() (runRecord, bool) {
	for _, r := range a.historyView.Recent {
		if r.ID == a.historySelected {
			return r, true
		}
	}
	for _, r := range a.historyView.Latest {
		if r.ID == a.historySelected {
			return r, true
		}
	}
	if n := len(a.historyView.Recent); n > 0 {
		return a.historyView.Recent[n-1], true
	}
	return runRecord{}, false
}
func (a *application) historyViewContent(c *ui.Context) {
	ui.Column(c).Grow(1).FillWidth().Gap(8).Padding(16).Children(func() {
		ui.Text(c, a.tr("近期运行记录")).FontSize(18).FontWeight(500)
		ui.Text(c, a.tr("最多100次近期记录 / 总计4 MiB；最近任务结果优先保留。错误摘要最多1 KiB；只供诊断，不授权执行或删除。")).FontSize(12)
		ui.Text(c, pathLabel(filepath.Join(a.installation.data, runHistoryFile))).FontSize(12)
		if a.historyError != nil {
			ui.Text(c, a.tr("记录不可用或未保存：")+historyText(a.historyError.Error(), historyErrorBytes))
			ui.Text(c, a.tr("已发生的文件执行结果不变。请保留并修复记录文件后重新载入。"))
		}
		ui.Scroll(c).Height(145).Children(func() {
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				for i := len(a.historyView.Recent) - 1; i >= 0; i-- {
					r := a.historyView.Recent[i]
					id := r.ID
					label := a.tr("查看 ") + r.Name + " · " + r.Started.Local().Format("01-02 15:04:05") + " · " + a.tr(runOutcome(r))
					ui.Button(c, label).FillWidth().OnClick(func() { a.historySelected = id })
				}
			})
		})
		r, ok := a.selectedHistory()
		if !ok {
			ui.Text(c, a.tr("暂无可用运行记录。"))
			return
		}
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Gap(10).Children(func() {
				ui.Text(c, r.Name+a.tr(" · ")+a.tr(runOutcome(r))).Bold()
				ui.Text(c, fmt.Sprintf(a.tr("任务 %s · 运行 %s · %s / %s"), r.TaskID, r.ID, r.Kind, a.tr(r.Origin))).FontSize(12)
				ui.Text(c, a.tr("开始: ")+r.Started.Local().Format("2006-01-02 15:04:05 MST"))
				if !r.Ended.IsZero() {
					ui.Text(c, a.tr("结束: ")+r.Ended.Local().Format("2006-01-02 15:04:05 MST"))
				} else {
					ui.Text(c, a.tr("未记录结束；可能已有文件操作，请核对，不能视作成功或未执行。"))
				}
				if r.Error != "" {
					ui.Text(c, a.tr("错误: ")+r.Error)
				}
				for _, g := range r.Goals {
					ui.Text(c, g.Name+a.tr(" · ")+a.tr(string(g.State))).Bold()
					ui.Text(c, a.tr("目标ID: ")+string(g.ID)).FontSize(12)
					if !g.Started.IsZero() {
						ui.Text(c, a.tr("目标开始: ")+g.Started.Local().Format("01-02 15:04:05"))
					}
					if !g.Ended.IsZero() {
						ui.Text(c, a.tr("目标结束: ")+g.Ended.Local().Format("01-02 15:04:05"))
					}
					if r.Outcome != "running" {
						p := g.Counts
						ui.Text(c, fmt.Sprintf(a.tr("源 %d · 匹配 %d · 已发布 %d · 移动 %d · 移除 %d · 保留/待核对 %d · 未变化 %d · 移动失败 %d · 源未匹配 %d · %d字节"), p.SourceFiles, p.Total, p.Copied, p.Moved, p.Removed, p.Retained, p.Unchanged, p.Failed, p.Unmatched, p.Bytes))
					}
					if g.Error != "" {
						ui.Text(c, a.tr("错误: ")+g.Error)
					}
				}
			})
		})
	})
}

func taskIDs(tasks []taskconfig.Task) []taskconfig.ID {
	ids := make([]taskconfig.ID, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	return ids
}
