//go:build !go1.24 && !linux

package agent

import "errors"

func openWorkspace(string) (workspaceReader, error) {
	return nil, errors.New("secure agent file access requires Go 1.24 or later on this platform")
}
