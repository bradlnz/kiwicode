<p align="center">
  <img src="assets/kiwicode-logo.png" alt="KiwiCode logo" width="220">
</p>

<h1 align="center">KiwiCode</h1>

<p align="center">
  <strong>A terminal-native code editor, built in Go.</strong><br>
  Explore a codebase, edit files, find functions, and run tests in one keyboard-friendly workspace.
</p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white">
  <img alt="Terminal native" src="https://img.shields.io/badge/UI-terminal--native-6d286c">
  <img alt="Built with Go" src="https://img.shields.io/badge/built_with-Go-181717?logo=go&logoColor=white">
</p>

![KiwiCode editing the Taskboard Go API with an expanded file explorer and multiple open tabs](assets/screenshots/editor-explorer.png)

*The included [Taskboard example](examples/taskboard), running in KiwiCode with the Forest theme.
The file explorer is expanded and `internal/httpapi/tasks.go` is open alongside `main.go`.*

## Quick start

Use the Go version declared in [`go.mod`](go.mod) (currently Go 1.27), a Unix-like
terminal with `stty`, and Git. Install the `sqlite3` command for workspace persistence.
The current editor build does not require libvterm.

```sh
git clone git@github.com:bradlnz/kiwicode.git
cd kiwicode
go build -o code-editor ./src

# Launch the same codebase used in the screenshots.
./code-editor examples/taskboard
```

Press **Ctrl+P** to show and focus the file explorer. Use the arrow keys to select
and expand folders, or click them. **Enter** opens the selected file; **Tab** returns
focus to the editor. **Ctrl+B** toggles the sidebar.

Open your own project with `./code-editor /path/to/project`, or run from source:

```sh
go run ./src examples/taskboard
```

For an installed `kiwicode` command, run `./build.sh`. It tests `./src`, builds the
editor, and installs a symlink in `${XDG_BIN_HOME:-~/.local/bin}`. Add that directory
to your `PATH` if necessary.

### A real example, not placeholder files

[Taskboard](examples/taskboard) is a small Go HTTP API with a browser client, an
in-memory task store, API documentation, and four Go tests. It has no external
package dependencies, credentials, database, or cloud services.

```sh
cd examples/taskboard
go test ./...
go run .
# Open http://127.0.0.1:8080 in your browser.
```

The example binds to loopback, is read-only, and resets its seeded tasks on restart.
It is a development sample, not a production service.

## See it in action

These are captures of the **running editor**, not UI mock-ups. The same sample
workspace is used throughout, and the file explorer remains visible during search.

### Find a file

Press **Ctrl+F** and type part of a file path. Here, `tasks` finds API code, tests,
store code, documentation, and the browser client. Use **Up/Down** to select a
result and **Enter** to open it; **Escape** cancels.

![File search for tasks showing matching paths while the editor and expanded explorer remain visible](assets/screenshots/file-search.png)

File search filters **paths**, not text inside files. Matching is case-insensitive;
the list shows up to eight results, so narrow the query when needed.

### Jump to a function

Press **Ctrl+U** to search discovered functions. Results include the file path and
line number. Searching `ListTasks` finds both implementations and the matching
test; **Enter** jumps to the selected definition.

![Function search for ListTasks showing handler, store, and test definitions with file paths and line numbers](assets/screenshots/symbol-search.png)

### Explore tests

Press **Ctrl+E** to open the test explorer. **Enter** opens the selected test at its
definition. **Space** selects or deselects a test, and **R** runs the checked tests.

![Test explorer with four discovered Go tests and TestListTasks open at its definition](assets/screenshots/test-explorer.png)

The checkboxes indicate which tests are selected to run; they are not pass/fail badges.

### Run the tests

This capture follows **R** in the test explorer: KiwiCode detects `go test ./...`
and displays the example's actual successful test output. **Escape** closes the
output view and returns to the code.

![KiwiCode command output showing go test ./... succeeding for the Taskboard API and task store](assets/screenshots/test-run.png)

The current command panel captures output and displays it when the command
finishes. It is **not** a persistent interactive PTY shell; full-screen terminal
programs and interactive coding CLIs are not demonstrated by these screenshots.

## Everyday controls

