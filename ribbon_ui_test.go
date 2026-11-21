package main

import (
	"testing"

	"syncmaid/internal/appcore"
	"syncmaid/internal/syncengine"
	"syncmaid/internal/taskconfig"

	"github.com/egoist/mygo/ui"
)

func TestTopologyLayoutDetectsCascade(t *testing.T) {
	destMedia := taskconfig.Destination{
		ID:       "d-media",
		Name:     "MediaStage",
		Strategy: taskconfig.Move,
		Target:   taskconfig.Target{Kind: "folder", Path: `D:\Staging\Media`},
		Filters:  syncengine.AllFileRules(),
	}
	destArchive := taskconfig.Destination{
		ID:       "d-archive",
		Name:     "ArchiveCopy",
		Strategy: taskconfig.Move,
		Target:   taskconfig.Target{Kind: "folder", Path: `D:\Archive`},
		Filters:  syncengine.AllFileRules(),
	}
	taskA := taskconfig.Task{
		ID:           "task-a",
		Name:         "Ingest",
		Kind:         taskconfig.Moving,
		SourcePath:   `D:\Inbox`,
		Destinations: []taskconfig.Destination{destMedia, destArchive},
	}
	taskB := taskconfig.Task{
		ID:         "task-b",
		Name:       "BackupMedia",
		Kind:       taskconfig.Sync,
		SourcePath: `d:/staging/media/`,
		Destinations: []taskconfig.Destination{
			{
				ID:       "d-nas",
				Name:     "NAS",
				Strategy: taskconfig.Mirror,
				Target:   taskconfig.Target{Kind: "folder", Path: `\\nas\Media`},
				Filters:  syncengine.AllFileRules(),
			},
		},
	}

	layout := buildTopologyLayout([]taskconfig.Task{taskA, taskB})
	foundCascadeNode := false
	for _, n := range layout.Nodes {
		if n.TaskID == taskB.ID && n.IsCascade {
			foundCascadeNode = true
			break
		}
	}
	if !foundCascadeNode {
		t.Fatalf("expected cascade node for task %s, got %+v", taskB.ID, layout.Nodes)
	}
	foundCascadeEdge := false
	for _, e := range layout.Edges {
		if e.CascadeLabel == taskB.Name {
			foundCascadeEdge = true
			break
		}
	}
	if !foundCascadeEdge {
		t.Fatalf("expected cascade edge labeled %q, got %+v", taskB.Name, layout.Edges)
	}

	a := application{
		locale: "zh-Hans",
		tasks:  &appcore.SavedTaskLibrary{Tasks: []taskconfig.Task{taskA, taskB}},
	}
	parent, ok := a.findCascadeParent(taskB)
	if !ok || parent.ID != taskA.ID {
		t.Fatalf("expected cascade parent %s, got ok=%v parent=%s", taskA.ID, ok, parent.ID)
	}
	children := a.findCascadeChildren(taskA, destMedia)
	if len(children) != 1 || children[0].ID != taskB.ID {
		t.Fatalf("expected cascade child [%s], got %+v", taskB.ID, children)
	}
	if nonCascade := a.findCascadeChildren(taskA, destArchive); len(nonCascade) != 0 {
		t.Fatalf("expected no cascade child for ArchiveCopy, got %+v", nonCascade)
	}
}

