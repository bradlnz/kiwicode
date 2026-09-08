package main

import "fmt"

type workspaceCheckpointJob struct {
	data []byte
	done chan error
}

// One writer per live workspace keeps writes ordered. Only the newest pending
// immutable JSON snapshot is needed; returning to a cached editor never waits
// for its writer. Queue/flush/close have a single owner (the transition worker).
type workspaceCheckpoint struct {
	root, path string
	jobs       chan workspaceCheckpointJob
	done       chan struct{}
	errors     chan error
	err        error // read only after done closes
}

func newWorkspaceCheckpoint(root, path string) *workspaceCheckpoint {
	c := &workspaceCheckpoint{root: root, path: path, jobs: make(chan workspaceCheckpointJob, 1), done: make(chan struct{}), errors: make(chan error, 1)}
	go func() {
		defer close(c.done)
		for job := range c.jobs {
			if job.data != nil {
				c.err = saveStateData(c.path, job.data)
				if c.err != nil {
					select {
					case c.errors <- fmt.Errorf("Workspace %s: %w", c.root, c.err):
					default:
					}
				}
			}
			if job.done != nil {
				job.done <- c.err
			}
		}
	}()
	return c
}

func (c *workspaceCheckpoint) queue(data []byte) {
	select {
	case <-c.jobs:
	default:
	}
	c.jobs <- workspaceCheckpointJob{data: data}
}

func (c *workspaceCheckpoint) flush() error {
	done := make(chan error, 1)
	c.jobs <- workspaceCheckpointJob{done: done}
	return <-done
}

func (c *workspaceCheckpoint) close() error {
	close(c.jobs)
	<-c.done
	return c.err
}

func (e *editor) pollWorkspaceCheckpoints() bool {
	changed := false
	check := func(c *workspaceCheckpoint) {
		if c == nil {
			return
		}
		select {
		case err := <-c.errors:
			e.status, changed = err.Error(), true
		default:
		}
	}
	check(e.checkpoint)
	for _, cached := range e.workspaces {
		check(cached.editor.checkpoint)
	}
	return changed
}
