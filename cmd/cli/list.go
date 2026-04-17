package cli

import (
	"fmt"
	"path/filepath"

	"compiler/internal/core/manifest"
)

func ListCommand(args []string) error {
	manifestPath, err := manifest.Find(".")
	if err != nil {
		return err
	}
	file, err := manifest.Load(manifestPath)
	if err != nil {
		return err
	}

	projectRoot := filepath.Dir(manifestPath)
	lockfile, lockErr := manifest.LoadLockfile(projectRoot)
	hasLockfile := lockErr == nil

	fmt.Printf("%s v%s\n", file.Package.Name, file.Package.Version)
	if len(file.Dependencies) == 0 {
		fmt.Println("\nNo dependencies")
		return nil
	}

	fmt.Printf("\nDependencies (%d):\n", len(file.Dependencies))
	for name, dep := range file.Dependencies {
		switch dep.Type {
		case manifest.DependencyNeighbor:
			fmt.Printf("  %s (neighbor)\n", name)
			fmt.Printf("    Path: %s\n", dep.Path)
		case manifest.DependencyRemote:
			fmt.Printf("  %s (remote)\n", name)
			fmt.Printf("    URL: %s\n", dep.Path)
			fmt.Printf("    Constraint: %s\n", dep.Version)
			if hasLockfile {
				if packageID, ok := lockfile.GetDirectDependency(name); ok {
					if entry, found := lockfile.GetDependency(packageID); found {
						fmt.Printf("    Locked: %s (%s)\n", entry.Version, packageID)
					}
				}
			}
		}
	}

	if hasLockfile {
		transitiveCount := 0
		entries := lockfile.Packages
		if len(entries) == 0 {
			entries = lockfile.Dependencies
		}
		for _, entry := range entries {
			if !entry.Direct {
				transitiveCount++
			}
		}
		if transitiveCount > 0 {
			fmt.Printf("\nTransitive dependencies (%d):\n", transitiveCount)
			for depName, entry := range entries {
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
