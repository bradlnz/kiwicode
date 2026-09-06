package main

import (
	"code-editor/internal/agent"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func agentTestEditor(t *testing.T) *editor {
	t.Helper()
	t.Setenv("KIWICODE_AGENT_HISTORY", "0")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	path := filepath.Join(root, "sample.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	e := &editor{buffers: []*buffer{newBuffer(path, []byte("before\n"))}, files: []string{"sample.txt"}, rows: 30, cols: 100, agent: newAgentWorkspace(root)}
	t.Cleanup(e.closeAgent)
	e.ensureAgent()
	select {
	case v := <-e.contextEvents():
		e.receiveContext(v)
	case <-time.After(5 * time.Second):
		t.Fatal("context did not load")
	}
	return e
}

func TestAgentIsNotAFileBuffer(t *testing.T) {
	e := agentTestEditor(t)
	b := e.current()
	b.row, b.col, b.scrollX = 0, 3, 1
	b.insert([]rune("!"))
	before := strings.Join(runeLines(b.lines), "\n")
	undoCount := len(b.undo)
	e.selection = textSelection{buffer: b, startRow: 0, startCol: 1, endRow: 0, endCol: 3}
	selection := e.selection
	e.handleCommand("agent")
	if e.workspace != workspaceAgent || e.editorFocused() || len(e.buffers) != 1 {
		t.Fatal("Agent was not a separate workspace")
	}
	for _, action := range []string{"save", "undo", "format", "diagnostics", "run", "test", "debug", "paste", "copy", "new", "go-to-definition"} {
		e.handleCommand(action)
	}
	for _, k := range []key{{r: 'x'}, {code: keyBackspace}, {code: keyDelete}, {code: keyTab}, {code: keyLeft}} {
		e.handle(k)
	}
	if got := strings.Join(runeLines(b.lines), "\n"); got != before || len(b.undo) != undoCount || b.col != 4 || b.scrollX != 1 || e.selection != selection {
		t.Fatal("Agent input changed file state")
	}
	e.handleCommand("close")
	if e.workspace != workspaceFile || e.current() != b || len(e.buffers) != 1 {
		t.Fatal("closing Agent closed its underlying file")
	}
	data, _ := os.ReadFile(b.path)
	if string(data) != "before\n" {
		t.Fatal("Agent saved a hidden file")
	}
}

func TestAgentTabHitTestingAndState(t *testing.T) {
	e := agentTestEditor(t)
	e.selectWorkspaceTab(0)
	e.handle(key{r: 'h'})
	e.handle(key{r: 'i'})
	e.agent.session.View = 2
	e.selectWorkspaceTab(agentTabWidth + settings.tabPadding)
	if e.workspace != workspaceFile {
		t.Fatal("file tab was not selected")
	}
	e.selectWorkspaceTab(agentTabWidth - 1)
	if e.workspace != workspaceAgent || string(e.agent.input) != "hi" || e.agent.session.View != 2 {
		t.Fatal("tab switch lost draft or view")
	}
	for width := 0; width < agentTabWidth+2; width++ {
		_ = e.workspaceTabs(width)
	}
	e.handleAgentMouse(key{mouse: true, button: 0, x: agentTabWidth + settings.tabPadding + 1, y: 2})
	if e.workspace != workspaceFile {
		t.Fatal("mouse tab selection disagreed with rendering")
	}
}

func TestAgentApplyUndoAndConflictChecks(t *testing.T) {
	e := agentTestEditor(t)
	e.agent.session.Changes = []agent.Change{{Path: "sample.txt", Before: "before\n", After: "after\n", Existed: true}}
	b := e.current()
	b.dirty = true
	if err := e.applyAgentChange(0); err == nil {
		t.Fatal("overwrote dirty buffer")
	}
	b.dirty = false
	if err := os.WriteFile(b.path, []byte("external\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.applyAgentChange(0); err == nil {
		t.Fatal("overwrote stale disk file")
	}
	if err := os.WriteFile(b.path, []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.applyAgentChange(0); err != nil {
		t.Fatal(err)
	}
	if string(b.lines[0]) != "after" || !b.dirty || !e.agent.session.Changes[0].Applied {
		t.Fatal("edit was not applied to buffer")
	}
	if data, _ := os.ReadFile(b.path); string(data) != "before\n" {
		t.Fatal("apply wrote live disk")
	}
	if !b.undoChange() || string(b.lines[0]) != "before" || b.dirty {
		t.Fatal("apply was not undoable")
	}
}

func TestAgentNewFileAndSymlinkConflict(t *testing.T) {
	e := agentTestEditor(t)
	a := e.agent
	a.session.Changes = []agent.Change{{Path: "new.txt", After: "new\n"}}
	if err := e.applyAgentChange(0); err != nil {
		t.Fatal(err)
	}
	if len(e.buffers) != 2 || !e.buffers[1].dirty {
		t.Fatal("new file was not staged in a buffer")
	}
	if _, err := os.Stat(filepath.Join(a.root, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("new file was written without Save")
	}
	outside := filepath.Join(t.TempDir(), "other.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(a.root, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	a.session.Changes = []agent.Change{{Path: "alias.txt", Before: "secret", After: "changed", Existed: true}}
	if err := e.applyAgentChange(0); err == nil {
		t.Fatal("applied through symlink")
	}
}

func TestAgentRejectAndUnconfiguredProvider(t *testing.T) {
	e := agentTestEditor(t)
	e.activateAgent()
	e.agent.session.Changes = []agent.Change{{Path: "sample.txt", Before: "before\n", After: "after\n", Existed: true}}
	if !e.agentSlash("/reject 1") || len(e.agent.session.Changes) != 0 || e.current().dirty {
		t.Fatal("reject changed the file")
	}
	t.Setenv("KIWICODE_AGENT_ENDPOINT", "")
	t.Setenv("KIWICODE_AGENT_MODEL", "")
	e.agent.insert("perform a task")
	e.submitAgent()
	if e.agentRunning() || !strings.Contains(e.status, "unavailable") || string(e.agent.input) != "perform a task" {
		t.Fatal("unconfigured agent simulated a run or lost the draft")
	}
}

func uiStream(w http.ResponseWriter, m agent.Message) {
	w.Header().Set("Content-Type", "text/event-stream")
	delta := map[string]any{"content": m.Content}
	finish := "stop"
	if len(m.ToolCalls) > 0 {
		finish = "tool_calls"
		var calls []any
		for i, c := range m.ToolCalls {
			calls = append(calls, map[string]any{"index": i, "id": c.ID, "type": "function", "function": c.Function})
		}
		delta["tool_calls"] = calls
	}
	data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
}
func uiCall(name string, args any) agent.Message {
	data, _ := json.Marshal(args)
	return agent.Message{Role: "assistant", ToolCalls: []agent.Call{{ID: name, Type: "function", Function: agent.FunctionCall{Name: name, Arguments: string(data)}}}}
}

func TestAgentHTTPTaskToReviewAndFileNavigation(t *testing.T) {
	e := agentTestEditor(t)
	var step atomic.Int32
	answers := []agent.Message{
		uiCall("read_file", map[string]string{"path": "sample.txt"}),
		uiCall("propose_edit", map[string]string{"path": "sample.txt", "old_text": "before", "new_text": "after"}),
		{Role: "assistant", Content: "Proposed change is ready; no checks were run."},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(step.Add(1)) - 1
		if i >= len(answers) {
			w.WriteHeader(500)
			return
		}
		uiStream(w, answers[i])
	}))
	defer server.Close()
	t.Setenv("KIWICODE_AGENT_ENDPOINT", server.URL)
	t.Setenv("KIWICODE_AGENT_MODEL", "integration-test")
	t.Setenv("KIWICODE_AGENT_API_KEY", "")
	e.activateAgent()
	e.agent.insert("Replace before with after in sample.txt")
	e.submitAgent()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	approvals := 0
	for e.agentRunning() {
		select {
		case event, ok := <-e.agentEvents():
			e.receiveAgent(event, ok)
			if e.agent.approval != nil {
				approvals++
				if !strings.Contains(e.agentFrame(), "sample.txt") {
					t.Fatal("approval did not identify context")
				}
				if !e.agentSlash("/approve") {
					t.Fatal("approval not routed to worker")
				}
			}
		case <-deadline.C:
			t.Fatal("agent UI pipeline timed out")
		}
	}
	if approvals != 1 || e.agent.session.State != agent.Reviewing || len(e.agent.session.Changes) != 1 || e.current().dirty {
		t.Fatal("unexpected review state")
	}
	if !e.agentSlash("/apply 1") || !e.agentSlash("/open 1") || e.workspace != workspaceFile || string(e.current().lines[0]) != "after" {
		t.Fatal("review did not navigate to applied buffer")
	}
}

type waitingAgentProvider struct{}

func (waitingAgentProvider) Respond(ctx context.Context, _ []agent.Message, _ func(string) error) (agent.Message, error) {
	<-ctx.Done()
	return agent.Message{}, ctx.Err()
}

func TestAgentHiddenRunCancellationAndQuitGuard(t *testing.T) {
	e := agentTestEditor(t)
	e.agent.run = agent.Start(context.Background(), agent.Request{Root: e.agent.root, Prompt: "wait"}, waitingAgentProvider{})
	if handled, quit := e.handleCommand("quit"); !handled || quit || !e.quitArmed {
		t.Fatal("quit did not guard active run")
	}
	e.handle(key{r: 'x'})
	if e.quitArmed {
		t.Fatal("typing did not disarm quit")
	}
	e.handleCommand("agent-cancel")
	deadline := time.After(2 * time.Second)
	for e.agentRunning() {
		select {
		case ev, ok := <-e.agentEvents():
			e.receiveAgent(ev, ok)
		case <-deadline:
			t.Fatal("hidden cancellation did not finish")
		}
	}
	if e.agent.session.State != agent.Cancelled || !e.agent.unread {
		t.Fatal("hidden run result was lost")
	}
}

func TestAgentEarlyDraftSurvivesWorkspaceLoad(t *testing.T) {
	e := agentTestEditor(t)
	e.activateAgent()
	e.agent.insert("early draft")
	e.workspaceDone = make(chan editorLoad, 1)
	loaded := &editor{buffers: []*buffer{newBuffer("loaded.txt", nil)}, agent: newAgentWorkspace(e.agent.root)}
	loaded.agent.session.Draft = "old persisted draft"
	e.workspaceDone <- editorLoad{editor: loaded}
	if ok, err := e.pollWorkspace(); !ok || err != nil {
		t.Fatal("load did not finish")
	}
	if e.workspace != workspaceAgent || e.agent.session.Draft != "early draft" {
		t.Fatal("workspace restore replaced in-flight agent draft")
	}
}

func TestAgentFrameSanitizationAndBoundedAppend(t *testing.T) {
	e := agentTestEditor(t)
	e.activateAgent()
	e.agent.session.View = 1
	e.agent.session.Log.Add("safe\x1b[2J\x1b]52;c;secret\a\u202ehidden")
	frame := e.agentFrame()
	if strings.Contains(frame, "\x1b[2J") || strings.Contains(frame, "\x1b]52") || strings.Contains(frame, "\u202e") {
		t.Fatal("model output injected terminal control")
	}
	for i := 0; i < 2000; i++ {
		e.agent.appendActivityRows("new event")
	}
	if e.agent.activityRows.Len() > agent.MaxLogEntries {
		t.Fatal("presentation ring grew without bound")
	}
	e.agent.scroll(-3)
	if e.agent.follow {
		t.Fatal("scrolling up did not disable follow")
	}
	e.agent.scroll(10000)
	if !e.agent.follow {
		t.Fatal("scrolling to bottom did not restore follow")
	}
	for _, size := range [][2]int{{30, 8}, {80, 24}, {120, 40}, {29, 7}} {
		e.cols, e.rows = size[0], size[1]
		_ = e.agentFrame()
	}
}

func TestBracketedPasteNeverExecutesAgentCommands(t *testing.T) {
	e := agentTestEditor(t)
	e.activateAgent()
	var d inputDecoder
	var events []inputEvent
	emit := func(ev inputEvent) bool { events = append(events, ev); return true }
	payload := "/approve\n\x11hello 世界"
	for _, part := range []string{"\x1b[20", "0~" + payload[:8], payload[8:] + "\x1b[20", "1~"} {
		if !d.feed([]byte(part), emit) {
			t.Fatal("decode failed")
		}
	}
	if len(events) != 1 || !events[0].isPaste || events[0].paste != payload {
		t.Fatalf("paste became key events: %+v", events)
	}
	e.handlePaste(events[0].paste)
	if e.quitArmed || e.agentRunning() || e.current().dirty || !strings.Contains(e.agent.session.Draft, "/approve\n") {
		t.Fatal("paste executed instead of editing a draft")
	}
}

func TestInputDecoderFragmentsAndOverflow(t *testing.T) {
	var d inputDecoder
	var got []key
	emit := func(ev inputEvent) bool {
		if ev.err != nil || ev.isPaste {
			t.Fatal("unexpected paste")
		}
		got = append(got, ev.key)
		return true
	}
	data := []byte("é\x1b[A\x1b[3~\x1b[<0;5;3M\r")
	for _, b := range data {
		d.feed([]byte{b}, emit)
	}
	want := []key{{r: 'é'}, {code: keyUp}, {code: keyDelete}, {mouse: true, button: 0, x: 5, y: 3}, {code: keyEnter}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded %+v", got)
	}
	d.feed([]byte{27}, emit)
	d.timeout(emit)
	if len(got) != len(want)+1 || got[len(got)-1] != (key{}) {
		t.Fatal("lone Escape not decoded")
	}
	var overflow []inputEvent
	d.feed([]byte("\x1b[200~"+strings.Repeat("x", maxPasteBytes+1)+"\x1b[201~"), func(ev inputEvent) bool { overflow = append(overflow, ev); return true })
	if len(overflow) != 1 || overflow[0].err != errPasteTooLarge || overflow[0].paste != "" {
		t.Fatal("oversized paste was not discarded")
	}
}

func TestFrameScheduleCoalescesWithoutPostponing(t *testing.T) {
	var frames frameSchedule
	defer frames.stop()
	if frames.channel() != nil {
		t.Fatal("idle scheduler has a live frame channel")
	}
	frames.request()
	first := frames.timer
	for i := 0; i < 10000; i++ {
		frames.request()
	}
	if frames.timer != first {
		t.Fatal("allocated one timer per event")
	}
	select {
	case <-frames.channel():
		frames.fired()
	case <-time.After(time.Second):
		t.Fatal("frame starved")
	}
	if frames.channel() != nil {
		t.Fatal("idle frame kept redrawing")
	}
}

func BenchmarkAgentVisibleFrame(b *testing.B) {
	e := &editor{buffers: []*buffer{newBuffer("bench.txt", nil)}, agent: newAgentWorkspace("/tmp"), workspace: workspaceAgent, cols: 120, rows: 40}
	e.agent.session.View = 1
	for i := 0; i < agent.MaxLogEntries; i++ {
		e.agent.session.Log.Add(strings.Repeat("x", 1024))
	}
	_ = e.agentFrame()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = e.agentFrame()
	}
}
func BenchmarkInputASCII(b *testing.B) {
	var d inputDecoder
	data := []byte("abcdefghij")
	emit := func(inputEvent) bool { return true }
	d.feed(data, emit)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.feed(data, emit)
	}
}
func BenchmarkFrameIdle(b *testing.B) {
	var f frameSchedule
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if f.channel() != nil {
			b.Fatal("unexpected frame")
		}
	}
}
