package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type editor struct {
	languages                 *languageIntelligence
	workspaces                map[string]cachedWorkspace
	switching                 *workspaceSwitch
	lastFrame                 string
	checkpoint                *workspaceCheckpoint
	projectSlots              []string
	projectSlot               int
	files                     []string
	fileRefreshAt             time.Time
	fileRefreshDone           chan fileTreeSnapshot
	fileGeneration            int
	tree                      []treeEntry
	visibleTreeCache          []treeEntry
	visibleTreeWidth          int
	visibleTreeIndent         int
	visibleTreeIcons          [2]string
	collapsed                 map[string]bool
	tests                     []testCase
	selected, explorerTop     int
	testSelected, testTop     int
	sourceSelected, sourceTop int
	buffers                   []*buffer
	active                    int
	explorer, showExplorer    bool
	testMode                  bool
	sourceMode                bool
	sourceChanges             []sourceChange
	sourceMessage             string
	sourceDone                chan sourceResult
	sourceLoading             bool
	sourceGeneration          int
	sourceLineChanges         map[string]map[int]rune
	sourceDiffDone            chan sourceDiffResult
	sourceDiffGeneration      int
	sourceCommitFocused       bool
	sourceDiscardArmed        string
	sourceRefresh             bool
	commitInput               []rune
	status                    string
	quitArmed                 bool
	closeArmed                *buffer
	graph                     bool
	dependencyCanvas          *nodeCanvas
	architectureView          *nodeCanvas
	shell                     shellPanel
	help                      bool
	wordWrap                  bool
	opsMode                   string
	opsLines                  []string
	opsTop                    int
	popup                     *popupMenu
	searchMode                string
	searchInput               []rune
	searchPool                []searchResult
	searchResults             []searchResult
	searchSelected            int
	folderPrompt              bool
	newProjectPrompt          bool
	folderInput               []rune
	folderPredictions         []string
	folderPredictionSelected  int
	newFilePrompt             bool
	newFileInput              []rune
	newFileDirectory          string
	selection                 textSelection
	clipboard                 string
	completionCache           map[string]dependencyCompletion
	completionPending         map[string]bool
	completionDone            chan completionResult
	completionGeneration      int
	completionDirty           bool
	completionBuffer          *buffer
	completionRow             int
	completionCol             int
	completionSelected        int
	completionDismissed       bool
	definitionLoading         bool
	definitionDone            chan definitionResult
	workspaceDone             chan editorLoad
	restoredTheme             string
	quitRequested             bool
	rows, cols                int
}

type treeEntry struct {
	path string
	dir  bool
}

func newEditor() *editor {
	e := &editor{status: shortcutLabel("help") + " Shortcuts  " + shortcutLabel("explorer") + " Explorer", shell: shellPanel{open: true, interactive: true}}
	var err error
	e.projectSlots, e.projectSlot, err = loadProjectSlots(mustCwd())
	if err != nil {
		e.status = "Projects: " + err.Error()
	}
	e.refreshFiles()
	if len(e.files) > 0 {
		e.open(e.files[0])
	}
	if len(e.buffers) == 0 {
		e.buffers = []*buffer{newBuffer(untitledName(), nil)}
	}
	return e
}

func untitledName() string {
	for i := 1; ; i++ {
		name := "untitled.txt"
		if i > 1 {
			name = fmt.Sprintf("untitled-%d.txt", i)
		}
		if _, err := os.Stat(name); os.IsNotExist(err) {
			return name
		}
	}
}

func (e *editor) refreshFiles() {
	snapshot := readFileTree(mustCwd(), nil, nil)
	if snapshot.err != nil {
		e.status = "Files: " + snapshot.err.Error()
		return
	}
	e.applyFileTree(snapshot)
}

