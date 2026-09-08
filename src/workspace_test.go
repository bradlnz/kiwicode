package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func awaitWorkspaceSwitch(t testing.TB, e *editor) {
	t.Helper()
	select {
	case loaded := <-e.workspaceSwitchEvents():
		e.finishWorkspaceSwitch(loaded)
	case <-time.After(5 * time.Second):
		t.Fatal("workspace switch timed out")
	}
}

func TestWorkspaceSwitchRetainsLiveState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	oldSettings, oldColors := settings, colors
	t.Cleanup(func() { settings, colors = oldSettings, oldColors })
	root, other := t.TempDir(), t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile("draft.txt", []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	e := newEditor()
	defer e.closeWorkspaces()
	b := e.current()
	b.handle(key{r: 'X'})
	e.shell.history = []string{"keep this history"}
	e.collapsed["nested"] = false
	e.rows, e.cols = 30, 100
	e.projectSlots = []string{root, other}
	e.projectSlot = 0
	e.completionCache = map[string]dependencyCompletion{"draft.txt": {words: []string{"cached"}}}
	setColorScheme("forest")
	e.openProjectSlot(1)
	if e.switching == nil {
		t.Fatalf("cold switch did not start loading: %s", e.status)
	}
	awaitWorkspaceSwitch(t, e)
	e.rows, e.cols = 40, 120
	// A warm switch must restore the live objects, not reconstruct a snapshot.
	e.handleTopBarMouse(key{mouse: true, button: 0, x: 107, y: 1})
	awaitWorkspaceSwitch(t, e)
	if mustCwd() != root || e.workspaceDone != nil || e.current() != b || len(b.undo) == 0 || !b.dirty || colors.name != "Forest" || e.rows != 40 || e.cols != 120 {
		t.Fatalf("warm switch lost live state or restarted loading: %s", e.status)
	}
	if len(e.shell.history) != 1 || e.shell.history[0] != "keep this history" || e.collapsed["nested"] || e.completionCache["draft.txt"].words[0] != "cached" {
		t.Fatal("workspace caches were rebuilt")
	}
	data, _ := os.ReadFile("draft.txt")
	if string(data) != "original" {
		t.Fatal("switch wrote dirty contents to the source file")
	}
	// A failed checkpoint must leave the current project and edits intact.
	stateHome := os.Getenv("XDG_STATE_HOME")
	if e.checkpoint != nil {
		_ = e.checkpoint.close()
		e.checkpoint = nil
	}
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "draft.txt", "invalid"))
	e.openFolder(t.TempDir())
	awaitWorkspaceSwitch(t, e)
	if mustCwd() != root || e.current() != b || !strings.Contains(e.status, "Cache workspace failed") {
		t.Fatal("failed checkpoint discarded the live workspace")
	}
	t.Setenv("XDG_STATE_HOME", stateHome)
}

