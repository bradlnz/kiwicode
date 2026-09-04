package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var slopPhrases = []string{
	"as an ai", "in order to", "this function is responsible for", "this method is responsible for",
	"robust and scalable", "comprehensive solution", "seamlessly integrates", "todo: implement",
	"placeholder implementation",
}

func (e *editor) checkSlop() {
	e.clearModalViews()
	e.shell.open, e.shell.focused = true, true
	e.shell.output = append(e.shell.output, "$ AI slop scan", "Heuristic quality signals only — this does not prove AI authorship.")
	findings := slopFindings(e.files)
	if len(findings) == 0 {
		findings = []string{"✓ No obvious slop signals found"}
	}
	e.shell.output = append(e.shell.output, findings...)
	e.shell.scroll = 0
}

func slopFindings(files []string) []string {
	var findings []string
	for _, path := range files {
		if _, supported := languageSyntaxes[strings.ToLower(filepath.Ext(path))]; !supported {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) > 2<<20 {
			continue
		}
		lines := strings.Split(string(data), "\n")
		comment, nonblank := 0, 0
		repeated := map[string]int{}
		for index, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			nonblank++
			ext := strings.ToLower(filepath.Ext(path))
			marker := languageSyntaxes[ext].comment
			isComment := marker != "" && strings.HasPrefix(trimmed, marker)
			if isComment {
				comment++
			}
			lower := strings.ToLower(trimmed)
			for _, phrase := range slopPhrases {
				if isComment && strings.Contains(lower, phrase) {
					findings = append(findings, fmt.Sprintf("%s:%d  suspicious filler/placeholder: %s", path, index+1, plain(trimmed)))
					break
				}
			}
			if strings.HasPrefix(lower, `panic("todo`) || strings.HasPrefix(lower, "except exception") ||
				strings.HasPrefix(lower, "catch (exception") || strings.HasPrefix(lower, "throw new notimplementedexception") {
				findings = append(findings, fmt.Sprintf("%s:%d  placeholder or broad error handling: %s", path, index+1, plain(trimmed)))
			}
			if len(trimmed) > 30 && !strings.Contains("{}[]()", trimmed) {
				repeated[trimmed]++
			}
		}
		if nonblank >= 30 && comment*100/nonblank > 50 {
			findings = append(findings, fmt.Sprintf("%s  comments are %d%% of nonblank lines", path, comment*100/nonblank))
		}
		for line, count := range repeated {
			if count >= 4 {
				findings = append(findings, fmt.Sprintf("%s  line repeated %d times: %s", path, count, plain(line)))
			}
		}
		if len(findings) >= 100 {
			return append(findings[:100], "… scan capped at 100 findings")
		}
	}
	return findings
}
