package main

import (
	"github.com/egoist/mygo/ui"
	"strings"
	"syncmaid/internal/taskconfig"
)

func (a *application) sourcePreviewView(c *ui.Context) {
	p := &a.sourcePreview
	ui.Column(c).FillWidth().Gap(8).Padding(12, 0).Children(func() {
		ui.Row(c).Wrap().Gap(10).Children(func() {
			label := "源预览"
			if p.phase != previewIdle {
				label = "刷新预览"
			}
			porcelainButton(c, a.tr(label)).Gap(8).Children(func() { ui.Icon(c, porcelainGlyphs["eye"]).Size(16, 16); ui.Text(c, a.tr(label)).FontSize(16) }).Disabled(a.manual.busy() || a.manual.quitPending || p.job != nil || a.taskError != nil).OnClick(a.startSourcePreview)
			if p.job != nil {
				porcelainButton(c, a.tr("取消预览")).Disabled(p.phase == previewStopping).OnClick(p.invalidate)
			}
		})
		ui.Text(c, a.tr("只读预览当前草稿的规则归属，不预测覆盖或删除；目录变化后需刷新。")).FontSize(12).TextColor(porcelain.TextMuted)
		switch p.phase {
		case previewScanning:
			ui.Text(c, a.tr("正在读取源文件…"))
		case previewStopping:
			ui.Text(c, a.tr("正在取消预览…"))
		case previewStale:
			ui.Text(c, a.tr("预览已失效，请刷新。"))
		case previewFailed:
			ui.Text(c, a.tr("预览失败: %s", p.err.Error())).TextColor(porcelain.Danger)
		case previewReady:
			r := p.result
			if r.EmptyRoot {
				ui.Text(c, a.tr("源目录为空。"))
			} else if r.SourceFiles == 0 {
				ui.Text(c, a.tr("源目录存在，但没有普通文件。"))
			} else {
				ui.Text(c, a.tr("源文件: %d", r.SourceFiles))
			}
			ui.Text(c, a.tr("未匹配: %d · %s", r.Unmatched.Count, strings.Join(r.Unmatched.Samples, ", "))).MaxLines(3)
			for _, item := range r.Contested {
				names := make([]string, 0, len(item.Targets))
				for _, id := range item.Targets {
					for _, d := range a.manual.targets {
						if d.ID == id {
							names = append(names, d.Name)
							break
						}
					}
				}
				text := a.tr("多规则匹配: %s → %s", item.Path, strings.Join(names, " → "))
				ui.Text(c, text).FontSize(12).MaxLines(2).Tooltip(text)
			}
		}
	})
}
func (a *application) sourcePreviewTargetView(c *ui.Context, id taskconfig.ID) {
	if a.sourcePreview.phase != previewReady || a.sourcePreview.result == nil {
		return
	}
	r, ok := a.sourcePreview.result.Targets[id]
	if !ok {
		return
	}
	text := a.tr("预览匹配: %d · %s", r.Count, strings.Join(r.Samples, ", "))
	ui.Text(c, text).FontSize(12).TextColor(porcelain.TextMuted).MaxLines(2).Tooltip(text)
}
