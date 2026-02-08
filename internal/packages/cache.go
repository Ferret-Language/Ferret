package packages

import (
	"fmt"
	"os"
	"path/filepath"
)

// IsModuleCached checks if a module is already in the cache
func IsModuleCached(cachePath, repoName, version string) bool {
	moduleDir := filepath.Join(cachePath, "github.com", repoName+"@"+version)
	_, err := os.Stat(moduleDir)
	return err == nil
}

// GetModulePath returns the path to a cached module
func GetModulePath(cachePath, repoName, version string) string {
	return filepath.Join(cachePath, repoName+"@"+version)
}

// DeleteModule removes a module from the cache
func DeleteModule(cachePath, repoName, version string) error {
	modulePath := GetModulePath(cachePath, repoName, version)
	if err := os.RemoveAll(modulePath); err != nil {
		return fmt.Errorf("failed to delete module from cache: %w", err)
	}
	return nil
}

// ValidateModuleCache verifies that a cached module has required files
func ValidateModuleCache(cachePath, repoName, version string) error {
	modulePath := GetModulePath(cachePath, repoName, version)

	// Check if directory exists
	info, err := os.Stat(modulePath)
	if err != nil {
		return fmt.Errorf("module not found in cache: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("cached module path is not a directory: %s", modulePath)
	}

	// Check for fer.ret manifest
	manifestPath := filepath.Join(modulePath, "fer.ret")
	if _, err := os.Stat(manifestPath); err != nil {
		return fmt.Errorf("cached module missing fer.ret: %w", err)
	}

	return nil
}
