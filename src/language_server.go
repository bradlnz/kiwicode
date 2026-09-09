package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
)

// One server per language/workspace. Commands are argv, never shell programs.
var languageCommands = map[string][]string{
	"go": {"gopls"}, "csharp": {"csharp-ls"},
	"typescript": {"typescript-language-server", "--stdio"},
	"python":     {"pyright-langserver", "--stdio"}, "rust": {"rust-analyzer"},
	"cpp": {"clangd"}, "java": {"jdtls"},
	"ruby": {"ruby-lsp"}, "php": {"intelephense", "--stdio"},
	"css":         {"vscode-css-language-server", "--stdio"},
	"html":        {"vscode-html-language-server", "--stdio"},
	"json":        {"vscode-json-language-server", "--stdio"},
	"yaml":        {"yaml-language-server", "--stdio"},
	"shellscript": {"bash-language-server", "start"},
}

func languageID(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".cs":
		return "csharp"
	case ".ts", ".mts", ".cts":
		return "typescript"
	case ".tsx":
		return "typescriptreact"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".jsx":
		return "javascriptreact"
	case ".py", ".pyi":
		return "python"
	case ".rs":
		return "rust"
	case ".c":
		return "c"
	case ".h", ".cpp", ".hpp", ".cc", ".cxx":
		return "cpp"
	case ".java":
		return "java"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".css", ".scss", ".less":
		return ext[1:]
	case ".html", ".htm":
		return "html"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".sh", ".bash", ".zsh":
		return "shellscript"
	case ".kt", ".kts":
		return "kotlin"
	case ".swift":
		return "swift"
	case ".fs", ".fsx":
		return "fsharp"
	}
	return ""
}

func languageGroup(id string) string {
	switch id {
	case "typescriptreact", "javascript", "javascriptreact":
		return "typescript"
	case "c":
		return "cpp"
	case "scss", "less":
		return "css"
	}
	return id
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}
type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}
type lspEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

