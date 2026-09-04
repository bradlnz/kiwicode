package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type persistedState struct {
	Buffers         []persistedBuffer              `json:"buffers"`
	ActivePath      string                         `json:"active_path"`
	Collapsed       map[string]bool                `json:"collapsed"`
	TestChecks      map[string]bool                `json:"test_checks"`
	ShowExplorer    bool                           `json:"show_explorer"`
	Explorer        bool                           `json:"explorer"`
	TestMode        bool                           `json:"test_mode"`
	SourceMode      bool                           `json:"source_mode"`
	Selected        int                            `json:"selected"`
	ExplorerTop     int                            `json:"explorer_top"`
	TestSelected    int                            `json:"test_selected"`
	TestTop         int                            `json:"test_top"`
	SourceSelected  int                            `json:"source_selected"`
	SourceTop       int                            `json:"source_top"`
	Theme           string                         `json:"theme"`
	WordWrap        bool                           `json:"word_wrap"`
	TerminalInput   string                         `json:"terminal_input"`
	TerminalOutput  []string                       `json:"terminal_output"`
	TerminalHistory []string                       `json:"terminal_history"`
	CompletionCache map[string]persistedCompletion `json:"completion_cache,omitempty"`
}

type persistedCompletion struct {
	Words       []string                       `json:"words"`
	Members     map[string][]string            `json:"members"`
	Definitions map[string]persistedDefinition `json:"definitions"`
}

type persistedDefinition struct {
	Path string `json:"path"`
	Row  int    `json:"row"`
}

type persistedBuffer struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Newline string `json:"newline"`
	Row     int    `json:"row"`
	Col     int    `json:"col"`
	ScrollY int    `json:"scroll_y"`
	ScrollX int    `json:"scroll_x"`
	Dirty   bool   `json:"dirty"`
}

func stateDatabasePath() string {
	root := os.Getenv("XDG_STATE_HOME")
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".local", "state")
	}
	workspace, _ := filepath.Abs(".")
	hash := sha256.Sum256([]byte(workspace))
	return filepath.Join(root, "code-editor", base64.RawURLEncoding.EncodeToString(hash[:12])+".db")
}

func (e *editor) saveState() error {
	state := persistedState{
		Collapsed: e.collapsed, ShowExplorer: e.showExplorer, Explorer: e.explorer,
		TestMode: e.testMode, SourceMode: e.sourceMode, Selected: e.selected, ExplorerTop: e.explorerTop,
		TestSelected: e.testSelected, TestTop: e.testTop, SourceSelected: e.sourceSelected, SourceTop: e.sourceTop,
		Theme: colors.name, WordWrap: e.wordWrap, TerminalInput: string(e.shell.input), TerminalOutput: e.shell.output, TerminalHistory: e.shell.history,
		TestChecks: map[string]bool{}, CompletionCache: map[string]persistedCompletion{},
	}
	for path, completion := range e.completionCache {
		definitions := make(map[string]persistedDefinition, len(completion.definitions))
		for name, definition := range completion.definitions {
			definitions[name] = persistedDefinition{definition.path, definition.row}
		}
		state.CompletionCache[path] = persistedCompletion{
			Words: completion.words, Members: completion.members, Definitions: definitions,
		}
	}
	if len(e.buffers) > 0 {
		state.ActivePath = e.current().path
	}
	for _, b := range e.buffers {
		state.Buffers = append(state.Buffers, persistedBuffer{
			Path: b.path, Content: strings.Join(runeLines(b.lines), b.newline), Newline: b.newline,
			Row: b.row, Col: b.col, ScrollY: b.scrollY, ScrollX: b.scrollX, Dirty: b.dirty,
		})
	}
	for _, test := range e.tests {
		state.TestChecks[test.path+"\x00"+test.name] = test.checked
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(stateDatabasePath()), 0755); err != nil {
		return err
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	sql := "CREATE TABLE IF NOT EXISTS editor_state (id INTEGER PRIMARY KEY, snapshot TEXT NOT NULL);" +
		"INSERT INTO editor_state(id,snapshot) VALUES(1,'" + encoded + "') ON CONFLICT(id) DO UPDATE SET snapshot=excluded.snapshot;"
	cmd := exec.Command("sqlite3", "-batch", stateDatabasePath())
	cmd.Stdin = strings.NewReader(sql)
	return cmd.Run()
}

func (e *editor) restoreState() error {
	if _, err := os.Stat(stateDatabasePath()); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	out, err := exec.Command("sqlite3", "-batch", "-noheader", stateDatabasePath(), "SELECT snapshot FROM editor_state WHERE id=1;").Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return err
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
	if err != nil {
		return err
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if e.collapsed == nil {
		e.collapsed = map[string]bool{}
	}
	for path, collapsed := range state.Collapsed {
		e.collapsed[path] = collapsed
	}
	for index := range e.tests {
		if checked, ok := state.TestChecks[e.tests[index].path+"\x00"+e.tests[index].name]; ok {
			e.tests[index].checked = checked
		}
	}
	e.buffers = nil
	for _, saved := range state.Buffers {
		content := []byte(saved.Content)
		fresh, readErr := os.ReadFile(saved.Path)
		if !saved.Dirty && readErr != nil {
			continue
		}
		if !saved.Dirty || readErr == nil && string(fresh) == saved.Content {
			content = fresh
			saved.Dirty = false
		}
		b := newBuffer(saved.Path, content)
		b.row = min(max(0, saved.Row), len(b.lines)-1)
		b.col = min(max(0, saved.Col), len(b.lines[b.row]))
		b.scrollY, b.scrollX, b.dirty = max(0, saved.ScrollY), max(0, saved.ScrollX), saved.Dirty
		if saved.Newline != "" {
			b.newline = saved.Newline
		}
		e.buffers = append(e.buffers, b)
		if saved.Path == state.ActivePath {
			e.active = len(e.buffers) - 1
		}
	}
	if len(e.buffers) == 0 {
		if len(e.files) > 0 {
			e.open(e.files[0])
		} else {
			e.buffers = []*buffer{newBuffer(untitledName(), nil)}
		}
	}
	e.showExplorer, e.explorer, e.testMode, e.sourceMode = state.ShowExplorer, state.Explorer, state.TestMode, state.SourceMode
	e.selected, e.explorerTop = state.Selected, state.ExplorerTop
	e.testSelected, e.testTop, e.sourceSelected, e.sourceTop = state.TestSelected, state.TestTop, state.SourceSelected, state.SourceTop
	e.shell.input, e.shell.output, e.shell.history = []rune(state.TerminalInput), state.TerminalOutput, state.TerminalHistory
	if len(state.CompletionCache) > 0 {
		e.completionCache = make(map[string]dependencyCompletion, len(state.CompletionCache))
		for path, completion := range state.CompletionCache {
			var definitions map[string]definitionLocation
			if completion.Definitions != nil {
				definitions = make(map[string]definitionLocation, len(completion.Definitions))
				for name, definition := range completion.Definitions {
					definitions[name] = definitionLocation{definition.Path, definition.Row}
				}
			}
			e.completionCache[path] = dependencyCompletion{
				words: completion.Words, members: completion.Members, definitions: definitions,
			}
		}
	}
	e.wordWrap = state.WordWrap
	e.restoredTheme = state.Theme
	if e.sourceMode {
		e.loadSourceControl()
	}
	e.loadMembersInBackground(e.current().path)
	return nil
}
