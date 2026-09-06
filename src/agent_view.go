package main

import (
	"code-editor/internal/agent"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// This is a bounded presentation cache, not another transcript. Resize may
// rebuild it, but a streamed delta appends only its own wrapped rows in O(delta).
func (a *agentWorkspace) appendActivityRows(text string) {
	if a.width == 0 || a.viewDirty[1] {
		return
	}
	for _, line := range wrapAgentText(text, a.width, 1024) {
		a.activityRows.Add(line)
	}
}

func wrapAgentText(text string, width, limit int) []string {
	width = max(1, width)
	text = strings.ReplaceAll(agent.Display(text), "\t", "    ")
	var out []string
	for _, line := range strings.Split(text, "\n") {
		runes := []rune(line)
		if len(runes) == 0 {
			out = append(out, " ")
		}
		for start := 0; start < len(runes); start += width {
			if len(out) >= limit {
				return append(out, "… display limit; request a smaller result")
			}
			out = append(out, string(runes[start:min(len(runes), start+width)]))
		}
		if len(out) >= limit {
			return append(out, "… display limit; request a smaller result")
		}
	}
	return out
}

func (a *agentWorkspace) rebuildView(width int) {
	if a.width != width {
		a.width = width
		a.viewDirty = [agent.ViewCount]bool{true, true, true, true, true, true, true}
		a.approvalLines = nil
	}
	if a.approval != nil && a.approvalLines == nil {
		// Every byte of the bounded approval summary is available to scroll.
		a.approvalLines = wrapAgentText("APPROVAL REQUIRED: "+a.approval.Operation+"\n\n"+a.approval.Summary+"\n\nType /approve or /deny. No action has been authorised yet.", width, 300000)
	}
	view := a.session.View
	if !a.viewDirty[view] {
		return
	}
	switch view {
	case 0:
		a.viewLines[0] = nil
		for i, step := range a.session.Plan {
			a.viewLines[0] = append(a.viewLines[0], wrapAgentText(strconv.Itoa(i+1)+". "+step, width, 512)...)
		}
		if len(a.viewLines[0]) == 0 {
			a.viewLines[0] = []string{"Enter a task below. The agent will publish its plan here.", "No plan has been generated yet."}
		}
	case 1:
		a.activityRows = agent.Log{}
		for i := 0; i < a.session.Log.Len(); i++ {
			for _, line := range wrapAgentText(a.session.Log.At(i), width, 1024) {
				a.activityRows.Add(line)
			}
		}
	case 2:
		a.viewLines[2] = nil
		for i, change := range a.session.Changes {
			state := "proposed"
			if change.Applied {
				state = "applied to buffer; use Save to write disk"
			}
			a.viewLines[2] = append(a.viewLines[2], fmt.Sprintf("%d. %s [%s]", i+1, change.Path, state))
			// Wrap, rather than crop, every review line so a narrow terminal
			// cannot hide a changed suffix that /apply would still accept.
			for _, line := range change.Diff() {
				a.viewLines[2] = append(a.viewLines[2], wrapAgentText(line, width, 4096)...)
			}
			a.viewLines[2] = append(a.viewLines[2], fmt.Sprintf("/apply %d  ·  /open %d", i+1, i+1), "")
		}
		if len(a.viewLines[2]) == 0 {
			a.viewLines[2] = []string{"No proposed changes. Live files are not modified by the agent."}
		}
	case 3:
		a.viewLines[3] = nil
		for i, check := range a.session.Checks {
			argv, _ := json.Marshal(check.Command)
			header := fmt.Sprintf("Check %d · exit %d · %s", i+1, check.ExitCode, argv)
			a.viewLines[3] = append(a.viewLines[3], wrapAgentText(header+"\n"+check.Output+"\n"+check.Error, width, 8192)...)
		}
		for _, check := range a.memoryView.Session.Checks {
			header := fmt.Sprintf("Context snapshot check · exit %d · %s", check.ExitCode, strings.Join(check.Command, " "))
			a.viewLines[3] = append(a.viewLines[3], wrapAgentText(header+"\n"+check.Output+"\n"+check.Error, width, 8192)...)
		}
		if len(a.viewLines[3]) == 0 {
			a.viewLines[3] = []string{"No checks have run.", "Host commands are disabled unless KIWICODE_AGENT_ALLOW_COMMANDS=1.", "Each command requires approval; a snapshot is NOT a security sandbox."}
		}
	case 4, 5, 6:
		a.viewLines[view] = nil
		for _, line := range a.contextLines(view) {
			a.viewLines[view] = append(a.viewLines[view], wrapAgentText(line, width, 512)...)
			if len(a.viewLines[view]) >= 8192 {
				a.viewLines[view] = a.viewLines[view][:8192]
				break
			}
		}
	}
	a.viewDirty[view] = false
}
func (a *agentWorkspace) viewCount(view int) int {
	if view == 1 {
		return a.activityRows.Len()
	}
	return len(a.viewLines[view])
}
func (a *agentWorkspace) viewLine(view, index int) string {
	if view == 1 {
		return a.activityRows.At(index)
	}
	if index < 0 || index >= len(a.viewLines[view]) {
		return ""
	}
	return a.viewLines[view][index]
}

func (e *editor) agentFrame() string {
	a := e.ensureAgent()
	if e.cols < 30 || e.rows < 8 {
		return "\x1b[H\x1b[2JAgent requires at least 30x8; Ctrl+A returns to files"
	}
	width, height := e.cols, e.rows
	a.rebuildView(max(1, width-2))
	var out strings.Builder
	out.Grow(min(1<<20, width*height+1024))
	out.WriteString("\x1b[?25l\x1b[H")
	row := func(y int, text string) {
		writeCell(&out, y, 1, ansiFG(colors.text)+fit(agent.Display(text), width))
	}
	row(1, " KiwiCode · AGENT  |  Ctrl+A files  Ctrl+X cancel  Ctrl+Q quit")
	writeCell(&out, 2, 1, e.workspaceTabs(width))
	var tabs strings.Builder
	visible := max(1, width/12)
	a.viewTabStart = max(0, a.session.View-visible+1)
	for i := a.viewTabStart; i < min(agent.ViewCount, a.viewTabStart+visible); i++ {
		name := agentViewNames[i]
		tabs.WriteString(style(a.session.View == i, "\x1b[1;4m"+ansiFG(colors.accent), "\x1b[22;24m"+ansiFG(colors.muted)) + fit(" "+name, 12))
	}
	writeCell(&out, 3, 1, fitANSI(tabs.String()+"\x1b[22;24m", width))
	contentRows := max(1, height-7)
	a.pageRows = contentRows
	approvalVisible := a.approval != nil && a.session.View == 1
	view := a.session.View
	count, top := a.viewCount(view), a.session.Scroll[view]
	if approvalVisible {
		count, top = len(a.approvalLines), a.approvalTop
	} else if view == 1 && a.follow {
		top = max(0, count-contentRows)
	}
	top = max(0, min(max(0, count-contentRows), top))
	if approvalVisible {
		a.approvalTop = top
	} else {
		a.session.Scroll[view] = top
	}
	for y := 0; y < contentRows; y++ {
		line := ""
		if top+y < count {
			if approvalVisible {
				line = a.approvalLines[top+y]
			} else {
				line = a.viewLine(view, top+y)
			}
		}
		row(4+y, " "+line)
	}
	info := string(a.session.State) + " · PgUp/PgDn scroll · Tab views · Enter send"
	if approvalVisible {
		info = fmt.Sprintf("APPROVAL: lines %d–%d/%d · /approve or /deny", top+1, min(count, top+contentRows), count)
	}
	if a.approval != nil && !approvalVisible {
		info = "Approval pending in Activity · /approve or /deny"
	}
	row(height-3, " "+info)
	// Show the configured destination before sending the first task. The key is
	// never displayed, marshalled into session state, or passed to a child.
	destination := os.Getenv("KIWICODE_AGENT_ENDPOINT")
	if destination == "" {
		destination = "not configured"
	}
	row(height-2, " Model: "+os.Getenv("KIWICODE_AGENT_MODEL")+"  Endpoint: "+destination)
	prompt, start := agentPrompt(a.input, a.cursor, max(1, width-3))
	row(height-1, " > "+prompt)
	row(height, " "+e.status)
	fmt.Fprintf(&out, "\x1b[%d;%dH\x1b[?25h\x1b[0m", height-1, min(width, 4+a.cursor-start))
	return out.String()
}

func agentPrompt(input []rune, cursor, width int) (string, int) {
	start := max(0, cursor-width+1)
	end := min(len(input), start+width)
	visible := append([]rune(nil), input[start:end]...)
	for i, r := range visible {
		if r == '\n' || r == '\r' {
			visible[i] = '↵'
		} else if r == '\t' {
			visible[i] = ' '
		}
	}
	return string(visible), start
}
