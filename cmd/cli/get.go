package cli

import (
	"compiler/internal/manifest"
	"compiler/internal/packages"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GetCommand installs a package and its dependencies
func GetCommand(args []string) error {
	if len(args) == 0 {
		// No package specified, install all dependencies from fer.ret
		return installAllDependencies()
	}

	// Install specific packages
	for _, packageSpec := range args {
		if err := installPackage(packageSpec); err != nil {
			return err
		}
	}
	return nil
}

// installAllDependencies installs all packages listed in fer.ret
func installAllDependencies() error {
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
		printInfo("No dependencies to install")
		return nil
	}

	printHeader(fmt.Sprintf("Installing dependencies for %s", m.Package.Name))

	// Setup paths
	projectRoot := filepath.Dir(manifestPath)
	cachePath := filepath.Join(projectRoot, ".ferret", "modules")

	// Ensure cache directory exists
	if err := os.MkdirAll(cachePath, 0755); err != nil {
		printError(fmt.Sprintf("Cannot create cache directory: %v", err))
		return err
	}

	// Load or create lockfile
	lockfile, err := manifest.LoadLockfile(projectRoot)
	if err != nil {
		lockfile = manifest.NewLockfile()
	}

	// Resolve mock path if needed
	devConfig := m.Dev
	if devConfig.MockRemote && devConfig.MockPath != "" {
		devConfig.MockPath = filepath.Join(projectRoot, devConfig.MockPath)
	}

	// Track version constraints for each package
	constraints := make(map[string][]string)

	// Install each direct dependency
	for name, dep := range m.Dependencies {
		if dep.Type == manifest.DependencyNeighbor {
			printPackage(name, "neighbor")
			printDim(fmt.Sprintf("  Local: %s", dep.Path))
			// Neighbor packages don't need to be downloaded
			continue
		}

		if dep.Type == manifest.DependencyRemote {
			// Install package and its transitive dependencies
			if err := installPackageRecursive(cachePath, dep.Path, dep.Version, &devConfig, lockfile, constraints, true, ""); err != nil {
				printError(fmt.Sprintf("Failed to install %s: %v", dep.Path, err))
				return err
			}
		}
	}

	// Save lockfile
	if err := manifest.SaveLockfile(projectRoot, lockfile); err != nil {
		printError(fmt.Sprintf("Failed to save lockfile: %v", err))
		return err
	}

	fmt.Println()
	printSuccess("All dependencies installed successfully")
	return nil
}

// installPackageRecursive installs a package and its transitive dependencies
func installPackageRecursive(cachePath, repoPath, versionConstraint string, devConfig *manifest.DevConfig, lockfile *manifest.Lockfile, constraints map[string][]string, isDirect bool, parentPath string) error {
	// Track constraint for this package
	if !contains(constraints[repoPath], versionConstraint) {
		constraints[repoPath] = append(constraints[repoPath], versionConstraint)
	}

	// Check if already processed
	if entry, exists := lockfile.GetDependency(repoPath); exists {
		// Verify the installed version satisfies the new constraint
		matches, err := packages.MatchesConstraint(entry.Version, versionConstraint)
		if err != nil || !matches {
			return fmt.Errorf("version conflict: %s@%s already installed but does not satisfy constraint %s", 
				repoPath, entry.Version, versionConstraint)
		}

		// Already installed and compatible, just update usage tracking
		if parentPath != "" && !contains(entry.UsedBy, parentPath) {
			lockfile.AddUsedBy(repoPath, parentPath)
		}
		return nil
	}

	// Resolve version based on constraints
	availableVersions, err := packages.ListAvailableVersions(repoPath, devConfig)
	if err != nil {
		return fmt.Errorf("failed to list versions for %s: %w", repoPath, err)
	}

	version, err := packages.FindBestMatchMultipleConstraints(availableVersions, constraints[repoPath])
	if err != nil {
		printError(fmt.Sprintf("Version conflict for %s", repoPath))
		printDim(fmt.Sprintf("  Constraints: %v", constraints[repoPath]))
		return fmt.Errorf("version conflict for %s: %w (constraints: %v)", repoPath, err, constraints[repoPath])
	}

	printPackage(repoPath, version)
	if len(constraints[repoPath]) > 1 {
		printDim(fmt.Sprintf("  Resolved from: %v", constraints[repoPath]))
	}

	// Download if not cached
	if !packages.IsModuleCached(cachePath, repoPath, version) {
		printDownload(fmt.Sprintf("Downloading %s@%s...", repoPath, version))
		err := packages.DownloadRemotePackage(cachePath, repoPath, version, devConfig)
		if err != nil {
			printError(fmt.Sprintf("Failed to download: %v", err))
			return fmt.Errorf("failed to download %s: %w", repoPath, err)
		}
		printCached()
	} else {
		printCached()
	}

	// Get package path in cache
	modulePath := packages.GetModulePath(cachePath, repoPath, version)

	// Load package manifest to get its dependencies
	packageManifestPath := filepath.Join(modulePath, "fer.ret")
	packageManifest, err := manifest.LoadManifest(packageManifestPath)
	if err != nil {
		return fmt.Errorf("failed to load package manifest: %w", err)
	}

	// Collect transitive dependency names
	var transitiveDeps []string
	for _, dep := range packageManifest.Dependencies {
		if dep.Type == manifest.DependencyRemote {
			transitiveDeps = append(transitiveDeps, dep.Path)
		}
	}

	// Add to lockfile
	lockfile.SetDependency(repoPath, manifest.LockfileEntry{
		Version:      version,
		ResolvedURL:  repoPath,
		Direct:       isDirect,
		Description:  packageManifest.Package.Name,
		Dependencies: transitiveDeps,
	})

	// Mark as direct if it is
	if isDirect {
		lockfile.AddDirectDependency(repoPath)
	}

	// Track who uses this package
	if parentPath != "" {
		lockfile.AddUsedBy(repoPath, parentPath)
	}

	// Install transitive dependencies
	for _, dep := range packageManifest.Dependencies {
		if dep.Type == manifest.DependencyRemote {
			printTransitive(dep.Path, dep.Version)
			if err := installPackageRecursive(cachePath, dep.Path, dep.Version, devConfig, lockfile, constraints, false, repoPath); err != nil {
				printError(fmt.Sprintf("Failed to install transitive dependency: %v", err))
				return fmt.Errorf("failed to install transitive dependency %s: %w", dep.Path, err)
			}
		}
	}

	return nil
}

