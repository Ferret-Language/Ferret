package packages

import (
	"fmt"
	"os"
	"path/filepath"

	"compiler/internal/manifest"
)

// Manager handles dependency management for a project
type Manager struct {
	projectRoot string
	manifest    *manifest.PackageManifest
	lockfile    *manifest.Lockfile
}

// NewManager creates a new package manager for the given project
func NewManager(projectRoot string) (*Manager, error) {
	// Find and load manifest
	manifestPath, err := manifest.FindManifest(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("no fer.ret found: %w", err)
	}

	m, err := manifest.LoadManifest(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load manifest: %w", err)
	}

	// Load or create lockfile
	lockfile, err := manifest.LoadLockfile(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to load lockfile: %w", err)
	}

	return &Manager{
		projectRoot: filepath.Dir(manifestPath),
		manifest:    m,
		lockfile:    lockfile,
	}, nil
}

// GetCachePath returns the path to the package cache
func (m *Manager) GetCachePath() string {
	return filepath.Join(m.projectRoot, ".ferret", "modules")
}

// EnsureCacheDir creates the cache directory if it doesn't exist
func (m *Manager) EnsureCacheDir() error {
	cachePath := m.GetCachePath()
	return os.MkdirAll(cachePath, 0755)
}

// SaveLockfile saves the current lockfile to disk
func (m *Manager) SaveLockfile() error {
	return manifest.SaveLockfile(m.projectRoot, m.lockfile)
}

// GetManifest returns the package manifest
func (m *Manager) GetManifest() *manifest.PackageManifest {
	return m.manifest
}

// GetLockfile returns the lockfile
func (m *Manager) GetLockfile() *manifest.Lockfile {
	return m.lockfile
}
