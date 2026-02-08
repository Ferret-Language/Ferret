package cli

import (
	"compiler/internal/manifest"
	"compiler/internal/packages"
	"fmt"
	"path/filepath"
)

// RemoveCommand uninstalls a package and cleans up orphaned dependencies
func RemoveCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ferret remove <package-name>")
	}

	packageName := args[0]

	// Find fer.ret
	manifestPath, err := manifest.FindManifest(".")
	if err != nil {
		return fmt.Errorf("no fer.ret found in current directory or parents")
	}

	// Load manifest
	m, err := manifest.LoadManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	// Check if package exists
	dep, exists := m.Dependencies[packageName]
	if !exists {
		return fmt.Errorf("package '%s' not found in dependencies", packageName)
	}

	// Setup paths
	projectRoot := filepath.Dir(manifestPath)
	cachePath := filepath.Join(projectRoot, ".ferret", "modules")

	// Load lockfile
	lockfile, err := manifest.LoadLockfile(projectRoot)
	if err != nil {
		return fmt.Errorf("failed to load lockfile: %w", err)
	}

	fmt.Printf("Removing %s...\n", packageName)

	// For remote dependencies, track what needs to be cleaned up
	if dep.Type == manifest.DependencyRemote {
		// Get the dependency entry
		entry, exists := lockfile.GetDependency(dep.Path)
		if exists {
			// Remove from lockfile
			lockfile.RemoveDependency(dep.Path)

			// Update used_by for transitive dependencies
			if entry.Dependencies != nil {
				for _, transitiveDep := range entry.Dependencies {
					lockfile.RemoveUsedBy(transitiveDep, dep.Path)
				}
			}

			// Find and remove orphaned dependencies
			orphaned := lockfile.GetUnusedDependencies()
			for _, orphanName := range orphaned {
				orphanEntry, exists := lockfile.GetDependency(orphanName)
				if exists {
					fmt.Printf("  → Removing orphaned dependency: %s\n", orphanName)

					// Delete from cache
					if err := packages.DeleteModule(cachePath, orphanName, orphanEntry.Version); err != nil {
						fmt.Printf("    Warning: failed to delete from cache: %v\n", err)
					}

					// Remove from lockfile
					lockfile.RemoveDependency(orphanName)
				}
			}

			// Delete main package from cache
			if err := packages.DeleteModule(cachePath, dep.Path, dep.Version); err != nil {
				fmt.Printf("  Warning: failed to delete from cache: %v\n", err)
			}
		}
	}

	// Save updated lockfile
	if err := manifest.SaveLockfile(projectRoot, lockfile); err != nil {
		return fmt.Errorf("failed to save lockfile: %w", err)
	}

	// Remove from fer.ret
	if err := manifest.RemoveDependencyFromManifest(manifestPath, packageName); err != nil {
		fmt.Printf("Warning: failed to update fer.ret: %v\n", err)
		fmt.Println("Note: Please manually remove the entry from fer.ret [dependencies]")
	}

	fmt.Printf("\n✓ Removed %s\n", packageName)
	return nil
}
