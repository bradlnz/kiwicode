package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Keep at most nine inactive workspaces. Each owns its buffers, tree, undo
// history and worker channels; switching never reconstructs a cached editor.
type cachedWorkspace struct {
	editor   editor
	settings editorSettings
	colors   colorScheme
}

type workspaceSwitch struct {
	done     chan *editor
	started  time.Time
	frame    int
	quitKey  rune
	backdrop string
	colors   colorScheme
}

func (e *editor) workspaceSwitchFrame() string {
	if time.Since(e.switching.started) < 100*time.Millisecond {
		return ""
	}
	s := e.switching
	spinner := "|/-\\"[s.frame%4]
	width, height := min(48, max(1, e.cols-4)), min(5, max(1, e.rows))
	x, y := max(1, (e.cols-width)/2+1), max(1, (e.rows-height)/2+1)
	var out strings.Builder
	// Replay the last rendered frame, not the live editor owned by the worker.
	out.WriteString(s.backdrop)
	out.WriteString("\x1b[0m\x1b[?25l")
	if width < 4 || height < 5 {
		writeCell(&out, y, x, ansiBG(s.colors.menu, s.colors.accent, "1")+fit("Switching…", width))
		return out.String() + "\x1b[0m"
	}
	lines := []string{
		"╭" + strings.Repeat("─", width-2) + "╮",
		"│" + strings.Repeat(" ", width-2) + "│",
		"│" + fit(fmt.Sprintf(" %c Switching project…", spinner), width-2) + "│",
		"│" + strings.Repeat(" ", width-2) + "│",
		"╰" + strings.Repeat("─", width-2) + "╯",
	}
	for row, line := range lines {
		writeCell(&out, y+row, x, ansiBG(s.colors.menu, s.colors.accent, "1")+line)
	}
	return out.String() + "\x1b[0m"
}

func workspacePath(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}

func (e *editor) switchWorkspace(path string, create bool) {
	if e.switching != nil {
		return
	}
	if e.workspaceDone != nil {
		e.status = "Workspace is still loading"
		return
	}
	if e.shell.running {
		e.status = "Stop the running command before opening a folder"
		return
	}
	transition := &workspaceSwitch{done: make(chan *editor, 1), started: time.Now(), quitKey: 17, backdrop: e.lastFrame, colors: colors}
	for k, action := range settings.bindings {
		if action == "quit" {
			transition.quitKey = k
		}
	}
	// Transfer ownership of the live state to the worker. Until it returns,
	// the UI only renders the loader and handles resize/quit; it must not read
	// workspace data or settings that the worker is restoring.
	worker := *e
	e.switching = transition
	go func() {
		worker.switchWorkspaceNow(path, create)
		worker.visibleTree() // Build the first explorer layout off the UI thread.
		transition.done <- &worker
	}()
}

func (e *editor) pollWorkspaceSwitch() bool {
	s := e.switching
	select {
	case loaded := <-s.done:
		e.finishWorkspaceSwitch(loaded)
		return true
	default:
		frame := int(time.Since(s.started) / (100 * time.Millisecond))
		if frame != s.frame {
			s.frame = frame
			return true
		}
		return false
	}
}

func (e *editor) workspaceSwitchEvents() <-chan *editor {
	if e.switching == nil {
		return nil
	}
	return e.switching.done
}

func (e *editor) finishWorkspaceSwitch(loaded *editor) {
	rows, cols := e.rows, e.cols
	*e = *loaded
	e.rows, e.cols = rows, cols
}

