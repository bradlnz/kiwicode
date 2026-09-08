package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type testCase struct {
	name, path string
	row        int
}

var testCall = regexp.MustCompile("\\b(?:test|it)\\s*\\(\\s*['\"`]([^'\"`]+)")
var methodCall = regexp.MustCompile(`([A-Za-z_]\w*)\s*\(`)

func discoverTests(path string) []testCase {
	ext := strings.ToLower(filepath.Ext(path))
	var tests []testCase
	if ext == ".js" || ext == ".jsx" || ext == ".ts" || ext == ".tsx" {
		if data, err := os.ReadFile(path); err == nil {
			for _, match := range testCall.FindAllSubmatchIndex(data, -1) {
				tests = append(tests, testCase{name: string(data[match[2]:match[3]]), path: path, row: bytes.Count(data[:match[0]], []byte{'\n'}) + 1})
			}
		}
	} else if ext == ".cs" || ext == ".java" || ext == ".kt" || ext == ".rs" {
		tests = annotatedTests(path)
	} else {
		for _, symbol := range fileSymbols(path) {
			name := strings.ToLower(symbol.name)
			valid := false
			switch ext {
			case ".go":
				valid = strings.HasPrefix(symbol.name, "Test") || strings.HasPrefix(symbol.name, "Benchmark") || strings.HasPrefix(symbol.name, "Fuzz") || strings.HasPrefix(symbol.name, "Example")
			case ".py":
				valid = strings.HasPrefix(name, "test_")
			default:
				valid = strings.HasPrefix(name, "test") || strings.Contains(name, "_test") || strings.Contains(name, "spec")
			}
			if valid {
				tests = append(tests, testCase{name: symbol.name, path: path, row: symbol.row})
			}
		}
	}
	if len(tests) == 0 {
		tests = append(tests, testCase{name: filepath.Base(path), path: path})
	}
	return tests
}

func annotatedTests(path string) []testCase {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	pending := false
	var tests []testCase
	for row, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(lower, "[fact") || strings.HasPrefix(lower, "[theory") || strings.HasPrefix(lower, "[testmethod") || strings.HasPrefix(lower, "@test") || lower == "#[test]" {
			pending = true
			continue
		}
		if !pending || strings.HasPrefix(lower, "[") || strings.HasPrefix(lower, "@") || lower == "" {
			continue
		}
		matches := methodCall.FindAllStringSubmatch(line, -1)
		if len(matches) > 0 {
			tests = append(tests, testCase{name: matches[len(matches)-1][1], path: path, row: row + 1})
			pending = false
		}
	}
	return tests
}

func testRowLabel(test testCase) string { return test.name }

const testButtonWidth = 8

func testTarget(test testCase) string {
	return test.path + ":" + strconv.Itoa(test.row) + ":" + test.name
}

func (e *editor) testButtonLabel(test testCase) string {
	if e.shell.running && e.shell.testRun && e.shell.testTarget == testTarget(test) {
		return " [Stop] "
	}
	return " [Run]  "
}

var testClassDeclaration = regexp.MustCompile(`\bclass\s+([A-Za-z_]\w*)`)
var testNamespaceDeclaration = regexp.MustCompile(`\bnamespace\s+([A-Za-z_][\w.]*)`)

