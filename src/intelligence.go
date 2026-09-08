package main

import (
	"bytes"
	"encoding/json"
	"go/format"
	"path/filepath"
	"strings"
)

func shellArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func (e *editor) formatCurrent() {
	b := e.current()
	input := []byte(strings.Join(runeLines(b.lines), "\n"))
	formatted, err := formatCode(b.path, input)
	if err != nil {
		e.status = "Format failed: " + err.Error()
		return
	}
	if bytes.Equal(input, formatted) {
		e.status = "Already formatted"
		return
	}
	b.recordUndo()
	fresh := newBuffer(b.path, formatted)
	b.lines = fresh.lines
	b.row = min(b.row, len(b.lines)-1)
	b.col = min(b.col, len(b.lines[b.row]))
	b.dirty = true
	e.invalidateCompletion()
	e.status = "Formatted " + b.path
}

func formatCode(path string, input []byte) ([]byte, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return format.Source(input)
	case ".json":
		var out bytes.Buffer
		if err := json.Indent(&out, bytes.TrimSpace(input), "", "  "); err != nil {
			return nil, err
		}
		return append(out.Bytes(), '\n'), nil
	default:
		lines := strings.Split(string(input), "\n")
		for i := range lines {
			lines[i] = strings.TrimRight(lines[i], " \t")
		}
		return []byte(strings.Join(lines, "\n")), nil
	}
}
