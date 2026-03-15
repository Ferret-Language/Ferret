package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const LockfileName = "ferret.lock"

type LockfileEntry struct {
	Version      string   `json:"version"`
	ResolvedURL  string   `json:"resolved_url,omitempty"`
	Checksum     string   `json:"checksum,omitempty"`
	Direct       bool     `json:"direct,omitempty"`
	Description  string   `json:"description,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	UsedBy       []string `json:"used_by,omitempty"`
	DownloadedAt string   `json:"downloaded_at,omitempty"`
}

type Lockfile struct {
	Version      string                   `json:"version"`
	DirectDeps   []string                 `json:"direct_deps,omitempty"`
	Dependencies map[string]LockfileEntry `json:"dependencies"`
	GeneratedAt  string                   `json:"generated_at,omitempty"`
}

func NewLockfile() *Lockfile {
	return &Lockfile{
		Version:      "1.0",
		DirectDeps:   []string{},
		Dependencies: map[string]LockfileEntry{},
		GeneratedAt:  time.Now().Format(time.RFC3339),
	}
}

func LoadLockfile(projectRoot string) (*Lockfile, error) {
	path := filepath.Join(projectRoot, LockfileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewLockfile(), nil
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
	if lock.DirectDeps == nil {
		lock.DirectDeps = []string{}
	}
	return &lock, nil
}

func SaveLockfile(projectRoot string, lock *Lockfile) error {
	if lock == nil {
		return fmt.Errorf("nil lockfile")
	}
	if lock.Dependencies == nil {
		lock.Dependencies = make(map[string]LockfileEntry)
	}
	lock.GeneratedAt = time.Now().Format(time.RFC3339)

	keys := make([]string, 0, len(lock.Dependencies))
	for key := range lock.Dependencies {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	sorted := make(map[string]LockfileEntry, len(lock.Dependencies))
	for _, key := range keys {
		sorted[key] = lock.Dependencies[key]
	}

	out := &Lockfile{
		Version:      lock.Version,
		DirectDeps:   append([]string(nil), lock.DirectDeps...),
		Dependencies: sorted,
		GeneratedAt:  lock.GeneratedAt,
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal lockfile: %w", err)
	}
	path := filepath.Join(projectRoot, LockfileName)
	return os.WriteFile(path, data, 0o644)
}

func (l *Lockfile) SetDependency(key string, entry LockfileEntry) {
	if l == nil {
		return
	}
	if l.Dependencies == nil {
		l.Dependencies = make(map[string]LockfileEntry)
	}
	l.Dependencies[key] = entry
}

func (l *Lockfile) GetDependency(key string) (LockfileEntry, bool) {
	if l == nil {
		return LockfileEntry{}, false
	}
	entry, ok := l.Dependencies[key]
	return entry, ok
}

func (l *Lockfile) RemoveDependency(key string) {
	if l == nil || l.Dependencies == nil {
		return
	}
	delete(l.Dependencies, key)
	for index, dep := range l.DirectDeps {
		if dep == key {
			l.DirectDeps = append(l.DirectDeps[:index], l.DirectDeps[index+1:]...)
			break
		}
	}
}

func (l *Lockfile) AddDirectDependency(key string) {
	if l == nil {
		return
	}
	for _, dep := range l.DirectDeps {
		if dep == key {
			return
		}
	}
	l.DirectDeps = append(l.DirectDeps, key)
}

func (l *Lockfile) AddUsedBy(depKey, parentKey string) {
	entry, ok := l.GetDependency(depKey)
	if !ok {
		return
	}
	for _, usedBy := range entry.UsedBy {
		if usedBy == parentKey {
			return
		}
	}
	entry.UsedBy = append(entry.UsedBy, parentKey)
	l.SetDependency(depKey, entry)
}

func (l *Lockfile) RemoveUsedBy(depKey, parentKey string) {
	entry, ok := l.GetDependency(depKey)
	if !ok {
		return
	}
	filtered := make([]string, 0, len(entry.UsedBy))
	for _, usedBy := range entry.UsedBy {
		if usedBy != parentKey {
			filtered = append(filtered, usedBy)
		}
	}
	entry.UsedBy = filtered
	l.SetDependency(depKey, entry)
}

func (l *Lockfile) GetUnusedDependencies() []string {
	if l == nil {
		return nil
	}
	unused := make([]string, 0)
	for key, entry := range l.Dependencies {
		if !entry.Direct && len(entry.UsedBy) == 0 {
			unused = append(unused, key)
		}
	}
	sort.Strings(unused)
	return unused
}
