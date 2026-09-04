package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type inspectionFinding struct {
	principle, message, path string
	row                      int
}

type inspectionRow struct {
	text    string
	finding int
}

var concreteDependency = regexp.MustCompile(`\bnew\s+([A-Z][A-Za-z0-9_]*)`)

func (e *editor) toggleInspector() {
	if e.inspect {
		e.inspect = false
		e.status = "Inspector closed"
		return
	}
	e.shell.open, e.shell.focused = false, false
	e.graph, e.help, e.explorer = false, false, false
	e.opsMode = ""
	e.inspect = true
	e.refreshInspector()
}

func (e *editor) refreshInspector() {
	if !e.inspect || len(e.buffers) == 0 {
		return
	}
	e.inspectionFindings = inspectProject([]string{e.current().path}, e.completionSources())
	e.inspectionSelected, e.inspectionTop = 0, 0
	if len(e.inspectionFindings) == 0 {
		e.status = "Inspect: no structural issues in " + filepath.Base(e.current().path)
	} else {
		e.status = fmt.Sprintf("Inspect: %d improvement%s in %s", len(e.inspectionFindings), map[bool]string{true: "", false: "s"}[len(e.inspectionFindings) == 1], filepath.Base(e.current().path))
	}
}

func inspectProject(files []string, openSources ...map[string][]byte) []inspectionFinding {
	// ponytail: fast structural heuristics cover obvious design smells; use a language server when semantic proof matters.
	var findings []inspectionFinding
	type blockLocation struct {
		path string
		row  int
	}
	blocks := map[string]blockLocation{}
	reportedBlocks := map[string]bool{}
	for _, path := range files {
		if _, supported := languageSyntaxes[strings.ToLower(filepath.Ext(path))]; !supported {
			continue
		}
		data, err := os.ReadFile(path)
		if len(openSources) > 0 {
			if open, ok := openSources[0][path]; ok {
				data, err = open, nil
			}
		}
		if err != nil || len(data) > 2<<20 {
			continue
		}
		lines := strings.Split(string(data), "\n")
		if len(lines) > 400 {
			findings = append(findings, inspectionFinding{"SRP", fmt.Sprintf("File has %d lines; split distinct responsibilities", len(lines)), path, 1})
		}

		types, members := fileModelMembers(path, data)
		if len(types) > 3 {
			findings = append(findings, inspectionFinding{"SRP", fmt.Sprintf("File declares %d types; consider one cohesive type per file", len(types)), path, lineContaining(lines, types[3])})
		}
		if len(types) == 1 && len(members) > 15 {
			findings = append(findings, inspectionFinding{"ISP", fmt.Sprintf("%s exposes %d members; smaller role-focused interfaces may help", types[0], len(members)), path, lineContaining(lines, types[0])})
		}

		symbols := fileSymbols(path)
		sort.Slice(symbols, func(i, j int) bool { return symbols[i].row < symbols[j].row })
		for index, symbol := range symbols {
			end := len(lines) + 1
			if index+1 < len(symbols) {
				end = symbols[index+1].row
			}
			if end-symbol.row > 60 {
				findings = append(findings, inspectionFinding{"SRP", fmt.Sprintf("%s spans about %d lines; extract a focused operation", symbol.name, end-symbol.row), path, symbol.row})
			}
		}

		cases, firstSwitch := 0, 0
		dependencies := map[string]bool{}
		firstDependency := 0
		for index, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(trimmed, "switch ") || strings.HasPrefix(trimmed, "switch(") || strings.HasPrefix(trimmed, "switch (") {
				if firstSwitch == 0 {
					firstSwitch = index + 1
				}
			}
			if strings.HasPrefix(trimmed, "case ") || strings.HasPrefix(trimmed, "case\t") {
				cases++
			}
			for _, match := range concreteDependency.FindAllStringSubmatch(line, -1) {
				dependencies[match[1]] = true
				if firstDependency == 0 {
					firstDependency = index + 1
				}
			}
			lower := strings.ToLower(trimmed)
			if strings.Contains(lower, "notimplemented") || strings.Contains(lower, "unsupportedoperation") {
				findings = append(findings, inspectionFinding{"LSP", "Subtype rejects an operation; verify it can replace its base type", path, index + 1})
			}
		}
		if cases > 10 {
			findings = append(findings, inspectionFinding{"OCP", fmt.Sprintf("%d case branches may grow with each new variant", cases), path, max(1, firstSwitch)})
		}
		if len(dependencies) > 5 {
			findings = append(findings, inspectionFinding{"DIP", fmt.Sprintf("Constructs %d concrete dependencies; inject stable boundaries", len(dependencies)), path, max(1, firstDependency)})
		}

		for row := 0; row+3 < len(lines); row++ {
			block := normalizedBlock(lines[row : row+4])
			if block == "" || reportedBlocks[block] {
				continue
			}
			if first, exists := blocks[block]; exists && (first.path != path || row+1-first.row > 8) {
				findings = append(findings, inspectionFinding{"DRY", fmt.Sprintf("Repeated 4-line block also at %s:%d", first.path, first.row), path, row + 1})
				reportedBlocks[block] = true
			} else if !exists {
				blocks[block] = blockLocation{path, row + 1}
			}
		}
		if len(findings) >= 100 {
			break
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].path == findings[j].path {
			return findings[i].row < findings[j].row
		}
		return findings[i].path < findings[j].path
	})
	if len(findings) > 100 {
		findings = findings[:100]
	}
	return findings
}

