package main

import (
	"fmt"
	"strings"
)

type canvasCell struct {
	r     rune
	style uint8
}

func (c *nodeCanvas) nodePosition(node canvasNode) (x, y, width int) {
	width = 18 + c.zoom*8
	return 2 + node.column*(width+12) - c.panX, 1 + node.row*(5+c.zoom) - c.panY, width
}

func (c *nodeCanvas) draw(out *strings.Builder, x, y, width, height int) {
	if width < 1 || height < 3 {
		return
	}
	c.x, c.y, c.width, c.height = x, y+1, width, height-2
	help := " Tab select · Enter open · arrows/drag pan · +/- zoom · [ ] links · F center · R refresh"
	if c.expanded != nil {
		help = " Click/Enter folder: expand/collapse · Tab select · Enter file: open · arrows/drag pan · +/- zoom · R refresh"
	}
	writeCell(out, y, x, ansiFG(colors.accent)+fit(help, width))
	if c.center && len(c.nodes) > 0 {
		nx, ny, nw := c.nodePosition(c.nodes[c.selected])
		c.panX = max(0, c.panX+nx+nw/2-width/2)
		c.panY = max(0, c.panY+ny+1-c.height/2)
		c.center = false
	}
	maxX, maxY := 0, 0
	for _, node := range c.nodes {
		nx, ny, nw := c.nodePosition(node)
		maxX, maxY = max(maxX, nx+c.panX+nw+6), max(maxY, ny+c.panY+5)
	}
	c.panX, c.panY = min(c.panX, max(0, maxX-width)), min(c.panY, max(0, maxY-c.height))
	if cap(c.cells) < width*c.height {
		c.cells = make([]canvasCell, width*c.height)
	} else {
		c.cells = c.cells[:width*c.height]
	}
	for i := range c.cells {
		c.cells[i] = canvasCell{' ', 0}
	}
	put := func(x, y int, r rune, style uint8) {
		if x >= 0 && x < width && y >= 0 && y < c.height {
			cell := &c.cells[y*width+x]
			if r == '─' && cell.r == '│' || r == '│' && cell.r == '─' {
				r = '┼'
			}
			*cell = canvasCell{r, style}
		}
	}
	horizontal := func(a, b, y int, style uint8) {
		if y < 0 || y >= c.height {
			return
		}
		for x := max(0, min(a, b)); x <= min(width-1, max(a, b)); x++ {
			put(x, y, '─', style)
		}
	}
	vertical := func(x, a, b int, style uint8) {
		if x < 0 || x >= width {
			return
		}
		for y := max(0, min(a, b)); y <= min(c.height-1, max(a, b)); y++ {
			put(x, y, '│', style)
		}
	}
	neighbors := make([]bool, len(c.nodes))
	// Draw selected connections last so they remain traceable at crossings.
	for pass := 0; pass < 2; pass++ {
		for _, edge := range c.edges {
			selected := edge.from == c.selected || edge.to == c.selected
			if selected != (pass == 1) {
				continue
			}
			style := uint8(0)
			if selected {
				style = 1
				neighbors[edge.from], neighbors[edge.to] = true, true
			}
			fx, fy, fw := c.nodePosition(c.nodes[edge.from])
			tx, ty, tw := c.nodePosition(c.nodes[edge.to])
			fx, fy, ty = fx+fw, fy+1, ty+1
			arrow := '▶'
			end := tx - 1
			bend := (fx + end) / 2
			if tx <= fx {
				end, bend, arrow = tx+tw, max(fx, tx+tw)+4, '◀'
			}
			horizontal(fx, bend, fy, style)
			vertical(bend, fy, ty, style)
			horizontal(bend, end, ty, style)
			if fy < ty {
				put(bend, fy, '┐', style)
				corner := '└'
				if arrow == '◀' {
					corner = '┘'
				}
				put(bend, ty, corner, style)
			} else if fy > ty {
				put(bend, fy, '┘', style)
				corner := '┌'
				if arrow == '◀' {
					corner = '┐'
				}
				put(bend, ty, corner, style)
			}
			put(end, ty, arrow, style)
		}
	}
	for index, node := range c.nodes {
		nx, ny, nw := c.nodePosition(node)
		if nx+nw <= 0 || nx >= width || ny+3 <= 0 || ny >= c.height {
			continue
		}
		style, icon := uint8(2), "◇"
		if node.kind == "folder" {
			style, icon = 5, "▣"
			if c.expanded != nil {
				icon = "▸"
				if c.expanded[node.id] {
					icon = "▾"
				}
			}
		} else if node.kind == "import" {
			icon = "↗"
		} else if node.kind == "interface" {
			icon = "I"
		}
		if neighbors[index] {
			style = 4
		}
		if index == c.selected {
			style = 3
		}
		lines := []string{
			"╭" + strings.Repeat("─", nw-2) + "╮",
			"│" + fit(" "+icon+" "+node.label, nw-2) + "│",
			"╰" + strings.Repeat("─", nw-2) + "╯",
		}
		for row, line := range lines {
			for col, r := range []rune(line) {
				put(nx+col, ny+row, r, style)
			}
		}
	}
	styles := []string{
		"\x1b[0;49m" + ansiFG(colors.muted),
		"\x1b[1;49m" + ansiFG(colors.function),
		"\x1b[22;49m" + ansiFG(colors.text),
		ansiBG(colors.menuActive, colors.function, "1"),
		"\x1b[1;49m" + ansiFG(colors.accent),
		"\x1b[22;49m" + ansiFG(colors.folder),
	}
	for row := 0; row < c.height; row++ {
		fmt.Fprintf(out, "\x1b[%d;%dH", c.y+row, c.x)
		lastStyle := uint8(255)
		for _, cell := range c.cells[row*width : (row+1)*width] {
			if cell.style != lastStyle {
				out.WriteString(styles[cell.style])
				lastStyle = cell.style
			}
			out.WriteRune(cell.r)
		}
	}
	footer := fmt.Sprintf(" %d nodes · %d links · zoom %d/3", len(c.nodes), len(c.edges), c.zoom+1)
	if len(c.nodes) > 0 {
		node := c.nodes[c.selected]
		name := node.path
		if name == "" {
			name = node.label
		}
		footer += " · " + node.kind + ": " + name
	}
	if c.limited {
		footer = " Graph limit reached (400 files / 600 nodes / 2000 links) ·" + footer
	}
	if c.done != nil {
		footer = " Loading graph…" + footer
	} else if len(c.nodes) == 0 {
		footer = " No project files to display · R refresh"
	}
	writeCell(out, y+height-1, x, ansiFG(colors.accent)+fit(plain(footer), width))
}
