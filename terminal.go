package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	mouseOn  = "\x1b[?1000h\x1b[?1002h\x1b[?1006h"
	mouseOff = "\x1b[?1000l\x1b[?1002l\x1b[?1006l"
)

func (e *editor) resize() {
	out, err := stty("size")
	if err != nil || len(strings.Fields(string(out))) != 2 {
		e.rows, e.cols = 24, 80
		return
	}
	fields := strings.Fields(string(out))
	e.rows, _ = strconv.Atoi(fields[0])
	e.cols, _ = strconv.Atoi(fields[1])
}

func (e *editor) sidebarWidth() int {
	if !e.showExplorer {
		return 0
	}
	width := sidebarActivityWidth()
	if e.sourceMode {
		width = max(width, 28)
		for _, change := range e.sourceChanges {
			width = max(width, len([]rune(sourceLabel(change)))+3)
		}
	} else if e.testMode {
		for _, test := range e.tests {
			width = max(width, len([]rune(testRowLabel(test)))+3)
		}
	} else {
		for _, entry := range e.visibleTree() {
			width = max(width, len([]rune(e.explorerTreeLabel(entry)))+3)
		}
	}
	return min(max(16, width), max(16, e.cols*settings.explorerMaxPercent/100))
}

func (e *editor) panelHeights() (content, shell int) {
	return max(1, e.rows-3), 0
}

