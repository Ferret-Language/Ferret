package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"compiler/internal/core/manifest"
	"compiler/internal/packages"
)

func GetCommand(args []string) error {
	if len(args) == 0 {
		return installAllDependencies()
	}
	for _, packageSpec := range args {
		if err := installPackage(packageSpec); err != nil {
			return err
		}
	}
	return nil
}

func installAllDependencies() error {
	manifestPath, err := manifest.Find(".")
	if err != nil {
		return err
	}
	file, err := manifest.Load(manifestPath)
	if err != nil {
		return err
	}
	if len(file.Dependencies) == 0 {
		printInfo("No dependencies to install")
		return nil
	}

	projectRoot := filepath.Dir(manifestPath)
	cachePath := filepath.Join(projectRoot, ".ferret", "modules")
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		return err
	}

	lockfile, err := manifest.LoadLockfile(projectRoot)
	if err != nil {
		lockfile = manifest.NewLockfile()
	}
	lockfile.DirectDeps = map[string]string{}

	devConfig := file.Dev
	if devConfig.MockRemote && devConfig.MockPath != "" {
		devConfig.MockPath = filepath.Join(projectRoot, devConfig.MockPath)
	}

	constraints := make(map[string][]string)
	printHeader(fmt.Sprintf("Installing dependencies for %s", file.Package.Name))
	for name, dep := range file.Dependencies {
		if dep.Type == manifest.DependencyNeighbor {
			printPackage(name, "neighbor")
			printDim(fmt.Sprintf("  Local: %s", dep.Path))
			continue
		}
		if err := installPackageRecursive(cachePath, dep.Path, dep.Version, &devConfig, lockfile, constraints, name, "", map[string]bool{}); err != nil {
			return err
		}
		if resolved, ok := resolvedDirectVersion(lockfile, name); ok {
			dep.Version = resolved
			file.Dependencies[name] = dep
		}
	}
	if err := manifest.SaveLockfile(projectRoot, lockfile); err != nil {
		return err
	}
	if err := manifest.Save(manifestPath, file); err != nil {
		return err
	}
	printSuccess("All dependencies installed successfully")
	return nil
}

func installPackageRecursive(cachePath, repoPath, versionConstraint string, devConfig *manifest.DevConfig, lockfile *manifest.Lockfile, constraints map[string][]string, directAlias, parentPackageID string, processed map[string]bool) error {
	if !slices.Contains(constraints[repoPath], versionConstraint) {
		constraints[repoPath] = append(constraints[repoPath], versionConstraint)
	}

	packageID, version, found, err := findBestLockedPackageID(lockfile, repoPath, constraints[repoPath])
	if err != nil {
		return err
	}
	if !found {
		availableVersions, listErr := packages.ListAvailableVersions(repoPath, devConfig)
		if listErr != nil {
			return fmt.Errorf("list versions for %s: %w", repoPath, listErr)
		}
		version, err = packages.FindBestMatchMultipleConstraints(availableVersions, constraints[repoPath])
		if err != nil {
			return err
		}
		packageID = manifest.PackageID(repoPath, version)
	}
	printPackage(repoPath, version)
	if !packages.IsModuleCached(cachePath, repoPath, version) {
		printDownload(fmt.Sprintf("Downloading %s@%s...", repoPath, version))
		if err := packages.DownloadRemotePackage(cachePath, repoPath, version, devConfig); err != nil {
			return fmt.Errorf("download %s@%s: %w", repoPath, version, err)
		}
		printCached()
	} else {
		printCached()
	}

	modulePath := packages.GetModulePath(cachePath, repoPath, version)
	packageManifest, err := manifest.Load(filepath.Join(modulePath, manifest.FileName))
	if err != nil {
		return fmt.Errorf("load package manifest for %s: %w", repoPath, err)
	}

	transitiveDeps := make([]string, 0)
	for _, dep := range packageManifest.Dependencies {
		if dep.Type == manifest.DependencyRemote {
			transitiveDeps = append(transitiveDeps, dep.Path)
		}
	}

	entry, exists := lockfile.GetDependency(packageID)
	usedBy := []string{}
	existingDependencies := []string{}
	if exists {
		usedBy = append(usedBy, entry.UsedBy...)
		existingDependencies = append(existingDependencies, entry.Dependencies...)
	}
	newEntry := manifest.LockfileEntry{
		Version:      version,
		ResolvedURL:  repoPath,
		Direct:       directAlias != "",
		Description:  packageManifest.Package.Name,
		Dependencies: existingDependencies,
		UsedBy:       usedBy,
	}
	lockfile.SetDependency(packageID, newEntry)
	if directAlias != "" {
		lockfile.SetDirectDependency(directAlias, packageID)
	}
	if parentPackageID != "" {
		lockfile.AddUsedBy(packageID, parentPackageID)
	}
	if processed[packageID] {
		return nil
	}
	processed[packageID] = true

	resolvedTransitive := make([]string, 0)
	for _, dep := range packageManifest.Dependencies {
		if dep.Type != manifest.DependencyRemote {
			continue
		}
		printTransitive(dep.Path, dep.Version)
		childIDBefore, _, _, _ := findBestLockedPackageID(lockfile, dep.Path, []string{dep.Version})
		if err := installPackageRecursive(cachePath, dep.Path, dep.Version, devConfig, lockfile, constraints, "", packageID, processed); err != nil {
			return err
		}
		childID := childIDBefore
		if childID == "" {
			resolved, _, ok, findErr := findBestLockedPackageID(lockfile, dep.Path, []string{dep.Version})
			if findErr != nil {
				return findErr
			}
			if ok {
				childID = resolved
			}
		}
		if childID != "" && !slices.Contains(resolvedTransitive, childID) {
			resolvedTransitive = append(resolvedTransitive, childID)
		}
	}
	sort.Strings(resolvedTransitive)
	lockfile.UpdateDependencyEdges(packageID, resolvedTransitive)
	return nil
}

