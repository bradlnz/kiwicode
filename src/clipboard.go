package main

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type textSelection struct {
	buffer             *buffer
	startRow, startCol int
	endRow, endCol     int
	selecting          bool
}

func (s textSelection) bounds() (startRow, startCol, endRow, endCol int) {
	startRow, startCol, endRow, endCol = s.startRow, s.startCol, s.endRow, s.endCol
	if startRow > endRow || startRow == endRow && startCol > endCol {
		startRow, startCol, endRow, endCol = endRow, endCol, startRow, startCol
	}
	return
}

func (s textSelection) empty() bool {
	return s.buffer == nil || s.startRow == s.endRow && s.startCol == s.endCol
}

func (e *editor) editorPosition(x, y int) (int, int, bool) {
	contentHeight, _ := e.panelHeights()
	side := e.sidebarWidth()
	if y < 3 || y >= 3+contentHeight || x <= side || x > side+e.codeAreaWidth() || e.graph || e.help || e.opsMode != "" {
		return 0, 0, false
	}
	b := e.current()
	gutter := len(strconv.Itoa(len(b.lines))) + 2
	row := min(len(b.lines)-1, b.scrollY+y-3)
	cell := max(0, x-(side+1+gutter)+b.scrollX)
	if e.wordWrap {
		wrapWidth := max(1, e.codeAreaWidth()-gutter-1)
		rows := b.wrappedRows(contentHeight, wrapWidth)
		if y-3 >= len(rows) {
			return 0, 0, false
		}
		row = rows[y-3].row
		cell = rows[y-3].segment*wrapWidth + max(0, x-(side+1+gutter))
	}
	return row, runeColAtCell(b.lines[row], cell), true
}

func (e *editor) updateSelection(x, y int) {
	row, col, ok := e.editorPosition(x, y)
	if !ok || e.selection.buffer != e.current() {
		return
	}
	e.selection.endRow, e.selection.endCol = row, col
	e.current().row, e.current().col = row, col
}

func (e *editor) drawSelection(out *strings.Builder, editorX, contentHeight int) {
	if e.selection.empty() || e.selection.buffer != e.current() || contentHeight == 0 || e.graph || e.help || e.opsMode != "" {
		return
	}
	b := e.current()
	startRow, startCol, endRow, endCol := e.selection.bounds()
	gutter := len(strconv.Itoa(len(b.lines))) + 2
	codeWidth := max(0, e.codeAreaWidth()-gutter)
	if e.wordWrap {
		wrapWidth := max(1, codeWidth-1)
		for screenRow, view := range b.wrappedRows(contentHeight, wrapWidth) {
			if view.row < startRow || view.row > endRow {
				continue
			}
			from, to := 0, len(b.lines[view.row])
			if view.row == startRow {
				from = startCol
			}
			if view.row == endRow {
				to = endCol
			}
			startCell, endCell := cursorCell(b.lines[view.row], from), cursorCell(b.lines[view.row], to)
			expanded := expandLine(b.lines[view.row])
			if view.row < endRow {
				expanded = append(expanded, ' ')
				endCell++
			}
			segmentStart := view.segment * wrapWidth
			left, right := max(startCell, segmentStart), min(endCell, segmentStart+wrapWidth)
			if right > left {
				x := editorX + gutter + 1 + left - segmentStart
				writeCell(out, 3+screenRow, x, ansiBG(colors.menuActive, colors.text, "1")+string(expanded[left:right]))
			}
		}
		return
	}
	for row := startRow; row <= endRow; row++ {
		y := 3 + row - b.scrollY
		if y < 3 || y >= 3+contentHeight {
			continue
		}
		from, to := 0, len(b.lines[row])
		if row == startRow {
			from = startCol
		}
		if row == endRow {
			to = endCol
		}
		startCell, endCell := cursorCell(b.lines[row], from), cursorCell(b.lines[row], to)
		expanded := expandLine(b.lines[row])
		if row < endRow {
			expanded = append(expanded, ' ')
			endCell++
		}
		left := max(startCell, b.scrollX)
		right := min(endCell, b.scrollX+codeWidth)
		if right <= left {
			continue
		}
		x := editorX + gutter + 1 + left - b.scrollX
		writeCell(out, y, x, ansiBG(colors.menuActive, colors.text, "1")+string(expanded[left:right]))
	}
}

