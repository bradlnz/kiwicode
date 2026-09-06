# Agent workspace: Go, bounded work, explicit review

KiwiCode's **Agent** tab is a first-class workspace alongside file tabs. It is
not a file buffer or a modal terminal. One agent run can execute per workspace;
switching to source code does not cancel the run or lose the task draft.

## Configure a real provider

Set these in your own shell before starting KiwiCode:

```sh
export KIWICODE_AGENT_ENDPOINT='https://YOUR-PROVIDER/v1/chat/completions'
export KIWICODE_AGENT_MODEL='YOUR-MODEL'
# Set KIWICODE_AGENT_API_KEY privately in your shell/secret manager when required.
kiwicode
```

The endpoint must implement **streamed Chat Completions with function tools**.
There is no default vendor, model, remote destination, or simulated response.
HTTPS is required except for a loopback HTTP endpoint. Redirects, URL userinfo,
query strings and fragments are rejected. API keys are read from the environment,
not repository configuration, session JSON, or child-command environments.
The configured destination is displayed in Agent before submitting a task.

The initial request contains the task and bounded conversation history, not
open-buffer contents. `list_files` exposes permitted filenames from the existing
workspace index. Every first `read_file` requires approval before its contents
are sent. Reads are cached for that run; later reads include staged edits.
Dirty buffers captured at run start cannot be read. New edits made while a run
executes are protected by the review-time conflict checks.

## Controls and workflow

- **Ctrl+A:** switch between Agent and the selected file. Mouse tabs also work.
- **Enter:** submit a task or slash command. **Tab:** Plan / Activity / Changes / Checks.
- **Up/Down, PgUp/PgDn:** scroll. Left/Right, Home/End edit the draft cursor.
- **Ctrl+X:** cancel the agent from any workspace. Ctrl+C also cancels inside Agent.
- **Ctrl+W in Agent:** return to files without closing a file or discarding the session.
- **Ctrl+Q:** quit; an active run or unsaved files require a second confirmation.

The keyboard bindings for Agent and Cancel Agent are configurable through the
existing `agent` and `agent-cancel` shortcut names. Bracketed terminal paste is
inserted as one draft/edit operation; pasted newlines are not Enter commands.
The Agent draft is limited to 16 KiB.

The execution loop is task -> plan -> approved reads -> proposed edits -> approved
checks -> review. Plan updates, user-facing activity, proposed diffs, and actual
check results each have their own view. Agent activity is not hidden model
reasoning. An unread, running, or approval badge remains visible on the tab.

Slash commands:

| Command | Effect |
| --- | --- |
| `/approve`, `/deny` | Decide the currently displayed read/command request. No automatic approval. |
| `/cancel` | Cancel the run, including an approval wait or a full event queue. |
| `/apply N` | Apply reviewed change N to an editor buffer, **not disk**. |
| `/open N` | Open that file in the ordinary editor. Save and Undo remain file actions. |
| `/reject N` | Discard an unapplied proposal without modifying a file. |
| `/new` | Begin a new session. Repeat to confirm discarding an existing review. |
| `/forget` | Clear the current history and its optional persisted checkpoint. Applied edits remain. |

Ordinary file Save, Undo, Format, Run and other file commands cannot fall through
from Agent into a hidden file. A reviewed edit uses the existing undo system.
Apply requires the file's disk contents and any open clean buffer to match the
original snapshot; dirty/stale files, symlinks and oversized/truncated reviews
are refused. Existing newline mode is preserved; changing it requires manual
editing. New files can be proposed; their parent directory must exist before the
normal editor Save can succeed. File deletion is not exposed as an agent tool.

## Checks and execution policy

Commands are disabled by default. For a **trusted project**, explicitly enable:

```sh
export KIWICODE_AGENT_ALLOW_COMMANDS=1
```

Every command still requires its own approval, showing its exact argument array.
There is no implicit shell expansion, although approving `sh -c ...` explicitly
runs a shell. Commands operate on a temporary project snapshot with proposed
changes overlaid, not the live working directory. Captured dirty files and stale
read snapshots prevent checks. A truncated file manifest is never presented as
a complete, passing workspace check.

