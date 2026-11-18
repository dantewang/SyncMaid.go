package main

import (
	"github.com/egoist/mygo/ui"
	"syncmaid/internal/taskconfig"
	"testing"
)

// Regression for the observed 760-DIP dirty editor footer overflow. Native
// screenshots are still required for visual acceptance; this checks geometry.
func TestDirtyEditorFooterActionsStayInside760(t *testing.T) {
	for _, lang := range languageChoices {
		d := manualDestination{ID: taskconfig.NewID(), Name: "Fixture", Path: `C:\fixture`, Filters: allFileRules()}
		a := &application{locale: lang.tag, manual: manualSession{phase: manualDraft, dirty: true, source: `C:\source`, targets: []manualDestination{d}, targetID: d.ID, filterEditor: filterEditor{Mode: filterAll}}}
		a.cards.editor = true
		a.cards.destinations = true
		view := ui.NewTester(a.view, 760, 620)
		for _, key := range []string{"取消", "保存"} {
			r, ok := view.Find(a.tr(key))
			if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > 760 || r.Y+r.H > 620 {
				t.Fatalf("%s %s: %+v visible=%v", lang.tag, key, r, ok)
			}
		}
	}
}

func TestPreviewButtonHasOneVisibleCaption(t *testing.T) {
	for _, lang := range languageChoices {
		for _, phase := range []sourcePreviewPhase{previewIdle, previewStale} {
			a := &application{locale: lang.tag, sourcePreview: sourcePreviewSession{phase: phase}}
			key := "源预览"
			if phase == previewStale {
				key = "刷新预览"
			}
			view := ui.NewTester(a.sourcePreviewView, 760, 620)
			// ButtonBase exposes its accessibility label and its one visible
			// text child. The former styled Button plus Children exposed three.
			countText := func(v *ui.Tester) int {
				n := 0
				for _, s := range v.Texts() {
					if s == a.tr(key) {
						n++
					}
				}
				return n
			}
			count := countText(view)
			if count != 2 {
				t.Fatalf("%s %s: %d captions", lang.tag, key, count)
			}
		}
	}
}
