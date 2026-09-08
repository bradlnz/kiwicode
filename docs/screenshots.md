# README screenshots

The README gallery shows the real KiwiCode executable editing
[`examples/taskboard`](../examples/taskboard), not generated or mocked editor UI.
The capture driver only sends input and observes output; it does not inject
rendered UI text, patch editor state, or alter source code for the images.

## Capture locally

On Debian/Ubuntu, install the capture-only dependencies:

```sh
sudo apt-get install xvfb xauth xterm x11-utils fonts-dejavu-core python3-pil sqlite3 libvterm-dev pkg-config
```

Use the Go version from the repository's `go.mod`, then run from the repository root:

```sh
go build -o code-editor ./src
python3 tools/capture_readme.py --binary ./code-editor
```

`--output /path/to/captures` changes the output directory. The default is
`assets/screenshots`. `--example` can point to a copy of the bundled example;
the scripted paths and assertions are intentionally specific to Taskboard,
not a general-purpose driver for arbitrary projects.

The driver creates its own Xvfb display, renders xterm at 144 columns by 42 rows
using DejaVu Sans Mono, and captures the actual window with Pillow's X11 backend.
The terminal background is an xterm setting; the application uses its real Forest
palette. Screenshots are saved as lossless PNGs with no compositing or added UI.
Font rendering and timing can vary by platform; reproducibility means the same
verified interaction sequence, not guaranteed identical pixels across machines.

## What is verified

| Image | Interaction and assertions |
| --- | --- |
| `editor-explorer.png` | Open `main.go`, expand the sample's folders, and open the API handler through Files. |
| `file-search.png` | Search `tasks`, confirm matching file paths, and open the handler from the results. |
| `symbol-search.png` | Search `ListTasks`, confirm implementation paths and line numbers, and jump to the handler definition. |
| `test-explorer.png` | Discover four Go tests and open `TestListTasks` at its definition. |
| `test-run.png` | Run all tests with Ctrl+R and observe actual successful `go test ./...` output. |
| `new-project.png` | Open File > New Project and enter a new folder path. |
| `empty-project.png` | Create the project and show the empty Files explorer with first-file guidance. |
| `empty-tests.png` | Open Tests in the new project and show the No tests found message. |

Each capture asserts expected text in the current real terminal frame before
reading pixels from X11. Unexpected configuration errors, panics, command errors,
and failed tests stop the capture. The script checks that the example workspace
is byte-for-byte unchanged after the editor exits.

The test explorer shows per-test Run/Stop buttons. The capture sequence uses
Ctrl+R and waits for the captured test result; the separate interactive terminal
supports a persistent PTY shell. Project creation occurs inside the isolated HOME.
File search filters paths; function search uses discovered definitions. Neither
screenshot represents a full-text repository search.

## Isolation and provenance

The script copies only the bundled example into a temporary directory. It uses a
separate HOME and state directory and does not read a user's saved editor state.
The editor process receives a small environment allowlist; model configuration
and provider credentials are not inherited. GOCACHE may be supplied to reuse
compiled Go packages while HOME and editor state remain isolated. No provider is called. The only
command run through the UI is the included example's `go test ./...`.

`capture.json` records the source commit supplied by `KIWICODE_SOURCE_COMMIT`,
executable SHA-256, window dimensions, image SHA-256 values, and assertions.
For a local unlabelled build, the source field says `local working tree`; set
`KIWICODE_SOURCE_COMMIT` only when it accurately identifies the executable's source.

The driver also writes `session.ansi`, per-view `.ansi` frames, `actions.json`, and
`xterm.log`. These are diagnostic artifacts, ignored by Git. They contain only
the isolated sample session. Commit the PNGs and `capture.json`, not the logs.
Do not replace the sample with confidential source for public documentation.

## GitHub Actions

[`readme-screenshots.yml`](../.github/workflows/readme-screenshots.yml) builds with
the repository's Go version, tests and vets the sample, captures the eight views,
and uploads the screenshots and diagnostic evidence as `readme-screenshots`.
It runs for relevant pull requests and can also be run manually.

A narrowly scoped publishing job is enabled only for pushes to the documentation
capture branch `docs/readme-editor-tour`. It commits only the eight PNGs and their
manifest to that same branch, without force-pushing. The capture job has read-only
repository permissions; only the publishing job has contents-write permission.
Manual runs and pull requests do **not** auto-commit, and nothing in this workflow
writes directly to `main`.

After capture-branch publication, open or update the documentation PR and check
its normal Go CI before merging. If another commit moves the branch during a
capture, the ordinary non-fast-forward push is rejected rather than overwriting
that work.
