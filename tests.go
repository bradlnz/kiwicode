package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type testCase struct {
	name, path string
	row        int
	checked    bool
}

var testCall = regexp.MustCompile("\\b(?:test|it)\\s*\\(\\s*['\"`]([^'\"`]+)")
var methodCall = regexp.MustCompile(`([A-Za-z_]\w*)\s*\(`)

func discoverTests(path string) []testCase {
	ext := strings.ToLower(filepath.Ext(path))
	var tests []testCase
	if ext == ".js" || ext == ".jsx" || ext == ".ts" || ext == ".tsx" {
		if data, err := os.ReadFile(path); err == nil {
			for _, match := range testCall.FindAllSubmatchIndex(data, -1) {
				tests = append(tests, testCase{name: string(data[match[2]:match[3]]), path: path, row: bytes.Count(data[:match[0]], []byte{'\n'}) + 1, checked: true})
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
				tests = append(tests, testCase{name: symbol.name, path: path, row: symbol.row, checked: true})
			}
		}
	}
	if len(tests) == 0 {
		tests = append(tests, testCase{name: filepath.Base(path), path: path, checked: true})
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
			tests = append(tests, testCase{name: matches[len(matches)-1][1], path: path, row: row + 1, checked: true})
			pending = false
		}
	}
	return tests
}

func testLabel(test testCase) string {
	box := settings.icons["test_unchecked"]
	if test.checked {
		box = settings.icons["test_checked"]
	}
	return box + " " + test.name
}

const testRunButton = "[Run] "

func testRowLabel(test testCase) string { return testRunButton + testLabel(test) }

func testRowAction(column int, test testCase) string {
	offset := column - 3
	if offset >= 0 && offset < len([]rune(testRunButton))-1 {
		return "run"
	}
	box := settings.icons["test_unchecked"]
	if test.checked {
		box = settings.icons["test_checked"]
	}
	if start := len([]rune(testRunButton)); offset >= start && offset < start+len([]rune(box)) {
		return "toggle"
	}
	return "open"
}
