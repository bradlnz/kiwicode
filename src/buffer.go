package main

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const (
	keyUp = iota + 1
	keyDown
	keyLeft
	keyRight
	keyHome
	keyEnd
	keyPageUp
	keyPageDown
	keyDelete
	keyEnter
	keyBackspace
	keyTab
)

type key struct {
	raw            string
	code           int
	r              rune
	alt            bool
	mouse, release bool
	button, x, y   int
}

type buffer struct {
	path             string
	lines            [][]rune
	row, col         int
	scrollY, scrollX int
	wrapSegment      int
	dirty            bool
	newline          string
	undo             []bufferSnapshot
	suppressUndo     bool
}

type bufferSnapshot struct {
	// replaced < 0 denotes a full-document snapshot for compound edits.
	// Otherwise lines restores the range beginning at start after removing
	// replaced lines from the edited buffer.
	start, replaced            int
	lines                      [][]rune
	row, col, scrollY, scrollX int
	wrapSegment                int
	dirty                      bool
}

func newBuffer(path string, data []byte) *buffer {
	newline := "\n"
	if strings.Contains(string(data), "\r\n") {
		newline = "\r\n"
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	parts := strings.Split(text, "\n")
	lines := make([][]rune, len(parts))
	for i := range parts {
		lines[i] = []rune(parts[i])
	}
	return &buffer{path: path, lines: lines, newline: newline}
}

func runeLines(lines [][]rune) []string {
	out := make([]string, len(lines))
	for i := range lines {
		out[i] = string(lines[i])
	}
	return out
}

func (b *buffer) handle(k key) {
	switch k.code {
	case keyUp:
		if b.row > 0 {
			b.row--
			b.clampCol()
		}
	case keyDown:
		if b.row+1 < len(b.lines) {
			b.row++
			b.clampCol()
		}
	case keyLeft:
		if b.col > 0 {
			b.col--
		} else if b.row > 0 {
			b.row--
			b.col = len(b.lines[b.row])
		}
	case keyRight:
		if b.col < len(b.lines[b.row]) {
			b.col++
		} else if b.row+1 < len(b.lines) {
			b.row++
			b.col = 0
		}
	case keyHome:
		b.col = 0
	case keyEnd:
		b.col = len(b.lines[b.row])
	case keyPageUp:
		b.row -= 10
		if b.row < 0 {
			b.row = 0
		}
		b.clampCol()
	case keyPageDown:
		b.row += 10
		if b.row >= len(b.lines) {
			b.row = len(b.lines) - 1
		}
		b.clampCol()
	case keyEnter:
		b.recordUndoRange(b.row, b.row+1, 2)
		before := b.lines[b.row][:b.col]
		indent := before[:len(before)-len([]rune(strings.TrimLeft(string(before), " \t")))]
		if strings.ContainsRune("{[(:", lastNonSpace(before)) {
			indent = append(append([]rune(nil), indent...), []rune("    ")...)
		}
		rest := append([]rune(nil), b.lines[b.row][b.col:]...)
		b.lines[b.row] = b.lines[b.row][:b.col]
		b.lines = append(b.lines, nil)
		copy(b.lines[b.row+2:], b.lines[b.row+1:])
		b.lines[b.row+1] = append(append([]rune(nil), indent...), rest...)
		b.row++
		b.col = len(indent)
		b.dirty = true
	case keyBackspace:
		if b.col > 0 {
			b.recordUndoRange(b.row, b.row+1, 1)
			line := b.lines[b.row]
			b.lines[b.row] = append(line[:b.col-1], line[b.col:]...)
			b.col--
			b.dirty = true
		} else if b.row > 0 {
			b.recordUndoRange(b.row-1, b.row+1, 1)
			previous := len(b.lines[b.row-1])
			b.lines[b.row-1] = append(b.lines[b.row-1], b.lines[b.row]...)
			b.lines = append(b.lines[:b.row], b.lines[b.row+1:]...)
			b.row--
			b.col = previous
			b.dirty = true
		}
	case keyDelete:
		if b.col < len(b.lines[b.row]) {
			b.recordUndoRange(b.row, b.row+1, 1)
			line := b.lines[b.row]
			b.lines[b.row] = append(line[:b.col], line[b.col+1:]...)
			b.dirty = true
		} else if b.row+1 < len(b.lines) {
			b.recordUndoRange(b.row, b.row+2, 1)
			b.lines[b.row] = append(b.lines[b.row], b.lines[b.row+1]...)
			b.lines = append(b.lines[:b.row+1], b.lines[b.row+2:]...)
			b.dirty = true
		}
	case keyTab:
		b.insert([]rune("    "))
	default:
		if k.r >= 32 && k.r != 127 {
			b.insert([]rune{k.r})
		}
	}
}

func lastNonSpace(line []rune) rune {
	for i := len(line) - 1; i >= 0; i-- {
		if line[i] != ' ' && line[i] != '\t' {
			return line[i]
		}
	}
	return 0
}

func (b *buffer) insert(chars []rune) {
	if len(chars) == 0 {
		return
	}
	b.recordUndoRange(b.row, b.row+1, 1)
	line := b.lines[b.row]
	line = append(line, make([]rune, len(chars))...)
	copy(line[b.col+len(chars):], line[b.col:])
	copy(line[b.col:], chars)
	b.lines[b.row] = line
	b.col += len(chars)
	b.dirty = true
}

func (b *buffer) insertText(text string) {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	parts := strings.Split(text, "\n")
	if len(parts) == 1 {
		b.insert([]rune(text))
		return
	}
	b.recordUndoRange(b.row, b.row+1, len(parts))
	before := append([]rune(nil), b.lines[b.row][:b.col]...)
	after := append([]rune(nil), b.lines[b.row][b.col:]...)
	replacement := make([][]rune, len(parts))
	replacement[0] = append(before, []rune(parts[0])...)
	for i := 1; i+1 < len(parts); i++ {
		replacement[i] = []rune(parts[i])
	}
	replacement[len(parts)-1] = append([]rune(parts[len(parts)-1]), after...)
	tail := append([][]rune(nil), b.lines[b.row+1:]...)
	b.lines = append(b.lines[:b.row], replacement...)
	b.lines = append(b.lines, tail...)
	b.row += len(parts) - 1
	b.col = len([]rune(parts[len(parts)-1]))
	b.dirty = true
}

// recordUndo preserves the full-snapshot API used by compound editor operations
// (selection replacement, clipboard paste and formatting). Ordinary buffer edits
// use recordUndoRange so typing does not copy every line in the document.
func (b *buffer) recordUndo() {
	b.recordUndoRange(0, len(b.lines), -1)
}

// recordUndoRange records [start, end) before an edit replaces it with
// replacementCount lines. The saved runes are owned by this entry: subsequent
// in-place edits cannot mutate earlier undo history.
func (b *buffer) recordUndoRange(start, end, replacementCount int) {
	if b.suppressUndo {
		return
	}
	lines := make([][]rune, end-start)
	for index := range lines {
		lines[index] = append([]rune(nil), b.lines[start+index]...)
	}
	snapshot := bufferSnapshot{
		start: start, replaced: replacementCount, lines: lines,
		row: b.row, col: b.col, scrollY: b.scrollY, scrollX: b.scrollX,
		wrapSegment: b.wrapSegment, dirty: b.dirty,
	}
	if len(b.undo) == 50 {
		// Reuse the bounded history rather than retaining discarded entries in
		// a backing array before the start of a subslice.
		copy(b.undo, b.undo[1:])
		b.undo[len(b.undo)-1] = snapshot
	} else {
		b.undo = append(b.undo, snapshot)
	}
}

func (b *buffer) undoChange() bool {
	if len(b.undo) == 0 {
		return false
	}
	index := len(b.undo) - 1
	last := b.undo[index]
	b.undo[index] = bufferSnapshot{} // Release popped history for GC.
	b.undo = b.undo[:index]
	if last.replaced < 0 {
		b.lines = last.lines
	} else if len(last.lines) == last.replaced {
		// The common typing/deletion case does not move untouched line headers.
		copy(b.lines[last.start:], last.lines)
	} else {
		oldLen := len(b.lines)
		newLen := oldLen - last.replaced + len(last.lines)
		if newLen > oldLen {
			b.lines = append(b.lines, make([][]rune, newLen-oldLen)...)
		}
		copy(b.lines[last.start+len(last.lines):], b.lines[last.start+last.replaced:oldLen])
		copy(b.lines[last.start:], last.lines)
		if newLen < oldLen {
			clear(b.lines[newLen:])
			b.lines = b.lines[:newLen]
		}
	}
	b.row, b.col = last.row, last.col
	b.scrollY, b.scrollX, b.wrapSegment, b.dirty = last.scrollY, last.scrollX, last.wrapSegment, last.dirty
	return true
}

func (b *buffer) suggestion() []rune { return b.suggestionWith(nil) }

func (b *buffer) suggestionWith(extra []string) []rune {
	candidates := b.wordCandidates(extra)
	if len(candidates) == 0 {
		return nil
	}
	return []rune(candidates[0])[len([]rune(b.completionPrefix())):]
}

func (b *buffer) completionPrefix() string {
	line := b.lines[b.row]
	if b.col < 0 || b.col > len(line) || b.col < len(line) && identifierRune(line[b.col]) {
		return ""
	}
	start := b.col
	for start > 0 && identifierRune(line[start-1]) {
		start--
	}
	return string(line[start:b.col])
}

func (b *buffer) wordCandidates(extra []string) []string {
	prefix := b.completionPrefix()
	if prefix == "" {
		return nil
	}
	words := map[string]bool{}
	consider := func(word string) {
		if word != prefix && strings.HasPrefix(word, prefix) {
			words[word] = true
		}
	}
	if syntax, ok := languageSyntaxes[strings.ToLower(filepath.Ext(b.path))]; ok {
		for word := range syntax.keywords {
			consider(word)
		}
	}
	for _, source := range b.lines {
		for _, word := range strings.FieldsFunc(string(source), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
		}) {
			consider(word)
		}
	}
	for _, word := range extra {
		consider(word)
	}
	var matches []string
	for word := range words {
		matches = append(matches, word)
	}
	sort.Slice(matches, func(i, j int) bool {
		return len(matches[i]) < len(matches[j]) || len(matches[i]) == len(matches[j]) && matches[i] < matches[j]
	})
	return matches
}

