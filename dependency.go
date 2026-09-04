package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var importPatterns = map[string]*regexp.Regexp{
	".js":     regexp.MustCompile(`(?:from\s+|require\s*\(\s*)["']([^"']+)`),
	".jsx":    regexp.MustCompile(`(?:from\s+|require\s*\(\s*)["']([^"']+)`),
	".ts":     regexp.MustCompile(`(?:from\s+|require\s*\(\s*)["']([^"']+)`),
	".tsx":    regexp.MustCompile(`(?:from\s+|require\s*\(\s*)["']([^"']+)`),
	".py":     regexp.MustCompile(`(?m)^\s*(?:from|import)\s+([\w.]+)`),
	".rs":     regexp.MustCompile(`(?m)^\s*(?:use|mod)\s+([^;{]+)`),
	".c":      regexp.MustCompile(`(?m)^\s*#include\s*[<"]([^>"]+)`),
	".cpp":    regexp.MustCompile(`(?m)^\s*#include\s*[<"]([^>"]+)`),
	".h":      regexp.MustCompile(`(?m)^\s*#include\s*[<"]([^>"]+)`),
	".cs":     regexp.MustCompile(`(?m)^\s*using\s+([\w.]+)`),
	".csproj": regexp.MustCompile(`(?i)<(?:PackageReference|ProjectReference)\s+Include=["']([^"']+)`),
	".fs":     regexp.MustCompile(`(?m)^\s*open\s+([\w.]+)`),
	".fsx":    regexp.MustCompile(`(?m)^\s*open\s+([\w.]+)`),
	".tf":     regexp.MustCompile(`(?m)^\s*source\s*=\s*["']([^"']+)`),
	".hcl":    regexp.MustCompile(`(?m)^\s*source\s*=\s*["']([^"']+)`),
}

func dependencyGraph(files []string) []string {
	var lines []string
	for _, path := range files {
		dependencies := fileImports(path)
		if len(dependencies) > 0 {
			lines = append(lines, path+" → "+strings.Join(dependencies, ", "))
		}
		for _, model := range fileInterfaces(path) {
			lines = append(lines, path+" :: interface "+model.name+" { "+strings.Join(model.members, ", ")+" }")
		}
	}
	if len(lines) == 0 {
		return []string{"No supported imports or interfaces found"}
	}
	if len(lines) > 400 {
		lines = append(lines[:400], "… graph truncated at 400 files")
	}
	return lines
}

type graphInterface struct {
	name    string
	members []string
}

var interfacePatterns = map[string]*regexp.Regexp{
	"script": regexp.MustCompile(`(?m)\binterface\s+([A-Za-z_$][\w$]*)[^\{]*\{`),
	"rust":   regexp.MustCompile(`(?m)\btrait\s+([A-Za-z_]\w*)[^\{]*\{`),
	"java":   regexp.MustCompile(`(?m)\binterface\s+([A-Za-z_]\w*)[^\{]*\{`),
	"kotlin": regexp.MustCompile(`(?m)\binterface\s+([A-Za-z_]\w*)[^\{]*\{`),
	"swift":  regexp.MustCompile(`(?m)\bprotocol\s+([A-Za-z_]\w*)[^\{]*\{`),
	"dotnet": regexp.MustCompile(`(?mi)\binterface\s+([A-Za-z_]\w*)[^\{]*\{`),
	"php":    regexp.MustCompile(`(?mi)\binterface\s+([A-Za-z_]\w*)[^\{]*\{`),
}

var dotnetInterfaceMember = regexp.MustCompile(`(?mi)^\s*(?:(?:public|private|protected|internal|static|virtual|abstract)\s+)*[\w<>,.?\[\]]+\s+([A-Za-z_]\w*)\s*(?:\{|=>|;|\()`)

func fileInterfaces(path string) []graphInterface {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	family := completionFamily(filepath.Ext(path))
	if family == "go" {
		file, _ := parser.ParseFile(token.NewFileSet(), path, data, parser.AllErrors)
		if file == nil {
			return nil
		}
		var interfaces []graphInterface
		ast.Inspect(file, func(node ast.Node) bool {
			typeSpec, ok := node.(*ast.TypeSpec)
			if !ok {
				return true
			}
			model, ok := typeSpec.Type.(*ast.InterfaceType)
			if !ok {
				return true
			}
			entry := graphInterface{name: typeSpec.Name.Name}
			for _, field := range model.Methods.List {
				for _, name := range field.Names {
					entry.members = append(entry.members, name.Name)
				}
			}
			sort.Strings(entry.members)
			interfaces = append(interfaces, entry)
			return false
		})
		return interfaces
	}
	pattern := interfacePatterns[family]
	if pattern == nil {
		return nil
	}
	var interfaces []graphInterface
	for _, match := range pattern.FindAllSubmatchIndex(data, -1) {
		bodyStart, bodyEnd := match[1], matchingBrace(data, match[1]-1)
		if bodyEnd < bodyStart {
			continue
		}
		members := map[string]bool{}
		patterns := modelMemberPatterns[family]
		if family == "dotnet" {
			patterns = append(patterns, dotnetInterfaceMember)
		}
		for _, memberPattern := range patterns {
			for _, member := range memberPattern.FindAllSubmatch(data[bodyStart:bodyEnd], -1) {
				if len(member) > 1 {
					members[string(member[1])] = true
				}
			}
		}
		entry := graphInterface{name: string(data[match[2]:match[3]])}
		for member := range members {
			entry.members = append(entry.members, member)
		}
		sort.Strings(entry.members)
		interfaces = append(interfaces, entry)
	}
	return interfaces
}

