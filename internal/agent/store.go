package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

const maxSessionBytes = 8 << 20

func SessionPath(root string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", errors.New("session root must be absolute")
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	if !filepath.IsAbs(base) {
		return "", errors.New("XDG_STATE_HOME must be absolute")
	}
	hash := sha256.Sum256([]byte(filepath.Clean(root)))
	return filepath.Join(base, "code-editor", "agents", hex.EncodeToString(hash[:])+".json"), nil
}

func validateSession(s Session) error {
	switch s.State {
	case Idle, Running, AwaitingApproval, Reviewing, Completed, Failed, Cancelled, Interrupted:
	default:
		return errors.New("invalid agent session state")
	}
	if s.Version != 1 {
		return errors.New("unsupported agent session version")
	}
	if len(s.Draft) > 16<<10 || len(s.Plan) > 32 || len(s.Changes) > MaxChanges || len(s.Checks) > 16 || len(s.History) > 16 || len(s.Log.Entries) > MaxLogEntries || s.Log.Next < 0 || s.Log.Next >= MaxLogEntries {
		return errors.New("agent session exceeds limits")
	}
	for _, line := range s.Log.Entries {
		if len(line) > MaxLogEntryBytes {
			return errors.New("session log entry exceeds limit")
		}
	}
	for _, step := range s.Plan {
		if len(step) > 512 {
			return errors.New("session plan entry exceeds limit")
		}
	}
	for _, change := range s.Changes {
		if _, err := CleanPath(change.Path); err != nil {
			return err
		}
		if !utf8.ValidString(change.Before) || !utf8.ValidString(change.After) || strings.ContainsRune(change.Before, 0) || strings.ContainsRune(change.After, 0) {
			return errors.New("invalid text in persisted change")
		}
		if len(change.Before) > MaxFileBytes || len(change.After) > MaxFileBytes {
			return errors.New("session change exceeds limit")
		}
	}
	for _, check := range s.Checks {
		if len(check.Command) > 64 || len(check.Output) > 33<<10 || len(check.Error) > 8192 {
			return errors.New("session check exceeds limit")
		}
		for _, arg := range check.Command {
			if len(arg) > 4096 {
				return errors.New("session command argument exceeds limit")
			}
		}
	}
	for _, m := range s.History {
		if (m.Role != "user" && m.Role != "assistant") || len(m.Content) > 16<<10 || len(m.ToolCalls) != 0 || m.ToolCallID != "" {
			return errors.New("invalid persisted conversation")
		}
	}
	return nil
}

func LoadSession(path string) (Session, error) {
	s := NewSession()
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSessionBytes {
		return s, errors.New("invalid agent session file")
	}
	file, err := os.Open(path)
	if err != nil {
		return s, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSessionBytes+1))
	if err != nil {
		return s, err
	}
	if len(data) > maxSessionBytes {
		return s, errors.New("agent session exceeds size limit")
	}
	if err = json.Unmarshal(data, &s); err != nil {
		return NewSession(), err
	}
	if err = validateSession(s); err != nil {
		return NewSession(), err
	}
	if s.State == Running || s.State == AwaitingApproval {
		s.State = Interrupted
		s.Log.Add("Previous run was interrupted. Commands and approvals were not replayed.")
	}
	if s.View < 0 || s.View >= ViewCount {
		s.View = 0
	}
	for i := range s.Scroll {
		if s.Scroll[i] < 0 {
			s.Scroll[i] = 0
		}
	}
	return s, nil
}

func saveSession(path string, s Session) error {
	if err := validateSession(s); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if len(data) > maxSessionBytes {
		return errors.New("agent session exceeds size limit")
	}
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".agent-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

type storeJob struct{ session *Session }

// Store keeps at most one pending checkpoint. Older pending checkpoints are
// replaced; disk IO and JSON encoding never happen on the UI event loop.
type Store struct {
	jobs   chan storeJob
	done   chan struct{}
	errors chan error
	mu     sync.Mutex
	closed bool
}

func NewStore(path string) *Store {
	s := &Store{jobs: make(chan storeJob, 1), done: make(chan struct{}), errors: make(chan error, 1)}
	go func() {
		defer close(s.done)
		for job := range s.jobs {
			var err error
			if job.session == nil {
				err = os.Remove(path)
				if os.IsNotExist(err) {
					err = nil
				}
			} else {
				err = saveSession(path, *job.session)
			}
			if err != nil {
				select {
				case s.errors <- fmt.Errorf("agent history: %w", err):
				default:
				}
			}
		}
	}()
	return s
}
func (s *Store) Errors() <-chan error { return s.errors }

// Queue(nil) forgets persisted history in the same serialized stream as saves.
func (s *Store) Queue(session *Session) {
	var job storeJob
	if session != nil {
		copy := session.Clone()
		job.session = &copy
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.jobs <- job:
		return
	default:
	}
	select {
	case <-s.jobs:
	default:
	}
	s.jobs <- job
}
func (s *Store) Close() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.jobs)
	}
	s.mu.Unlock()
	<-s.done
}
