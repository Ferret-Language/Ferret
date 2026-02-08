package cli

import (
	"compiler/internal/manifest"
	"compiler/internal/packages"
	"fmt"
	"os"
	"path/filepath"
)

// UpdateCommand updates packages to latest compatible versions
func UpdateCommand(args []string) error {
	// Find fer.ret in current directory or parent
	manifestPath, err := manifest.FindManifest(".")
	if err != nil {
		printError("No fer.ret found in current directory or parents")
		return err
	}

	// Load manifest
	m, err := manifest.LoadManifest(manifestPath)
	if err != nil {
		printError(fmt.Sprintf("Failed to load manifest: %v", err))
		return err
	}

	if len(m.Dependencies) == 0 {
		printInfo("No dependencies to update")
		return nil
	}

	// Build filter set if specific packages requested
	var packageFilter map[string]bool
	if len(args) > 0 {
		packageFilter = make(map[string]bool)
		for _, pkg := range args {
			packageFilter[pkg] = true
		}
	}

	// Setup paths
	projectRoot := filepath.Dir(manifestPath)
	cachePath := filepath.Join(projectRoot, ".ferret", "modules")

	// Ensure cache directory exists
	if err := os.MkdirAll(cachePath, 0755); err != nil {
		printError(fmt.Sprintf("Cannot create cache directory: %v", err))
		return err
	}

	// Load lockfile
	lockfile, err := manifest.LoadLockfile(projectRoot)
	if err != nil {
		printError("No lockfile found. Run 'ferret get' first")
		return err
	}

	// Resolve mock path if needed
	devConfig := m.Dev
	if devConfig.MockRemote && devConfig.MockPath != "" {
		devConfig.MockPath = filepath.Join(projectRoot, devConfig.MockPath)
	}

	printHeader(fmt.Sprintf("Checking updates for %s", m.Package.Name))

	updated := 0
	checked := 0
	allPackages := make(map[string]bool) // Track all packages to update

	// Collect all direct dependencies
	for name, dep := range m.Dependencies {
		if dep.Type == manifest.DependencyRemote {
			allPackages[dep.Path] = true
		}
		_ = name // Avoid unused variable warning
	}

	// Collect all transitive dependencies from lockfile
	for path, entry := range lockfile.Dependencies {
		if !entry.Direct {
			allPackages[path] = true
		}
	}

	// Check each package for updates
	for packagePath := range allPackages {
		// Skip if not in filter (when filter specified)
		if packageFilter != nil && !packageFilter[packagePath] {
			continue
		}

		entry, exists := lockfile.GetDependency(packagePath)
		if !exists {
			continue
		}

		checked++
		currentVersion := entry.Version

		// Get constraint from manifest (for direct deps) or from lockfile
		var constraint string
		isDirect := entry.Direct
		
		if isDirect {
			// Find constraint in manifest
			for _, dep := range m.Dependencies {
				if dep.Path == packagePath {
					constraint = dep.Version
					break
				}
			}
		} else {
			// For transitive deps, use the existing version as constraint
			// This allows patch/minor updates based on parent constraints
			constraint = currentVersion
		}

		if constraint == "" {
			continue
		}

		// List available versions
		availableVersions, err := packages.ListAvailableVersions(packagePath, &devConfig)
		if err != nil {
			printWarning(fmt.Sprintf("%s: failed to list versions", packagePath))
			continue
		}

		// Find latest version matching constraint
		latestVersion, err := packages.FindBestMatch(availableVersions, constraint)
		if err != nil {
			continue
		}

		// Compare versions
		current, err := packages.ParseVersion(currentVersion)
		if err != nil {
			continue
		}

		latest, err := packages.ParseVersion(latestVersion)
		if err != nil {
			continue
		}

		if latest.Compare(current) > 0 {
			printUpdate(fmt.Sprintf("%s: %s → %s", packagePath, currentVersion, latestVersion))

			// Download new version
			if !packages.IsModuleCached(cachePath, packagePath, latestVersion) {
				printDownload(fmt.Sprintf("Downloading %s@%s...", packagePath, latestVersion))
				err := packages.DownloadRemotePackage(cachePath, packagePath, latestVersion, &devConfig)
				if err != nil {
					printError(fmt.Sprintf("Failed to download: %v", err))
					continue
				}
				printCached()
			} else {
				printCached()
			}

			// Update lockfile
			entry.Version = latestVersion
			lockfile.SetDependency(packagePath, entry)
			updated++
		}
	}

	// Save updated lockfile
	if updated > 0 {
		if err := manifest.SaveLockfile(projectRoot, lockfile); err != nil {
			printError(fmt.Sprintf("Failed to save lockfile: %v", err))
			return err
		}
		fmt.Println()
		printSuccess(fmt.Sprintf("Updated %d/%d packages", updated, checked))
	} else {
		fmt.Println()
		printSuccess(fmt.Sprintf("All %d packages are up to date", checked))
	}

	return nil
}
