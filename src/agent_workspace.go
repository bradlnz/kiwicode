package main

import (
	"code-editor/internal/agent"
	"code-editor/internal/contextgraph"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// File selection remains a buffer index. Agent is a different workspace kind,
// never a synthetic file, and never owns a worker's lifetime.
type workspaceKind uint8

const (
	workspaceFile workspaceKind = iota
	workspaceAgent
	agentTabWidth = 12
)

type agentWorkspace struct {
	memory                  *contextgraph.Service
	memoryView              contextgraph.View
	memoryReady, checkArmed bool
	viewTabStart            int
	root                    string
	session                 agent.Session
	input                   []rune
	cursor                  int
	run                     *agent.Run
	provider                *agent.HTTPProvider
	approval                *agent.Approval
	store                   *agent.Store
	unread                  bool
	dirty                   bool
	lastSaved               time.Time
	forget                  bool
	newArmed                bool
	// Presentation caches rebuild on changes or resize, never on every frame.
	width         int
	activityRows  agent.Log
	follow        bool
	pageRows      int
	viewLines     [agent.ViewCount][]string
	viewDirty     [agent.ViewCount]bool
	approvalLines []string
	approvalTop   int
}

func init() {
	defaultShortcuts["agent"], defaultShortcuts["agent-cancel"] = 1, 24
	shortcutNames["agent"], shortcutNames["agent-cancel"] = "Agent Workspace", "Cancel Agent Run"
	if settings.bindings != nil {
		settings.bindings[1], settings.bindings[24] = "agent", "agent-cancel"
	}
}

func newAgentWorkspace(root string) *agentWorkspace {
	return &agentWorkspace{root: root, session: agent.NewSession(), follow: true, viewDirty: [agent.ViewCount]bool{true, true, true, true, true, true, true}}
}

// Only the workspace loader calls this. No history read or JSON decoding runs
// in a key handler. Persistence is opt-in because transcripts contain code.
func (e *editor) restoreAgent() {
	if os.Getenv("KIWICODE_AGENT_HISTORY") != "1" {
		return
	}
	a := newAgentWorkspace(mustCwd())
	path, err := agent.SessionPath(a.root)
	if err == nil {
		a.session, err = agent.LoadSession(path)
	}
	if err != nil {
		a.session = agent.NewSession()
		a.session.Log.Add("History not restored: " + err.Error())
	}
	a.session.Draft = agent.Display(a.session.Draft)
	a.follow = a.session.Scroll[1] == 0
	a.input = []rune(a.session.Draft)
	a.cursor = len(a.input)
	e.agent = a
}

func (e *editor) ensureAgent() *agentWorkspace {
	if e.agent == nil {
		e.agent = newAgentWorkspace(mustCwd())
	}
	a := e.agent
	if a.memory == nil && e.workspaceDone == nil {
		a.memory = contextgraph.Start(a.root, "")
	}
	if a.store == nil && os.Getenv("KIWICODE_AGENT_HISTORY") == "1" && e.workspaceDone == nil {
		if path, err := agent.SessionPath(a.root); err == nil {
			a.store = agent.NewStore(path)
		} else {
			e.status = err.Error()
		}
	}
	return a
}

func (e *editor) activateAgent() {
	e.ensureAgent().unread = false
	e.workspace = workspaceAgent
	e.clearModalViews()
	e.popup = nil
	e.searchMode = ""
	e.folderPrompt, e.newFilePrompt = false, false
	e.explorer = false
	e.selection.selecting = false
	e.closeArmed = nil
	e.quitArmed = false
}

func (e *editor) agentRunning() bool {
	return e.agent != nil && (e.agent.run != nil || e.agent.memoryBusy())
}

func (e *editor) cancelAgent() {
	if e.agent != nil {
		if e.agent.run != nil {
			e.agent.run.Cancel()
		}
		if e.agent.memory != nil {
			e.agent.memory.Cancel()
		}
		e.agent.checkArmed = false
	}
	e.status = "Cancellation requested"
}

// agentCommand runs before file command dispatch. It cannot save, format,
// close, undo, or otherwise mutate a hidden file buffer.
func (e *editor) agentCommand(action string) (handled, quit bool) {
	switch action {
	case "agent":
		if e.workspace == workspaceAgent {
			e.workspace = workspaceFile
		} else {
			e.activateAgent()
		}
		return true, false
	case "agent-cancel":
		e.cancelAgent()
		return true, false
	case "quit":
		if e.agentRunning() && !e.quitArmed {
			e.quitArmed = true
			e.status = "Agent is running; unsaved work may exist. Press Quit again to cancel and exit"
			return true, false
		}
		if e.agentRunning() && e.quitArmed {
			e.cancelAgent()
			return true, true
		}
		// The existing quit guard still protects unsaved file buffers.
		return false, false
	}
	if e.workspace != workspaceAgent {
		return false, false
	}
	switch action {
	case "close":
		e.workspace = workspaceFile
		e.status = "Returned to files; agent session retained"
	case "search", "function-search":
		e.workspace = workspaceFile
		mode := "files"
		if action == "function-search" {
			mode = "functions"
		}
		e.openSearch(mode)
	case "help":
		e.status = "Agent: Enter send · Tab views · /approve /deny /apply N /open N /reject N /new /forget · Ctrl+X cancel"
	case "":
		return false, false
	default:
		e.status = "File commands are unavailable in Agent; select a file tab first"
	}
	return true, false
}

func (e *editor) handleAgent(k key) bool {
	if k.mouse {
		e.handleAgentMouse(k)
		return false
	}
	a := e.ensureAgent()
	if k.code != keyEnter {
		a.checkArmed = false
	}
	action := shortcutAction(k.r)
	if action != "quit" {
		e.quitArmed = false
	}
	if k.r == 3 && k.code == 0 {
		e.cancelAgent()
		return false
	}
	if handled, quit := e.handleCommand(action); handled {
		return quit
	}
	switch k.code {
	case keyTab:
		a.session.View = (a.session.View + 1) % agent.ViewCount
		a.dirty = true
	case keyUp, keyDown, keyPageUp, keyPageDown:
		delta := 1
		if k.code == keyPageUp || k.code == keyPageDown {
			delta = max(1, e.rows-8)
		}
		if k.code == keyUp || k.code == keyPageUp {
			delta = -delta
		}
		a.scroll(delta)
	case keyLeft:
		a.cursor = max(0, a.cursor-1)
	case keyRight:
		a.cursor = min(len(a.input), a.cursor+1)
	case keyHome:
		a.cursor = 0
	case keyEnd:
		a.cursor = len(a.input)
	case keyBackspace:
		if a.cursor > 0 {
			a.input = append(a.input[:a.cursor-1], a.input[a.cursor:]...)
			a.cursor--
			a.edited()
		}
	case keyDelete:
		if a.cursor < len(a.input) {
			a.input = append(a.input[:a.cursor], a.input[a.cursor+1:]...)
			a.edited()
		}
	case keyEnter:
		e.submitAgent()
	default:
		if k.r >= 32 && k.r != 127 {
			a.insert(string(k.r))
		}
	}
	return false
}

func (a *agentWorkspace) edited() {
	a.checkArmed = false
	a.session.Draft = string(a.input)
	a.dirty = true
	a.forget = false
	a.newArmed = false
}
func (a *agentWorkspace) insert(text string) {
	text = strings.ReplaceAll(agent.Display(text), "\r", "")
	if len(a.session.Draft)+len(text) > 16<<10 {
		return
	}
	runes := []rune(text)
	a.input = append(a.input, make([]rune, len(runes))...)
	copy(a.input[a.cursor+len(runes):], a.input[a.cursor:])
	copy(a.input[a.cursor:], runes)
	a.cursor += len(runes)
	a.edited()
}
func (a *agentWorkspace) scroll(delta int) {
	if a.approval != nil && a.session.View == 1 {
		a.approvalTop = max(0, min(max(0, len(a.approvalLines)-max(1, a.pageRows)), a.approvalTop+delta))
		return
	}
	view := a.session.View
	a.session.Scroll[view] = max(0, min(max(0, a.viewCount(view)-max(1, a.pageRows)), a.session.Scroll[view]+delta))
	if view == 1 {
		a.follow = delta > 0 && a.session.Scroll[view] >= max(0, a.viewCount(view)-max(1, a.pageRows))
	}
	a.dirty = true
}

func (e *editor) submitAgent() {
	a := e.ensureAgent()
	text := strings.TrimSpace(string(a.input))
	if text == "" {
		return
	}
	if strings.HasPrefix(text, "/") {
		if e.agentSlash(text) {
			a.input, a.cursor, a.session.Draft = nil, 0, ""
			a.dirty = !a.forget
		}
		return
	}
	if e.agentRunning() {
		e.status = "A run is active; /cancel to stop it"
		return
	}
	if e.workspaceDone != nil {
		e.status = "Wait for workspace loading to finish before starting an agent"
		return
	}
	for _, change := range a.session.Changes {
		if !change.Applied && (!change.Existed || change.Before != change.After) {
			e.status = "Review/apply the current changes, or /new twice to discard them"
			return
		}
	}
	p, err := agent.NewHTTPProvider(agent.Config{
		Endpoint: os.Getenv("KIWICODE_AGENT_ENDPOINT"), Model: os.Getenv("KIWICODE_AGENT_MODEL"), APIKey: os.Getenv("KIWICODE_AGENT_API_KEY"),
	})
	if err != nil {
		e.status = "Agent unavailable: configure KIWICODE_AGENT_ENDPOINT and KIWICODE_AGENT_MODEL; " + err.Error()
		return
	}
	if a.memory != nil && (!a.memoryReady || a.memoryBusy()) {
		p.Close()
		e.status = "Context is loading or working; task draft retained"
		return
	}
	if a.memoryView.Error != "" {
		p.Close()
		e.status = "Resolve the context-store error before starting a run"
		return
	}
	// No file text is included at launch. Each read requires explicit approval;
	// initial dirty paths are blocked, and apply rechecks all intervening edits.
	var blocked []string
	for _, b := range e.buffers {
		if b.dirty {
			if path, err := workspaceRelative(a.root, b.path); err == nil {
				blocked = append(blocked, path)
			}
		}
	}
	a.provider = p
	history := a.session.History
	if a.memoryView.ProviderContext != "" {
		history = append([]agent.Message{{Role: "user", Content: "Untrusted saved notes and included graph metadata; not read/command approval:\n" + a.memoryView.ProviderContext}}, history[max(0, len(history)-15):]...)
	}
	a.run = agent.Start(context.Background(), agent.Request{
		Root: a.root, Prompt: text, Files: e.files, BlockedPaths: blocked,
		History: history, AllowCommands: os.Getenv("KIWICODE_AGENT_ALLOW_COMMANDS") == "1",
	}, p)
	a.session.Plan, a.session.Changes, a.session.Checks = nil, nil, nil
	a.viewLines = [agent.ViewCount][]string{}
	a.session.Remember("user", text)
	a.session.Log.Add("Task: " + text)
	a.session.State = agent.Running
	a.session.View = 1
	a.session.Scroll = [agent.ViewCount]int{}
	a.follow = true
	a.input, a.cursor, a.session.Draft = nil, 0, ""
	a.viewDirty = [agent.ViewCount]bool{true, true, true, true, true, true, true}
	a.dirty, a.forget, a.newArmed = true, false, false
	e.status = "Agent running; reads require approval. Ctrl+X cancels from any tab"
}

func workspaceRelative(root, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	return agent.CleanPath(filepath.ToSlash(rel))
}

func (e *editor) applyAgentChange(index int) error {
	a := e.ensureAgent()
	if a.run != nil || a.memoryBusy() {
		return errors.New("wait for the run/context operation to stop before applying changes")
	}
	if index < 0 || index >= len(a.session.Changes) {
		return errors.New("invalid change")
	}
	change := &a.session.Changes[index]
	if change.Applied {
		return errors.New("this change was already applied")
	}
	if !change.Reviewable() {
		return errors.New("diff exceeds review limits; ask the agent for a smaller edit")
	}
	var target *buffer
	for _, b := range e.buffers {
		if path, err := workspaceRelative(a.root, b.path); err == nil && path == change.Path {
			if b.dirty || strings.Join(runeLines(b.lines), b.newline) != change.Before {
				return errors.New("conflict: the editor buffer differs; user edits were preserved")
			}
			target = b
			break
		}
	}
	data, err := agent.ReadWorkspace(a.root, change.Path)
	if change.Existed {
		if err != nil || string(data) != change.Before {
			return errors.New("conflict: the file changed on disk; nothing applied")
		}
	} else if !os.IsNotExist(err) {
		return errors.New("conflict: new-file target already exists or cannot be checked")
	}
	path := filepath.Join(a.root, filepath.FromSlash(change.Path))
	fresh := newBuffer(path, []byte(change.After))
	if target == nil {
		target = newBuffer(path, []byte(change.Before))
		if !change.Existed {
			target.newline = fresh.newline
		}
	}
	// The existing undo record does not store newline mode. Refuse an EOL-mode
	// change rather than apply content whose bytes cannot be undone faithfully.
	if strings.Join(runeLines(fresh.lines), target.newline) != change.After {
		return errors.New("newline-mode changes require manual review; nothing applied")
	}
	target.recordUndo()
	target.lines = fresh.lines
	target.row = min(target.row, len(target.lines)-1)
	target.col = min(target.col, len(target.lines[target.row]))
	target.dirty = true
	found := false
	for _, b := range e.buffers {
		if b == target {
			found = true
			break
		}
	}
	if !found {
		e.buffers = append(e.buffers, target)
	}
	change.Applied = true
	a.viewDirty[2], a.dirty = true, true
	e.invalidateCompletion()
	e.status = "Applied to buffer, not disk: " + change.Path + " · /open " + strconv.Itoa(index+1) + " then Save"
	return nil
}

func (e *editor) agentEvents() <-chan agent.Event {
	if e.agent == nil || e.agent.run == nil {
		return nil
	}
	return e.agent.run.Events
}

// Called only by the UI loop. It is safe to read files or switch tabs while the
// worker runs, because this is the sole mutation point for visible run state.
func (e *editor) receiveAgent(event agent.Event, ok bool) bool {
	a := e.agent
	if a == nil || a.run == nil {
		return false
	}
	wasUnread, before := a.unread, a.session.State
	if !ok {
		result := a.run.Result()
		a.session.State = result.State
		if result.Err != nil {
			a.session.Log.Add("Agent: " + result.Err.Error())
		}
		if result.Summary != "" {
			a.session.Remember("assistant", result.Summary)
		}
		a.run, a.approval, a.approvalLines = nil, nil, nil
		if a.provider != nil {
			a.provider.Close()
			a.provider = nil
		}
		a.viewDirty[1] = true
		e.status = "Agent: " + string(result.State)
	} else {
		a.session.Apply(event)
		switch event.Kind {
		case "text", "activity":
			a.appendActivityRows(event.Text)
		case "plan":
			a.viewDirty[0] = true
		case "change":
			a.viewDirty[2] = true
		case "check":
			a.viewDirty[3] = true
		case "approval":
			a.session.View = 1
			a.approval, a.approvalTop = event.Approval, 0
			a.approvalLines = nil
			e.status = "Agent approval required · open Agent to review"
		}
	}
	a.dirty = true
	if e.workspace != workspaceAgent {
		a.unread = true
	}
	// Hidden token deltas do not redraw the code viewport once its badge is set.
	return e.workspace == workspaceAgent || !wasUnread || before != a.session.State
}

func (e *editor) checkpointAgent(now time.Time, force bool) bool {
	if e.agent == nil {
		return false
	}
	a := e.agent
	changed := false
	if a.store != nil {
		select {
		case err := <-a.store.Errors():
			e.status = agent.Display(err.Error())
			changed = true
		default:
		}
		if os.Getenv("KIWICODE_AGENT_HISTORY") == "1" && !a.forget && a.dirty && (force || now.Sub(a.lastSaved) >= time.Second) {
			a.store.Queue(&a.session)
			a.lastSaved, a.dirty = now, false
		}
	}
	return changed
}

func (e *editor) closeAgent() {
	if e.agent == nil {
		return
	}
	a := e.agent
	if a.run != nil {
		a.run.Cancel()
		// Cancellation unblocks both a saturated event queue and approval waits.
		<-a.run.Done
		result := a.run.Result()
		a.session.State = result.State
		a.run = nil
		a.dirty = true
	}
	a.approval, a.approvalLines = nil, nil
	if a.provider != nil {
		a.provider.Close()
		a.provider = nil
	}
	if a.memory != nil {
		if err := a.memory.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "context:", err)
		}
		a.memory = nil
	}
	e.checkpointAgent(time.Now(), true)
	if a.store != nil {
		a.store.Close()
		a.store = nil
	}
}

