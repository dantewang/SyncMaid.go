package main

import (
	"errors"
	"fmt"
	"github.com/egoist/mygo/ui"
	"strings"
	"syncmaid/internal/appcore"
	"syncmaid/internal/taskconfig"
)

func (s *manualSession) keepFilterDraft() {
	if s.targetID == "" {
		return
	}
	if s.filterDrafts == nil {
		s.filterDrafts = map[taskconfig.ID]appcore.FilterEditor{}
	}
	s.filterDrafts[s.targetID] = s.filterEditor
}
func (s *manualSession) loadFilterDraft(id taskconfig.ID) {
	if e, ok := s.filterDrafts[id]; ok {
		s.filterEditor = e
		return
	}
	for _, d := range s.targets {
		if d.ID == id {
			s.filterEditor = appcore.ProjectNativeFilter(d.Filters)
			s.keepFilterDraft()
			return
		}
	}
}
func (s *manualSession) filterDraftError() error {
	for _, d := range s.targets {
		if e, ok := s.filterDrafts[d.ID]; ok && e.Error != "" {
			return fmt.Errorf("%s: %s", d.Name, e.Error)
		}
	}
	return nil
}
func (a *application) commitFilterDraft(rules []taskconfig.Rule, err error) {
	s := &a.manual
	s.filterEditor.Error = ""
	if err != nil {
		s.filterEditor.Error = err.Error()
	} else {
		for i := range s.targets {
			if s.targets[i].ID == s.targetID {
				s.targets[i].Filters = rules
				break
			}
		}
	}
	s.keepFilterDraft()
	s.edited()
}
func (a *application) filterEdited() {
	s := &a.manual
	if !a.targetEditAllowed() || a.selectedMirror() || s.filterEditor.Mode == appcore.FilterReadOnly {
		return
	}
	rules, err := s.filterEditor.Rules()
	a.commitFilterDraft(rules, err)
}
func (a *application) setFilterMode(mode appcore.FilterMode) {
	if !a.targetEditAllowed() || a.selectedMirror() || a.manual.filterEditor.Mode == appcore.FilterReadOnly || a.manual.filterEditor.Mode == mode {
		return
	}
	a.manual.filterEditor.Mode = mode
	if mode == appcore.FilterGrouped && len(a.manual.filterEditor.Groups) == 0 {
		a.manual.filterEditor.Groups = []appcore.FilterDraftGroup{{}}
	}
	a.filterEdited()
}
func (a *application) taskDeletionIssue() error {
	if a.manual.id == "" {
		return errors.New("未选择任务")
	}
	t, err := a.tasks.Find(a.manual.id)
	if err != nil {
		return err
	}
	return appcore.TaskConfigurationDeletionIssue(t)
}

func (a *application) filtersView(c *ui.Context, locked bool) {
	e := &a.manual.filterEditor
	ui.Text(c, a.tr("此目标的文件过滤")).FontSize(16).FontWeight(500)
	readOnly := e.Mode == appcore.FilterReadOnly
	ui.Row(c).Gap(0).Children(func() {
		for _, item := range []struct {
			label string
			mode  appcore.FilterMode
		}{{"所有文件", appcore.FilterAll}, {"无匹配", appcore.FilterNone}, {"仅匹配", appcore.FilterGrouped}} {
			mode := item.mode
			glyph := ""
			if mode == appcore.FilterGrouped {
				glyph = "filter"
			}
			porcelainSegment(c, a.tr(item.label), e.Mode == mode, glyph).Disabled(locked || readOnly).OnClick(func() { a.setFilterMode(mode) })
		}
	})
	if e.Mode == appcore.FilterGrouped {
		a.filterGroupsView(c, locked)
	}
	if readOnly {
		ui.Text(c, a.tr("复杂或不支持的规则已原样保留，只读。"))
		ui.Text(c, a.tr("旧正则迁移")).Tooltip(a.tr("更多操作"))
		if a.tasks != nil {
			if bundle, ok := a.tasks.Restored[a.manual.id][a.manual.targetID]; ok {
				ui.Text(c, a.tr("已有正则确认记录；执行仍会检查全部规则、绑定及版本。"))
				if bundle.Err != nil {
					ui.Text(c, a.tr("确认无效，需重新预览：")+bundle.Err.Error())
				}
				return
			}
		}
		ui.Text(c, a.tr("诊断: ")+e.Reason)
		return
	}
	if e.Mode == appcore.FilterAll {
		ui.Text(c, a.tr("此目标接收所有源文件。")).FontSize(12)
	}
	if e.Mode == appcore.FilterNone {
		ui.Text(c, a.tr("此目标不接收文件；不会删除任何已有文件。")).FontSize(12)
	}

	if e.Error != "" {
		ui.Text(c, a.tr("过滤错误: ")+a.tr(e.Error))
	}
}

