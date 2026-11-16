package main

import (
	"github.com/egoist/mygo/ui"
	"syncmaid/internal/taskconfig"
)

func filterSegment(c *ui.Context, text string, selected bool) ui.Element {
	bg := porcelain.Background
	if selected {
		bg = rgb(0xD9DFE3)
	}
	return ui.ButtonBase(c).Label(text).Selected(selected).Padding(3, 6).Border(1, porcelain.Border).Background(bg).Children(func() { ui.Text(c, text).FontSize(12) })
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
						ui.Text(c, a.ruleSummary([]taskconfig.Rule{r})).SingleLine().Ellipsis("…").Tooltip(a.ruleSummary([]taskconfig.Rule{r})).Grow(1).Shrink(1).MinWidth(0)
						porcelainIcon(c, "×", a.tr("删除规则")).Disabled(locked).OnClick(func() { g.Rows = append(g.Rows[:j], g.Rows[j+1:]...); a.filterEdited() })
					})
				}
				ui.Row(c).Wrap().Gap(8).Children(func() {
					for k, key := range []string{"路径前缀", "扩展名", "通配符"} {
						filterSegment(c, a.tr(key), g.Kind == k).Disabled(locked).OnClick(func() { g.Kind = k })
					}
					ui.TextInput(c, &g.Input).Label(a.tr("新规则")).Grow(1).Shrink(1).MinWidth(100).Height(32).MaxLength(8192).ReadOnly(locked)
					porcelainButton(c, a.tr("添加")).Background(porcelain.Accent).TextColor(porcelain.AccentText).Disabled(locked || len(g.Rows) >= 128).OnClick(func() { a.addFilterRule(i) })
				})
			})
		}
		ui.Row(c).Children(func() {
			porcelainButton(c, "+ "+a.tr("添加组")).Disabled(locked || len(e.Groups) >= 32).OnClick(func() { e.Groups = append(e.Groups, filterDraftGroup{}); a.filterEdited() })
		})
		if len(e.Groups) == 0 {
			key := "此目标不接收文件；不会删除任何已有文件。"
			ui.Text(c, a.tr(key)).FontSize(12)
		}
		for _, d := range a.manual.targets {
			if d.ID == a.manual.targetID {
				ui.Text(c, a.ruleSummary(d.Filters)).FontSize(12).TextColor(porcelain.TextMuted)
			}
		}
	})
}
