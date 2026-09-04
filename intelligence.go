package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func (e *editor) runProject(tests bool, only ...int) {
	if e.dirty() {
		e.status = "Save changes before running"
		return
	}
	current := e.current().path
	if tests && len(only) > 0 && only[0] >= 0 && only[0] < len(e.tests) {
		current = e.tests[only[0]].path
	}
	command := projectCommand(current, tests)
	if command == "" {
		e.status = "No run command detected for this project"
		return
	}
	if tests {
		var err error
		command, err = e.checkedTestCommand(command, only...)
		if err != nil {
			e.status = err.Error()
			return
		}
	}
	if e.shell.running {
		e.status = "A command is already running — Ctrl+C stops it"
		return
	}
	e.clearModalViews()
	e.shell.open, e.shell.focused = true, true
	e.shell.start(command)
	e.status = "Running: " + command
}

func (e *editor) runContainer(build bool) {
	if e.dirty() {
		e.status = "Save changes before building or running a container"
		return
	}
	command := containerCommand(e.current().path, build)
	if command == "" {
		e.status = "No Containerfile or Dockerfile found for this project"
		return
	}
	tool := "nerdctl"
	if _, err := exec.LookPath(tool); err != nil {
		e.status = tool + " is required for containerd " + map[bool]string{true: "builds", false: "runs"}[build]
		return
	}
	if e.shell.running {
		e.status = "A command is already running — Ctrl+C stops it"
		return
	}
	e.clearModalViews()
	e.shell.open, e.shell.focused = true, true
	e.shell.start(command)
	e.status = "Running: " + command
}

