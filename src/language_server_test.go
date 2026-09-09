package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The test executable doubles as a stdio server, exercising the actual transport.
func TestLanguageServerProcess(t *testing.T) {
	if len(os.Args) == 0 || os.Args[len(os.Args)-1] != "kiwicode-test-lsp" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	documents := map[string]string{}
	send := func(id json.RawMessage, result any) {
		data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
		fmt.Printf("Content-Length: %d\r\n\r\n%s", len(data), data)
	}
	for {
		message, err := readLSP(reader)
		if err != nil {
			os.Exit(0)
		}
		switch message.Method {
		case "initialize":
			// A server request during initialization must not block the client.
			request := `{"jsonrpc":"2.0","id":"config","method":"workspace/configuration","params":{"items":[{},{}]}}`
			fmt.Printf("Content-Length: %d\r\n\r\n%s", len(request), request)
			reply, err := readLSP(reader)
			if err != nil || string(reply.ID) != `"config"` || string(reply.Result) != "[null,null]" {
				os.Exit(2)
			}
			send(message.ID, map[string]any{"capabilities": map[string]any{"textDocumentSync": 2, "completionProvider": map[string]any{}, "hoverProvider": true, "definitionProvider": true}})
		case "textDocument/didOpen":
			var params struct{ TextDocument struct{ URI, Text string } }
			_ = json.Unmarshal(message.Params, &params)
			documents[params.TextDocument.URI] = params.TextDocument.Text
		case "textDocument/didChange":
			var params struct {
				TextDocument   struct{ URI string }
				ContentChanges []struct {
					Range lspRange
					Text  string
				}
			}
			_ = json.Unmarshal(message.Params, &params)
			if len(params.ContentChanges) != 1 {
				os.Exit(3)
			}
			b := newBuffer("test.ts", []byte(documents[params.TextDocument.URI]))
			end, err := lspOffset(b.lines, params.ContentChanges[0].Range.End)
			if err != nil || end != len([]rune(documents[params.TextDocument.URI])) {
				os.Exit(4)
			}
			documents[params.TextDocument.URI] = params.ContentChanges[0].Text
		case "textDocument/didClose":
			var params struct{ TextDocument struct{ URI string } }
			_ = json.Unmarshal(message.Params, &params)
			delete(documents, params.TextDocument.URI)
		case "textDocument/completion", "textDocument/hover", "textDocument/definition":
			var params struct {
				TextDocument struct{ URI string }
				Position     lspPosition
			}
			_ = json.Unmarshal(message.Params, &params)
			if message.Method == "textDocument/completion" {
				send(message.ID, map[string]any{"items": []any{map[string]any{"label": "Property", "detail": documents[params.TextDocument.URI], "textEdit": lspEdit{Range: lspRange{Start: params.Position, End: params.Position}, NewText: "Property"}}}})
			} else if message.Method == "textDocument/hover" {
				send(message.ID, map[string]any{"contents": map[string]any{"kind": "plaintext", "value": "Property: string"}})
			} else {
				send(message.ID, []any{map[string]any{"targetUri": params.TextDocument.URI, "targetSelectionRange": lspRange{Start: lspPosition{0, 2}}}})
			}
		}
	}
}

func TestLanguageIntelligence(t *testing.T) {
	oldSettings := settings
	t.Cleanup(func() { settings = oldSettings })
	resetSettings()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := applySetting("language_servers", "typescript", fmt.Sprintf("[%q,%q,%q,%q]", executable, "-test.run=^TestLanguageServerProcess$", "--", "kiwicode-test-lsp")); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "a space #.ts")
	if err := os.WriteFile(path, []byte("😀obj."), 0600); err != nil {
		t.Fatal(err)
	}
	b := newBuffer(path, []byte("😀obj."))
	b.col = len(b.lines[0])
	e := &editor{buffers: []*buffer{b}, rows: 24, cols: 100}
	t.Cleanup(func() {
		e.closeLanguages()
		if e.languages != nil {
			for _, w := range e.languages.workers {
				if w != nil {
					select {
					case <-w.stopped:
					case <-time.After(3 * time.Second):
						t.Error("language server leaked")
					}
				}
			}
		}
	})
	request := func(method string) {
		t.Helper()
		if !e.requestLanguage(method) {
			t.Fatal("request not queued")
		}
		select {
		case result := <-e.languages.done:
			if result.err != nil {
				t.Fatal(result.err)
			}
			if result.job.position.Character != utf16Column(b.lines[b.row], b.col) {
				t.Fatal("wrong UTF-16 position")
			}
			e.languages.done <- result
			e.pollLanguages()
		case <-time.After(5 * time.Second):
			t.Fatal("language server did not reply")
		}
	}
	request("completion")
	if items := e.languageItems(); len(items) != 1 || items[0].Detail != "😀obj." {
		t.Fatalf("completion: %#v", items)
	}
	// A response for an older revision at the same cursor must not be accepted.
	stale := e.languageJob("completion")
	b.recordUndo()
	b.lines[0][1] = 'x'
	e.languages.done <- languageResult{job: stale, data: json.RawMessage(`[{"label":"Stale"}]`)}
	e.pollLanguages()
	if len(e.languageItems()) != 0 {
		t.Fatal("stale completion survived edit")
	}
	request("completion")
	if e.languageItems()[0].Detail != "😀xbj." {
		t.Fatal("unsaved change was not synchronized")
	}
	if !e.handleCompletionKey(key{code: keyTab}) || string(b.lines[0]) != "😀xbj.Property" {
		t.Fatal("server completion not accepted")
	}
	if !b.undoChange() || string(b.lines[0]) != "😀xbj." {
		t.Fatal("completion undo failed")
	}
	request("hover")
	if e.opsMode != "Symbol Info" || !strings.Contains(strings.Join(e.opsLines, "\n"), "Property: string") {
		t.Fatal("hover not displayed")
	}
	e.clearModalViews()
	request("definition")
	if len(e.buffers) != 1 {
		t.Fatal("definition opened a duplicate buffer")
	}
	if b.col != 1 {
		t.Fatalf("definition column: %d", b.col)
	}
	// Definitions without a server retain source navigation.
	if err := applySetting("language_servers", "go", "off"); err != nil {
		t.Fatal(err)
	}
	if languageGroup(languageID("a.jsx")) != languageGroup(languageID("b.ts")) {
		t.Fatal("script family does not share unsaved documents")
	}
}

