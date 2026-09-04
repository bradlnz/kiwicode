package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type sourceChange struct {
	status, path string
}

const (
	sourceTopPadding      = 1
	sourceCommitRows      = 3
	sourceMessageRow      = sourceTopPadding
	sourceInputStart      = sourceMessageRow + 1
	sourceCommitButtonRow = sourceInputStart + sourceCommitRows
	sourceHeaderRows      = sourceCommitButtonRow + 1
)

type sourceResult struct {
	output     string
	err        error
	generation int
}

type sourceDiffResult struct {
	path       string
	changes    map[int]rune
	generation int
}

func (change sourceChange) staged() bool {
	return len(change.status) > 0 && change.status[0] != ' ' && change.status[0] != '?'
}

func (change sourceChange) unstaged() bool {
	return change.status == "??" || len(change.status) > 1 && change.status[1] != ' '
}

func (e *editor) openSourceControl() {
	e.clearModalViews()
	e.showExplorer, e.explorer = true, true
	e.testMode, e.sourceMode = false, true
	e.sourceCommitFocused = false
	e.loadSourceControl()
}

func (e *editor) loadSourceControl() {
	e.sourceChanges, e.sourceSelected, e.sourceTop = nil, 0, 0
	e.sourceDiscardArmed = ""
	e.sourceLineChanges = nil
	e.sourceDiffGeneration++
	e.sourceMessage, e.sourceLoading = "Loading…", true
	e.sourceGeneration++
	if e.sourceDone == nil {
		e.sourceDone = make(chan sourceResult, 16)
	}
	generation := e.sourceGeneration
	go func() {
		out, err := exec.Command("git", "status", "--short", "--untracked-files=all").CombinedOutput()
		e.sourceDone <- sourceResult{string(out), err, generation}
	}()
}

func (e *editor) pollSourceControl() bool {
	if e.sourceDone != nil {
		select {
		case result := <-e.sourceDone:
			if result.generation != e.sourceGeneration {
				return false
			}
			e.sourceLoading = false
			if result.err != nil {
				e.sourceMessage = "Not a Git repository"
				e.sourceChanges = nil
			} else {
				e.sourceMessage = "No changes"
				e.sourceChanges = parseSourceChanges(result.output)
				e.loadSourceFileChanges(e.current().path)
			}
			return true
		default:
		}
	}
	if e.sourceDiffDone != nil {
		select {
		case result := <-e.sourceDiffDone:
			if result.generation != e.sourceDiffGeneration {
				return false
			}
			if e.sourceLineChanges == nil {
				e.sourceLineChanges = map[string]map[int]rune{}
			}
			e.sourceLineChanges[result.path] = result.changes
			return true
		default:
		}
	}
	return false
}

func (e *editor) openSourceChange(index int) {
	if index < 0 || index >= len(e.sourceChanges) {
		return
	}
	e.sourceSelected = index
	e.sourceCommitFocused = false
	path := e.sourceChanges[index].path
	e.open(path)
}

func (e *editor) handleSourceCommit(k key) {
	switch k.code {
	case keyEnter:
		e.commitInput = append(e.commitInput, '\n')
	case keyBackspace:
		if len(e.commitInput) > 0 {
			e.commitInput = e.commitInput[:len(e.commitInput)-1]
		}
	case keyTab:
		e.commitSourceMessage()
	default:
		if k.r >= 32 {
			e.commitInput = append(e.commitInput, k.r)
		}
	}
}

func (e *editor) sourceCommitLines(width int) []string {
	lines := wrapTextLines(strings.Split(string(e.commitInput), "\n"), max(1, width))
	return lines[max(0, len(lines)-sourceCommitRows):]
}

func wrapTextLines(lines []string, width int) []string {
	width = max(1, width)
	var wrapped []string
	for _, line := range lines {
		runes := []rune(line)
		if len(runes) == 0 {
			wrapped = append(wrapped, "")
		}
		for len(runes) > 0 {
			take := min(width, len(runes))
			wrapped = append(wrapped, string(runes[:take]))
			runes = runes[take:]
		}
	}
	return wrapped
}

func (e *editor) commitSourceMessage() {
	message := strings.TrimSpace(string(e.commitInput))
	if message == "" {
		e.status = "Commit message cannot be empty"
		return
	}
	e.sourceCommitFocused = false
	e.commitInput = nil
	e.runSourceCommand("git commit -m " + shellArg(message))
}

