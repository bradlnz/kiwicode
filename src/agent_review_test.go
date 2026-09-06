package main

import (
	"code-editor/internal/agent"
	"strings"
	"testing"
)

func TestAgentOpenReusesRelativeBuffer(t *testing.T) {
	e := agentTestEditor(t)
	b := e.current()
	b.path = "sample.txt"
	e.agent.session.Changes = []agent.Change{{Path: "sample.txt", Before: "before\n", After: "after\n", Existed: true}}
	if err := e.applyAgentChange(0); err != nil {
		t.Fatal(err)
	}
	if !e.agentSlash("/open 1") || len(e.buffers) != 1 || e.current() != b || string(b.lines[0]) != "after" {
		t.Fatal("opening a relative path alias reloaded disk instead of the reviewed buffer")
	}
}

func TestAgentDiffWrapRetainsChangedSuffix(t *testing.T) {
	a := newAgentWorkspace("/tmp")
	a.session.View = 2
	suffix := "changed-tail"
	a.session.Changes = []agent.Change{{Path: "sample.txt", Before: strings.Repeat("x", 200), After: strings.Repeat("x", 200) + suffix, Existed: true}}
	a.rebuildView(28)
	var joined strings.Builder
	for _, line := range a.viewLines[2] {
		joined.WriteString(line)
	}
	if !strings.Contains(joined.String(), suffix) {
		t.Fatal("diff suffix is not reviewable on a narrow terminal")
	}
	for _, line := range a.viewLines[2] {
		if strings.HasPrefix(line, "+") && len([]rune(line)) > 28 {
			t.Fatal("diff line was cropped rather than wrapped")
		}
	}
}

func TestAgentCommandsRejectTrailingText(t *testing.T) {
	e := agentTestEditor(t)
	e.agent.session.Log.Add("retained")
	for _, command := range []string{"/approve", "/deny", "/cancel", "/new", "/forget"} {
		if e.agentSlash(command+"\nnot a command") || !strings.Contains(e.status, "alone") {
			t.Fatalf("accepted extra input after %s", command)
		}
	}
	if e.agent.session.Log.Len() != 1 {
		t.Fatal("malformed command cleared the session")
	}
}