func (e *editor) draw() {
	if e.rows < 5 || e.cols < 30 {
		fmt.Print("\x1b[H\x1b[2JTerminal must be at least 30x5")
		return
	}
	side := e.sidebarWidth()
	editorX := side
	availableWidth := e.cols - editorX
	inspectWidth := e.inspectorWidth(availableWidth)
	editorWidth := availableWidth - inspectWidth
	inspectX := editorX + editorWidth
	totalHeight := e.rows - 3
	contentHeight, _ := e.panelHeights()
	explorerEntries := e.visibleTree()
	e.selected = min(e.selected, max(0, len(explorerEntries)-1))
	b := e.current()
	gutter := len(strconv.Itoa(len(b.lines))) + 2
	codeWidth := max(1, editorWidth-gutter)
	wrapWidth := max(1, codeWidth-1)
	var wrappedRows []codeViewRow
	if contentHeight > 0 {
		if e.wordWrap {
			b.ensureWrappedVisible(contentHeight, wrapWidth)
			wrappedRows = b.wrappedRows(contentHeight, wrapWidth)
		} else {
			b.ensureVisible(contentHeight, editorWidth)
		}
	}
	firstLine, lastLine := b.scrollY, min(len(b.lines), b.scrollY+contentHeight)
	if len(wrappedRows) > 0 {
		firstLine, lastLine = wrappedRows[0].row, wrappedRows[len(wrappedRows)-1].row+1
	}
	rawHighlights := csharpRawHighlights(b.path, b.lines, firstLine, lastLine)
	markupDocument := isMarkupDocument(b.path, b.lines)
	highlightedLine := func(row int) string {
		if line, ok := rawHighlights[row]; ok {
			return line
		}
		if markupDocument {
			return highlightMarkup(expandLine(b.lines[row]))
		}
		return highlightLine(b.path, expandLine(b.lines[row]))
	}
	if e.sourceMode {
		visible := max(1, totalHeight-sourceHeaderRows)
		if e.sourceSelected < e.sourceTop {
			e.sourceTop = e.sourceSelected
		} else if e.sourceSelected >= e.sourceTop+visible {
			e.sourceTop = e.sourceSelected - visible + 1
		}
	} else if e.testMode {
		if e.testSelected < e.testTop {
			e.testTop = e.testSelected
		} else if e.testSelected >= e.testTop+totalHeight {
			e.testTop = e.testSelected - totalHeight + 1
		}
	} else if e.selected < e.explorerTop {
		e.explorerTop = e.selected
	} else if e.selected >= e.explorerTop+totalHeight {
		e.explorerTop = e.selected - totalHeight + 1
	}
	if e.opsMode == "architecture canvas" && e.canvasWidth != editorWidth {
		e.opsLines = architectureCanvas(e.tree, e.files, editorWidth)
		e.canvasWidth = editorWidth
	}

	var out strings.Builder
	out.WriteString("\x1b[0;49m\x1b[?25l\x1b[H")
	writeRow(&out, e.topBar(), e.cols)
	out.WriteString("\x1b[49m")

	if side > 0 {
		writeCell(&out, 2, 1, fitANSI(e.sidebarActivityBar(), side))
	}
	header := e.tabs(editorWidth)
	writeCell(&out, 2, editorX+1, "\x1b[22m"+ansiFG(colors.text)+header)
	if inspectWidth > 0 {
		writeCell(&out, 2, inspectX+1, "\x1b[1m"+ansiFG(colors.accent)+fit(" INSPECT · SOLID / DRY", inspectWidth))
	}
	inspectRows := e.inspectionRows(inspectWidth)
	commitLines := e.sourceCommitLines(max(1, side-2))

	for y := 0; y < totalHeight; y++ {
		row := y + 3
		if side > 0 {
			text := ""
			selected := false
			rowColor := colors.text
			if e.sourceMode {
				switch {
				case y < sourceTopPadding:
				case y == sourceMessageRow:
					text = " Commit message:"
				case y >= sourceInputStart && y < sourceCommitButtonRow:
					selected = e.sourceCommitFocused
					index := y - sourceInputStart
					if index < len(commitLines) {
						text = " " + commitLines[index]
					}
					if len(e.commitInput) == 0 && index == 0 {
						text = " Type message…"
						rowColor = colors.muted
					}
				case y == sourceCommitButtonRow:
					text = " [ Commit · Tab ]"
				default:
					idx := e.sourceTop + y - sourceHeaderRows
					selected = idx < len(e.sourceChanges) && idx == e.sourceSelected
					if idx < len(e.sourceChanges) {
						text = "  " + sourceLabel(e.sourceChanges[idx])
						if selected {
							text = "> " + sourceLabel(e.sourceChanges[idx])
						}
					} else if idx == 0 {
						text = "  " + e.sourceMessage
					}
				}
			} else if e.testMode {
				idx := e.testTop + y
				selected = idx < len(e.tests) && idx == e.testSelected
				if idx < len(e.tests) {
					text = "  " + testRowLabel(e.tests[idx])
					if selected {
						text = "> " + testRowLabel(e.tests[idx])
					}
				}
			} else {
				idx := e.explorerTop + y
				selected = idx < len(explorerEntries) && idx == e.selected
				if idx < len(explorerEntries) {
					if explorerEntries[idx].dir {
						rowColor = colors.folder
					}
					marker := "  "
					if selected {
						marker = "> "
					}
					text = marker + e.explorerTreeLabel(explorerEntries[idx])
				}
			}
			writeCell(&out, row, 1, style(selected, "\x1b[1m"+ansiFG(colors.accent), "\x1b[22m"+ansiFG(rowColor))+fit(text, side))
		}

		line := ""
		if e.wordWrap && y < len(wrappedRows) {
			view := wrappedRows[y]
			marker := rune(0)
			if e.sourceMode {
				marker = e.sourceLineChanges[b.path][view.row+1]
			}
			number := strings.Repeat(" ", gutter)
			if view.segment == 0 {
				number = fmt.Sprintf("%*d", gutter-1, view.row+1) + e.sourceGutter(b.path, view.row+1)
			}
			line = sourceLineHighlight(marker) + ansiFG(colors.muted) + number + ansiFG(colors.text) + cropANSI(highlightedLine(view.row), view.segment*wrapWidth, wrapWidth)
		} else if !e.wordWrap {
			lineIndex := b.scrollY + y
			if lineIndex < len(b.lines) {
				marker := rune(0)
				if e.sourceMode {
					marker = e.sourceLineChanges[b.path][lineIndex+1]
				}
				number := fmt.Sprintf("%*d", gutter-1, lineIndex+1) + e.sourceGutter(b.path, lineIndex+1)
				line = sourceLineHighlight(marker) + ansiFG(colors.muted) + number + ansiFG(colors.text) + cropANSI(highlightedLine(lineIndex), b.scrollX, codeWidth)
			}
		}
		writeCell(&out, row, editorX+1, "\x1b[49m"+fitANSI(line, editorWidth))
		if inspectWidth > 0 {
			inspectLine := ""
			index := e.inspectionTop + y
			if index < len(inspectRows) {
				entry := inspectRows[index]
				selected := entry.finding >= 0 && entry.finding == e.inspectionSelected
				inspectLine = style(selected, ansiBG(colors.menuActive, colors.text, "1"), ansiFG(colors.muted)) + entry.text
			}
			writeCell(&out, row, inspectX+1, fitANSI(inspectLine, inspectWidth))
		}
	}
	e.drawSelection(&out, editorX, contentHeight)

	activeMode := mode(e.explorer)
	if e.graph {
		activeMode = "GRAPH"
	}
	if e.help {
		activeMode = "HELP"
	}
	if e.explorer && e.testMode {
		activeMode = "TESTS"
	}
	if e.explorer && e.sourceMode {
		activeMode = "SOURCE"
	}
	if e.opsMode != "" {
		activeMode = strings.ToUpper(e.opsMode)
	}
	if e.shell.focused {
		activeMode = "TERMINAL"
	}
	left := fmt.Sprintf(" %s  Ln %d, Col %d ", activeMode, b.row+1, b.col+1)
	right := " " + e.status + " "
	status := left
	if len([]rune(left))+len([]rune(right)) <= e.cols {
		status += strings.Repeat(" ", e.cols-len([]rune(left))-len([]rune(right))) + right
	}
	writeCell(&out, e.rows, 1, "\x1b[49;1m"+ansiFG(colors.accent)+fit(status, e.cols))
	modalRow, modalCol := e.drawModal(&out, editorX, editorWidth, totalHeight)

	if e.popup != nil {
		for i, item := range e.popup.items {
			itemStyle := ansiBG(colors.menu, colors.text, "22")
			if i == e.popup.selected {
				itemStyle = ansiBG(colors.menuActive, colors.text, "1")
			}
			padding := strings.Repeat(" ", settings.popupPadding)
			writeCell(&out, e.popup.y+i, e.popup.x, itemStyle+fit(padding+displayMenuItem(item)+padding, e.popup.width))
		}
	}
	if e.searchMode != "" {
		width := min(60, e.cols-4)
		x := max(2, (e.cols-width)/2)
		title := " Search files: "
		if e.searchMode == "functions" {
			title = " Search functions: "
		}
		writeCell(&out, 2, x, ansiBG(colors.menuActive, colors.accent, "1")+fit(title+string(e.searchInput), width))
		for i, result := range e.searchResults {
			resultStyle := ansiBG(colors.menu, colors.text, "22")
			if i == e.searchSelected {
				resultStyle = ansiBG(colors.menuActive, colors.text, "1")
			}
			writeCell(&out, 3+i, x, resultStyle+fit(" "+result.label, width))
		}
	}
	if e.folderPrompt {
		width := min(70, e.cols-4)
		x := max(2, (e.cols-width)/2)
		writeCell(&out, 2, x, ansiBG(colors.menuActive, colors.accent, "1")+fit(" Open folder", width))
		writeCell(&out, 3, x, ansiBG(colors.menu, colors.text, "22")+fit(" Path: "+string(e.folderInput), width))
		writeCell(&out, 4, x, ansiBG(colors.menu, colors.muted, "2")+fit(" ↑↓ choose · Tab complete · Enter open · Esc cancel", width))
		for index, prediction := range e.folderPredictions {
			predictionStyle := ansiBG(colors.menu, colors.text, "22")
			if index == e.folderPredictionSelected {
				predictionStyle = ansiBG(colors.menuActive, colors.text, "1")
			}
			writeCell(&out, 5+index, x, predictionStyle+fit(" "+prediction, width))
		}
	}
	if e.newFilePrompt {
		width := min(70, e.cols-4)
		x := max(2, (e.cols-width)/2)
		title := " New file · " + e.newFileDirectory
		writeCell(&out, 2, x, ansiBG(colors.menuActive, colors.accent, "1")+fit(title, width))
		writeCell(&out, 3, x, ansiBG(colors.menu, colors.text, "22")+fit(" Name: "+string(e.newFileInput), width))
		writeCell(&out, 4, x, ansiBG(colors.menu, colors.muted, "2")+fit(" Enter create · Esc cancel", width))
	}
	if e.newFilePrompt {
		width := min(70, e.cols-4)
		x := max(2, (e.cols-width)/2) + len([]rune(" Name: ")) + len(e.newFileInput)
		fmt.Fprintf(&out, "\x1b[3;%dH\x1b[?25h", min(e.cols, x))
	} else if e.folderPrompt {
		width := min(70, e.cols-4)
		x := max(2, (e.cols-width)/2) + len([]rune(" Path: ")) + len(e.folderInput)
		fmt.Fprintf(&out, "\x1b[3;%dH\x1b[?25h", min(e.cols, x))
	} else if e.searchMode != "" {
		width := min(60, e.cols-4)
		title := " Search files: "
		if e.searchMode == "functions" {
			title = " Search functions: "
		}
		x := max(2, (e.cols-width)/2) + len([]rune(title)) + len(e.searchInput)
		fmt.Fprintf(&out, "\x1b[2;%dH\x1b[?25h", min(e.cols, x))
	} else if e.popup != nil {
		// Menus own the cursor while open.
	} else if e.sourceCommitFocused {
		lines := e.sourceCommitLines(max(1, e.sidebarWidth()-2))
		row := 3 + sourceInputStart + len(lines) - 1
		column := min(e.sidebarWidth(), len([]rune(lines[len(lines)-1]))+2)
		fmt.Fprintf(&out, "\x1b[%d;%dH\x1b[?25h", row, max(1, column))
	} else if modalRow > 0 {
		fmt.Fprintf(&out, "\x1b[%d;%dH\x1b[?25h", modalRow, modalCol)
	} else if !e.explorer && !e.graph && !e.help && e.opsMode == "" {
		cell := cursorCell(b.lines[b.row], b.col)
		x, y := editorX+gutter+cell-b.scrollX+1, 3+b.row-b.scrollY
		if e.wordWrap {
			cursor := b.cursorViewRow(wrapWidth)
			x = editorX + gutter + cell - cursor.segment*wrapWidth + 1
			for index, view := range wrappedRows {
				if view == cursor {
					y = 3 + index
					break
				}
			}
		}
		if suggestion := e.suggestion(); len(suggestion) > 0 && y >= 3 && y < 3+contentHeight && x <= editorX+editorWidth {
			writeCell(&out, y, x, "\x1b[2;37m"+fit(string(suggestion), editorX+editorWidth-x+1))
		}
		if members := e.memberSuggestions(); len(members) > 0 && y >= 3 && y < 3+contentHeight {
			count, width := min(7, len(members)), len(" Tab  accept ")
			for _, member := range members[:count] {
				width = max(width, len([]rune(member))+3)
			}
			width = min(width, editorWidth)
			popupX := min(max(editorX+1, x), editorX+editorWidth-width+1)
			popupY := y + 1
			if popupY+count >= 3+contentHeight {
				popupY = max(3, y-count)
			}
			for index, member := range members[:count] {
				label := "   " + member
				itemStyle := ansiBG(colors.menu, colors.text, "22")
				if index == 0 {
					label = " ▸ " + member + "  Tab"
					itemStyle = ansiBG(colors.menuActive, colors.text, "1")
				}
				writeCell(&out, popupY+index, popupX, itemStyle+fit(label, width))
			}
		}
		fmt.Fprintf(&out, "\x1b[%d;%dH\x1b[?25h", y, x)
	}
	out.WriteString("\x1b[0m")
	fmt.Print(out.String())
}