func selectedTestCommand(test testCase) string {
	if settings.selectedTestCommand != "" {
		return strings.NewReplacer("{file}", shellArg(test.path), "{name}", shellArg(test.name), "{line}", strconv.Itoa(test.row)).Replace(settings.selectedTestCommand)
	}
	if settings.testCommand != "" || test.row <= 0 {
		return ""
	}
	pattern := "^" + regexp.QuoteMeta(test.name) + "$"
	switch strings.ToLower(filepath.Ext(test.path)) {
	case ".go":
		dir := filepath.ToSlash(filepath.Dir(test.path))
		if !filepath.IsAbs(dir) {
			dir = "./" + dir
		}
		if strings.HasPrefix(test.name, "Benchmark") {
			return "go test " + shellArg(dir) + " -run '^$' -bench " + shellArg(pattern) + " -count=1"
		}
		return "go test " + shellArg(dir) + " -run " + shellArg(pattern) + " -count=1"
	case ".py", ".cs":
		data, err := os.ReadFile(test.path)
		if err != nil {
			return ""
		}
		lines := strings.Split(string(data), "\n")
		if test.row > len(lines) {
			return ""
		}
		class, namespace := "", ""
		for _, line := range lines[:test.row] {
			if match := testClassDeclaration.FindStringSubmatch(line); match != nil {
				class = match[1]
			}
			if match := testNamespaceDeclaration.FindStringSubmatch(line); match != nil {
				namespace = match[1] + "."
			}
		}
		// ponytail: ordinary single-level test classes; nested classes need tests.selected_command.
		if filepath.Ext(test.path) == ".cs" {
			if class == "" {
				return ""
			}
			return "dotnet test --filter " + shellArg("FullyQualifiedName="+namespace+class+"."+test.name)
		}
		node := test.path + "::"
		if line := lines[test.row-1]; len(line) > len(strings.TrimLeft(line, " \t")) && class != "" {
			node += class + "::"
		}
		return "python -m pytest " + shellArg(node+test.name)
	case ".java":
		class := strings.TrimSuffix(filepath.Base(test.path), ".java")
		return "mvn test " + shellArg("-Dtest="+class+"#"+test.name)
	case ".js", ".jsx", ".ts", ".tsx":
		var pkg struct{ Scripts map[string]string }
		data, err := os.ReadFile("package.json")
		if err != nil || json.Unmarshal(data, &pkg) != nil {
			return ""
		}
		runner := projectTestCommand()
		if runner == "" {
			return ""
		}
		separator := " "
		if runner == "npm test" || runner == "pnpm test" {
			separator = " -- "
		}
		// Jest/Vitest prepend describe names; match the leaf name at the end.
		pattern = "(^| )" + regexp.QuoteMeta(test.name) + "$"
		switch script := pkg.Scripts["test"]; {
		case strings.Contains(script, "vitest"):
			return runner + separator + "--run " + shellArg(test.path) + " -t " + shellArg(pattern)
		case strings.Contains(script, "jest"):
			return runner + separator + "--watch=false --runTestsByPath " + shellArg(test.path) + " --testNamePattern " + shellArg(pattern)
		}
	}
	return ""
}

func projectTestCommand() string {
	if settings.testCommand != "" {
		return settings.testCommand
	}
	// ponytail: root manifests cover common projects; use tests.command for monorepos or custom runners.
	for _, runner := range []struct{ manifest, command string }{
		{"go.mod", "go test ./..."},
		{"Cargo.toml", "cargo test"},
		{"*.sln", "dotnet test"},
		{"*.slnx", "dotnet test"},
		{"*.csproj", "dotnet test"},
		{"pytest.ini", "python -m pytest"},
		{"pyproject.toml", "python -m pytest"},
		{"pom.xml", "mvn test"},
		{"Gemfile", "bundle exec rake test"},
	} {
		if paths, _ := filepath.Glob(runner.manifest); len(paths) > 0 {
			return runner.command
		}
	}
	var pkg struct{ Scripts map[string]string }
	if data, err := os.ReadFile("package.json"); err == nil && json.Unmarshal(data, &pkg) == nil && pkg.Scripts["test"] != "" {
		for _, manager := range []struct{ lock, command string }{
			{"pnpm-lock.yaml", "pnpm test"}, {"yarn.lock", "yarn test"},
			{"bun.lock", "bun run test"}, {"bun.lockb", "bun run test"},
		} {
			if _, err := os.Stat(manager.lock); err == nil {
				return manager.command
			}
		}
		return "npm test"
	}
	return ""
}

func (e *editor) runTests(selected ...testCase) {
	target := ""
	if len(selected) > 0 {
		target = testTarget(selected[0])
	}
	if e.shell.running {
		e.shell.open, e.shell.focused, e.shell.interactive = true, true, false
		if e.shell.testRun && (target == "" || target == e.shell.testTarget) {
			e.shell.stop()
		} else {
			e.status = "A command is already running; Ctrl+C stops it"
		}
		return
	}
	if e.dirty() {
		e.status = "Save your changes before running tests"
		return
	}
	command := projectTestCommand()
	if len(selected) > 0 {
		command = selectedTestCommand(selected[0])
		if command == "" {
			e.status = "Set tests.selected_command with {file}, {name}, {line} in .code-editor.yaml"
			return
		}
	}
	if command == "" {
		e.status = "Set tests.command in .code-editor.yaml to run this project's tests"
		return
	}
	e.clearModalViews()
	e.shell.open, e.shell.focused, e.shell.scroll = true, true, 0
	e.shell.start(command)
	e.shell.testRun = true
	e.shell.testTarget = target
	e.status = "Tests: " + command
}
