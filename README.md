<p align="center">
  <img src="assets/kiwicode-logo.png" alt="KiwiCode logo" width="240">
</p>

<h1 align="center">KiwiCode</h1>

<p align="center">
  <strong>A high-performance, terminal-native code editor, built in Go.</strong><br>
  Edit code, explore projects, and run your tools in a built-in interactive terminal.
</p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white">
  <img alt="Terminal native" src="https://img.shields.io/badge/UI-terminal--native-6d286c">
  <img alt="GitHub repository" src="https://img.shields.io/badge/source-GitHub-181717?logo=github">
</p>

KiwiCode is open source and built as a configurable, terminal-native TUI for coding and terminal workflows, not a separate AI-agent flow.

---

## Why KiwiCode?

KiwiCode brings code editing and a real shell into one keyboard-friendly workspace:

- Default-open interactive terminal for shell commands and coding CLIs
- Full-screen tools such as Architecture Canvas render as tabs instead of overlays
- Bounded streaming queues and history, batched input, coalesced rendering, and resize-event handling
- 🌈 Syntax colours for Go, Ruby/Rails, C#, HTML/XML, JavaScript/TypeScript, Python, Rust, C/C++, Java, SQL, JSON, YAML, HCL, and more
- ⚡ Immediate UI startup with project scanning, state restoration, and indexing off the UI thread
- ⚡ Configurable project slots in the top-right (five by default); right-click to assign, then click or press **Alt+number** to switch
- ⚡ Project switching retains complete workspaces (buffers, undo history, file trees and terminal sessions) in a bounded memory cache; first visits show a loading indicator
- Workspace checkpoints and folder transitions run off the UI thread; slow switches show an animated modal over the current workspace and continue processing terminal resize events
- Returning to a cached slot does not wait for disk checkpoints; each project's writes are ordered and pending snapshots are coalesced
- 🔎 Fast file, symbol, member, and go-to-definition lookup
- C# completion caches receiver types, follows simple awaited method return types, and supplies common collection/LINQ members (`members.Gr` → `GroupBy`), including continued lines and completion before parentheses
- 🧪 Test explorer for jumping to test definitions
- 🌿 Source Control with diffs, staging, guarded discard, and wrapped multiline commits
- 🎨 Plum, Forest, Amber, and Mono themes

## Quick start

Building requires Go, a C compiler, pkg-config and libvterm 0.3+ development headers
(`libvterm pkgconf` on Arch; `libvterm-dev pkg-config` on Debian/Ubuntu).

`.code-editor.yaml` includes all four theme palettes and every colour role, plus
layout, explorer, icons and shortcuts. Select `theme: plum|forest|amber|mono`;
edits under `themes.<name>` also apply when selecting that theme from View.

```sh
git clone git@github.com:bradlnz/kiwicode.git
cd kiwicode
./build.sh
```

Then open any project:

```sh
cd ~/code/my-project
kiwicode
```

Or run directly from source:

```sh
go run ./src [folder]
```

`build.sh` tests the project, builds the editor, and installs a `kiwicode` symlink in `${XDG_BIN_HOME:-~/.local/bin}`.

## Coding tools

Run `codex`, `claude`, or your preferred CLI directly in the embedded terminal.
KiwiCode keeps editing and shell work in one interface, with no built-in Agent-specific UI.

## Everyday controls

| Key | Action |
| --- | --- |
| Mouse | Menus, tabs, files, scrolling, and drag selection |
| Arrow keys | Move the cursor; collapse or expand folders |
| `Ctrl+F` / `Ctrl+U` | Find a file / symbol |
| `Ctrl+D` | Go to definition |
| `Ctrl+E` | Test Explorer |
| `Ctrl+T` | Embedded terminal |
| `Ctrl+K` | Format current file |
| `Ctrl+G` | Dependency graph |
| `Ctrl+P` / `Ctrl+B` | Focus / toggle Explorer |
| `Ctrl+S` / `Ctrl+Z` | Save / undo in a file tab |
| `Ctrl+W` / `Ctrl+Q` | Close tab / quit |
| `Ctrl+O` | All shortcuts |

Closing a dirty tab or quitting requires the command twice, protecting unsaved work.
Restored dirty flags are checked against the file on disk.

## Running commands

Use the embedded terminal to run, build, test, debug, or launch containers with your usual shell commands.
There are no separate Run/Test/Debug launchers or automatically detected project commands.