func (a *application) addFilterRule(group int) {
	if !a.targetEditAllowed() || a.selectedMirror() || a.manual.filterEditor.Mode != appcore.FilterGrouped {
		return
	}
	e := &a.manual.filterEditor
	if group < 0 || group >= len(e.Groups) {
		return
	}
	g := &e.Groups[group]
	if len(g.Rows) >= 128 {
		return
	}
	var r taskconfig.Rule
	switch g.Kind {
	case 0:
		r = taskconfig.PathFilter(g.Input)
	case 1:
		r = taskconfig.ExtensionFilter(g.Input)
	case 2:
		r = taskconfig.WildcardFilter(g.Input)
	}
	if g.Input == "" {
		return
	}
	if r.Prefix != nil && *r.Prefix == "" || r.Extension != nil && *r.Extension == "" || r.Pattern != nil && *r.Pattern == "" {
		e.Error = "过滤规则不能为空"
		a.manual.keepFilterDraft()
		return
	}
	g.Rows = append(g.Rows, r)
	rules, err := e.GroupRules()
	if err != nil {
		g.Rows = g.Rows[:len(g.Rows)-1]
		e.Error = err.Error()
		a.manual.keepFilterDraft()
		return
	}
	g.Input = ""
	a.commitFilterDraft(rules, nil)
}

func filterSegment(c *ui.Context, text string, selected bool) ui.Element {
	bg := porcelain.Background
	if selected {
		bg = rgb(0xDCE1EC)
	}
	return ui.ButtonBase(c).Label(text).Selected(selected).Padding(3, 6).Border(1, porcelain.Border).Background(bg).TextColor(porcelain.Text).Children(func() { ui.Text(c, text).FontSize(12).TextColor(porcelain.Text) })
}

func (a *application) filterGroupsView(c *ui.Context, locked bool) {
	e := &a.manual.filterEditor
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		if len(e.Groups) > 1 {
			ui.Row(c).Children(func() {
				for _, x := range []struct {
					key string
					all bool
				}{{"任一组", false}, {"全部组", true}} {
					filterSegment(c, a.tr(x.key), e.MatchAll == x.all).Disabled(locked).OnClick(func() { e.MatchAll = x.all; a.filterEdited() })
				}
			})
		}
		for i := range e.Groups {
			g := &e.Groups[i]
			ui.Column(c).FillWidth().Gap(6).Padding(10).Border(1, porcelain.Border).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, a.tr("匹配")).FontSize(12)
					for _, x := range []struct {
						key string
						all bool
					}{{"任一规则", false}, {"全部规则", true}} {
						filterSegment(c, a.tr(x.key), g.All == x.all).Disabled(locked).OnClick(func() { g.All = x.all; a.filterEdited() })
					}
					ui.Box(c).Grow(1)
					porcelainIcon(c, "×", a.tr("删除组")).Disabled(locked).OnClick(func() { e.Groups = append(e.Groups[:i], e.Groups[i+1:]...); a.filterEdited() })
				})
				for j, r := range g.Rows {
					ui.Row(c).Gap(8).Children(func() {
						excluded := r.Kind == "not"
						porcelainSegment(c, a.tr("排除"), excluded, "").Disabled(locked).OnClick(func() {
							if excluded {
								g.Rows[j] = r.Rule.Clone()
							} else {
								v := r.Clone()
								g.Rows[j] = taskconfig.Rule{Kind: "not", Rule: &v}
							}
							a.filterEdited()
						})
						display := r
						if excluded {
							display = *r.Rule
						}
						summary := a.ruleSummary([]taskconfig.Rule{display})
						ui.Text(c, summary).SingleLine().Ellipsis("…").Tooltip(summary).Grow(1).Shrink(1).MinWidth(0)
						porcelainIcon(c, "×", a.tr("删除规则")).Disabled(locked).OnClick(func() { g.Rows = append(g.Rows[:j], g.Rows[j+1:]...); a.filterEdited() })
					})
				}
				ui.Row(c).Wrap().Gap(8).Children(func() {
					for k, key := range []string{"路径前缀", "扩展名", "通配符"} {
						filterSegment(c, a.tr(key), g.Kind == k).Disabled(locked).OnClick(func() { g.Kind = k })
					}
					ui.TextInput(c, &g.Input).Label(a.tr("新规则")).Placeholder([]string{"photos/2024", "jpg", "**/*.png"}[g.Kind]).Grow(1).Shrink(1).MinWidth(100).Height(32).MaxLength(8192).ReadOnly(locked)
					porcelainButton(c, a.tr("添加")).Background(porcelain.Accent).TextColor(porcelain.AccentText).Disabled(locked || strings.TrimSpace(g.Input) == "" || len(g.Rows) >= 128).OnClick(func() { a.addFilterRule(i) })
				})
				if a.sourcePreview.phase == previewReady && a.sourcePreview.result != nil {
					ui.Text(c, a.tr("源扩展名：点击添加到此组")).FontSize(12).TextColor(porcelain.TextMuted)
					ui.Row(c).Wrap().Gap(6).Children(func() {
						for _, ext := range a.sourcePreview.result.Extensions {
							filterSegment(c, fmt.Sprintf("%s (%d)", ext.Text, ext.Count), false).Disabled(locked).OnClick(func() { a.addPreviewExtension(i, ext.Text) })
						}
					})
				}
			})
		}
		ui.Row(c).Children(func() {
			porcelainButton(c, "+ "+a.tr("添加组")).Disabled(locked || len(e.Groups) >= 32).OnClick(func() { e.Groups = append(e.Groups, appcore.FilterDraftGroup{}); a.filterEdited() })
		})
		if len(e.Groups) == 0 {
			key := "此目标不接收文件；不会删除任何已有文件。"
			ui.Text(c, a.tr(key)).FontSize(12)
		}

	})
}
