package contextgraph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func waitReady(t *testing.T, s *Service) View {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case v := <-s.Events():
			if !v.Busy {
				return v
			}
		case <-timer.C:
			t.Fatal("context service did not finish")
			return View{}
		}
	}
}
func submitWait(t *testing.T, s *Service, r Request) View {
	t.Helper()
	if !s.Submit(r) {
		t.Fatalf("rejected %+v", r)
	}
	return waitReady(t, s)
}
func TestContextPersistsWithoutModelRuntime(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(root, "main.go"), sourceWithBranches(0), 0600)
	s := Start(root, home)
	waitReady(t, s)
	submitWait(t, s, Request{"remember", "Keep Go and measure allocations"})
	submitWait(t, s, Request{"task", "Improve typing latency"})
	submitWait(t, s, Request{"include", "main.go"})
	v := submitWait(t, s, Request{"index", ""})
	if v.Error != "" || v.GraphFiles != 1 {
		t.Fatalf("index: %+v", v)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = Start(root, home)
	defer s.Close()
	v = waitReady(t, s)
	if v.Session.Task != "Improve typing latency" || len(v.Session.Notes) != 1 || v.GraphFiles != 1 {
		t.Fatalf("lost context: %+v", v)
	}
	if !strings.Contains(v.ProviderContext, "Keep Go") || !strings.Contains(v.ProviderContext, "main.go") {
		t.Fatal("provider context omitted approved metadata")
	}
	if strings.Contains(v.ProviderContext, "return x") {
		t.Fatal("raw source included implicitly")
	}
	v = submitWait(t, s, Request{"run", "not a model worker"})
	if v.Error == "" {
		t.Fatal("second model runtime admitted")
	}
}
func TestContextSnapshotChecksAndStaleReward(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module sample\n\ngo 1.23\n"), 0600)
	source := filepath.Join(root, "main.go")
	os.WriteFile(source, sourceWithBranches(12), 0600)
	os.WriteFile(filepath.Join(root, "main_test.go"), []byte("package sample\nimport (\"testing\";\"os\")\nfunc TestWork(t *testing.T){if err:=os.WriteFile(\"only-in-snapshot.txt\",[]byte(\"x\"),0600);err!=nil{t.Fatal(err)}}\n"), 0600)
	s := Start(root, home)
	defer s.Close()
	waitReady(t, s)
	submitWait(t, s, Request{"index", ""})
	os.WriteFile(source, sourceWithBranches(9), 0600)
	v := submitWait(t, s, Request{"check", ""})
	if v.Error != "" {
		t.Fatalf("checks: %s", v.Error)
	}
	if !v.Session.Evidence.TestsPassed || !v.Session.Evidence.VetPassed {
		t.Fatal("missing evidence")
	}
	if _, err := os.Stat(filepath.Join(root, "only-in-snapshot.txt")); !os.IsNotExist(err) {
		t.Fatal("checks executed in live tree")
	}
	v = submitWait(t, s, Request{"reward", ""})
	if v.Session.Reward.NewCredit != 15 {
		t.Fatalf("wrong reward: %+v", v.Session.Reward)
	}
	v = submitWait(t, s, Request{"reward", ""})
	if v.Session.Reward.NewCredit != 0 {
		t.Fatal("duplicate credit")
	}
	os.WriteFile(source, sourceWithBranches(8), 0600)
	v = submitWait(t, s, Request{"reward", ""})
	if v.Session.Reward.NewCredit != 0 || !strings.Contains(v.Session.Reward.Reason, "exact snapshot") {
		t.Fatal("stale evidence rewarded")
	}
}
func TestContextBudgetExcludeAndForget(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(root, "main.go"), sourceWithBranches(1), 0600)
	s := Start(root, home)
	defer s.Close()
	waitReady(t, s)
	for i := 0; i < 4; i++ {
		v := submitWait(t, s, Request{"remember", strings.Repeat("a", 2048)})
		if v.Error != "" {
			t.Fatal(v.Error)
		}
	}
	if v := submitWait(t, s, Request{"remember", "over limit"}); v.Error == "" {
		t.Fatal("unbounded notes")
	}
	submitWait(t, s, Request{"include", "main.go"})
	submitWait(t, s, Request{"index", ""})
	v := submitWait(t, s, Request{"exclude", "main.go"})
	if strings.Contains(v.ProviderContext, "main.go") {
		t.Fatal("excluded metadata retained")
	}
	v = submitWait(t, s, Request{"forget", ""})
	if v.ProviderContext != "" || len(v.Session.Notes) != 0 || v.GraphFiles != 1 {
		t.Fatal("forget discarded graph or retained notes")
	}
}
func TestContextRejectsCorruptCheckpointWithoutOverwrite(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	store, err := OpenStore(root, home)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Dir, "session.json")
	os.WriteFile(path, []byte("bad json"), 0600)
	store.Close()
	s := Start(root, home)
	v := waitReady(t, s)
	if v.Error == "" {
		t.Fatal("corruption hidden")
	}
	s.Close()
	got, _ := os.ReadFile(path)
	if string(got) != "bad json" {
		t.Fatal("corrupt checkpoint overwritten")
	}
}

func TestContextIndexExcludesItsOwnNestedState(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "state")
	os.WriteFile(filepath.Join(root, "main.go"), sourceWithBranches(0), 0600)
	s := Start(root, home)
	defer s.Close()
	waitReady(t, s)
	v := submitWait(t, s, Request{"index", ""})
	hash := v.GraphHash
	v = submitWait(t, s, Request{"index", ""})
	if v.GraphFiles != 1 || v.GraphHash != hash || v.Stats.Parsed != 0 {
		t.Fatalf("state indexed itself: %+v", v)
	}
}
