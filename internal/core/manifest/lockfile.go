package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const LockfileName = "ferret.lock"

type LockfileEntry struct {
	Version     string `json:"version"`
	ResolvedURL string `json:"resolved_url,omitempty"`
	Checksum    string `json:"checksum,omitempty"`
}

type Lockfile struct {
	Version      string                   `json:"version"`
	Dependencies map[string]LockfileEntry `json:"dependencies"`
}

func LoadLockfile(projectRoot string) (*Lockfile, error) {
	path := filepath.Join(projectRoot, LockfileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Lockfile{Version: "1.0", Dependencies: map[string]LockfileEntry{}}, nil
		}
		return nil, fmt.Errorf("read lockfile: %w", err)
	}
	var lock Lockfile
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("parse lockfile: %w", err)
	}
	if lock.Dependencies == nil {
		lock.Dependencies = make(map[string]LockfileEntry)
	}
	return &lock, nil
}
