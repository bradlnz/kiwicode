package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
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
	fmt.Print("\x1b[?1049h\x1b[?25l" + mouseOn)
	defer fmt.Print(mouseOff + "\x1b[?25h\x1b[?1049l")

	e := loadEditorAsync()
	defer func() {
		if e.workspaceDone == nil {
			_ = e.saveState()
		}
	}()
	if configErr != nil {
		e.status = "Config: " + configErr.Error()
	}
	e.resize()
	e.draw()

	for {
		k, ok := readKey()
		if !ok {
			workspaceDone, workspaceErr := e.pollWorkspace()
			if workspaceDone && workspaceErr != nil {
				e.status = "State: " + workspaceErr.Error()
			} else if workspaceDone && configErr != nil {
				e.status = "Config: " + configErr.Error()
			}
			completionDone := e.pollCompletion()
			definitionDone := e.pollDefinition()
			sourceDone := e.pollSourceControl()
			shellDone := e.shell.poll()
			if shellDone && e.sourceRefresh {
				e.sourceRefresh = false
				e.refreshFiles()
				e.reloadCleanBuffers()
			}
			if shellDone && e.debugging {
				e.finishDebug()
			}
			if workspaceDone || shellDone || completionDone || definitionDone || sourceDone {
				e.draw()
			}
			continue
		}
		if e.handle(k) {
			return nil
		}
		e.resize()
		e.draw()
	}
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
	done := make(chan editorLoad, 1)
	*e = editor{
		buffers:       []*buffer{newBuffer(untitledName(), nil)},
		collapsed:     map[string]bool{},
		status:        status,
		rows:          rows,
		cols:          cols,
		workspaceDone: done,
	}
	go func() {
		loaded := newEditor()
		done <- editorLoad{loaded, loaded.restoreState()}
	}()
}

func (e *editor) pollWorkspace() (bool, error) {
	if e.workspaceDone == nil {
		return false, nil
	}
	select {
	case loaded := <-e.workspaceDone:
		rows, cols := e.rows, e.cols
		var edited []*buffer
		for _, b := range e.buffers {
			if b.dirty {
				edited = append(edited, b)
			}
		}
		*e = *loaded.editor
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