func (e *editor) drawModal(out *strings.Builder, editorX, editorWidth, totalHeight int) (cursorRow, cursorCol int) {
	if !e.modalViewOpen() {
		return 0, 0
	}
	width, height := min(100, editorWidth), min(24, totalHeight)
	if width < 8 || height < 3 {
		return 0, 0
	}
	x, y := editorX+(editorWidth-width)/2+1, 3+(totalHeight-height)/2
	innerWidth, contentRows := width-2, height-2
	title, prompt := "", ""
	var lines []string
	switch {
	case e.shell.open:
		title, prompt = e.shellTitleAndPrompt()
		title = strings.TrimSpace(title)
		contentRows--
		lines = e.shell.visibleLines(max(0, contentRows))
	case e.help:
		title, lines = "KEYBOARD SHORTCUTS", shortcutHelpLines()
	case e.graph:
		title = "DEPENDENCY GRAPH"
		lines = e.graphLines[min(e.graphTop, len(e.graphLines)):]
	case e.opsMode != "":
		title = strings.ToUpper(e.opsMode) + " VIEW"
		lines = e.opsLines[min(e.opsTop, len(e.opsLines)):]
	}
	modalStyle := ansiBG(colors.menu, colors.text, "22")
	headingStyle := modalStyle
	heading := fit(" "+title+" · Esc close ", innerWidth)
	writeCell(out, y, x, headingStyle+"╭"+heading+"╮")
	for row := 0; row < height-2; row++ {
		text := ""
		if row < contentRows && row < len(lines) {
			text = " " + plain(lines[row])
		} else if prompt != "" && row == height-3 {
			text, _ = panelPromptLine(" "+prompt, innerWidth)
		}
		writeCell(out, y+row+1, x, modalStyle+"│"+fitANSI(text, innerWidth)+modalStyle+"│")
	}
	writeCell(out, y+height-1, x, modalStyle+"╰"+strings.Repeat("─", innerWidth)+"╯")
	if prompt != "" {
		_, cursor := panelPromptLine(" "+prompt, innerWidth)
		return y + height - 2, x + 1 + cursor
	}
	return 0, 0
}

