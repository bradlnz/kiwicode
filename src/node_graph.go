package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxCanvasNodes = 600

type canvasNode struct {
	id, label, path, kind string
	column, row           int
}

type canvasEdge struct {
	from, to int
	kind     string
}

type canvasGraph struct {
	nodes   []canvasNode
	edges   []canvasEdge
	limited bool
}

type nodeCanvas struct {
	canvasGraph
	source                     canvasGraph
	expanded                   map[string]bool // nil keeps Architecture's full overview.
	cells                      []canvasCell
	done                       chan canvasGraph
	stale, center, dragging    bool
	selected, panX, panY, zoom int
	x, y, width, height        int
	dragX, dragY               int
}

func (e *editor) activeNodeCanvas() *nodeCanvas {
	if e.graph {
		return e.dependencyCanvas
	}
	if e.opsMode == "architecture canvas" {
		return e.architectureView
	}
	return nil
}

func (e *editor) startNodeCanvas(architecture, refresh bool) {
	view := &e.dependencyCanvas
	if architecture {
		view = &e.architectureView
	}
	if *view == nil {
		*view = &nodeCanvas{stale: true, center: true, zoom: 1}
		if !architecture {
			(*view).expanded = map[string]bool{"dir:.": true}
		}
	}
	c := *view
	if c.done != nil || !refresh && !c.stale {
		return
	}
	c.stale = false
	c.done = make(chan canvasGraph, 1)
	root, files, done := mustCwd(), append([]string(nil), e.files...), c.done
	go func() { done <- buildCanvasGraph(root, files) }()
}

func (e *editor) pollNodeCanvases() bool {
	changed := false
	for _, c := range []*nodeCanvas{e.dependencyCanvas, e.architectureView} {
		if c == nil || c.done == nil {
			continue
		}
		select {
		case graph := <-c.done:
			c.done = nil
			if c.expanded != nil {
				c.source = graph
				c.organizeFolders()
			} else {
				c.canvasGraph = graph
			}
			c.selected = min(c.selected, max(0, len(c.nodes)-1))
			c.center, changed = true, true
		default:
		}
	}
	return changed
}

// Imports are syntactic: relative file imports and Go module packages are
// resolved against known project files; other names remain import nodes.
func buildCanvasGraph(root string, files []string) canvasGraph {
	var g canvasGraph
	ids := map[string]int{}
	rows := map[int]int{}
	add := func(id, label, path, kind string, column int) int {
		if index, ok := ids[id]; ok {
			return index
		}
		if len(g.nodes) == maxCanvasNodes {
			g.limited = true
			return -1
		}
		index := len(g.nodes)
		ids[id] = index
		g.nodes = append(g.nodes, canvasNode{id, plain(label), path, kind, column, rows[column]})
		rows[column]++
		return index
	}
	seenEdges := map[canvasEdge]bool{}
	connect := func(from, to int, kind string) {
		edge := canvasEdge{from, to, kind}
		if from < 0 || to < 0 || seenEdges[edge] {
			return
		}
		if len(g.edges) >= 2000 {
			g.limited = true
			return
		}
		seenEdges[edge] = true
		g.edges = append(g.edges, edge)
	}
	sort.Strings(files)
	if len(files) > 400 {
		g.limited, files = true, files[:400]
	}
	maxColumn := 0
	add("dir:.", filepath.Base(root), ".", "folder", 0)
	for _, path := range files {
		path = filepath.ToSlash(path)
		column, parent := 0, 0
		dir := filepath.ToSlash(filepath.Dir(path))
		if dir != "." {
			parts := strings.Split(dir, "/")
			for depth := range parts {
				folder := strings.Join(parts[:depth+1], "/")
				next := add("dir:"+folder, parts[depth], folder, "folder", depth+1)
				connect(parent, next, "contains")
				parent = next
			}
			column = len(parts)
		}
		column++
		index := add("file:"+path, filepath.Base(path), path, "file", column)
		connect(parent, index, "contains")
		maxColumn = max(maxColumn, column)
	}
	module := ""
	if data, err := os.ReadFile(filepath.Join(root, "go.mod")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "module" {
				module = strings.Trim(fields[1], "\"")
				break
			}
		}
	}
	for _, path := range files {
		from, ok := ids["file:"+filepath.ToSlash(path)]
		if !ok {
			continue
		}
		absolute := workspacePath(root, path)
		info, err := os.Stat(absolute)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
			continue
		}
		for _, dependency := range fileImports(absolute) {
			to := -1
			if strings.HasPrefix(dependency, ".") {
				base := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(path), dependency)))
				for _, suffix := range []string{"", ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", "/index.ts", "/index.js"} {
					if index, found := ids["file:"+base+suffix]; found {
						to = index
						break
					}
				}
			} else if module != "" && (dependency == module || strings.HasPrefix(dependency, module+"/")) {
				dir := strings.TrimPrefix(strings.TrimPrefix(dependency, module), "/")
				if dir == "" {
					dir = "."
				}
				for _, local := range files {
					if filepath.ToSlash(filepath.Dir(local)) == dir && filepath.Ext(local) == ".go" {
						to = add("dir:"+dir, dir, dir, "folder", maxColumn+1)
						if index, found := ids["file:"+filepath.ToSlash(local)]; found {
							connect(to, index, "contains")
						}
					}
				}
			}
			if to < 0 {
				to = add("import:"+dependency, dependency, "", "import", maxColumn+1)
			}
			connect(from, to, "imports")
		}
		for _, model := range fileInterfaces(absolute) {
			to := add("interface:"+path+":"+model.name, model.name, path, "interface", maxColumn+1)
			connect(from, to, "defines")
		}
	}
	return g
}

