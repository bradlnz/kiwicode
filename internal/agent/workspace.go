package agent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type workspaceReader interface {
	Read(string) ([]byte, os.FileMode, error)
	ReadLimit(string, int) ([]byte, os.FileMode, error)
	Close() error
}

func CleanPath(name string) (string, error) {
	if name == "" || len(name) > 4096 || !utf8.ValidString(name) || filepath.IsAbs(name) || strings.ContainsAny(name, "\\\x00:") {
		return "", errors.New("expected a relative workspace path")
	}
	parts := strings.Split(name, "/")
	for _, part := range parts {
		lower := strings.ToLower(part)
		if part == "" || part == "." || part == ".." {
			return "", errors.New("path traversal and empty components are not allowed")
		}
		if lower == ".git" || lower == ".ssh" || lower == ".aws" || lower == ".kube" || lower == ".gnupg" || strings.HasPrefix(lower, ".env") || strings.Contains(lower, "credentials") || strings.HasPrefix(lower, "id_rsa") || strings.HasPrefix(lower, "id_ed25519") || lower == "secrets.json" || lower == "secrets.yaml" || lower == "secrets.yml" {
			return "", errors.New("sensitive or repository-metadata path is excluded")
		}
		for _, suffix := range []string{".pem", ".key", ".p12", ".pfx", ".keystore"} {
			if strings.HasSuffix(lower, suffix) {
				return "", errors.New("credential file types are excluded")
			}
		}
		if Display(part) != part {
			return "", errors.New("control characters are not allowed in workspace paths")
		}
	}
	return filepath.ToSlash(filepath.Clean(name)), nil
}

func readRegular(file *os.File, limit int) ([]byte, os.FileMode, error) {
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, errors.New("agent files must be regular files")
	}
	if info.Size() > int64(limit) {
		return nil, 0, fmt.Errorf("file exceeds limit of %d bytes", limit)
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if len(data) > limit {
		return nil, 0, errors.New("file grew beyond the agent size limit")
	}
	return data, info.Mode().Perm(), err
}

// ReadWorkspace is also used at review time to reject stale proposed changes.
func ReadWorkspace(root, path string) ([]byte, error) {
	reader, err := openWorkspace(root)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, _, err := reader.Read(path)
	return data, err
}
