package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if filepath.Base(mustCwd()) == "src" {
		_ = os.Chdir("..")
	}
	os.Exit(m.Run())
}

func TestEditing(t *testing.T) {
	b := newBuffer("test.txt", []byte("ab\ncd"))
	b.col = 1

	b.handle(key{r: 'X'})
	b.handle(key{code: keyEnter})
	b.handle(key{code: keyBackspace})
	b.handle(key{code: keyDelete})

	got := runeLines(b.lines)
	if len(got) != 2 || got[0] != "aX" || got[1] != "cd" || !b.dirty {
		t.Fatalf("unexpected edit result: %#v, dirty=%v", got, b.dirty)
	}
	undo := newBuffer("undo.txt", []byte("a"))
	undo.col = 1
	undo.handle(key{r: 'b'})
	if !undo.undoChange() || string(undo.lines[0]) != "a" || undo.dirty {
		t.Fatalf("undo did not restore the buffer: %#v", undo)
	}

	wide := newBuffer("wide.go", []byte("abcdefghijklmnopqrstuvwxyz"))
	for range 20 {
		wide.handle(key{code: keyRight})
		wide.ensureVisible(5, 12)
	}
	if wide.scrollX == 0 {
		t.Fatal("right arrow did not reveal the end of a long line")
	}
	for range 20 {
		wide.handle(key{code: keyLeft})
	}
	wide.ensureVisible(5, 12)
	if wide.scrollX != 0 {
		t.Fatal("left arrow did not return the horizontal viewport")
	}

	wide.col = 20
	wide.ensureWrappedVisible(3, 6)
	rows := wide.wrappedRows(3, 6)
	if cursor := wide.cursorViewRow(6); len(rows) != 3 || cursor.segment != 3 || rows[2] != cursor {
		t.Fatalf("word-wrapped cursor was not visible: %#v cursor=%#v", rows, cursor)
	}
	view := &editor{buffers: []*buffer{wide}}
	if handled, _ := view.handleCommand("word-wrap"); !handled || !view.wordWrap {
		t.Fatal("View → Word Wrap did not enable wrapping")
	}
}

func TestNestedProjectCommand(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "app"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app", "go.mod"), []byte("module app\n"), 0644); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	if got := projectCommand("app/main_test.go", true); got != "cd 'app' && go test ./..." {
		t.Fatalf("nested test project was not detected: %q", got)
	}
	e := &editor{tests: []testCase{{name: "TestOne"}, {name: "TestTwo"}}}
	if got, _ := e.checkedTestCommand(projectCommand("app/main_test.go", true), 1); got != "cd 'app' && go test ./... -run '^(TestTwo)$'" {
		t.Fatalf("nested single-test command was not filtered: %q", got)
	}
}

func TestFileClickKeepsSelection(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	e := newEditor()
	e.showExplorer, e.explorer = true, true
	e.cols, e.rows = 90, 24
	e.handle(key{mouse: true, button: 0, x: 5, y: 4})
	if e.selected != 1 || e.current().path != "b.go" {
		t.Fatalf("click did not persist selection: selected=%d path=%q", e.selected, e.current().path)
	}
}

func TestNewFilePromptUsesSelectedFolder(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "models"), 0755); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	e := &editor{buffers: []*buffer{newBuffer("main.go", nil)}, explorer: true, tree: []treeEntry{{path: "models", dir: true}}}
	e.openNewFilePrompt()
	for _, character := range "customer.go" {
		e.handleNewFilePrompt(key{r: character})
	}
	e.handleNewFilePrompt(key{code: keyEnter})
	if e.newFilePrompt || e.current().path != filepath.Join("models", "customer.go") {
		t.Fatalf("new file did not open in the selected folder: %q", e.current().path)
	}
	if _, err := os.Stat(e.current().path); err != nil {
		t.Fatal(err)
	}
}

