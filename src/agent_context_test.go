package main

import (
	"code-editor/internal/agent"
	"strings"
	"testing"
	"time"
)

func finishContext(t *testing.T, e *editor) {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case v := <-e.contextEvents():
			e.receiveContext(v)
			if !v.Busy {
				return
			}
		case <-timer.C:
			t.Fatal("context stuck")
		}
	}
}
func TestMergedAgentContextAndSevenViews(t *testing.T) {
	e := agentTestEditor(t)
	e.activateAgent()
	if !e.agentSlash("/remember Keep Go and measure performance") {
		t.Fatal(e.status)
	}
	finishContext(t, e)
	if !strings.Contains(e.agent.memoryView.ProviderContext, "Keep Go") {
		t.Fatal("notes missing")
	}
	if !e.agentSlash("/include sample.txt") {
		t.Fatal(e.status)
	}
	finishContext(t, e)
	if !e.agentSlash("/index") {
		t.Fatal(e.status)
	}
	finishContext(t, e)
	if e.agent.memoryView.GraphFiles != 1 {
		t.Fatalf("graph files %d", e.agent.memoryView.GraphFiles)
	}
	for i := 0; i < agent.ViewCount; i++ {
		e.agent.session.View = i
		e.cols = 30
		frame := e.agentFrame()
		if !strings.Contains(frame, agentViewNames[i]) {
			t.Fatalf("view %d hidden", i)
		}
	}
	e.agent.session.History = []agent.Message{{Role: "assistant", Content: "source was previously read"}}
	if !e.agentSlash("/exclude sample.txt") {
		t.Fatal(e.status)
	}
	finishContext(t, e)
	if len(e.agent.session.History) != 0 {
		t.Fatal("excluded source history retained")
	}
	if !e.agentSlash("/forget") {
		t.Fatal(e.status)
	}
	finishContext(t, e)
	if len(e.agent.memoryView.Session.Notes) != 0 || len(e.agent.session.History) != 0 {
		t.Fatal("forget missed a store")
	}
}
func TestMergedAgentCheckNeedsOptInAndExactConfirmation(t *testing.T) {
	e := agentTestEditor(t)
	t.Setenv("KIWICODE_AGENT_ALLOW_COMMANDS", "0")
	if e.agentSlash("/check") || !strings.Contains(e.status, "disabled") {
		t.Fatal("check bypassed opt-in")
	}
	t.Setenv("KIWICODE_AGENT_ALLOW_COMMANDS", "1")
	if e.agentSlash("/check") || !e.agent.checkArmed {
		t.Fatal("check bypassed confirmation")
	}
	if e.agentSlash("/check extra") {
		t.Fatal("trailing args executed")
	}
	e.current().insert([]rune("x"))
	if e.agentSlash("/check") || !strings.Contains(e.status, "dirty") {
		t.Fatal("dirty file check allowed")
	}
	if e.agentSlash("/reward") {
		t.Fatal("dirty file rewarded")
	}
}
