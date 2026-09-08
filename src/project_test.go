package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewProject(t *testing.T) {
	oldSettings, oldColors := settings, colors
	t.Cleanup(func() { settings, colors = oldSettings, oldColors })
	resetSettings()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	t.Chdir(root)
	b := newBuffer("draft.txt", []byte("unsaved work"))
	b.dirty = true
	e := &editor{rows: 30, cols: 100, buffers: []*buffer{b}}
	defer e.closeWorkspaces()
	e.performAction("new-project")
	if !e.folderPrompt || !e.newProjectPrompt {
		t.Fatal("New Project did not open the folder prompt")
	}
	e.folderInput = nil
	e.handleFolderPrompt(key{code: keyEnter})
	if !e.folderPrompt || e.switching != nil {
		t.Fatal("empty project path must keep the prompt open")
	}
	e.folderInput = []rune(root)
	e.handleFolderPrompt(key{code: keyEnter})
	awaitWorkspaceSwitch(t, e)
	if !e.folderPrompt || !strings.Contains(e.status, "already exists") || e.current() != b {
		t.Fatal("existing paths must be rejected without losing the prompt or edits")
	}
	path := filepath.Join(root, "new parent", "my project")
	e.folderInput = []rune(path)
	e.shell.running = true
	e.handleFolderPrompt(key{code: keyEnter})
	if e.switching != nil || !e.folderPrompt {
		t.Fatal("creating a project must not interrupt a running command")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("blocked creation touched the filesystem")
	}
	e.shell.running = false
	e.handleFolderPrompt(key{code: keyEnter})
	awaitWorkspaceSwitch(t, e)
	if mustCwd() != path || !e.showExplorer || !e.explorer || e.folderPrompt || e.newProjectPrompt || len(e.files) != 0 {
		t.Fatalf("new project did not open as an empty workspace: %s", e.status)
	}
	if entries, err := os.ReadDir(path); err != nil || len(entries) != 0 {
		t.Fatalf("new project directory should be empty: %v", err)
	}
	e.performAction("new")
	e.newFileInput = []rune("main.go")
	e.handleNewFilePrompt(key{code: keyEnter})
	if _, err := os.Stat(filepath.Join(path, "main.go")); err != nil {
		t.Fatal("first file was not created in the new project:", err)
	}
	e.openFolder(root)
	awaitWorkspaceSwitch(t, e)
	if e.current() != b || !b.dirty || e.folderPrompt || e.newProjectPrompt {
		t.Fatal("creating a project lost the previous workspace's unsaved edits")
	}
	e.performAction("new-project")
	e.handleFolderPrompt(key{})
	if e.folderPrompt || e.newProjectPrompt {
		t.Fatal("Escape did not cancel project creation")
	}
}