func TestGroupMirrorPathsByDirectory(t *testing.T) {
	paths := []string{
		"root1.txt",
		`OldB\a.txt`,
		"OldA/sub/1.txt",
		"OldA/sub/2.txt",
		"OldA/sub/3.txt",
		"OldB/b.txt",
		"Alpha/z.txt",
	}
	got := groupMirrorPaths(paths)
	want := []mirrorDirGroup{
		{Dir: "OldA/sub/", Count: 3},
		{Dir: "OldB/", Count: 2},
		{Dir: "(root)", Count: 1},
		{Dir: "Alpha/", Count: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("group count mismatch: got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("group[%d] = %+v, want %+v (full=%+v)", i, got[i], want[i], got)
		}
	}
}

func TestSourcePreviewTaskPanelProfileAndBuckets(t *testing.T) {
	snap := &appcore.SourceSnapshot{
		Source:      `D:\BigSource`,
		FolderCount: 4,
		TotalBytes:  1024 * 1024 * 120,
		Files: []appcore.PreviewFile{
			{Path: "Media/photo1.jpg", Size: 60 * 1024 * 1024},
			{Path: "Docs/readme.txt", Size: 40 * 1024 * 1024},
			{Path: "notes.md", Size: 20 * 1024 * 1024},
		},
		TopDirs: []appcore.PreviewDirectory{
			{Prefix: "Media/", Files: 1, Bytes: 60 * 1024 * 1024},
			{Prefix: "Docs/", Files: 1, Bytes: 40 * 1024 * 1024},
			{Prefix: "(root)", Files: 1, Bytes: 20 * 1024 * 1024},
		},
		Extensions: []appcore.PreviewExtension{
			{Text: "jpg", Count: 1},
			{Text: "txt", Count: 1},
		},
	}

	destAll := syncengine.ManualDestination{
		ID:      "d-all",
		Name:    "MirrorNAS",
		Filters: syncengine.AllFileRules(),
	}
	a := application{
		locale: "zh-Hans",
		manual: manualSession{
			kind:    taskconfig.Sync,
			source:  `D:\BigSource`,
			targets: []syncengine.ManualDestination{destAll},
		},
		sourcePreview: sourcePreviewSession{
			phase:          previewReady,
			cachedSnapshot: snap,
			result: &appcore.RoutePreview{
				SourceFiles: 3,
				FolderCount: 4,
				TotalBytes:  snap.TotalBytes,
				TopDirs:     snap.TopDirs,
				Extensions:  snap.Extensions,
				Snapshot:    snap,
				Targets: map[taskconfig.ID]appcore.PreviewSelection{
					"d-all": {Count: 3, Samples: []string{"Media/photo1.jpg", "Docs/readme.txt", "notes.md"}},
				},
			},
		},
	}

	// 1. All-files target renders Profile view with TopDirs and Extensions.
	panel := ui.NewTester(a.sourcePreviewTaskPanel, 320, 640)
	if !panel.HasText(a.tr("顶层目录分布")) || !panel.HasText(a.tr("高频扩展名")) {
		t.Fatalf("expected Profile view sections, got %v", panel.Texts())
	}
	if !panel.HasText("Media/") {
		t.Fatalf("expected Media/ directory bar in Profile view, got %v", panel.Texts())
	}

	// 2. Setting a search query switches to instant search hits.
	a.sourcePreview.searchQuery = "photo1"
	panel = ui.NewTester(a.sourcePreviewTaskPanel, 320, 640)
	if !panel.HasText(a.tr("命中 %d 项", 1)) || !panel.HasText("Media/photo1.jpg") {
		t.Fatalf("expected search hit in panel, got %v", panel.Texts())
	}
	a.sourcePreview.searchQuery = ""

	// 3. Adding a filtered target switches away from Profile to Sampled Buckets.
	extTxt := "txt"
	destFiltered := syncengine.ManualDestination{
		ID:   "d-txt",
		Name: "OnlyDocs",
		Filters: []taskconfig.Rule{
			{Kind: "extension", Extension: &extTxt},
		},
	}
	a.manual.targets = []syncengine.ManualDestination{destFiltered, destAll}
	a.sourcePreview.result.Targets = map[taskconfig.ID]appcore.PreviewSelection{
		"d-txt": {Count: 1, Samples: []string{"Docs/readme.txt"}},
		"d-all": {Count: 3, Samples: []string{"Media/photo1.jpg", "Docs/readme.txt", "notes.md"}},
	}
	panel = ui.NewTester(a.sourcePreviewTaskPanel, 320, 640)
	if panel.HasText(a.tr("顶层目录分布")) {
		t.Fatalf("expected Profile view to yield to Sampled Buckets when filtered, got %v", panel.Texts())
	}
	if !panel.HasText("OnlyDocs") || !panel.HasText("MirrorNAS") {
		t.Fatalf("expected bucket headings in Sampled Buckets view, got %v", panel.Texts())
	}
}

func TestPorcelainScrollDynamicGutter(t *testing.T) {
	var state ui.ScrollState
	contentHeight := float32(80)
	view := func(c *ui.Context) {
		porcelainScroll(c, &state, func() {
			ui.Box(c).FillWidth().Height(contentHeight).Label("inner-content")
		}).Size(300, 160)
	}

	tt := ui.NewTester(view, 300, 160)
	tt.Frame()
	if state.MaxY != 0 {
		t.Fatalf("expected no overflow for short content, got MaxY=%v", state.MaxY)
	}
	rc, ok := tt.Find("inner-content")
	if !ok || rc.W != 300 {
		t.Fatalf("expected full width 300 without scrollbar gutter, got ok=%v rect=%+v", ok, rc)
	}

	contentHeight = 320
	tt.Frame()
	if state.MaxY <= 0 {
		t.Fatalf("expected overflow MaxY > 0 after content grew, got %v", state.MaxY)
	}
	tt.Frame()
	rc, ok = tt.Find("inner-content")
	if !ok || rc.W != 300-scrollbarGutterWidth {
		t.Fatalf("expected width %v when scrollbar gutter active, got ok=%v rect=%+v", 300-scrollbarGutterWidth, ok, rc)
	}
}
