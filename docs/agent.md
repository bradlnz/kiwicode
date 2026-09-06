# Persistent context, code graph and quality scores

The integrated Agent uses the streamed runtime, explicit approvals, temporary
snapshot checks, batched input and coalesced rendering documented in
[Agent workspace](agent-workspace.md). There is **one model runtime**, not two
competing implementations. The context worker is a separate local service.

## Views and commands

Agent has Plan, Activity, Changes, Checks, Graph, Quality and Context views. Tab
cycles through them; a narrow terminal keeps the selected view label visible.
File-tab Save/Undo, read approvals, `/approve`, `/deny`, `/apply N`, `/open N`,
`/reject N` and `/new` retain their existing behaviour.

| Command | Behaviour |
| --- | --- |
| `/task TEXT` | Persist a task locally without contacting a provider. |
| `/run [TEXT]` | Submit TEXT, or the saved task, to the configured streamed provider. Plain task text also starts a run. |
| `/remember NOTE` | Persist an explicit context note. |
| `/include PATH` | Include a permitted file's graph metadata in future task context. This does not approve reading source. |
| `/exclude PATH` | Exclude that metadata and clear provider conversation history so previously read content is not reused there. Explicit notes remain separate. |
| `/index` | Refresh local content hashes and reparse only changed files. |
| `/check` | With `KIWICODE_AGENT_ALLOW_COMMANDS=1`, repeat the exact command to authorise `go test -count=1 ./...` and `go vet ./...` on temporary snapshots. Dirty buffers are refused. |
| `/reward` | Record user review and evaluate checks against the current graph snapshot. Dirty buffers are refused. |
| `/forget` | Clear task notes, included paths and transcript checkpoints; retain graph, fixed reward baseline and high-water credit. Applied buffer changes are not discarded. |

Ctrl+X cancels either model work or context work. Switching tabs does not cancel
work. Model execution, context checks and proposal application cannot race each
other through the UI. File editing remains available during worker operations.

## Persistence and disclosure

Context notes, included paths, graph, baseline, check evidence and recent scores
are stored under `$XDG_STATE_HOME/code-editor/agent/<workspace-hash>/`, falling
back to `~/.local/state`. Files use owner-only permissions, atomic replacement
and a nonblocking single-writer lock. Corrupt/unsupported context is reported
rather than overwritten. Interrupted checks are not replayed. The context store
excludes its own application-state directory from scans when state is nested
inside a workspace.

The existing transcript/draft checkpoint remains opt-in with
`KIWICODE_AGENT_HISTORY=1`, under `code-editor/agents/`. Context and transcript
stores have different purposes. Clearing history is not secure erasure or deletion
of backups; state is not encrypted. Known provider-key redaction and conservative
source guards are not comprehensive secret detection.

Only explicit notes and explicitly included graph metadata accompany a submitted
task. File bytes still require the streamed runtime's individual read approval.
Provider configuration remains `KIWICODE_AGENT_ENDPOINT`,
`KIWICODE_AGENT_MODEL`, and `KIWICODE_AGENT_API_KEY`. No provider is configured
or contacted automatically by indexing, remembering a note or restoring state.

## Performance and limits

Context/model/file I/O and checks are worker operations, never per-keypress scans.
The context worker has a one-entry command queue and latest-view queue. View
projections rebuild on context changes or resize, not on every input rune.
Main's 16 ms frame scheduler and hidden-token redraw suppression are preserved.

Notes are bounded to 8 KiB total, 64 entries, 2 KiB each; included paths to 32;
provider metadata to 16 KiB; graph input to 20,000 files/128 MiB with a 2 MiB
per-file limit; persisted graph to 64 MiB. Graph presentation is capped. Checks
inherit the stricter existing snapshot runner's 2,048-file/16 MiB limits and
reject incomplete snapshots. Source-size and parse limits are reported, not
represented as complete successful analysis.

## Graph and reward semantics

Go files have declarations, imports, syntactic calls and complexity/debt facts.
Other allowlisted source types have metadata only. Calls are not type-resolved;
build tags, full `.gitignore`, filesystem watching and whole-program semantics
are not implemented. Each explicit scan still reads and hashes source; unchanged
nodes avoid parsing. TODO/FIXME notes are facts, not rewarded work.

Policy v1 awards **5 points per reduction of complexity excess above 10** against
the initial fixed baseline. Evidence requires passing tests and vet for the exact
indexed source snapshot, then explicit user review. Changed/deleted baseline
tests, deleted/renamed baseline functions and parse errors conservatively withhold
credit. High-water accounting avoids repeat payouts and simple fix/revert farming.
Low-complexity code is recorded as a quality fact, but creating code volume alone
never earns credit. This is an auditable heuristic, not a learned reward model,
proof of correctness or coverage of every kind of technical debt.

Both check pathways use the existing temporary-snapshot runner with its minimal
child environment. **A temporary snapshot is not an OS security sandbox**:
approved code can access host files/network. Use commands only for trusted code.
The reward ledger is local application state, not tamper-proof financial credit.

## Validation

CI runs unit/integration tests, race detection, vet, a full build, the real-PTY
smoke test and component benchmarks. Context integration tests cover persistence,
metadata-only provider context, snapshot-only writes, stale evidence, duplicate
credit, nested-state exclusion, corrupt checkpoints, seven-view navigation,
input isolation, and command approval/dirty-buffer gates.
