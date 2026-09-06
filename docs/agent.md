# Agent workspace, persistent context and quality signals

KiwiCode remains a Go application. This is a bounded first implementation of the agent workflow, not a complete autonomous-development platform or a trained reward model. It implements a main-workspace Agent tab, local context, a syntactic Go graph, a provider/tool loop, reviewed buffer proposals and evidence-gated complexity-debt scoring.

## Start and navigate

Build with the Go version declared by `go.mod`. The existing editor state restoration also requires the `sqlite3` executable.

`Ctrl+A` switches between Agent and code. The Agent tab is also mouse-accessible. `Tab` cycles Plan, Activity, Changes, Checks, Graph, Quality and Context. Arrow/Page keys scroll the active view. `Ctrl+X` cancels the active operation from either workspace tab; `Ctrl+C` also cancels while Agent is active. File save/undo/close shortcuts cannot act on a hidden buffer through the Agent tab.

The run service is independent of the selected tab. Do not confuse activity within the running editor with an external/background assistant service: exiting KiwiCode cancels the service and flushes local state.

## Local context and graph

Enter commands in Agent and press Enter:

```text
/remember Keep Go. Benchmark changes before merging.
/task Improve typing latency without changing behaviour.
/include src/buffer.go
/index
```

A plain prompt saves a task locally; it does not call a model. `/remember` persists an explicit context note. `/include` selects an existing source file that a subsequent user-authorised run may read. `/exclude path` removes that selection, clears recent provider history and removes that file's pending proposals. Notes are separate. `/forget` clears task context, notes, included paths, proposals and recent history, but retains the graph, initial baseline and reward history. It is not secure erasure and cannot retract data already sent to a provider.

The application stores `session.json`, `graph.json` and `baseline.json` under `$XDG_STATE_HOME/code-editor/agent/<workspace-hash>/`, falling back to `~/.local/state/code-editor/agent/<workspace-hash>/`. This is inside KiwiCode's local application state, **not in tracked repository files**. Each canonical workspace path has separate state and an exclusive writer lock. Writes use temporary files, sync and rename; state directories and files use owner-only permissions. State is not encrypted. Store corruption or an unsupported schema is reported rather than silently overwritten. Interrupted runs are not replayed.

The graph stores content hashes, declarations, import edges, syntactic calls, complexity facts and debt-note locations. It does not retain ASTs or complete source text. Session proposals do retain their original and proposed source for review; notes, outputs and conversation summaries also persist locally. Avoid placing credentials in prompts, source context or notes; redaction is deliberately limited and is not a general secret detector.

Go source gets syntactic analysis; other allowlisted languages currently get file/hash metadata only. Calls are not type-resolved and build tags are not resolved. Symlinks and common secret/hidden/vendor paths are restricted. This is not a complete `.gitignore` implementation or a sandbox against concurrent malicious filesystem changes.

## Provider and controlled workflow

Configure these environment variables before starting KiwiCode:

```sh
export KIWICODE_AGENT_URL='https://YOUR-PROVIDER/full/chat/completions/endpoint'
export KIWICODE_AGENT_MODEL='YOUR-MODEL'
# Supply KIWICODE_AGENT_KEY through your local secret-management mechanism.
```

The adapter uses a Chat-Completions-shaped HTTP JSON protocol with function tools. Supply the full endpoint URL. HTTPS is required except for loopback HTTP; redirects are disabled. Compatibility with a particular hosted provider/model must be tested, not assumed. There is no automatic provider fallback and no model contact until `/run`.

`/run` authorises transmission of the task, saved notes, recent summaries and explicitly included file context to the configured provider. The loop can set a plan, read included files, inspect their graphs, propose replacement contents, and request checks. It cannot approve its own changes or invoke an arbitrary shell. Responses are currently delivered per model turn, not token-streamed.

