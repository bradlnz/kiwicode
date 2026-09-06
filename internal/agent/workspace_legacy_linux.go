//go:build !go1.24 && linux

package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// The conservative no-symlink openat implementation also permits validation
// with older Go installations without weakening the rooted-read boundary.
type rootedWorkspace struct{ root *os.File }

func openWorkspace(path string) (workspaceReader, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("workspace root must be absolute")
	}
	root, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := root.Stat()
	if err != nil || !info.IsDir() {
		root.Close()
		return nil, errors.New("workspace root is not a directory")
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
	dir := int(w.root.Fd())
	owned := -1
	defer func() {
		if owned >= 0 {
			syscall.Close(owned)
		}
	}()
	for _, part := range parts[:len(parts)-1] {
		next, err := syscall.Openat(dir, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if err != nil {
			return nil, 0, &os.PathError{Op: "openat", Path: path, Err: err}
		}
		if owned >= 0 {
			syscall.Close(owned)
		}
		dir = next
		owned = next
	}
	fd, err := syscall.Openat(dir, parts[len(parts)-1], syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, 0, &os.PathError{Op: "openat", Path: path, Err: err}
	}
	return readRegular(os.NewFile(uintptr(fd), path), limit)
}
