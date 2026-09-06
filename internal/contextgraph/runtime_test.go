package agent

import (
 "context"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "os"
 "path/filepath"
 "strings"
 "testing"
 "time"
)

type providerFunc func(context.Context, []Message) (Message, error)
func (f providerFunc) Complete(c context.Context, m []Message) (Message, error) { return f(c, m) }
func waitReady(t *testing.T, s *Service) View {
 t.Helper()
 timer := time.NewTimer(5*time.Second)
 defer timer.Stop()
 for {
  select {
  case v := <-s.Events():
   if !v.Busy { return v }
  case <-timer.C:
   t.Fatal("service did not become ready")
   return View{}
  }
 }
}
func submitWait(t *testing.T, s *Service, r Request) View {
 t.Helper()
 if !s.Submit(r) { t.Fatalf("submit rejected: %+v", r) }
 return waitReady(t, s)
}
func TestContextPersistsWithoutReplaying(t *testing.T) {
 root, home := t.TempDir(), t.TempDir()
 p := providerFunc(func(context.Context, []Message) (Message, error) {
  t.Error("restore called provider")
  return Message{}, nil
 })
 s := Start(root, home, p)
 waitReady(t, s)
 submitWait(t, s, Request{"remember", "Keep Go and benchmark every change"})
 submitWait(t, s, Request{"task", "Improve typing latency"})
 s.SetDraft("unsent draft")
 if err := s.Close(); err != nil { t.Fatal(err) }
 s = Start(root, home, p)
 defer s.Close()
 v := waitReady(t, s)
 if v.Session.Draft != "unsent draft" || v.Session.Task != "Improve typing latency" || len(v.Session.Notes) != 1 { t.Fatalf("lost context: %+v", v.Session) }
}
func TestRunToolsProposeWithoutWriting(t *testing.T) {
 root, home := t.TempDir(), t.TempDir()
 original := sourceWithBranches(2)
 if err := os.WriteFile(filepath.Join(root, "main.go"), original, 0600); err != nil { t.Fatal(err) }
 calls := 0
 p := providerFunc(func(ctx context.Context, m []Message) (Message, error) {
  calls++
  if calls > 1 { return Message{Role:"assistant", Content:"Please review the proposal."}, nil }
  var call ToolCall
  call.ID = "edit-1"
  call.Type = "function"
  call.Function.Name = "propose_edit"
  args, _ := json.Marshal(map[string]string{"path":"main.go", "original_hash":Hash(original), "replacement":string(sourceWithBranches(0))})
  call.Function.Arguments = string(args)
  return Message{Role:"assistant", ToolCalls:[]ToolCall{call}}, nil
 })
 s := Start(root, home, p)
 defer s.Close()
 waitReady(t, s)
 submitWait(t, s, Request{"include", "main.go"})
 v := submitWait(t, s, Request{"run", "Reduce complexity"})
 if v.Session.Status != "review" || len(v.Session.Proposals) != 1 { t.Fatalf("run did not propose: %+v", v.Session) }
 data, _ := os.ReadFile(filepath.Join(root, "main.go"))
 if string(data) != string(original) { t.Fatal("agent wrote source without user approval") }
 if v.Session.Evidence.TestsPassed || v.Session.Reward.NewCredit != 0 { t.Fatal("model invented evidence or reward") }
}
func TestCancellationAndBoundedSubmission(t *testing.T) {
 entered := make(chan struct{})
 p := providerFunc(func(ctx context.Context, _ []Message) (Message, error) {
  close(entered)
  <-ctx.Done()
  return Message{}, ctx.Err()
 })
 s := Start(t.TempDir(), t.TempDir(), p)
 defer s.Close()
 waitReady(t, s)
 if !s.Submit(Request{"run", "test cancellation"}) { t.Fatal("run rejected") }
 select {
 case <-entered:
 case <-time.After(5*time.Second): t.Fatal("provider not called")
 }
 if s.Submit(Request{"index", ""}) { t.Fatal("unbounded concurrent run accepted") }
 s.Cancel()
 v := waitReady(t, s)
 if v.Session.Status != "cancelled" { t.Fatalf("cancel: %+v", v.Session) }
}
func TestProviderProtocolAndNoRedirect(t *testing.T) {
 srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  if r.Header.Get("Authorization") != "Bearer test-key" { t.Error("missing authorization") }
  var payload struct {
   Tools []any `json:"tools"`
   Messages []Message `json:"messages"`
  }
  if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.Tools) != 5 { t.Error("bad request") }
  w.Header().Set("Content-Type", "application/json")
  w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Done"}}]}`))
 }))
 defer srv.Close()
 p, err := NewHTTPProvider(srv.URL, "test-model", "test-key")
 if err != nil { t.Fatal(err) }
 m, err := p.Complete(context.Background(), []Message{{Role:"user", Content:"hello"}})
 if err != nil || m.Content != "Done" { t.Fatalf("response: %+v %v", m, err) }
 for _, endpoint := range []string{"http://example.com/v1", "https://user:pass@example.com/v1", "https://example.com/v1?key=secret"} {
  if _, err := NewHTTPProvider(endpoint, "test", ""); err == nil { t.Errorf("unsafe endpoint %s", endpoint) }
 }
 redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, srv.URL, http.StatusFound) }))
 defer redirect.Close()
 p, _ = NewHTTPProvider(redirect.URL, "test-model", "test-key")
 _, err = p.Complete(context.Background(), []Message{{Role:"user", Content:"hello"}})
 if err == nil || strings.Contains(err.Error(), "test-key") { t.Fatalf("redirect or secret leak: %v", err) }
}
func TestToolCannotExecuteCommandsOrReadUnincludedSource(t *testing.T) {
 root := t.TempDir()
 os.WriteFile(filepath.Join(root, "main.go"), sourceWithBranches(0), 0600)
 w := worker{service:&Service{root:root}, session:Session{Version:SchemaVersion}}
 for _, name := range []string{"shell", "read_file", "propose_edit"} {
  var call ToolCall
  call.Function.Name = name
  call.Function.Arguments = `{"path":"main.go"}`
  if _, err := w.tool(context.Background(), call); err == nil { t.Errorf("unauthorised %s", name) }
 }
 var call ToolCall
 call.Function.Name = "request_checks"
 call.Function.Arguments = `{}`
 out, err := w.tool(context.Background(), call)
 if err != nil || !strings.Contains(out, "no command executed") || w.session.Evidence.TestsPassed { t.Fatalf("checks ran implicitly: %s %v", out, err) }
}