func (b *buffer) clampCol() {
	if b.col > len(b.lines[b.row]) {
		b.col = len(b.lines[b.row])
	}
}

func (b *buffer) ensureVisible(height, width int) {
	if b.row < b.scrollY {
		b.scrollY = b.row
	}
	if b.row >= b.scrollY+height {
		b.scrollY = b.row - height + 1
	}
	gutter := len(strconv.Itoa(len(b.lines))) + 2
	visible := width - gutter - 1
	if visible < 1 {
		visible = 1
	}
	cell := cursorCell(b.lines[b.row], b.col)
	if cell < b.scrollX {
		b.scrollX = cell
	}
	if cell >= b.scrollX+visible {
		b.scrollX = cell - visible + 1
	}
}

type codeViewRow struct{ row, segment int }

func wrapCount(line []rune, width int) int {
	return max(1, (expandedCellWidth(line)+width-1)/width)
}

func (b *buffer) cursorViewRow(width int) codeViewRow {
	cell := cursorCell(b.lines[b.row], b.col)
	if cell > 0 {
		cell--
	}
	return codeViewRow{b.row, cell / width}
}

func (b *buffer) previousViewRow(position codeViewRow, width int) (codeViewRow, bool) {
	if position.segment > 0 {
		position.segment--
		return position, true
	}
	if position.row == 0 {
		return position, false
	}
	position.row--
	position.segment = wrapCount(b.lines[position.row], width) - 1
	return position, true
}

