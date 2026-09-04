<p align="center">
  <img src="assets/kiwicode-logo.png" alt="KiwiCode logo" width="240">
</p>

<h1 align="center">KiwiCode</h1>

<p align="center">
  <strong>A quick, colourful code editor that lives in your terminal.</strong><br>
  Open a project and start typing—workspace discovery, state restoration, and indexing continue in the background.
</p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white">
  <img alt="Terminal native" src="https://img.shields.io/badge/UI-terminal--native-6d286c">
  <img alt="containerd" src="https://img.shields.io/badge/containers-containerd-65d46e">
  <img alt="GitHub repository" src="https://img.shields.io/badge/source-GitHub-181717?logo=github">
</p>

---

## Why KiwiCode?

KiwiCode keeps the useful parts of a desktop IDE while staying small and keyboard-friendly:

- 🌈 Syntax colours for Go, Ruby/Rails, C#, HTML/XML, JavaScript/TypeScript, Python, Rust, C/C++, Java, SQL, JSON, YAML, HCL, and more
- ⚡ Immediate UI startup with project scanning, state restoration, and indexing off the UI thread
- 🔎 Fast file, symbol, member, and go-to-definition lookup
- 🧪 Discoverable tests with selection and in-editor output
- 🌿 Source Control with diffs, staging, guarded discard, and wrapped multiline commits
- 🐛 Run and Debug detection for Go, Node, Rust, Python, .NET, and standalone files
- 📦 Sandboxed containerd builds and runs from `Containerfile` or `Dockerfile`—no Docker daemon
- 🎨 Plum, Forest, Amber, and Mono themes

## Quick start

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
| `Ctrl+R` | Project diagnostics |
| `Ctrl+G` | Dependency graph |
| `Ctrl+P` / `Ctrl+B` | Focus / toggle Explorer |
| `Ctrl+S` / `Ctrl+Z` | Save / undo |
| `Ctrl+W` / `Ctrl+Q` | Close tab / quit |
| `Ctrl+O` | All shortcuts |

Closing a dirty tab or quitting requires the command twice, protecting unsaved work. Restored dirty flags are checked against the file on disk before Run or Debug is blocked.

## Run, debug, and containers

Run and Test detect the nearest project marker and keep output inside KiwiCode. Debug adds language-specific crash tracing. When a `Containerfile` or `Dockerfile` exists, Debug builds and launches it with `nerdctl` in the `kiwicode` containerd namespace.

Container runs use a read-only filesystem, isolated networking, dropped capabilities, `no-new-privileges`, and CPU, memory, and process limits. `nerdctl` and a reachable containerd socket are required.

## Editor features

The hierarchical Files, Tests, and Source Control explorers share a compact sidebar. Tabs show five or six files at once. Word wrap, inline suggestions, C# raw strings, Rails/ERB, structural XML detection, and code-aware type/parameter colours are built in.

The architecture canvas and direct-import graph help explain a repository without blocking editing. SOLID/DRY inspection stays scoped to the active file so feedback remains useful.

## Configuration

Use the project `.code-editor.yaml` or global `~/.config/code-editor/config.yaml` to customise:

- theme and 256-colour roles
- keyboard shortcuts
- Explorer width, indentation, icons, and layout spacing
- terminal behaviour

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
├── assets/       # Brand assets
├── src/          # Editor source and package-level Go tests
├── tests/        # Repository test entrypoints
├── build.sh      # Test, build, and install
└── run.sh        # Run from source
```

```sh
./tests/unit.sh
./build.sh
```

KiwiCode is currently developed in a private repository. Licensing and any public release can be decided later.
