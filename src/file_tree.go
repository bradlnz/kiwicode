package main

import (
	"io/fs"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

type fileTreeSnapshot struct {
	files, directories []string
	tests              []testCase
	generation         int
	changed            bool
	err                error
}

func readFileTree(root string, previousFiles, previousDirectories []string) fileTreeSnapshot {
	var snapshot fileTreeSnapshot
	rules := readIgnoreRules(filepath.Join(root, ".gitignore"))
	snapshot.err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root || !entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if skipProjectDir(entry.Name()) || ignoredProjectPath(relative, true, rules) {
			return filepath.SkipDir
		}
		snapshot.directories = append(snapshot.directories, filepath.ToSlash(relative))
		return nil
	})
	if snapshot.err != nil {
		return snapshot
	}
	// Git handles nested ignore files and repository/global excludes for empty folders too.
	if len(snapshot.directories) > 0 {
		cmd := exec.Command("git", "-C", root, "check-ignore", "--stdin", "-z")
		cmd.Stdin = strings.NewReader(strings.Join(snapshot.directories, "\x00") + "\x00")
		if out, err := cmd.Output(); err == nil {
			ignored := strings.Split(string(out), "\x00")
			sort.Strings(ignored)
			snapshot.directories = slices.DeleteFunc(snapshot.directories, func(path string) bool {
				_, found := slices.BinarySearch(ignored, path)
				return found
			})
		}
	}
	for _, path := range projectFiles(root) {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			snapshot.err = err
			return snapshot
		}
		relative = filepath.ToSlash(relative)
		snapshot.files = append(snapshot.files, relative)
		for parent := filepath.ToSlash(filepath.Dir(relative)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			snapshot.directories = append(snapshot.directories, parent)
		}
	}
	sort.Strings(snapshot.files)
	snapshot.files = slices.Compact(snapshot.files)
	sort.Strings(snapshot.directories)
	snapshot.directories = slices.Compact(snapshot.directories)
	snapshot.changed = !slices.Equal(snapshot.files, previousFiles) || !slices.Equal(snapshot.directories, previousDirectories)
	if snapshot.changed {
		for _, path := range snapshot.files {
			if !isTestFile(path) {
				continue
			}
			for _, test := range discoverTests(filepath.Join(root, filepath.FromSlash(path))) {
				test.path = path
				snapshot.tests = append(snapshot.tests, test)
			}
		}
	}
	return snapshot
}

func (e *editor) applyFileTree(snapshot fileTreeSnapshot) {
	selectedPath, selectedTest := "", ""
	if entries := e.visibleTree(); e.selected >= 0 && e.selected < len(entries) {
		selectedPath = entries[e.selected].path
	}
	if e.testSelected >= 0 && e.testSelected < len(e.tests) {
		selectedTest = testTarget(e.tests[e.testSelected])
	}
	e.indexFiles(snapshot.files, snapshot.tests)
	known := make(map[string]bool)
	for _, entry := range e.tree {
		if entry.dir {
			known[entry.path] = true
		}
	}
	for _, path := range snapshot.directories {
		if !known[path] {
			e.tree = append(e.tree, treeEntry{path: path, dir: true})
			if _, exists := e.collapsed[path]; !exists {
				e.collapsed[path] = true
			}
		}
	}
	sort.Slice(e.tree, func(i, j int) bool { return e.tree[i].path < e.tree[j].path })
	entries := e.visibleTree()
	e.selected = min(e.selected, max(0, len(entries)-1))
	for i, entry := range entries {
		if entry.path == selectedPath {
			e.selected = i
			break
		}
	}
	e.testSelected = min(e.testSelected, max(0, len(e.tests)-1))
	for i, test := range e.tests {
		if testTarget(test) == selectedTest {
			e.testSelected = i
			break
		}
	}
}

func (e *editor) pollFileTree() bool {
	if e.workspaceDone != nil {
		return false
	}
	if e.fileRefreshDone != nil {
		select {
		case snapshot := <-e.fileRefreshDone:
			e.fileRefreshDone = nil
			if snapshot.err == nil && snapshot.generation == e.fileGeneration && snapshot.changed {
				e.applyFileTree(snapshot)
				return true
			}
		default:
		}
		return false
	}
	if time.Now().Before(e.fileRefreshAt) {
		return false
	}
	// ponytail: one background scan per 500 ms; use OS watchers if large trees make polling costly.
	e.fileRefreshAt = time.Now().Add(500 * time.Millisecond)
	e.fileRefreshDone = make(chan fileTreeSnapshot, 1)
	root, done, generation := mustCwd(), e.fileRefreshDone, e.fileGeneration
	files := slices.Clone(e.files)
	var directories []string
	for _, entry := range e.tree {
		if entry.dir {
			directories = append(directories, entry.path)
		}
	}
	go func() {
		snapshot := readFileTree(root, files, directories)
		snapshot.generation = generation
		done <- snapshot
	}()
	return false
}
