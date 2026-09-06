package main

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

type undoTestState struct {
	lines                      []string
	row, col, scrollY, scrollX int
	wrapSegment                int
	dirty                      bool
	newline                    string
}

func captureUndoTestState(buf *buffer) undoTestState {
	return undoTestState{runeLines(buf.lines), buf.row, buf.col, buf.scrollY, buf.scrollX, buf.wrapSegment, buf.dirty, buf.newline}
}

func assertUndoTestState(t *testing.T, buf *buffer, want undoTestState) {
	t.Helper()
	if got := captureUndoTestState(buf); !reflect.DeepEqual(got, want) {
		t.Fatalf("buffer state mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestBufferUndoEditRoundTrip(t *testing.T) {
	cases := []struct {
		name, text string
		row, col   int
		edit       func(*buffer)
		want       string
	}{
		{"insert", "alpha\nbeta\ngamma", 1, 2, func(b *buffer) { b.insert([]rune("X")) }, "alpha\nbeXta\ngamma"},
		{"unicode", "alpha\n世界\ngamma", 1, 1, func(b *buffer) { b.insert([]rune("é🙂")) }, "alpha\n世é🙂界\ngamma"},
		{"tab", "alpha\nbeta\ngamma", 1, 2, func(b *buffer) { b.handle(key{code: keyTab}) }, "alpha\nbe    ta\ngamma"},
		{"enter", "alpha\nbeta\ngamma", 1, 2, func(b *buffer) { b.handle(key{code: keyEnter}) }, "alpha\nbe\nta\ngamma"},
		{"indent", "alpha\n    if ok {\ngamma", 1, 11, func(b *buffer) { b.handle(key{code: keyEnter}) }, "alpha\n    if ok {\n        \ngamma"},
		{"backspace", "alpha\nbeta\ngamma", 1, 2, func(b *buffer) { b.handle(key{code: keyBackspace}) }, "alpha\nbta\ngamma"},
		{"backspace-join", "alpha\nbeta\ngamma", 1, 0, func(b *buffer) { b.handle(key{code: keyBackspace}) }, "alphabeta\ngamma"},
		{"delete", "alpha\nbeta\ngamma", 1, 2, func(b *buffer) { b.handle(key{code: keyDelete}) }, "alpha\nbea\ngamma"},
		{"delete-join", "alpha\nbeta\ngamma", 1, 4, func(b *buffer) { b.handle(key{code: keyDelete}) }, "alpha\nbetagamma"},
		{"multiline", "alpha\nbeta\ngamma", 1, 2, func(b *buffer) { b.insertText("X\nY\nZ") }, "alpha\nbeX\nY\nZta\ngamma"},
		{"crlf", "alpha\r\nbeta\r\ngamma", 1, 2, func(b *buffer) { b.insertText("X\r\nY\rZ") }, "alpha\nbeX\nY\nZta\ngamma"},
		{"empty", "", 0, 0, func(b *buffer) { b.handle(key{code: keyEnter}) }, "\n"},
		{"trailing-newline", "a\n", 1, 0, func(b *buffer) { b.handle(key{code: keyBackspace}) }, "a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := newBuffer("test.txt", []byte(tc.text))
			buf.row, buf.col = tc.row, tc.col
			buf.scrollY, buf.scrollX, buf.wrapSegment = 1, 2, 3
			before := captureUndoTestState(buf)
			tc.edit(buf)
			if got := strings.Join(runeLines(buf.lines), "\n"); got != tc.want {
				t.Fatalf("edit produced %q, want %q", got, tc.want)
			}
			if !buf.dirty || !buf.undoChange() {
				t.Fatal("edit was not dirty or could not be undone")
			}
			assertUndoTestState(t, buf, before)
			if buf.undoChange() {
				t.Fatal("undo history should be empty")
			}
		})
	}
}

func TestBufferUndoMixedHistory(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			buf := newBuffer("test.txt", []byte("alpha\nbeta\n世界\n"))
			var states []undoTestState
			for step := 0; step < 100; step++ {
				buf.row = rng.Intn(len(buf.lines))
				buf.col = rng.Intn(len(buf.lines[buf.row]) + 1)
				buf.scrollY, buf.scrollX, buf.wrapSegment = rng.Intn(5), rng.Intn(5), rng.Intn(3)
				before := captureUndoTestState(buf)
				switch rng.Intn(6) {
				case 0:
					buf.insert([]rune("é"))
				case 1:
					buf.handle(key{code: keyEnter})
				case 2:
					if buf.col == 0 && buf.row == 0 {
						continue
					}
					buf.handle(key{code: keyBackspace})
				case 3:
					if buf.col == len(buf.lines[buf.row]) && buf.row == len(buf.lines)-1 {
						continue
					}
					buf.handle(key{code: keyDelete})
				case 4:
					buf.insertText("one\ntwo\n")
				case 5:
					// Model a compound editor operation that records one full snapshot.
					buf.recordUndo()
					buf.suppressUndo = true
					buf.insertText("first\nsecond")
					buf.handle(key{code: keyBackspace})
					buf.suppressUndo = false
				}
				states = append(states, before)
				if len(states) > 50 {
					states = states[1:]
				}
			}
			for i := len(states) - 1; i >= 0; i-- {
				if !buf.undoChange() {
					t.Fatalf("missing undo entry %d", i)
				}
				assertUndoTestState(t, buf, states[i])
			}
			if buf.undoChange() {
				t.Fatal("history exceeded the 50-entry limit")
			}
		})
	}
}

