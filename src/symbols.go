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

type codeSymbol struct {
	name, path string
	row        int
}

var symbolPatterns = map[string][]*regexp.Regexp{
	".py":    {regexp.MustCompile(`(?m)^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)`), regexp.MustCompile(`(?m)^\s*class\s+([A-Za-z_]\w*)`)},
	".js":    {regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)`), regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=.*=>`)},
	".jsx":   {regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)`), regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=.*=>`)},
	".ts":    {regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)`), regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=.*=>`)},
	".tsx":   {regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)`), regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=.*=>`)},
	".rs":    {regexp.MustCompile(`(?m)^\s*(?:pub\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)`)},
	".cs":    {regexp.MustCompile(`(?m)^\s*(?:(?:public|private|protected|internal|static|async|virtual|override|sealed)\s+)*[\w<>,.?\[\]]+\s+([A-Za-z_]\w*)\s*\(`)},
	".fs":    {regexp.MustCompile(`(?m)^\s*let\s+(?:rec\s+)?([A-Za-z_]\w*)`)},
	".fsx":   {regexp.MustCompile(`(?m)^\s*let\s+(?:rec\s+)?([A-Za-z_]\w*)`)},
	".vb":    {regexp.MustCompile(`(?mi)^\s*(?:(?:public|private|protected|friend|shared|async)\s+)*(?:function|sub)\s+([A-Za-z_]\w*)`)},
	".java":  {regexp.MustCompile(`(?m)^\s*(?:(?:public|private|protected|static|final|synchronized|native|abstract)\s+)*[\w<>,.?\[\]]+\s+([A-Za-z_]\w*)\s*\(`)},
	".tf":    {regexp.MustCompile(`(?m)^\s*(?:resource|data|module|variable|output)\s+"([^"]+)"`)},
	".c":     {regexp.MustCompile(`(?m)^\s*(?:[\w*]+\s+)+([A-Za-z_]\w*)\s*\([^;]*\)\s*\{`)},
	".h":     {regexp.MustCompile(`(?m)^\s*(?:[\w*]+\s+)+([A-Za-z_]\w*)\s*\([^;]*\)\s*\{`)},
	".cpp":   {regexp.MustCompile(`(?m)^\s*(?:[\w:*&<>]+\s+)+([A-Za-z_]\w*)\s*\([^;]*\)\s*\{`)},
	".hpp":   {regexp.MustCompile(`(?m)^\s*(?:[\w:*&<>]+\s+)+([A-Za-z_]\w*)\s*\([^;]*\)\s*\{`)},
	".rb":    {regexp.MustCompile(`(?m)^\s*def\s+([A-Za-z_]\w*[!?=]?)`)},
	".php":   {regexp.MustCompile(`(?m)^\s*(?:(?:public|private|protected|static)\s+)*function\s+([A-Za-z_]\w*)`)},
	".kt":    {regexp.MustCompile(`(?m)^\s*(?:(?:public|private|protected|internal|suspend|inline)\s+)*fun\s+([A-Za-z_]\w*)`)},
	".swift": {regexp.MustCompile(`(?m)^\s*(?:(?:public|private|internal|fileprivate|open|static|class)\s+)*func\s+([A-Za-z_]\w*)`)},
}

func projectSymbols(files []string) []codeSymbol {
	var symbols []codeSymbol
	for _, path := range files {
		symbols = append(symbols, fileSymbols(path)...)
		if len(symbols) >= 2000 {
			return symbols[:2000]
		}
	}
	return symbols
}

func fileSymbols(path string) []codeSymbol {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 2<<20 {
		return nil
	}
	return sourceSymbols(path, data)
}

func sourceSymbols(path string, data []byte) []codeSymbol {
	if strings.EqualFold(filepath.Ext(path), ".go") {
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, path, data, 0)
		if err != nil {
			return nil
		}
		var symbols []codeSymbol
		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok {
				symbols = append(symbols, codeSymbol{name: function.Name.Name, path: path, row: set.Position(function.Pos()).Line})
			}
		}
		return symbols
	}
	var symbols []codeSymbol
	for _, pattern := range symbolPatterns[strings.ToLower(filepath.Ext(path))] {
		for _, match := range pattern.FindAllSubmatchIndex(data, -1) {
			if len(match) < 4 || match[2] < 0 {
				continue
			}
			symbols = append(symbols, codeSymbol{
				name: string(data[match[2]:match[3]]), path: path,
				row: bytes.Count(data[:match[0]], []byte{'\n'}) + 1,
			})
		}
	}
	return symbols
}
