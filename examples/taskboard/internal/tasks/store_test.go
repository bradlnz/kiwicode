package tasks

import "testing"

func TestSeedTasks(t *testing.T) {
	store := NewStore()
	if got := len(store.ListTasks()); got != 4 {
		t.Fatalf("task count = %d, want 4", got)
	}
	if got := store.CompletedTasks(); got != 2 {
		t.Fatalf("completed = %d, want 2", got)
	}
}

func TestTasksAreCopied(t *testing.T) {
	store := NewStore()
	board := store.ListTasks()
	board[0].Title = "Changed outside the store"
	if store.ListTasks()[0].Title == board[0].Title {
		t.Fatal("ListTasks exposed the store's internal slice")
	}
}