func (e *editor) sourceGutter(path string, row int) string {
	marker := e.sourceLineChanges[path][row]
	if !e.sourceMode || marker == 0 {
		return " "
	}
	color := colors.function
	if marker == '+' {
		color = colors.stringValue
	} else if marker == '-' {
		color = colors.parameter
	}
	return ansiFG(color) + string(marker) + ansiFG(colors.muted)
}

func sourceLineHighlight(marker rune) string {
	switch marker {
	case '+':
		return "\x1b[48;5;22m"
	case '~':
		return "\x1b[48;5;24m"
	case '-':
		return "\x1b[48;5;52m"
	}
	return ""
}

func treeLabel(entry treeEntry) string {
	depth := strings.Count(entry.path, "/")
	name := filepath.Base(entry.path)
	if entry.dir {
		return strings.Repeat(" ", depth*settings.explorerIndent) + settings.icons["folder_open"] + " " + name + "/"
	}
	return strings.Repeat(" ", (depth+1)*settings.explorerIndent) + name
}

type sidebarTab struct{ mode string }

var sidebarTabs = []sidebarTab{{"files"}, {"tests"}, {"source"}}

func sidebarModeAt(column int) (string, bool) {
	if column < 1 {
		return "", false
	}
	x := 1
	for _, tab := range sidebarTabs {
		width := len([]rune(settings.icons[tab.mode])) + settings.sidebarTabPadding*2
		if column >= x && column < x+width {
			return tab.mode, true
		}
		x += width
	}
	return "", false
}