## Editor features

The hierarchical Files, Tests, and Source Control explorers share a compact sidebar. File tabs show only those that fit the available width, keeping the active file visible. Word wrap, inline suggestions, C# raw strings, Rails/ERB, structural XML detection, and code-aware type/parameter colours are built in.

![KiwiCode editor with sidebar explorer](assets/kiwicode-editor-sidebar.png)

![KiwiCode node graph view](assets/kiwicode-node-view.png)

Architecture Canvas and Dependency Graph show connected, selectable nodes. Click or Tab to select;
Dependency Graph starts folder-first: click a folder (or press Enter) to expand/collapse it,
revealing its files and subfolders. Cross-folder dependencies connect the collapsed folder nodes;
external imports and interfaces appear as their files are revealed. Expansion is cached per project
and does not rescan the filesystem. Architecture Canvas keeps its full overview.
Enter opens a file/folder, arrows or background dragging pan, +/- zoom, [ and ] follow links,
F centers the selection, and R refreshes. Graphs build in the background and are cached per project.
Imports are syntactic, not a full language-server call graph; unresolved dependencies remain named nodes.
Member completion also uses bounded syntax inference, not a language server: complex expression
chains, overload resolution and exact extension-method scope are not resolved.
Views are bounded to 400 files / 600 nodes / 2,000 links, with truncation shown in the footer.
SOLID/DRY inspection stays scoped to the active file.

### Interactive terminal

The terminal opens by default with a persistent PTY-backed `$SHELL -i` in the project directory
(Bash by default), leaving keyboard focus in the editor. Ctrl+T toggles it.
The terminal is docked below the editor, leaving the explorer visible. Set
`terminal.height: 10` in YAML to choose its screen height in rows (3–100, plus a header).
Small windows clamp the panel height to preserve room for the editor. Click either pane
to focus it; Ctrl+T toggles the terminal without stopping its shell.
Bash loads `~/.bashrc`, so your PATH, aliases and installed `codex`/`claude` commands are available.
Output streams live, with terminal colours, cursor movement, alternate screens, mouse reporting,
resizing and 1,000 lines of scrollback. Wheel-scroll shows history when the child isn't using mouse input.
Ctrl+C, Ctrl+D, Escape, Tab and other terminal keys go to the child; Ctrl+T returns to the editor.
Alt+number and the top bar remain project shortcuts. Hidden terminal sessions survive cached project switches;
output queues are bounded (a busy inactive project eventually applies backpressure).
Sessions end on editor exit or project-cache eviction; shell processes are not restored across restarts.
Source Control operations and static inspection reports retain captured output.

## Configuration

KiwiCode’s behavior is highly customizable from YAML and keeps that configuration stable across cached sessions.

Use the project `.code-editor.yaml` or global `~/.config/code-editor/config.yaml` to:

- theme and 256-colour roles
- keyboard shortcuts
- Explorer width, indentation, icons, and layout spacing
- terminal behaviour
- `projects.quick_picks: 5` — show 1–9 project slots, with matching Alt+1…9 shortcuts;
  reducing the count hides extra assignments without deleting them

Workspace state is stored outside the repository in `$XDG_STATE_HOME/code-editor` or `~/.local/state/code-editor`.

## Dependency graph MCP

Expose the compact project graph to an external MCP client:

```sh
codex mcp add kiwicode -- /path/to/kiwicode/code-editor --mcp .
```

The server provides the `project_dependency_graph` tool over stdio.

## Development

```text
kiwicode/
├── assets/          # Brand assets
├── src/             # Editor, workspace tabs, rendering, input and integration tests
├── docs/            # Performance documentation
├── tests/           # Test entrypoints and real-terminal smoke test
├── build.sh         # Test, build, and install
└── run.sh           # Run from source
```

```sh
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -o /tmp/kiwicode ./src
python3 tests/editor_terminal.py /tmp/kiwicode
python3 tests/embedded_terminal.py /tmp/kiwicode
python3 tests/project_switch_terminal.py /tmp/kiwicode --slow-checkpoint
```

CI runs the full tests, race checks, build, terminal smoke test and component benchmarks.
See [buffer performance](docs/performance.md).
Benchmark results are component measurements, not whole-editor speedup claims.

KiwiCode is open source. Clone and contribute via the GitHub repository at `github.com/bradlnz/kiwicode`.
