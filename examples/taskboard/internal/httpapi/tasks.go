package httpapi

import (
	"encoding/json"
	"net/http"

	"example.com/kiwi-taskboard/internal/tasks"
)

// Handler connects the HTTP routes to the task store.
type Handler struct {
	store *tasks.Store
}

func NewHandler(store *tasks.Store) *Handler {
	return &Handler{store: store}
}

// ListTasks returns the current board as a JSON array.
func (h *Handler) ListTasks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	if err := json.NewEncoder(w).Encode(h.store.ListTasks()); err != nil {
		// The response may already be written; do not append an error body.
		return
	}
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write([]byte(`{"status":"ok","service":"kiwi-taskboard"}`))
}