func installPackage(packageSpec string) error {
	dep, err := manifest.ParseDependency(packageSpec)
	if err != nil {
		return err
	}
	manifestPath, err := manifest.Find(".")
	if err != nil {
		return err
	}
	file, err := manifest.Load(manifestPath)
	if err != nil {
		return err
	}

	depName := dep.Path
	if dep.Type == manifest.DependencyRemote {
		parts := strings.Split(dep.Path, "/")
		depName = parts[len(parts)-1]
	}

	projectRoot := filepath.Dir(manifestPath)
	cachePath := filepath.Join(projectRoot, ".ferret", "modules")
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		return err
	}

	lockfile, err := manifest.LoadLockfile(projectRoot)
	if err != nil {
		lockfile = manifest.NewLockfile()
	}

	devConfig := file.Dev
	if devConfig.MockRemote && devConfig.MockPath != "" {
		devConfig.MockPath = filepath.Join(projectRoot, devConfig.MockPath)
	}

	if dep.Type == manifest.DependencyRemote {
		constraints := map[string][]string{}
		if err := installPackageRecursive(cachePath, dep.Path, dep.Version, &devConfig, lockfile, constraints, depName, "", map[string]bool{}); err != nil {
			return err
		}
		if err := manifest.SaveLockfile(projectRoot, lockfile); err != nil {
			return err
		}
		if resolved, ok := resolvedDirectVersion(lockfile, depName); ok {
			dep.Version = resolved
		}
	}

	file.Dependencies[depName] = dep
	if err := manifest.Save(manifestPath, file); err != nil {
		return err
	}
	printSuccess(fmt.Sprintf("Installed %s", dep.Path))
	return nil
}

func findBestLockedPackageID(lockfile *manifest.Lockfile, repoPath string, constraintSet []string) (string, string, bool, error) {
	if lockfile == nil {
		return "", "", false, nil
	}
	ids := lockfile.FindPackageIDsByRepo(repoPath)
	if len(ids) == 0 {
		return "", "", false, nil
	}
	bestID := ""
	bestVersion := ""
	var bestParsed *packages.Version
	for _, id := range ids {
		entry, ok := lockfile.GetDependency(id)
		if !ok || entry.Version == "" {
			continue
		}
		matchesAll := true
		for _, constraint := range constraintSet {
			matches, err := packages.MatchesConstraint(entry.Version, constraint)
			if err != nil {
				return "", "", false, fmt.Errorf("version conflict for %s: %s does not satisfy %s", repoPath, entry.Version, constraint)
			}
			if !matches {
				matchesAll = false
				break
			}
		}
		if !matchesAll {
			continue
		}
		parsed, err := packages.ParseVersion(entry.Version)
		if err != nil {
			continue
		}
		if bestParsed == nil || parsed.Compare(bestParsed) > 0 {
			bestID = id
			bestVersion = entry.Version
			bestParsed = parsed
		}
	}
	if bestID == "" {
		return "", "", false, nil
	}
	return bestID, bestVersion, true, nil
}

func resolvedDirectVersion(lockfile *manifest.Lockfile, alias string) (string, bool) {
	if lockfile == nil {
		return "", false
	}
	packageID, ok := lockfile.GetDirectDependency(alias)
	if !ok {
		return "", false
	}
	if _, version, parsed := manifest.SplitPackageID(packageID); parsed {
		return version, true
	}
	entry, ok := lockfile.GetDependency(packageID)
	if !ok || entry.Version == "" {
		return "", false
	}
	return entry.Version, true
}
