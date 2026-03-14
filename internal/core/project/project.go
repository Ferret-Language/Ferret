package project

import (
	"fmt"
	"os"
	"path/filepath"

	"compiler/internal/core/context"
	"compiler/internal/core/manifest"
)

type Workspace struct {
	RootDir   string
	Manifest  *manifest.File
	Lockfile  *manifest.Lockfile
	Context   context.Config
	CachePath string
}

func Load(startPath string, extension string) (*Workspace, error) {
	absStart, err := filepath.Abs(startPath)
	if err != nil {
		return nil, err
	}
	startDir := absStart
	if filepath.Ext(absStart) != "" {
		startDir = filepath.Dir(absStart)
	}

	manifestPath, err := manifest.Find(startDir)
	if err != nil {
		root := startDir
		if filepath.Ext(absStart) != "" {
			root = filepath.Dir(absStart)
		}
		stdlibRoot, stdErr := resolveStdlibRoot(root)
		if stdErr != nil {
			return nil, stdErr
		}
		return &Workspace{
			RootDir: root,
			Context: context.Config{
				RootDir:         root,
				Extension:       extension,
				StdlibRoot:      stdlibRoot,
				DependencyRoots: map[string]string{},
			},
		}, nil
	}

	file, err := manifest.Load(manifestPath)
	if err != nil {
		return nil, err
	}
	root := filepath.Dir(manifestPath)
	lock, err := manifest.LoadLockfile(root)
	if err != nil {
		return nil, err
	}

	cfg := context.Config{
		RootDir:         root,
		Extension:       extension,
		DependencyRoots: map[string]string{},
	}
	stdlibRoot, err := resolveStdlibRoot(root)
	if err != nil {
		return nil, err
	}
	cfg.StdlibRoot = stdlibRoot

	ws := &Workspace{
		RootDir:   root,
		Manifest:  file,
		Lockfile:  lock,
		Context:   cfg,
		CachePath: filepath.Join(root, ".ferret", "modules"),
	}

	if err := ws.resolveDependencies(); err != nil {
		return nil, err
	}
	return ws, nil
}

func (ws *Workspace) resolveDependencies() error {
	if ws.Manifest == nil {
		return nil
	}
	for alias, dep := range ws.Manifest.Dependencies {
		switch dep.Type {
		case manifest.DependencyNeighbor:
			path, err := resolveNeighborPackage(ws.RootDir, dep.Path)
			if err != nil {
				return fmt.Errorf("resolve dependency %q: %w", alias, err)
			}
			ws.Context.DependencyRoots[alias] = path
		case manifest.DependencyRemote:
			path, err := resolveRemotePackage(ws.CachePath, ws.Lockfile, dep.Path, dep.Version)
			if err != nil {
				return fmt.Errorf("resolve dependency %q: %w", alias, err)
			}
			ws.Context.DependencyRoots[alias] = path
		default:
			return fmt.Errorf("resolve dependency %q: unknown dependency type", alias)
		}
	}
	return nil
}

func resolveNeighborPackage(projectRoot, neighborPath string) (string, error) {
	absPath := filepath.Join(projectRoot, filepath.Clean(neighborPath))
	absPath, err := filepath.Abs(absPath)
	if err != nil {
		return "", fmt.Errorf("resolve neighbor path: %w", err)
	}
	manifestPath, err := manifest.Find(absPath)
	if err != nil {
		return "", fmt.Errorf("neighbor package missing %s: %w", manifest.FileName, err)
	}
	return filepath.Dir(manifestPath), nil
}

func resolveRemotePackage(cachePath string, lock *manifest.Lockfile, repoName, version string) (string, error) {
	key := repoName + "@" + version
	if lock != nil && len(lock.Dependencies) > 0 {
		if _, ok := lock.Dependencies[key]; !ok {
			return "", fmt.Errorf("remote dependency %q is not locked in %s", key, manifest.LockfileName)
		}
	}
	modulePath := filepath.Join(cachePath, filepath.FromSlash(key))
	manifestPath := filepath.Join(modulePath, manifest.FileName)
	if _, err := os.Stat(manifestPath); err != nil {
		return "", fmt.Errorf("cached remote package missing %s", manifestPath)
	}
	return modulePath, nil
}

// resolveStdlibRoot finds the stdlib root directory.
//
// Search order:
//  1. Executable-relative: the ferret binary lives in bin/, so stdlib is at
//     <execDir>/../libs/std  (the canonical installed layout).
//  2. Walk up from projectRoot looking for libs/std or ferret_libs_dev/std,
//     which covers development checkouts where the repo contains the stdlib.
func resolveStdlibRoot(projectRoot string) (string, error) {
	var candidates []string

	// 1. Relative to the ferret executable.
	//    Layout: <root>/bin/ferret  →  <root>/libs/std
	if execPath, err := os.Executable(); err == nil {
		execDir := filepath.Dir(execPath)
		candidates = append(candidates,
			filepath.Join(execDir, "..", "libs", "std"),
			filepath.Join(execDir, "libs", "std"),
		)
	}

	// 2. Walk up from the project root (development / monorepo layout).
	stdNames := []string{"libs", "ferret_libs_dev", "ferret_libs"}
	for current := projectRoot; ; current = filepath.Dir(current) {
		for _, name := range stdNames {
			candidates = append(candidates, filepath.Join(current, name, "std"))
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}

	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		clean := filepath.Clean(candidate)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		if info, err := os.Stat(clean); err == nil && info.IsDir() {
			return clean, nil
		}
	}
	return "", nil
}
