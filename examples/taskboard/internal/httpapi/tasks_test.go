package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"example.com/kiwi-taskboard/internal/tasks"
)

func TestListTasks(t *testing.T) {
	handler := NewHandler(tasks.NewStore())
	request := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	response := httptest.NewRecorder()

	handler.ListTasks(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var board []tasks.Task
	if err := json.Unmarshal(response.Body.Bytes(), &board); err != nil {
		t.Fatal(err)
	}
	if len(board) != 4 {
		t.Fatalf("tasks = %d, want 4", len(board))
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache control = %q, want no-store", got)
	}
}

func TestHealth(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(tasks.NewStore()).Health(response,
		httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Fatalf("unexpected health response: %v", body)
	}
}