func TestSlowWorkspaceCheckpointDoesNotBlockUI(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	root, other, bin := t.TempDir(), t.TempDir(), t.TempDir()
	t.Chdir(root)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	oldSettings, oldColors := settings, colors
	t.Cleanup(func() { settings, colors = oldSettings, oldColors })
	release := filepath.Join(bin, "release")
	script := fmt.Sprintf("#!/bin/sh\nwhile [ ! -f '%s' ]; do sleep 0.01; done\nexec '%s' \"$@\"\n", release, sqlite)
	if err := os.WriteFile(filepath.Join(bin, "sqlite3"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	e := &editor{buffers: []*buffer{newBuffer("draft.txt", []byte("unsaved"))}, rows: 30, cols: 100}
	e.lastFrame = "\x1b[1;1HWorkspace tabs"
	e.current().dirty = true
	defer e.closeWorkspaces()
	defer func() { _ = os.WriteFile(release, nil, 0600) }()
	started := time.Now()
	e.openFolder(other)
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("folder switch blocked the input handler")
	}
	// SQLite is deliberately blocked. The UI must continue its own work.
	for frame := 0; frame < 3; frame++ {
		time.Sleep(110 * time.Millisecond)
		e.pollEditor(nil)
		e.rows, e.cols = 40+frame, 110+frame
		modal := e.workspaceSwitchFrame()
		if !strings.Contains(modal, "Switching project") || !strings.Contains(modal, "Workspace tabs") || !strings.Contains(modal, "╭") || strings.Contains(modal, "\x1b[2J") {
			t.Fatal("slow checkpoint did not show a modal over the workspace")
		}
		e.handle(key{r: 'X'})
		e.handlePaste("do not modify the worker's snapshot")
		if e.terminalEvents() != nil {
			t.Fatal("UI accessed worker-owned state during the checkpoint")
		}
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	awaitWorkspaceSwitch(t, e)
	if mustCwd() != other || e.rows != 42 || e.cols != 112 {
		t.Fatal("completion lost the new terminal size")
	}
	if err := os.Remove(release); err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	e.openFolder(root)
	awaitWorkspaceSwitch(t, e)
	if time.Since(started) > 500*time.Millisecond {
		t.Fatal("return to cached slot waited for the outgoing checkpoint")
	}
	if string(e.current().lines[0]) != "unsaved" || !e.current().dirty {
		t.Fatal("checkpoint or pending input changed the dirty buffer")
	}
	e.current().handle(key{r: 'X'})
	e.openFolder(other)
	awaitWorkspaceSwitch(t, e)
	e.openFolder(root)
	awaitWorkspaceSwitch(t, e)
	if string(e.current().lines[0]) != "Xunsaved" {
		t.Fatal("pending checkpoints overwrote live edits")
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.checkpoint.flush(); err != nil {
		t.Fatal(err)
	}
	// Serialized writes must leave the newest snapshot at this project's path.
	loaded := &editor{}
	if err := loaded.restoreState(); err != nil {
		t.Fatal(err)
	}
	if string(loaded.current().lines[0]) != "Xunsaved" || !loaded.current().dirty {
		t.Fatal("checkpoint ordering lost the latest unsaved edits")
	}
}

func BenchmarkWorkspaceSwitch(b *testing.B) {
	for _, warm := range []bool{false, true} {
		name := "RebuildFileListCache"
		if warm {
			name = "LiveWorkspaceCache"
		}
		b.Run(name, func(b *testing.B) {
			b.Setenv("XDG_STATE_HOME", b.TempDir())
			root, other := b.TempDir(), b.TempDir()
			b.Chdir(root)
			var files []string
			for i := 0; i < 100; i++ {
				path := fmt.Sprintf("sample_%03d_test.go", i)
				var source strings.Builder
				source.WriteString("package demo\nimport \"testing\"\n")
				for j := 0; j < 40; j++ {
					fmt.Fprintf(&source, "func TestExample%d(t *testing.T) {}\n", j)
				}
				if err := os.WriteFile(path, []byte(source.String()), 0600); err != nil {
					b.Fatal(err)
				}
				files = append(files, path)
			}
			e := &editor{buffers: []*buffer{newBuffer("draft.txt", []byte("draft"))}}
			defer e.closeWorkspaces()
			e.indexFiles(files)
			otherEditor := *e
			e.workspaces = map[string]cachedWorkspace{other: {otherEditor, settings, colors}}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if warm {
					destination := other
					if i%2 == 1 {
						destination = root
					}
					e.openFolder(destination)
					awaitWorkspaceSwitch(b, e)
					if e.workspaceDone != nil || mustCwd() != destination {
						b.Fatalf("warm switch failed: %s", e.status)
					}
				} else {
					if err := e.saveState(); err != nil {
						b.Fatal(err)
					}
					e.indexFiles(files)
				}
			}
		})
	}
}

func TestWorkspaceWorkersKeepTheirOrigin(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile("model.go", []byte("package demo\ntype Original struct { Name string }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	e := &editor{files: []string{"model.go"}, buffers: []*buffer{newBuffer("app.go", []byte("package demo\n"))}}
	e.loadMembersInBackground("app.go")
	done := e.completionDone
	// The UI editor is overwritten on every switch. A worker must retain the
	// old channel and absolute root instead of looking through that pointer.
	*e = editor{}
	if err := os.Chdir(other); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.completion.definitions["Original"].path != "model.go" {
			t.Fatalf("worker read the new project: %#v", result.completion.definitions)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("completion result was sent to the new workspace")
	}
	if path, _, found := findDefinition("app.go", "Original", []string{"model.go"}, nil, root); !found || path != "model.go" {
		t.Fatal("definition lookup used the new working directory")
	}
	if markers := sourceChangeMarkers(sourceChange{path: "model.go", status: "??"}, root); len(markers) != 2 {
		t.Fatal("source markers used the new working directory")
	}
}
