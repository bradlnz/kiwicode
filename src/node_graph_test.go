package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInteractiveNodeGraphs(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "lib"), 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"go.mod": "module example.test/app\n", "main.go": "package main\nimport (\"fmt\"; \"example.test/app/lib\")\n", "lib/api.go": "package lib\ntype Service interface { Run() }\n"}
	paths := []string{}
	for path, data := range files {
		if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	for _, architecture := range []bool{false, true} {
		g := buildCanvasGraph(root, paths)
		ids := map[string]int{}
		for i, node := range g.nodes {
			ids[node.id] = i
		}
		for _, id := range []string{"file:main.go", "dir:lib", "import:fmt", "interface:lib/api.go:Service"} {
			if _, ok := ids[id]; !ok {
				t.Fatalf("missing node %s", id)
			}
		}
		linked := false
		for _, edge := range g.edges {
			if edge.from == ids["file:main.go"] && edge.to == ids["dir:lib"] && edge.kind == "imports" {
				linked = true
			}
		}
		if !linked {
			t.Fatal("local package import is not connected")
		}
		c := &nodeCanvas{canvasGraph: g, zoom: 1, selected: ids["file:main.go"]}
		if !architecture {
			c.source, c.expanded = g, map[string]bool{"dir:.": true, "dir:lib": true}
			c.organizeFolders()
			for i, node := range c.nodes {
				ids[node.id] = i
			}
		}
		var out strings.Builder
		c.draw(&out, 1, 3, 220, 50)
		if !strings.Contains(out.String(), "╭") || !strings.Contains(out.String(), "▶") || !strings.Contains(out.String(), "Service") {
			t.Fatal("graph did not render connected visual nodes")
		}
		e := &editor{}
		e.handleCanvasKey(c, key{r: ']'})
		if c.selected == ids["file:main.go"] {
			t.Fatal("link navigation did not change selection")
		}
		e.handleCanvasKey(c, key{r: '+'})
		if c.zoom != 2 {
			t.Fatal("zoom did not change")
		}
		c.center = false
		x, y, _ := c.nodePosition(g.nodes[0])
		e.handleCanvasMouse(c, key{mouse: true, button: 0, x: c.x + x + 1, y: c.y + y + 1})
		if c.selected != 0 {
			t.Fatal("click did not select node")
		}
		e.handleCanvasKey(c, key{code: keyRight})
		if c.panX == 0 {
			t.Fatal("pan did not move")
		}
		out.Reset()
		c.draw(&out, 1, 3, 12, 5) // Clipped views must remain bounded.
		if len(c.cells) != 36 {
			t.Fatal("canvas allocated beyond its viewport")
		}
	}
	paths = make([]string, 700)
	for i := range paths {
		paths[i] = filepath.Join("generated", strings.Repeat("x", i+1)+".go")
	}
	if g := buildCanvasGraph(root, paths); !g.limited || len(g.nodes) > maxCanvasNodes {
		t.Fatal("large graph not bounded")
	}
}

func TestDependencyFolderExpansion(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":               "module example.test/app\n",
		"src/main.go":          "package main\nimport \"example.test/app/lib\"\n",
		"src/nested/helper.go": "package nested\n",
		"lib/api.go":           "package lib\nimport \"fmt\"\ntype Service interface { Run() }\n",
	}
	var paths []string
	for path, data := range files {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(path)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	c := &nodeCanvas{source: buildCanvasGraph(root, paths), expanded: map[string]bool{"dir:.": true}, zoom: 1}
	c.organizeFolders()
	index := func(id string) int {
		for i, node := range c.nodes {
			if node.id == id {
				return i
			}
		}
		return -1
	}
	if index("dir:lib") < 0 || index("dir:src") < 0 || index("file:lib/api.go") >= 0 || index("dir:src/nested") >= 0 || index("import:fmt") >= 0 {
		t.Fatalf("initial view is not folder-first: %+v", c.nodes)
	}
	if c.nodes[0].id != "dir:lib" || c.nodes[1].id != "dir:src" {
		t.Fatal("folders must be sorted before root files")
	}
	linked := false
	for _, edge := range c.edges {
		if c.nodes[edge.from].id == "dir:src" && c.nodes[edge.to].id == "dir:lib" && edge.kind == "imports" {
			linked = true
		}
	}
	if !linked {
		t.Fatal("collapsed folders lost cross-folder dependency")
	}
	e := &editor{graph: true, dependencyCanvas: c}
	click := func(id string) {
		t.Helper()
		var out strings.Builder
		c.draw(&out, 1, 3, 180, 60)
		i := index(id)
		if i < 0 {
			t.Fatalf("missing node %s", id)
		}
		x, y, _ := c.nodePosition(c.nodes[i])
		e.handleMouse(key{mouse: true, button: 0, x: c.x + x + 1, y: c.y + y + 1})
		e.handleMouse(key{mouse: true, release: true, button: 0, x: c.x + x + 1, y: c.y + y + 1})
	}
	click("dir:src")
	if !e.graph || index("file:src/main.go") < 0 || index("dir:src/nested") < 0 || index("file:src/nested/helper.go") >= 0 {
		t.Fatal("click did not expand only the immediate folder")
	}
	c.selected = index("dir:src/nested")
	e.handleCanvasKey(c, key{code: keyEnter})
	if index("file:src/nested/helper.go") < 0 {
		t.Fatal("Enter did not expand nested folder")
	}
	click("dir:src")
	if index("dir:src/nested") >= 0 || c.nodes[c.selected].id != "dir:src" {
		t.Fatal("collapse left hidden descendants or lost selection")
	}
	click("dir:src")
	if index("file:src/nested/helper.go") < 0 {
		t.Fatal("nested expansion state was not retained")
	}
	click("dir:lib")
	if index("file:lib/api.go") < 0 || index("import:fmt") < 0 || index("interface:lib/api.go:Service") < 0 {
		t.Fatal("expanded files did not reveal dependencies/interfaces")
	}
	// Refresh uses the same expansion state and keeps selection by ID.
	c.done = make(chan canvasGraph, 1)
	c.done <- buildCanvasGraph(root, paths)
	if !e.pollNodeCanvases() || index("file:src/nested/helper.go") < 0 || c.nodes[c.selected].id != "dir:lib" {
		t.Fatal("refresh lost expansion/selection")
	}
	click("dir:lib")
	if index("import:fmt") >= 0 || index("interface:lib/api.go:Service") >= 0 {
		t.Fatal("collapsed folder left orphan detail nodes")
	}
}
