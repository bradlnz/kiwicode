package main

import (
	"log"
	"net/http"
	"time"

	"example.com/kiwi-taskboard/internal/httpapi"
	"example.com/kiwi-taskboard/internal/tasks"
)

func main() {
	store := tasks.NewStore()
	api := httpapi.NewHandler(store)
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/tasks", api.ListTasks)
	mux.HandleFunc("GET /healthz", api.Health)
	mux.Handle("GET /", http.FileServer(http.Dir("web")))

	server := &http.Server{
		Addr:              "127.0.0.1:8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("Kiwi Taskboard is ready at http://%s", server.Addr)
	log.Fatal(server.ListenAndServe())
}