// installPackage installs a specific package (interactive add to fer.ret)
func installPackage(packageSpec string) error {
	// Parse package spec (e.g., "github.com/user/repo@v1.0.0")
	dep, err := manifest.ParseDependency(packageSpec)
	if err != nil {
		printError(fmt.Sprintf("Invalid package spec: %v", err))
		return err
	}

	// Find and load manifest
	manifestPath, err := manifest.FindManifest(".")
	if err != nil {
		printError("No fer.ret found in current directory or parents")
		return err
	}

	m, err := manifest.LoadManifest(manifestPath)
	if err != nil {
		printError(fmt.Sprintf("Failed to load manifest: %v", err))
		return err
	}

	// Determine dependency name (use last part of path for remote deps)
	depName := dep.Path
	if dep.Type == manifest.DependencyRemote {
		parts := strings.Split(dep.Path, "/")
		depName = parts[len(parts)-1]
	}

	// Check if already in manifest
	needsUpdate := false
	alreadyExists := false
	if existingDep, exists := m.Dependencies[depName]; exists {
		// Check if version constraint changed
		if existingDep.Version != dep.Version {
			needsUpdate = true
		} else {
			alreadyExists = true
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

	// Load or create lockfile
	lockfile, err := manifest.LoadLockfile(projectRoot)
	if err != nil {
		lockfile = manifest.NewLockfile()
	}

	// Resolve mock path if needed
	devConfig := m.Dev
	if devConfig.MockRemote && devConfig.MockPath != "" {
		devConfig.MockPath = filepath.Join(projectRoot, devConfig.MockPath)
	}

	printHeader(fmt.Sprintf("Installing %s", dep.Path))

	// Only install remote dependencies
	if dep.Type == manifest.DependencyNeighbor {
		printInfo(fmt.Sprintf("Skipping neighbor dependency: %s", dep.Path))
		return nil
	}

	// Install package and its dependencies
	constraints := make(map[string][]string)
	if err := installPackageRecursive(cachePath, dep.Path, dep.Version, &devConfig, lockfile, constraints, true, ""); err != nil {
		printError(fmt.Sprintf("Failed to install %s: %v", dep.Path, err))
		return err
	}

	// Save lockfile
	if err := manifest.SaveLockfile(projectRoot, lockfile); err != nil {
		printError(fmt.Sprintf("Failed to save lockfile: %v", err))
		return err
	}

	// Now save to manifest after successful installation
	if needsUpdate {
		m.Dependencies[depName] = dep
		if err := manifest.SaveManifest(manifestPath, m); err != nil {
			printError(fmt.Sprintf("Failed to save manifest: %v", err))
			return err
		}
		printSuccess(fmt.Sprintf("Updated %s to %s in fer.ret", dep.Path, dep.Version))
	} else if !alreadyExists {
		m.Dependencies[depName] = dep
		if err := manifest.SaveManifest(manifestPath, m); err != nil {
			printError(fmt.Sprintf("Failed to save manifest: %v", err))
			return err
		}
		printSuccess(fmt.Sprintf("Added %s to fer.ret", dep.Path))
	}

	printSuccess(fmt.Sprintf("Installed %s successfully", dep.Path))
	return nil
}

// contains checks if a string slice contains a value
func contains(slice []string, value string) bool {
	for _, item := range slice {
		if item == value {
			return true
		}
	}
	return false
}
