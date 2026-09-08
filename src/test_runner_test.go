package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunTestsButton(t *testing.T) {
	oldSettings := settings
	t.Cleanup(func() { settings = oldSettings })
	resetSettings()
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("HOME", t.TempDir())
	for _, tc := range []struct{ manifest, data, command string }{
		{"go.mod", "module example", "go test ./..."},
		{"Cargo.toml", "", "cargo test"},
		{"app.csproj", "", "dotnet test"},
		{"pyproject.toml", "", "python -m pytest"},
		{"package.json", `{"scripts":{"test":"node --test"}}`, "npm test"},
		{"package.json", `{"scripts":{}}`, ""},
		{"package.json", `invalid`, ""},
	} {
		t.Chdir(t.TempDir())
		if err := os.WriteFile(tc.manifest, []byte(tc.data), 0600); err != nil {
			t.Fatal(err)
		}
		if got := projectTestCommand(); got != tc.command {
			t.Fatalf("%s: got %q, want %q", tc.manifest, got, tc.command)
		}
	}
	if err := applySetting("tests", "command", "printf runner-ok"); err != nil {
		t.Fatal(err)
	}
	e := &editor{rows: 30, cols: 100, buffers: []*buffer{newBuffer("sample.go", nil)}}
	e.current().dirty = true
	e.runTests()
	if e.shell.running || !strings.Contains(e.status, "Save") {
		t.Fatal("unsaved edits must not be silently excluded")
	}
	e.current().dirty = false
	x := 1
	for _, menu := range topMenus {
		x += topMenuWidth(menu)
	}
	if topActionAt(x) != "run-tests" || !strings.Contains(e.topBar(), "[Run Tests]") {
		t.Fatal("Run Tests button is not visible and clickable")
	}
	for _, command := range []string{"printf runner-ok", "exit 7"} {
		settings.testCommand = command
		e.handleMouse(key{button: 0, x: x, y: 1})
		if !e.shell.testRun || !e.shell.running || !strings.Contains(e.topBar(), "[Stop Tests]") {
			t.Fatalf("button did not start tests: %s", e.status)
		}
		select {
		case result := <-e.shell.done:
			e.shell.done <- result
		case <-time.After(5 * time.Second):
			e.shell.stop()
			t.Fatal("test command timed out")
		}
		e.shell.poll()
		want := "Tests passed"
		if command == "exit 7" {
			want = "Tests failed"
		}
		if got := e.shell.output[len(e.shell.output)-1]; got != want {
			t.Fatalf("result %q, want %q", got, want)
		}
	}
	e.shell.running, e.shell.testRun = true, false
	e.runTests()
	if !strings.Contains(e.status, "already running") {
		t.Fatal("busy command was not protected")
	}
	e.shell.running = false
	e.performAction("licenses")
	text := strings.Join(e.opsLines, "\n")
	for _, notice := range []string{"MIT License", "KiwiCode contributors", "Paul Evans", "The Go Authors"} {
		if !strings.Contains(text, notice) {
			t.Fatalf("missing embedded notice: %s", notice)
		}
	}
}

func TestSelectedTestButton(t *testing.T) {
	oldSettings := settings
	t.Cleanup(func() { settings = oldSettings })
	resetSettings()
	t.Setenv("SHELL", "/bin/sh")
	if os.Getenv("GOCACHE") == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("GOCACHE", filepath.Join(cache, "go-build"))
	}
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	for name, text := range map[string]string{
		"go.mod":         "module selected\n\ngo 1.27\n",
		"sample_test.go": "package selected\nimport \"testing\"\nfunc TestOnly(t *testing.T) {}\nfunc TestOnlyOther(t *testing.T) { t.Fatal(\"wrong test ran\") }\n",
		"checks.py":      "class TestWidget:\n    def test_one(self): pass\n\ndef test_top(): pass\n",
		"Checks.cs":      "namespace Example.Tests;\nclass WidgetTests {\n  public void Saves() {}\n}\n",
	} {
		if err := os.WriteFile(name, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		test testCase
		want string
	}{
		{testCase{name: "TestOnly", path: "sample_test.go", row: 3}, "go test './.' -run '^TestOnly$' -count=1"},
		{testCase{name: "BenchmarkOne", path: "pkg/check_test.go", row: 3}, "go test './pkg' -run '^$' -bench '^BenchmarkOne$' -count=1"},
		{testCase{name: "test_one", path: "checks.py", row: 2}, "python -m pytest 'checks.py::TestWidget::test_one'"},
		{testCase{name: "test_top", path: "checks.py", row: 4}, "python -m pytest 'checks.py::test_top'"},
		{testCase{name: "Saves", path: "Checks.cs", row: 3}, "dotnet test --filter 'FullyQualifiedName=Example.Tests.WidgetTests.Saves'"},
		{testCase{name: "unknown", path: "check.rs", row: 2}, ""},
	} {
		if got := selectedTestCommand(tc.test); got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.test.name, got, tc.want)
		}
	}
	e := &editor{rows: 30, cols: 100, showExplorer: true, testMode: true, buffers: []*buffer{newBuffer("sample_test.go", nil)}}
	e.tests = discoverTests("sample_test.go")
	if len(e.tests) != 2 {
		t.Fatalf("expected two tests, got %#v", e.tests)
	}
	e.handleMouse(key{button: 0, x: e.sidebarWidth() - 3, y: 3})
	if !e.shell.running || !e.shell.focused || e.testButtonLabel(e.tests[0]) != " [Stop] " || e.testButtonLabel(e.tests[1]) != " [Run]  " {
		t.Fatal("row button did not start only the selected test")
	}
	command := e.shell.cmd
	e.runTests(e.tests[1])
	if e.shell.cmd != command || e.shell.stopping {
		t.Fatal("another test's Run button interrupted the active test")
	}
	select {
	case result := <-e.shell.done:
		e.shell.done <- result
	case <-time.After(30 * time.Second):
		e.shell.stop()
		t.Fatal("selected test timed out")
	}
	e.shell.poll()
	if output := strings.Join(e.shell.output, "\n"); !strings.HasSuffix(output, "Tests passed") {
		t.Fatalf("selected test failed or ran its failing sibling: %s", output)
	}
	settings.testCommand = "custom-all"
	if selectedTestCommand(e.tests[0]) != "" {
		t.Fatal("custom all-tests command must not be used for a single test")
	}
	if err := applySetting("tests", "selected_command", "printf '%s|%s|%s' {file} {name} {line}"); err != nil {
		t.Fatal(err)
	}
	e.tests[0] = testCase{name: "it's $(false)", path: "some file.go", row: 9}
	e.performAction("run-selected-test")
	select {
	case result := <-e.shell.done:
		e.shell.done <- result
	case <-time.After(5 * time.Second):
		e.shell.stop()
		t.Fatal("custom test command timed out")
	}
	e.shell.poll()
	if output := strings.Join(e.shell.output, "\n"); !strings.Contains(output, "some file.go|it's $(false)|9\nTests passed") {
		t.Fatalf("custom selected-test arguments were not quoted: %s", output)
	}
}
