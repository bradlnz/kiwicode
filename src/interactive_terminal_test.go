package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInteractiveTerminal(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	if err := os.WriteFile(filepath.Join(root, ".bashrc"), []byte("export KIWI_RC_MARKER=rc_loaded\nalias kiwi_alias='printf alias_loaded'\nPS1='kiwi> '\n"), 0600); err != nil {
		t.Fatal(err)
	}
	term, err := newInteractiveTerminal(root, "/bin/bash", 12, 90)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(term.close)
	e := &editor{shell: shellPanel{terminal: term, interactive: true, open: true, focused: true}}
	frame := func() string {
		var out strings.Builder
		term.draw(&out, 1, 3, term.cols, term.rows)
		return out.String()
	}
	wait := func(want string) {
		t.Helper()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for !strings.Contains(frame(), want) {
			select {
			case event := <-term.events:
				e.receiveTerminal(event, true)
			case <-deadline.C:
				t.Fatalf("missing %q in %q", want, frame())
			}
		}
	}
	command := func(text string) {
		t.Helper()
		if err := term.send([]byte(text + "\r")); err != nil {
			t.Fatal(err)
		}
	}
	wait("kiwi>")
	command("printf '%s:' \"$KIWI_RC_MARKER\"; kiwi_alias; test -t 0 && printf ':tty_ready'; printf '\\n'")
	wait("rc_loaded:alias_loaded:tty_ready")
	command("printf 'cwd=%s\\n' \"$PWD\"")
	wait("cwd=" + root)
	// Install the handler before announcing readiness. Forking sleep after the
	// marker leaves a window where Ctrl+C can arrive before the child starts.
	command("sh -c \"trap 'exit 130' INT; printf '\\123\\124\\122\\105\\101\\115\\137\\116\\117\\127'; read line\"")
	wait("STREAM_NOW")  // Visible before the command exits.
	e.handle(key{r: 3}) // Ctrl+C must interrupt the foreground process, not copy editor text.
	// Wait for the fresh prompt, not a previous prompt still on screen.
	var interrupted strings.Builder
	interruptDeadline := time.After(5 * time.Second)
	for !strings.Contains(interrupted.String(), "kiwi> ") {
		select {
		case event := <-term.events:
			interrupted.Write(event.data)
			e.receiveTerminal(event, true)
		case <-interruptDeadline:
			t.Fatalf("Ctrl+C did not return to the shell prompt: status=%q output=%q", e.status, interrupted.String())
		}
	}
	command("printf '\\101\\106\\124\\105\\122\\137\\111\\116\\124\\105\\122\\122\\125\\120\\124\\n'")
	wait("AFTER_INTERRUPT")
	var resized strings.Builder
	term.draw(&resized, 1, 3, 70, 10)
	command("stty size")
	wait("10 70")
	command("for i in {1..30}; do printf 'history_%s\\n' \"$i\"; done")
	wait("history_30")
	if err := term.mouse(key{button: 64}); err != nil {
		t.Fatal(err)
	}
	if term.scroll != 3 {
		t.Fatal("missing terminal scrollback")
	}
	term.scroll = 0
	before := frame()
	e.receiveTerminal(terminalEvent{data: []byte("\x1b[?1049h\x1b[2J\x1b[2;5H\x1b[31mTUI_NODE\x1b[?25l")}, true)
	var tui strings.Builder
	row, col := term.draw(&tui, 1, 3, 70, 10)
	if !strings.Contains(tui.String(), "TUI_NODE") || row != 0 || col != 0 {
		t.Fatal("alternate screen/cursor visibility failed")
	}
	e.receiveTerminal(terminalEvent{data: []byte("\x1b[?1049l\x1b[?25h")}, true)
	if frame() != before {
		t.Fatal("alternate screen did not restore shell screen")
	}
	command("exit")
	deadline := time.After(5 * time.Second)
	for !term.ended {
		select {
		case event := <-term.events:
			e.receiveTerminal(event, true)
		case <-deadline:
			t.Fatal("shell did not exit")
		}
	}
	if term.events != nil {
		t.Fatal("exited terminal would spin the event loop")
	}
}

func TestTerminalKeySequences(t *testing.T) {
	for _, seq := range []string{"\x1bOP", "\x1b[1;5A", "\x1b[Z"} {
		var decoder inputDecoder
		var events []inputEvent
		decoder.feed([]byte(seq), func(event inputEvent) bool { events = append(events, event); return true })
		if len(events) != 1 || events[0].key.raw != seq {
			t.Fatalf("lost terminal key %q: %+v", seq, events)
		}
	}
}

func TestDockedTerminalLayoutAndFocus(t *testing.T) {
	oldSettings := settings
	t.Cleanup(func() { settings = oldSettings })
	settings.terminalHeight = 8
	e := &editor{rows: 30, cols: 100, buffers: []*buffer{newBuffer("file.go", []byte("package main"))}, shell: shellPanel{open: true, focused: true}}
	if content, panel := e.panelHeights(); content != 18 || panel != 9 {
		t.Fatalf("panel heights = %d,%d", content, panel)
	}
	var out strings.Builder
	row, _ := e.drawTerminalPanel(&out)
	if row != 29 || !strings.Contains(out.String(), "\x1b[21;1H") {
		t.Fatal("terminal is not docked immediately above status bar")
	}
	if e.modalViewOpen() || e.workspaceViewLabel() != "" {
		t.Fatal("terminal still takes a workspace tab")
	}
	e.handle(key{mouse: true, button: 0, x: 5, y: 3})
	if e.shell.focused || !e.editorFocused() {
		t.Fatal("click above dock did not focus editor")
	}
	e.handle(key{mouse: true, button: 0, x: 5, y: 22})
	if !e.shell.focused || e.editorFocused() {
		t.Fatal("click in dock did not focus terminal")
	}
	e.clearModalViews()
	if !e.shell.open {
		t.Fatal("changing workspace views closed the dock")
	}
	e.rows = 10
	if content, panel := e.panelHeights(); content < 5 || content+panel != 7 {
		t.Fatal("small window lost editor space")
	}
	if err := applySetting("terminal", "height", "14"); err != nil || settings.terminalHeight != 14 {
		t.Fatal("YAML height not applied")
	}
	if err := applySetting("terminal", "height", "-1"); err == nil {
		t.Fatal("invalid height accepted")
	}
}
