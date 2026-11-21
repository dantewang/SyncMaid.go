package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"syncmaid/internal/appcore"
)

func (a *application) beginImport() {
	s := &a.manual
	if s.busy() || s.dirty || a.tasks == nil || a.taskError != nil {
		return
	}
	previous := s.phase
	a.importError = nil
	title := a.tr("选择旧任务 JSON 或备份")
	library := a.tasks
	type importChoice struct {
		preview *appcore.LegacyImport
	}
	runDialog(a, func() (importChoice, error) {
		paths, err := e2eDialogOpen(mygo.OpenDialogOptions{Parent: a.window, Title: title})
		var out importChoice
		if err == nil && len(paths) > 0 {
			out.preview, err = appcore.ReadLegacyImport(paths[0], library)
		}
		return out, err
	}, func(res importChoice, err error) {
		if s.quitPending {
			return
		}
		if err != nil {
			a.importError = err
			s.message = messagef("导入拒绝，旧文件和任务库未修改：%s", err.Error())
		} else if res.preview != nil {
			a.importPreview = res.preview
			a.importPrevious = previous
			a.importError = nil
			s.phase = manualImporting
			a.settingsOpen = false
			a.topologyOpen = false
			a.historyOpen = false
		}
	})
}
func (a *application) cancelImport() {
	if a.manual.phase != manualImporting {
		return
	}
	a.discardImportPreview()
	a.finishAsyncStep(true)
}
func (a *application) discardImportPreview() {
	a.importPreview = nil
	a.importError = nil
	a.manual.phase = a.importPrevious
}
func (a *application) confirmImport() {
	if a.manual.phase != manualImporting || a.importPreview == nil || a.taskError != nil {
		return
	}
	p, library := a.importPreview, a.tasks
	a.manual.phase = manualSaving
	go func() {
		next, err := library.AppendImport(p)
		a.window.Update(func() {
			a.manual.phase = manualImporting
			if err != nil {
				a.importError = err
				a.taskError = err
				a.pauseQuitAfterTaskError()
				return
			}
			a.tasks = next
			a.importPreview = nil
			a.manual.phase = a.importPrevious
			a.selectManual(p.Tasks[0].ID)
			a.manual.message = messagef("已追加导入 %d 个任务；全部为手动触发，尚未运行。", len(p.Tasks))
			a.syncWatches()
			a.finishAsyncStep(false)
		})
	}()
}
func (a *application) importView(c *ui.Context) {
	p := a.importPreview
	porcelainScroll(c, &a.scrolls.legacyImport, func() {
		ui.Column(c).FillWidth().Gap(10).Padding(20).Children(func() {
			ui.Text(c, a.tr("旧任务导入预览")).FontSize(22).Bold()
			ui.Text(c, p.Path)
			ui.Text(c, a.tr("确认将追加任务，不覆盖现有任务；旧主文件和备份均不修改。"))
			ui.Text(c, a.tr("所有导入任务将设为 Manual，原自动触发配置会被替换。不会自动执行。"))
			ui.Text(c, a.tr("运行前必须退出旧 Rust 版；Go 的锁不能阻止旧版同时操作文件。"))
			ui.Text(c, a.tr("未知类型整份拒绝；可保留的未知成员或旧非空正则不会获得运行许可。文件系统条件仍在运行时检查。"))
			for i, t := range p.Tasks {
				ui.Text(c, t.Name+" · "+string(t.Kind)).Bold()
				ui.Text(c, string(t.ID)).FontSize(12)
				ui.Text(c, t.SourcePath)
				if issue := p.Issues[i]; issue != nil {
					ui.Text(c, a.tr("保留但存在运行限制：")+issue.Error())
				} else {
					ui.Text(c, a.tr("当前功能支持；尚未验证实际文件夹。"))
				}
				for _, d := range t.Destinations {
					ui.Text(c, d.Name+" · "+string(d.Strategy)+" · "+d.Target.Path)
				}
			}
			if a.importError != nil {
				ui.Text(c, a.tr("保存失败或结果待核对，预览保留。取消后重新载入任务库，勿重复提交：")+a.importError.Error())
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.Button(c, a.tr("取消导入")).Disabled(a.manual.phase != manualImporting).OnClick(a.cancelImport)
				ui.Button(c, a.tr("确认追加并设为手动")).Disabled(a.manual.phase != manualImporting || a.taskError != nil).OnClick(a.confirmImport)
			})
		})
	}).Grow(1).FillWidth()
}
