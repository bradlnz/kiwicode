package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalFirstWorkspace(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")
	e := newEditor()
	defer e.closeWorkspaces()
	e.rows, e.cols = 30, 100
	if !e.shell.open || !e.shell.interactive || e.shell.focused {
		t.Fatal("terminal must default to an open dock without stealing focus")
	}
	e.pollEditor(nil)
	term := e.shell.terminal
	if term == nil || e.shell.focused {
		t.Fatalf("default terminal did not start: %s", e.status)
	}
	e.handleCommand("terminal")
	e.pollEditor(nil)
	if e.shell.open || e.shell.terminal != term {
		t.Fatal("closed dock reopened itself or lost its session")
	}
	e.handleCommand("terminal")
	if !e.terminalFocused() || e.shell.terminal != term {
		t.Fatal("reopening did not focus the existing shell")
	}
	e.shell.focused = false
	e.openContextMenu(key{x: 90, y: 3})
	items := append([]menuItem{}, quickActions...)
	items = append(items, e.popup.items...)
	for _, menu := range topMenus {
		if menu.label == "Run" {
			t.Fatal("obsolete Run menu remains")
		}
		items = append(items, menu.items...)
	}
	for _, action := range []string{"inspect", "slop", "agent", "agent-cancel", "run", "test", "debug", "diagnostics", "container-build", "container-run", "stop-process", "run-selected-test", "toggle-test"} {
		for _, item := range items {
			if item.action == action {
				t.Fatalf("obsolete launcher %s remains in menus", action)
			}
		}
		if handled, _ := e.handleCommand(action); handled || shortcutNames[action] != "" {
			t.Fatalf("obsolete launcher %s remains registered", action)
		}
	}
	e.popup, e.help = nil, true
	e.performAction("terminal")
	if !e.help || e.shell.open {
		t.Fatal("terminal quick action should toggle the dock without leaving the current view")
	}
}

func TestConfigurableProjectQuickPicks(t *testing.T) {
	oldSettings, oldColors := settings, colors
	t.Cleanup(func() { settings, colors = oldSettings, oldColors })
	resetSettings()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	t.Chdir(root)
	config := filepath.Join(root, "config.yaml")
	// Retired settings must not prevent subsequent settings from loading.
	if err := os.WriteFile(config, []byte("shortcuts:\n  run: ctrl+r\n  test: ctrl+r\n  debug: ctrl+r\n  diagnostics: ctrl+r\nicons:\n  test_checked: x\n  test_unchecked: o\nprojects:\n  quick_picks: 9\nterminal:\n  height: 7\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := readSettings(config); err != nil || settings.projectQuickPicks != 9 || settings.terminalHeight != 7 || shortcutAction(18) != "run-tests" {
		t.Fatalf("settings not applied: %v", err)
	}
	var saved []string
	for i := 1; i <= 9; i++ {
		path := filepath.Join(root, fmt.Sprint(i))
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		saved = append(saved, path)
	}
	if err := saveProjectSlots(saved); err != nil {
		t.Fatal(err)
	}
	e := &editor{cols: 100, projectSlots: saved, projectSlot: 8}
	for i := 0; i < 9; i++ {
		x := e.cols - projectSlotBarWidth() + i*3 + 2
		if index, ok := e.projectSlotAt(x); !ok || index != i {
			t.Fatalf("slot %d hit test failed", i+1)
		}
	}
	e.handle(key{alt: true, r: '9'})
	if !strings.HasPrefix(e.status, "Project 9:") || !strings.Contains(e.projectSlotBar(), " 9 ") {
		t.Fatal("ninth slot is not rendered or keyboard-accessible")
	}
	e.status = ""
	e.handle(key{mouse: true, button: 0, x: 99, y: 1})
	if !strings.HasPrefix(e.status, "Project 9:") {
		t.Fatal("ninth slot is not clickable")
	}
	for _, count := range []string{"1", "3", "9"} {
		if err := applySetting("projects", "quick_picks", count); err != nil {
			t.Fatal(err)
		}
		slots, active, err := loadProjectSlots(saved[8])
		if err != nil || len(slots) != 9 || slots[8] != saved[8] || (active == 8) != (count == "9") {
			t.Fatalf("changing count lost saved assignments: %v, active=%d, %v", slots, active, err)
		}
	}
	for _, value := range []string{"0", "-1", "10", "abc", "1.5"} {
		if err := applySetting("projects", "quick_picks", value); err == nil {
			t.Fatalf("invalid quick-pick count %q accepted", value)
		}
	}
}
