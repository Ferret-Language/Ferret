package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"compiler/internal/core/manifest"
)

func UpdateCommand(args []string) error {
	manifestPath, err := manifest.Find(".")
	if err != nil {
		return err
	}
	file, err := manifest.Load(manifestPath)
	if err != nil {
		return err
	}
	if len(file.Dependencies) == 0 {
		printInfo("No dependencies to update")
		return nil
	}

	projectRoot := filepath.Dir(manifestPath)
	cachePath := filepath.Join(projectRoot, ".ferret", "modules")
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		return err
	}

	lockfile, err := manifest.LoadLockfile(projectRoot)
	if err != nil {
		return err
	}

	filter := map[string]bool{}
	if len(args) > 0 {
		for _, arg := range args {
			filter[arg] = true
		}
	}

	devConfig := file.Dev
	if devConfig.MockRemote && devConfig.MockPath != "" {
		devConfig.MockPath = filepath.Join(projectRoot, devConfig.MockPath)
	}

	plans, checked, err := collectUpdatePlans(file, lockfile, &devConfig, filter)
	if err != nil {
		return err
	}

	updated := 0
	for _, plan := range plans {
		printUpdate(fmt.Sprintf("%s: %s → %s", plan.RepoPath, plan.CurrentVersion, plan.TargetVersion))
		constraints := map[string][]string{
			plan.RepoPath: []string{">" + plan.CurrentVersion, "<=" + plan.TargetVersion},
		}
		if err := installPackageRecursive(cachePath, plan.RepoPath, "latest", &devConfig, lockfile, constraints, plan.Alias, "", map[string]bool{}); err != nil {
			printError(fmt.Sprintf("Failed to update %s: %v", plan.RepoPath, err))
			continue
		}
		if dep, ok := file.Dependencies[plan.Alias]; ok {
			dep.Version = plan.TargetVersion
			file.Dependencies[plan.Alias] = dep
		}
		updated++
	}
	if updated > 0 {
		pruneUnusedDependencies(lockfile, cachePath)
	}

	if err := manifest.SaveLockfile(projectRoot, lockfile); err != nil {
		return err
	}
	if updated > 0 {
		if err := manifest.Save(manifestPath, file); err != nil {
			return err
		}
	}
	if updated == 0 {
		printSuccess(fmt.Sprintf("All %d packages are up to date", checked))
		return nil
	}
	printSuccess(fmt.Sprintf("Updated %d/%d packages", updated, checked))
	return nil
}