func (b *buffer) nextViewRow(position codeViewRow, width int) (codeViewRow, bool) {
	if position.segment+1 < wrapCount(b.lines[position.row], width) {
		position.segment++
		return position, true
	}
	if position.row+1 == len(b.lines) {
		return position, false
	}
	return codeViewRow{row: position.row + 1}, true
}

func (b *buffer) ensureWrappedVisible(height, width int) {
	b.scrollX = 0
	top := codeViewRow{min(b.scrollY, len(b.lines)-1), b.wrapSegment}
	top.segment = min(top.segment, wrapCount(b.lines[top.row], width)-1)
	cursor := b.cursorViewRow(width)
	visible := false
	position := top
	for range max(1, height) {
		if position == cursor {
			visible = true
			break
		}
		var ok bool
		position, ok = b.nextViewRow(position, width)
		if !ok {
			break
		}
	}
	if cursor.row < top.row || cursor.row == top.row && cursor.segment < top.segment {
		top = cursor
	} else if !visible {
		top = cursor
		for range max(0, height-1) {
			previous, ok := b.previousViewRow(top, width)
			if !ok {
				break
			}
			top = previous
		}
	}
	b.scrollY, b.wrapSegment = top.row, top.segment
}

func (b *buffer) wrappedRows(height, width int) []codeViewRow {
	position := codeViewRow{b.scrollY, b.wrapSegment}
	rows := make([]codeViewRow, 0, height)
	for range height {
		rows = append(rows, position)
		next, ok := b.nextViewRow(position, width)
		if !ok {
			break
		}
		position = next
	}
	return rows
}

func cursorCell(line []rune, col int) int {
	return expandedCellWidth(line[:col])
}

// expandedCellWidth mirrors expandLine's four-cell tab stops and current
// one-cell-per-rune convention, without allocating a rendered copy of the line.
func expandedCellWidth(line []rune) int {
	cell := 0
	for _, r := range line {
		if r == '\t' {
			cell += 4 - cell%4
		} else {
			cell++
		}
	}
	return cell
}

func runeColAtCell(line []rune, target int) int {
	cell := 0
	for i, r := range line {
		width := 1
		if r == '\t' {
			width = 4 - cell%4
		}
		if target < cell+width {
			return i
		}
		cell += width
	}
	return len(line)
}

func expandLine(line []rune) []rune {
	var out []rune
	for _, r := range line {
		if r == '\t' {
			out = append(out, []rune(strings.Repeat(" ", 4-len(out)%4))...)
		} else if r < 32 || r == 127 {
			out = append(out, '·')
		} else {
			out = append(out, r)
		}
	}
	// ponytail: runes count as one cell; add a width library when CJK alignment matters.
	return out
}
