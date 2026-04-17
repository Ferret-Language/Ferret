package project

import (
	"fmt"
	"os"
	"path/filepath"

	"compiler/internal/core/context"
	"compiler/internal/core/manifest"
	"compiler/internal/packages"
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

	stdlibRoot := context.FindStdlibRoot()
	manifestPath, err := manifest.Find(startDir)

	if err != nil {
		root := startDir
		if filepath.Ext(absStart) != "" {
			root = filepath.Dir(absStart)
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
		StdlibRoot:      stdlibRoot,
	}

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
			path, err := resolveRemotePackage(ws.CachePath, ws.Lockfile, alias, dep.Path, dep.Version)
			if err != nil {
				return fmt.Errorf("resolve dependency %q: %w", alias, err)
			}
			ws.Context.DependencyRoots[alias] = path
			ws.Context.DependencyRoots[dep.Path] = path
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

func resolveRemotePackage(cachePath string, lock *manifest.Lockfile, alias, repoName, versionConstraint string) (string, error) {
	if lock == nil {
		return "", fmt.Errorf("remote dependency %q is not locked in %s", repoName, manifest.LockfileName)
	}
	if packageID, ok := lock.GetDirectDependency(alias); ok {
		entry, exists := lock.GetDependency(packageID)
		if exists && entry.ResolvedURL == repoName {
			modulePath := filepath.Join(cachePath, filepath.FromSlash(packageID))
			manifestPath := filepath.Join(modulePath, manifest.FileName)
			if _, err := os.Stat(manifestPath); err == nil {
				return modulePath, nil
			}
		}
	}
	packageID, _, ok, err := selectLockedPackage(lock, repoName, versionConstraint)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("remote dependency %q is not locked in %s", repoName, manifest.LockfileName)
	}
	modulePath := filepath.Join(cachePath, filepath.FromSlash(packageID))
	manifestPath := filepath.Join(modulePath, manifest.FileName)
	if _, err := os.Stat(manifestPath); err != nil {
		return "", fmt.Errorf("cached remote package missing %s", manifestPath)
	}
	return modulePath, nil
}

func selectLockedPackage(lock *manifest.Lockfile, repoName, versionConstraint string) (string, manifest.LockfileEntry, bool, error) {
	ids := lock.FindPackageIDsByRepo(repoName)
	if len(ids) == 0 {
		return "", manifest.LockfileEntry{}, false, nil
	}
	bestID := ""
	bestEntry := manifest.LockfileEntry{}
	var bestVersion *packages.Version
	for _, id := range ids {
		entry, ok := lock.GetDependency(id)
		if !ok || entry.Version == "" {
			continue
		}
		matches, err := packages.MatchesConstraint(entry.Version, versionConstraint)
		if err != nil {
			return "", manifest.LockfileEntry{}, false, fmt.Errorf("check locked version for %q: %w", repoName, err)
		}
		if !matches {
			continue
		}
		parsed, err := packages.ParseVersion(entry.Version)
		if err != nil {
			continue
		}
		if bestVersion == nil || parsed.Compare(bestVersion) > 0 {
			bestID = id
			bestEntry = entry
			bestVersion = parsed
		}
	}
	if bestID == "" {
		return "", manifest.LockfileEntry{}, false, fmt.Errorf("remote dependency %q has no locked version satisfying %q", repoName, versionConstraint)
	}
	return bestID, bestEntry, true, nil
}
