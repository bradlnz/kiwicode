package main

import (
	"code-editor/internal/agent"
	"testing"
)

func TestHotPathAllocationBudgets(t *testing.T) {
	var decoder inputDecoder
	input := []byte("abcdefghij")
	emit := func(inputEvent) bool { return true }
	decoder.feed(input, emit)
	if allocations := testing.AllocsPerRun(1000, func() { decoder.feed(input, emit) }); allocations != 0 {
		t.Fatalf("ASCII input decoding allocated %.1f times; budget is zero", allocations)
	}
	var log agent.Log
	for i := 0; i < agent.MaxLogEntries; i++ {
		log.Add("warm log")
	}
	if allocations := testing.AllocsPerRun(1000, func() { log.Add("agent progress") }); allocations != 0 {
		t.Fatalf("steady-state log append allocated %.1f times; budget is zero", allocations)
	}
}
