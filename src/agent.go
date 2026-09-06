package main

import (
	"code-editor/internal/agent"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Agent is a workspace tab, not a buffer or modal. Its service outlives tab
// switches; only the terminal loop owns editor/buffer mutations.
type workspaceTabKind uint8

const (
	workspaceFileTab workspaceTabKind = iota
	workspaceAgentTab
)

type workspaceTab struct {
	kind                       workspaceTabKind
	buffer, left, right, close int
}
type agentWorkspace struct {
	editor                              *editor
	service                             *agent.Service
	root                                string
	kind                                workspaceTabKind
	view                                agent.View
	input                               []rune
	inputCol                            int
	inputLoaded, inputEdited, quitArmed bool
	section, top                        int
	lines                               []string
	tabs                                []workspaceTab
	status                              string
}

var agentSections = []string{"Plan", "Activity", "Changes", "Checks", "Graph", "Quality", "Context"}

func init() {
	// Register before loadSettings parses customised bindings. resetSettings also
	// reads these maps, so the shortcuts participate in normal configuration.
	defaultShortcuts["agent"] = 1
	defaultShortcuts["agent-cancel"] = 24
	shortcutNames["agent"] = "Agent Workspace"
	shortcutNames["agent-cancel"] = "Cancel Agent Run"
}
func newAgentWorkspace(e *editor) *agentWorkspace {
	root := mustCwd()
	w := &agentWorkspace{editor: e, root: root, status: "Agent: Ctrl+A | Ctrl+X cancels a run"}
	w.service = agent.Start(root, "", nil)
	return w
}
func (w *agentWorkspace) close() {
	if w.inputLoaded || w.inputEdited {
		w.service.SetDraft(string(w.input))
	}
	if err := w.service.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "agent:", err)
	}
}
func (w *agentWorkspace) syncRoot() {
	root := mustCwd()
	if root == w.root {
		return
	}
	w.close()
	e := w.editor
	*w = agentWorkspace{editor: e, root: root, status: "Workspace changed; previous agent stopped without replaying actions."}
	w.service = agent.Start(root, "", nil)
}
func (w *agentWorkspace) poll() bool {
	select {
	case view := <-w.service.Events():
		w.view = view
		if !w.inputLoaded {
			if !w.inputEdited {
				w.input = []rune(view.Session.Draft)
				w.inputCol = len(w.input)
			}
			w.inputLoaded = true
		}
		w.rebuildLines()
		return true
	default:
		return false
	}
}
func (w *agentWorkspace) rebuildLines() {
	s := w.view.Session
	switch w.section {
	case 0:
		w.lines = []string{"Task: " + s.Task, "", "Plan"}
		w.lines = append(w.lines, s.Plan...)
	case 1:
		w.lines = append([]string(nil), s.Activity...)
	case 2:
		w.lines = []string{"Proposals are NOT disk edits. Review, /apply ID, switch to the file and Ctrl+S.", ""}
		for _, p := range s.Proposals {
			w.lines = append(w.lines, p.ID+"  "+p.Path+"  ["+p.Status+"]", "--- saved original", "+++ proposed replacement")
			for _, line := range strings.Split(p.Original, "\n") {
				w.lines = append(w.lines, "- "+line)
			}
			for _, line := range strings.Split(p.Replacement, "\n") {
				w.lines = append(w.lines, "+ "+line)
			}
			w.lines = append(w.lines, "")
		}
	case 3:
		w.lines = []string{"Checks execute local workspace code with your account, NOT in a sandbox."}
		for _, check := range s.Checks {
			w.lines = append(w.lines, fmt.Sprintf("%s | passed=%v | %d ms", check.Command, check.Passed, check.Milliseconds), check.Output)
		}
	case 4:
		w.lines = []string{fmt.Sprintf("Code graph: %d files, %d symbols, %d findings", w.view.GraphFiles, w.view.GraphSymbols, w.view.GraphFindings), "Snapshot: " + w.view.GraphHash, fmt.Sprintf("Last refresh: %d read, %d changed, %d reused, %d removed", w.view.Stats.Read, w.view.Stats.Parsed, w.view.Stats.Reused, w.view.Stats.Removed), "", "Go: imports, declarations and syntactic calls. Other supported files: metadata only.", "Calls are not type-resolved; build tags are not resolved.", "/index refreshes hashes and only reparses changed files. Nothing is indexed per keypress."}
		w.lines = append(w.lines, w.view.GraphLines...)
	case 5:
		w.lines = []string{fmt.Sprintf("Reward score: %d | New credit: %d | High water: %d", s.Reward.Score, s.Reward.NewCredit, s.HighWater), s.Reward.Reason, "", fmt.Sprintf("Checks: tests=%v vet=%v reviewed=%v", s.Evidence.TestsPassed, s.Evidence.VetPassed, s.Evidence.Reviewed), "Checked snapshot: " + s.Evidence.Snapshot, "", "Policy v1: 5 points per reduced complexity-excess unit against the initial baseline.", "Tests/vet and review must match the exact source snapshot. Existing test changes,", "deleted functions and parse errors withhold credit. TODO removal earns nothing.", "These are maintainability signals, not proof of correctness or a trained reward model."}
		w.lines = append(w.lines, fmt.Sprintf("%d low-complexity quality facts recorded; code volume itself earns no points.", w.view.GraphQuality))
		for _, delta := range s.Reward.Components {
			w.lines = append(w.lines, fmt.Sprintf("%+d  %s", delta.Points, delta.Label))
		}
		w.lines = append(w.lines, fmt.Sprintf("%d recent verified reward records persisted (maximum 64).", len(s.Rewards)))
	default:
		w.lines = []string{"Persisted task context (local, outside the repository)", "", "Included files (explicit /run permits provider access):"}
		w.lines = append(w.lines, s.Included...)
		w.lines = append(w.lines, "", "Notes:")
		w.lines = append(w.lines, s.Notes...)
	}
	// Expand multiline events once per event/view change, never per input rune.
	var rows []string
	for _, line := range w.lines {
		rows = append(rows, strings.Split(line, "\n")...)
		if len(rows) > 4096 {
			rows = rows[:4096]
			break
		}
	}
	w.lines = rows
	w.top = min(w.top, max(0, len(w.lines)-1))
}