func (e *editor) loadSourceFileChanges(path string) {
	var selected *sourceChange
	for index := range e.sourceChanges {
		if filepath.Clean(e.sourceChanges[index].path) == filepath.Clean(path) {
			selected = &e.sourceChanges[index]
			break
		}
	}
	if selected == nil {
		return
	}
	e.sourceDiffGeneration++
	if e.sourceDiffDone == nil {
		e.sourceDiffDone = make(chan sourceDiffResult, 16)
	}
	generation := e.sourceDiffGeneration
	change := *selected
	go func() {
		e.sourceDiffDone <- sourceDiffResult{path, sourceChangeMarkers(change), generation}
	}()
}

var sourceHunkPattern = regexp.MustCompile(`(?m)^@@ -\d+(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

func sourceChangeMarkers(change sourceChange) map[int]rune {
	markers := map[int]rune{}
	if change.status == "??" {
		data, _ := os.ReadFile(change.path)
		if len(data) == 0 {
			return markers
		}
		for row := 1; row <= len(strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")); row++ {
			markers[row] = '+'
		}
		return markers
	}
	if out, err := exec.Command("git", "--no-pager", "diff", "--no-ext-diff", "--unified=0", "HEAD", "--", change.path).Output(); err == nil {
		return parseSourceMarkers(out)
	}
	var patches []byte
	if change.staged() {
		out, _ := exec.Command("git", "--no-pager", "diff", "--no-ext-diff", "--unified=0", "--cached", "--", change.path).Output()
		patches = append(patches, out...)
	}
	if change.unstaged() {
		out, _ := exec.Command("git", "--no-pager", "diff", "--no-ext-diff", "--unified=0", "--", change.path).Output()
		patches = append(patches, out...)
	}
	return parseSourceMarkers(patches)
}

func parseSourceMarkers(diff []byte) map[int]rune {
	markers := map[int]rune{}
	for _, match := range sourceHunkPattern.FindAllSubmatch(diff, -1) {
		oldCount, newStart, newCount := 1, 0, 1
		if len(match[1]) > 0 {
			oldCount, _ = strconv.Atoi(string(match[1]))
		}
		newStart, _ = strconv.Atoi(string(match[2]))
		if len(match[3]) > 0 {
			newCount, _ = strconv.Atoi(string(match[3]))
		}
		marker := '~'
		if oldCount == 0 {
			marker = '+'
		}
		if newCount == 0 {
			markers[max(1, newStart)] = '-'
			continue
		}
		for row := newStart; row < newStart+newCount; row++ {
			markers[row] = marker
		}
	}
	return markers
}

func (e *editor) runSourceCommand(command string) {
	if e.shell.running {
		e.status = "A command is already running — Ctrl+C stops it"
		return
	}
	e.clearModalViews()
	e.shell.open, e.shell.focused = true, true
	e.sourceRefresh = true
	e.shell.start(command)
	e.status = "Running: " + command
}

func (e *editor) selectedSourceChange() (sourceChange, bool) {
	if e.sourceSelected < 0 || e.sourceSelected >= len(e.sourceChanges) {
		return sourceChange{}, false
	}
	return e.sourceChanges[e.sourceSelected], true
}

func (e *editor) discardSourceChange() {
	change, ok := e.selectedSourceChange()
	if !ok {
		return
	}
	for _, buffer := range e.buffers {
		if filepath.Clean(buffer.path) == filepath.Clean(change.path) && buffer.dirty {
			e.status = "Save or undo editor changes before discarding " + change.path
			return
		}
	}
	if e.sourceDiscardArmed != change.path {
		e.sourceDiscardArmed = change.path
		e.status = "Discard changes to " + change.path + "? Choose Discard again to confirm"
		return
	}
	e.sourceDiscardArmed = ""
	e.runSourceCommand(sourceDiscardCommand(change))
}

func sourceDiscardCommand(change sourceChange) string {
	path := shellArg(change.path)
	if change.status == "??" {
		return "git clean -f -- " + path
	}
	if len(change.status) > 0 && strings.ContainsRune("ACR", rune(change.status[0])) {
		return "git restore --staged -- " + path + " && git clean -f -- " + path
	}
	return "git restore --source=HEAD --staged --worktree -- " + path
}

func parseSourceChanges(output string) []sourceChange {
	var changes []sourceChange
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if arrow := strings.LastIndex(path, " -> "); arrow >= 0 {
			path = path[arrow+4:]
		}
		changes = append(changes, sourceChange{status: line[:2], path: strings.Trim(path, `"`)})
	}
	return changes
}

func sourceLabel(change sourceChange) string {
	status := strings.TrimSpace(change.status)
	if status == "??" {
		status = "U"
	}
	return status + "  " + change.path
}