func matchingBrace(data []byte, open int) int {
	// ponytail: this ignores braces in strings/comments; replace with language parsers if it misidentifies real interfaces.
	depth := 0
	for index := open; index < len(data); index++ {
		switch data[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

func fileImports(path string) []string {
	if strings.EqualFold(filepath.Ext(path), ".go") {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return nil
		}
		imports := make([]string, 0, len(file.Imports))
		for _, spec := range file.Imports {
			if value, err := strconv.Unquote(spec.Path.Value); err == nil {
				imports = append(imports, value)
			}
		}
		return imports
	}
	pattern := importPatterns[strings.ToLower(filepath.Ext(path))]
	if pattern == nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var imports []string
	for _, match := range pattern.FindAllStringSubmatch(string(data), -1) {
		imports = append(imports, strings.TrimSpace(match[1]))
	}
	return imports
}

func projectFiles(root string) []string {
	if files, ok := gitProjectFiles(root); ok {
		return files
	}
	rules := readIgnoreRules(filepath.Join(root, ".gitignore"))
	var files []string
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		relative, _ := filepath.Rel(root, path)
		if path != root && (entry.IsDir() && skipProjectDir(entry.Name()) || ignoredProjectPath(relative, entry.IsDir(), rules)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	return files
}

func gitProjectFiles(root string) ([]string, bool) {
	out, err := exec.Command("git", "-C", root, "ls-files", "--cached", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		return nil, false
	}
	var files []string
	for _, raw := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if raw == "" || hasSkippedDirectory(raw) {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(raw))
		if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
			files = append(files, path)
		}
	}
	return files, true
}

type ignoreRule struct {
	pattern   string
	negated   bool
	directory bool
}

func readIgnoreRules(path string) []ignoreRule {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var rules []ignoreRule
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rule := ignoreRule{negated: strings.HasPrefix(line, "!")}
		line = strings.TrimPrefix(line, "!")
		rule.directory = strings.HasSuffix(line, "/")
		rule.pattern = strings.Trim(strings.TrimSuffix(filepath.ToSlash(line), "/"), "/")
		if rule.pattern != "" {
			rules = append(rules, rule)
		}
	}
	return rules
}

func ignoredProjectPath(path string, directory bool, rules []ignoreRule) bool {
	path = filepath.ToSlash(path)
	ignored := false
	for _, rule := range rules {
		matched := false
		if strings.Contains(rule.pattern, "/") {
			matched, _ = filepath.Match(filepath.FromSlash(rule.pattern), filepath.FromSlash(path))
			matched = matched || rule.directory && strings.HasPrefix(path+"/", rule.pattern+"/")
		} else {
			for _, part := range strings.Split(path, "/") {
				if ok, _ := filepath.Match(rule.pattern, part); ok {
					matched = true
					break
				}
			}
		}
		if matched && (!rule.directory || directory || strings.Contains(path, rule.pattern+"/")) {
			ignored = !rule.negated
		}
	}
	return ignored
}

func hasSkippedDirectory(path string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, part := range parts[:len(parts)-1] {
		if skipProjectDir(part) {
			return true
		}
	}
	return false
}

func skipProjectDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "npm_modules", "vendor", "dist", "build", "target", "bin", "obj", "out", "coverage", "__pycache__", "venv":
		return true
	}
	return false
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func runMCP(root string) error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request mcpRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			continue
		}
		if len(request.ID) == 0 {
			continue
		}
		result, rpcErr := handleMCP(request, root)
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		if rpcErr != nil {
			response["error"] = map[string]any{"code": -32601, "message": rpcErr.Error()}
		} else {
			response["result"] = result
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func handleMCP(request mcpRequest, root string) (any, error) {
	switch request.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(request.Params, &params)
		if params.ProtocolVersion == "" {
			params.ProtocolVersion = "2024-11-05"
		}
		return map[string]any{
			"protocolVersion": params.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "code-editor", "version": "0.1.0"},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": []any{map[string]any{
			"name":        "project_dependency_graph",
			"description": "Return a compact direct-import graph. Use this before reading source files to understand project structure with fewer tokens.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		}}}, nil
	case "tools/call":
		var params struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(request.Params, &params)
		if params.Name != "project_dependency_graph" {
			return nil, fmt.Errorf("unknown tool %q", params.Name)
		}
		graph := strings.Join(dependencyGraph(projectFiles(root)), "\n")
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": graph}}}, nil
	default:
		return nil, fmt.Errorf("unknown method %q", request.Method)
	}
}
