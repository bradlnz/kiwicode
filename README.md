# Code Editor

A small terminal code editor written in Go. It has a VS Code-style hierarchical
file and test explorer, tabs, line numbers, multi-language syntax colors, project
function search, suggestions, a dependency graph MCP, quick search, debug stack
traces, architecture canvas, Source Control, and an embedded terminal.

```sh
go run . [folder]
```

| Key | Action |
| --- | --- |
| Mouse | Use menus, drag-select text, Copy/Paste, open files/tabs, close `×`, and scroll |
| Arrow keys | Move cursor; Left/Right collapse or expand Explorer folders |
| `Ctrl+E` | Focus test functions; Space toggles their checkboxes |
| `Ctrl+F` | Quick file search |
| `Ctrl+U` | Quick function/symbol search |
| `Ctrl+T` | Toggle and focus the embedded terminal |
| `Ctrl+K` | Format the current file |
| `Ctrl+R` | Run project diagnostics |
| `Ctrl+O` | Show all keyboard shortcuts |
| `Ctrl+G` | Toggle the Go package/import graph |
| `Ctrl+P` | Focus Explorer |
| `Enter` | Open selected file / add a new line |
| `Tab` | Accept an inline suggestion, or insert four spaces |
| `Ctrl+N` | New file |
| `Ctrl+S` | Save |
| `Ctrl+Z` | Undo the last edit |
| `Ctrl+W` | Close tab |
| `Ctrl+B` | Toggle Explorer |
| `Ctrl+Q` | Quit |

Unsaved tabs require the close or quit shortcut twice, so an accidental keypress
does not lose work.

The top bar has menus plus clickable Search, Run, and Test actions. Run and Test
auto-detect Go, Node, Rust, Python, or .NET projects and keep output inside the editor.
Run can also use nerdctl to build a project's Containerfile or Dockerfile into the platform
containerd namespace, then launch it with an isolated network, read-only
filesystem, dropped capabilities, seccomp, and CPU/memory limits.
Debug automatically builds and runs a `Containerfile` or `Dockerfile` through
the same containerd path, otherwise it compiles/runs the detected project language.
File → Open Folder switches workspaces without leaving the TUI. The transparent
interface uses the terminal's own background. Terminal, graph, help, architecture,
and debug views open as modals over the current file; Esc closes them.

File and context menus support Up/Down/Left/Right and Enter. Edit → Function Search
finds project symbols and jumps to their source line. Explorer width follows folder
depth and filename length; the Explorer starts hidden, and folders collapse by click,
Enter, or Left/Right. The Test
Explorer shows discovered test names with checkboxes and opens their exact source line.
The embedded command terminal opens in a modal over the current file.

The `▤`, `✓`, and `⑂` sidebar activity icons open Files, Tests, and Source Control.
The Source Control view keeps the open file visible, highlights added, modified, and
deleted lines, and includes a commit box above the changed-file list. Its context and
top Source menus provide wrapped multiline commits, diff, stage/unstage, guarded
discard, stage-all, fetch, pull, push, and refresh; output stays in the embedded terminal. Ignored paths use
Git's own exclude rules when available; generated `bin`, `obj`, `node_modules`, build,
coverage, vendor, and cache directories are always hidden. Inline completion indexes
related project types and model members across Go, JavaScript/TypeScript, Python, Rust,
C/C++, Java, Kotlin, Swift, .NET languages, Ruby, and PHP; type `object.` (or PHP/C++
`object->`) and press Tab to accept the suggestion. The dependency/member index is
cached in the workspace SQLite state, loaded in the background when a file opens, and
refreshed after `.` only when the source changed.
Dependency views also list declared interfaces with their members.

Run includes application, unit-test, diagnostics, full crash-trace debugging, and
an evidence-based AI Slop Scan. Syntax and fenced Markdown highlighting cover Go, JavaScript/TypeScript,
Python, Rust, C/C++, Java, shells, SQL/JSON, YAML, Terraform/HCL, and .NET languages.
View → Architecture Canvas combines the folder tree and direct dependency graph.
View → Word Wrap toggles soft wrapping for long code lines.
Edit → Inspect Code opens a compact right-hand SOLID/DRY panel for only the active
file. Findings link back to and highlight their source line; use the top-bar Inspect
button to toggle the panel. Ctrl+D jumps from the symbol under the cursor to its cached
project definition (also available from Edit and the editor context menu).
Choose Plum, Forest, Amber, or Mono from View. Project `.code-editor.yaml` and global
`~/.config/code-editor/config.yaml` files customize themes, 256-color roles, shortcuts,
terminal maximization, menu/tab/activity padding, icons, tree indentation, and Explorer
width; the included `.code-editor.yaml` is a template.

The editor opens immediately while project scanning and SQLite restoration continue in
the background. Each workspace persists open and unsaved tabs, cursor/scroll positions, expanded
folders, test selections, sidebar state, terminal history, and theme in
`$XDG_STATE_HOME/code-editor` (or `~/.local/state/code-editor`). Blocking scans, Git,
terminal and C# indexing run in background workers.

External clients can use the compact direct-import graph through the
`project_dependency_graph` MCP stdio server:

```sh
codex mcp add code-editor -- /path/to/code-editor --mcp .
```
