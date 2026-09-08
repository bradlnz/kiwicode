package main

import (
	"strconv"
	"strings"
)

func projectSlotBarWidth() int { return settings.projectQuickPicks * 3 }

type menuItem struct{ label, action string }

type popupMenu struct {
	x, y, width int
	top         string
	items       []menuItem
	selected    int
}

type topMenu struct {
	label string
	items []menuItem
}

var topMenus = []topMenu{
	{"File", []menuItem{{"Open File", "open-file"}, {"Open Folder", "open-folder"}, {"New File", "new"}, {"Save", "save"}, {"Close Tab", "close"}, {"Quit", "quit"}}},
	{"Edit", []menuItem{{"Undo", "undo"}, {"File Search", "search"}, {"Function Search", "function-search"}, {"Go to Definition", "go-to-definition"}, {"Format", "format"}}},
	{"View", []menuItem{{"Files Explorer", "files-explorer"}, {"Test Explorer", "test-explorer"}, {"Source Control", "source-control"}, {"Word Wrap", "word-wrap"}, {"Dependency Graph", "graph"}, {"Architecture Canvas", "architecture"}, {"Terminal", "terminal"}, {"Shortcuts", "help"}, {"Theme: Plum", "theme:plum"}, {"Theme: Forest", "theme:forest"}, {"Theme: Amber", "theme:amber"}, {"Theme: Mono", "theme:mono"}}},
	{"Source", []menuItem{{"Source Control", "source-control"}, {"Stage All", "source-stage-all"}, {"Commit…", "source-commit"}, {"Discard Selected…", "source-discard"}, {"Pull", "source-pull"}, {"Push", "source-push"}, {"Fetch", "source-fetch"}, {"Refresh", "refresh-source"}}},
	{"Help", []menuItem{{"Keyboard Shortcuts", "help"}, {"About / Open Source Licenses", "licenses"}}},
}

var quickActions = []menuItem{{" [Run Tests] ", "run-tests"}, {" Search ", "search"}, {" Terminal ", "terminal"}}

func (p *popupMenu) itemAt(x, y int) (menuItem, bool) {
	i := y - p.y
	if x < p.x || x >= p.x+p.width || i < 0 || i >= len(p.items) {
		return menuItem{}, false
	}
	return p.items[i], true
}

func (e *editor) openPopup(x, y int, top string, items []menuItem) {
	width := 0
	for _, item := range items {
		width = max(width, len([]rune(displayMenuItem(item)))+settings.popupPadding*2)
	}
	x = min(max(1, x), max(1, e.cols-width+1))
	y = min(max(2, y), max(2, e.rows-len(items)))
	e.popup = &popupMenu{x: x, y: y, width: width, top: top, items: items}
}

func topMenuAt(column int) (topMenu, int, bool) {
	x := 1
	for _, menu := range topMenus {
		width := topMenuWidth(menu)
		if column >= x && column < x+width {
			return menu, x, true
		}
		x += width
	}
	return topMenu{}, 0, false
}

func (e *editor) openTopMenu(column int) {
	if menu, x, ok := topMenuAt(column); ok {
		e.openPopup(x, 2, menu.label, menu.items)
	}
}

func (e *editor) handlePopupKey(k key) {
	if e.popup == nil {
		return
	}
	switch k.code {
	case keyUp:
		e.popup.selected = max(0, e.popup.selected-1)
	case keyDown:
		e.popup.selected = min(len(e.popup.items)-1, e.popup.selected+1)
	case keyEnter:
		action := e.popup.items[e.popup.selected].action
		e.popup = nil
		e.performAction(action)
	case keyLeft:
		e.cycleTopMenu(-1)
	case keyRight:
		e.cycleTopMenu(1)
	default:
		e.popup = nil
	}
}

func (e *editor) handleTopBarMouse(k key) bool {
	if k.button == 2 && k.y == 1 {
		if index, ok := e.projectSlotAt(k.x); ok {
			e.setProjectSlot(index)
			return true
		}
	}
	if k.button == 0 && k.y == 1 {
		if index, ok := e.projectSlotAt(k.x); ok {
			e.popup = nil
			e.openProjectSlot(index)
			return true
		}
	}
	if e.popup != nil {
		if k.button == 0 {
			if k.y == 1 {
				e.popup = nil
				if action := topActionAt(k.x); action != "" {
					e.performAction(action)
				} else {
					e.openTopMenu(k.x)
				}
			} else if item, ok := e.popup.itemAt(k.x, k.y); ok {
				e.popup = nil
				e.performAction(item.action)
			} else {
				e.popup = nil
			}
		}
		return true
	}
	if k.button != 0 || k.y != 1 {
		return false
	}
	if action := topActionAt(k.x); action != "" {
		e.performAction(action)
	} else {
		e.openTopMenu(k.x)
	}
	return true
}

