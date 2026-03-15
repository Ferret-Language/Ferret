package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"compiler/internal/core/manifest"
	"compiler/internal/packages"
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

	allPackages := map[string]bool{}
	for _, dep := range file.Dependencies {
		if dep.Type == manifest.DependencyRemote {
			allPackages[dep.Path] = true
		}
	}
	for key, entry := range lockfile.Dependencies {
		if !entry.Direct {
			allPackages[key] = true
		}
	}

	updated := 0
	checked := 0
	for packagePath := range allPackages {
		if len(filter) > 0 && !filter[packagePath] {
			continue
		}
		entry, exists := lockfile.GetDependency(packagePath)
		if !exists {
			continue
		}
		checked++
		currentVersion := entry.Version
		constraint := currentVersion
		if entry.Direct {
			for _, dep := range file.Dependencies {
				if dep.Type == manifest.DependencyRemote && dep.Path == packagePath {
					constraint = dep.Version
					break
				}
			}
		}

		available, err := packages.ListAvailableVersions(packagePath, &devConfig)
		if err != nil {
			printWarning(fmt.Sprintf("%s: %v", packagePath, err))
			continue
		}
		latest, err := packages.FindBestMatch(available, constraint)
		if err != nil {
			continue
		}
		currentParsed, currentErr := packages.ParseVersion(currentVersion)
		latestParsed, latestErr := packages.ParseVersion(latest)
		if currentErr != nil || latestErr != nil || latestParsed.Compare(currentParsed) <= 0 {
			continue
		}

		printUpdate(fmt.Sprintf("%s: %s → %s", packagePath, currentVersion, latest))
		if !packages.IsModuleCached(cachePath, packagePath, latest) {
			printDownload(fmt.Sprintf("Downloading %s@%s...", packagePath, latest))
			if err := packages.DownloadRemotePackage(cachePath, packagePath, latest, &devConfig); err != nil {
				printError(fmt.Sprintf("Failed to download %s: %v", packagePath, err))
				continue
			}
			printCached()
		}
		entry.Version = latest
		lockfile.SetDependency(packagePath, entry)
		updated++
	}

	if err := manifest.SaveLockfile(projectRoot, lockfile); err != nil {
		return err
	}
	if updated == 0 {
		printSuccess(fmt.Sprintf("All %d packages are up to date", checked))
		return nil
	}
	printSuccess(fmt.Sprintf("Updated %d/%d packages", updated, checked))
	return nil
}
