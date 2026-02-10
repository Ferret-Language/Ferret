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

// LockfileEntry represents a single dependency in the lockfile
type LockfileEntry struct {
	Version      string   `json:"version"`                // e.g., "v1.0.0"
	ResolvedURL  string   `json:"resolved_url,omitempty"` // Download URL (for remote)
	Checksum     string   `json:"checksum,omitempty"`     // SHA256 checksum
	Direct       bool     `json:"direct"`                 // Is this a direct dependency?
	Description  string   `json:"description,omitempty"`  // Optional description
	Dependencies []string `json:"dependencies,omitempty"` // Dependencies of this package
	UsedBy       []string `json:"used_by,omitempty"`      // Which packages depend on this
	DownloadedAt string   `json:"downloaded_at,omitempty"`
}

// Lockfile represents the ferret.lock file
type Lockfile struct {
	Version      string                   `json:"version"`      // Lockfile format version
	DirectDeps   []string                 `json:"direct_deps"`  // List of direct dependencies
	Dependencies map[string]LockfileEntry `json:"dependencies"` // package@version -> info
	GeneratedAt  string                   `json:"generated_at"`
}

// NewLockfile creates a new empty lockfile
func NewLockfile() *Lockfile {
	return &Lockfile{
		Version:      "1.0",
		DirectDeps:   []string{},
		Dependencies: make(map[string]LockfileEntry),
		GeneratedAt:  time.Now().Format(time.RFC3339),
	}
}

// LoadLockfile loads a lockfile from disk
func LoadLockfile(projectRoot string) (*Lockfile, error) {
	lockfilePath := filepath.Join(projectRoot, LockfileName)

	data, err := os.ReadFile(lockfilePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create new lockfile if it doesn't exist
			return NewLockfile(), nil
		}
		return nil, fmt.Errorf("failed to read lockfile: %w", err)
	}

	var lockfile Lockfile
	if err := json.Unmarshal(data, &lockfile); err != nil {
		return nil, fmt.Errorf("failed to parse lockfile: %w", err)
	}

	return &lockfile, nil
}

// SaveLockfile saves the lockfile to disk (sorted for deterministic output)
func SaveLockfile(projectRoot string, lockfile *Lockfile) error {
	lockfilePath := filepath.Join(projectRoot, LockfileName)

	// Update generated timestamp
	lockfile.GeneratedAt = time.Now().Format(time.RFC3339)

	// Sort dependencies for deterministic output
	sortedDeps := make([]string, 0, len(lockfile.Dependencies))
	for dep := range lockfile.Dependencies {
		sortedDeps = append(sortedDeps, dep)
	}
	sort.Strings(sortedDeps)

	// Create sorted lockfile for output
	outputLockfile := &Lockfile{
		Version:      lockfile.Version,
		DirectDeps:   lockfile.DirectDeps,
		Dependencies: make(map[string]LockfileEntry),
		GeneratedAt:  lockfile.GeneratedAt,
	}

	for _, dep := range sortedDeps {
		outputLockfile.Dependencies[dep] = lockfile.Dependencies[dep]
	}

	// Marshal to JSON with indentation
	data, err := json.MarshalIndent(outputLockfile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal lockfile: %w", err)
	}

	// Write to file
	if err := os.WriteFile(lockfilePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write lockfile: %w", err)
	}

	return nil
}

// SetDependency adds or updates a dependency in the lockfile
func (l *Lockfile) SetDependency(key string, entry LockfileEntry) {
	l.Dependencies[key] = entry
}

// GetDependency retrieves a dependency from the lockfile
func (l *Lockfile) GetDependency(key string) (LockfileEntry, bool) {
	entry, exists := l.Dependencies[key]
	return entry, exists
}

// RemoveDependency removes a dependency from the lockfile
func (l *Lockfile) RemoveDependency(key string) {
	delete(l.Dependencies, key)

	// Remove from direct deps list if present
	for i, dep := range l.DirectDeps {
		if dep == key {
			l.DirectDeps = append(l.DirectDeps[:i], l.DirectDeps[i+1:]...)
			break
		}
	}
}

// AddUsedBy adds a parent to the UsedBy list for a dependency
func (l *Lockfile) AddUsedBy(depKey, parentKey string) {
	entry, exists := l.Dependencies[depKey]
	if !exists {
		return
	}

	// Check if already present
	for _, u := range entry.UsedBy {
		if u == parentKey {
			return
		}
	}

	entry.UsedBy = append(entry.UsedBy, parentKey)
	l.Dependencies[depKey] = entry
}

// RemoveUsedBy removes a parent from the UsedBy list for a dependency
func (l *Lockfile) RemoveUsedBy(depKey, parentKey string) {
	entry, exists := l.Dependencies[depKey]
	if !exists {
		return
	}

	newUsedBy := make([]string, 0, len(entry.UsedBy))
	for _, u := range entry.UsedBy {
		if u != parentKey {
			newUsedBy = append(newUsedBy, u)
		}
	}

	entry.UsedBy = newUsedBy
	l.Dependencies[depKey] = entry
}

// AddDirectDependency marks a dependency as direct
func (l *Lockfile) AddDirectDependency(key string) {
	// Check if already in list
	for _, dep := range l.DirectDeps {
		if dep == key {
			return
		}
	}
	l.DirectDeps = append(l.DirectDeps, key)
}

// GetUnusedDependencies returns dependencies that are not direct and have no users
func (l *Lockfile) GetUnusedDependencies() []string {
	var unused []string
	for key, entry := range l.Dependencies {
		if !entry.Direct && len(entry.UsedBy) == 0 {
			unused = append(unused, key)
		}
	}
	return unused
}
