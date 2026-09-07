# Task API

`GET /api/tasks` returns the four seeded tasks as JSON.
Each task contains an integer `id`, a string `title`, and a boolean `done`.
The handler sets `Cache-Control: no-store`.

`GET /healthz` returns `{"status":"ok","service":"kiwi-taskboard"}`.

The example is deliberately read-only. The store returns copies of its slice;
there is no persistence, authentication, or write endpoint.
