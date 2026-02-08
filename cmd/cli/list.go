package cli

import (
	"compiler/internal/manifest"
	"fmt"
	"path/filepath"
)

// ListCommand shows all dependencies
func ListCommand(args []string) error {
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

	// Load lockfile
	projectRoot := filepath.Dir(manifestPath)
	lockfile, err := manifest.LoadLockfile(projectRoot)
	hasLockfile := err == nil

	fmt.Printf("%s v%s\n", m.Package.Name, m.Package.Version)

	if len(m.Dependencies) == 0 {
		fmt.Println("\nNo dependencies")
		return nil
	}

	fmt.Printf("\nDependencies (%d):\n", len(m.Dependencies))

	// Show direct dependencies
	for name, dep := range m.Dependencies {
		if dep.Type == manifest.DependencyNeighbor {
			fmt.Printf("  %s (neighbor)\n", name)
			fmt.Printf("    Path: %s\n", dep.Path)
		} else if dep.Type == manifest.DependencyRemote {
			fmt.Printf("  %s (remote)\n", name)
			fmt.Printf("    URL: %s\n", dep.Path)
			fmt.Printf("    Constraint: %s\n", dep.Version)

			// Show locked version if available
			if hasLockfile {
				if entry, exists := lockfile.GetDependency(dep.Path); exists {
					fmt.Printf("    Locked: %s\n", entry.Version)
				}
			}
		}
	}

	// Show transitive dependencies if lockfile exists
	if hasLockfile {
		transitiveCount := 0
		for _, entry := range lockfile.Dependencies {
			if !entry.Direct {
				transitiveCount++
			}
		}

		if transitiveCount > 0 {
			fmt.Printf("\nTransitive dependencies (%d):\n", transitiveCount)
			for depName, entry := range lockfile.Dependencies {
				if !entry.Direct {
					fmt.Printf("  %s @ %s\n", depName, entry.Version)
					if len(entry.UsedBy) > 0 {
						fmt.Printf("    Used by: %v\n", entry.UsedBy)
					}
				}
			}
		}
	}

	return nil
}
