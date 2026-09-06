package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCleanPath(t *testing.T) {
	for _, path := range []string{"../x", "a/../b", "/etc/passwd", "a//b", "a\\b", ".git/config", ".env.local", "a/.ssh/id_rsa", "service.key", "credentials.json", "a\x1bb", "a\u202eb", "", "./x"} {
		if _, err := CleanPath(path); err == nil {
			t.Errorf("accepted %q", path)
		}
	}
	for _, path := range []string{"src/main.go", ".github/workflows/test.yml", "hello 世界.txt", "go.mod"} {
		if got, err := CleanPath(path); err != nil || got != path {
			t.Errorf("%q: %q, %v", path, got, err)
		}
	}
}
func TestRootedReads(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(root, "ok.txt"), "hello")
	mustWrite(t, filepath.Join(outside, "secret.txt"), "outside")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"escape/secret.txt", "alias.txt"} {
		if _, err := ReadWorkspace(root, path); err == nil {
			t.Fatalf("followed %s", path)
		}
	}
	if got, err := ReadWorkspace(root, "ok.txt"); err != nil || string(got) != "hello" {
		t.Fatalf("read: %q %v", got, err)
	}
	if _, err := ReadWorkspace(root, "missing.txt"); !os.IsNotExist(err) {
		t.Fatalf("missing: %v", err)
	}
	mustWrite(t, filepath.Join(root, "large.txt"), strings.Repeat("x", MaxFileBytes+1))
	if _, err := ReadWorkspace(root, "large.txt"); err == nil {
		t.Fatal("accepted oversized file")
	}
	if _, err := ReadWorkspace(root, "escape"); err == nil {
		t.Fatal("accepted directory symlink")
	}
}
func TestArgumentValidation(t *testing.T) {
	for _, input := range []string{`{"path":"a"} false`, `{"path":"a"} garbage`, `{"unexpected":1}`, `{"path":`} {
		var v struct {
			Path string `json:"path"`
		}
		if err := decodeArguments(input, &v); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
}
func TestProviderConfiguration(t *testing.T) {
	for _, endpoint := range []string{"", "http://example.com/api", "https://user:pass@example.com/api", "https://example.com/api?key=secret", "file:///tmp/model"} {
		if p, err := NewHTTPProvider(Config{Endpoint: endpoint, Model: "test"}); err == nil {
			p.Close()
			t.Errorf("accepted %q", endpoint)
		}
	}
}
func frame(content string) string {
	data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": content}}}})
	return "data: " + string(data) + "\n\n"
}

const stopFrame = "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

func TestHTTPProvider(t *testing.T) {
	var called atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		if r.Header.Get("Authorization") != "Bearer testing-key" {
			t.Error("missing bearer header")
		}
		var body struct {
			Model    string    `json:"model"`
			Messages []Message `json:"messages"`
			Tools    []any     `json:"tools"`
			Stream   bool      `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "test-model" || !body.Stream || len(body.Tools) != 5 {
			t.Errorf("request: %+v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, frame("Hello ")+frame("世界")+stopFrame)
	}))
	defer server.Close()
	p, err := NewHTTPProvider(Config{server.URL, "test-model", "testing-key"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var text strings.Builder
	result, err := p.Respond(context.Background(), []Message{{Role: "user", Content: "hello"}}, func(s string) error { text.WriteString(s); return nil })
	if err != nil || result.Content != "Hello 世界" || text.String() != result.Content || called.Load() != 1 {
		t.Fatalf("response: %+v %v", result, err)
	}
	_, err = p.Respond(context.Background(), []Message{{Role: "user", Content: strings.Repeat("x", MaxContextBytes)}}, func(string) error { return nil })
	if err == nil || called.Load() != 1 {
		t.Fatal("oversized request reached provider")
	}
}
func TestHTTPNoRedirectOrErrorBodyDisclosure(t *testing.T) {
	var reached atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	p, err := NewHTTPProvider(Config{redirect.URL, "test", "secret"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err = p.Respond(context.Background(), nil, func(string) error { return nil }); err == nil || reached.Load() != 0 {
		t.Fatal("redirect was followed")
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, "secret credential body")
	}))
	defer bad.Close()
	q, _ := NewHTTPProvider(Config{bad.URL, "test", "secret"})
	defer q.Close()
	if _, err = q.Respond(context.Background(), nil, func(string) error { return nil }); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe error: %v", err)
	}
}
func TestStreamToolAssembly(t *testing.T) {
	stream := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"read_file","arguments":"{\"pa"}}]}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"a.go\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n"
	m, err := readStream(strings.NewReader(stream), func(string) error { return nil })
	if err != nil || len(m.ToolCalls) != 1 || m.ToolCalls[0].Function.Arguments != `{"path":"a.go"}` {
		t.Fatalf("%+v %v", m, err)
	}
}
func TestBadStreams(t *testing.T) {
	cases := []string{
		frame("partial"),
		`data: not-json` + "\n\n",
		`data: {"choices":[{"delta":{},"finish_reason":"length"}]}` + "\n\ndata: [DONE]\n",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":9}]}}]}` + "\n\n",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"x","type":"function","function":{"name":"read_file","arguments":"{"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n",
		frame(strings.Repeat("x", (64<<10)+1)) + stopFrame,
	}
	for i, input := range cases {
		if _, err := readStream(strings.NewReader(input), func(string) error { return nil }); err == nil {
			t.Errorf("accepted bad stream %d", i)
		}
	}
	_, err := readStream(strings.NewReader(frame("x")+stopFrame), func(string) error { return context.Canceled })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("callback cancellation: %v", err)
	}
}

type scriptProvider struct {
	step    int
	answers []Message
	before  func(int, []Message)
}

func (s *scriptProvider) Respond(ctx context.Context, messages []Message, onText func(string) error) (Message, error) {
	if s.before != nil {
		s.before(s.step, messages)
	}
	if s.step >= len(s.answers) {
		return Message{}, errors.New("script exhausted")
	}
	m := s.answers[s.step]
	s.step++
	if m.Content != "" {
		if err := onText(m.Content); err != nil {
			return Message{}, err
		}
	}
	return m, nil
}
func callMessage(name string, args any) Message {
	data, _ := json.Marshal(args)
	return Message{Role: "assistant", ToolCalls: []Call{{ID: "call-" + name, Type: "function", Function: FunctionCall{name, string(data)}}}}
}
func runToEnd(t *testing.T, r *Run, approve bool) []Event {
	t.Helper()
	defer r.Cancel()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	var events []Event
	for {
		select {
		case e, ok := <-r.Events:
			if !ok {
				return events
			}
			events = append(events, e)
			if e.Approval != nil {
				if r.Decide(e.Approval.ID+1, true) {
					t.Fatal("accepted wrong approval id")
				}
				if !r.Decide(e.Approval.ID, approve) {
					t.Fatal("approval not accepted")
				}
				if r.Decide(e.Approval.ID, true) {
					t.Fatal("approval replay accepted")
				}
			}
		case <-deadline.C:
			t.Fatal("agent did not finish")
			return events
		}
	}
}
func TestTaskToolsChangesChecksReview(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.txt"), "hello\n")
	t.Setenv("KIWICODE_AGENT_API_KEY", "do-not-leak")
	provider := &scriptProvider{answers: []Message{
		callMessage("set_plan", map[string]any{"steps": []string{"Read", "Edit", "Check"}}),
		callMessage("read_file", map[string]string{"path": "main.txt"}),
		callMessage("propose_edit", map[string]string{"path": "main.txt", "old_text": "hello", "new_text": "hello Go"}),
		callMessage("run_check", map[string]any{"command": []string{"sh", "-c", "cat main.txt; env"}}),
		callMessage("read_file", map[string]string{"path": "main.txt"}),
		{Role: "assistant", Content: "Staged a change and ran a snapshot check. Review before applying."},
	}}
	provider.before = func(step int, m []Message) {
		if step == 5 && m[len(m)-1].Content != "hello Go\n" {
			t.Error("subsequent read did not see staged text")
		}
	}
	r := Start(context.Background(), Request{Root: root, Prompt: "Improve greeting", Files: []string{"main.txt"}, AllowCommands: true}, provider)
	events := runToEnd(t, r, true)
	if result := r.Result(); result.State != Reviewing || result.Err != nil {
		t.Fatalf("result: %+v", result)
	}
	var session Session
	session.Version = 1
	approvals := 0
	for _, e := range events {
		session.Apply(e)
		if e.Approval != nil {
			approvals++
		}
	}
	if approvals != 2 || len(session.Changes) != 1 || len(session.Checks) != 1 {
		t.Fatalf("events: approvals=%d changes=%d checks=%d", approvals, len(session.Changes), len(session.Checks))
	}
	check := session.Checks[0]
	if check.ExitCode != 0 || !strings.Contains(check.Output, "hello Go") || strings.Contains(check.Output, "do-not-leak") {
		t.Fatalf("check: %+v", check)
	}
	data, _ := os.ReadFile(filepath.Join(root, "main.txt"))
	if string(data) != "hello\n" {
		t.Fatal("agent modified the live workspace")
	}
}
func TestDeniedAndDirtyReads(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(fmt.Sprint(blocked), func(t *testing.T) {
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, "file.txt"), "private")
			p := &scriptProvider{answers: []Message{callMessage("read_file", map[string]string{"path": "file.txt"}), {Content: "Cannot read it."}}}
			p.before = func(step int, m []Message) {
				if step == 1 && (!strings.Contains(m[len(m)-1].Content, "Tool error:") || strings.Contains(m[len(m)-1].Content, "private")) {
					t.Fatal("denied content leaked")
				}
			}
			req := Request{Root: root, Prompt: "read"}
			if blocked {
				req.BlockedPaths = []string{"file.txt"}
			}
			events := runToEnd(t, Start(context.Background(), req, p), false)
			for _, e := range events {
				if blocked && e.Approval != nil {
					t.Fatal("dirty file requested approval")
				}
			}
		})
	}
}
func TestStaleSnapshotCheckIsRefused(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	mustWrite(t, path, "old")
	p := &scriptProvider{answers: []Message{
		callMessage("read_file", map[string]string{"path": "a.txt"}),
		callMessage("propose_edit", map[string]string{"path": "a.txt", "old_text": "old", "new_text": "agent"}),
		callMessage("run_check", map[string]any{"command": []string{"sh", "-c", "echo should-not-run"}}),
		{Content: "Source changed; check was not run."},
	}}
	p.before = func(step int, m []Message) {
		if step == 2 {
			mustWrite(t, path, "user change")
		}
		if step == 3 && !strings.Contains(m[len(m)-1].Content, "changed since read") {
			t.Fatalf("stale check: %s", m[len(m)-1].Content)
		}
	}
	events := runToEnd(t, Start(context.Background(), Request{Root: root, Prompt: "edit", Files: []string{"a.txt"}, AllowCommands: true}, p), true)
	for _, e := range events {
		if e.Check != nil {
			t.Fatal("ran a stale snapshot check")
		}
	}
	data, _ := os.ReadFile(path)
	if string(data) != "user change" {
		t.Fatal("overwrote user edit")
	}
}
func TestCancelWhileAwaitingApproval(t *testing.T) {
	p := &scriptProvider{answers: []Message{callMessage("read_file", map[string]string{"path": "file.txt"})}}
	r := Start(context.Background(), Request{Root: t.TempDir(), Prompt: "read"}, p)
	deadline := time.After(time.Second)
	for {
		select {
		case e := <-r.Events:
			if e.Approval != nil {
				r.Cancel()
				goto cancelled
			}
		case <-deadline:
			t.Fatal("no approval")
		}
	}
cancelled:
	select {
	case <-r.Done:
	case <-time.After(time.Second):
		t.Fatal("cancellation blocked")
	}
	if r.Result().State != Cancelled {
		t.Fatalf("%+v", r.Result())
	}
}

