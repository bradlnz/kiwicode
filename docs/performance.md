# Buffer performance: affected-line undo and allocation-free measurement

## Scope

This change optimises the existing Go implementation, without introducing Rust or changing the editor's public behaviour.

- Ordinary insertion, deletion, line splitting/joining and `insertText` save only the affected lines for undo. Same-line undo does not copy untouched line headers.
- Existing `recordUndo()` callers retain full-document snapshots for compound operations such as selection replacement and clipboard paste. `suppressUndo` and the 50-entry history limit are preserved.
- Discarded and popped undo entries release references rather than retaining document data in the history backing array.
- Cursor and word-wrap width calculation counts cells directly, without constructing expanded rune slices. Four-column tab stops and the existing one-cell-per-rune convention are unchanged. This is not a CJK/grapheme-width correction.

Per-keystroke undo capture now scales with the affected line(s), not the entire document. Long single lines still require line-sized copies, line splits/joins still move line headers, and compound full-snapshot operations remain document-sized.

## Measurements

Baseline commit: `0b564c7c2347dadb29ead7f4702e4714fea90425`.
Original `src/buffer.go` Git blob: `580ba725fa45c020621aa9257e97036681bba4fc` (verified byte-for-byte before testing).

Environment: Linux amd64, AMD EPYC 9V74, Go 1.23.2, `GOMAXPROCS=2`. Times below are medians of three 200 ms benchmark runs, not confidence intervals or service-level latency measurements.

| Benchmark | Before (ns/op) | After (ns/op) | Before (B/op) | After (B/op) | Allocations before / after |
| --- | ---: | ---: | ---: | ---: | ---: |
| Insert + undo, 100 lines | 6,735 | 197.4 | 27,168 | 744 | 102 / 3 |
| Insert + undo, 10,000 lines | 742,925 | 256.7 | 2,646,240 | 744 | 10,002 / 3 |
| Insert + undo, 30,000 lines | 2,453,948 | 194.9 | 7,921,376 | 744 | 30,002 / 3 |
| Cursor cell measurement | 13,751 | 2,553 | 49,776 | 0 | 13 / 0 |
| Wrap count | 13,972 | 3,075 | 49,776 | 0 | 13 / 0 |

The insertion benchmark uses 60-character lines, inserts one character into the middle line and immediately undoes it. The 30,000-line input is below the editor's 2 MiB file-open limit. Measurement benchmarks use a tab-heavy line. These are isolated component benchmarks, not key-to-screen, rendering, startup, completion or whole-editor speedups. The near-constant after times vary with normal measurement noise and allocator state; more document lines do not inherently make edits faster.

## Validation performed

The exact baseline and updated `buffer.go` were compiled independently with the same new test/benchmark file. An uncommitted test-harness stub supplied an empty `languageSyntaxes` map so unrelated syntax/completion code was not required. No suggestion functions were tested or benchmarked.

Baseline passed the behaviour tests. Updated buffer passed all added tests, `go test -race`, and `go vet` in that isolated harness. Coverage includes single-line edits, line splitting/joining, indentation, Unicode, CRLF, empty buffers, trailing newlines, no-op edits, 20 seeded mixed histories with 100 edit attempts each, compound/full replacement compatibility, the history limit, unchanged-line identity, popped-reference release, and allocation-free measurement equivalence.

The repository declares Go 1.27. The full repository test suite, application build with that toolchain, and interactive terminal smoke tests have **not** been run. Keep the PR in draft until these checks pass.

## Reproduce in the repository

With the repository's required Go toolchain:

```sh
go test ./src
go test -race ./src
go vet ./src
GOMAXPROCS=2 go test ./src -run '^$' -bench '^BenchmarkBuffer' -benchmem -benchtime=200ms -count=3
go build -o /tmp/kiwicode-performance ./src
```

For a before/after comparison, copy `src/buffer_performance_test.go` into a worktree at the baseline commit and run only benchmarks there (`-run '^$'`); allocation/storage regression tests intentionally fail against the old implementation. Use identical toolchains and hardware for both worktrees.

## Remaining performance work

`run()` currently calls `e.resize()` after every key and `resize()` launches `stty size`. That path is not changed here. An event-driven resize path is a separate priority, followed by profiling completion, visible-tree generation and syntax highlighting. Measure end-to-end key-to-screen latency before deciding whether a Rust component or a larger buffer data-structure change is justified.
