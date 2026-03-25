package project

import (
	"fmt"
	"os"
	"path/filepath"

	"compiler/internal/core/context"
	"compiler/internal/core/manifest"
)

var ExecutablePath = os.Executable

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

// resolveStdlibRoot finds the packaged stdlib beside the compiler binary.
//
// Layout: <bundle>/bin/ferret -> <bundle>/libs/std
func resolveStdlibRoot(projectRoot string) (string, error) {
	if projectRoot != "" {
		candidate := filepath.Clean(filepath.Join(projectRoot, "ferret_libs_dev", "std"))
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
	}
	if execPath, err := ExecutablePath(); err == nil {
		execDir := filepath.Dir(execPath)
		candidate := filepath.Clean(filepath.Join(execDir, "..", "libs", "std"))
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
	}
	return "", nil
}