func (e *editor) indexFiles(files []string, discovered ...[]testCase) {
	e.fileGeneration++
	e.visibleTreeCache = nil
	e.files = append(e.files[:0], files...)
	sort.Strings(e.files)
	e.tree = e.tree[:0]
	e.tests = e.tests[:0]
	directories := map[string]bool{}
	for index, path := range e.files {
		path = strings.TrimPrefix(filepath.ToSlash(path), "./")
		e.files[index] = path
		for parent := filepath.ToSlash(filepath.Dir(path)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			directories[parent] = true
		}
		if len(discovered) == 0 && isTestFile(path) {
			e.tests = append(e.tests, discoverTests(path)...)
		}
	}
	if len(discovered) > 0 {
		e.tests = append(e.tests, discovered[0]...)
	}
	for path := range directories {
		e.tree = append(e.tree, treeEntry{path: path, dir: true})
	}
	if e.collapsed == nil {
		e.collapsed = map[string]bool{}
	}
	for path := range directories {
		if _, known := e.collapsed[path]; !known {
			e.collapsed[path] = true
		}
	}
	for _, path := range e.files {
		e.tree = append(e.tree, treeEntry{path: path})
	}
	sort.Slice(e.tree, func(i, j int) bool { return e.tree[i].path < e.tree[j].path })
	for _, canvas := range []*nodeCanvas{e.dependencyCanvas, e.architectureView} {
		if canvas != nil {
			canvas.stale = true
		}
	}
	e.invalidateCompletion()
	if e.sourceMode {
		e.loadSourceControl()
	}
}

