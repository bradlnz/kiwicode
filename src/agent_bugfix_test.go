package main

import (
	"code-editor/internal/agent"
	"context"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestRunCommandRetainsDraftWhenAlreadyRunning(t *testing.T) {
	e := agentTestEditor(t)
	a := e.agent
	a.run = agent.Start(context.Background(), agent.Request{Root: a.root, Prompt: "existing task"}, waitingAgentProvider{})
	original := a.run
	a.insert("/run preserve this next task")
	draft, cursor := a.session.Draft, a.cursor
	history := append([]agent.Message(nil), a.session.History...)
	e.submitAgent()
	if string(a.input) != draft || a.session.Draft != draft || a.cursor != cursor {
		t.Fatal("a rejected /run erased the user's draft")
	}
	if a.run != original || !reflect.DeepEqual(a.session.History, history) {
		t.Fatal("rejected task replaced run/history")
	}
}

func seedOldAgentHistory(t *testing.T, e *editor) string {
	t.Helper()
	path, err := agent.SessionPath(e.agent.root)
	if err != nil {
		t.Fatal(err)
	}
	saved := agent.NewSession()
	saved.Remember("user", "old private task")
	store := agent.NewStore(path)
	store.Queue(&saved)
	store.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestForgetDeletesPreviousHistoryWhenPersistenceDisabled(t *testing.T) {
	e := agentTestEditor(t)
	path := seedOldAgentHistory(t, e)
	if e.agent.store != nil {
		t.Fatal("fixture should have persistence disabled")
	}
	if !e.agentSlash("/forget") {
		t.Fatal(e.status)
	}
	finishContext(t, e)
	e.closeAgent()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("forgotten history remained on disk: %v", err)
	}
}
func TestForgetDoesNotEnableSubsequentTranscriptPersistence(t *testing.T) {
	e := agentTestEditor(t)
	path := seedOldAgentHistory(t, e)
	if !e.agentSlash("/forget") {
		t.Fatal(e.status)
	}
	finishContext(t, e)
	e.agent.insert("new opt-out draft must not be saved")
	e.checkpointAgent(time.Now(), true)
	e.closeAgent()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("forget or subsequent typing left history on disk: %v", err)
	}
}