**A temporary directory is not a security sandbox.** An approved program executes
on the host and may access host files and the network. Enable commands only for
trusted code, review the entire command, and run KiwiCode inside an OS/container
sandbox when isolation is needed. Children receive a minimal environment and a
temporary home; model credentials and the user's home configuration are not
inherited. Go's automatic module/toolchain downloads are disabled for checks.
Dependency installation and missing tools are reported as failures, not simulated
successes. Cancellation terminates the command's Unix process group; deliberately
detached processes are not a security boundary.

## Performance and resource limits

The UI has a single state owner. A worker sends immutable events through a
**64-entry** queue. Saturation applies backpressure rather than accumulating
unbounded tokens; cancellation also unblocks a full queue. Small text deltas are
coalesced before delivery. Hidden token events do not repeatedly redraw the file
viewport once the notification badge has been set.

Input is read in batches through a separate, bounded input channel. Terminal size
is queried at startup and on **SIGWINCH**, never on each keypress. One reusable
frame timer coalesces bursts with a 16 ms minimum frame spacing. An idle-to-active
interaction need not wait a full frame interval. The idle frame channel is nil.
The Agent renderer bypasses file highlighting, completion and Explorer scanning;
it renders only visible rows. Activity uses a bounded presentation ring and
incremental append; a resize, rather than every streamed token, can rebuild it.
Diff generation is linear and cached per changed proposal, not quadratic.

| Resource | Bound |
| --- | --- |
| Task / retained message | 16 KiB |
| Request context | 512 KiB of JSON, not a token count |
| Response body / assistant text | 1 MiB / 64 KiB |
| Run turns / tool calls | 16 / 64 |
| Model-readable or proposed file | 256 KiB |
| Reads / total read bytes | 64 / 2 MiB |
| Changed files | 16 |
| Workspace manifest | 2,048 permitted filenames |
| Check snapshot | 16 MiB total, 8 MiB per asset/file |
| Command output | 32 KiB, with an explicit truncation marker |
| Stored activity | 512 entries of at most 1,024 bytes |
| Displayed activity | 512 wrapped rows |
| Conversation / checks | 16 messages / 16 results |
| Default run / HTTP / command deadlines | 5 minutes / 2 minutes / 90 seconds |

Deadline cancellation is cooperative for file-system calls; this is not a
hard-real-time or memory-isolated runtime. Very large repositories require a
smaller workspace instead of silently dropping files from checks. Secret-like
paths and repository internals are excluded; path filtering is not a guarantee
that arbitrary source text contains no secrets. Read/command approvals are still
required. Other model transports and concurrent multi-agent sessions are not
implemented by this change.

## Optional restoration

History is **off by default**. `KIWICODE_AGENT_HISTORY=1` enables a latest-session
checkpoint per workspace under `$XDG_STATE_HOME/code-editor/agents` (or
`~/.local/state/code-editor/agents`). It contains code, diffs, prompts and tool
output, so enable it only when retaining that content is acceptable. Directories
are mode 0700 and checkpoint files 0600. Saves use one background writer with a
latest-only pending checkpoint; JSON and disk IO do not run in key handlers.
`/forget` deletes the checkpoint. There is no automatic archival of old sessions.

Interrupted runs restore as **interrupted**. No commands, approvals or other side
effects are replayed. Drafts typed during initial workspace loading are retained.
Normal editor buffer persistence is separate from optional Agent history.

## Validation and reproduction

Use the repository's declared Go toolchain:

```sh
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -o /tmp/kiwicode ./src
python3 tests/agent_terminal.py /tmp/kiwicode
GOMAXPROCS=2 go test ./... -run '^$' \
  -bench 'Benchmark(Agent|Input|Frame|Log|VisibleLog|Buffer)' \
  -benchmem -benchtime=200ms -count=3
```

The workflow in `.github/workflows/go.yml` runs these checks. Tests use scripted
providers and local HTTP/SSE servers, not live credentials or paid model calls.
The PTY test exercises the built terminal application: tab switching, draft
retention, input isolation, paste, idle resize, guarded quit and terminal-mode
restoration. Component benchmarks are not whole-editor speedup claims; measure
real project key-to-screen latency before extending performance conclusions.