func TestOpenFolderPredictions(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"alpha", "alpine", ".archive"} {
		if err := os.Mkdir(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "also.txt"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	predictions := predictFolders(filepath.Join(root, "al"))
	if len(predictions) != 2 || !strings.HasSuffix(predictions[0], "alpha/") || !strings.HasSuffix(predictions[1], "alpine/") {
		t.Fatalf("unexpected folder predictions: %#v", predictions)
	}
	e := &editor{folderInput: []rune(filepath.Join(root, "al"))}
	e.updateFolderPredictions()
	e.handleFolderPrompt(key{code: keyDown})
	e.handleFolderPrompt(key{code: keyTab})
	if !strings.HasSuffix(string(e.folderInput), "alpha/") {
		t.Fatalf("folder prediction was not completed: %q", e.folderInput)
	}
}

func TestMouseInput(t *testing.T) {
	k := parseMouse("0;5;3", false)
	if !k.mouse || k.button != 0 || k.x != 5 || k.y != 3 {
		t.Fatalf("unexpected mouse event: %#v", k)
	}

	e := &editor{buffers: []*buffer{newBuffer("test.txt", []byte("abc"))}, cols: 90, rows: 24}
	e.handle(k)
	if e.current().row != 0 || e.current().col != 1 {
		t.Fatalf("unexpected cursor: %d:%d", e.current().row, e.current().col)
	}
	e.handle(key{r: 20})
	if !e.shell.open || !e.shell.focused {
		t.Fatal("terminal activity button did not open the terminal")
	}
}

func TestTabClose(t *testing.T) {
	first := newBuffer("first.go", nil)
	e := &editor{buffers: []*buffer{first, newBuffer("second.go", nil)}}
	e.selectTab(tabWidth(first) - 3)
	if len(e.buffers) != 1 || e.current().path != "second.go" {
		t.Fatalf("tab did not close: %#v", runeLines(e.current().lines))
	}
	for i := 3; i <= 8; i++ {
		e.buffers = append(e.buffers, newBuffer(fmt.Sprintf("tab%d.go", i), nil))
	}
	e.active = len(e.buffers) - 1
	if start, end := e.visibleTabRange(); start != 1 || end != 7 {
		t.Fatalf("visible tab range = %d:%d", start, end)
	}
	if i, _ := e.tabAt(0); i != 1 {
		t.Fatalf("first visible tab click selected %d", i)
	}
}

func TestShellAndSyntax(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	var shell shellPanel
	shell.run("printf hello")
	if len(shell.output) != 2 || shell.output[1] != "hello" {
		t.Fatalf("unexpected shell output: %#v", shell.output)
	}
	shell.start("sleep 30")
	defer shell.stop()
	deadline := time.Now().Add(time.Second)
	for shell.cmd.Process == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if shell.cmd.SysProcAttr == nil || !shell.cmd.SysProcAttr.Setpgid || !shell.stop() || !shell.stopping {
		t.Fatal("running process group could not be stopped")
	}
	if !shell.stop() {
		t.Fatal("second stop did not force the running process group to exit")
	}
	deadline = time.Now().Add(time.Second)
	for !shell.poll() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if shell.running {
		t.Fatal("stopped process group is still running")
	}
	colored := highlightLine("main.go", []rune(`func main() { // hi`))
	if !strings.Contains(colored, "\x1b[38;5;213mfunc") || !strings.Contains(colored, "\x1b[38;5;244m// hi") {
		t.Fatalf("expected keyword and comment colors: %q", colored)
	}
}

func TestFormattingAndIndent(t *testing.T) {
	formatted, err := formatCode("main.go", []byte("package main\nfunc main(){println(1)}"))
	if err != nil || !strings.Contains(string(formatted), "func main() {") {
		t.Fatalf("format failed: %q, %v", formatted, err)
	}
	b := newBuffer("main.go", []byte("if ok {"))
	b.col = len(b.lines[0])
	b.handle(key{code: keyEnter})
	if string(b.lines[1]) != "    " || b.col != 4 {
		t.Fatalf("smart indent failed: %q col=%d", b.lines[1], b.col)
	}
}

func TestDirtyStateAndAsyncWorkspaceLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clean.go")
	if err := os.WriteFile(path, []byte("package clean\n"), 0644); err != nil {
		t.Fatal(err)
	}
	clean := newBuffer(path, []byte("package clean\n"))
	clean.dirty = true
	e := &editor{buffers: []*buffer{clean}}
	if e.dirty() || clean.dirty {
		t.Fatal("a buffer matching disk was still marked dirty")
	}

	edited := newBuffer("untitled.txt", []byte("work"))
	edited.dirty = true
	e = &editor{buffers: []*buffer{edited}, rows: 30, cols: 100, workspaceDone: make(chan editorLoad, 1)}
	loaded := &editor{buffers: []*buffer{newBuffer(path, nil)}, collapsed: map[string]bool{}}
	e.workspaceDone <- editorLoad{editor: loaded}
	if done, err := e.pollWorkspace(); !done || err != nil || len(e.buffers) != 2 || e.current() != edited || e.rows != 30 || e.cols != 100 {
		t.Fatalf("async workspace load lost live editor state: done=%v err=%v buffers=%d", done, err, len(e.buffers))
	}
}

