package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"compiler/internal/core/manifest"
	"compiler/internal/packages"
)

func CleanupCommand(args []string) error {
	manifestPath, err := manifest.Find(".")
	if err != nil {
		return err
	}
	projectRoot := filepath.Dir(manifestPath)
	cachePath := filepath.Join(projectRoot, ".ferret", "modules")
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		printInfo("No cache directory found")
		return nil
	}

	lockfile, err := manifest.LoadLockfile(projectRoot)
	if err != nil {
		return err
	}
	orphaned := lockfile.GetUnusedDependencies()
	if len(orphaned) == 0 {
		printInfo("No unused dependencies to clean up")
		return nil
	}

	fmt.Printf("Found %d unused dependencies:\n", len(orphaned))
	for _, depName := range orphaned {
		entry, ok := lockfile.GetDependency(depName)
		if !ok {
			continue
		}
		fmt.Printf("  → Removing %s@%s\n", depName, entry.Version)
		_ = packages.DeleteModule(cachePath, depName, entry.Version)
		lockfile.RemoveDependency(depName)
	}

	if err := manifest.SaveLockfile(projectRoot, lockfile); err != nil {
		return err
	}
	printSuccess(fmt.Sprintf("Cleaned up %d unused dependencies", len(orphaned)))
	return nil
}
