package cli

import (
	"compiler/internal/manifest"
	"compiler/internal/packages"
	"fmt"
	"os"
	"path/filepath"
)

// CleanupCommand removes unused packages from cache
func CleanupCommand(args []string) error {
	// Find fer.ret
	manifestPath, err := manifest.FindManifest(".")
	if err != nil {
		return fmt.Errorf("no fer.ret found in current directory or parents")
	}

	// Setup paths
	projectRoot := filepath.Dir(manifestPath)
	cachePath := filepath.Join(projectRoot, ".ferret", "modules")

	// Check if cache exists
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		fmt.Println("No cache directory found")
		return nil
	}

	// Load lockfile
	lockfile, err := manifest.LoadLockfile(projectRoot)
	if err != nil {
		return fmt.Errorf("failed to load lockfile: %w", err)
	}

	// Find unused dependencies
	orphaned := lockfile.GetUnusedDependencies()

	if len(orphaned) == 0 {
		fmt.Println("No unused dependencies to clean up")
		return nil
	}

	fmt.Printf("Found %d unused dependencies:\n", len(orphaned))

	// Remove each orphaned dependency
	for _, depName := range orphaned {
		entry, exists := lockfile.GetDependency(depName)
		if exists {
			fmt.Printf("  → Removing %s@%s\n", depName, entry.Version)

			// Delete from cache
			if err := packages.DeleteModule(cachePath, depName, entry.Version); err != nil {
				fmt.Printf("    Warning: failed to delete: %v\n", err)
			}

			// Remove from lockfile
			lockfile.RemoveDependency(depName)
		}
	}

	// Save updated lockfile
	if err := manifest.SaveLockfile(projectRoot, lockfile); err != nil {
		return fmt.Errorf("failed to save lockfile: %w", err)
	}

	fmt.Printf("\n✓ Cleaned up %d unused dependencies\n", len(orphaned))
	return nil
}