func containerCommand(current string, build bool) string {
	dir := filepath.Dir(current)
	if absolute, err := filepath.Abs(dir); err == nil {
		dir = absolute
	}
	for {
		containerfile := ""
		for _, name := range []string{"Containerfile", "Dockerfile"} {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				containerfile = filepath.Join(dir, name)
				break
			}
		}
		if containerfile != "" {
			name := strings.Map(func(value rune) rune {
				if value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || strings.ContainsRune("._-", value) {
					return value
				}
				if value >= 'A' && value <= 'Z' {
					return value + ('a' - 'A')
				}
				return '-'
			}, filepath.Base(dir))
			name = strings.Trim(name, ".-_")
			if name == "" {
				name = "app"
			}
			image := "localhost/kiwicode/" + name + ":dev"
			if build {
				return "nerdctl --namespace kiwicode build -f " + shellArg(containerfile) + " -t " + shellArg(image) + " " + shellArg(dir)
			}
			return "nerdctl --namespace kiwicode run --rm --read-only --network none --cap-drop ALL --security-opt no-new-privileges --pids-limit 256 --memory 512m --cpus 1 --tmpfs /tmp:rw,nosuid,nodev,noexec,size=64m " + shellArg(image)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func (e *editor) checkedTestCommand(command string, only ...int) (string, error) {
	if len(e.tests) == 0 {
		return command, nil
	}
	var names []string
	if len(only) > 0 && only[0] >= 0 && only[0] < len(e.tests) {
		names = append(names, e.tests[only[0]].name)
	} else {
		for _, test := range e.tests {
			if test.checked {
				names = append(names, test.name)
			}
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("Select at least one test")
	}
	if len(names) == len(e.tests) {
		return command, nil
	}
	escaped := make([]string, len(names))
	for index, name := range names {
		escaped[index] = regexp.QuoteMeta(name)
	}
	filter := "^(" + strings.Join(escaped, "|") + ")$"
	prefix := ""
	if directory, base, found := strings.Cut(command, " && "); found && strings.HasPrefix(directory, "cd ") {
		prefix, command = directory+" && ", base
	}
	switch {
	case strings.HasPrefix(command, "go test"):
		return prefix + command + " -run " + shellArg(filter), nil
	case strings.HasPrefix(command, "dotnet test"):
		parts := make([]string, len(names))
		for index, name := range names {
			parts[index] = "FullyQualifiedName~" + name
		}
		return prefix + command + " --filter " + shellArg(strings.Join(parts, "|")), nil
	case strings.HasPrefix(command, "python3 -m unittest"):
		for _, name := range names {
			command += " -k " + shellArg(name)
		}
		return prefix + command, nil
	case strings.HasPrefix(command, "npm test"):
		return prefix + command + " -- --testNamePattern " + shellArg(filter), nil
	case strings.HasPrefix(command, "cargo test"):
		commands := make([]string, len(names))
		for index, name := range names {
			commands[index] = command + " " + shellArg(name)
		}
		return prefix + strings.Join(commands, " && "), nil
	}
	return prefix + command, nil
}

func projectCommand(current string, tests bool) string {
	path := filepath.Clean(current)
	if filepath.IsAbs(path) {
		if relative, err := filepath.Rel(".", path); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			path = relative
		} else {
			path = filepath.Base(path)
		}
	}
	for dir := filepath.Dir(path); dir != ".." && !strings.HasPrefix(dir, ".."+string(filepath.Separator)); dir = filepath.Dir(dir) {
		prefix := ""
		if dir != "." {
			prefix = "cd " + shellArg(dir) + " && "
		}
		if solutions, _ := filepath.Glob(filepath.Join(dir, "*.sln")); len(solutions) > 0 {
			if tests {
				return prefix + "dotnet test"
			}
			return prefix + "dotnet run"
		}
		if projects, _ := filepath.Glob(filepath.Join(dir, "*.csproj")); len(projects) > 0 {
			if tests {
				return prefix + "dotnet test"
			}
			return prefix + "dotnet run"
		}
		local := path
		if dir != "." {
			local, _ = filepath.Rel(dir, path)
		}
		projects := []struct{ marker, run, test string }{
			{"go.mod", "go run .", "go test ./..."},
			{"package.json", "npm start", "npm test"},
			{"Cargo.toml", "cargo run", "cargo test"},
			{"pyproject.toml", "python3 " + shellArg(local), "python3 -m unittest discover"},
		}
		for _, project := range projects {
			if _, err := os.Stat(filepath.Join(dir, project.marker)); err == nil {
				if tests {
					return prefix + project.test
				}
				return prefix + project.run
			}
		}
		if dir == "." {
			break
		}
	}
	if tests {
		return ""
	}
	switch strings.ToLower(filepath.Ext(current)) {
	case ".py":
		return "python3 " + shellArg(current)
	case ".js", ".mjs", ".cjs":
		return "node " + shellArg(current)
	case ".go":
		return "go run " + shellArg(current)
	}
	return ""
}

func shellArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func (e *editor) formatCurrent() {
	b := e.current()
	input := []byte(strings.Join(runeLines(b.lines), "\n"))
	formatted, err := formatCode(b.path, input)
	if err != nil {
		e.status = "Format failed: " + err.Error()
		return
	}
	if bytes.Equal(input, formatted) {
		e.status = "Already formatted"
		return
	}
	b.recordUndo()
	fresh := newBuffer(b.path, formatted)
	b.lines = fresh.lines
	b.row = min(b.row, len(b.lines)-1)
	b.col = min(b.col, len(b.lines[b.row]))
	b.dirty = true
	e.invalidateCompletion()
	e.status = "Formatted " + b.path
}

func formatCode(path string, input []byte) ([]byte, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return format.Source(input)
	case ".json":
		var out bytes.Buffer
		if err := json.Indent(&out, bytes.TrimSpace(input), "", "  "); err != nil {
			return nil, err
		}
		return append(out.Bytes(), '\n'), nil
	default:
		lines := strings.Split(string(input), "\n")
		for i := range lines {
			lines[i] = strings.TrimRight(lines[i], " \t")
		}
		return []byte(strings.Join(lines, "\n")), nil
	}
}

func (e *editor) checkProject() {
	e.shell.open, e.shell.focused = true, true
	b := e.current()
	ext := strings.ToLower(filepath.Ext(b.path))
	switch ext {
	case ".go":
		e.runDiagnostic("go test ./...", "go", "test", "-buildvcs=false", "./...")
	case ".py":
		e.runDiagnostic("python syntax "+b.path, "python3", "-m", "py_compile", b.path)
	case ".js", ".jsx":
		e.runDiagnostic("node --check "+b.path, "node", "--check", b.path)
	case ".rs":
		if _, err := os.Stat("Cargo.toml"); err == nil {
			e.runDiagnostic("cargo check", "cargo", "check")
		} else {
			e.shell.output = append(e.shell.output, "No Cargo.toml found")
		}
	case ".json":
		var value any
		if err := json.Unmarshal([]byte(strings.Join(runeLines(b.lines), "\n")), &value); err != nil {
			e.shell.output = append(e.shell.output, "JSON: "+err.Error())
		} else {
			e.shell.output = append(e.shell.output, "JSON: valid")
		}
	case ".tf", ".tfvars", ".hcl":
		e.runDiagnostic("terraform validate", "terraform", "validate")
	case ".cs", ".fs", ".vb", ".razor", ".cshtml":
		e.runDiagnostic("dotnet test", "dotnet", "test")
	default:
		e.shell.output = append(e.shell.output, "No diagnostics available for "+ext)
	}
	e.shell.scroll = 0
}

func (e *editor) runDiagnostic(label, name string, args ...string) {
	e.shell.output = append(e.shell.output, "$ "+label)
	path, err := exec.LookPath(name)
	if err != nil {
		e.shell.output = append(e.shell.output, name+" is not installed")
		return
	}
	out, runErr := exec.Command(path, args...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if text != "" {
		e.shell.output = append(e.shell.output, strings.Split(text, "\n")...)
	}
	if runErr != nil {
		e.shell.output = append(e.shell.output, "failed: "+runErr.Error())
	} else {
		e.shell.output = append(e.shell.output, fmt.Sprintf("✓ %s passed", label))
	}
}