func (e *editor) copySelection() {
	text := e.selectedText()
	if text == "" {
		e.status = "Select text by dragging before copying"
		return
	}
	e.clipboard = text
	writeSystemClipboard(text)
	e.status = "Copied selection"
}

func (e *editor) selectedText() string {
	if e.selection.empty() || e.selection.buffer != e.current() {
		return ""
	}
	b := e.current()
	startRow, startCol, endRow, endCol := e.selection.bounds()
	if startRow == endRow {
		return string(b.lines[startRow][startCol:endCol])
	}
	parts := []string{string(b.lines[startRow][startCol:])}
	for row := startRow + 1; row < endRow; row++ {
		parts = append(parts, string(b.lines[row]))
	}
	parts = append(parts, string(b.lines[endRow][:endCol]))
	return strings.Join(parts, "\n")
}

func (e *editor) pasteClipboard() {
	text := readSystemClipboard()
	if text == "" {
		text = e.clipboard
	}
	if text == "" {
		e.status = "Clipboard is empty"
		return
	}
	if len(text) > 2<<20 {
		e.status = "Clipboard is larger than 2 MiB"
		return
	}
	b := e.current()
	b.recordUndo()
	b.suppressUndo = true
	e.deleteSelection()
	b.insertText(text)
	b.suppressUndo = false
	e.selection = textSelection{}
	e.invalidateCompletion()
	e.status = "Pasted clipboard"
}

func (e *editor) deleteSelection() {
	if e.selection.empty() || e.selection.buffer != e.current() {
		return
	}
	b := e.current()
	b.recordUndo()
	startRow, startCol, endRow, endCol := e.selection.bounds()
	if startRow == endRow {
		b.lines[startRow] = append(b.lines[startRow][:startCol], b.lines[startRow][endCol:]...)
	} else {
		line := append([]rune(nil), b.lines[startRow][:startCol]...)
		line = append(line, b.lines[endRow][endCol:]...)
		b.lines = append(b.lines[:startRow], append([][]rune{line}, b.lines[endRow+1:]...)...)
	}
	b.row, b.col, b.dirty = startRow, startCol, true
}

func writeSystemClipboard(text string) {
	name, args := clipboardCommand(true)
	path, err := exec.LookPath(name)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin = strings.NewReader(text)
	_ = cmd.Run()
}

func readSystemClipboard() string {
	name, args := clipboardCommand(false)
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, args...).Output()
	if err != nil {
		return ""
	}
	return strings.ReplaceAll(string(out), "\x00", "")
}

func clipboardCommand(write bool) (string, []string) {
	if _, err := exec.LookPath("wl-copy"); err == nil {
		if write {
			return "wl-copy", nil
		}
		return "wl-paste", []string{"--no-newline"}
	}
	if write {
		return "xclip", []string{"-selection", "clipboard"}
	}
	return "xclip", []string{"-selection", "clipboard", "-o"}
}

func (e *editor) handlePaste(text string) {
	if e.switching != nil {
		return
	}
	if e.terminalFocused() {
		if err := e.shell.terminal.paste(text); err != nil {
			e.status = err.Error()
		}
		return
	}
	if !e.editorFocused() || e.popup != nil || e.searchMode != "" || e.folderPrompt || e.newFilePrompt || e.sourceCommitFocused {
		e.status = "Paste into an editor buffer or the terminal"
		return
	}
	if len(text) > 2<<20 {
		e.status = "Paste is larger than 2 MiB"
		return
	}
	b := e.current()
	b.recordUndo()
	b.suppressUndo = true
	e.deleteSelection()
	b.insertText(text)
	b.suppressUndo = false
	e.selection = textSelection{}
	e.invalidateCompletion()
}
