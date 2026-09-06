package main

import (
	"code-editor/internal/agent"
	"path/filepath"
	"strconv"
	"strings"
)

func (e *editor) agentSlash(text string) bool {
	a := e.ensureAgent()
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "/approve", "/deny", "/cancel", "/new", "/forget":
		if len(fields) != 1 {
			e.status = "Use " + fields[0] + " alone; additional text was not executed"
			return false
		}
	}
	switch fields[0] {
	case "/cancel":
		e.cancelAgent()
	case "/approve", "/deny":
		if a.run == nil || a.approval == nil || !a.run.Decide(a.approval.ID, fields[0] == "/approve") {
			e.status = "No current approval request"
			return false
		}
		a.session.Log.Add(fields[0] + ": " + a.approval.Operation)
		a.approval, a.approvalLines = nil, nil
		a.session.State = agent.Running
		a.viewDirty[1], a.dirty = true, true
		e.status = "Approval decision sent"
	case "/apply", "/open", "/reject":
		if len(fields) != 2 {
			e.status = "Use /apply N, /open N or /reject N with a change number"
			return false
		}
		i, err := strconv.Atoi(fields[1])
		if err != nil || i < 1 || i > len(a.session.Changes) {
			e.status = "Invalid change number"
			return false
		}
		if fields[0] == "/apply" {
			if err = e.applyAgentChange(i - 1); err != nil {
				e.status = err.Error()
				return false
			}
		} else if fields[0] == "/reject" {
			if a.run != nil {
				e.status = "Wait for the run to stop before rejecting changes"
				return false
			}
			if a.session.Changes[i-1].Applied {
				e.status = "Use file Undo for an already-applied change"
				return false
			}
			path := a.session.Changes[i-1].Path
			copy(a.session.Changes[i-1:], a.session.Changes[i:])
			a.session.Changes[len(a.session.Changes)-1] = agent.Change{}
			a.session.Changes = a.session.Changes[:len(a.session.Changes)-1]
			a.viewDirty[2] = true
			e.status = "Rejected proposal: " + path
		} else {
			path := a.session.Changes[i-1].Path
			// Reuse the live buffer even when its stored path is relative.
			// Opening an absolute alias must not reload old disk contents.
			target := filepath.Join(a.root, filepath.FromSlash(path))
			for _, b := range e.buffers {
				if relative, err := workspaceRelative(a.root, b.path); err == nil && relative == path {
					target = b.path
					break
				}
			}
			e.open(target)
		}
	case "/new":
		if a.run != nil {
			e.status = "Cancel the run before starting a new session"
			return false
		}
		if len(a.session.Changes) > 0 && !a.newArmed {
			a.newArmed = true
			e.status = "Repeat /new to discard the current review (applied buffer edits are retained)"
			return false
		}
		a.session = agent.NewSession()
		a.viewLines, a.activityRows = [4][]string{}, agent.Log{}
		a.follow = true
		a.viewDirty = [4]bool{true, true, true, true}
		a.newArmed, a.forget = false, false
		e.status = "New agent session"
	case "/forget":
		if a.run != nil {
			e.status = "Cancel the run before forgetting history"
			return false
		}
		if a.store != nil {
			a.store.Queue(nil)
		}
		a.session = agent.NewSession()
		a.viewLines, a.activityRows = [4][]string{}, agent.Log{}
		a.follow = true
		a.viewDirty = [4]bool{true, true, true, true}
		a.forget = true
		e.status = "Agent history cleared; applied file edits are retained"
	default:
		e.status = "Commands: /approve /deny /cancel /apply N /open N /reject N /new /forget"
		return false
	}
	return true
}