// Re-project the bounded, cached graph only when folders change, never on draw.
func (c *nodeCanvas) organizeFolders() {
	selectedID := ""
	if c.selected < len(c.nodes) {
		selectedID = c.nodes[c.selected].id
	}
	g := canvasGraph{limited: c.source.limited}
	ids := map[string]int{}
	children := make([][]int, len(c.source.nodes))
	representative := make([]int, len(c.source.nodes))
	for i, node := range c.source.nodes {
		ids[node.id] = i
		representative[i] = -1
	}
	for i, node := range c.source.nodes {
		if node.id == "dir:." || node.kind != "folder" && node.kind != "file" {
			continue
		}
		if parent, ok := ids["dir:"+filepath.ToSlash(filepath.Dir(node.path))]; ok {
			children[parent] = append(children[parent], i)
		}
	}
	for _, group := range children {
		sort.Slice(group, func(i, j int) bool {
			a, b := c.source.nodes[group[i]], c.source.nodes[group[j]]
			if a.kind != b.kind {
				return a.kind == "folder"
			}
			return a.path < b.path
		})
	}
	row, maxColumn := 0, 0
	var visit func(int, int, int)
	visit = func(index, collapsedParent, column int) {
		node := c.source.nodes[index]
		if collapsedParent >= 0 {
			representative[index] = collapsedParent
			for _, child := range children[index] {
				visit(child, collapsedParent, column)
			}
			return
		}
		representative[index] = len(g.nodes)
		node.column, node.row = column, row
		g.nodes = append(g.nodes, node)
		maxColumn = max(maxColumn, column)
		if node.kind == "folder" && c.expanded[node.id] && len(children[index]) > 0 {
			for _, child := range children[index] {
				visit(child, -1, column+1)
			}
		} else {
			row++
			for _, child := range children[index] {
				visit(child, representative[index], column+1)
			}
		}
	}
	if root, ok := ids["dir:."]; ok {
		// The project itself is implicit; start with its folders in column zero.
		for _, child := range children[root] {
			visit(child, -1, 0)
		}
	}
	// Reveal external imports and interfaces only for files that are visible.
	detailRow := 0
	for _, edge := range c.source.edges {
		from, to := representative[edge.from], representative[edge.to]
		node := c.source.nodes[edge.to]
		if from < 0 || to >= 0 || node.kind != "import" && node.kind != "interface" {
			continue
		}
		if g.nodes[from].kind != "file" {
			continue
		}
		representative[edge.to] = len(g.nodes)
		node.column, node.row = maxColumn+1, detailRow
		detailRow++
		g.nodes = append(g.nodes, node)
	}
	seen := map[canvasEdge]bool{}
	for _, edge := range c.source.edges {
		edge.from, edge.to = representative[edge.from], representative[edge.to]
		if edge.from < 0 || edge.to < 0 || edge.from == edge.to || seen[edge] {
			continue
		}
		seen[edge] = true
		g.edges = append(g.edges, edge)
	}
	c.canvasGraph, c.selected = g, 0
	for i, node := range c.nodes {
		if node.id == selectedID {
			c.selected = i
			break
		}
	}
}

