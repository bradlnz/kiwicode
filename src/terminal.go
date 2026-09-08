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
			width = max(width, len([]rune(testRowLabel(test)))+3+testButtonWidth)
		}
	} else {
		e.visibleTree()
		width = max(width, e.visibleTreeWidth)
	}
	return min(max(16, width), max(16, e.cols*settings.explorerMaxPercent/100))
}

func (e *editor) codeAreaWidth() int { return e.cols - e.sidebarWidth() }

func (e *editor) panelHeights() (content, shell int) {
	content = max(1, e.rows-3)
	if e.shell.open && content >= 7 {
		shell = min(max(4, settings.terminalHeight+1), content-5)
		content -= shell
	}
	return content, shell
}

func (e *editor) draw() {
	if e.switching != nil {
		fmt.Print(e.workspaceSwitchFrame())
		return
	}
	if e.rows < 5 || e.cols < 30 {
		fmt.Print("\x1b[H\x1b[2JTerminal must be at least 30x5")
		return
	}
	side := e.sidebarWidth()
	editorX := side
	editorWidth := e.cols - editorX
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

	var out strings.Builder
	out.WriteString("\x1b[0;49m\x1b[?25l\x1b[H")
	writeRow(&out, e.topBar(), e.cols)
	out.WriteString("\x1b[49m")

	if side > 0 {
		writeCell(&out, 2, 1, fitANSI(e.sidebarActivityBar(), side))
	}
	header := e.workspaceTabs(editorWidth)
	writeCell(&out, 2, editorX+1, "\x1b[22m"+ansiFG(colors.text)+header)
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
				} else if len(e.tests) == 0 && y == 0 {
					text = " No tests found"
					rowColor = colors.muted
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
				} else if len(explorerEntries) == 0 {
					if y == 0 {
						text = " No files found"
					} else if y == 1 {
						text = " " + shortcutLabel("new") + " new file"
					}
					rowColor = colors.muted
				}
			}
			writeCell(&out, row, 1, style(selected, "\x1b[1m"+ansiFG(colors.accent), "\x1b[22m"+ansiFG(rowColor))+fit(text, side))
			if e.testMode && !e.sourceMode && e.testTop+y < len(e.tests) {
				writeCell(&out, row, 1, style(selected, "\x1b[1m"+ansiFG(colors.accent), "\x1b[22m"+ansiFG(rowColor))+fit(text, side-testButtonWidth))
				writeCell(&out, row, side-testButtonWidth+1, ansiBG(colors.menu, colors.accent, "1")+e.testButtonLabel(e.tests[e.testTop+y]))
			}
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
		} else if !e.wordWrap && y < contentHeight {
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
	modalRow, modalCol := e.drawModal(&out, editorX, editorWidth, contentHeight)
	terminalRow, terminalCol := e.drawTerminalPanel(&out)
	if e.workspaceDone != nil {
		writeCell(&out, max(3, e.rows/2), editorX+1, ansiFG(colors.accent)+fit(" Loading project…", editorWidth))
	}

	e.drawPopup(&out)
	if e.searchMode != "" {
		width := min(60, e.cols-4)
		x := max(2, (e.cols-width)/2)
		title := " Search files: "
		if e.searchMode == "functions" {
			title = " Search functions: "
		}
		writeCell(&out, 2, x, ansiBG(colors.menuActive, colors.accent, "1")+fit(title+string(e.searchInput), width))
		if len(e.searchResults) == 0 {
			message := " No matching files"
			if e.searchMode == "functions" {
				message = " No matching functions"
			}
			writeCell(&out, 3, x, ansiBG(colors.menu, colors.muted, "22")+fit(message, width))
		}
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
		title, hint := " Open folder", " ↑↓ choose · Tab complete · Enter open · Esc cancel"
		if e.newProjectPrompt {
			title, hint = " New project folder", " Tab complete parent · Enter create · Esc cancel"
		}
		writeCell(&out, 2, x, ansiBG(colors.menuActive, colors.accent, "1")+fit(title, width))
		writeCell(&out, 3, x, ansiBG(colors.menu, colors.text, "22")+fit(" Path: "+string(e.folderInput), width))
		writeCell(&out, 4, x, ansiBG(colors.menu, colors.muted, "2")+fit(hint, width))
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
	} else if e.shell.open && e.shell.focused {
		if terminalRow > 0 {
			fmt.Fprintf(&out, "\x1b[%d;%dH\x1b[?25h", terminalRow, terminalCol)
		}
	} else if modalRow > 0 {
		fmt.Fprintf(&out, "\x1b[%d;%dH\x1b[?25h", modalRow, modalCol)
	} else if !e.explorer && !e.modalViewOpen() {
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
	e.lastFrame = out.String()
	fmt.Print(e.lastFrame)
}

func (e *editor) drawModal(out *strings.Builder, editorX, editorWidth, totalHeight int) (cursorRow, cursorCol int) {
	if !e.modalViewOpen() {
		return 0, 0
	}
	if c := e.activeNodeCanvas(); c != nil {
		c.draw(out, editorX+1, 3, editorWidth, totalHeight)
		return 0, 0
	}
	width, height := editorWidth, totalHeight
	if width < 8 || height < 3 {
		return 0, 0
	}
	x, y := editorX+1, 3
	var lines []string
	switch {
	case e.help:
		lines = shortcutHelpLines()
	case e.opsMode != "":
		lines = e.opsLines[min(e.opsTop, len(e.opsLines)):]
	}
	viewStyle := ansiFG(colors.text)
	for row := 0; row < height; row++ {
		text := ""
		if row < len(lines) {
			text = " " + plain(lines[row])
		}
		writeCell(out, y+row, x, viewStyle+fitANSI(text, width))
	}
	return 0, 0
}

func (e *editor) drawTerminalPanel(out *strings.Builder) (int, int) {
	content, height := e.panelHeights()
	if height == 0 {
		return 0, 0
	}
	x, y, width := e.sidebarWidth()+1, 3+content, e.cols-e.sidebarWidth()
	if width < 1 {
		return 0, 0
	}
	title, prompt := e.shellTitleAndPrompt()
	writeCell(out, y, x, ansiBG(colors.menu, colors.accent, "1")+fit(title+" · Ctrl+T hide · click to focus", width))
	y++
	height--
	if e.shell.interactive && e.shell.terminal != nil {
		return e.shell.terminal.draw(out, x, y, width, height)
	}
	lines := e.shell.visibleLines(max(0, height-1))
	for row := 0; row < height; row++ {
		text := ""
		if row < len(lines) {
			text = " " + plain(lines[row])
		}
		if row == height-1 {
			text, _ = panelPromptLine(" "+prompt, width)
		}
		writeCell(out, y+row, x, ansiFG(colors.text)+fitANSI(text, width))
	}
	_, cursor := panelPromptLine(" "+prompt, width)
	return y + height - 1, x + cursor
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
		bar.WriteString(padding + style(active, "\x1b[1m"+ansiFG(colors.accent), "\x1b[22m"+ansiFG(colors.text)) + settings.icons[tab.mode] + "\x1b[22m" + ansiFG(colors.text) + padding)
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
	if e.shell.testRun {
		if e.shell.running {
			if e.shell.stopping {
				return " TESTS - stopping", "Click Stop Tests again to force stop"
			}
			return " TESTS - running", "Output appears when finished; click Stop Tests to cancel"
		}
		return " TEST RESULTS", "Click Run Tests to run again"
	}
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

const workspaceViewTabWidth = 22

func (e *editor) workspaceTabs(width int) string {
	if width <= 0 {
		return ""
	}
	prefix := ""
	if label := e.workspaceViewLabel(); label != "" {
		viewWidth := min(width, workspaceViewTabWidth)
		prefix = "\x1b[1m" + ansiFG(colors.function) + fit(" "+label, viewWidth) + "\x1b[22m"
		width -= viewWidth
	}
	return prefix + e.tabs(width)
}

func (e *editor) selectWorkspaceTab(column int) {
	if column < 0 || column >= e.codeAreaWidth() {
		return
	}
	if e.modalViewOpen() {
		if column < workspaceViewTabWidth {
			return
		}
		column -= workspaceViewTabWidth
	}
	// Resolve the file while the view still reserves its tab width.
	e.selectTab(column)
}

func (e *editor) workspaceViewLabel() string {
	switch {
	case e.opsMode == "architecture canvas":
		return "Architecture Canvas"
	case e.opsMode != "":
		return e.opsMode
	case e.graph:
		return "Dependency Graph"
	case e.help:
		return "Shortcuts"
	}
	return ""
}

func (e *editor) fileTabsWidth() int {
	width := e.codeAreaWidth()
	if e.modalViewOpen() {
		width -= workspaceViewTabWidth
	}
	return max(0, width)
}

func (e *editor) tabs(width int) string {
	var s strings.Builder
	start, end := e.visibleTabRange(width)
	for i := start; i < end; i++ {
		b := e.buffers[i]
		if !e.modalViewOpen() && i == e.active {
			s.WriteString("\x1b[1m" + ansiFG(colors.text))
		} else {
			s.WriteString("\x1b[22m" + ansiFG(colors.muted))
		}
		padding := strings.Repeat(" ", settings.tabPadding)
		s.WriteString(padding + tabLabel(b) + padding + "×" + padding + "\x1b[22m" + ansiFG(colors.text))
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

func (e *editor) visibleTabRange(width int) (int, int) {
	if width <= 0 || len(e.buffers) == 0 {
		return 0, 0
	}
	start := max(0, min(e.active, len(e.buffers)-1))
	end := start + 1
	used := min(width, tabWidth(e.buffers[start]))
	for start > 0 && used+tabWidth(e.buffers[start-1]) <= width {
		start--
		used += tabWidth(e.buffers[start])
	}
	for end < len(e.buffers) && used+tabWidth(e.buffers[end]) <= width {
		used += tabWidth(e.buffers[end])
		end++
	}
	return start, end
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
