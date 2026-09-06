package main

import (
 "code-editor/internal/agent"
 "os"
 "path/filepath"
 "strings"
 "testing"
 "time"
)

func agentFixture(t *testing.T) *agentWorkspace {
 t.Helper()
 old := mustCwd()
 root := t.TempDir()
 if err := os.Chdir(root); err != nil { t.Fatal(err) }
 resetSettings()
 t.Cleanup(func() { os.Chdir(old); resetSettings() })
 data := []byte("package sample\nfunc Work() {}\n")
 if err := os.WriteFile("main.go", data, 0600); err != nil { t.Fatal(err) }
 e := &editor{buffers:[]*buffer{newBuffer("main.go", data)}, rows:24, cols:100}
 w := &agentWorkspace{editor:e, root:root, kind:workspaceAgentTab, service:agent.Start(root,t.TempDir(),nil)}
 deadline := time.After(5*time.Second)
 for {
  select {
  case v := <-w.service.Events():
   w.view = v
   if !v.Busy { t.Cleanup(w.close); return w }
  case <-deadline:
   t.Fatal("agent startup timeout")
  }
 }
}
func TestAgentInputCannotMutateFile(t *testing.T) {
 w := agentFixture(t)
 b := w.editor.current()
 original := strings.Join(runeLines(b.lines), "\n")
 w.handle(key{r:'X'})
 w.handle(key{code:keyBackspace})
 w.handle(key{r:19})
 w.handle(key{r:26})
 w.handle(key{r:23})
 if b.dirty || strings.Join(runeLines(b.lines), "\n") != original || len(w.editor.buffers) != 1 { t.Fatal("agent input mutated underlying file") }
 w.handle(key{r:1})
 if w.kind != workspaceFileTab { t.Fatal("agent shortcut cannot return to file") }
}
func TestAgentApplyProtectsDirtyAndStaleBuffers(t *testing.T) {
 w := agentFixture(t)
 b := w.editor.current()
 original, _ := os.ReadFile("main.go")
 p := agent.Proposal{ID:"proposal", Path:"main.go", OriginalHash:agent.Hash(original), Original:string(original), Replacement:"package sample\nfunc Work() { println(1) }\n", Status:"awaiting review"}
 w.view.Session.Proposals = []agent.Proposal{p}
 b.insert([]rune("user edit"))
 if err := w.apply(p.ID); err == nil { t.Fatal("dirty buffer overwritten") }
 b.undoChange()
 if err := os.WriteFile("main.go", []byte("package changed\n"), 0600); err != nil { t.Fatal(err) }
 if err := w.apply(p.ID); err == nil { t.Fatal("stale disk overwritten") }
 if err := os.WriteFile("main.go", original, 0600); err != nil { t.Fatal(err) }
 if err := w.apply(p.ID); err != nil { t.Fatal(err) }
 disk, _ := os.ReadFile(filepath.Join(w.root, "main.go"))
 if string(disk) != string(original) || !b.dirty { t.Fatal("apply should modify buffer, not disk") }
 if !b.undoChange() || strings.Join(runeLines(b.lines), "\n") != string(original) { t.Fatal("proposal was not undoable") }
}
func TestAgentTabHitTesting(t *testing.T) {
 w := agentFixture(t)
 var out strings.Builder
 w.paintTabs(&out)
 for _, tab := range w.tabs {
  if tab.kind == workspaceFileTab { w.handle(key{mouse:true, y:2, x:tab.left, button:0}); break }
 }
 if w.kind != workspaceFileTab { t.Fatal("file tab click ignored") }
 out.Reset()
 w.paintTabs(&out)
 for _, tab := range w.tabs {
  if tab.kind == workspaceAgentTab { w.handle(key{mouse:true, y:2, x:tab.left, button:0}); break }
 }
 if w.kind != workspaceAgentTab { t.Fatal("Agent tab click ignored") }
}
func TestAgentDisplayRejectsTerminalControls(t *testing.T) {
 got := fitAgentText("model\x1b[2J\x07output", 80)
 if strings.ContainsAny(got, "\x1b\x07") { t.Fatalf("untrusted terminal controls rendered: %q", got) }
}
