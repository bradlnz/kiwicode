package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type definitionResult struct {
	word, path string
	row        int
	found      bool
}

type definitionLocation struct {
	path string
	row  int
}

func (e *editor) goToDefinition() {
	if e.definitionLoading {
		e.status = "Go to Definition: loading…"
		return
	}
	word := wordAtCursor(e.current())
	if word == "" {
		e.status = "Place the cursor on a symbol"
		return
	}
	current := e.current().path
	if completion, ok := e.completionCache[current]; ok {
		if definition, found := completion.definitions[word]; found && !e.completionDirty {
			e.open(definition.path)
			e.highlightDefinition(word, definition.row, definition.path)
			return
		}
	}
	currentSource := []byte(strings.Join(runeLines(e.current().lines), "\n"))
	if row := definitionInSource(current, currentSource, word); row > 0 {
		e.highlightDefinition(word, row, current)
		return
	}
	if definition, found := e.completionCache[current].definitions[word]; found {
		e.open(definition.path)
		e.highlightDefinition(word, definition.row, definition.path)
		return
	}
	for path, completion := range e.completionCache {
		if path == current || completionFamily(filepath.Ext(path)) != completionFamily(filepath.Ext(current)) {
			continue
		}
		if definition, found := completion.definitions[word]; found {
			e.open(definition.path)
			e.highlightDefinition(word, definition.row, definition.path)
			return
		}
	}
	if e.definitionDone == nil {
		e.definitionDone = make(chan definitionResult, 1)
	}
	e.definitionLoading = true
	e.status = "Go to Definition: loading…"
	files, sources := append([]string(nil), e.files...), e.completionSources()
	go func() {
		path, row, found := findDefinition(current, word, files, sources)
		e.definitionDone <- definitionResult{word, path, row, found}
	}()
}

func (e *editor) pollDefinition() bool {
	if e.definitionDone == nil {
		return false
	}
	select {
	case result := <-e.definitionDone:
		e.definitionLoading = false
		if !result.found {
			e.status = "Definition not found for " + result.word
			return true
		}
		e.open(result.path)
		e.highlightDefinition(result.word, result.row, result.path)
		return true
	default:
		return false
	}
}

func (e *editor) highlightDefinition(word string, row int, path string) {
	b := e.current()
	b.row, b.col = min(max(0, row-1), len(b.lines)-1), 0
	e.selection = textSelection{buffer: b, startRow: b.row, endRow: b.row, endCol: len(b.lines[b.row])}
	e.status = "Definition: " + word + " · " + path
}

func wordAtCursor(b *buffer) string {
	line := b.lines[b.row]
	if len(line) == 0 {
		return ""
	}
	index := min(b.col, len(line)-1)
	if !identifierRune(line[index]) && strings.ContainsRune(".>-", line[index]) && index > 0 {
		index--
	}
	if !identifierRune(line[index]) {
		return ""
	}
	start, end := index, index+1
	for start > 0 && identifierRune(line[start-1]) {
		start--
	}
	for end < len(line) && identifierRune(line[end]) {
		end++
	}
	return strings.TrimPrefix(string(line[start:end]), "$")
}

func findDefinition(current, word string, files []string, sources map[string][]byte) (string, int, bool) {
	family, needle := completionFamily(filepath.Ext(current)), []byte(word)
	seen := map[string]bool{}
	for _, path := range files {
		if path == current || seen[path] || completionFamily(filepath.Ext(path)) != family {
			continue
		}
		seen[path] = true
		data, ok := sources[path]
		if !ok {
			var err error
			data, err = os.ReadFile(path)
			if err != nil {
				continue
			}
		}
		if bytes.Contains(data, needle) {
			if row := definitionInSource(path, data, word); row > 0 {
				return path, row, true
			}
		}
	}
	return "", 0, false
}

func definitionInSource(path string, data []byte, word string) int {
	return sourceDefinitionIndex(path, data)[word]
}

var localDefinitionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?mi)^\s*(?:var|let|const|val|dim)\s+\$?([A-Za-z_]\w*)\b`),
	regexp.MustCompile(`(?m)^\s*[A-Z][A-Za-z0-9_.:<>,?\[\]]*\s+[*&]?\$?([A-Za-z_]\w*)\b`),
	regexp.MustCompile(`(?m)^\s*\$?([A-Za-z_]\w*)\s*=`),
}

func sourceDefinitionIndex(path string, data []byte) map[string]int {
	definitions := map[string]int{}
	if completionFamily(filepath.Ext(path)) == "go" {
		set := token.NewFileSet()
		file, _ := parser.ParseFile(set, path, data, parser.AllErrors)
		if file != nil {
			ast.Inspect(file, func(node ast.Node) bool {
				var names []*ast.Ident
				switch value := node.(type) {
				case *ast.TypeSpec:
					names = []*ast.Ident{value.Name}
				case *ast.FuncDecl:
					names = []*ast.Ident{value.Name}
				case *ast.Field:
					names = value.Names
				case *ast.ValueSpec:
					names = value.Names
				case *ast.AssignStmt:
					for _, expression := range value.Lhs {
						if name, ok := expression.(*ast.Ident); ok {
							names = append(names, name)
						}
					}
				}
				for _, name := range names {
					if _, exists := definitions[name.Name]; !exists {
						definitions[name.Name] = set.Position(name.Pos()).Line
					}
				}
				return true
			})
		}
		return definitions
	}

	family := completionFamily(filepath.Ext(path))
	patterns := []*regexp.Regexp{modelTypePatterns[family]}
	patterns = append(patterns, modelMemberPatterns[family]...)
	for _, pattern := range patterns {
		if pattern == nil {
			continue
		}
		for _, match := range pattern.FindAllSubmatchIndex(data, -1) {
			if len(match) >= 4 && match[2] >= 0 {
				name := string(data[match[2]:match[3]])
				if _, exists := definitions[name]; !exists {
					definitions[name] = bytes.Count(data[:match[2]], []byte{'\n'}) + 1
				}
			}
		}
	}
	for _, pattern := range localDefinitionPatterns {
		for _, match := range pattern.FindAllSubmatchIndex(data, -1) {
			if len(match) >= 4 && match[2] >= 0 {
				name := string(data[match[2]:match[3]])
				if _, exists := definitions[name]; !exists {
					definitions[name] = bytes.Count(data[:match[2]], []byte{'\n'}) + 1
				}
			}
		}
	}
	return definitions
}
