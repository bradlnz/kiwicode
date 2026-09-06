// Package agent implements a bounded, cancellable agent runtime independently
// of the editor UI. Workers emit immutable events; only the UI mutates a Session.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxFileBytes     = 256 << 10
	MaxContextBytes  = 512 << 10
	MaxResponseBytes = 1 << 20
	MaxChanges       = 16
	MaxFiles         = 2048
	MaxLogEntries    = 512
	MaxLogEntryBytes = 1024
	EventCapacity    = 64
)

type State string

const (
	Idle             State = "idle"
	Running          State = "running"
	AwaitingApproval State = "approval required"
	Reviewing        State = "review changes"
	Completed        State = "completed"
	Failed           State = "failed"
	Cancelled        State = "cancelled"
	Interrupted      State = "interrupted"
)

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type Call struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}
type Message struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCalls  []Call `json:"tool_calls,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type Provider interface {
	Respond(context.Context, []Message, func(string) error) (Message, error)
}

type Change struct {
	Path    string `json:"path"`
	Before  string `json:"before"`
	After   string `json:"after"`
	Existed bool   `json:"existed"`
	Applied bool   `json:"applied,omitempty"`
}
type CheckResult struct {
	Command  []string `json:"command"`
	Output   string   `json:"output"`
	ExitCode int      `json:"exit_code"`
	Error    string   `json:"error,omitempty"`
}
type Approval struct {
	ID        uint64
	Operation string
	Summary   string
}
type Event struct {
	Kind     string
	Text     string
	State    State
	Plan     []string
	Change   *Change
	Check    *CheckResult
	Approval *Approval
}

// Log is an O(1) append ring. Rendering reads only the visible entries; a full
// transcript is never rebuilt for each streamed token.
type Log struct {
	Entries []string `json:"entries,omitempty"`
	Next    int      `json:"next,omitempty"`
}

func (l *Log) Add(text string) {
	text = Display(text)
	for len(text) > 0 {
		piece := Clip(text, MaxLogEntryBytes)
		if len(l.Entries) < MaxLogEntries {
			l.Entries = append(l.Entries, piece)
		} else {
			l.Entries[l.Next] = piece
			l.Next = (l.Next + 1) % MaxLogEntries
		}
		text = text[len(piece):]
	}
}
func (l Log) Len() int { return len(l.Entries) }
func (l Log) At(i int) string {
	if i < 0 || i >= len(l.Entries) {
		return ""
	}
	if len(l.Entries) == MaxLogEntries {
		i = (l.Next + i) % len(l.Entries)
	}
	return l.Entries[i]
}

type Session struct {
	Version int           `json:"version"`
	Draft   string        `json:"draft,omitempty"`
	State   State         `json:"state"`
	Plan    []string      `json:"plan,omitempty"`
	Log     Log           `json:"log"`
	Changes []Change      `json:"changes,omitempty"`
	Checks  []CheckResult `json:"checks,omitempty"`
	History []Message     `json:"history,omitempty"`
	View    int           `json:"view,omitempty"`
	Scroll  [4]int        `json:"scroll,omitempty"`
}

func NewSession() Session { return Session{Version: 1, State: Idle} }
func (s *Session) Apply(e Event) {
	switch e.Kind {
	case "text", "activity":
		s.Log.Add(e.Text)
	case "state":
		s.State = e.State
	case "plan":
		s.Plan = append([]string(nil), e.Plan...)
	case "approval":
		s.State = AwaitingApproval
	case "change":
		if e.Change == nil {
			return
		}
		unchanged := e.Change.Existed && e.Change.Before == e.Change.After
		for i := range s.Changes {
			if s.Changes[i].Path == e.Change.Path {
				if unchanged {
					copy(s.Changes[i:], s.Changes[i+1:])
					s.Changes[len(s.Changes)-1] = Change{}
					s.Changes = s.Changes[:len(s.Changes)-1]
				} else {
					s.Changes[i] = *e.Change
				}
				return
			}
		}
		if !unchanged && len(s.Changes) < MaxChanges {
			s.Changes = append(s.Changes, *e.Change)
		}
	case "check":
		if e.Check != nil {
			if len(s.Checks) == 16 {
				copy(s.Checks, s.Checks[1:])
				s.Checks = s.Checks[:15]
			}
			s.Checks = append(s.Checks, *e.Check)
		}
	}
}
func (s *Session) Remember(role, text string) {
	if role != "user" && role != "assistant" {
		return
	}
	if len(s.History) == 16 {
		copy(s.History, s.History[2:])
		s.History = s.History[:14]
	}
	s.History = append(s.History, Message{Role: role, Content: Clip(text, 16<<10)})
}

// Clone duplicates mutable slice headers/backing arrays, not immutable strings.
func (s Session) Clone() Session {
	s.Plan = append([]string(nil), s.Plan...)
	s.Log.Entries = append([]string(nil), s.Log.Entries...)
	s.Changes = append([]Change(nil), s.Changes...)
	s.Checks = append([]CheckResult(nil), s.Checks...)
	s.History = append([]Message(nil), s.History...)
	return s
}

// Display strips terminal control characters. Raw model/tool text must never be
// concatenated into a terminal's trusted ANSI control stream.
func Display(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			return -1
		}
		return r
	}, strings.ToValidUTF8(text, "�"))
}
func Clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Diff is linear in input size, rather than a quadratic edit-distance algorithm.
// It shows one context hunk containing the changed span. Application uses exact
// before/after data, not this deliberately bounded display representation.
const truncatedDiff = "… diff display truncated; request a smaller edit before applying"

func (c Change) Diff() []string   { lines, _ := c.diff(); return lines }
func (c Change) Reviewable() bool { _, complete := c.diff(); return complete }

func (c Change) diff() ([]string, bool) {
	before, after := strings.Split(c.Before, "\n"), strings.Split(c.After, "\n")
	start := 0
	for start < len(before) && start < len(after) && before[start] == after[start] {
		start++
	}
	endB, endA := len(before), len(after)
	for endB > start && endA > start && before[endB-1] == after[endA-1] {
		endB--
		endA--
	}
	lo := max(0, start-3)
	hiB := min(len(before), endB+3)
	hiA := min(len(after), endA+3)
	out := []string{"--- " + c.Path, "+++ " + c.Path, fmt.Sprintf("@@ -%d,%d +%d,%d @@", lo+1, hiB-lo, lo+1, hiA-lo)}
	complete := true
	add := func(prefix, line string) {
		if len(out) >= 2048 {
			complete = false
			return
		}
		if len(line) > 2048 {
			complete = false
		}
		out = append(out, prefix+Clip(Display(line), 2048))
	}
	for i := lo; i < start; i++ {
		add(" ", before[i])
	}
	for i := start; i < endB; i++ {
		add("-", before[i])
	}
	for i := start; i < endA; i++ {
		add("+", after[i])
	}
	for i := endA; i < hiA; i++ {
		add(" ", after[i])
	}
	if !complete {
		out = append(out, truncatedDiff)
	}
	return out, complete
}

func decodeArguments(text string, dst any) error {
	if len(text) > MaxFileBytes*2 {
		return fmt.Errorf("tool arguments exceed limit")
	}
	d := json.NewDecoder(strings.NewReader(text))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("invalid tool arguments: %v", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("multiple JSON values in tool arguments")
	}
	return nil
}
