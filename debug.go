package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func (e *editor) debugProject() {
	if e.dirty() {
		e.status = "Save changes before debugging"
		return
	}
	if e.shell.running {
		e.status = "Stop the running command before debugging"
		return
	}
	command := debugCommand(e.current().path)
	if command == "" {
		e.status = "No debug command detected for this project"
		return
	}
	if strings.HasPrefix(command, "nerdctl ") {
		if _, err := exec.LookPath("nerdctl"); err != nil {
			e.status = "nerdctl is required to debug container projects with containerd"
			return
		}
	}
	e.clearModalViews()
	e.shell.open, e.shell.focused = true, true
	e.debugOutputStart = len(e.shell.output)
	e.debugging = true
	e.shell.start(command)
	e.status = "Debugging — crashes open in Debug Trace"
}

func debugCommand(current string) string {
	if build := containerCommand(current, true); build != "" {
		return build + " && " + containerCommand(current, false)
	}
	if _, err := os.Stat("go.mod"); err == nil {
		return "GOTRACEBACK=all go run ."
	}
	if _, err := os.Stat("Cargo.toml"); err == nil {
		return "RUST_BACKTRACE=full cargo run"
	}
	if _, err := os.Stat("package.json"); err == nil {
		return "NODE_OPTIONS=--trace-uncaught npm start"
	}
	if matches, _ := filepath.Glob("*.csproj"); len(matches) > 0 {
		return "dotnet run"
	}
	switch strings.ToLower(filepath.Ext(current)) {
	case ".py":
		return "PYTHONFAULTHANDLER=1 python3 " + shellArg(current)
	case ".js", ".mjs", ".cjs":
		return "node --trace-uncaught " + shellArg(current)
	case ".go":
		return "GOTRACEBACK=all go run " + shellArg(current)
	}
	return ""
}

func (e *editor) finishDebug() {
	e.debugging = false
	e.shell.open, e.shell.focused = false, false
	e.opsMode, e.opsTop = "debug trace", 0
	e.opsLines = append([]string(nil), e.shell.output[e.debugOutputStart:]...)
	if len(e.opsLines) == 0 {
		e.opsLines = []string{"Program exited without output or a stack trace"}
	}
	e.status = "Debug run finished"
}
