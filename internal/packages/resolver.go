package packages

import (
	"fmt"
	"path/filepath"

	"compiler/internal/manifest"
)

// ResolveNeighborPackage resolves a neighbor package path (../) to its absolute path
func ResolveNeighborPackage(projectRoot, neighborPath string) (string, error) {
	// Resolve relative path from project root
	absPath := filepath.Join(projectRoot, neighborPath)
	absPath, err := filepath.Abs(absPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve neighbor path: %w", err)
	}

	// Verify neighbor package has fer.ret
	manifestPath, err := manifest.FindManifest(absPath)
	if err != nil {
		return "", fmt.Errorf("neighbor package missing fer.ret: %w", err)
	}

	// Return the directory containing fer.ret
	return filepath.Dir(manifestPath), nil
}

// ResolveRemotePackage resolves a remote package from cache
func ResolveRemotePackage(cachePath, repoName, version string) (string, error) {
	modulePath := GetModulePath(cachePath, repoName, version)
	
	// Verify module exists and is valid
	if err := ValidateModuleCache(cachePath, repoName, version); err != nil {
		return "", err
	}

	return modulePath, nil
}

// ResolveDependency resolves a dependency from the manifest
// Returns the resolved path and dependency type (neighbor or remote)
func ResolveDependency(projectRoot, cachePath string, dep manifest.Dependency) (string, manifest.DependencyType, error) {
	switch dep.Type {
	case manifest.DependencyNeighbor:
		path, err := ResolveNeighborPackage(projectRoot, dep.Path)
		return path, manifest.DependencyNeighbor, err

	case manifest.DependencyRemote:
		// Extract repo name from path (assumes github.com/user/repo format)
		path, err := ResolveRemotePackage(cachePath, dep.Path, dep.Version)
		return path, manifest.DependencyRemote, err

	default:
		return "", dep.Type, fmt.Errorf("unknown dependency type: %d", dep.Type)
	}
}