func sidebarActivityWidth() int {
	width := 0
	for _, tab := range sidebarTabs {
		width += len([]rune(settings.icons[tab.mode])) + settings.sidebarTabPadding*2
	}
	return width
}

func (e *editor) sidebarActivityBar() string {
	var bar strings.Builder
	for _, tab := range sidebarTabs {
		active := e.explorer && (tab.mode == "files" && !e.testMode && !e.sourceMode || tab.mode == "tests" && e.testMode || tab.mode == "source" && e.sourceMode)
		padding := strings.Repeat(" ", settings.sidebarTabPadding)
		bar.WriteString(padding + style(active, "\x1b[1;4m"+ansiFG(colors.accent), "\x1b[22;24m"+ansiFG(colors.text)) + settings.icons[tab.mode] + "\x1b[22;24m" + ansiFG(colors.text) + padding)
	}
	return bar.String()
}

func (e *editor) explorerTreeLabel(entry treeEntry) string {
	depth := strings.Count(entry.path, "/")
	name := filepath.Base(entry.path)
	if entry.dir {
		icon := settings.icons["folder_open"]
		if e.collapsed[entry.path] {
			icon = settings.icons["folder_closed"]
		}
		return strings.Repeat(" ", depth*settings.explorerIndent) + icon + " " + name + "/"
	}
	return strings.Repeat(" ", (depth+1)*settings.explorerIndent) + name
}

