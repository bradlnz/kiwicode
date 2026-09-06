//go:build go1.24

package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type rootedWorkspace struct{ root *os.Root }

func openWorkspace(path string) (workspaceReader, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("workspace root must be absolute")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	return &rootedWorkspace{root}, nil
}
func (w *rootedWorkspace) Close() error { return w.root.Close() }
func (w *rootedWorkspace) Read(name string) ([]byte, os.FileMode, error) {
	return w.ReadLimit(name, MaxFileBytes)
}
func (w *rootedWorkspace) ReadLimit(name string, limit int) ([]byte, os.FileMode, error) {
	path, err := CleanPath(name)
	if err != nil {
		return nil, 0, err
	}
	parts := strings.Split(path, "/")
	for i := range parts {
		info, err := w.root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil {
			return nil, 0, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, 0, errors.New("symbolic links are excluded from agent files")
		}
	}
	// Root prevents directory traversal even if paths change after validation;
	// NONBLOCK ensures a concurrently substituted FIFO cannot hang the worker.
	file, err := w.root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, 0, err
	}
	return readRegular(file, limit)
}