func (e *editor) workspaceTabs(width int) string {
	if width <= 0 {
		return ""
	}
	label := " Agent"
	if e.agent != nil {
		switch {
		case e.agent.approval != nil:
			label += " !"
		case e.agent.run != nil || e.agent.memoryBusy():
			label += " ~"
		case e.agent.unread:
			label += " *"
		}
	}
	active := e.workspace == workspaceAgent
	prefix := style(active, "\x1b[1;4m"+ansiFG(colors.accent), "\x1b[22;24m"+ansiFG(colors.muted))
	first := prefix + fit(label, min(width, agentTabWidth)) + "\x1b[22;24m"
	if width <= agentTabWidth {
		return first
	}
	return first + e.tabs(width-agentTabWidth)
}

func (e *editor) selectWorkspaceTab(column int) {
	if column < 0 {
		return
	}
	if column < agentTabWidth {
		e.activateAgent()
		return
	}
	e.selectTab(column - agentTabWidth)
}

func (e *editor) handleAgentMouse(k key) {
	if k.release || k.button&32 != 0 {
		return
	}
	if k.button == 64 || k.button == 65 {
		delta := 3
		if k.button == 64 {
			delta = -3
		}
		e.ensureAgent().scroll(delta)
		return
	}
	if k.button != 0 {
		return
	}
	if k.y == 2 {
		e.selectWorkspaceTab(k.x - 1)
		return
	}
	if k.y == 3 {
		view := e.ensureAgent().viewTabStart + (k.x-1)/12
		if view >= 0 && view < agent.ViewCount {
			e.ensureAgent().session.View = view
			e.agent.dirty = true
		}
	}
	// Approval is deliberately not bound to arbitrary content clicks. The user
	// must issue /approve or /deny after reading the complete request.
}

func (e *editor) handlePaste(text string) {
	if e.workspace == workspaceAgent {
		a := e.ensureAgent()
		if len(text)+len(a.session.Draft) > 16<<10 {
			e.status = "Agent draft is limited to 16 KiB"
			return
		}
		a.insert(text) // Newlines remain draft text, never Enter events.
		return
	}
	if !e.editorFocused() || e.popup != nil || e.searchMode != "" || e.folderPrompt || e.newFilePrompt || e.sourceCommitFocused {
		e.status = "Paste into an editor buffer or the Agent draft"
		return
	}
	if len(text) > 2<<20 {
		e.status = "Paste is larger than 2 MiB"
		return
	}
	b := e.current()
	b.recordUndo()
	b.suppressUndo = true
	e.deleteSelection()
	b.insertText(text)
	b.suppressUndo = false
	e.selection = textSelection{}
	e.invalidateCompletion()
}

// Kept separate so callers can show actionable status without accepting a
// provider or terminal error as trusted ANSI text.
func agentStatus(format string, args ...any) string {
	return agent.Display(fmt.Sprintf(format, args...))
}