func (e *editor) shellTitleAndPrompt() (string, string) {
	if e.shell.running {
		if e.shell.stopping {
			return " TERMINAL — stopping…  Ctrl+C force", "Waiting for process to exit…"
		}
		return " TERMINAL — running…  Ctrl+C stop", "Command is running…"
	}
	return " TERMINAL", "> " + string(e.shell.input)
}

func panelPromptLine(text string, width int) (string, int) {
	prompt := []rune(text)
	if len(prompt) > width {
		prompt = append([]rune("…"), prompt[len(prompt)-width+1:]...)
	}
	return fit(string(prompt), width), min(len(prompt), max(0, width-1))
}

func (e *editor) tabs(width int) string {
	var s strings.Builder
	start, end := e.visibleTabRange()
	for i := start; i < end; i++ {
		b := e.buffers[i]
		if i == e.active {
			s.WriteString("\x1b[1;4m" + ansiFG(colors.text))
		} else {
			s.WriteString("\x1b[22;24m" + ansiFG(colors.muted))
		}
		padding := strings.Repeat(" ", settings.tabPadding)
		s.WriteString(padding + tabLabel(b) + padding + "×" + padding + "\x1b[22;24m" + ansiFG(colors.text))
	}
	return fitANSI(s.String(), width)
}

func tabLabel(b *buffer) string {
	name := plain(filepath.Base(b.path))
	if b.dirty {
		name += " *"
	}
	return name
}

func tabWidth(b *buffer) int { return len([]rune(tabLabel(b))) + settings.tabPadding*3 + 1 }

func (e *editor) visibleTabRange() (int, int) {
	start := max(0, min(e.active, len(e.buffers)-1)-5)
	return start, min(len(e.buffers), start+6)
}

func mode(explorer bool) string {
	if explorer {
		return "EXPLORER"
	}
	return "EDITOR"
}

func style(active bool, yes, no string) string {
	if active {
		return yes
	}
	return no
}

func mustCwd() string {
	cwd, _ := os.Getwd()
	return cwd
}

func writeRow(out *strings.Builder, text string, width int) {
	out.WriteString(fitANSI(text, width))
}

func writeCell(out *strings.Builder, row, col int, text string) {
	// Reset attributes per cell so an underline or background cannot leak into the next row.
	fmt.Fprintf(out, "\x1b[%d;%dH\x1b[0;49m%s", row, col, text)
}

func fit(s string, width int) string {
	r := []rune(plain(s))
	if len(r) > width {
		if width > 1 {
			return string(r[:width-1]) + "…"
		}
		return string(r[:width])
	}
	return s + strings.Repeat(" ", width-len(r))
}

func plain(s string) string {
	r := []rune(s)
	for i := range r {
		if r[i] < 32 || r[i] == 127 {
			r[i] = ' '
		}
	}
	return string(r)
}