func (e *editor) projectSlotAt(column int) (int, bool) {
	start := max(1, e.cols-projectSlotBarWidth()+1)
	index := (column - start) / 3
	return index, column >= start && column <= e.cols && index >= 0 && index < settings.projectQuickPicks
}

func (e *editor) cycleTopMenu(delta int) {
	if e.popup == nil || e.popup.top == "" {
		return
	}
	index := 0
	for i, menu := range topMenus {
		if menu.label == e.popup.top {
			index = i
			break
		}
	}
	index = (index + delta + len(topMenus)) % len(topMenus)
	x := 1
	for i := 0; i < index; i++ {
		x += topMenuWidth(topMenus[i])
	}
	e.openPopup(x, 2, topMenus[index].label, topMenus[index].items)
}

func (e *editor) topBar() string {
	var out strings.Builder
	for _, menu := range topMenus {
		active := e.popup != nil && e.popup.top == menu.label
		background := colors.top
		if active {
			background = colors.topActive
		}
		out.WriteString(ansiBG(background, colors.text, style(active, "1", "22")))
		padding := strings.Repeat(" ", settings.topMenuPadding)
		out.WriteString(padding + menu.label + padding)
	}
	for _, item := range quickActions {
		if item.action == "run-tests" {
			label := item.label
			if e.shell.testRun && e.shell.running {
				label = " [Stop Tests]"
			}
			out.WriteString(ansiBG(colors.menu, colors.accent, "1") + label)
			continue
		}
		out.WriteString(ansiBG(colors.top, colors.accent, "1") + item.label)
	}
	if e.cols <= projectSlotBarWidth() {
		return fitANSI(e.projectSlotBar(), e.cols)
	}
	return fitANSI(out.String(), e.cols-projectSlotBarWidth()) + e.projectSlotBar()
}

func (e *editor) projectSlotBar() string {
	var out strings.Builder
	for index := 0; index < settings.projectQuickPicks; index++ {
		foreground, attributes := colors.muted, "2"
		if index < len(e.projectSlots) && e.projectSlots[index] != "" {
			foreground, attributes = colors.accent, "22"
		}
		if index == e.projectSlot {
			out.WriteString(ansiBG(colors.topActive, colors.text, "1"))
		} else {
			out.WriteString(ansiBG(colors.top, foreground, attributes))
		}
		out.WriteString(" " + strconv.Itoa(index+1) + " ")
	}
	return out.String()
}

func topActionAt(column int) string {
	x := 1
	for _, menu := range topMenus {
		x += topMenuWidth(menu)
	}
	for _, item := range quickActions {
		width := len([]rune(item.label))
		if column >= x && column < x+width {
			return item.action
		}
		x += width
	}
	return ""
}

