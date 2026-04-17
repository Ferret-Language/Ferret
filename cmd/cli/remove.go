package cli

import (
	"fmt"
	"path/filepath"

	"compiler/internal/core/manifest"
)

func RemoveCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ferret remove <package-name>")
	}
	packageName := args[0]

	manifestPath, err := manifest.Find(".")
	if err != nil {
		return err
	}
	file, err := manifest.Load(manifestPath)
	if err != nil {
		return err
	}

	dep, exists := file.Dependencies[packageName]
	if !exists {
		return fmt.Errorf("package %q not found", packageName)
	}

	projectRoot := filepath.Dir(manifestPath)
	cachePath := filepath.Join(projectRoot, ".ferret", "modules")
	lockfile, err := manifest.LoadLockfile(projectRoot)
	if err != nil {
		return err
	}

	if dep.Type == manifest.DependencyRemote {
		lockfile.RemoveDirectDependency(packageName)
		pruneUnusedDependencies(lockfile, cachePath)
	}

	delete(file.Dependencies, packageName)
	if err := manifest.Save(manifestPath, file); err != nil {
		return err
	}
	if err := manifest.SaveLockfile(projectRoot, lockfile); err != nil {
		return err
	}
	printSuccess(fmt.Sprintf("Removed %s", packageName))
	return nil
}
