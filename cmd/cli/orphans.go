package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"compiler/internal/core/manifest"
)

func OrphansCommand(args []string) error {
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
	candidates, err := listOrphanCandidates(cachePath, lockfile)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		printSuccess("No orphaned dependencies")
		return nil
	}

	fmt.Printf("Orphaned dependencies (%d):\n", len(candidates))
	for _, candidate := range candidates {
		reason := "stale cache"
		if candidate.InLock {
			reason = "unused lockfile dependency"
		}
		fmt.Printf("  %s (%s)\n", candidate.PackageID, reason)
	}
	return nil
}