func (e *editor) openContextMenu(k key) {
	side := e.sidebarWidth()
	if k.y == 1 {
		e.openTopMenu(k.x)
		return
	}
	if k.y == 2 && k.x > side && !e.graph && !e.help && e.opsMode == "" {
		if i, _ := e.tabAt(k.x - side - 1); i >= 0 {
			e.active = i
			e.openPopup(k.x, k.y, "", []menuItem{{"Save", "save"}, {"Format", "format"}, {"Close Tab", "close"}})
		}
		return
	}
	if side > 0 && k.x <= side && k.y >= 3 {
		if e.sourceMode {
			items := []menuItem{{"Stage All", "source-stage-all"}, {"Commit…", "source-commit"}, {"Pull", "source-pull"}, {"Push", "source-push"}, {"Refresh", "refresh-source"}}
			if i := e.sourceTop + k.y - 3 - sourceHeaderRows; i >= 0 && i < len(e.sourceChanges) {
				e.sourceSelected = i
				change := e.sourceChanges[i]
				fileItems := []menuItem{{"Open Change", "open-change"}, {"View Diff", "source-diff"}}
				if change.staged() {
					fileItems = append(fileItems, menuItem{"View Staged Diff", "source-diff-staged"}, menuItem{"Unstage File", "source-unstage"})
				}
				if change.unstaged() {
					fileItems = append(fileItems, menuItem{"Stage File", "source-stage"})
				}
				discardLabel := "Discard Changes…"
				if e.sourceDiscardArmed == change.path {
					discardLabel = "Confirm Discard"
				}
				fileItems = append(fileItems, menuItem{discardLabel, "source-discard"})
				items = append(fileItems, items...)
			}
			e.openPopup(k.x, k.y, "", items)
		} else if e.testMode {
			if i := e.testTop + k.y - 3; i >= 0 && i < len(e.tests) {
				e.testSelected = i
				e.openPopup(k.x, k.y, "", []menuItem{{"Open Test", "open-test"}, {"Run / Stop Selected Test", "run-selected-test"}, {"Run All Tests", "run-tests"}})
			}
		} else if entries, i := e.visibleTree(), e.explorerTop+k.y-3; i >= 0 && i < len(entries) {
			e.selected = i
			items := []menuItem{{"File Search", "search"}, {"Function Search", "function-search"}, {"New File", "new"}}
			if !entries[i].dir {
				items = append([]menuItem{{"Open", "open-selected"}}, items...)
			} else if e.collapsed[entries[i].path] {
				items = append([]menuItem{{"Expand Folder", "toggle-folder"}}, items...)
			} else {
				items = append([]menuItem{{"Collapse Folder", "toggle-folder"}}, items...)
			}
			e.openPopup(k.x, k.y, "", items)
		}
		return
	}
	contentHeight, _ := e.panelHeights()
	if k.y >= 3+contentHeight && e.shell.open {
		e.openPopup(k.x, k.y, "", []menuItem{{"Clear", "clear-panel"}, {"Close Panel", "close-panel"}})
		return
	}
	if e.graph {
		e.openPopup(k.x, k.y, "", []menuItem{{"Refresh Graph", "refresh-graph"}})
		return
	}
	if e.opsMode != "" {
		e.openPopup(k.x, k.y, "", []menuItem{{"Refresh Architecture", "refresh-architecture"}})
		return
	}
	e.openPopup(k.x, k.y, "", []menuItem{{"Copy", "copy"}, {"Paste", "paste"}, {"Go to Definition", "go-to-definition"}, {"Save", "save"}, {"Format", "format"}})
}

func (e *editor) performAction(action string) {
	switch action {
	case "run-selected-test":
		if e.testSelected >= 0 && e.testSelected < len(e.tests) {
			e.runTests(e.tests[e.testSelected])
		}
	case "licenses":
		e.openLicenses()
	case "open-file":
		e.openSearch("files")
	case "open-folder":
		e.folderPrompt, e.searchMode = true, ""
		e.folderInput = nil
		e.updateFolderPredictions()
	case "files-explorer":
		e.clearModalViews()
		e.showExplorer, e.explorer, e.testMode, e.sourceMode = true, true, false, false
	case "test-explorer":
		e.clearModalViews()
		e.showExplorer, e.explorer, e.testMode, e.sourceMode = true, true, true, false
	case "source-control":
		e.openSourceControl()
	case "architecture":
		e.openArchitecture()
	case "refresh-architecture":
		e.startNodeCanvas(true, true)
	case "theme:plum", "theme:forest", "theme:amber", "theme:mono":
		name := strings.TrimPrefix(action, "theme:")
		setColorScheme(name)
		e.status = "Theme: " + colors.name
	case "open-selected":
		entries := e.visibleTree()
		if e.selected < len(entries) && !entries[e.selected].dir {
			e.open(entries[e.selected].path)
		}
	case "toggle-folder":
		entries := e.visibleTree()
		if e.selected < len(entries) && entries[e.selected].dir {
			e.toggleFolder(entries[e.selected].path)
		}
	case "open-test":
		if len(e.tests) > 0 {
			e.openTest(e.tests[e.testSelected])
		}
	case "open-change":
		if len(e.sourceChanges) > 0 {
			e.openSourceChange(e.sourceSelected)
		}
	case "refresh-source":
		e.loadSourceControl()
	case "source-stage":
		if change, ok := e.selectedSourceChange(); ok {
			e.runSourceCommand("git add -- " + shellArg(change.path))
		}
	case "source-unstage":
		if change, ok := e.selectedSourceChange(); ok {
			e.runSourceCommand("git restore --staged -- " + shellArg(change.path))
		}
	case "source-discard":
		if !e.sourceMode {
			e.openSourceControl()
			e.status = "Select a change to discard"
			break
		}
		e.discardSourceChange()
	case "source-stage-all":
		e.runSourceCommand("git add -A")
	case "source-diff":
		if change, ok := e.selectedSourceChange(); ok {
			e.runSourceCommand("git --no-pager diff -- " + shellArg(change.path))
		}
	case "source-diff-staged":
		if change, ok := e.selectedSourceChange(); ok {
			e.runSourceCommand("git --no-pager diff --cached -- " + shellArg(change.path))
		}
	case "source-commit":
		if !e.sourceMode {
			e.openSourceControl()
		}
		e.showExplorer, e.explorer, e.sourceCommitFocused = true, true, true
	case "source-pull":
		e.runSourceCommand("git pull")
	case "source-push":
		e.runSourceCommand("git push")
	case "source-fetch":
		e.runSourceCommand("git fetch")
	case "function-search":
		e.openSearch("functions")
	case "refresh-graph":
		e.loadGraph()
	case "copy":
		e.copySelection()
	case "paste":
		e.pasteClipboard()
	case "clear-panel":
		e.shell.output = nil
	case "close-panel":
		e.shell.open, e.shell.focused = false, false
	default:
		if handled, quit := e.handleCommand(action); handled && quit {
			e.quitRequested = true
		}
	}
}

