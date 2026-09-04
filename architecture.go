package main

import (
	"path/filepath"
	"strings"
)

func (e *editor) openArchitecture() {
	e.clearModalViews()
	e.opsMode, e.opsTop = "architecture canvas", 0
	e.canvasWidth = -1
	e.explorer = false
}

func architectureCanvas(tree []treeEntry, files []string, width int) []string {
	folders := []string{"◆ " + filepath.Base(mustCwd())}
	for _, entry := range tree {
		depth := strings.Count(entry.path, "/")
		icon := "◇"
		if entry.dir {
			icon = "▣"
		}
		folders = append(folders, strings.Repeat("│ ", depth)+"├─"+icon+" "+filepath.Base(entry.path))
		if len(folders) == 36 {
			folders = append(folders, "… folder canvas truncated")
			break
		}
	}
	dependencies := dependencyGraph(files)
	if len(dependencies) > 36 {
		dependencies = append(dependencies[:36], "… dependency canvas truncated")
	}
	if width >= 72 {
		column := (width - 3) / 2
		left := canvasBox("FOLDERS", folders, column)
		right := canvasBox("DIRECT DEPENDENCIES", dependencies, width-column-3)
		height := max(len(left), len(right))
		var canvas []string
		for row := 0; row < height; row++ {
			l, r := strings.Repeat(" ", column), ""
			if row < len(left) {
				l = fit(left[row], column)
			}
			if row < len(right) {
				r = right[row]
			}
			canvas = append(canvas, l+"   "+r)
		}
		return canvas
	}
	return append(canvasBox("FOLDERS", folders, width), append([]string{""}, canvasBox("DIRECT DEPENDENCIES", dependencies, width)...)...)
}

func canvasBox(title string, lines []string, width int) []string {
	width = max(12, width)
	top := "╭─ " + title + " "
	top += strings.Repeat("─", max(0, width-len([]rune(top))-1)) + "╮"
	out := []string{fit(top, width)}
	for _, line := range lines {
		out = append(out, "│ "+fit(line, width-4)+" │")
	}
	out = append(out, "╰"+strings.Repeat("─", width-2)+"╯")
	return out
}
