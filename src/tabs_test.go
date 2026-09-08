package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestFileTabsFitViewport(t *testing.T) {
	e := &editor{rows: 30, cols: 240}
	for i := 0; i < 12; i++ {
		e.buffers = append(e.buffers, newBuffer(fmt.Sprintf("file%02d.go", i), nil))
	}
	if start, end := e.visibleTabRange(e.fileTabsWidth()); start != 0 || end != 12 {
		t.Fatal("wide screens are still limited to six file tabs")
	}
	for _, cols := range []int{0, 10, 40, 80, 140, 240} {
		for _, sidebar := range []bool{false, true} {
			{
				e.cols, e.showExplorer = cols, sidebar
				for _, view := range []bool{false, true} {
					e.help = view
					width := e.fileTabsWidth()
					for active := range e.buffers {
						e.active = active
						start, end := e.visibleTabRange(width)
						if width == 0 {
							if start != end {
								t.Fatal("tabs rendered outside the viewport")
							}
							continue
						}
						if active < start || active >= end {
							t.Fatal("active file tab is hidden")
						}
						x := 0
						for i := start; i < end; i++ {
							if got, _ := e.tabAt(x); got != i {
								t.Fatalf("tab click selected %d, want %d", got, i)
							}
							x += tabWidth(e.buffers[i])
						}
						if x > width && end-start != 1 {
							t.Fatal("partially visible extra file tab")
						}
						if i, _ := e.tabAt(min(width, x)); i != -1 {
							t.Fatal("off-screen or empty tab area accepted a click")
						}
					}
				}
			}
		}
	}
	e.cols, e.showExplorer, e.help = 100, false, false
	e.active = 8
	e.opsMode = "architecture canvas"
	if tabs := e.workspaceTabs(100); !strings.Contains(tabs, "Architecture Canvas") || strings.Contains(tabs, "Agent") {
		t.Fatal("architecture tab was removed or Agent tab remains")
	}
	start, _ := e.visibleTabRange(e.fileTabsWidth())
	e.selectWorkspaceTab(workspaceViewTabWidth + settings.tabPadding)
	if e.active != start || e.modalViewOpen() {
		t.Fatal("file click from architecture used the wrong tab offset")
	}
	e.selectWorkspaceTab(0)
	if e.modalViewOpen() {
		t.Fatal("first file tab still opens a separate workspace")
	}
	for _, action := range []string{"agent", "agent-cancel"} {
		if shortcutLabel(action) != "" || shortcutNames[action] != "" {
			t.Fatal("Agent shortcut remains registered")
		}
		if err := applySetting("shortcuts", action, "ctrl+a"); err != nil || shortcutLabel(action) != "" {
			t.Fatal("retired Agent config interrupted settings loading")
		}
	}
}
