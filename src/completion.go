package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type dependencyCompletion struct {
	words        []string
	members      map[string][]string
	receivers    map[string]string
	memberOwners map[string]string
	definitions  map[string]definitionLocation
}

type completionResult struct {
	path       string
	completion dependencyCompletion
	generation int
}

func (e *editor) invalidateCompletion() {
	e.completionCache = nil
	e.markCompletionDirty()
}

func (e *editor) markCompletionDirty() {
	e.completionGeneration++
	e.completionDirty = true
}

func (e *editor) completionSources() map[string][]byte {
	sources := make(map[string][]byte, len(e.buffers))
	for _, open := range e.buffers {
		sources[open.path] = []byte(strings.Join(runeLines(open.lines), "\n"))
	}
	return sources
}

func (e *editor) loadMembersInBackground(path string) {
	cached := e.completionCache[path]
	typed := filepath.Ext(path) != ".cs" || cached.receivers != nil
	if completionFamily(filepath.Ext(path)) == "" || e.completionPending[path] || cached.members != nil && cached.definitions != nil && typed && !e.completionDirty {
		return
	}
	if e.completionPending == nil {
		e.completionPending = map[string]bool{}
	}
	if e.completionDone == nil {
		e.completionDone = make(chan completionResult, 16)
	}
	e.completionPending[path] = true
	files, sources, generation := append([]string(nil), e.files...), e.completionSources(), e.completionGeneration
	root, done := mustCwd(), e.completionDone
	go func() {
		done <- completionResult{path, dependencyCompletionsAt(root, path, files, sources), generation}
	}()
}

var modelTypePatterns = map[string]*regexp.Regexp{
	"script": regexp.MustCompile(`(?m)\b(?:class|interface|type)\s+([A-Za-z_$][\w$]*)`),
	"python": regexp.MustCompile(`(?m)^\s*class\s+([A-Za-z_]\w*)`),
	"rust":   regexp.MustCompile(`(?m)\b(?:struct|enum|trait|type)\s+([A-Za-z_]\w*)`),
	"cpp":    regexp.MustCompile(`(?m)\b(?:class|struct|union)\s+([A-Za-z_]\w*)`),
	"java":   regexp.MustCompile(`(?m)\b(?:class|record|interface|enum)\s+([A-Za-z_]\w*)`),
	"kotlin": regexp.MustCompile(`(?m)\b(?:(?:data|sealed|open)\s+)?(?:class|interface|object)\s+([A-Za-z_]\w*)`),
	"swift":  regexp.MustCompile(`(?m)\b(?:class|struct|protocol|enum)\s+([A-Za-z_]\w*)`),
	"dotnet": regexp.MustCompile(`(?mi)\b(?:class|record|struct|structure|interface|enum|type)\s+([A-Za-z_]\w*)`),
	"ruby":   regexp.MustCompile(`(?m)^\s*class\s+([A-Za-z_]\w*)`),
	"php":    regexp.MustCompile(`(?mi)\b(?:class|interface|trait|enum)\s+([A-Za-z_]\w*)`),
}