func TestLanguageEditsAndFraming(t *testing.T) {
	b := newBuffer("test.ts", []byte("// 😀\nobj.Pr;\n"))
	b.row, b.col = 1, 6
	main := lspEdit{lspRange{lspPosition{1, 4}, lspPosition{1, 6}}, "Property"}
	additional := []lspEdit{{lspRange{}, "import X\n"}}
	if err := applyLanguageEdits(b, main, additional); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(runeLines(b.lines), "\n"); got != "import X\n// 😀\nobj.Property;\n" || b.row != 2 || b.col != 12 {
		t.Fatalf("edits: %q, %d:%d", got, b.row, b.col)
	}
	if !b.undoChange() || len(b.undo) != 0 {
		t.Fatal("edits were not one undo step")
	}
	before := strings.Join(runeLines(b.lines), "\n")
	for _, bad := range []lspEdit{
		{lspRange{lspPosition{0, 4}, lspPosition{0, 5}}, "x"}, // middle of surrogate pair
		{lspRange{lspPosition{9, 0}, lspPosition{9, 0}}, "x"},
		{lspRange{lspPosition{1, 6}, lspPosition{1, 4}}, "x"},
	} {
		if applyLanguageEdits(b, bad, nil) == nil || strings.Join(runeLines(b.lines), "\n") != before {
			t.Fatal("invalid edit changed buffer")
		}
	}
	if applyLanguageEdits(b, main, []lspEdit{main}) == nil {
		t.Fatal("overlap accepted")
	}
	uri := fileURI("/tmp/a #😀.ts")
	path, err := uriPath(uri)
	if err != nil || path != "/tmp/a #😀.ts" {
		t.Fatalf("URI roundtrip: %s %v", path, err)
	}
	for _, input := range []string{"Content-Length: -1\r\n\r\n", "Content-Length: 999999999\r\n\r\n", "Content-Length: 2\r\n\r\nx"} {
		if _, err := readLSP(bufio.NewReader(strings.NewReader(input))); err == nil {
			t.Fatal("malformed frame accepted")
		}
	}
	items, err := languageCompletions([]byte(`{"items":[{"label":"Zoo","sortText":"2"},{"label":"Alpha","sortText":"1"},{"label":"Snippet","insertTextFormat":2}]}`), "")
	if err != nil || len(items) != 2 || items[0].Label != "Alpha" {
		t.Fatalf("completion list: %#v %v", items, err)
	}
}

func TestLanguageServerClangd(t *testing.T) {
	if os.Getenv("KIWICODE_TEST_CLANGD") == "" {
		t.Skip("set KIWICODE_TEST_CLANGD=1 for real clangd smoke test")
	}
	if _, err := exec.LookPath("clangd"); err != nil {
		t.Fatal(err)
	}
	oldSettings := settings
	t.Cleanup(func() { settings = oldSettings })
	resetSettings()
	root := t.TempDir()
	path := filepath.Join(root, "main.cpp")
	source := "struct Item { int property; };\nvoid test() { Item item; item. }\n"
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	b := newBuffer(path, []byte(source))
	b.row, b.col = 1, len("void test() { Item item; item.")
	e := &editor{buffers: []*buffer{b}, rows: 24, cols: 100}
	t.Cleanup(e.closeLanguages)
	if !e.requestLanguage("completion") {
		t.Fatal("clangd not started")
	}
	select {
	case result := <-e.languages.done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		items, err := languageCompletions(result.data, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if strings.Contains(item.Label, "property") {
				e.acceptLanguageCompletion(item)
				if !strings.Contains(string(b.lines[1]), "item.property") {
					t.Fatalf("clangd edit: %s", string(b.lines[1]))
				}
				return
			}
		}
		t.Fatalf("property absent: %s", result.data)
	case <-time.After(15 * time.Second):
		t.Fatal("clangd timed out")
	}
}

func TestLanguageSyntaxAliases(t *testing.T) {
	for alias, original := range map[string]string{".mjs": ".js", ".cjs": ".js", ".mts": ".ts", ".cts": ".ts", ".pyi": ".py", ".cc": ".cpp", ".cxx": ".cpp", ".kts": ".kt"} {
		if highlightLine("file"+alias, []rune("class Item {}")) != highlightLine("file"+original, []rune("class Item {}")) {
			t.Fatalf("missing syntax for %s", alias)
		}
	}
	if _, err := languageCompletions([]byte(`[{"label":"bad","textEdit":{"newText":"oops"}}]`), ""); err == nil {
		t.Fatal("missing edit range accepted")
	}
	root := t.TempDir()
	t.Chdir(root)
	b := newBuffer("sample.ts", []byte("unsaved"))
	e := &editor{buffers: []*buffer{b}}
	data, _ := json.Marshal(map[string]any{"uri": fileURI(filepath.Join(root, b.path)), "range": lspRange{Start: lspPosition{0, 3}}})
	e.showLanguageDefinition(data)
	if len(e.buffers) != 1 || b.col != 3 {
		t.Fatal("definition lost an unsaved relative-path buffer")
	}
}