func fitANSI(s string, width int) string {
	visible := 0
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			if end := strings.IndexByte(s[i:], 'm'); end >= 0 {
				i += end + 1
				continue
			}
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		visible++
		i += size
	}
	if visible >= width {
		var out strings.Builder
		seen := 0
		for i := 0; i < len(s) && seen < width; {
			if s[i] == '\x1b' {
				if end := strings.IndexByte(s[i:], 'm'); end >= 0 {
					out.WriteString(s[i : i+end+1])
					i += end + 1
					continue
				}
			}
			_, size := utf8.DecodeRuneInString(s[i:])
			out.WriteString(s[i : i+size])
			i += size
			seen++
		}
		return out.String()
	}
	return s + strings.Repeat(" ", width-visible)
}

func cropANSI(s string, start, width int) string {
	var out strings.Builder
	seen, written := 0, 0
	for i := 0; i < len(s) && written < width; {
		if s[i] == '\x1b' {
			if end := strings.IndexByte(s[i:], 'm'); end >= 0 {
				out.WriteString(s[i : i+end+1])
				i += end + 1
				continue
			}
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		if seen >= start {
			out.WriteString(s[i : i+size])
			written++
		}
		seen++
		i += size
	}
	out.WriteString(ansiFG(colors.text))
	out.WriteString(strings.Repeat(" ", max(0, width-written)))
	return out.String()
}

func readKey() (key, bool) {
	var first [1]byte
	n, err := os.Stdin.Read(first[:])
	if err != nil || n == 0 {
		return key{}, false
	}
	b := first[0]
	switch b {
	case '\r', '\n':
		return key{code: keyEnter}, true
	case '\t':
		return key{code: keyTab}, true
	case 8, 127:
		return key{code: keyBackspace}, true
	case 27:
		return readEscape(), true
	}
	if b < utf8.RuneSelf {
		return key{r: rune(b)}, true
	}
	buf := []byte{b}
	for !utf8.FullRune(buf) && len(buf) < utf8.UTFMax {
		var next [1]byte
		if n, _ := os.Stdin.Read(next[:]); n == 0 {
			break
		}
		buf = append(buf, next[0])
	}
	r, _ := utf8.DecodeRune(buf)
	return key{r: r}, true
}

func readEscape() key {
	var seq [3]byte
	n, _ := os.Stdin.Read(seq[:1])
	if n == 0 || seq[0] != '[' {
		return key{}
	}
	n, _ = os.Stdin.Read(seq[1:2])
	if n == 0 {
		return key{}
	}
	switch seq[1] {
	case '<':
		return readMouse()
	case 'A':
		return key{code: keyUp}
	case 'B':
		return key{code: keyDown}
	case 'C':
		return key{code: keyRight}
	case 'D':
		return key{code: keyLeft}
	case 'H':
		return key{code: keyHome}
	case 'F':
		return key{code: keyEnd}
	case '3', '5', '6':
		n, _ = os.Stdin.Read(seq[2:3])
		if n > 0 && seq[2] == '~' {
			return key{code: map[byte]int{'3': keyDelete, '5': keyPageUp, '6': keyPageDown}[seq[1]]}
		}
	}
	return key{}
}

func readMouse() key {
	var payload []byte
	for len(payload) < 32 {
		var next [1]byte
		if n, _ := os.Stdin.Read(next[:]); n == 0 {
			return key{}
		}
		if next[0] == 'M' || next[0] == 'm' {
			return parseMouse(string(payload), next[0] == 'm')
		}
		payload = append(payload, next[0])
	}
	return key{}
}

func parseMouse(payload string, release bool) key {
	parts := strings.Split(payload, ";")
	if len(parts) != 3 {
		return key{}
	}
	button, errButton := strconv.Atoi(parts[0])
	x, errX := strconv.Atoi(parts[1])
	y, errY := strconv.Atoi(parts[2])
	if errButton != nil || errX != nil || errY != nil || x < 1 || y < 1 {
		return key{}
	}
	return key{mouse: true, release: release, button: button, x: x, y: y}
}