var modelMemberPatterns = map[string][]*regexp.Regexp{
	"script": {
		regexp.MustCompile(`(?m)^\s*(?:(?:public|private|protected|static|readonly|async|get|set|abstract|declare|override)\s+)*([A-Za-z_$][\w$]*)\s*[?!]?\s*(?:[:=(])`),
	},
	"python": {
		regexp.MustCompile(`(?m)^\s+(?:async\s+)?def\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`\bself\.([A-Za-z_]\w*)\s*=`),
	},
	"rust": {
		regexp.MustCompile(`(?m)^\s*(?:pub(?:\([^)]*\))?\s+)?fn\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`(?m)^\s*(?:pub\s+)?([A-Za-z_]\w*)\s*:`),
	},
	"cpp": {
		regexp.MustCompile(`(?m)^\s*(?:(?:public|private|protected|static|virtual|const|inline|constexpr|explicit|friend)\s+)*[\w:<>,*&~]+\s+([A-Za-z_]\w*)\s*(?:[({;=])`),
	},
	"java": {
		regexp.MustCompile(`(?m)^\s*(?:(?:public|private|protected|static|final|abstract|synchronized|native|default|transient|volatile)\s+)*[\w<>,.?\[\]]+\s+([A-Za-z_]\w*)\s*(?:[({;=])`),
	},
	"kotlin": {
		regexp.MustCompile(`(?m)^\s*(?:(?:public|private|protected|internal|open|override|suspend|inline)\s+)*(?:fun\s+)?(?:val\s+|var\s+)?([A-Za-z_]\w*)\s*(?:[:=(])`),
	},
	"swift": {
		regexp.MustCompile(`(?m)^\s*(?:(?:public|private|internal|fileprivate|open|static|class|mutating)\s+)*(?:func\s+|var\s+|let\s+)([A-Za-z_]\w*)`),
	},
	"dotnet": {
		regexp.MustCompile(`(?mi)^\s*(?:(?:public|private|protected|internal|friend|shared|static|virtual|override|async|required|readonly|abstract|member|val)\s+)+[\w<>,.?\[\]]+\s+([A-Za-z_]\w*)\s*(?:\{|=>|;|\()`),
		regexp.MustCompile(`(?mi)^\s*(?:(?:public|private|protected|friend|shared|async)\s+)*(?:function|sub|property)\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`(?mi)^\s*(?:abstract\s+)?member\s+(?:\w+\.)?([A-Za-z_]\w*)`),
	},
	"ruby": {
		regexp.MustCompile(`(?m)^\s*def\s+([A-Za-z_]\w*[!?=]?)`),
		regexp.MustCompile(`@([A-Za-z_]\w*)\s*=`),
	},
	"php": {
		regexp.MustCompile(`(?mi)^\s*(?:(?:public|private|protected|static|readonly|final|abstract)\s+)*(?:function\s+)?(?:[?\\\w|]+\s+)?\$?([A-Za-z_]\w*)\s*(?:[=(;])`),
	},
}

var observedMemberPattern = regexp.MustCompile(`(?:^|[^\w$])([A-Za-z_$][\w$]*)\s*(?:\.|->)\s*([A-Za-z_$][\w$]*)`)
var webApplicationBuild = regexp.MustCompile(`(?m)\bvar\s+([A-Za-z_]\w*)\s*=\s*[A-Za-z_]\w*\.Build\s*\(`)

var webApplicationMembers = strings.Fields("Configuration Environment Lifetime Logger Services Urls Use Run RunAsync StartAsync StopAsync DisposeAsync Map MapGet MapPost MapPut MapDelete MapPatch MapControllers MapControllerRoute MapGroup MapFallback UseRouting UseHttpsRedirection UseAuthentication UseAuthorization UseStaticFiles UseExceptionHandler UseDeveloperExceptionPage UseCors UseWebSockets")

func completionFamily(ext string) string {
	switch strings.ToLower(ext) {
	case ".go":
		return "go"
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx":
		return "script"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".c", ".h", ".cpp", ".hpp", ".cc", ".cxx":
		return "cpp"
	case ".java":
		return "java"
	case ".kt", ".kts":
		return "kotlin"
	case ".swift":
		return "swift"
	case ".cs", ".fs", ".fsx", ".vb", ".razor", ".cshtml":
		return "dotnet"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	}
	return ""
}

func (e *editor) suggestion() []rune {
	b := e.current()
	if candidates := e.completionSuggestions(); len(candidates) > 0 {
		return []rune(candidates[e.completionSelected])[len([]rune(b.completionPrefix())):]
	}
	if completionFamily(filepath.Ext(b.path)) == "" {
		return b.suggestion()
	}
	return nil
}

func (e *editor) pollCompletion() bool {
	if e.completionDone == nil {
		return false
	}
	updated := false
	for {
		select {
		case result := <-e.completionDone:
			delete(e.completionPending, result.path)
			if e.completionCache == nil {
				e.completionCache = map[string]dependencyCompletion{}
			}
			if result.generation != e.completionGeneration {
				if _, cached := e.completionCache[result.path]; !cached {
					e.completionCache[result.path] = result.completion
					updated = true
				}
				if result.path == e.current().path {
					e.loadMembersInBackground(result.path)
				}
				continue
			}
			e.completionCache[result.path] = result.completion
			e.completionDirty = false
			if result.path == e.current().path {
				receiver, _, memberAccess := memberAccessAtCursor(e.current())
				if memberAccess {
					count := len(dependencyCandidates(e.current(), result.completion))
					if count == 0 {
						e.status = "No members found for " + receiver
					} else {
						e.status = fmt.Sprintf("%d members for %s · Tab accepts", count, receiver)
					}
				}
			}
			updated = true
		default:
			return updated
		}
	}
}

func dependencyCompletions(current string, files []string, openSources ...map[string][]byte) dependencyCompletion {
	return dependencyCompletionsAt(mustCwd(), current, files, openSources...)
}

func dependencyCompletionsAt(root, current string, files []string, openSources ...map[string][]byte) dependencyCompletion {
	family := completionFamily(filepath.Ext(current))
	words := map[string]bool{}
	members := map[string]map[string]bool{}
	definitions := map[string]definitionLocation{}
	returnTypes := map[string]string{}
	var currentData []byte
	for _, dependency := range fileImports(workspacePath(root, current)) {
		addDependencyWords(words, dependency)
	}

	// ponytail: scan the current language family; add an indexed LSP when workspaces make this measurably slow.
	seen := 0
	visited := map[string]bool{}
	for _, path := range append([]string{current}, files...) {
		if visited[path] || completionFamily(filepath.Ext(path)) != family || seen >= 400 {
			continue
		}
		visited[path] = true
		var data []byte
		var err error
		open := false
		if len(openSources) > 0 {
			data, open = openSources[0][path]
		}
		if !open {
			data, err = os.ReadFile(workspacePath(root, path))
		}
		if err != nil || len(data) > 2<<20 {
			continue
		}
		seen++
		if path == current {
			currentData = data
		}
		if filepath.Ext(path) == ".cs" {
			indexCSharpReturns(returnTypes, data)
		}
		for name, row := range sourceDefinitionIndex(path, data) {
			if _, exists := definitions[name]; !exists {
				definitions[name] = definitionLocation{path, row}
			}
		}
		types, names := fileModelMembers(path, data)
		for _, typeName := range types {
			words[typeName] = true
			if members[typeName] == nil {
				members[typeName] = map[string]bool{}
			}
			for _, name := range names {
				if name != typeName && name != "constructor" && name != "initialize" && name != "__init__" {
					members[typeName][name] = true
				}
			}
		}
		module := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if members[module] == nil {
			members[module] = map[string]bool{}
		}
		for _, symbol := range sourceSymbols(path, data) {
			words[symbol.name] = true
			members[module][symbol.name] = true
		}
		addObservedMembers(members, path, data, path == current)
		addDotnetFrameworkMembers(members, family, data)
	}

	result := dependencyCompletion{members: map[string][]string{}, definitions: definitions, memberOwners: map[string]string{}}
	if filepath.Ext(current) == ".cs" {
		result.receivers = csharpReceiverTypes(current, currentData, returnTypes)
		addCollectionMembers(members, result.receivers)
	}
	for word := range words {
		if word != "" {
			result.words = append(result.words, word)
		}
	}
	sort.Strings(result.words)
	for owner, names := range members {
		for name := range names {
			result.members[owner] = append(result.members[owner], name)
		}
		sort.Strings(result.members[owner])
		result.memberOwners[strings.ToLower(owner)] = owner
	}
	return result
}

func addDotnetFrameworkMembers(members map[string]map[string]bool, family string, data []byte) {
	if family != "dotnet" || !bytes.Contains(data, []byte("WebApplication.Create")) {
		return
	}
	// ponytail: cover the SDK Web bootstrap without shipping Roslyn; replace this list when an LSP client lands.
	for _, match := range webApplicationBuild.FindAllSubmatch(data, -1) {
		receiver := string(match[1])
		if members[receiver] == nil {
			members[receiver] = map[string]bool{}
		}
		for _, member := range webApplicationMembers {
			members[receiver][member] = true
		}
	}
}

func addObservedMembers(members map[string]map[string]bool, path string, data []byte, includeReceivers bool) {
	b := newBuffer(path, data)
	receiverTypes := map[string]string{}
	for _, match := range observedMemberPattern.FindAllSubmatch(data, -1) {
		receiver, member := string(match[1]), string(match[2])
		if member == "new" || member == "constructor" || member == "__init__" {
			continue
		}
		var owners []string
		if includeReceivers {
			owners = append(owners, strings.TrimPrefix(receiver, "$"))
		}
		owner, known := receiverTypes[receiver]
		if !known {
			owner = objectType(b, receiver)
			receiverTypes[receiver] = owner
		}
		if owner != "" {
			owners = append(owners, owner)
		}
		for _, owner := range owners {
			if members[owner] == nil {
				members[owner] = map[string]bool{}
			}
			members[owner][member] = true
		}
	}
}

func fileModelMembers(path string, data []byte) (types, members []string) {
	family := completionFamily(filepath.Ext(path))
	if family == "go" {
		return goModelMembers(path, data)
	}
	if pattern := modelTypePatterns[family]; pattern != nil {
		for _, match := range pattern.FindAllSubmatch(data, -1) {
			types = append(types, string(match[1]))
		}
	}
	for _, pattern := range modelMemberPatterns[family] {
		for _, match := range pattern.FindAllSubmatch(data, -1) {
			members = append(members, string(match[1]))
		}
	}
	return types, members
}

func goModelMembers(path string, data []byte) (types, members []string) {
	file, _ := parser.ParseFile(token.NewFileSet(), path, data, parser.AllErrors)
	if file == nil {
		return nil, nil
	}
	owned := map[string][]string{}
	for _, declaration := range file.Decls {
		switch value := declaration.(type) {
		case *ast.GenDecl:
			for _, spec := range value.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				types = append(types, typeSpec.Name.Name)
				var fields *ast.FieldList
				switch model := typeSpec.Type.(type) {
				case *ast.StructType:
					fields = model.Fields
				case *ast.InterfaceType:
					fields = model.Methods
				}
				if fields != nil {
					for _, field := range fields.List {
						for _, name := range field.Names {
							owned[typeSpec.Name.Name] = append(owned[typeSpec.Name.Name], name.Name)
						}
					}
				}
			}
		case *ast.FuncDecl:
			if value.Recv != nil && len(value.Recv.List) > 0 {
				if owner := goReceiverType(value.Recv.List[0].Type); owner != "" {
					owned[owner] = append(owned[owner], value.Name.Name)
				}
			}
		}
	}
	for _, names := range owned {
		members = append(members, names...)
	}
	return types, members
}

func goReceiverType(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return goReceiverType(value.X)
	case *ast.IndexExpr:
		return goReceiverType(value.X)
	case *ast.IndexListExpr:
		return goReceiverType(value.X)
	}
	return ""
}

func dependencySuggestion(b *buffer, completion dependencyCompletion) []rune {
	if _, _, ok := memberAccessAtCursor(b); !ok {
		return b.suggestionWith(completion.words)
	}
	candidates := dependencyCandidates(b, completion)
	if len(candidates) == 0 {
		return nil
	}
	_, prefix, _ := memberAccessAtCursor(b)
	return []rune(candidates[0])[len([]rune(prefix)):]
}

func dependencyCandidates(b *buffer, completion dependencyCompletion) []string {
	receiver, prefix, ok := memberAccessAtCursor(b)
	if !ok {
		return nil
	}
	owner, indexed := completion.receivers[receiver]
	if !indexed {
		owner = objectType(b, receiver)
	}
	if owner == "" {
		owner = strings.TrimPrefix(receiver, "$")
	}
	candidates := completion.members[owner]
	if candidates == nil && completion.memberOwners != nil {
		candidates = completion.members[completion.memberOwners[strings.ToLower(owner)]]
	} else if candidates == nil {
		for name, values := range completion.members {
			if strings.EqualFold(name, owner) {
				candidates = values
				break
			}
		}
	}
	var matches []string
	for _, member := range candidates {
		if len(member) > len(prefix) && strings.EqualFold(member[:len(prefix)], prefix) {
			matches = append(matches, member)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return len(matches[i]) < len(matches[j]) || len(matches[i]) == len(matches[j]) && matches[i] < matches[j]
	})
	return matches
}

func (e *editor) completionSuggestions() []string {
	b := e.current()
	if e.completionBuffer != b || e.completionRow != b.row || e.completionCol != b.col {
		e.completionSelected, e.completionDismissed = 0, false
		e.completionBuffer, e.completionRow, e.completionCol = b, b.row, b.col
	}
	if e.completionDismissed || completionFamily(filepath.Ext(b.path)) == "" || !e.selection.empty() {
		return nil
	}
	completion, cached := e.completionCache[b.path]
	_, _, member := memberAccessAtCursor(b)
	// Reuse the project index while typing words; member inference needs fresh receiver types.
	if member || !cached && b.completionPrefix() != "" {
		e.loadMembersInBackground(b.path)
	}
	var candidates []string
	if member {
		candidates = dependencyCandidates(b, completion)
	} else {
		candidates = b.wordCandidates(completion.words)
	}
	e.completionSelected = min(e.completionSelected, max(0, len(candidates)-1))
	return candidates
}

func (e *editor) handleCompletionKey(k key) bool {
	if k.code != keyUp && k.code != keyDown && k.code != keyTab && k.code != keyEnter && (k.r != 0 || k.code != 0) {
		return false
	}
	candidates := e.completionSuggestions()
	if len(candidates) == 0 {
		return false
	}
	switch {
	case k.code == keyUp:
		e.completionSelected = (e.completionSelected + len(candidates) - 1) % len(candidates)
	case k.code == keyDown:
		e.completionSelected = (e.completionSelected + 1) % len(candidates)
	case k.code == keyTab || k.code == keyEnter:
		b := e.current()
		b.insert([]rune(candidates[e.completionSelected])[len([]rune(b.completionPrefix())):])
		e.markCompletionDirty()
		e.completionBuffer, e.completionRow, e.completionCol = b, b.row, b.col
		e.completionDismissed = true
		e.status = "Suggestion accepted"
	case k.r == 0 && k.code == 0:
		e.completionDismissed = true
	default:
		return false
	}
	return true
}

func memberAccessAtCursor(b *buffer) (receiver, prefix string, ok bool) {
	line := b.lines[b.row]
	if b.col < 0 || b.col > len(line) || b.col < len(line) && identifierRune(line[b.col]) {
		return "", "", false
	}
	start := b.col
	for start > 0 && identifierRune(line[start-1]) {
		start--
	}
	end := start - 1
	if end < 0 || line[end] != '.' {
		if end < 0 || line[end] != '>' || end == 0 || line[end-1] != '-' {
			return "", "", false
		}
		end--
	}
	if end > 0 && line[end-1] == '?' {
		end--
	}
	row := b.row
	for {
		for end > 0 && unicode.IsSpace(line[end-1]) {
			end--
		}
		if end > 0 || row == 0 {
			break
		}
		row--
		line = b.lines[row]
		end = len(line)
	}
	from := end
	for from > 0 && identifierRune(line[from-1]) {
		from--
	}
	if from == end {
		return "", "", false
	}
	return string(line[from:end]), string(b.lines[b.row][start:b.col]), true
}

func identifierRune(value rune) bool {
	return unicode.IsLetter(value) || unicode.IsDigit(value) || value == '_' || value == '$'
}

func objectType(b *buffer, receiver string) string {
	bare := strings.TrimPrefix(receiver, "$")
	if first := []rune(bare); len(first) > 0 && unicode.IsUpper(first[0]) {
		return bare
	}
	source := strings.Join(runeLines(b.lines), "\n")
	quoted := regexp.QuoteMeta(receiver)
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?:^|[^\w$])` + quoted + `\s*(?::=|=)\s*(?:new\s+|&\s*)?([A-Z][A-Za-z0-9_.:]*)`),
		regexp.MustCompile(`\b(?:const|let|var|val)\s+` + regexp.QuoteMeta(bare) + `\s*:\s*([A-Z][A-Za-z0-9_.:]*)`),
		regexp.MustCompile(`(?i)\b(?:var|dim)\s+` + regexp.QuoteMeta(bare) + `\s+(?:as\s+)?([A-Z][A-Za-z0-9_.:]*)`),
		regexp.MustCompile(`\b([A-Z][A-Za-z0-9_.:<>,?]*)\s+[*&]?` + quoted + `\b`),
	}
	if bare == "this" || bare == "self" {
		if pattern := modelTypePatterns[completionFamily(filepath.Ext(b.path))]; pattern != nil {
			patterns = []*regexp.Regexp{pattern}
		}
	}
	for _, pattern := range patterns {
		if match := pattern.FindStringSubmatch(source); len(match) > 1 {
			return baseType(match[1])
		}
	}
	return ""
}

func baseType(value string) string {
	value = strings.TrimSuffix(value, ".new")
	value = strings.TrimRight(value, "?*&")
	if index := strings.IndexAny(value, "<["); index >= 0 {
		value = value[:index]
	}
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '.' || r == ':' || r == '\\' })
	if len(parts) == 0 {
		return value
	}
	return parts[len(parts)-1]
}

func addDependencyWords(words map[string]bool, dependency string) {
	for _, word := range strings.FieldsFunc(dependency, func(r rune) bool { return r == '.' || r == '/' || r == '\\' || r == '-' || r == ':' }) {
		words[strings.TrimSuffix(word, filepath.Ext(word))] = true
	}
}