func (edit *lspEdit) UnmarshalJSON(data []byte) error {
	var value struct {
		Range *struct {
			Start *lspPosition `json:"start"`
			End   *lspPosition `json:"end"`
		} `json:"range"`
		NewText *string `json:"newText"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value.Range == nil || value.Range.Start == nil || value.Range.End == nil || value.NewText == nil {
		return fmt.Errorf("incomplete completion edit")
	}
	edit.Range, edit.NewText = lspRange{*value.Range.Start, *value.Range.End}, *value.NewText
	return nil
}

type lspCompletion struct {
	Label               string          `json:"label"`
	Detail              string          `json:"detail"`
	InsertText          string          `json:"insertText"`
	FilterText          string          `json:"filterText"`
	SortText            string          `json:"sortText"`
	InsertTextFormat    int             `json:"insertTextFormat"`
	TextEdit            *lspEdit        `json:"textEdit"`
	AdditionalTextEdits []lspEdit       `json:"additionalTextEdits"`
	Command             json.RawMessage `json:"command"`
}

type languageKey struct {
	buffer             *buffer
	revision, row, col int
	generation         int
}

func (e *editor) languageKey() languageKey {
	b := e.current()
	return languageKey{b, b.revision, b.row, b.col, e.completionGeneration}
}

type languageJob struct {
	key                 languageKey
	method, uri, prefix string
	position            lspPosition
	documents           map[string]string
}
type languageResult struct {
	job  languageJob
	data json.RawMessage
	err  error
}
type languageWorker struct {
	jobs    chan languageJob
	cancel  context.CancelFunc
	stopped <-chan struct{}
}
type languageIntelligence struct {
	root                           string
	workers                        map[string]*languageWorker
	done                           chan languageResult
	observed, requested, completed languageKey
	changed                        time.Time
	items                          []lspCompletion
}

func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}
func uriPath(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" || u.Host != "" && u.Host != "localhost" || !filepath.IsAbs(u.Path) {
		return "", fmt.Errorf("unsupported source URI %q", uri)
	}
	return filepath.FromSlash(u.Path), nil
}
func utf16Column(line []rune, col int) int {
	return len(utf16.Encode(line[:min(max(0, col), len(line))]))
}

func (e *editor) languageWorker() *languageWorker {
	id := languageGroup(languageID(e.current().path))
	if id == "" {
		return nil
	}
	if e.languages == nil {
		e.languages = &languageIntelligence{root: mustCwd(), workers: map[string]*languageWorker{}, done: make(chan languageResult, 16)}
	}
	l := e.languages
	if worker, ok := l.workers[id]; ok {
		return worker
	}
	command, configured := settings.languageServers[id]
	if !configured {
		command = languageCommands[id]
	}
	if len(command) == 0 {
		l.workers[id] = nil
		return nil
	}
	executable, err := exec.LookPath(command[0])
	if err != nil {
		l.workers[id] = nil
		return nil
	}
	// Resolve before any workspace switch changes the process working directory.
	executable, err = filepath.Abs(executable)
	if err != nil {
		l.workers[id] = nil
		return nil
	}
	command = append([]string{executable}, command[1:]...)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	w := &languageWorker{make(chan languageJob, 1), cancel, stopped}
	l.workers[id] = w
	go runLanguageWorker(ctx, w, l.root, command, l.done, stopped)
	return w
}

func (e *editor) languageJob(method string) languageJob {
	b, l := e.current(), e.languages
	job := languageJob{key: e.languageKey(), method: method, uri: fileURI(workspacePath(l.root, b.path)), prefix: b.completionPrefix(), position: lspPosition{b.row, utf16Column(b.lines[b.row], b.col)}, documents: map[string]string{}}
	for _, open := range e.buffers {
		if languageGroup(languageID(open.path)) == languageGroup(languageID(b.path)) {
			job.documents[fileURI(workspacePath(l.root, open.path))] = strings.Join(runeLines(open.lines), "\n")
		}
	}
	return job
}

func (e *editor) requestLanguage(method string) bool {
	w := e.languageWorker()
	if w == nil {
		return false
	}
	select {
	case <-w.stopped:
		return false
	default:
	}
	job := e.languageJob(method)
	// Explicit navigation supersedes a queued automatic completion.
	select {
	case <-w.jobs:
	default:
	}
	select {
	case w.jobs <- job:
		e.status = "Language server: loading " + method + "…"
		return true
	default:
		return false
	}
}

func (e *editor) pollLanguages() bool {
	changed := false
	if e.languages != nil {
		for {
			select {
			case result := <-e.languages.done:
				if result.job.key != e.languageKey() {
					continue
				}
				changed = true
				if result.err != nil {
					e.status = "Language server: " + plain(result.err.Error())
					if result.job.method == "definition" {
						e.goToSourceDefinition()
					}
					continue
				}
				switch result.job.method {
				case "completion":
					items, err := languageCompletions(result.data, result.job.prefix)
					if err == nil {
						e.languages.completed, e.languages.items = result.job.key, items
						e.completionSelected = 0
						if len(items) > 0 {
							e.status = fmt.Sprintf("%d language suggestions · Tab accepts", len(items))
						}
					}
				case "definition":
					e.showLanguageDefinition(result.data)
				case "hover":
					e.showLanguageHover(result.data)
				}
			default:
				goto drained
			}
		}
	}
drained:
	if !e.editorFocused() || e.workspaceDone != nil {
		return changed
	}
	w := e.languageWorker()
	if w == nil {
		return changed
	}
	select {
	case <-w.stopped:
		return changed
	default:
	}
	l, key := e.languages, e.languageKey()
	if l.observed != key {
		l.observed, l.changed = key, time.Now()
		return changed
	}
	if time.Since(l.changed) < 150*time.Millisecond || l.requested == key || e.completionDismissed {
		return changed
	}
	b := e.current()
	_, _, member := memberAccessAtCursor(b)
	if b.completionPrefix() == "" && !member {
		return changed
	}
	select {
	case w.jobs <- e.languageJob("completion"):
		l.requested = key
	default:
	}
	return changed
}

func (e *editor) languageItems() []lspCompletion {
	if e.languages != nil && e.languages.completed == e.languageKey() {
		return e.languages.items
	}
	return nil
}
func (e *editor) closeLanguages() {
	if e.languages == nil {
		return
	}
	for _, w := range e.languages.workers {
		if w != nil {
			w.cancel()
		}
	}
	for _, w := range e.languages.workers {
		if w != nil {
			<-w.stopped
		}
	}
}

func languageCompletions(data []byte, prefix string) ([]lspCompletion, error) {
	var items []lspCompletion
	if len(data) > 0 && data[0] == '{' {
		var list struct {
			Items []lspCompletion `json:"items"`
		}
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, err
		}
		items = list.Items
	} else if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	filtered := items[:0]
	for _, item := range items {
		// Snippet/command execution is deliberately not advertised to servers.
		if item.Label == "" || item.InsertTextFormat == 2 || len(item.Command) > 0 && string(item.Command) != "null" {
			continue
		}
		filter := item.FilterText
		if filter == "" {
			filter = item.Label
		}
		if (item.TextEdit == nil || item.FilterText != "") && !strings.HasPrefix(strings.ToLower(filter), strings.ToLower(prefix)) {
			continue
		}
		filtered = append(filtered, item)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		a, b := filtered[i].SortText, filtered[j].SortText
		if a == "" {
			a = filtered[i].Label
		}
		if b == "" {
			b = filtered[j].Label
		}
		return a < b
	})
	return filtered, nil
}

func (e *editor) showLanguageDefinition(data []byte) {
	var locations []struct {
		URI                  string   `json:"uri"`
		Range                lspRange `json:"range"`
		TargetURI            string   `json:"targetUri"`
		TargetSelectionRange lspRange `json:"targetSelectionRange"`
	}
	if len(data) > 0 && data[0] == '{' {
		data = append(append([]byte{'['}, data...), ']')
	}
	if json.Unmarshal(data, &locations) != nil || len(locations) == 0 {
		e.goToSourceDefinition()
		return
	}
	location := locations[0]
	if location.TargetURI != "" {
		location.URI, location.Range = location.TargetURI, location.TargetSelectionRange
	}
	path, err := uriPath(location.URI)
	if err != nil {
		e.status = "Definition: " + err.Error()
		return
	}
	root, target := mustCwd(), path
	if relative, err := filepath.Rel(root, path); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		target = relative
	}
	for _, open := range e.buffers {
		if workspacePath(root, open.path) == path {
			target = open.path
			break
		}
	}
	e.open(target)
	b := e.current()
	if workspacePath(root, b.path) != path {
		return // Keep open's error and leave the previous buffer's cursor alone.
	}
	b.row = min(max(0, location.Range.Start.Line), len(b.lines)-1)
	col, err := runeColumn(b.lines[b.row], location.Range.Start.Character)
	if err != nil {
		col = 0
	}
	b.col = col
	e.status = "Definition: " + plain(path)
}
func (e *editor) showLanguageHover(data []byte) {
	var hover struct {
		Contents json.RawMessage `json:"contents"`
	}
	if json.Unmarshal(data, &hover) != nil {
		return
	}
	var parts []json.RawMessage
	if len(hover.Contents) > 0 && hover.Contents[0] == '[' {
		_ = json.Unmarshal(hover.Contents, &parts)
	} else {
		parts = []json.RawMessage{hover.Contents}
	}
	var lines []string
	for _, part := range parts {
		var value string
		if json.Unmarshal(part, &value) != nil {
			var markup struct {
				Value string `json:"value"`
			}
			_ = json.Unmarshal(part, &markup)
			value = markup.Value
		}
		if value != "" {
			for _, line := range strings.Split(value, "\n") {
				lines = append(lines, plain(line))
			}
			lines = append(lines, "")
		}
	}
	if len(lines) == 0 {
		e.status = "No symbol information at cursor"
		return
	}
	e.clearModalViews()
	e.explorer = false
	e.opsMode, e.opsTop = "Symbol Info", 0
	e.opsLines = wrapTextLines(lines, max(1, e.codeAreaWidth()-2))
	e.status = "Symbol Info: arrows to scroll · Esc closes"
}

// JSON-RPC framing and lifecycle use only the standard library.
type lspMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}
type lspConnection struct {
	ctx     context.Context
	cancel  context.CancelFunc
	input   io.WriteCloser
	writes  sync.Mutex
	replies chan lspMessage
	id      int
}

func readLSP(reader *bufio.Reader) (lspMessage, error) {
	length := -1
	for total := 0; ; {
		raw, err := reader.ReadSlice('\n')
		if err != nil {
			return lspMessage{}, err
		}
		line := string(raw)
		total += len(line)
		if total > 8192 {
			return lspMessage{}, fmt.Errorf("language server header too large")
		}
		if line == "\r\n" || line == "\n" {
			break
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && strings.EqualFold(key, "Content-Length") {
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return lspMessage{}, err
			}
		}
	}
	if length < 0 || length > 16<<20 {
		return lspMessage{}, fmt.Errorf("invalid language server message length")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(reader, data); err != nil {
		return lspMessage{}, err
	}
	var message lspMessage
	err := json.Unmarshal(data, &message)
	return message, err
}
func (c *lspConnection) write(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	c.writes.Lock()
	defer c.writes.Unlock()
	_, err = fmt.Fprintf(c.input, "Content-Length: %d\r\n\r\n%s", len(data), data)
	return err
}
func (c *lspConnection) notify(method string, params any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}
func (c *lspConnection) call(method string, params any) (json.RawMessage, error) {
	// ponytail: requests serialize per server; add cancellation/retry if real servers need concurrent requests.
	c.id++
	id := c.id
	timeout := time.AfterFunc(30*time.Second, c.cancel)
	defer timeout.Stop()
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		select {
		case <-c.ctx.Done():
			return nil, fmt.Errorf("server stopped or request timed out")
		case reply := <-c.replies:
			if string(reply.ID) != strconv.Itoa(id) {
				continue
			}
			if reply.Error != nil {
				return nil, fmt.Errorf("%s", reply.Error.Message)
			}
			return reply.Result, nil
		}
	}
}
func (c *lspConnection) read(output io.Reader) {
	reader := bufio.NewReaderSize(output, 8192)
	defer c.cancel()
	for {
		message, err := readLSP(reader)
		if err != nil {
			return
		}
		if message.Method != "" {
			if len(message.ID) == 0 {
				continue
			}
			reply := map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": nil}
			switch message.Method {
			case "workspace/configuration":
				var params struct {
					Items []json.RawMessage `json:"items"`
				}
				_ = json.Unmarshal(message.Params, &params)
				reply["result"] = make([]any, len(params.Items))
			case "workspace/workspaceFolders":
				reply["result"] = []any{}
			case "window/workDoneProgress/create":
			default:
				delete(reply, "result")
				reply["error"] = map[string]any{"code": -32601, "message": "Unsupported client request"}
			}
			if c.write(reply) != nil {
				return
			}
			continue
		}
		select {
		case c.replies <- message:
		case <-c.ctx.Done():
			return
		}
	}
}

type lspDocument struct {
	text    string
	version int
}

func runLanguageWorker(ctx context.Context, w *languageWorker, root string, command []string, done chan<- languageResult, stopped chan struct{}) {
	defer close(stopped)
	defer w.cancel()
	var active *languageJob
	defer func() {
		// Report startup failures and disconnects even when no RPC response arrives.
		report := func(job languageJob) {
			select {
			case done <- languageResult{job: job, err: fmt.Errorf("server stopped: %s", filepath.Base(command[0]))}:
			default:
			}
		}
		if active != nil {
			report(*active)
		}
		select {
		case job := <-w.jobs:
			report(job)
		default:
		}
	}()
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = root
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	input, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	defer output.Close()
	if err = cmd.Start(); err != nil {
		return
	}
	defer func() { w.cancel(); _ = cmd.Wait() }()
	c := &lspConnection{ctx: ctx, cancel: w.cancel, input: input, replies: make(chan lspMessage, 16)}
	go c.read(output)
	initResult, initErr := c.call("initialize", map[string]any{
		"processId": os.Getpid(), "rootUri": fileURI(root),
		"workspaceFolders": []any{map[string]any{"uri": fileURI(root), "name": filepath.Base(root)}},
		"capabilities": map[string]any{"general": map[string]any{"positionEncodings": []string{"utf-16"}}, "textDocument": map[string]any{
			"completion": map[string]any{"completionItem": map[string]any{"snippetSupport": false}},
			"hover":      map[string]any{"contentFormat": []string{"plaintext"}},
		}},
	})
	var initialization struct {
		Capabilities struct {
			PositionEncoding string          `json:"positionEncoding"`
			TextDocumentSync json.RawMessage `json:"textDocumentSync"`
		} `json:"capabilities"`
	}
	if initErr == nil {
		initErr = json.Unmarshal(initResult, &initialization)
	}
	if encoding := initialization.Capabilities.PositionEncoding; encoding != "" && encoding != "utf-16" {
		initErr = fmt.Errorf("unsupported position encoding %s", encoding)
	}
	syncKind := 0
	syncData := initialization.Capabilities.TextDocumentSync
	if len(syncData) > 0 && syncData[0] == '{' {
		var options struct {
			Change int `json:"change"`
		}
		_ = json.Unmarshal(syncData, &options)
		syncKind = options.Change
	} else {
		_ = json.Unmarshal(syncData, &syncKind)
	}
	if initErr == nil {
		initErr = c.notify("initialized", map[string]any{})
	}
	documents := map[string]lspDocument{}
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-w.jobs:
			active = &job
			err = initErr
			if err == nil {
				err = c.syncDocuments(documents, job.documents, syncKind)
			}
			var data json.RawMessage
			if err == nil {
				data, err = c.call("textDocument/"+job.method, map[string]any{"textDocument": map[string]any{"uri": job.uri}, "position": job.position})
			}
			select {
			case done <- languageResult{job, data, err}:
				active = nil
			case <-ctx.Done():
				return
			}
			if initErr != nil {
				return
			}
		}
	}
}
func (c *lspConnection) syncDocuments(known map[string]lspDocument, current map[string]string, kind int) error {
	for uri := range known {
		if _, ok := current[uri]; !ok {
			if err := c.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": uri}}); err != nil {
				return err
			}
			delete(known, uri)
		}
	}
	for uri, text := range current {
		old, exists := known[uri]
		if exists && old.text == text {
			continue
		}
		version := old.version + 1
		if !exists {
			path, _ := uriPath(uri)
			if err := c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": languageID(path), "version": version, "text": text}}); err != nil {
				return err
			}
		} else {
			if kind == 0 {
				return fmt.Errorf("server does not support document changes")
			}
			change := map[string]any{"text": text}
			if kind == 2 {
				lines := strings.Split(old.text, "\n")
				last := []rune(lines[len(lines)-1])
				change["range"] = lspRange{End: lspPosition{len(lines) - 1, utf16Column(last, len(last))}}
			}
			if err := c.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": version}, "contentChanges": []any{change}}); err != nil {
				return err
			}
		}
		known[uri] = lspDocument{text, version}
	}
	return nil
}

func runeColumn(line []rune, character int) (int, error) {
	units := 0
	for col, r := range line {
		if units == character {
			return col, nil
		}
		units += len(utf16.Encode([]rune{r}))
	}
	if units == character {
		return len(line), nil
	}
	return 0, fmt.Errorf("invalid UTF-16 character offset")
}
func lspOffset(lines [][]rune, position lspPosition) (int, error) {
	if position.Line < 0 || position.Line >= len(lines) || position.Character < 0 {
		return 0, fmt.Errorf("invalid edit position")
	}
	col, err := runeColumn(lines[position.Line], position.Character)
	if err != nil {
		return 0, err
	}
	for _, line := range lines[:position.Line] {
		col += len(line) + 1
	}
	return col, nil
}
func (e *editor) acceptLanguageCompletion(item lspCompletion) {
	b := e.current()
	text := item.InsertText
	if text == "" {
		text = item.Label
	}
	edit := lspEdit{Range: lspRange{Start: lspPosition{b.row, utf16Column(b.lines[b.row], b.col-len([]rune(b.completionPrefix())))}, End: lspPosition{b.row, utf16Column(b.lines[b.row], b.col)}}, NewText: text}
	if item.TextEdit != nil {
		edit = *item.TextEdit
	}
	if err := applyLanguageEdits(b, edit, item.AdditionalTextEdits); err != nil {
		e.status = "Completion: " + err.Error()
		return
	}
	e.invalidateCompletion()
	e.completionBuffer, e.completionRow, e.completionCol = b, b.row, b.col
	e.completionDismissed = true
	e.status = "Suggestion accepted"
}
func applyLanguageEdits(b *buffer, main lspEdit, additional []lspEdit) error {
	type replacement struct {
		start, end int
		text       []rune
		main       bool
	}
	var edits []replacement
	for i, edit := range append([]lspEdit{main}, additional...) {
		start, err := lspOffset(b.lines, edit.Range.Start)
		if err != nil {
			return err
		}
		end, err := lspOffset(b.lines, edit.Range.End)
		if err != nil {
			return err
		}
		if end < start {
			return fmt.Errorf("reversed edit range")
		}
		edits = append(edits, replacement{start, end, []rune(strings.ReplaceAll(edit.NewText, "\r\n", "\n")), i == 0})
	}
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	for i := 1; i < len(edits); i++ {
		if edits[i].start < edits[i-1].end || edits[i].start == edits[i-1].start {
			return fmt.Errorf("overlapping completion edits")
		}
	}
	source := []rune(strings.Join(runeLines(b.lines), "\n"))
	var result []rune
	end, cursor := 0, 0
	for _, edit := range edits {
		result = append(result, source[end:edit.start]...)
		result = append(result, edit.text...)
		if edit.main {
			cursor = len(result)
		}
		end = edit.end
	}
	result = append(result, source[end:]...)
	b.recordUndo()
	b.lines = newBuffer(b.path, []byte(string(result))).lines
	before := string(result[:cursor])
	b.row = strings.Count(before, "\n")
	b.col = len([]rune(before[strings.LastIndex(before, "\n")+1:]))
	b.dirty = true
	return nil
}