func (c *nodeCanvas) toggleFolder() bool {
	if c.expanded == nil || c.selected >= len(c.nodes) || c.nodes[c.selected].kind != "folder" {
		return false
	}
	id := c.nodes[c.selected].id
	c.expanded[id] = !c.expanded[id]
	c.organizeFolders()
	return true
}

func (e *editor) handleCanvasKey(c *nodeCanvas, k key) {
	switch k.code {
	case keyTab:
		if len(c.nodes) > 0 {
			c.selected = (c.selected + 1) % len(c.nodes)
			c.center = true
		}
	case keyEnter:
		e.openCanvasNode(c)
	case keyLeft:
		c.panX = max(0, c.panX-6)
	case keyRight:
		c.panX += 6
	case keyUp, keyPageUp:
		c.panY = max(0, c.panY-4)
	case keyDown, keyPageDown:
		c.panY += 4
	default:
		switch k.r {
		case '+', '=':
			c.zoom, c.center = min(2, c.zoom+1), true
		case '-':
			c.zoom, c.center = max(0, c.zoom-1), true
		case 'f', 'F':
			c.center = true
		case 'r', 'R':
			e.startNodeCanvas(e.opsMode == "architecture canvas", true)
		case '[', ']':
			for _, edge := range c.edges {
				if k.r == ']' && edge.from == c.selected {
					c.selected, c.center = edge.to, true
					break
				}
				if k.r == '[' && edge.to == c.selected {
					c.selected, c.center = edge.from, true
					break
				}
			}
		}
	}
}

func (e *editor) openCanvasNode(c *nodeCanvas) {
	if c.toggleFolder() {
		return
	}
	if len(c.nodes) == 0 {
		return
	}
	node := c.nodes[c.selected]
	if node.path == "" {
		e.status = "Import: " + node.label + " (not resolved to a local file)"
		return
	}
	if node.kind != "folder" {
		e.open(node.path)
		return
	}
	e.clearModalViews()
	e.showExplorer, e.explorer, e.sourceMode, e.testMode = true, true, false, false
	for path := node.path; path != "."; path = filepath.ToSlash(filepath.Dir(path)) {
		e.setFolderCollapsed(path, false)
	}
	for index, entry := range e.visibleTree() {
		if entry.path == node.path {
			e.selected = index
			break
		}
	}
	e.status = "Folder: " + node.path
}

func (e *editor) handleCanvasMouse(c *nodeCanvas, k key) {
	if k.release {
		c.dragging = false
		return
	}
	if k.button&32 != 0 && c.dragging {
		c.panX, c.panY = max(0, c.panX+c.dragX-k.x), max(0, c.panY+c.dragY-k.y)
		c.dragX, c.dragY = k.x, k.y
		return
	}
	if k.button == 64 || k.button == 65 {
		delta := 3
		if k.button == 64 {
			delta = -delta
		}
		c.panY = max(0, c.panY+delta)
		return
	}
	if k.button != 0 {
		return
	}
	for index, node := range c.nodes {
		x, y, width := c.nodePosition(node)
		if k.x >= c.x+x && k.x < c.x+x+width && k.y >= c.y+y && k.y < c.y+y+3 {
			c.selected = index
			if c.toggleFolder() {
				e.status = "Folder: " + node.label + " · click or Enter to expand/collapse"
				return
			}
			e.status = fmt.Sprintf("%s: %s · Enter open · [ ] follow links", node.kind, node.label)
			return
		}
	}
	c.dragging, c.dragX, c.dragY = true, k.x, k.y
}
