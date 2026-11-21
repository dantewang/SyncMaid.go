package main

import (
	"fmt"
	"strings"
	"syncmaid/internal/taskconfig"

	"github.com/egoist/mygo/ui"
)

type topologyNode struct {
	X, Y, W, H float32
	TaskID     taskconfig.ID
	Title      string
	Subtitle   string
	Accent     ui.Color
	IsCascade  bool
}

type topologyEdge struct {
	X1, Y1, X2, Y2 float32
	Color          ui.Color
	Dashed         bool
	CascadeLabel   string
}

type topologyLayout struct {
	Width, Height float32
	Nodes         []topologyNode
	Edges         []topologyEdge
}

func normWinTopoPath(p string) string {
	cleaned := strings.ReplaceAll(strings.TrimSpace(p), "/", "\\")
	cleaned = strings.TrimRight(cleaned, "\\")
	return strings.ToLower(cleaned)
}

func topologyDestColor(kind taskconfig.TaskKind, d taskconfig.Destination) ui.Color {
	if kind == taskconfig.Moving {
		return ribbonMove
	}
	if d.Strategy == taskconfig.Mirror {
		return porcelain.Warning
	}
	return ribbonBlue
}

func buildTopologyLayout(tasks []taskconfig.Task) topologyLayout {
	destTargets := map[string]taskconfig.ID{}
	for _, t := range tasks {
		for _, d := range t.Destinations {
			k := normWinTopoPath(d.Target.Path)
			if k != "" {
				destTargets[k] = t.ID
			}
		}
	}

	var roots, children []taskconfig.Task
	for _, t := range tasks {
		srcKey := normWinTopoPath(t.SourcePath)
		if parentID, ok := destTargets[srcKey]; ok && parentID != t.ID {
			children = append(children, t)
		} else {
			roots = append(roots, t)
		}
	}

	const (
		srcW    float32 = 200
		dstW    float32 = 216
		nodeH   float32 = 52
		rowStep float32 = 66
		srcX    float32 = 24
		dstX    float32 = 292
		casX    float32 = 584
	)

	var layout topologyLayout
	type anchor struct{ x, y float32 }
	anchors := map[string]anchor{}

	yCursor := float32(24)
	for _, t := range roots {
		nDest := max(1, len(t.Destinations))
		blockH := float32(nDest) * rowStep
		srcY := yCursor + (blockH-rowStep)/2
		srcColor := ribbonBlue
		if t.Kind == taskconfig.Moving {
			srcColor = ribbonMove
		}
		layout.Nodes = append(layout.Nodes, topologyNode{
			X: srcX, Y: srcY, W: srcW, H: nodeH,
			TaskID: t.ID, Title: t.Name, Subtitle: t.SourcePath, Accent: srcColor,
		})

		if len(t.Destinations) == 0 {
			layout.Nodes = append(layout.Nodes, topologyNode{
				X: dstX, Y: srcY, W: dstW, H: nodeH,
				TaskID: t.ID, Title: "—", Subtitle: "", Accent: porcelain.Border,
			})
			layout.Edges = append(layout.Edges, topologyEdge{
				X1: srcX + srcW, Y1: srcY + nodeH/2, X2: dstX, Y2: srcY + nodeH/2,
				Color: porcelain.Border, Dashed: true,
			})
		} else {
			for i, d := range t.Destinations {
				dy := yCursor + float32(i)*rowStep
				title := d.Name
				if t.Kind == taskconfig.Moving {
					title = fmt.Sprintf("%02d %s", i+1, d.Name)
				}
				c := topologyDestColor(t.Kind, d)
				layout.Nodes = append(layout.Nodes, topologyNode{
					X: dstX, Y: dy, W: dstW, H: nodeH,
					TaskID: t.ID, Title: title, Subtitle: d.Target.Path, Accent: c,
				})
				layout.Edges = append(layout.Edges, topologyEdge{
					X1: srcX + srcW, Y1: srcY + nodeH/2, X2: dstX, Y2: dy + nodeH/2,
					Color: c, Dashed: t.Kind == taskconfig.Moving && i > 0,
				})
				k := normWinTopoPath(d.Target.Path)
				if k != "" {
					anchors[k] = anchor{x: dstX + dstW, y: dy + nodeH/2}
				}
			}
		}
		yCursor += blockH + 26
	}

	casY := float32(24)
	for _, ct := range children {
		a, ok := anchors[normWinTopoPath(ct.SourcePath)]
		startY := casY
		if ok && a.y-nodeH/2 > startY {
			startY = a.y - nodeH/2
		}
		nDest := max(1, len(ct.Destinations))
		for i, d := range ct.Destinations {
			dy := startY + float32(i)*rowStep
			c := topologyDestColor(ct.Kind, d)
			title := d.Name
			if ct.Kind == taskconfig.Moving {
				title = fmt.Sprintf("%02d %s", i+1, d.Name)
			}
			layout.Nodes = append(layout.Nodes, topologyNode{
				X: casX, Y: dy, W: dstW, H: nodeH,
				TaskID: ct.ID, Title: title, Subtitle: d.Target.Path, Accent: c, IsCascade: true,
			})
			if ok {
				label := ""
				if i == 0 {
					label = ct.Name
				}
				layout.Edges = append(layout.Edges, topologyEdge{
					X1: a.x, Y1: a.y, X2: casX, Y2: dy + nodeH/2,
					Color: c, CascadeLabel: label,
				})
			}
		}
		if len(ct.Destinations) == 0 {
			layout.Nodes = append(layout.Nodes, topologyNode{
				X: casX, Y: startY, W: dstW, H: nodeH,
				TaskID: ct.ID, Title: ct.Name, Subtitle: ct.SourcePath, Accent: ribbonBlue, IsCascade: true,
			})
			if ok {
				layout.Edges = append(layout.Edges, topologyEdge{
					X1: a.x, Y1: a.y, X2: casX, Y2: startY + nodeH/2,
					Color: ribbonBlue, Dashed: true, CascadeLabel: ct.Name,
				})
			}
		}
		casY = startY + float32(nDest)*rowStep + 26
	}

	layout.Width = 540
	if len(children) > 0 {
		layout.Width = 824
	}
	layout.Height = max(float32(360), max(yCursor, casY)+12)
	return layout
}

