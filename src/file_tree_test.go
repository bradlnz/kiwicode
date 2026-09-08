package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestLiveFileTree(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "ignored/\n")
	write("kept/original.txt", "on disk")
	if err := os.Mkdir("vacant", 0700); err != nil {
		t.Fatal(err)
	}
	b := newBuffer("kept/original.txt", []byte("unsaved"))
	b.dirty = true
	e := &editor{buffers: []*buffer{b}, showExplorer: true, rows: 30, cols: 100}
	e.refreshFiles()
	e.setFolderCollapsed("kept", false)
	for i, entry := range e.visibleTree() {
		if entry.path == b.path {
			e.selected = i
		}
	}
	has := func(path string, dir bool) bool {
		return slices.Contains(e.tree, treeEntry{path: path, dir: dir})
	}
	if !has("vacant", true) {
		t.Fatal("initial empty directory is missing")
	}
	await := func(ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			e.pollEditor(nil)
			if ready() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("file tree did not update automatically: %#v", e.tree)
	}
	write("added/deep/new_test.go", "package demo\nfunc TestNew(t *testing.T) {}\n")
	write("ignored/hidden.go", "ignored")
	write("node_modules/hidden.js", "ignored")
	await(func() bool { return has("added/deep/new_test.go", false) })
	if has("ignored", true) || has("node_modules", true) || len(e.tests) != 1 || e.tests[0].path != "added/deep/new_test.go" {
		t.Fatalf("ignore rules or test discovery lost: %#v %#v", e.tree, e.tests)
	}
	if e.collapsed["kept"] || e.visibleTree()[e.selected].path != b.path || e.current() != b || !b.dirty {
		t.Fatal("refresh lost expansion, selection, or unsaved buffer")
	}
	if err := os.Rename("added", "moved"); err != nil {
		t.Fatal(err)
	}
	await(func() bool { return has("moved/deep/new_test.go", false) && !has("added", true) })
	if err := os.Remove("moved/deep/new_test.go"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove("vacant"); err != nil {
		t.Fatal(err)
	}
	await(func() bool { return !has("moved/deep/new_test.go", false) && !has("vacant", true) })
	if !has("moved/deep", true) || len(e.tests) != 0 {
		t.Fatal("deleting the last file lost its empty directory or retained stale tests")
	}
	// A scan that finishes after a manual refresh must not overwrite newer data.
	e.fileRefreshAt = time.Time{}
	e.pollFileTree()
	select {
	case snapshot := <-e.fileRefreshDone:
		e.fileRefreshDone <- snapshot
	case <-time.After(5 * time.Second):
		t.Fatal("background scan timed out")
	}
	write("newest.txt", "new")
	e.refreshFiles()
	e.pollFileTree()
	if !has("newest.txt", false) {
		t.Fatal("stale scan overwrote a manual refresh")
	}
	// Cached workspaces retain their own channels and absolute scan roots.
	e.fileRefreshAt = time.Time{}
	e.pollFileTree()
	other := t.TempDir()
	if err := os.Chdir(other); err != nil {
		t.Fatal(err)
	}
	write("foreign.txt", "other workspace")
	select {
	case snapshot := <-e.fileRefreshDone:
		if !slices.Contains(snapshot.files, "newest.txt") || slices.Contains(snapshot.files, "foreign.txt") {
			t.Fatalf("scan followed a changed working directory: %v", snapshot.files)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("background scan timed out after directory change")
	}
}
