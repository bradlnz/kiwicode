package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxSnapshotBytes = 16 << 20
const maxSnapshotFileBytes = 8 << 20

type boundedOutput struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := min(len(p), (32<<10)-len(b.data))
	b.data = append(b.data, p[:n]...)
	if n < len(p) {
		b.truncated = true
	}
	return len(p), nil
}
func (b *boundedOutput) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := Display(string(b.data))
	if b.truncated {
		out += "\n[output truncated at 32 KiB]"
	}
	return out
}

func (w *worker) check(ctx context.Context, argv []string) (CheckResult, error) {
	if w.request.FilesTruncated {
		return CheckResult{}, errors.New("workspace exceeds file-count limit; refusing checks on a partial snapshot")
	}
	dir, err := os.MkdirTemp("", "kiwicode-agent-check-*")
	if err != nil {
		return CheckResult{}, err
	}
	defer os.RemoveAll(dir)
	paths := map[string]bool{}
	for _, path := range w.files {
		paths[path] = true
	}
	for path := range w.snapshots {
		paths[path] = true
	}
	if len(paths) > MaxFiles {
		return CheckResult{}, errors.New("snapshot exceeds file-count limit")
	}
	names := make([]string, 0, len(paths))
	for path := range paths {
		names = append(names, path)
	}
	sort.Strings(names)
	total := 0
	for _, path := range names {
		if err = ctx.Err(); err != nil {
			return CheckResult{}, err
		}
		if w.blocked[path] {
			return CheckResult{}, errors.New("cannot check a workspace with captured unsaved buffers")
		}
		data, mode, readErr := w.root.ReadLimit(path, maxSnapshotFileBytes)
		if snapshot, ok := w.snapshots[path]; ok {
			if snapshot.existed {
				if readErr != nil || string(data) != snapshot.text {
					return CheckResult{}, fmt.Errorf("file changed since read: %s", path)
				}
			} else if !os.IsNotExist(readErr) {
				return CheckResult{}, fmt.Errorf("new-file path changed since read: %s", path)
			} else if _, proposed := w.changes[path]; !proposed {
				continue
			}
		}
		if change, ok := w.changes[path]; ok {
			data = []byte(change.After)
			if !change.Existed {
				mode = 0644
			}
			readErr = nil
		}
		if readErr != nil {
			return CheckResult{}, fmt.Errorf("cannot snapshot %s: %w", path, readErr)
		}
		total += len(data)
		if total > maxSnapshotBytes {
			return CheckResult{}, errors.New("snapshot exceeds 16 MiB limit")
		}
		target := filepath.Join(dir, filepath.FromSlash(path))
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return CheckResult{}, err
		}
		if err = os.WriteFile(target, data, mode&0777); err != nil {
			return CheckResult{}, err
		}
	}
	checkCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	return executeCheck(checkCtx, dir, argv), nil
}

// Commands require both a per-run opt-in and a per-command user approval.
// A temp directory and minimal environment are NOT an OS security sandbox.
func executeCheck(ctx context.Context, dir string, argv []string) CheckResult {
	result := CheckResult{Command: append([]string(nil), argv...), ExitCode: -1}
	if len(argv) == 0 {
		return result
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	home := filepath.Join(dir, ".kiwicode-agent-home")
	tmp := filepath.Join(home, "tmp")
	if err := os.MkdirAll(tmp, 0700); err != nil {
		result.Error = err.Error()
		return result
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = home
	}
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TMPDIR=" + tmp, "LANG=C.UTF-8", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOCACHE=" + filepath.Join(cache, "go-build")}
	cmd.WaitDelay = time.Second
	configureCommand(cmd)
	var output boundedOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	err = cmd.Run()
	result.Output = output.String()
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		result.Error = err.Error()
	}
	if ctx.Err() != nil {
		result.Error = ctx.Err().Error()
	}
	// Defensive: avoid accidental display control injection in executable errors.
	result.Error = strings.TrimSpace(Display(result.Error))
	return result
}
