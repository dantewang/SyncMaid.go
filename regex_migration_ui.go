package main

import (
	"strings"
	"syncmaid/internal/filefilter"
	"syncmaid/internal/taskconfig"

	"github.com/egoist/mygo/ui"
)

type regexEdit struct {
	spec             filefilter.RegexSpec
	pattern, samples string
	preview          *filefilter.RegexPreview
	err              error
	notice           localizedMessage
}
type regexSession struct {
	task     taskconfig.Task
	edits    []regexEdit
	selected int
	previous manualPhase
}

func (a *application) beginRegexMigration() {
	if a.manual.busy() || a.manual.dirty || a.tasks == nil || a.taskError != nil || a.manual.id == "" {
		return
	}
	t, err := a.tasks.find(a.manual.id)
	if err != nil {
		return
	}
	session := &regexSession{task: t, previous: a.manual.phase}
	for _, d := range t.Destinations {
		specs, e := filefilter.Proposals(t, d.ID)
		if e != nil {
			err = e
			break
		}
		for _, spec := range specs {
			edit := regexEdit{spec: spec, pattern: spec.Pattern, samples: "example.txt\n子目录/照片.jpg"}
			for _, saved := range a.tasks.reviews {
				if saved.Origin == spec.Origin() {
					edit.pattern = saved.Pattern
					edit.notice = messagef("已有保存决定，执行时仍须通过绑定和版本校验。")
				}
			}
			session.edits = append(session.edits, edit)
		}
	}
	if err != nil {
		a.manual.message = messagef("错误: %s", err.Error())
		return
	}
	if len(session.edits) == 0 {
		a.manual.message = messagef("此任务没有旧正则规则。")
		return
	}
	a.regexSession = session
	a.manual.phase = manualReviewing
	a.settingsOpen = false
	a.historyOpen = false
}
func (a *application) discardRegexMigration() {
	previous := a.regexSession.previous
	a.regexSession = nil
	a.manual.phase = previous
	// Refresh the runnable request after any saved decisions; no run is started.
	if a.tasks != nil && a.taskError == nil {
		a.selectManual(a.manual.id)
	}
}
func (a *application) closeRegexMigration() {
	if a.manual.phase != manualReviewing || a.regexSession == nil {
		return
	}
	a.discardRegexMigration()
	if a.manual.quitPending {
		a.requestQuit()
	} else {
		a.drainWatches()
	}
}
func (a *application) previewRegex() {
	s := a.regexSession
	if s == nil || a.manual.phase != manualReviewing {
		return
	}
	e := &s.edits[s.selected]
	spec := e.spec
	spec.Pattern = e.pattern
	samples := strings.Split(strings.ReplaceAll(e.samples, "\r\n", "\n"), "\n")
	a.manual.phase = manualReviewWorking
	e.preview = nil
	e.err = nil
	e.notice = localizedMessage{}
	go func() {
		preview, err := filefilter.PrepareRegexPreview(spec, samples)
		a.window.Update(func() {
			a.manual.phase = manualReviewing
			e.preview = preview
			e.err = err
			if a.manual.quitPending {
				a.requestQuit()
			}
		})
	}()
}
func (a *application) confirmRegexMigration() {
	s := a.regexSession
	if s == nil || a.manual.phase != manualReviewing || a.taskError != nil {
		return
	}
	e := &s.edits[s.selected]
	if e.preview == nil {
		return
	}
	if e.preview.Spec().Pattern != e.pattern {
		e.preview = nil
		e.err = filefilter.ErrStaleReview
		return
	}
	review, err := e.preview.Confirm()
	if err != nil {
		e.err = err
		return
	}
	library := a.tasks
	a.manual.phase = manualReviewWorking
	go func() {
		next, err := library.saveRegexConfirmation(review)
		a.window.Update(func() {
			a.manual.phase = manualReviewing
			e.err = err
			if err != nil {
				a.taskError = err
				a.pauseQuitAfterTaskError()
				return
			}
			a.tasks = next
			e.preview = nil
			e.notice = messagef("确认已保存；尚未运行。编辑任务或规则后需重新确认。")
			if a.manual.quitPending {
				a.requestQuit()
			}
		})
	}()
}
func (a *application) regexMigrationView(c *ui.Context) {
	s := a.regexSession
	e := &s.edits[s.selected]
	locked := a.manual.phase != manualReviewing
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Gap(8).Padding(20).Children(func() {
			ui.Text(c, a.tr("旧正则迁移")).FontSize(22).Bold()
			ui.Text(c, s.task.Name)
			ui.Text(c, a.tr("仅预览新的 Go 行为，不提供 Rust 实测对照或等价保证。"))
			ui.Text(c, a.tr("默认忽略大小写，输入反斜杠转为斜杠；不做 Unicode 归一。Perl 字符类和单词边界为 ASCII。"))
			ui.Text(c, a.tr("flags、字符类运算、锚点、换行和非法语法可能不同；空表达式不匹配任何文件。请逐条确认意图。"))
			ui.Row(c).Gap(8).Children(func() {
				ui.Button(c, a.tr("返回任务编辑")).Disabled(locked).OnClick(a.closeRegexMigration)
				ui.Button(c, a.tr("预览新正则")).Disabled(locked || a.taskError != nil).OnClick(a.previewRegex)
				ui.Button(c, a.tr("确认新语义并保存")).Disabled(locked || a.taskError != nil || e.preview == nil).OnClick(a.confirmRegexMigration)
			})
			ui.Scroll(c).Height(85).Children(func() {
				ui.Column(c).FillWidth().Children(func() {
					for i, edit := range s.edits {
						index := i
						label := edit.spec.Origin().Field
						ui.Button(c, label).Selected(i == s.selected).Disabled(locked).OnClick(func() { s.selected = index })
					}
				})
			})
			ui.Text(c, a.tr("旧表达式：")+e.spec.Origin().LegacyPattern)
			ui.Text(c, "Profile: "+string(filefilter.GoRegexpV1)).FontSize(12)
			ui.Text(c, a.tr("新表达式")+":")
			ui.TextInput(c, &e.pattern).Label(a.tr("新表达式")).MaxLength(filefilter.MaxRegexPatternBytes).ReadOnly(locked || a.taskError != nil).OnChange(func() { e.preview = nil; e.err = nil; e.notice = localizedMessage{} })
			ui.Text(c, a.tr("示例路径（每行一项，最多16项；只显示新语义）"))
			ui.TextArea(c, &e.samples).Label(a.tr("示例路径")).Height(90).MaxLength(filefilter.MaxPreviewBytes).ReadOnly(locked).OnChange(func() { e.preview = nil; e.err = nil })
			ui.Text(c, a.render(e.notice))
			if e.err != nil {
				ui.Text(c, a.tr("预览或保存失败：")+e.err.Error())
			}
			if a.taskError != nil {
				ui.Text(c, a.tr("保存结果需核对；返回后重新载入，勿重复提交。"))
			}
			if e.preview != nil {
				for _, sample := range e.preview.Samples() {
					result := a.tr("不匹配")
					if sample.Matched {
						result = a.tr("匹配")
					}
					ui.Text(c, result+" · "+sample.Input)
				}
			}
		})
	})
}