func (a *application) findCascadeParent(t taskconfig.Task) (taskconfig.Task, bool) {
	if a.tasks == nil {
		return taskconfig.Task{}, false
	}
	src := normWinTopoPath(t.SourcePath)
	if src == "" {
		return taskconfig.Task{}, false
	}
	for _, other := range a.tasks.Tasks {
		if other.ID == t.ID {
			continue
		}
		for _, d := range other.Destinations {
			if normWinTopoPath(d.Target.Path) == src {
				return other, true
			}
		}
	}
	return taskconfig.Task{}, false
}

func (a *application) findCascadeChildren(t taskconfig.Task, d taskconfig.Destination) []taskconfig.Task {
	if a.tasks == nil {
		return nil
	}
	dst := normWinTopoPath(d.Target.Path)
	if dst == "" {
		return nil
	}
	var out []taskconfig.Task
	for _, other := range a.tasks.Tasks {
		if other.ID != t.ID && normWinTopoPath(other.SourcePath) == dst {
			out = append(out, other)
		}
	}
	return out
}

func (a *application) topologyView(c *ui.Context) {
	ui.Column(c).Grow(1).FillHeight().Gap(12).Padding(16).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Text(c, a.tr("拓扑")).FontSize(18).FontWeight(600).Grow(1)
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				for _, item := range []struct {
					label string
					color ui.Color
				}{
					{"Sync", ribbonBlue},
					{"Move", ribbonMove},
					{"Mirror", porcelain.Warning},
				} {
					ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(10, 3).Background(item.color)
						ui.Text(c, item.label).FontSize(12).TextColor(porcelain.TextMuted)
					})
				}
			})
		})
		ui.Box(c).Height(1).FillWidth().Background(porcelain.Border)

		if a.tasks == nil || len(a.tasks.Tasks) == 0 {
			ui.Column(c).Grow(1).FillWidth().AlignItems(ui.Center).Justify(ui.Center).Gap(10).Children(func() {
				if maidAvatar != nil {
					ui.Image(c, maidAvatar).Size(44, 44)
				}
				ui.Text(c, a.tr("暂无同步任务")).TextColor(porcelain.TextMuted)
				ribbonInkButton(c, "＋ "+a.tr("新建任务")).Disabled(!a.cardActionAllowed()).OnClick(func() {
					a.topologyOpen = false
					a.newManual()
					a.cards.editor = true
					a.cards.destinations = false
				})
			})
			return
		}

		layout := buildTopologyLayout(a.tasks.Tasks)
		ui.ScrollBoth(c).Grow(1).FillWidth().Border(1, porcelain.Border).Background(ribbonSidebarBg).Children(func() {
			ui.Box(c).Size(layout.Width, layout.Height).Draw(func(p *ui.Painter, r ui.Rect) {
				gridColor := ui.Color{R: 204, G: 210, B: 223, A: 95}
				for x := r.X + 24; x < r.X+r.W; x += 24 {
					p.Line(x, r.Y, x, r.Y+r.H, 1, gridColor)
				}
				for y := r.Y + 24; y < r.Y+r.H; y += 24 {
					p.Line(r.X, y, r.X+r.W, y, 1, gridColor)
				}
				for _, e := range layout.Edges {
					x1, y1 := r.X+e.X1, r.Y+e.Y1
					x2, y2 := r.X+e.X2, r.Y+e.Y2
					midX := (x1 + x2) / 2
					path := new(ui.Path).MoveTo(x1, y1).CubeTo(midX, y1, midX, y2, x2-5, y2)
					if e.Dashed {
						path = path.Dash(0, 5, 4)
					}
					p.StrokePath(path, 1.6, e.Color)
					arrow := new(ui.Path).MoveTo(x2, y2).LineTo(x2-7, y2-4).LineTo(x2-7, y2+4).Close()
					p.FillPath(arrow, e.Color)
					if e.CascadeLabel != "" {
						p.Text(midX-28, min(y1, y2)-14, "↳ "+e.CascadeLabel, 12, ribbonBlue)
					}
				}
			}).Children(func() {
				for _, n := range layout.Nodes {
					node := n
					btn := ui.ButtonBase(c).Absolute().Left(node.X).Top(node.Y).Size(node.W, node.H).Label(node.Title).Background(porcelain.Surface).Border(1, porcelain.Border).Padding(6, 10)
					btn = btn.DrawOver(func(p *ui.Painter, r ui.Rect) {
						p.Fill(ui.Rect{X: r.X, Y: r.Y, W: 3, H: r.H}, node.Accent, 0)
					})
					btn.Children(func() {
						ui.Column(c).FillWidth().Gap(2).Padding(0, 0, 0, 4).Children(func() {
							ui.Text(c, node.Title).FontSize(13).FontWeight(600).SingleLine().Ellipsis("…")
							ui.Text(c, node.Subtitle).Font("Consolas").FontSize(12).TextColor(porcelain.TextMuted).SingleLine().Ellipsis("…")
						})
					}).Disabled(!a.cardActionAllowed()).OnClick(func() {
						a.topologyOpen = false
						a.historyOpen = false
						a.settingsOpen = false
						a.selectManual(node.TaskID)
					})
				}
			})
		})
	})
}
