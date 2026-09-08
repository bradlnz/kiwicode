package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBracketedPasteIsOneUndoableEdit(t *testing.T) {
	e := &editor{buffers: []*buffer{newBuffer("sample.txt", []byte("original"))}}
	var d inputDecoder
	var events []inputEvent
	emit := func(ev inputEvent) bool { events = append(events, ev); return true }
	payload := "pasted\n\x11hello 世界"
	for _, part := range []string{"\x1b[20", "0~" + payload[:8], payload[8:] + "\x1b[20", "1~"} {
		d.feed([]byte(part), emit)
	}
	if len(events) != 1 || !events[0].isPaste || events[0].paste != payload {
		t.Fatalf("paste became key events: %+v", events)
	}
	e.handlePaste(events[0].paste)
	if e.quitArmed || !e.current().dirty || len(e.current().undo) != 1 {
		t.Fatal("paste was executed or split into multiple undo operations")
	}
	if !e.current().undoChange() || string(e.current().lines[0]) != "original" || e.current().dirty {
		t.Fatal("paste undo did not restore the buffer")
	}
}

func TestInputDecoderFragmentsAndOverflow(t *testing.T) {
	var d inputDecoder
	var got []key
	emit := func(ev inputEvent) bool {
		if ev.err != nil || ev.isPaste {
			t.Fatal("unexpected paste")
		}
		got = append(got, ev.key)
		return true
	}
	data := []byte("é\x1b1\x1b[A\x1b[3~\x1b[<0;5;3M\r")
	for _, b := range data {
		d.feed([]byte{b}, emit)
	}
	want := []key{{r: 'é'}, {r: '1', alt: true}, {code: keyUp}, {code: keyDelete}, {mouse: true, button: 0, x: 5, y: 3}, {code: keyEnter}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded %+v", got)
	}
	d.feed([]byte{27}, emit)
	d.timeout(emit)
	if len(got) != len(want)+1 || got[len(got)-1] != (key{}) {
		t.Fatal("lone Escape not decoded")
	}
	var overflow []inputEvent
	d.feed([]byte("\x1b[200~"+strings.Repeat("x", maxPasteBytes+1)+"\x1b[201~"), func(ev inputEvent) bool { overflow = append(overflow, ev); return true })
	if len(overflow) != 1 || overflow[0].err != errPasteTooLarge || overflow[0].paste != "" {
		t.Fatal("oversized paste was not discarded")
	}
}

func TestFrameScheduleCoalescesWithoutPostponing(t *testing.T) {
	var frames frameSchedule
	defer frames.stop()
	if frames.channel() != nil {
		t.Fatal("idle scheduler has a live frame channel")
	}
	frames.request()
	first := frames.timer
	for i := 0; i < 10000; i++ {
		frames.request()
	}
	if frames.timer != first {
		t.Fatal("allocated one timer per event")
	}
	select {
	case <-frames.channel():
		frames.fired()
	case <-time.After(time.Second):
		t.Fatal("frame starved")
	}
	if frames.channel() != nil {
		t.Fatal("idle frame kept redrawing")
	}
}

func BenchmarkInputASCII(b *testing.B) {
	var d inputDecoder
	data := []byte("abcdefghij")
	emit := func(inputEvent) bool { return true }
	d.feed(data, emit)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.feed(data, emit)
	}
}
func BenchmarkFrameIdle(b *testing.B) {
	var f frameSchedule
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if f.channel() != nil {
			b.Fatal("unexpected frame")
		}
	}
}
