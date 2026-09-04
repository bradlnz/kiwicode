package main

import (
	"path/filepath"
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
	code           int
	r              rune
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
		b.recordUndo()
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
			b.recordUndo()
			line := b.lines[b.row]
			b.lines[b.row] = append(line[:b.col-1], line[b.col:]...)
			b.col--
			b.dirty = true
		} else if b.row > 0 {
			b.recordUndo()
			previous := len(b.lines[b.row-1])
			b.lines[b.row-1] = append(b.lines[b.row-1], b.lines[b.row]...)
			b.lines = append(b.lines[:b.row], b.lines[b.row+1:]...)
			b.row--
			b.col = previous
			b.dirty = true
		}
	case keyDelete:
		if b.col < len(b.lines[b.row]) {
			b.recordUndo()
			line := b.lines[b.row]
			b.lines[b.row] = append(line[:b.col], line[b.col+1:]...)
			b.dirty = true
		} else if b.row+1 < len(b.lines) {
			b.recordUndo()
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
	b.recordUndo()
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
	b.recordUndo()
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

func (b *buffer) recordUndo() {
	if b.suppressUndo {
		return
	}
	lines := make([][]rune, len(b.lines))
	for index := range b.lines {
		lines[index] = append([]rune(nil), b.lines[index]...)
	}
	b.undo = append(b.undo, bufferSnapshot{lines, b.row, b.col, b.scrollY, b.scrollX, b.wrapSegment, b.dirty})
	if len(b.undo) > 50 {
		b.undo = b.undo[len(b.undo)-50:]
	}
	// ponytail: snapshots keep undo reliable; replace with edit operations if large-file profiling warrants it.
}

func (b *buffer) undoChange() bool {
	if len(b.undo) == 0 {
		return false
	}
	last := b.undo[len(b.undo)-1]
	b.undo = b.undo[:len(b.undo)-1]
	b.lines, b.row, b.col = last.lines, last.row, last.col
	b.scrollY, b.scrollX, b.wrapSegment, b.dirty = last.scrollY, last.scrollX, last.wrapSegment, last.dirty
	return true
}

func (b *buffer) suggestion() []rune { return b.suggestionWith(nil) }

func (b *buffer) suggestionWith(extra []string) []rune {
	line := b.lines[b.row]
	if b.col != len(line) {
		return nil
	}
	start := b.col
	for start > 0 && (unicode.IsLetter(line[start-1]) || unicode.IsDigit(line[start-1]) || line[start-1] == '_') {
		start--
	}
	prefix := string(line[start:b.col])
	if len([]rune(prefix)) < 2 {
		return nil
	}
	best := ""
	consider := func(word string) {
		if word != prefix && strings.HasPrefix(word, prefix) && (best == "" || len(word) < len(best) || len(word) == len(best) && word < best) {
			best = word
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
	if best == "" {
		return nil
	}
	return []rune(best)[len([]rune(prefix)):]
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
	return max(1, (len(expandLine(line))+width-1)/width)
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
	return len(expandLine(line[:col]))
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
