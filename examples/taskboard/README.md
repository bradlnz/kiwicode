# Kiwi Taskboard

A small, dependency-free Go API and browser client for exploring KiwiCode.
This is a local development example, not a production service.

```sh
go run .
# Open http://127.0.0.1:8080

go test ./...
```

The server binds to loopback only. Tasks live in memory and reset on restart.
No credentials, cloud services, package downloads, or database are required.

## Explore it in KiwiCode

From the KiwiCode repository: `./code-editor examples/taskboard`.

- `Ctrl+P`: show and focus Files; Right expands a selected directory.
- `Ctrl+F`, then `tasks`: filter file paths; Enter opens the selection.
- `Ctrl+U`, then `ListTasks`: find functions and jump to their definitions.
- `Ctrl+E`: discover tests; Enter opens one, R runs the checked tests.
- `Ctrl+G`: inspect the import/dependency report.

The Forest theme is supplied by this folder's `.code-editor.yaml`.