| Key | Action |
| --- | --- |
| Mouse | Menus, tabs, file selection, folder expansion, scrolling, and code selection |
| `Ctrl+P` / `Ctrl+B` | Show and focus Files / toggle the explorer |
| `Ctrl+F` / `Ctrl+U` | Search file paths / discovered functions |
| `Ctrl+D` | Go to definition |
| `Ctrl+E` | Test explorer |
| `Ctrl+T` | Toggle command panel |
| `Ctrl+K` | Format the current file |
| `Ctrl+G` | Dependency report |
| `Ctrl+A` / `Ctrl+X` | Switch Agent/file workspace / cancel an agent run |
| `Ctrl+S` / `Ctrl+Z` | Save / undo in a file tab |
| `Ctrl+W` / `Ctrl+Q` | Close tab / quit |
| `Ctrl+O` | Shortcut help |

Closing a dirty tab or quitting with unsaved changes requires a second command.
When a search or menu is open, its own navigation keys take precedence.

## More of the workspace

**Source Control** is available from the Source menu or the sidebar's branch icon:
review diffs, stage changes, and compose commits. Discard operations are guarded.

**Dependency Graph** (`Ctrl+G`) reports syntactic imports and interfaces.
**View → Architecture Canvas** shows a folder/dependency overview. These are
bounded static views, not a language-server call graph or a draggable node editor.

**Editing** includes syntax colours for Go, Ruby/Rails, C#, JavaScript/TypeScript,
Python, Rust, C/C++, Java, SQL, JSON, YAML, HCL, and other supported formats, plus
word wrap, completion, formatting, and code inspection. Completion and definition
lookup use bounded syntax inference; they are not a replacement for full
language-server type and overload resolution.

**Agent** is a first-class tab alongside files. Provider configuration, approvals,
reviewed changes, cancellation, and persisted context are covered in the
[Agent workspace guide](docs/agent-workspace.md). The screenshot workflow does not
configure or call an external model and makes no hosted-provider acceptance claim.

## Configuration

Use a project `.code-editor.yaml` or global `~/.config/code-editor/config.yaml`.
Project settings are read after global settings. The sample includes this layout:

```yaml
theme: forest

explorer:
  max_width_percent: 35

layout:
  top_menu_padding: 2
  tab_padding: 1
  sidebar_tab_padding: 1
  explorer_indent: 2
```

Choose `plum`, `forest`, `amber`, or `mono`; themes are also available in View.
The `colors` section overrides individual 256-colour roles. Icons and keyboard
shortcuts are configurable; see [the repository configuration](.code-editor.yaml).
Layout padding values must be between 0 and 8.

Workspace state is stored outside the project in `$XDG_STATE_HOME/code-editor`
or `~/.local/state/code-editor`, using the `sqlite3` command.

## Dependency graph MCP

Expose the compact project graph to an external MCP client:

```sh
/path/to/kiwicode/code-editor --mcp /path/to/project
```

The server provides the `project_dependency_graph` tool over stdio.

## Development

```text
kiwicode/
├── assets/             # Logo and real editor screenshots
├── examples/taskboard/ # Runnable demo; its own Go module
├── src/                # Editor, rendering, input, and integration tests
├── internal/           # Agent and persisted context implementation
├── docs/               # Agent, performance, and screenshot guides
├── tests/              # Real-terminal smoke test and test entrypoint
├── tools/              # Repeatable README screenshot capture
├── build.sh            # Test, build, and install
└── run.sh              # Run from source
```

```sh
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -o code-editor ./src
python3 tests/agent_terminal.py ./code-editor

# Nested example modules are not covered by the root ./... pattern.
(cd examples/taskboard && go test -count=1 ./... && go vet ./...)
```

CI runs tests, race checks, vet, the editor build, a real-terminal smoke test, and
component benchmarks. See [performance notes](docs/performance.md); component
measurements are not whole-editor latency claims.

### Refresh the screenshots

```sh
# Debian/Ubuntu capture dependencies; Go is installed separately.
sudo apt-get install xvfb xauth xterm x11-utils fonts-dejavu-core python3-pil sqlite3

go build -o code-editor ./src
python3 tools/capture_readme.py --binary ./code-editor
```

The capture script launches the real binary under xterm/Xvfb, drives the example
with keyboard and mouse input, checks navigation and actual test output, and
captures the X11 window. It uses a temporary copy of the example and isolated
home/state directories, then verifies the example files were not modified.

See [screenshot capture and provenance](docs/screenshots.md) for dependencies,
artifacts, assertions, and the GitHub Actions workflow.
