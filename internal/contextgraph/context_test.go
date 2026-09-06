package contextgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sourceWithBranches(n int) []byte {
	return []byte("package sample\nfunc Work(v int) int {\n" + strings.Repeat("if v > 0 { v-- }\n", n) + "return v\n}\n")
}
func graphOf(data []byte) *Graph {
	f, _ := Analyze("main.go", data, nil)
	return &Graph{Version: SchemaVersion, Files: map[string]*File{"main.go": f}, Hash: Hash(data)}
}
func TestAnalyzeFactsAndReuse(t *testing.T) {
	data := []byte("package sample\nimport \"fmt\"\n// TODO: improve later\ntype Value struct{}\nfunc (v *Value) Run(ok bool) { if ok { fmt.Println(ok) } }\n")
	f, changed := Analyze("main.go", data, nil)
	if !changed || f.ParseError || len(f.Symbols) != 2 || f.Symbols[1].Name != "Value.Run" || f.Symbols[1].Complexity != 2 {
		t.Fatalf("bad graph: %+v", f)
	}
	kinds := map[string]bool{}
	for _, e := range f.Edges {
		kinds[e.Kind] = true
	}
	if !kinds["imports"] || !kinds["declares"] || !kinds["calls_syntactic"] || len(f.Quality) != 1 || len(f.Findings) != 1 {
		t.Fatalf("missing facts: %+v", f)
	}
	same, changed := Analyze("main.go", data, f)
	if changed || same != f {
		t.Fatal("unchanged AST reparsed")
	}
	bad, _ := Analyze("bad.go", []byte("package !"), nil)
	if !bad.ParseError {
		t.Fatal("parse error hidden")
	}
}
func TestScanIncrementalAndCancelled(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("f%d.go", i)), sourceWithBranches(i), 0600); err != nil {
			t.Fatal(err)
		}
	}
	first, stats, err := Scan(ctx, root, nil)
	if err != nil || stats.Parsed != 3 {
		t.Fatalf("first scan: %+v %v", stats, err)
	}
	next, stats, err := Scan(ctx, root, first)
	if err != nil || stats.Reused != 3 || stats.Parsed != 0 || first.Hash != next.Hash {
		t.Fatalf("reuse: %+v %v", stats, err)
	}
	if next.Files["f1.go"] != first.Files["f1.go"] {
		t.Fatal("unchanged node copied")
	}
	if err := os.WriteFile(filepath.Join(root, "f1.go"), sourceWithBranches(7), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "f2.go")); err != nil {
		t.Fatal(err)
	}
	next, stats, err = Scan(ctx, root, next)
	if err != nil || stats.Parsed != 1 || stats.Removed != 1 || stats.Reused != 1 {
		t.Fatalf("change: %+v %v", stats, err)
	}
	if len(first.Files) != 3 || first.Hash == next.Hash {
		t.Fatal("previous graph mutated")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := Scan(cancelled, root, next); err != context.Canceled {
		t.Fatalf("cancel: %v", err)
	}
}
func TestContextPathGuards(t *testing.T) {
	for _, p := range []string{"../a.go", "/a.go", "a/../b.go", ".env", "secrets/a.go", "a/credentials.json", "x\x1b.go", "a\\b.go"} {
		if AllowedPath(p) {
			t.Errorf("accepted %q", p)
		}
	}
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "source.go")
	if err := os.WriteFile(outside, sourceWithBranches(0), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSource(root, "link.go"); err == nil {
		t.Fatal("symlink read")
	}
	if err := os.WriteFile(filepath.Join(root, "key.txt"), []byte("-----BEGIN "+"PRIVATE KEY-----"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSource(root, "key.txt"); err == nil {
		t.Fatal("private key accepted")
	}
}
func TestStoreRoundTripLockAndSize(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	s, err := OpenStore(root, home)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if other, err := OpenStore(root, home); err == nil {
		other.Close()
		t.Fatal("duplicate state owner")
	}
	original := Session{Version: SchemaVersion, Task: "speed up editor", Notes: []string{"Keep Go"}, Status: "ready"}
	if err := s.Save("session.json", original, MaxStateBytes); err != nil {
		t.Fatal(err)
	}
	var restored Session
	if err := s.Load("session.json", &restored, MaxStateBytes); err != nil || restored.Task != original.Task {
		t.Fatalf("restore: %+v %v", restored, err)
	}
	if err := s.Save("../outside", original, MaxStateBytes); err == nil {
		t.Fatal("state traversal")
	}
	if err := s.Save("session.json", original, 1); err == nil {
		t.Fatal("size limit bypass")
	}
	var still Session
	s.Load("session.json", &still, MaxStateBytes)
	if still.Task != original.Task {
		t.Fatal("failed save destroyed checkpoint")
	}
	info, err := os.Stat(filepath.Join(s.Dir, "session.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v %v", info, err)
	}
}
func TestRewardRequiresEvidenceAndCannotFarm(t *testing.T) {
	base, current := graphOf(sourceWithBranches(12)), graphOf(sourceWithBranches(9))
	evidence := Evidence{Snapshot: current.Hash, TestsPassed: true, VetPassed: true, Reviewed: true}
	got := Evaluate(base, current, evidence, 0)
	if got.Score != 15 || got.NewCredit != 15 || len(got.Components) != 1 {
		t.Fatalf("reward: %+v", got)
	}
	if got = Evaluate(base, current, evidence, 15); got.NewCredit != 0 {
		t.Fatal("duplicate credit")
	}
	evidence.Snapshot = "stale"
	if got = Evaluate(base, current, evidence, 0); got.NewCredit != 0 || got.Score != 0 {
		t.Fatal("stale evidence rewarded")
	}
	evidence.Snapshot = base.Hash
	got = Evaluate(base, base, evidence, 15)
	if got.NewCredit != 0 || got.Score != 0 {
		t.Fatal("revert farm rewarded")
	}
	worse := graphOf(sourceWithBranches(15))
	evidence.Snapshot = worse.Hash
	if got = Evaluate(base, worse, evidence, 0); got.Score != -15 || got.NewCredit != 0 {
		t.Fatalf("new debt: %+v", got)
	}
	for _, candidate := range []string{"package sample\n", "package sample\nfunc Renamed() {}", "package !"} {
		c := graphOf([]byte(candidate))
		evidence.Snapshot = c.Hash
		if got = Evaluate(base, c, evidence, 0); got.NewCredit != 0 {
			t.Fatalf("unsafe credit: %+v", got)
		}
	}
}
func TestRewardRejectsChangedOrDeletedTests(t *testing.T) {
	base, current := graphOf(sourceWithBranches(12)), graphOf(sourceWithBranches(9))
	f, _ := Analyze("main_test.go", []byte("package sample\nfunc TestExample(){}"), nil)
	base.Files[f.Path] = f
	current.Files[f.Path] = f
	evidence := Evidence{Snapshot: current.Hash, TestsPassed: true, VetPassed: true, Reviewed: true}
	if Evaluate(base, current, evidence, 0).NewCredit != 15 {
		t.Fatal("unchanged tests withheld credit")
	}
	copyFile := *f
	copyFile.Hash = "different"
	current.Files[f.Path] = &copyFile
	if Evaluate(base, current, evidence, 0).NewCredit != 0 {
		t.Fatal("test modification rewarded")
	}
	delete(current.Files, f.Path)
	if Evaluate(base, current, evidence, 0).NewCredit != 0 {
		t.Fatal("test deletion rewarded")
	}
}
func TestGraphOmitsSourceText(t *testing.T) {
	marker := "private contextual comment not for graph persistence"
	f, _ := Analyze("main.go", []byte("package sample\n// "+marker+"\nfunc Work(){}"), nil)
	data, err := json.Marshal(f)
	if err != nil || strings.Contains(string(data), marker) {
		t.Fatalf("source retained in graph: %s %v", data, err)
	}
}
func BenchmarkAnalyzeUnchanged(b *testing.B) {
	data := sourceWithBranches(30)
	f, _ := Analyze("main.go", data, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Analyze("main.go", data, f)
	}
}
func BenchmarkAnalyzeChanged(b *testing.B) {
	data := sourceWithBranches(30)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Analyze("main.go", data, nil)
	}
}
