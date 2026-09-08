package main

import (
	"context"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	bracketedPasteOn  = "\x1b[?2004h"
	bracketedPasteOff = "\x1b[?2004l"
	pasteEnd          = "\x1b[201~"
	maxPasteBytes     = 2 << 20
)

type inputEvent struct {
	key     key
	paste   string
	isPaste bool
	err     error
}

type inputDecoder struct {
	pending  []byte // at most one incomplete UTF-8 rune or short CSI sequence
	pasting  bool
	paste    []byte
	end      []byte // rolling candidate for a split bracketed-paste terminator
	overflow bool
}

func (d *inputDecoder) feed(data []byte, emit func(inputEvent) bool) bool {
	for _, b := range data {
		if d.pasting {
			d.end = append(d.end, b)
			for len(d.end) > 0 && !strings.HasPrefix(pasteEnd, string(d.end)) {
				if len(d.paste) < maxPasteBytes {
					d.paste = append(d.paste, d.end[0])
				} else {
					d.overflow = true
				}
				copy(d.end, d.end[1:])
				d.end = d.end[:len(d.end)-1]
			}
			if string(d.end) == pasteEnd {
				event := inputEvent{paste: strings.ToValidUTF8(string(d.paste), "�"), isPaste: true}
				if d.overflow {
					event.paste = ""
					event.err = errPasteTooLarge
				}
				d.pasting = false
				d.paste = nil
				d.end = d.end[:0]
				d.overflow = false
				if !emit(event) {
					return false
				}
			}
			continue
		}
		d.pending = append(d.pending, b)
		for len(d.pending) > 0 {
			k, n, complete := decodeInputKey(d.pending)
			if !complete {
				break
			}
			if n == 0 { // bracketed paste start
				d.pasting = true
				d.pending = d.pending[:0]
				break
			}
			copy(d.pending, d.pending[n:])
			d.pending = d.pending[:len(d.pending)-n]
			if !emit(inputEvent{key: k}) {
				return false
			}
		}
	}
	return true
}

// A raw terminal's 100 ms read timeout disambiguates a lone Escape key. Paste
// mode is intentionally never timed out into executable Enter/key events.
func (d *inputDecoder) timeout(emit func(inputEvent) bool) bool {
	if d.pasting || len(d.pending) == 0 {
		return true
	}
	if d.pending[0] == 27 {
		d.pending = d.pending[:0]
		return emit(inputEvent{})
	}
	if !utf8.FullRune(d.pending) {
		return true
	}
	return d.feed(nil, emit)
}

type inputError string

func (e inputError) Error() string { return string(e) }

const errPasteTooLarge = inputError("Paste exceeded 2 MiB and was discarded; no input was executed")

func decodeInputKey(data []byte) (key, int, bool) {
	b := data[0]
	if b != 27 {
		switch b {
		case '\r', '\n':
			return key{code: keyEnter}, 1, true
		case '\t':
			return key{code: keyTab}, 1, true
		case 8, 127:
			return key{code: keyBackspace}, 1, true
		}
		if !utf8.FullRune(data) {
			return key{}, 0, false
		}
		r, n := utf8.DecodeRune(data)
		return key{r: r}, n, true
	}
	if len(data) == 1 {
		return key{}, 0, false
	}
	if data[1] == 'O' {
		if len(data) < 3 {
			return key{}, 0, false
		}
		return key{raw: string(data[:3])}, 3, true
	}
	if data[1] != '[' {
		if !utf8.FullRune(data[1:]) {
			return key{}, 0, false
		}
		r, n := utf8.DecodeRune(data[1:])
		return key{r: r, alt: true}, n + 1, true
	}
	if len(data) == 2 {
		return key{}, 0, false
	}
	if len(data) > 64 {
		return key{}, len(data), true
	}
	last := data[len(data)-1]
	if last < 0x40 || last > 0x7e {
		return key{}, 0, false
	}
	seq := string(data[2:])
	if seq == "200~" {
		return key{}, 0, true
	}
	switch seq {
	case "A":
		return key{code: keyUp}, len(data), true
	case "B":
		return key{code: keyDown}, len(data), true
	case "C":
		return key{code: keyRight}, len(data), true
	case "D":
		return key{code: keyLeft}, len(data), true
	case "H", "1~", "7~":
		return key{code: keyHome}, len(data), true
	case "F", "4~", "8~":
		return key{code: keyEnd}, len(data), true
	case "3~":
		return key{code: keyDelete}, len(data), true
	case "5~":
		return key{code: keyPageUp}, len(data), true
	case "6~":
		return key{code: keyPageDown}, len(data), true
	}
	if data[2] == '<' && (last == 'M' || last == 'm') {
		return parseMouse(string(data[3:len(data)-1]), last == 'm'), len(data), true
	}
	return key{raw: string(data)}, len(data), true
}

// Only this goroutine reads stdin. Read batching avoids a syscall for every
// byte, and the bounded event channel applies backpressure to pasted input.
func readInput(ctx context.Context, file *os.File) (<-chan inputEvent, <-chan struct{}) {
	events := make(chan inputEvent, 128)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(events)
		var d inputDecoder
		var data [4096]byte
		emit := func(event inputEvent) bool {
			select {
			case events <- event:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			n, err := file.Read(data[:])
			if n > 0 && !d.feed(data[:n], emit) {
				return
			}
			if err != nil && err != io.EOF {
				emit(inputEvent{err: err})
				return
			}
			if n == 0 {
				if !d.timeout(emit) {
					return
				}
				// VMIN=0/VTIME=1 returns zero bytes on idle. A closed terminal
				// may return immediately, so also prevent an EOF busy loop.
				time.Sleep(time.Millisecond)
			}
		}
	}()
	return events, done
}
