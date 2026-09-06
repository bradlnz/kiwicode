package main

import (
	"code-editor/internal/agent"
	"code-editor/internal/contextgraph"
	"fmt"
	"os"
	"strings"
)

var agentViewNames = []string{"Plan", "Activity", "Changes", "Checks", "Graph", "Quality", "Context"}

func (e *editor) contextEvents() <-chan contextgraph.View {
	if e.agent == nil || e.agent.memory == nil {
		return nil
	}
	return e.agent.memory.Events()
}
func (e *editor) receiveContext(v contextgraph.View) bool {
	a := e.agent
	if a == nil {
		return false
	}
	a.memoryView = v
	a.memoryReady = true
	for _, i := range []int{3, 4, 5, 6} {
		a.viewDirty[i] = true
	}
	if v.Error != "" {
		e.status = "Context: " + agent.Display(v.Error)
	} else if !v.Busy {
		e.status = "Context: " + v.Session.Status
	}
	wasUnread := a.unread
	if e.workspace != workspaceAgent {
		a.unread = true
	}
	return e.workspace == workspaceAgent || !wasUnread
}
func (a *agentWorkspace) memoryBusy() bool { return a.memory != nil && a.memory.Busy() }
func (e *editor) contextSlash(text string) (handled, accepted bool) {
	command, arg, _ := strings.Cut(text, " ")
	arg = strings.TrimSpace(arg)
	switch command {
	case "/index", "/remember", "/include", "/exclude", "/task", "/check", "/reward":
	default:
		return false, false
	}
	a := e.ensureAgent()
	if e.workspaceDone != nil {
		e.status = "Workspace is still loading"
		return true, false
	}
	if a.run != nil || a.memory == nil || a.memoryBusy() {
		e.status = "Finish or cancel the current agent/context operation first"
		return true, false
	}
	if command == "/index" || command == "/check" || command == "/reward" {
		if arg != "" {
			a.checkArmed = false
			e.status = "Use " + command + " alone; additional input was not executed"
			return true, false
		}
	}
	if command == "/check" {
		if os.Getenv("KIWICODE_AGENT_ALLOW_COMMANDS") != "1" {
			e.status = "Host checks disabled; set KIWICODE_AGENT_ALLOW_COMMANDS=1 for trusted code"
			return true, false
		}
		if e.dirty() {
			e.status = "Save all dirty buffers before checking disk contents"
			return true, false
		}
		if !a.checkArmed {
			a.checkArmed = true
			e.status = "Repeat /check to authorise go test -count=1 ./... and go vet ./...; temporary snapshot, NOT a security sandbox"
			return true, false
		}
	}
	if command == "/reward" && e.dirty() {
		e.status = "Save all dirty buffers before evaluating a disk snapshot"
		return true, false
	}
	if !a.memory.Submit(contextgraph.Request{Kind: strings.TrimPrefix(command, "/"), Text: arg}) {
		e.status = "Context is unavailable or busy; input retained"
		return true, false
	}
	a.checkArmed = false
	switch command {
	case "/index":
		a.session.View = 4
	case "/reward":
		a.session.View = 5
	case "/check":
		a.session.View = 3
	default:
		a.session.View = 6
	}
	// Excluding a file cannot leave its old source in provider conversation history.
	if command == "/exclude" {
		a.session.History = nil
	}
	a.dirty = true
	e.status = "Context operation submitted: " + command
	return true, true
}
func (a *agentWorkspace) contextLines(view int) []string {
	v := a.memoryView
	s := v.Session
	switch view {
	case 4:
		rows := []string{fmt.Sprintf("Graph: %d files, %d symbols, %d debt facts", v.GraphFiles, v.GraphSymbols, v.GraphFindings), "Snapshot: " + v.GraphHash, fmt.Sprintf("Last scan: %d read, %d changed, %d reused, %d removed", v.Stats.Read, v.Stats.Parsed, v.Stats.Reused, v.Stats.Removed), "/index refreshes on the context worker; no per-keypress scan.", "Go calls are syntactic, not type-resolved. Other files have metadata only."}
		return append(rows, v.GraphLines...)
	case 5:
		r := s.Reward
		rows := []string{fmt.Sprintf("Score %d | new credit %d | high water %d", r.Score, r.NewCredit, s.HighWater), r.Reason, fmt.Sprintf("%d low-complexity quality facts; code volume itself earns no points.", v.GraphQuality), "Policy v1: 5 points per reduced complexity-excess unit against the fixed baseline.", "/check requires opt-in and confirmation; /reward records your review.", "Test changes/deletions and removed/renamed functions withhold automatic credit.", "This is an auditable heuristic, not proof of overall quality or a trained model."}
		for _, d := range r.Components {
			rows = append(rows, fmt.Sprintf("%+d %s", d.Points, d.Label))
		}
		return rows
	default:
		rows := []string{"Local persistent context. /remember NOTE, /include PATH, /exclude PATH.", "Notes and included graph metadata accompany your next submitted task.", "Source reads still require the runtime's explicit approval.", "Task: " + s.Task, "Notes:"}
		rows = append(rows, s.Notes...)
		rows = append(rows, "Included files:")
		rows = append(rows, s.Included...)
		return rows
	}
}
