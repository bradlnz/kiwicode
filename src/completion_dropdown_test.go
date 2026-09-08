package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCompletionDropdown(t *testing.T) {
	root := t.TempDir()
	app := newBuffer(filepath.Join(root, "Handler.cs"), []byte("private readonly ;"))
	app.col = len("private readonly ")
	var source strings.Builder
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&source, "public interface IService%d {}\n", i)
	}
	model := newBuffer(filepath.Join(root, "Interfaces.cs"), []byte(source.String()))
	e := &editor{buffers: []*buffer{app, model}, files: []string{app.path, model.path}, rows: 24, cols: 80}
	e.handle(key{r: 'I'})
	e.completionSuggestions()
	deadline := time.Now().Add(time.Second)
	for !e.pollCompletion() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	names := e.completionSuggestions()
	if len(names) != 10 || !slices.Contains(names, "IService9") {
		t.Fatalf("single-letter project interface suggestions: %v", names)
	}
	for i := 0; i < 9; i++ {
		e.handle(key{code: keyDown})
	}
	e.draw()
	if !strings.Contains(e.lastFrame, "▸ IService9") || app.row != 0 {
		t.Fatal("dropdown did not scroll to the selected interface")
	}
	e.handle(key{code: keyEnter})
	if got := string(app.lines[0]); got != "private readonly IService9;" {
		t.Fatalf("completion before punctuation = %q", got)
	}
	if !app.undoChange() || string(app.lines[0]) != "private readonly I;" {
		t.Fatal("completion was not a single undo step")
	}
	e.handle(key{}) // Escape dismisses until the next edit or cursor move.
	if len(e.completionSuggestions()) != 0 {
		t.Fatal("Escape did not dismiss suggestions")
	}
	e.handle(key{code: keyEnter})
	if len(app.lines) != 2 {
		t.Fatal("Enter after Escape did not insert a newline")
	}
	app.row, app.col = 0, len(app.lines[0])
	e.handle(key{r: 'S'})
	e.handle(key{code: keyUp})
	e.handle(key{code: keyTab})
	if string(app.lines[0]) != "private readonly IService9" {
		t.Fatal("typing did not reopen suggestions or Up/Tab failed")
	}

	// Reuse the same selection behavior for object members.
	app = newBuffer("member.cs", []byte("service."))
	app.col = len(app.lines[0])
	e = &editor{buffers: []*buffer{app}, rows: 5, cols: 30,
		completionCache: map[string]dependencyCompletion{app.path: {
			members:   map[string][]string{"service": {"Read", "Write"}},
			receivers: map[string]string{}, definitions: map[string]definitionLocation{},
		}}}
	e.handle(key{code: keyDown})
	e.draw() // A tiny viewport must keep the dropdown off the status row.
	if !strings.Contains(e.lastFrame, "▸ Write") {
		t.Fatal("selected member missing from the tiny dropdown")
	}
	e.handle(key{code: keyTab})
	if string(app.lines[0]) != "service.Write" {
		t.Fatal("Tab did not accept the selected member")
	}
}