func normalizedBlock(lines []string) string {
	parts := make([]string, 0, len(lines))
	meaningful := 0
	for _, line := range lines {
		line = strings.Join(strings.Fields(strings.TrimSpace(line)), " ")
		if len(line) >= 8 && line != "{" && line != "}" {
			meaningful++
		}
		parts = append(parts, line)
	}
	if meaningful < 3 {
		return ""
	}
	return strings.Join(parts, "\n")
}

func lineContaining(lines []string, value string) int {
	for index, line := range lines {
		if strings.Contains(line, value) {
			return index + 1
		}
	}
	return 1
}

func (e *editor) inspectorWidth(available int) int {
	if !e.inspect || e.graph || e.help || e.opsMode != "" || e.shell.open || available < 70 {
		return 0
	}
	return min(38, max(24, available/3))
}

func (e *editor) codeAreaWidth() int {
	available := e.cols - e.sidebarWidth()
	return available - e.inspectorWidth(available)
}

func (e *editor) inspectionRows(width int) []inspectionRow {
	if len(e.inspectionFindings) == 0 {
		return []inspectionRow{{text: " ✓ No structural issues found", finding: -1}}
	}
	rows := make([]inspectionRow, 0, len(e.inspectionFindings)*2)
	for index, finding := range e.inspectionFindings {
		location := filepath.Base(finding.path) + ":" + fmt.Sprint(finding.row)
		rows = append(rows,
			inspectionRow{fmt.Sprintf(" %s  %s", finding.principle, location), index},
			inspectionRow{"   " + finding.message, index},
		)
	}
	return rows
}

func (e *editor) selectInspection(screenRow int) {
	rows := e.inspectionRows(e.inspectorWidth(e.cols - e.sidebarWidth()))
	index := e.inspectionTop + screenRow
	if index < 0 || index >= len(rows) || rows[index].finding < 0 {
		return
	}
	e.inspectionSelected = rows[index].finding
	finding := e.inspectionFindings[e.inspectionSelected]
	e.open(finding.path)
	b := e.current()
	b.row = min(max(0, finding.row-1), len(b.lines)-1)
	b.col = 0
	e.selection = textSelection{buffer: b, startRow: b.row, endRow: b.row, endCol: len(b.lines[b.row])}
	e.status = finding.principle + ": " + finding.message
}