func TestMenusSearchAndProjects(t *testing.T) {
	e := &editor{
		files:   []string{"cmd/app/main.go", "README.md"},
		tree:    []treeEntry{{path: "cmd", dir: true}, {path: "cmd/app/main.go"}},
		buffers: []*buffer{newBuffer("README.md", nil)},
		cols:    100,
		rows:    30,
	}
	e.handle(key{mouse: true, button: 0, x: 2, y: 1})
	if e.popup == nil || e.popup.top != "File" {
		t.Fatal("top menu did not open")
	}
	e.handle(key{code: keyEnter})
	if e.searchMode != "files" {
		t.Fatal("Enter did not activate Open File from the File menu")
	}
	e.searchMode = ""
	e.handle(key{mouse: true, button: 2, x: 50, y: 8})
	if e.popup == nil || len(e.popup.items) == 0 {
		t.Fatal("context menu did not open")
	}
	e.openSearch("files")
	e.handleSearch(key{r: 'r'})
	if len(e.searchResults) != 1 || e.searchResults[0].path != "README.md" {
		t.Fatalf("unexpected search results: %#v", e.searchResults)
	}
	if treeLabel(e.tree[1]) != "      main.go" {
		t.Fatalf("unexpected tree label: %q", treeLabel(e.tree[1]))
	}
	wide := &editor{showExplorer: true, cols: 100, tree: []treeEntry{{path: "deep/folder/with/a-very-long-file-name.go"}}}
	if width := wide.sidebarWidth(); width <= 27 {
		t.Fatalf("sidebar did not grow for deep filenames: %d", width)
	}
	if !strings.Contains(e.topBar(), "48;5;53") {
		t.Fatal("top menu does not use the distinct plum palette")
	}
	completion := newBuffer("main.go", []byte("fu"))
	completion.col = 2
	if got := string(completion.suggestion()); got != "nc" {
		t.Fatalf("unexpected suggestion: %q", got)
	}
	if !isTestFile("pkg/widget_test.go") || !isTestFile("app.spec.ts") || isTestFile("main.go") {
		t.Fatal("test discovery failed")
	}
	request := mcpRequest{Method: "tools/list", ID: json.RawMessage("1")}
	if result, err := handleMCP(request, "."); err != nil || result == nil {
		t.Fatalf("MCP tool listing failed: %#v %v", result, err)
	}
	if got := projectCommand("main.go", true); got != "go test ./..." {
		t.Fatalf("unexpected test command: %q", got)
	}
	containerRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(containerRoot, "Containerfile"), []byte("FROM scratch\n"), 0644); err != nil {
		t.Fatal(err)
	}
	buildContainer := containerCommand(filepath.Join(containerRoot, "cmd", "main.go"), true)
	runContainer := containerCommand(filepath.Join(containerRoot, "cmd", "main.go"), false)
	if !strings.Contains(buildContainer, "nerdctl --namespace kiwicode build") ||
		!strings.Contains(runContainer, "nerdctl --namespace kiwicode run --rm --read-only") ||
		!strings.Contains(runContainer, "--network none --cap-drop ALL --security-opt no-new-privileges") {
		t.Fatalf("container commands are not sandboxed: %q %q", buildContainer, runContainer)
	}
	dockerRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(dockerRoot, "Dockerfile"), []byte("FROM scratch\n"), 0644); err != nil {
		t.Fatal(err)
	}
	containerDebug := debugCommand(filepath.Join(dockerRoot, "main.go"))
	if !strings.Contains(containerDebug, "build -f '"+filepath.Join(dockerRoot, "Dockerfile")+"'") || !strings.Contains(containerDebug, " && nerdctl --namespace kiwicode run") {
		t.Fatalf("Dockerfile debug did not build and run through containerd: %q", containerDebug)
	}
	if !strings.Contains(highlightLine("main.tf", []rune(`resource "x" "y" {`)), "\x1b[38;5;213mresource") ||
		!strings.Contains(highlightLine("app.yaml", []rune("enabled: true")), "\x1b[38;5;81menabled") ||
		!strings.Contains(highlightLine("Program.cs", []rune("public class App")), "\x1b[38;5;213mpublic") {
		t.Fatal("YAML, Terraform, or .NET syntax highlighting failed")
	}
	csharpTypes := highlightLine("Worker.cs", []rune(`public Guid Id { get; set; } public bool IsReady; public string Name;`))
	for _, token := range []string{"Guid", "bool", "string"} {
		if !strings.Contains(csharpTypes, ansiFG(colors.typeName)+token) {
			t.Fatalf("C# type %q did not use the type color: %q", token, csharpTypes)
		}
	}
	for _, name := range []string{"Id", "IsReady", "Name"} {
		if strings.Contains(csharpTypes, ansiFG(colors.typeName)+name) {
			t.Fatalf("C# name %q incorrectly used the type color: %q", name, csharpTypes)
		}
	}
	markup := highlightLine("index.html", []rune(`<main class="page">Hello</main>`))
	for _, token := range []string{ansiFG(colors.function) + "main", ansiFG(colors.parameter) + "class", ansiFG(colors.stringValue) + `"page"`} {
		if !strings.Contains(markup, token) {
			t.Fatalf("HTML token %q was not colored: %q", token, markup)
		}
	}
	xml := highlightLine("feed.xml", []rune(`<?xml version="1.0"?><item id="7"/>`))
	if !strings.Contains(xml, ansiFG(colors.function)+"xml") || !strings.Contains(xml, ansiFG(colors.parameter)+"id") {
		t.Fatalf("XML syntax highlighting failed: %q", xml)
	}
	if !isMarkupDocument("manifest", [][]rune{[]rune(`<application>`), []rune(`</application>`)}) ||
		!isMarkupDocument("fragment", [][]rune{[]rune(`<service />`)}) ||
		isMarkupDocument("compare.txt", [][]rune{[]rune(`value < limit && value > minimum`)}) {
		t.Fatal("structural XML detection failed")
	}
	rails := highlightLine("app/models/user.rb", []rune(`belongs_to :account`))
	if !strings.Contains(rails, ansiFG(colors.function)+"belongs_to") || !strings.Contains(rails, ansiFG(colors.stringValue)+":account") {
		t.Fatalf("Rails model syntax highlighting failed: %q", rails)
	}
	gemfile := highlightLine("Gemfile", []rune(`gem "rails"`))
	if !strings.Contains(gemfile, ansiFG(colors.function)+"gem") || !strings.Contains(gemfile, ansiFG(colors.stringValue)+`"rails"`) {
		t.Fatalf("Gemfile syntax highlighting failed: %q", gemfile)
	}
	rawString := [][]rune{
		[]rune(`var hostAction = $"""`),
		[]rune(`    On {nodeName}, install the control-plane.`),
		[]rune(`    """;`),
	}
	opener := csharpRawHighlights("Cluster.cs", rawString, 0, 1)[0]
	scrolled := csharpRawHighlights("Cluster.cs", rawString, 1, 3)
	if !strings.Contains(opener, ansiFG(colors.keyword)+"var") ||
		!strings.Contains(opener, ansiFG(colors.stringValue)+`$"""`) ||
		!strings.Contains(scrolled[1], ansiFG(colors.stringValue)+"    On {nodeName}") ||
		!strings.Contains(scrolled[2], ansiFG(colors.stringValue)+`    """`) {
		t.Fatalf("C# raw string highlighting failed: %q %#v", opener, scrolled)
	}
	erb := highlightLine("app/views/users/show.html.erb", []rune(`<%= link_to @user.name, user_path(@user) %>`))
	if !strings.Contains(erb, ansiFG(colors.operator)+"<%=") || !strings.Contains(erb, ansiFG(colors.function)+"link_to") || !strings.Contains(erb, ansiFG(colors.parameter)+"@user") {
		t.Fatalf("ERB syntax highlighting failed: %q", erb)
	}
	slopPath := filepath.Join(t.TempDir(), "generated.go")
	if err := os.WriteFile(slopPath, []byte("package p\n// This function is responsible for everything\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if findings := slopFindings([]string{slopPath}); len(findings) != 1 {
		t.Fatalf("slop signal was not detected: %#v", findings)
	}
	csPath := filepath.Join(t.TempDir(), "Program.cs")
	if err := os.WriteFile(csPath, []byte("using System.Text;\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if graph := strings.Join(dependencyGraph([]string{csPath}), "\n"); !strings.Contains(graph, "System.Text") {
		t.Fatalf(".NET dependency was not detected: %q", graph)
	}
	interfaces := []struct{ name, source, want string }{
		{"Customer.ts", "export interface Customer {\n  Name: string\n  Save(): void\n}\n", "interface Customer { Name, Save }"},
		{"Customer.cs", "public interface ICustomer {\n  string Name { get; }\n  void Save();\n}\n", "interface ICustomer { Name, Save }"},
		{"customer.go", "package p\ntype Customer interface {\n  Name() string\n  Save()\n}\n", "interface Customer { Name, Save }"},
	}
	for _, model := range interfaces {
		interfacePath := filepath.Join(t.TempDir(), model.name)
		if err := os.WriteFile(interfacePath, []byte(model.source), 0644); err != nil {
			t.Fatal(err)
		}
		if graph := strings.Join(dependencyGraph([]string{interfacePath}), "\n"); !strings.Contains(graph, model.want) {
			t.Fatalf("interface members were not added to the dependency graph: %q", graph)
		}
	}
	if command := debugCommand("main.go"); command != "GOTRACEBACK=all go run ." {
		t.Fatalf("unexpected debug command: %q", command)
	}
	symbolPath := filepath.Join(t.TempDir(), "symbols.go")
	if err := os.WriteFile(symbolPath, []byte("package p\nfunc SearchMe() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if symbols := fileSymbols(symbolPath); len(symbols) != 1 || symbols[0].name != "SearchMe" || symbols[0].row != 2 {
		t.Fatalf("function search failed: %#v", symbols)
	}
	selectionBuffer := newBuffer("copy.go", []byte("copy this"))
	selectionEditor := &editor{buffers: []*buffer{selectionBuffer}, selection: textSelection{buffer: selectionBuffer, startCol: 0, endCol: 4}}
	if text := selectionEditor.selectedText(); text != "copy" {
		t.Fatalf("selection copy failed: %q", text)
	}
	paste := newBuffer("paste.go", []byte("ab"))
	paste.col = 1
	paste.insertText("X\nY")
	if lines := runeLines(paste.lines); len(lines) != 2 || lines[0] != "aX" || lines[1] != "Yb" {
		t.Fatalf("multiline paste failed: %#v", lines)
	}
	if !paste.undoChange() || string(paste.lines[0]) != "ab" {
		t.Fatalf("multiline paste was not undoable: %#v", runeLines(paste.lines))
	}
	canvas := strings.Join(architectureCanvas(e.tree, e.files, 90), "\n")
	if !strings.Contains(canvas, "FOLDERS") || !strings.Contains(canvas, "DIRECT DEPENDENCIES") {
		t.Fatalf("architecture canvas failed: %q", canvas)
	}
	maximized := &editor{rows: 30, cols: 100, showExplorer: true, shell: shellPanel{open: true}}
	if content, terminal := maximized.panelHeights(); content != 27 || terminal != 0 {
		t.Fatalf("terminal modal hid the file: content=%d terminal=%d", content, terminal)
	}
}

func TestThemeKeymapAndParameters(t *testing.T) {
	defer resetSettings()
	resetSettings()
	if shortcutAction(21) != "function-search" {
		t.Fatal("Ctrl+U should open quick function search")
	}
	if shortcutAction(26) != "undo" {
		t.Fatal("Ctrl+Z should undo")
	}
	if err := applySetting("", "theme", "forest"); err != nil {
		t.Fatal(err)
	}
	if err := applySetting("colors", "parameter", "201"); err != nil {
		t.Fatal(err)
	}
	if err := applySetting("colors", "folder", "202"); err != nil || colors.folder != 202 {
		t.Fatalf("custom folder color failed: %v %d", err, colors.folder)
	}
	if err := applySetting("layout", "tab_padding", "3"); err != nil || settings.tabPadding != 3 {
		t.Fatalf("custom tab padding failed: %v %d", err, settings.tabPadding)
	}
	if err := applySetting("icons", "files", "F"); err != nil || settings.icons["files"] != "F" {
		t.Fatalf("custom activity icon failed: %v %q", err, settings.icons["files"])
	}
	if err := applySetting("shortcuts", "save", "ctrl+x"); err != nil || shortcutAction(24) != "save" || shortcutAction(19) == "save" {
		t.Fatalf("custom shortcut failed: %v %#v", err, settings.bindings)
	}
	colored := highlightLine("main.go", []rune("func add(value int, name string) {}"))
	if !strings.Contains(colored, ansiFG(201)+"value") || !strings.Contains(colored, ansiFG(201)+"name") {
		t.Fatalf("parameters did not use the configured color: %q", colored)
	}
	signature := highlightLine("buffer.go", []rune("func newBuffer(path string, data []byte) *buffer {"))
	for _, token := range []string{"string", "byte", "buffer"} {
		if !strings.Contains(signature, ansiFG(colors.typeName)+token) {
			t.Fatalf("type %q was not colored in signature: %q", token, signature)
		}
	}
	if !strings.Contains(signature, ansiFG(colors.function)+"newBuffer") {
		t.Fatalf("function name was not colored: %q", signature)
	}
	if incomplete := highlightLine("main.go", []rune("func (s Service) Run")); !strings.Contains(incomplete, "Run") {
		t.Fatalf("incomplete Go method highlighting failed: %q", incomplete)
	}
	var cell strings.Builder
	writeCell(&cell, 2, 3, "text")
	if !strings.Contains(cell.String(), "\x1b[0;49m") {
		t.Fatalf("cell did not reset leaked terminal attributes: %q", cell.String())
	}
}

func TestCollapsibleFoldersAndTestCases(t *testing.T) {
	if bar := (&editor{explorer: true}).sidebarActivityBar(); !strings.HasPrefix(bar, "  \x1b[1;4m") {
		t.Fatalf("file activity underline included its padding: %q", bar)
	}
	if view, ok := sidebarModeAt(2); !ok || view != "files" {
		t.Fatalf("file activity icon mapping failed: %q %v", view, ok)
	}
	if view, ok := sidebarModeAt(7); !ok || view != "tests" {
		t.Fatalf("test activity icon mapping failed: %q %v", view, ok)
	}
	if view, ok := sidebarModeAt(12); !ok || view != "source" {
		t.Fatalf("source activity icon mapping failed: %q %v", view, ok)
	}
	e := &editor{
		tree: []treeEntry{
			{path: "cmd", dir: true},
			{path: "cmd/app", dir: true},
			{path: "cmd/app/main.go"},
			{path: "README.md"},
		},
		buffers: []*buffer{newBuffer("README.md", nil)},
	}
	e.toggleFolder("cmd")
	visible := e.visibleTree()
	if len(visible) != 2 || visible[0].path != "cmd" || !strings.Contains(e.explorerTreeLabel(visible[0]), "▸") {
		t.Fatalf("folder did not collapse: %#v %q", visible, e.explorerTreeLabel(visible[0]))
	}
	e.handleExplorer(key{code: keyRight})
	if len(e.visibleTree()) != 4 || e.collapsed["cmd"] {
		t.Fatalf("folder did not expand: %#v", e.visibleTree())
	}

	path := filepath.Join(t.TempDir(), "widget_test.go")
	if err := os.WriteFile(path, []byte("package widget\nfunc TestCreate(t *testing.T) {}\nfunc helper() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tests := discoverTests(path)
	if len(tests) != 1 || tests[0].name != "TestCreate" || tests[0].row != 2 || !strings.Contains(testLabel(tests[0]), "☑ TestCreate") {
		t.Fatalf("test functions were not discovered: %#v", tests)
	}
	e.tests, e.testMode, e.explorer = tests, true, true
	e.handleExplorer(key{r: ' '})
	if e.tests[0].checked || !strings.Contains(testLabel(e.tests[0]), "☐ TestCreate") {
		t.Fatalf("test checkbox did not toggle: %#v", e.tests[0])
	}
	e.tests = []testCase{{name: "TestCreate", checked: true}, {name: "TestDelete", checked: false}}
	if testRowAction(3, e.tests[0]) != "run" || testRowAction(9, e.tests[0]) != "toggle" || testRowAction(10, e.tests[0]) != "open" {
		t.Fatalf("test row buttons do not match their labels: %q", testRowLabel(e.tests[0]))
	}
	command, err := e.checkedTestCommand("go test ./...")
	if err != nil || !strings.Contains(command, "-run") || !strings.Contains(command, "TestCreate") || strings.Contains(command, "TestDelete") {
		t.Fatalf("checked tests did not filter the runner: %q %v", command, err)
	}
	command, err = e.checkedTestCommand("go test ./...", 1)
	if err != nil || !strings.Contains(command, "TestDelete") || strings.Contains(command, "TestCreate") {
		t.Fatalf("single-test button did not filter the runner: %q %v", command, err)
	}
}

func TestIgnoredFilesCSharpCompletionAndSourceLabel(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"bin", "obj", "node_modules", "ignored"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, directory, "generated.go"), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored/\n*.log\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "visible.go"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "hidden.log"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	visible := strings.Join(projectFiles(root), "\n")
	if !strings.Contains(visible, "visible.go") || strings.Contains(visible, "generated.go") || strings.Contains(visible, "hidden.log") {
		t.Fatalf("project ignore filtering failed: %q", visible)
	}
	oldDirectory, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	explorer := &editor{}
	explorer.refreshFiles()
	if err := os.Chdir(oldDirectory); err != nil {
		t.Fatal(err)
	}
	if !explorer.collapsed["src"] {
		t.Fatal("folders should be collapsed on first discovery")
	}

	app := filepath.Join(root, "Program.cs")
	model := filepath.Join(root, "Customer.cs")
	appSource := []byte("using Demo.Models;\nCustomer customer;\ncustomer.")
	if err := os.WriteFile(app, appSource, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(model, []byte("namespace Demo.Models;\npublic class Customer {\n    public string Name { get; set; }\n    public int Age;\n}"), 0644); err != nil {
		t.Fatal(err)
	}
	b := newBuffer(app, appSource)
	b.row, b.col = 2, len(b.lines[2])
	e := &editor{buffers: []*buffer{b}, files: []string{app, model}}
	_ = e.suggestion()
	deadline := time.Now().Add(time.Second)
	for !e.pollCompletion() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := string(e.suggestion()); got != "Age" {
		t.Fatalf("C# dependency completion failed: %q", got)
	}
	if label := sourceLabel(sourceChange{status: "??", path: "new.cs"}); label != "U  new.cs" {
		t.Fatalf("unexpected source-control label: %q", label)
	}
	changes := parseSourceChanges(" M Program.cs\n?? New.cs\nR  Old.cs -> Renamed.cs\n")
	if len(changes) != 3 || changes[1].path != "New.cs" || changes[2].path != "Renamed.cs" {
		t.Fatalf("source-control status parsing failed: %#v", changes)
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	oldDirectory, _ = os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDirectory)
	savedBuffer := newBuffer("scratch.cs", []byte("unsaved"))
	savedBuffer.dirty, savedBuffer.row, savedBuffer.col = true, 0, 4
	saved := &editor{
		buffers: []*buffer{savedBuffer}, collapsed: map[string]bool{"src": false}, showExplorer: true,
		shell: shellPanel{history: []string{"go test ./..."}},
		completionCache: map[string]dependencyCompletion{"scratch.cs": {
			words: []string{"Customer"}, members: map[string][]string{"Customer": {"Name"}},
			definitions: map[string]definitionLocation{"Name": {"Customer.cs", 3}},
		}},
	}
	if err := saved.saveState(); err != nil {
		t.Fatal(err)
	}
	restored := &editor{collapsed: map[string]bool{}}
	if err := restored.restoreState(); err != nil {
		t.Fatal(err)
	}
	if len(restored.buffers) != 1 || string(restored.buffers[0].lines[0]) != "unsaved" || !restored.buffers[0].dirty || restored.collapsed["src"] || len(restored.shell.history) != 1 || restored.completionCache["scratch.cs"].members["Customer"][0] != "Name" || restored.completionCache["scratch.cs"].definitions["Name"].row != 3 {
		t.Fatalf("SQLite workspace state was not restored: %#v", restored)
	}
}

func TestDependencyMemberCompletionAcrossLanguages(t *testing.T) {
	tests := []struct {
		name, ext, model, app string
	}{
		{"Go", ".go", "package demo\ntype Customer struct { Name string }\nfunc (Customer) Save() {}\n", "package demo\nfunc f() {\ncustomer := Customer{}\ncustomer.\n}\n"},
		{"JavaScript", ".js", "export class Customer {\nName = ''\nSave() {}\n}\n", "const customer = new Customer()\ncustomer.\n"},
		{"TypeScript", ".ts", "export interface Customer {\nName: string\nSave(): void\n}\n", "const customer: Customer = getCustomer()\ncustomer.\n"},
		{"Python", ".py", "class Customer:\n    def __init__(self):\n        self.Name = ''\n    def Save(self): pass\n", "customer = Customer()\ncustomer.\n"},
		{"Rust", ".rs", "struct Customer {\n    Name: String,\n}\nimpl Customer { pub fn Save(&self) {} }\n", "let customer = Customer { Name: String::new() };\ncustomer.\n"},
		{"C++", ".cpp", "class Customer {\npublic:\n    string Name;\n    void Save() {}\n};\n", "Customer customer;\ncustomer.\n"},
		{"Java", ".java", "class Customer {\n    String Name;\n    void Save() {}\n}\n", "Customer customer;\ncustomer.\n"},
		{"Kotlin", ".kt", "class Customer {\n    val Name: String = \"\"\n    fun Save() {}\n}\n", "val customer: Customer = Customer()\ncustomer.\n"},
		{"Swift", ".swift", "struct Customer {\n    var Name: String\n    func Save() {}\n}\n", "let customer: Customer = makeCustomer()\ncustomer.\n"},
		{"FSharp", ".fs", "type Customer() =\n    member this.Name = \"\"\n    member this.Save() = ()\n", "let customer = Customer()\ncustomer.\n"},
		{"VisualBasic", ".vb", "Class Customer\n    Public Property Name As String\n    Public Sub Save()\n    End Sub\nEnd Class\n", "Dim customer As Customer\ncustomer.\n"},
		{"Ruby", ".rb", "class Customer\n  def initialize\n    @Name = ''\n  end\n  def Save; end\nend\n", "customer = Customer.new\ncustomer.\n"},
		{"PHP", ".php", "class Customer {\n    public string $Name;\n    public function Save() {}\n}\n", "$customer = new Customer();\n$customer->\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			model := filepath.Join(root, "Customer"+test.ext)
			app := filepath.Join(root, "app"+test.ext)
			if err := os.WriteFile(model, []byte(test.model), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(app, []byte(test.app), 0644); err != nil {
				t.Fatal(err)
			}
			completion := dependencyCompletions(app, []string{model})
			b := newBuffer(app, []byte(test.app))
			for row, line := range b.lines {
				if strings.HasSuffix(strings.TrimSpace(string(line)), ".") || strings.HasSuffix(strings.TrimSpace(string(line)), "->") {
					b.row = row
				}
			}
			b.col = len(b.lines[b.row])
			if got := string(dependencySuggestion(b, completion)); got != "Name" {
				t.Fatalf("member completion = %q, members = %#v", got, completion.members)
			}
		})
	}
}

func TestSourceControlMarksChangedLines(t *testing.T) {
	markers := parseSourceMarkers([]byte("@@ -1 +1 @@\n-old\n+new\n@@ -4,0 +5,2 @@\n+a\n+b\n@@ -8,2 +9,0 @@\n-x\n-y\n"))
	if markers[1] != '~' || markers[5] != '+' || markers[6] != '+' || markers[9] != '-' {
		t.Fatalf("source hunk markers were parsed incorrectly: %#v", markers)
	}
	path := filepath.Join(t.TempDir(), "new.txt")
	if err := os.WriteFile(path, []byte("first line\n"), 0644); err != nil {
		t.Fatal(err)
	}
	e := &editor{sourceChanges: []sourceChange{{status: "??", path: path}}}
	e.loadSourceFileChanges(path)
	deadline := time.Now().Add(time.Second)
	for !e.pollSourceControl() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if marker := e.sourceLineChanges[path][1]; marker != '+' {
		t.Fatalf("source change marker = %q", marker)
	}
	if sourceLineHighlight('~') == "" {
		t.Fatal("modified source lines were not highlighted")
	}
	e.sourceCommitFocused = true
	e.handleSourceCommit(key{r: 'm'})
	e.handleSourceCommit(key{code: keyEnter})
	e.handleSourceCommit(key{r: 'b'})
	wrapped := (&editor{commitInput: []rune("12345")}).sourceCommitLines(4)
	if string(e.commitInput) != "m\nb" || strings.Join(e.sourceCommitLines(10), "|") != "m|b" || strings.Join(wrapped, "|") != "1234|5" {
		t.Fatalf("source commit box input failed: %q focused=%v", e.commitInput, e.sourceCommitFocused)
	}
	e.discardSourceChange()
	if e.sourceDiscardArmed != path || !strings.HasPrefix(sourceDiscardCommand(e.sourceChanges[0]), "git clean -f") {
		t.Fatalf("source discard was not safely armed: %q", e.sourceDiscardArmed)
	}
}

func TestObjectDotCompletesFromUnsavedBuffersWithoutPerKeyScanning(t *testing.T) {
	root := t.TempDir()
	appPath := filepath.Join(root, "app.go")
	modelPath := filepath.Join(root, "customer.go")
	app := newBuffer(appPath, []byte("package demo\nfunc f() {\ncustomer := &Customer{}\ncustomer"))
	model := newBuffer(modelPath, []byte("package demo\ntype Customer struct {\nName string\n}\n"))
	app.row, app.col, app.dirty = 3, len(app.lines[3]), true
	model.dirty = true
	e := &editor{buffers: []*buffer{app, model}, files: []string{appPath, modelPath}}

	e.handle(key{r: '.'})
	_ = e.suggestion()
	deadline := time.Now().Add(time.Second)
	for !e.pollCompletion() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	e.handle(key{code: keyTab})

	if got := string(app.lines[3]); got != "customer.Name" {
		t.Fatalf("object-dot completion did not use the open model: %q (%s)", got, e.status)
	}
	plain := &editor{buffers: []*buffer{newBuffer(appPath, []byte("a"))}}
	plain.current().col = 1
	plain.handle(key{r: 'b'})
	if plain.completionDone != nil {
		t.Fatal("ordinary typing started a dependency scan")
	}
}

func TestOpeningFileLoadsMembersInBackground(t *testing.T) {
	root := t.TempDir()
	appPath := filepath.Join(root, "Program.cs")
	modelPath := filepath.Join(root, "Customer.cs")
	if err := os.WriteFile(appPath, []byte("Customer customer;\ncustomer."), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelPath, []byte("public class Customer {\npublic string Name { get; set; }\n}"), 0644); err != nil {
		t.Fatal(err)
	}
	e := &editor{files: []string{appPath, modelPath}}
	e.open(appPath)
	if !e.completionPending[appPath] {
		t.Fatal("opening a source file did not start background member loading")
	}
	deadline := time.Now().Add(time.Second)
	for !e.pollCompletion() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	e.current().row, e.current().col = 1, len(e.current().lines[1])
	if got := string(e.suggestion()); got != "Name" {
		t.Fatalf("open-file member cache was not ready: %q", got)
	}
}

func TestCSharpInferredAppCompletionAndDefinition(t *testing.T) {
	root := t.TempDir()
	programPath := filepath.Join(root, "Program.cs")
	programSource := "var builder = WebApplication.CreateBuilder(args);\nvar app = builder.Build();\napp."
	program := newBuffer(programPath, []byte(programSource))
	program.row, program.col = 2, len(program.lines[2])
	completion := dependencyCompletions(programPath, nil, map[string][]byte{programPath: []byte(programSource)})
	if got := string(dependencySuggestion(program, completion)); got != "Map" {
		t.Fatalf("inferred app completion = %q, members = %#v", got, completion.members)
	}

	program.row, program.col = 2, len("app")
	e := &editor{buffers: []*buffer{program}, files: []string{programPath}, completionCache: map[string]dependencyCompletion{programPath: completion}}
	e.goToDefinition()
	if e.definitionLoading || program.row != 1 || !strings.Contains(e.status, "Definition: app") {
		t.Fatalf("go to definition missed inferred app: row=%d status=%q", program.row, e.status)
	}
}

func TestGoToDefinitionUsesBackgroundIndexAcrossFiles(t *testing.T) {
	root := t.TempDir()
	appPath, modelPath := filepath.Join(root, "Program.cs"), filepath.Join(root, "Customer.cs")
	appSource := []byte("Customer customer;\ncustomer.Name")
	modelSource := []byte("public class Customer {\n    public string Name { get; set; }\n}")
	if err := os.WriteFile(modelPath, modelSource, 0644); err != nil {
		t.Fatal(err)
	}
	app := newBuffer(appPath, appSource)
	app.row, app.col = 1, len(app.lines[1])
	completion := dependencyCompletions(appPath, []string{modelPath}, map[string][]byte{appPath: appSource})
	coldApp := newBuffer(appPath, appSource)
	coldApp.row, coldApp.col = app.row, app.col
	cold := &editor{buffers: []*buffer{coldApp}, files: []string{appPath, modelPath}}
	cold.goToDefinition()
	if !cold.definitionLoading || cold.status != "Go to Definition: loading…" {
		t.Fatalf("cold definition search did not expose loading state: %q", cold.status)
	}
	deadline := time.Now().Add(time.Second)
	for !cold.pollDefinition() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if cold.current().path != modelPath || cold.current().row != 1 {
		t.Fatalf("cold definition search failed: path=%q row=%d", cold.current().path, cold.current().row)
	}

	e := &editor{buffers: []*buffer{app}, files: []string{appPath, modelPath}, completionCache: map[string]dependencyCompletion{appPath: completion}}

	e.goToDefinition()

	if e.definitionLoading || e.current().path != modelPath || e.current().row != 1 {
		t.Fatalf("cached definition was not immediate: loading=%v path=%q row=%d", e.definitionLoading, e.current().path, e.current().row)
	}

	otherPath := filepath.Join(root, "Other.cs")
	other := newBuffer(otherPath, []byte("Customer customer;\ncustomer.Name"))
	other.row, other.col = 1, len(other.lines[1])
	reused := &editor{
		buffers: []*buffer{other}, files: []string{otherPath, modelPath},
		completionCache: map[string]dependencyCompletion{appPath: completion},
	}
	reused.goToDefinition()
	if reused.definitionLoading || reused.current().path != modelPath || reused.current().row != 1 {
		t.Fatalf("shared definition index was not reused: loading=%v path=%q row=%d", reused.definitionLoading, reused.current().path, reused.current().row)
	}
}

func TestFileInspectorFindsDRYAndJumpsToSource(t *testing.T) {
	root := t.TempDir()
	block := "const first = loadCustomer()\nvalidateCustomer(first)\nsaveCustomer(first)\nnotifyCustomer(first)\n"
	first := filepath.Join(root, "first.js")
	source := block + "\n\n\n\n\n" + block
	if err := os.WriteFile(first, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	findings := inspectProject([]string{first})
	if len(findings) == 0 || findings[0].principle != "DRY" || findings[0].path != first {
		t.Fatalf("expected a DRY finding in the repeated file: %#v", findings)
	}
	b := newBuffer(first, []byte(source))
	e := &editor{buffers: []*buffer{b}, files: []string{first}, rows: 30, cols: 120, inspect: true, inspectionFindings: findings}
	e.selectInspection(0)
	if e.current().row != findings[0].row-1 || e.selection.empty() {
		t.Fatalf("inspector did not highlight its source: row=%d selection=%#v", e.current().row, e.selection)
	}
	other := filepath.Join(root, "other.js")
	if err := os.WriteFile(other, []byte("throw new UnsupportedOperationException()\n"), 0644); err != nil {
		t.Fatal(err)
	}
	e.files = append(e.files, other)
	e.refreshInspector()
	for _, finding := range e.inspectionFindings {
		if finding.path != first {
			t.Fatalf("inspector included a non-active file: %#v", finding)
		}
	}
}
