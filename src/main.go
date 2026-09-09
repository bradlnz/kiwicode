package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "code-editor:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 && os.Args[1] == "--mcp" {
		root := "."
		if len(os.Args) > 2 {
			root = os.Args[2]
		}
		return runMCP(root)
	}
	if len(os.Args) > 1 {
		if err := os.Chdir(os.Args[1]); err != nil {
			return err
		}
	}
	configErr := loadSettings()
	state, err := stty("-g")
	if err != nil {
		return fmt.Errorf("a terminal is required")
	}
	if _, err := stty("raw", "-echo", "min", "0", "time", "1"); err != nil {
		return err
	}
	defer stty(strings.TrimSpace(string(state)))
	fmt.Print("\x1b[?1049h\x1b[?25l" + mouseOn + bracketedPasteOn)
	defer fmt.Print(bracketedPasteOff + mouseOff + "\x1b[?25h\x1b[?1049l")

	e := loadEditorAsync()
	defer e.closeWorkspaces()
	if configErr != nil {
		e.status = "Config: " + configErr.Error()
	}
	resized := make(chan os.Signal, 1)
	signal.Notify(resized, syscall.SIGWINCH)
	defer signal.Stop(resized)
	e.resize()
	e.draw()

	ctx, cancel := context.WithCancel(context.Background())
	input, inputDone := readInput(ctx, os.Stdin)
	defer func() { cancel(); <-inputDone }()
	maintenance := time.NewTicker(50 * time.Millisecond)
	defer maintenance.Stop()
	// At most one scheduled frame. Never reset the deadline when new data
	// arrives: continuous token/input traffic must not postpone rendering.
	var frames frameSchedule
	defer frames.stop()
	for {
		changed := false
		select {
		case event, ok := <-input:
			if !ok {
				return nil
			}
			if event.err != nil {
				e.status = event.err.Error()
			} else if event.isPaste {
				e.handlePaste(event.paste)
			} else if e.handle(event.key) {
				return nil
			}
			changed = true
		case <-resized:
			e.resize() // only startup and actual resize notifications
			changed = true
		case loaded := <-e.workspaceSwitchEvents():
			e.finishWorkspaceSwitch(loaded)
			changed = true
		case event, ok := <-e.terminalEvents():
			changed = e.receiveTerminal(event, ok)
		case <-maintenance.C:
			changed = e.pollEditor(configErr)
		case <-frames.channel():
			frames.fired()
			e.draw()
		}
		if changed {
			frames.request()
		}
	}
}

const frameInterval = 16 * time.Millisecond

type frameSchedule struct {
	timer   *time.Timer
	pending bool
	last    time.Time
}

func (f *frameSchedule) request() {
	if f.pending {
		return
	}
	delay := max(time.Duration(0), time.Until(f.last.Add(frameInterval)))
	if f.timer == nil {
		f.timer = time.NewTimer(delay)
	} else {
		f.timer.Reset(delay)
	}
	f.pending = true
}
func (f *frameSchedule) channel() <-chan time.Time {
	if !f.pending {
		return nil
	}
	return f.timer.C
}
func (f *frameSchedule) fired() { f.pending = false; f.last = time.Now() }
func (f *frameSchedule) stop() {
	if f.timer != nil {
		f.timer.Stop()
	}
}

func (e *editor) pollEditor(configErr error) bool {
	if e.switching != nil {
		return e.pollWorkspaceSwitch()
	}
	checkpointChanged := e.pollWorkspaceCheckpoints()
	canvasChanged := e.pollNodeCanvases()
	workspaceDone, workspaceErr := e.pollWorkspace()
	// Start the default dock only after loading, with the real viewport size.
	terminalStarted := e.workspaceDone == nil && e.shell.open && e.shell.interactive && e.shell.terminal == nil
	if terminalStarted {
		e.openTerminal()
	}
	if workspaceDone && workspaceErr != nil {
		e.status = "State: " + workspaceErr.Error()
	} else if workspaceDone && configErr != nil {
		e.status = "Config: " + configErr.Error()
	}
	completionDone := e.pollCompletion()
	languageDone := e.pollLanguages()
	definitionDone := e.pollDefinition()
	sourceDone := e.pollSourceControl()
	filesChanged := e.pollFileTree()
	shellDone := e.shell.poll()
	if shellDone && e.sourceRefresh {
		e.sourceRefresh = false
		e.refreshFiles()
		e.reloadCleanBuffers()
	}
	return filesChanged || terminalStarted || canvasChanged || checkpointChanged || workspaceDone || shellDone || completionDone || languageDone || definitionDone || sourceDone
}

type editorLoad struct {
	editor *editor
	err    error
}

func loadEditorAsync() *editor {
	e := &editor{}
	e.startWorkspaceLoad("Loading workspace…")
	return e
}
func (e *editor) startWorkspaceLoad(status string) {
	rows, cols := e.rows, e.cols
	projectSlots, projectSlot := e.projectSlots, e.projectSlot
	done := make(chan editorLoad, 1)
	*e = editor{buffers: []*buffer{newBuffer(untitledName(), nil)}, collapsed: map[string]bool{}, status: status, rows: rows, cols: cols, workspaceDone: done, workspaces: e.workspaces, projectSlots: projectSlots, projectSlot: projectSlot}
	go func() {
		loaded := newEditor()
		err := loaded.restoreState()
		done <- editorLoad{loaded, err}
	}()
}
func (e *editor) pollWorkspace() (bool, error) {
	if e.workspaceDone == nil {
		return false, nil
	}
	select {
	case loaded := <-e.workspaceDone:
		rows, cols := e.rows, e.cols
		cache := e.workspaces
		var edited []*buffer
		for _, b := range e.buffers {
			if b.dirty {
				edited = append(edited, b)
			}
		}
		*e = *loaded.editor
		e.workspaces = cache
		e.rows, e.cols = rows, cols
		if e.restoredTheme != "" && !strings.EqualFold(e.restoredTheme, colors.name) {
			setColorScheme(strings.ToLower(e.restoredTheme))
		}
		if len(edited) > 0 {
			e.buffers = append(e.buffers, edited...)
			e.active = len(e.buffers) - 1
		}
		return true, loaded.err
	default:
		return false, nil
	}
}
func stty(args ...string) ([]byte, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = os.Stdin
	return cmd.Output()
}
