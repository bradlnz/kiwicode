package main

import (
	"fmt"
	"testing"
)

func TestHotPathAllocationBudgets(t *testing.T) {
	e := &editor{showExplorer: true, cols: 100, collapsed: map[string]bool{"folder": true}}
	e.tree = append(e.tree, treeEntry{path: "folder", dir: true})
	for i := 0; i < 10000; i++ {
		e.tree = append(e.tree, treeEntry{path: fmt.Sprintf("folder/file-%d.txt", i)})
	}
	if len(e.visibleTree()) != 1 {
		t.Fatal("collapsed folder exposed its children")
	}
	e.setFolderCollapsed("folder", false)
	if len(e.visibleTree()) != 10001 {
		t.Fatal("expanding a folder did not invalidate the cached tree")
	}
	if allocations := testing.AllocsPerRun(100, func() { e.visibleTree(); e.sidebarWidth() }); allocations != 0 {
		t.Fatalf("cached explorer layout allocated %.1f times per frame", allocations)
	}
	var decoder inputDecoder
	input := []byte("abcdefghij")
	emit := func(inputEvent) bool { return true }
	decoder.feed(input, emit)
	if allocations := testing.AllocsPerRun(1000, func() { decoder.feed(input, emit) }); allocations != 0 {
		t.Fatalf("ASCII input decoding allocated %.1f times; budget is zero", allocations)
	}
}