func displayMenuItem(item menuItem) string {
	if label := shortcutLabel(item.action); label != "" {
		return item.label + "  " + label
	}
	return item.label
}

func (e *editor) drawPopup(out *strings.Builder) {
	if e.popup == nil {
		return
	}
	for i, item := range e.popup.items {
		itemStyle := ansiBG(colors.menu, colors.text, "22")
		if i == e.popup.selected {
			itemStyle = ansiBG(colors.menuActive, colors.text, "1")
		}
		padding := strings.Repeat(" ", settings.popupPadding)
		writeCell(out, e.popup.y+i, e.popup.x, itemStyle+fit(padding+displayMenuItem(item)+padding, e.popup.width))
	}
}

func topMenuWidth(menu topMenu) int { return len([]rune(menu.label)) + settings.topMenuPadding*2 }

type searchResult struct {
	label, path string
	row         int
}

func (e *editor) openSearch(mode string) {
	e.searchMode, e.popup = mode, nil
	e.searchInput, e.searchSelected = nil, 0
	e.searchPool = e.searchPool[:0]
	if mode == "functions" {
		for _, symbol := range projectSymbols(e.files) {
			e.searchPool = append(e.searchPool, searchResult{
				label: symbol.name + "  " + symbol.path + ":" + strconv.Itoa(symbol.row),
				path:  symbol.path,
				row:   symbol.row,
			})
		}
	} else {
		for _, path := range e.files {
			e.searchPool = append(e.searchPool, searchResult{label: path, path: path})
		}
	}
	e.updateSearch()
}

func (e *editor) updateSearch() {
	query := strings.ToLower(string(e.searchInput))
	e.searchResults = e.searchResults[:0]
	for _, result := range e.searchPool {
		if query == "" || strings.Contains(strings.ToLower(result.label), query) {
			e.searchResults = append(e.searchResults, result)
			if len(e.searchResults) == 8 {
				break
			}
		}
	}
	e.searchSelected = min(e.searchSelected, max(0, len(e.searchResults)-1))
}

func (e *editor) handleSearch(k key) {
	switch k.code {
	case keyEnter:
		if len(e.searchResults) > 0 {
			e.openSearchResult(e.searchResults[e.searchSelected])
		}
		e.searchMode = ""
	case keyBackspace:
		if len(e.searchInput) > 0 {
			e.searchInput = e.searchInput[:len(e.searchInput)-1]
		}
	case keyUp:
		e.searchSelected = max(0, e.searchSelected-1)
	case keyDown:
		e.searchSelected = max(0, min(len(e.searchResults)-1, e.searchSelected+1))
	default:
		if k.r == 0 {
			e.searchMode = ""
		} else if k.r >= 32 {
			e.searchInput = append(e.searchInput, k.r)
		}
	}
	e.updateSearch()
}

func (e *editor) handleSearchMouse(k key) {
	if k.button != 0 || k.release {
		return
	}
	width := min(60, e.cols-4)
	x := max(2, (e.cols-width)/2)
	i := k.y - 3
	if k.x >= x && k.x < x+width && i >= 0 && i < len(e.searchResults) {
		e.searchSelected = i
		e.openSearchResult(e.searchResults[i])
	}
	e.searchMode = ""
}

func (e *editor) openSearchResult(result searchResult) {
	e.open(result.path)
	if result.row > 0 {
		b := e.current()
		b.row, b.col = min(result.row-1, len(b.lines)-1), 0
	}
}