func TestBufferUndoFullReplacement(t *testing.T) {
	buf := newBuffer("test.txt", []byte("first\nsecond\nthird"))
	original := captureUndoTestState(buf)
	buf.insert([]rune("x"))
	beforeReplace := captureUndoTestState(buf)
	buf.recordUndo()
	buf.lines = [][]rune{[]rune("formatted")}
	buf.row, buf.col, buf.dirty = 0, 0, true
	beforeInsert := captureUndoTestState(buf)
	buf.insertText("A\nB")
	for _, want := range []undoTestState{beforeInsert, beforeReplace, original} {
		if !buf.undoChange() {
			t.Fatal("missing mixed full/range undo entry")
		}
		assertUndoTestState(t, buf, want)
	}
}

func TestBufferUndoNoOps(t *testing.T) {
	buf := newBuffer("test.txt", nil)
	buf.insert(nil)
	buf.insertText("")
	buf.handle(key{code: keyBackspace})
	buf.handle(key{code: keyDelete})
	buf.handle(key{code: keyLeft})
	if len(buf.undo) != 0 || buf.dirty {
		t.Fatal("no-op edit recorded history or dirtied the buffer")
	}
}

func TestBufferUndoStoresAffectedLines(t *testing.T) {
	buf := newBuffer("large.txt", []byte(strings.Repeat("unchanged line\n", 10000)))
	buf.row, buf.col = 5000, 4
	untouched := &buf.lines[9000][0]
	buf.insert([]rune("X"))
	if len(buf.undo) != 1 || len(buf.undo[0].lines) != 1 {
		t.Fatal("single-line edit must retain only the changed line")
	}
	if !buf.undoChange() || untouched != &buf.lines[9000][0] {
		t.Fatal("range undo replaced an untouched line")
	}
	// Popping history must not retain document data beyond the slice length.
	for _, entry := range buf.undo[:cap(buf.undo)] {
		if entry.lines != nil {
			t.Fatal("popped history retained line data")
		}
	}
}

func TestBufferCellMeasurementMatchesExpansion(t *testing.T) {
	lines := []string{"", "plain", "\t", "a\tb\t", "\x00\x1f\x7f", "é世界🙂\tend", "\t\t\t"}
	rng := rand.New(rand.NewSource(42))
	alphabet := []rune("abc\t\x00\x7fé世界🙂")
	for range 100 {
		line := make([]rune, rng.Intn(80))
		for i := range line {
			line[i] = alphabet[rng.Intn(len(alphabet))]
		}
		lines = append(lines, string(line))
	}
	for _, text := range lines {
		line := []rune(text)
		for col := 0; col <= len(line); col++ {
			if got, want := cursorCell(line, col), len(expandLine(line[:col])); got != want {
				t.Fatalf("cursorCell(%q, %d)=%d, want %d", text, col, got, want)
			}
		}
		for _, width := range []int{1, 2, 4, 7, 80} {
			want := max(1, (len(expandLine(line))+width-1)/width)
			if got := wrapCount(line, width); got != want {
				t.Fatalf("wrapCount(%q, %d)=%d, want %d", text, width, got, want)
			}
		}
	}
}

var bufferPerformanceInt int

func TestBufferCellMeasurementAllocations(t *testing.T) {
	line := []rune(strings.Repeat("\tvalue = 42;", 64))
	allocs := testing.AllocsPerRun(100, func() {
		bufferPerformanceInt = cursorCell(line, len(line)) + wrapCount(line, 80)
	})
	if allocs != 0 {
		t.Fatalf("cell measurement allocated %g times, want zero", allocs)
	}
}

func BenchmarkBufferInsertUndo(b *testing.B) {
	for _, count := range []int{100, 10000, 30000} {
		b.Run(fmt.Sprintf("lines_%d", count), func(b *testing.B) {
			line := strings.Repeat("x", 60)
			buf := newBuffer("large.txt", []byte(strings.Repeat(line+"\n", count-1)+line))
			buf.row, buf.col = count/2, 30
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf.insert([]rune{'x'})
				if !buf.undoChange() {
					b.Fatal("edit could not be undone")
				}
			}
		})
	}
}

func BenchmarkBufferCursorCell(b *testing.B) {
	line := []rune(strings.Repeat("\tvalue = 42;", 256))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bufferPerformanceInt = cursorCell(line, len(line))
	}
}

func BenchmarkBufferWrapCount(b *testing.B) {
	line := []rune(strings.Repeat("\tvalue = 42;", 256))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bufferPerformanceInt = wrapCount(line, 80)
	}
}
