package tasks

// Task is one item on the board.
type Task struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

// Store holds an immutable seed board for this read-only demo.
type Store struct {
	items []Task
}

func NewStore() *Store {
	return &Store{items: []Task{
		{ID: 1, Title: "Explore the project files", Done: true},
		{ID: 2, Title: "Find the ListTasks handler", Done: true},
		{ID: 3, Title: "Run the API tests", Done: false},
		{ID: 4, Title: "Review the dependency graph", Done: false},
	}}
}

// ListTasks returns a copy so callers cannot change the stored board.
func (s *Store) ListTasks() []Task {
	items := make([]Task, len(s.items))
	copy(items, s.items)
	return items
}

func (s *Store) CompletedTasks() int {
	completed := 0
	for _, task := range s.items {
		if task.Done {
			completed++
		}
	}
	return completed
}