After a proposal, inspect Changes, use `/apply ID`, review the dirty file buffer and explicitly save with `Ctrl+S`. Application to a buffer is undoable and rejects stale disk content or unsaved edits. It does not write disk automatically. `/reject ID` records a rejection. Proposals currently replace existing included files up to 32 KiB; creating files, deleting files and multi-file transactions are not implemented. Changes displays original/replacement lines, not a minimal hunk diff.

`/check` is a separate explicit approval to execute `go test -count=1 ./...` followed by `go vet ./...`. These run with the user's account: **not in a sandbox**. Environment filtering and disabled Go proxy/toolchain downloads do not prevent repository code from accessing files or networking. Only use checks in trusted workspaces. Output is capped and cancellation kills the process group. Save dirty buffers first. Hashes must match before/after checks for evidence to be retained; hashes cover indexed source, not every possible runtime input or environment dependency.

## Reward policy v1

Debt is recorded, not rewarded for existing. A low-complexity function produces a quality fact; simply generating more code earns no credit.

For eligible reviewed changes, the cumulative score is five points per reduced complexity-excess unit, where excess is `max(0, complexity - 10)` for non-test, non-generated functions. The first indexed graph is the fixed baseline. New complexity debt reduces the score. Tests and vet must have passed for the current indexed snapshot, and `/reward` is the user's explicit review acknowledgement.

Changed existing tests, removed tests, deleted/renamed baseline functions, parse errors and stale evidence withhold automatic credit. Removing TODO comments earns nothing. A persisted high-water mark prevents paying for the same improvement twice or a simple fix/revert cycle. Per-symbol score deltas and a bounded history of verified snapshots are retained.

This policy is a conservative maintainability heuristic. It does not measure every form of technical debt, prove correctness, resist a malicious local user altering its state, or train/update model weights. Security, coverage, dependency architecture, measured runtime performance and richer quality evaluators remain additional work. Semantic refactors and test improvements may need manual evaluation because v1 withholds their automatic credit.

## Performance constraints and measurements

Graph parsing, model calls, state writes and checks run in the service worker, not in the keystroke handler. Content hashing detects changes; unchanged file nodes are reused without reparsing. A refresh still reads/hashes eligible files. There is no filesystem watcher yet. One operation runs per workspace, with bounded queues, context, output, proposal and history limits. Draft saves are coalesced on a 750 ms worker tick. The UI polls workers during typing as well as idle time. Terminal size is checked at startup and on SIGWINCH rather than spawning `stty size` per keypress.

Limits include 20,000 files, 128 MiB of indexed input, 2 MiB per source file, 8 MiB session state, 64 MiB graph state, 32 included files, 8 proposals, 8 model turns, 16 tool calls and a three-minute operation deadline. These bound normal workloads; they are not a formal peak-memory or hard real-time guarantee. Existing full-screen rendering and some synchronous editor operations remain optimisation targets.

Microbenchmark, Linux amd64 / Intel Xeon Platinum 8272CL / Go 1.23.2, medians of three 200 ms runs on the same 30-branch function fixture:

| Analysis path | ns/op | B/op | allocations/op |
| --- | ---: | ---: | ---: |
| Unchanged file/node reused | 2,308 | 128 | 2 |
| Parse fixture anew | 69,842 | 14,648 | 418 |

This compares cached and reparsed component paths, **not before/after whole-editor latency**. The local harness used an uncommitted alternate module file for Go 1.23.2; the repository's Go requirement was not changed.

```sh
go test ./...
go test -race ./...
go vet ./...
go build -o /tmp/kiwicode ./src
go test ./internal/agent -run '^$' -bench '^BenchmarkAnalyze' -benchmem -benchtime=200ms -count=3
```

Dedicated tests cover graph reuse/cancellation, source-path guards, state locking/round trips, evidence gates and duplicate-credit prevention, restored context without model replay, model-tool proposals without writes, provider transport guards, cancellation, tab hit testing, input isolation, dirty/stale-buffer protection, undo and terminal-control sanitisation. Hosted-provider smoke tests, interactive terminal validation and end-to-end latency measurements are still required before treating this as production-ready.
