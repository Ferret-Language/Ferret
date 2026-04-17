package cli

import (
	"fmt"
	"path/filepath"

	"compiler/internal/core/manifest"
)

func SniffCommand(args []string) error {
	manifestPath, err := manifest.Find(".")
	if err != nil {
		return err
	}
	file, err := manifest.Load(manifestPath)
	if err != nil {
		return err
	}
	if len(file.Dependencies) == 0 {
		printInfo("No dependencies to sniff")
		return nil
	}

	projectRoot := filepath.Dir(manifestPath)
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
	if len(plans) == 0 {
		printSuccess(fmt.Sprintf("No updates available (%d checked)", checked))
		return nil
	}

	fmt.Printf("Available updates (%d/%d):\n", len(plans), checked)
	for _, plan := range plans {
		fmt.Printf("  %s (%s): %s -> %s\n", plan.Alias, plan.RepoPath, plan.CurrentVersion, plan.TargetVersion)
	}
	return nil
}
