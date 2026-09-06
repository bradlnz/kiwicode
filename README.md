<p align="center">
  <img src="assets/kiwicode-logo.png" alt="KiwiCode logo" width="240">
</p>

<h1 align="center">KiwiCode</h1>

<p align="center">
  <strong>A performance-first, terminal-native agentic editor, built in Go.</strong><br>
  Edit code and run an agent workflow in first-class workspace tabs—with explicit approvals, reviewable changes, and bounded background work.
</p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white">
  <img alt="Terminal native" src="https://img.shields.io/badge/UI-terminal--native-6d286c">
  <img alt="containerd" src="https://img.shields.io/badge/containers-containerd-65d46e">
  <img alt="GitHub repository" src="https://img.shields.io/badge/source-GitHub-181717?logo=github">
</p>

---

## Why KiwiCode?

KiwiCode brings code editing and agent execution into one keyboard-friendly workspace:

- Agent tab with Plan, Activity, Changes and Checks; continue editing while a run is active
- Explicit file-read and command approvals, cancellable execution, and undoable application of reviewed changes
- Bounded streaming queues and history, batched input, coalesced rendering, and resize-event handling
- 🌈 Syntax colours for Go, Ruby/Rails, C#, HTML/XML, JavaScript/TypeScript, Python, Rust, C/C++, Java, SQL, JSON, YAML, HCL, and more
- ⚡ Immediate UI startup with project scanning, state restoration, and indexing off the UI thread
- 🔎 Fast file, symbol, member, and go-to-definition lookup
- 🧪 Discoverable tests with selection and in-editor output
- 🌿 Source Control with diffs, staging, guarded discard, and wrapped multiline commits
- 🐛 Run and Debug detection for Go, Node, Rust, Python, .NET, and standalone files
- 📦 Containerd builds and runs from `Containerfile` or `Dockerfile`—no Docker daemon
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

## Agent workflow

Set a real streamed Chat Completions/function-tools endpoint and model before launching:

```sh
export KIWICODE_AGENT_ENDPOINT='https://YOUR-PROVIDER/v1/chat/completions'
export KIWICODE_AGENT_MODEL='YOUR-MODEL'
# Set KIWICODE_AGENT_API_KEY privately when the endpoint requires authentication.
kiwicode
```

Press **Ctrl+A** or select **Agent**. Enter a task, review requested context, and follow
Plan → Activity → Changes → Checks. `/approve` and `/deny` decide the current request.
`/apply N` applies a reviewed change to a buffer without writing disk; `/open N` returns
to the ordinary editor, where Save and Undo remain available. **Ctrl+X** cancels a run
from either workspace. Switching tabs retains the draft and does not cancel execution.

There is no default remote provider and no simulated execution. Host commands are
disabled unless `KIWICODE_AGENT_ALLOW_COMMANDS=1`, and still require individual approval.
Checks use temporary project snapshots, **not an OS security sandbox**; approve commands
only for trusted code. Optional `KIWICODE_AGENT_HISTORY=1` retains a latest-session
checkpoint outside the repository. History can contain source code and tool output.

See [Agent configuration, limits, security boundaries and controls](docs/agent-workspace.md).
The initial implementation supports one active run per workspace, not concurrent agents.

## Everyday controls

| Key | Action |
| --- | --- |
| Mouse | Menus, tabs, files, scrolling, and drag selection |
| `Ctrl+A` | Switch between Agent and the selected file |
| `Ctrl+X` | Cancel the active agent run |
| Arrow keys | Move the cursor; collapse or expand folders |
| `Ctrl+F` / `Ctrl+U` | Find a file / symbol |
| `Ctrl+D` | Go to definition |
| `Ctrl+E` | Test Explorer |
| `Ctrl+T` | Embedded terminal |
| `Ctrl+K` | Format current file |
| `Ctrl+R` | Project diagnostics |
| `Ctrl+G` | Dependency graph |
| `Ctrl+P` / `Ctrl+B` | Focus / toggle Explorer |
| `Ctrl+S` / `Ctrl+Z` | Save / undo in a file tab |
| `Ctrl+W` / `Ctrl+Q` | Close tab / quit |
| `Ctrl+O` | All shortcuts |

Closing a dirty tab or quitting requires the command twice, protecting unsaved work.
Quitting during an agent run also requires confirmation. Closing Agent returns to
files without discarding the session. File commands cannot edit a hidden file from Agent.
Restored dirty flags are checked against the file on disk before Run or Debug is blocked.

## Run, debug, and containers

Run and Test detect the nearest project marker and keep output inside KiwiCode. Debug adds language-specific crash tracing. When a `Containerfile` or `Dockerfile` exists, Debug builds and launches it with `nerdctl` in the `kiwicode` containerd namespace.

Container runs use a read-only filesystem, isolated networking, dropped capabilities, `no-new-privileges`, and CPU, memory, and process limits. `nerdctl` and a reachable containerd socket are required. These container controls do not apply to the Agent's separately approved host-command tool.

## Editor features

The hierarchical Files, Tests, and Source Control explorers share a compact sidebar. File tabs sit alongside the persistent Agent tab. Word wrap, inline suggestions, C# raw strings, Rails/ERB, structural XML detection, and code-aware type/parameter colours are built in.

The architecture canvas and direct-import graph help explain a repository without blocking editing. SOLID/DRY inspection stays scoped to the active file so feedback remains useful.

## Configuration

Use the project `.code-editor.yaml` or global `~/.config/code-editor/config.yaml` to customise:

- theme and 256-colour roles
- keyboard shortcuts
- Explorer width, indentation, icons, and layout spacing
- terminal behaviour

Workspace state is stored outside the repository in `$XDG_STATE_HOME/code-editor` or `~/.local/state/code-editor`. Agent provider credentials are environment configuration, not project YAML.

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
├── internal/agent/  # Provider, bounded runtime, tools, snapshots and session store
├── src/             # Editor, workspace tabs, rendering, input and integration tests
├── docs/            # Agent and performance documentation
├── tests/           # Test entrypoints and real-terminal smoke test
├── build.sh         # Test, build, and install
└── run.sh           # Run from source
```

```sh
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -o /tmp/kiwicode ./src
python3 tests/agent_terminal.py /tmp/kiwicode
```

CI runs the full tests, race checks, build, terminal smoke test and component benchmarks.
See [buffer performance](docs/performance.md) and the [Agent performance/resource limits](docs/agent-workspace.md#performance-and-resource-limits).
Benchmark results are component measurements, not whole-editor speedup claims.

KiwiCode is currently developed in a private repository. Licensing and any public release can be decided later.