func (e *editor) open(path string) {
	for i, b := range e.buffers {
		if b.path == path {
			e.clearModalViews()
			e.active, e.explorer = i, false
			e.loadMembersInBackground(path)
			if e.sourceMode {
				e.loadSourceFileChanges(path)
			}
			return
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		e.status = err.Error()
		return
	}
	if info.Size() > 2<<20 {
		e.status = "File is larger than 2 MiB"
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		e.status = err.Error()
		return
	}
	if strings.IndexByte(string(data), 0) >= 0 {
		e.status = "Binary files are not editable"
		return
	}
	e.buffers = append(e.buffers, newBuffer(path, data))
	e.clearModalViews()
	e.active, e.explorer = len(e.buffers)-1, false
	e.loadMembersInBackground(path)
	if e.sourceMode {
		e.loadSourceFileChanges(path)
	}
}

func (e *editor) current() *buffer { return e.buffers[e.active] }

func (e *editor) handle(k key) bool {
	if e.switching != nil {
		return k.r == e.switching.quitKey
	}
	if k.alt && k.r >= '1' && k.r < '1'+rune(settings.projectQuickPicks) {
		e.openProjectSlot(int(k.r - '1'))
		return false
	}
	if k.mouse {
		e.handleMouse(k)
		return e.quitRequested
	}
	if e.terminalFocused() && !k.mouse && shortcutAction(k.r) != "terminal" {
		if err := e.shell.terminal.handle(k); err != nil {
			e.status = err.Error()
		}
		return false
	}
	if e.shell.open && e.shell.focused && !e.shell.interactive && e.popup == nil && shortcutAction(k.r) != "terminal" && shortcutAction(k.r) != "run-tests" {
		e.shell.handle(k)
		return false
	}
	if k.raw != "" {
		return false
	} // Unsupported editor keys still pass through in Terminal.
	if e.popup != nil {
		e.handlePopupKey(k)
		return false
	}
	if e.folderPrompt {
		e.handleFolderPrompt(k)
		return false
	}
	if e.newFilePrompt {
		e.handleNewFilePrompt(k)
		return false
	}
	if e.searchMode != "" {
		e.handleSearch(k)
		return false
	}
	if k.r == 0 && k.code == 0 && e.sourceMode && e.sourceCommitFocused {
		e.sourceCommitFocused = false
		return false
	}
	if k.r == 0 && k.code == 0 && e.closeModalView() {
		return false
	}
	action := shortcutAction(k.r)
	if action != "quit" {
		e.quitArmed = false
	}
	if action != "close" {
		e.closeArmed = nil
	}
	if handled, quit := e.handleCommand(action); handled {
		return quit
	}

	if e.sourceMode && e.sourceCommitFocused {
		e.handleSourceCommit(k)
	} else if e.shell.focused {
		e.shell.handle(k)
	} else if e.help {
		// Esc closes this read-only modal.
	} else if e.graph {
		e.handleGraph(k)
	} else if e.opsMode != "" {
		e.handleOperations(k)
	} else if e.explorer {
		e.handleExplorer(k)
	} else {
		b := e.current()
		if e.handleCompletionKey(k) {
			return false
		}
		_, _, memberBefore := memberAccessAtCursor(b)
		editing := k.r >= 32 || k.code == keyEnter || k.code == keyBackspace || k.code == keyDelete || k.code == keyTab
		if e.selection.buffer == b && !e.selection.empty() {
			if editing {
				b.recordUndo()
				b.suppressUndo = true
				defer func() { b.suppressUndo = false }()
				e.deleteSelection()
				e.selection = textSelection{}
				if k.code == keyBackspace || k.code == keyDelete {
					e.invalidateCompletion()
					return false
				}
			} else if k.code != 0 {
				e.selection = textSelection{}
			}
		}
		if k.code == keyTab {
			suggestion := e.suggestion()
			if len(suggestion) > 0 {
				b.insert(suggestion)
				e.status = "Suggestion accepted"
				return false
			}
			if _, _, memberAccess := memberAccessAtCursor(b); memberAccess {
				e.status = "No members found for this object"
				return false
			}
		}
		b.handle(k)
		_, _, memberAfter := memberAccessAtCursor(b)
		if editing && !memberBefore && !memberAfter {
			e.markCompletionDirty()
		}
		if memberAfter && !memberBefore {
			e.status = "Loading members…"
		}
	}
	return false
}

func (e *editor) closeModalView() bool {
	if !e.modalViewOpen() {
		return false
	}
	e.clearModalViews()
	e.status = "Closed view"
	return true
}

func (e *editor) modalViewOpen() bool {
	return e.graph || e.help || e.opsMode != ""
}

func (e *editor) clearModalViews() {
	e.shell.focused = false
	e.graph, e.help, e.opsMode = false, false, ""
	e.sourceCommitFocused = false
}

func (e *editor) handleCommand(action string) (handled, quit bool) {
	switch action {
	case "run-tests":
		e.runTests()
		return true, false
	case "undo":
		if e.current().undoChange() {
			e.invalidateCompletion()
			e.selection = textSelection{}
			e.status = "Undid last change"
		} else {
			e.status = "Nothing to undo"
		}
		return true, false
	case "copy":
		if e.editorFocused() {
			e.copySelection()
			return true, false
		}
	case "explorer":
		e.showExplorer = !e.showExplorer
		if !e.showExplorer {
			e.explorer = false
		}
		return true, false
	case "graph":
		e.shell.focused, e.help = false, false
		e.opsMode = ""
		e.toggleGraph()
		return true, false
	case "search":
		e.openSearch("files")
		return true, false
	case "test-explorer":
		e.clearModalViews()
		e.showExplorer, e.explorer, e.testMode, e.sourceMode = true, true, true, false
		e.status = "Tests: click or Enter to open"
		return true, false
	case "format":
		e.formatCurrent()
		return true, false
	case "new":
		e.openNewFilePrompt()
		return true, false
	case "help":
		open := !e.help
		e.clearModalViews()
		e.help, e.explorer = open, false
		return true, false
	case "files-explorer":
		e.clearModalViews()
		e.showExplorer, e.explorer, e.graph, e.help, e.sourceMode = true, true, false, false, false
		return true, false
	case "quit":
		if !e.dirty() || e.quitArmed {
			return true, true
		}
		e.quitArmed = true
		e.status = "Unsaved changes — press Ctrl+Q again to quit"
		return true, false
	case "save":
		e.save()
		return true, false
	case "terminal":
		open := !e.shell.open
		e.shell.open, e.shell.focused = open, open
		if open {
			e.openTerminal()
		}
		return true, false
	case "close":
		if !e.closeModalView() {
			e.close()
		}
		return true, false
	case "paste":
		if e.editorFocused() {
			e.pasteClipboard()
			return true, false
		}
	case "function-search":
		e.openSearch("functions")
		return true, false
	case "word-wrap":
		e.wordWrap = !e.wordWrap
		e.current().scrollX, e.current().wrapSegment = 0, 0
		e.status = map[bool]string{true: "Word Wrap: On", false: "Word Wrap: Off"}[e.wordWrap]
		return true, false
	case "go-to-definition":
		e.goToDefinition()
		return true, false
	case "symbol-info":
		if !e.requestLanguage("hover") {
			e.status = "Symbol info requires an installed language server"
		}
		return true, false
	case "architecture":
		e.openArchitecture()
		return true, false
	case "source-control":
		e.openSourceControl()
		return true, false
	case "source-stage-all", "source-commit", "source-pull", "source-push", "source-fetch":
		e.performAction(action)
		return true, false
	}
	return false, false
}

func (e *editor) reloadCleanBuffers() {
	for i, b := range e.buffers {
		if b.dirty {
			continue
		}
		data, err := os.ReadFile(b.path)
		if err != nil {
			continue
		}
		fresh := newBuffer(b.path, data)
		fresh.row = min(b.row, len(fresh.lines)-1)
		fresh.col = min(b.col, len(fresh.lines[fresh.row]))
		e.buffers[i] = fresh
	}
}

func (e *editor) toggleGraph() {
	open := !e.graph
	e.clearModalViews()
	if !open {
		return
	}
	e.startNodeCanvas(false, false)
	e.graph, e.explorer = true, false
}

func (e *editor) handleMouse(k key) {
	if e.handleTerminalMouse(k) {
		return
	}
	if c := e.activeNodeCanvas(); c != nil && e.popup == nil && !e.folderPrompt && !e.newFilePrompt && e.searchMode == "" {
		if c.dragging || k.x >= c.x && k.x < c.x+c.width && k.y >= c.y && k.y < c.y+c.height {
			e.handleCanvasMouse(c, k)
			return
		}
	}
	if e.newFilePrompt {
		return
	}
	if k.release {
		if e.selection.selecting {
			e.updateSelection(k.x, k.y)
			e.selection.selecting = false
		}
		return
	}
	if k.button&32 != 0 {
		if e.selection.selecting {
			e.updateSelection(k.x, k.y)
		}
		return
	}
	if e.searchMode != "" {
		e.handleSearchMouse(k)
		return
	}
	if e.folderPrompt {
		if k.button == 0 {
			index := k.y - 5
			if index >= 0 && index < len(e.folderPredictions) {
				e.folderInput = []rune(e.folderPredictions[index])
				e.updateFolderPredictions()
			}
		}
		return
	}
	if e.popup == nil && e.modalViewOpen() && e.activeNodeCanvas() == nil && k.x > e.sidebarWidth() && k.y > 2 && k.button != 64 && k.button != 65 {
		return
	}
	if k.button == 2 && k.y == 1 {
		if index, ok := e.projectSlotAt(k.x); ok {
			e.setProjectSlot(index)
			return
		}
	}
	if k.button == 2 {
		e.openContextMenu(k)
		return
	}
	if e.handleTopBarMouse(k) {
		return
	}
	if k.button == 64 || k.button == 65 {
		e.closeArmed = nil
		e.handleWheel(k)
		return
	}
	if k.button != 0 {
		return
	}
	side := e.sidebarWidth()
	editorX := side
	if k.y != 2 || k.x <= editorX || e.graph || e.help || e.opsMode != "" {
		e.closeArmed = nil
	}
	if k.y == 2 {
		if side > 0 && k.x <= side {
			if view, ok := sidebarModeAt(k.x); ok {
				e.clearModalViews()
				e.testMode, e.sourceMode = view == "tests", view == "source"
				e.explorer = true
				e.status = map[string]string{"files": "Files Explorer", "tests": "Test Explorer", "source": "Source Control"}[view]
				if e.sourceMode {
					e.loadSourceControl()
				}
			}
		} else {
			e.selectWorkspaceTab(k.x - editorX - 1)
		}
		return
	}
	if k.y < 3 || k.y >= e.rows {
		return
	}
	if side > 0 && k.x <= side {
		if e.sourceMode {
			switch row := k.y - 3; {
			case row < sourceTopPadding:
			case row >= sourceMessageRow && row < sourceCommitButtonRow:
				e.sourceCommitFocused = true
			case row == sourceCommitButtonRow:
				e.commitSourceMessage()
			default:
				index := e.sourceTop + row - sourceHeaderRows
				if index >= 0 && index < len(e.sourceChanges) {
					e.openSourceChange(index)
				}
			}
		} else if e.testMode {
			index := e.testTop + k.y - 3
			if index >= 0 && index < len(e.tests) {
				e.testSelected = index
				if k.x > side-testButtonWidth {
					e.runTests(e.tests[index])
					return
				}
				e.openTest(e.tests[index])
			}
		} else {
			index := e.explorerTop + k.y - 3
			entries := e.visibleTree()
			if index >= 0 && index < len(entries) {
				e.selected = index
				if entries[index].dir {
					e.toggleFolder(entries[index].path)
				} else {
					e.open(entries[index].path)
				}
			}
		}
		e.shell.focused, e.help = false, false
		return
	}
	contentHeight, _ := e.panelHeights()
	if e.shell.open && k.y >= 3+contentHeight {
		e.shell.focused = e.shell.open
		return
	}
	e.shell.focused = false
	if e.graph || e.help || e.opsMode != "" {
		return
	}

	if row, col, ok := e.editorPosition(k.x, k.y); ok {
		b := e.current()
		b.row, b.col = row, col
		e.selection = textSelection{buffer: b, startRow: row, startCol: col, endRow: row, endCol: col, selecting: true}
	}
	e.explorer, e.shell.focused = false, false
}

func (e *editor) editorFocused() bool {
	return !e.explorer && !e.shell.focused && !e.modalViewOpen()
}

func (e *editor) handleWheel(k key) {
	delta := 3
	if k.button == 64 {
		delta = -3
	}
	contentHeight, shellHeight := e.panelHeights()
	if shellHeight > 0 && k.x > e.sidebarWidth() && k.y >= 3+contentHeight {
		modalLines := max(1, shellHeight-2)
		if delta < 0 {
			e.shell.scroll = min(max(0, len(e.shell.output)-modalLines), e.shell.scroll+3)
		} else {
			e.shell.scroll = max(0, e.shell.scroll-3)
		}
		return
	}
	if c := e.activeNodeCanvas(); c != nil && k.x > e.sidebarWidth() {
		c.panY = max(0, c.panY+delta)
		return
	}
	if e.opsMode != "" {
		e.opsTop = max(0, min(len(e.opsLines)-1, e.opsTop+delta))
		return
	}
	if side := e.sidebarWidth(); side > 0 && k.x <= side {
		if e.sourceMode {
			e.sourceSelected = max(0, min(len(e.sourceChanges)-1, e.sourceSelected+delta))
		} else if e.testMode {
			e.testSelected = max(0, min(len(e.tests)-1, e.testSelected+delta))
		} else {
			e.selected = max(0, min(len(e.visibleTree())-1, e.selected+delta))
		}
		return
	}
	b := e.current()
	b.row = max(0, min(len(b.lines)-1, b.row+delta))
	b.clampCol()
}

func (e *editor) selectTab(column int) {
	i, offset := e.tabAt(column)
	if i < 0 {
		e.closeArmed = nil
		return
	}
	e.active = i
	e.clearModalViews()
	e.loadMembersInBackground(e.current().path)
	if offset >= tabWidth(e.buffers[i])-(settings.tabPadding+1) {
		e.close()
		return
	}
	e.closeArmed = nil
	e.explorer = false
}

func (e *editor) tabAt(column int) (int, int) {
	width := e.fileTabsWidth()
	if column < 0 || column >= width {
		return -1, 0
	}
	start, end := e.visibleTabRange(width)
	for i := start; i < end; i++ {
		b := e.buffers[i]
		if width := tabWidth(b); column >= 0 && column < width {
			return i, column
		} else {
			column -= width
		}
	}
	return -1, 0
}

func (e *editor) loadGraph() {
	e.startNodeCanvas(false, true)
	e.status = "Graph: click/Enter folder to expand · Enter file to open · arrows/drag pan · +/- zoom"
}

func (e *editor) handleGraph(k key) {
	if c := e.activeNodeCanvas(); c != nil {
		e.handleCanvasKey(c, k)
	}
}

func (e *editor) handleExplorer(k key) {
	if e.sourceMode {
		switch k.code {
		case keyUp:
			e.sourceSelected = max(0, e.sourceSelected-1)
		case keyDown:
			e.sourceSelected = max(0, min(len(e.sourceChanges)-1, e.sourceSelected+1))
		case keyEnter:
			if len(e.sourceChanges) > 0 {
				e.openSourceChange(e.sourceSelected)
			}
		case keyTab:
			e.sourceCommitFocused = true
		default:
			if k.r >= 32 {
				e.sourceCommitFocused = true
				e.commitInput = append(e.commitInput, k.r)
			}
		}
		return
	}
	if e.testMode {
		switch k.code {
		case keyUp:
			e.testSelected = max(0, e.testSelected-1)
		case keyDown:
			e.testSelected = max(0, min(len(e.tests)-1, e.testSelected+1))
		case keyEnter:
			if len(e.tests) > 0 {
				e.openTest(e.tests[e.testSelected])
			}
		case keyTab:
			e.explorer = false
		}
		return
	}
	entries := e.visibleTree()
	if len(entries) == 0 {
		return
	}
	e.selected = min(e.selected, len(entries)-1)
	switch k.code {
	case keyUp:
		if e.selected > 0 {
			e.selected--
		}
	case keyDown:
		if e.selected+1 < len(entries) {
			e.selected++
		}
	case keyEnter:
		if entries[e.selected].dir {
			e.toggleFolder(entries[e.selected].path)
		} else {
			e.open(entries[e.selected].path)
		}
	case keyLeft:
		e.collapseFolder(entries[e.selected])
	case keyRight:
		if entries[e.selected].dir {
			e.setFolderCollapsed(entries[e.selected].path, false)
		}
	case keyTab:
		e.explorer = false
	}
}

func (e *editor) openTest(test testCase) {
	e.open(test.path)
	if test.row > 0 {
		b := e.current()
		b.row, b.col = min(test.row-1, len(b.lines)-1), 0
	}
}

func (e *editor) visibleTree() []treeEntry {
	icons := [2]string{settings.icons["folder_open"], settings.icons["folder_closed"]}
	if e.visibleTreeCache != nil && e.visibleTreeIndent == settings.explorerIndent && e.visibleTreeIcons == icons {
		return e.visibleTreeCache
	}
	visible := make([]treeEntry, 0, len(e.tree))
	e.visibleTreeWidth = 0
	for _, entry := range e.tree {
		hidden := false
		for parent := filepath.ToSlash(filepath.Dir(entry.path)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			if e.collapsed[parent] {
				hidden = true
				break
			}
		}
		if !hidden {
			visible = append(visible, entry)
			e.visibleTreeWidth = max(e.visibleTreeWidth, len([]rune(e.explorerTreeLabel(entry)))+3)
		}
	}
	e.visibleTreeCache, e.visibleTreeIndent, e.visibleTreeIcons = visible, settings.explorerIndent, icons
	return visible
}

func (e *editor) toggleFolder(path string) { e.setFolderCollapsed(path, !e.collapsed[path]) }

func (e *editor) setFolderCollapsed(path string, collapsed bool) {
	if e.collapsed == nil {
		e.collapsed = map[string]bool{}
	}
	e.collapsed[path] = collapsed
	e.visibleTreeCache = nil
	e.status = map[bool]string{true: "Collapsed ", false: "Expanded "}[collapsed] + path
}

func (e *editor) collapseFolder(entry treeEntry) {
	if entry.dir && !e.collapsed[entry.path] {
		e.setFolderCollapsed(entry.path, true)
		return
	}
	parent := filepath.ToSlash(filepath.Dir(entry.path))
	for index, candidate := range e.visibleTree() {
		if candidate.path == parent {
			e.selected = index
			return
		}
	}
}

func isTestFile(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	normalized := "/" + strings.ToLower(filepath.ToSlash(path))
	return strings.HasSuffix(name, "_test.go") || strings.HasPrefix(name, "test_") && strings.HasSuffix(name, ".py") ||
		strings.Contains(name, ".test.") || strings.Contains(name, ".spec.") || strings.HasSuffix(name, "test.java") ||
		strings.Contains(normalized, "/tests/") && strings.HasSuffix(name, ".rs") || strings.HasSuffix(name, "tests.cs")
}

func (e *editor) openFolder(path string) {
	e.switchWorkspace(path, false)
}

func (e *editor) openProjectSlot(index int) {
	if index < 0 || index >= settings.projectQuickPicks || index >= len(e.projectSlots) || e.projectSlots[index] == "" {
		e.status = fmt.Sprintf("Project slot %d is empty", index+1)
		return
	}
	if index == e.projectSlot {
		e.status = fmt.Sprintf("Project %d: %s", index+1, e.projectSlots[index])
		return
	}
	e.openFolder(e.projectSlots[index])
}

func (e *editor) setProjectSlot(index int) {
	if index < 0 || index >= settings.projectQuickPicks {
		return
	}
	current := filepath.Clean(mustCwd())
	for len(e.projectSlots) <= index {
		e.projectSlots = append(e.projectSlots, "")
	}
	for other, project := range e.projectSlots {
		if other != index && project == current {
			e.projectSlots[other] = ""
		}
	}
	e.projectSlots[index], e.projectSlot = current, index
	if err := saveProjectSlots(e.projectSlots); err != nil {
		e.status = "Projects: " + err.Error()
		return
	}
	e.status = fmt.Sprintf("Project %d set to %s", index+1, current)
}

func (e *editor) openNewProjectPrompt() {
	e.folderPrompt, e.newProjectPrompt, e.searchMode = true, true, ""
	e.folderInput = []rune(filepath.Dir(mustCwd()) + string(filepath.Separator))
	e.updateFolderPredictions()
}

func (e *editor) handleFolderPrompt(k key) {
	switch k.code {
	case keyEnter:
		path := string(e.folderInput)
		if e.newProjectPrompt {
			if strings.TrimSpace(path) == "" {
				e.status = "Enter a new project folder path"
				return
			}
			e.switchWorkspace(path, true)
			if e.switching != nil {
				e.folderPrompt, e.newProjectPrompt = false, false
			}
			return
		}
		if e.folderPredictionSelected >= 0 && e.folderPredictionSelected < len(e.folderPredictions) {
			path = e.folderPredictions[e.folderPredictionSelected]
		}
		e.folderPrompt = false
		e.openFolder(path)
	case keyBackspace:
		if len(e.folderInput) > 0 {
			e.folderInput = e.folderInput[:len(e.folderInput)-1]
		}
		e.updateFolderPredictions()
	case keyUp:
		if len(e.folderPredictions) > 0 {
			e.folderPredictionSelected = max(0, e.folderPredictionSelected-1)
		}
	case keyDown:
		if len(e.folderPredictions) > 0 {
			e.folderPredictionSelected = min(len(e.folderPredictions)-1, e.folderPredictionSelected+1)
		}
	case keyTab:
		if len(e.folderPredictions) > 0 {
			index := max(0, e.folderPredictionSelected)
			e.folderInput = []rune(e.folderPredictions[index])
			e.updateFolderPredictions()
		}
	default:
		if k.r == 0 {
			e.folderPrompt, e.folderPredictions = false, nil
			e.newProjectPrompt = false
		} else if k.r >= 32 {
			e.folderInput = append(e.folderInput, k.r)
			e.updateFolderPredictions()
		}
	}
}

func (e *editor) updateFolderPredictions() {
	e.folderPredictions = predictFolders(string(e.folderInput))
	e.folderPredictionSelected = -1
}

func predictFolders(input string) []string {
	if input == "~" {
		return []string{"~/"}
	}
	expanded := expandFolderPath(input)
	dir, prefix := filepath.Dir(expanded), filepath.Base(expanded)
	displayDir := filepath.Dir(input)
	if input == "" {
		dir, prefix, displayDir = ".", "", ""
	} else if strings.HasSuffix(input, string(filepath.Separator)) {
		dir, prefix, displayDir = expanded, "", input
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	predictions := make([]string, 0, 6)
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || !strings.HasPrefix(name, prefix) || strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		prediction := filepath.Join(displayDir, name)
		if displayDir == "" {
			prediction = name
		}
		predictions = append(predictions, prediction+string(filepath.Separator))
		if len(predictions) == 6 {
			break
		}
	}
	return predictions
}

func expandFolderPath(path string) string {
	if path == "" {
		return "."
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func (e *editor) openNewFilePrompt() {
	directory := filepath.Dir(e.current().path)
	if e.explorer {
		entries := e.visibleTree()
		if e.selected < len(entries) {
			directory = entries[e.selected].path
			if !entries[e.selected].dir {
				directory = filepath.Dir(directory)
			}
		}
	}
	e.newFilePrompt, e.newFileInput, e.newFileDirectory = true, nil, directory
}

func (e *editor) handleNewFilePrompt(k key) {
	switch k.code {
	case keyEnter:
		name := strings.TrimSpace(string(e.newFileInput))
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\\`) {
			e.status = "Enter a filename without a folder path"
			return
		}
		path := filepath.Join(e.newFileDirectory, name)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			e.status = "New file failed: " + err.Error()
			return
		}
		if err = file.Close(); err != nil {
			e.status = err.Error()
			return
		}
		e.newFilePrompt, e.newFileInput = false, nil
		e.refreshFiles()
		e.open(path)
		e.status = "Created " + path
	case keyBackspace:
		if len(e.newFileInput) > 0 {
			e.newFileInput = e.newFileInput[:len(e.newFileInput)-1]
		}
	default:
		if k.r == 0 {
			e.newFilePrompt = false
		} else if k.r >= 32 {
			e.newFileInput = append(e.newFileInput, k.r)
		}
	}
}

func (e *editor) dirty() bool {
	for _, b := range e.buffers {
		if b.dirty {
			data, err := os.ReadFile(b.path)
			if err == nil && string(data) == strings.Join(runeLines(b.lines), b.newline) {
				b.dirty = false
				continue
			}
			return true
		}
	}
	return false
}

func (e *editor) close() {
	b := e.current()
	if b.dirty && e.closeArmed != b {
		e.closeArmed = b
		e.status = "Unsaved changes — press Ctrl+W or click × again to close"
		return
	}
	e.closeArmed = nil
	if len(e.buffers) == 1 {
		e.buffers[0] = newBuffer(untitledName(), nil)
		e.active = 0
		return
	}
	e.buffers = append(e.buffers[:e.active], e.buffers[e.active+1:]...)
	if e.active == len(e.buffers) {
		e.active--
	}
}

func (e *editor) save() {
	b := e.current()
	data := []byte(strings.Join(runeLines(b.lines), b.newline))
	dir := filepath.Dir(b.path)
	tmp, err := os.CreateTemp(dir, ".code-editor-*")
	if err == nil {
		mode := fs.FileMode(0644)
		if info, statErr := os.Stat(b.path); statErr == nil {
			mode = info.Mode()
		}
		err = tmp.Chmod(mode)
		if err == nil {
			_, err = tmp.Write(data)
		}
		if closeErr := tmp.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(tmp.Name(), b.path)
		}
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}
	if err != nil {
		e.status = "Save failed: " + err.Error()
		return
	}
	b.dirty = false
	e.status = "Saved " + b.path
	e.refreshFiles()
	e.loadMembersInBackground(b.path)
}