func (e *editor) switchWorkspaceNow(path string, create bool) {
	path = expandFolderPath(strings.TrimSpace(path))
	path, err := filepath.Abs(path)
	if err != nil {
		e.status = "Open folder failed: " + err.Error()
		return
	}
	current := mustCwd()
	if path == current && !create {
		return
	}
	info, err := os.Stat(path)
	if create {
		if err == nil {
			e.status = "Project path already exists; use Open Folder or choose a new path"
			return
		}
		if !os.IsNotExist(err) {
			e.status = "New project failed: " + err.Error()
			return
		}
	} else if err != nil || !info.IsDir() {
		e.status = "Folder not found: " + path
		return
	}
	cache := e.workspaces
	if cache == nil {
		cache = make(map[string]cachedWorkspace)
	}
	next, warm := cache[path]
	if create {
		warm = false
	}
	data, err := e.stateJSON()
	if err != nil {
		e.status = "Cache workspace failed: " + err.Error()
		return
	}
	if e.checkpoint == nil {
		e.checkpoint = newWorkspaceCheckpoint(current, stateDatabasePath())
	}
	e.checkpoint.queue(data)
	if !warm {
		if err := e.checkpoint.flush(); err != nil {
			e.status = "Cache workspace failed: " + err.Error()
			return
		}
	}
	// Eviction may wait for persistence; ordinary switches among cached slots
	// never evict or wait for either project's checkpoint.
	if !warm && len(cache) >= maxProjectSlots {
		for root, old := range cache {
			if old.editor.checkpoint != nil {
				if err := old.editor.checkpoint.flush(); err != nil {
					e.status = "Cache workspace failed: " + err.Error()
					return
				}
				_ = old.editor.checkpoint.close()
			}
			old.editor.shell.terminal.close()
			delete(cache, root)
			break
		}
	}
	if create {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			e.status = "New project failed: " + err.Error()
			return
		}
		if err := os.Mkdir(path, 0755); err != nil {
			e.status = "New project failed: " + err.Error()
			return
		}
	}
	if err := os.Chdir(path); err != nil {
		e.status = "Open folder failed: " + err.Error()
		return
	}
	delete(cache, path)
	parked := *e
	parked.workspaces = nil
	parked.popup, parked.folderPrompt, parked.searchMode = nil, false, ""
	parked.newProjectPrompt = false
	parked.selection.selecting = false
	parked.quitArmed, parked.closeArmed = false, nil
	cache[current] = cachedWorkspace{parked, settings, colors}
	rows, cols, slots := e.rows, e.cols, e.projectSlots
	if warm {
		*e = next.editor
		settings, colors = next.settings, next.colors
		e.rows, e.cols, e.projectSlots = rows, cols, slots
		e.projectSlot = slices.Index(slots, path)
		if e.projectSlot >= settings.projectQuickPicks {
			e.projectSlot = -1
		}
		e.workspaces = cache
		e.status = "Project: " + path
		return
	}
	configErr := loadSettings()
	loaded := newEditor()
	var stateErr error
	if create {
		loaded.showExplorer, loaded.explorer = true, true
		loaded.status = "Created project: " + path
	} else {
		stateErr = loaded.restoreState()
	}
	loaded.workspaces = cache
	*e = *loaded
	e.rows, e.cols = rows, cols
	if e.restoredTheme != "" {
		setColorScheme(strings.ToLower(e.restoredTheme))
	}
	if configErr != nil {
		e.status = "Config: " + configErr.Error()
	} else if stateErr != nil {
		e.status = "State: " + stateErr.Error()
	}
}

func (e *editor) closeWorkspaces() {
	if e.switching != nil {
		*e = *<-e.switching.done
	}
	e.shell.terminal.close()
	if e.checkpoint != nil {
		_ = e.checkpoint.close() // Drain older writes before the final snapshot.
	}
	if e.workspaceDone == nil {
		if err := e.saveState(); err != nil {
			fmt.Fprintln(os.Stderr, "Workspace:", err)
		}
	}
	for _, cached := range e.workspaces {
		if cached.editor.checkpoint != nil {
			if err := cached.editor.checkpoint.close(); err != nil {
				fmt.Fprintln(os.Stderr, "Workspace:", err)
			}
		}
		cached.editor.shell.terminal.close()
	}
}