type floodProvider struct{ started chan struct{} }

func (f floodProvider) Respond(ctx context.Context, _ []Message, emit func(string) error) (Message, error) {
	close(f.started)
	for {
		if err := emit(strings.Repeat("x", 4096)); err != nil {
			return Message{}, err
		}
	}
}
func TestCancellationWithFullEventQueue(t *testing.T) {
	p := floodProvider{started: make(chan struct{})}
	r := Start(context.Background(), Request{Root: t.TempDir(), Prompt: "stream"}, p)
	<-p.started
	deadline := time.Now().Add(time.Second)
	for len(r.events) < cap(r.events) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(r.events) != EventCapacity {
		t.Fatal("queue did not fill")
	}
	r.Cancel()
	select {
	case <-r.Done:
	case <-time.After(time.Second):
		t.Fatal("full queue prevented cancellation")
	}
	if r.Result().State != Cancelled {
		t.Fatalf("%+v", r.Result())
	}
}
func TestCommandCancellationAndBoundedOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	result := executeCheck(ctx, t.TempDir(), []string{"sh", "-c", "sleep 30 & wait"})
	if time.Since(start) > 2*time.Second || result.Error == "" {
		t.Fatalf("command cancellation: %+v", result)
	}
	var b boundedOutput
	payload := strings.Repeat("z", 100000)
	n, err := io.WriteString(&b, payload)
	if err != nil || n != len(payload) || len(b.data) != 32<<10 || !strings.Contains(b.String(), "truncated") {
		t.Fatal("output was not bounded/drained")
	}
}
func TestSessionRingStoreAndInterruptedRestore(t *testing.T) {
	s := NewSession()
	s.State = Running
	for i := 0; i < 1000; i++ {
		s.Log.Add(fmt.Sprint(i))
	}
	if s.Log.Len() != MaxLogEntries || s.Log.At(0) != "488" || s.Log.At(511) != "999" {
		t.Fatal("ring order incorrect")
	}
	clone := s.Clone()
	s.Log.Add("new")
	if clone.Log.At(0) != "488" {
		t.Fatal("checkpoint shares mutable ring storage")
	}
	path := filepath.Join(t.TempDir(), "agents", "session.json")
	store := NewStore(path)
	for i := 0; i < 50; i++ {
		s.Draft = fmt.Sprint(i)
		store.Queue(&s)
	}
	store.Close()
	loaded, err := LoadSession(path)
	if err != nil || loaded.Draft != "49" || loaded.State != Interrupted {
		t.Fatalf("restore: %+v %v", loaded, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("session permissions are not private")
	}
	forget := NewStore(path)
	forget.Queue(nil)
	forget.Close()
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("forget did not delete session")
	}
}
func TestDisplayAndDiffBounds(t *testing.T) {
	if strings.ContainsAny(Display("a\x1b[31mb\u009bc\u202ed"), "\x1b\u009b\u202e") {
		t.Fatal("terminal controls retained")
	}
	change := Change{Path: "a.txt", Before: strings.Repeat("old\n", 10000), After: strings.Repeat("new\n", 10000), Existed: true}
	if diff := change.Diff(); len(diff) > 2049 || !strings.Contains(diff[len(diff)-1], "truncated") {
		t.Fatalf("diff is not bounded: %d", len(diff))
	}
}
func BenchmarkLogAppend(b *testing.B) {
	var l Log
	for i := 0; i < MaxLogEntries; i++ {
		l.Add("progress")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Add("agent progress")
	}
}