func (w *agentWorkspace) paintTabs(out *strings.Builder) {
	e := w.editor
	left, width := 1, e.cols
	if w.kind == workspaceFileTab {
		left = e.sidebarWidth() + 1
		width = e.codeAreaWidth()
	}
	if width < 1 {
		return
	}
	agentWidth := min(16, width)
	end := left + width - agentWidth
	w.tabs = w.tabs[:0]
	writeCell(out, 2, left, strings.Repeat(" ", width))
	start, stop := e.visibleTabRange()
	x := left
	for i := start; i < stop && x < end; i++ {
		b := e.buffers[i]
		padding := strings.Repeat(" ", settings.tabPadding)
		label := padding + tabLabel(b) + padding + "×" + padding
		full := tabWidth(b)
		visible := min(full, end-x)
		c := 0
		if full <= end-x {
			c = x + full - settings.tabPadding - 1
		}
		w.tabs = append(w.tabs, workspaceTab{workspaceFileTab, i, x, x + visible - 1, c})
		prefix := "\x1b[22;24m" + ansiFG(colors.muted)
		if w.kind == workspaceFileTab && i == e.active {
			prefix = "\x1b[1;4m" + ansiFG(colors.text)
		}
		writeCell(out, 2, x, prefix+fit(label, visible))
		x += visible
	}
	label := " Agent · ^A "
	if w.service.Busy() {
		label = " Agent * · ^A "
	} else {
		for _, p := range w.view.Session.Proposals {
			if p.Status == "awaiting review" {
				label = " Agent ! · ^A "
				break
			}
		}
	}
	prefix := ansiFG(colors.accent)
	if w.kind == workspaceAgentTab {
		prefix += "\x1b[1;4m"
	}
	writeCell(out, 2, end, prefix+fit(label, agentWidth))
	w.tabs = append(w.tabs, workspaceTab{kind: workspaceAgentTab, left: end, right: left + width - 1})
}
func (w *agentWorkspace) draw() {
	e := w.editor
	if e.rows < 5 || e.cols < 30 {
		e.draw()
		return
	}
	var out strings.Builder
	if w.kind == workspaceFileTab {
		e.draw()
		out.WriteString("\x1b7") // Save/restore the existing file cursor.
		w.paintTabs(&out)
		out.WriteString("\x1b8")
		fmt.Print(out.String())
		return
	}
	out.WriteString("\x1b[?25l\x1b[H")
	for row := 1; row <= e.rows; row++ {
		writeCell(&out, row, 1, strings.Repeat(" ", e.cols))
	}
	writeCell(&out, 1, 1, fit("KiwiCode · Agent | "+w.view.Session.Status+" | Tab views · ^A code · ^X cancel", e.cols))
	w.paintTabs(&out)
	var labels []string
	for i, name := range agentSections {
		if i == w.section {
			name = "[" + name + "]"
		}
		labels = append(labels, name)
	}
	writeCell(&out, 3, 1, fit(strings.Join(labels, "  "), e.cols))
	height := max(0, e.rows-6)
	for i := 0; i < height && w.top+i < len(w.lines); i++ {
		writeCell(&out, 4+i, 1, fit(w.lines[w.top+i], e.cols))
	}
	writeCell(&out, e.rows-2, 1, fit(w.status, e.cols))
	writeCell(&out, e.rows-1, 1, fit("/task /remember /include /run /index /apply ID /check /reward /forget", e.cols))
	available := max(1, e.cols-3)
	start := max(0, w.inputCol-available+1)
	stop := min(len(w.input), start+available)
	writeCell(&out, e.rows, 1, fit("> "+string(w.input[start:stop]), e.cols))
	fmt.Fprintf(&out, "\x1b[%d;%dH\x1b[?25h", e.rows, 3+w.inputCol-start)
	fmt.Print(out.String())
}
func (w *agentWorkspace) handle(k key) bool {
	action := shortcutAction(k.r)
	if action != "quit" {
		w.quitArmed = false
		w.editor.quitArmed = false
	}
	if action == "agent-cancel" {
		w.service.Cancel()
		w.status = "Cancellation requested"
		return false
	}
	if action == "agent" {
		if w.kind == workspaceAgentTab {
			w.kind = workspaceFileTab
		} else {
			w.kind = workspaceAgentTab
			w.editor.clearModalViews()
		}
		return false
	}
	if k.mouse && k.y == 2 && !k.release && k.button == 0 {
		for _, tab := range w.tabs {
			if k.x < tab.left || k.x > tab.right {
				continue
			}
			w.kind = tab.kind
			if tab.kind == workspaceFileTab {
				w.editor.active = tab.buffer
				w.editor.explorer = false
				w.editor.clearModalViews()
				if tab.close != 0 && k.x == tab.close {
					w.editor.close()
				}
			}
			return false
		}
		return false
	}
	if action == "quit" {
		if w.service.Busy() && !w.quitArmed {
			w.quitArmed = true
			w.status = "Agent is working. Press quit again to cancel it and exit."
			w.editor.status = w.status
			return false
		}
		handled, quit := w.editor.handleCommand("quit")
		return handled && quit
	}
	if w.kind == workspaceFileTab {
		return w.editor.handle(k)
	}
	if k.mouse {
		if k.button == 64 {
			w.top = max(0, w.top-3)
		} else if k.button == 65 {
			w.top = min(max(0, len(w.lines)-1), w.top+3)
		}
		return false
	}
	if action == "paste" {
		w.insertInput(readSystemClipboard())
		return false
	}
	if k.r == 3 {
		w.service.Cancel()
		w.status = "Cancellation requested"
		return false
	}
	if action != "" {
		w.status = "File commands do not operate on the Agent tab. Ctrl+A returns to code."
		return false
	}
	switch k.code {
	case keyTab:
		w.section = (w.section + 1) % len(agentSections)
		w.top = 0
		w.rebuildLines()
	case keyUp:
		w.top = max(0, w.top-1)
	case keyDown:
		w.top = min(max(0, len(w.lines)-1), w.top+1)
	case keyPageUp:
		w.top = max(0, w.top-max(1, w.editor.rows-6))
	case keyPageDown:
		w.top = min(max(0, len(w.lines)-1), w.top+max(1, w.editor.rows-6))
	case keyLeft:
		w.inputCol = max(0, w.inputCol-1)
	case keyRight:
		w.inputCol = min(len(w.input), w.inputCol+1)
	case keyHome:
		w.inputCol = 0
	case keyEnd:
		w.inputCol = len(w.input)
	case keyBackspace:
		if w.inputCol > 0 {
			w.input = append(w.input[:w.inputCol-1], w.input[w.inputCol:]...)
			w.inputCol--
			w.draftChanged()
		}
	case keyDelete:
		if w.inputCol < len(w.input) {
			w.input = append(w.input[:w.inputCol], w.input[w.inputCol+1:]...)
			w.draftChanged()
		}
	case keyEnter:
		w.submit()
	default:
		if k.r >= 32 && k.r != 127 {
			w.insertInput(string(k.r))
		}
	}
	return false
}
func (w *agentWorkspace) draftChanged() { w.inputEdited = true; w.service.SetDraft(string(w.input)) }
func (w *agentWorkspace) insertInput(text string) {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r", " "), "\n", " ")
	if len(string(w.input))+len(text) > 8192 {
		w.status = "Agent prompt limit is 8192 bytes"
		return
	}
	chars := []rune(text)
	tail := append([]rune(nil), w.input[w.inputCol:]...)
	w.input = append(w.input[:w.inputCol], chars...)
	w.input = append(w.input, tail...)
	w.inputCol += len(chars)
	w.draftChanged()
}
func (w *agentWorkspace) submit() {
	text := strings.TrimSpace(string(w.input))
	if text == "" {
		return
	}
	if w.editor.workspaceDone != nil {
		w.status = "Finish loading the workspace before using the agent"
		return
	}
	command, arg := "task", text
	if strings.HasPrefix(text, "/") {
		command, arg, _ = strings.Cut(strings.TrimPrefix(text, "/"), " ")
		arg = strings.TrimSpace(arg)
	}
	if command == "apply" {
		if err := w.apply(arg); err != nil {
			w.status = err.Error()
			return
		}
	} else {
		if (command == "check" || command == "reward") && w.editor.dirty() {
			w.status = "Save all dirty buffers before checking or rewarding disk contents"
			return
		}
		if !w.service.Submit(agent.Request{Kind: command, Text: arg}) {
			w.status = "Agent is busy or unavailable; the prompt has been retained"
			return
		}
	}
	w.input = nil
	w.inputCol = 0
	w.draftChanged()
	w.status = "Submitted " + command
}
func (w *agentWorkspace) apply(id string) error {
	if w.service.Busy() {
		return fmt.Errorf("finish or cancel the current operation before applying")
	}
	var proposal *agent.Proposal
	for i := range w.view.Session.Proposals {
		p := &w.view.Session.Proposals[i]
		if p.ID == id {
			proposal = p
			break
		}
	}
	if proposal == nil || proposal.Status != "awaiting review" {
		return fmt.Errorf("no pending proposal with that ID")
	}
	// The provider never receives the ability to approve or mutate buffers.
	data, err := agent.ReadSource(w.root, proposal.Path)
	if err != nil {
		return err
	}
	if agent.Hash(data) != proposal.OriginalHash {
		return fmt.Errorf("disk content changed; proposal is stale")
	}
	for _, b := range w.editor.buffers {
		absolute, _ := filepath.Abs(b.path)
		if absolute == filepath.Join(w.root, proposal.Path) && b.dirty {
			return fmt.Errorf("unsaved user changes are protected; save or undo before applying")
		}
	}
	previous := w.editor.active
	w.editor.open(proposal.Path)
	b := w.editor.current()
	absolute, _ := filepath.Abs(b.path)
	if absolute != filepath.Join(w.root, proposal.Path) {
		w.editor.active = previous
		return fmt.Errorf("could not open proposal target")
	}
	current := []byte(strings.Join(runeLines(b.lines), b.newline))
	if b.dirty || agent.Hash(current) != proposal.OriginalHash {
		w.editor.active = previous
		return fmt.Errorf("buffer differs from proposal base; no changes applied")
	}
	fresh := newBuffer(b.path, []byte(proposal.Replacement))
	b.recordUndo()
	b.lines = fresh.lines
	b.row = min(b.row, len(b.lines)-1)
	b.col = min(b.col, len(b.lines[b.row]))
	b.dirty = true
	w.editor.selection = textSelection{}
	w.editor.invalidateCompletion()
	w.kind = workspaceFileTab
	w.editor.status = "Agent proposal applied to buffer; review and Ctrl+S to save. Ctrl+Z undoes it."
	w.service.Submit(agent.Request{Kind: "applied", Text: id})
	return nil
}