var benchText string

func BenchmarkVisibleLogRead(b *testing.B) {
	var l Log
	for i := 0; i < MaxLogEntries; i++ {
		l.Add("progress")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for row := 0; row < 30; row++ {
			benchText = l.At(400 + row)
		}
	}
}
func mustWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotAllowsAssetsWithoutSendingThemToModel(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "asset.bin"), strings.Repeat("\x00", 768<<10))
	if _, err := ReadWorkspace(root, "asset.bin"); err == nil {
		t.Fatal("large asset became model-readable")
	}
	p := &scriptProvider{answers: []Message{callMessage("run_check", map[string]any{"command": []string{"sh", "-c", "test $(wc -c < asset.bin) -eq 786432"}}), {Content: "Checks completed."}}}
	r := Start(context.Background(), Request{Root: root, Prompt: "check assets", Files: []string{"asset.bin"}, AllowCommands: true}, p)
	events := runToEnd(t, r, true)
	found := false
	for _, ev := range events {
		if ev.Check != nil {
			found = true
			if ev.Check.ExitCode != 0 {
				t.Fatalf("asset snapshot: %+v", ev.Check)
			}
		}
	}
	if !found {
		t.Fatalf("asset check did not run: %+v", events)
	}
}

func TestPartialManifestCannotProducePassingChecks(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.txt"), "a")
	files := make([]string, MaxFiles+1)
	for i := range files {
		files[i] = "a.txt"
	}
	p := &scriptProvider{answers: []Message{callMessage("run_check", map[string]any{"command": []string{"sh", "-c", "true"}}), {Content: "Check was refused."}}}
	r := Start(context.Background(), Request{Root: root, Prompt: "check", Files: files, AllowCommands: true}, p)
	events := runToEnd(t, r, true)
	refused := false
	for _, ev := range events {
		if ev.Check != nil {
			t.Fatal("ran check on a partial workspace")
		}
		if strings.Contains(ev.Text, "partial snapshot") {
			refused = true
		}
	}
	if !refused {
		t.Fatal("missing partial-snapshot refusal")
	}
}

func TestRevertedProposalIsNotRetained(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.txt"), "a")
	p := &scriptProvider{answers: []Message{
		callMessage("read_file", map[string]string{"path": "a.txt"}),
		callMessage("propose_edit", map[string]string{"path": "a.txt", "old_text": "a", "new_text": "b"}),
		callMessage("propose_edit", map[string]string{"path": "a.txt", "old_text": "b", "new_text": "a"}),
		{Content: "No changes remain."},
	}}
	r := Start(context.Background(), Request{Root: root, Prompt: "edit then revert", Files: []string{"a.txt"}}, p)
	s := NewSession()
	for _, ev := range runToEnd(t, r, true) {
		s.Apply(ev)
	}
	if len(s.Changes) != 0 || r.Result().State != Completed {
		t.Fatal("no-op proposal survived")
	}
	if (Change{Before: strings.Repeat("x", 3000), After: "y"}).Reviewable() {
		t.Fatal("allowed a truncated review")
	}
}
