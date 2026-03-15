package cli

import (
	"fmt"
	"os"
	"path/filepath"
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
		if err := installPackageRecursive(cachePath, dep.Path, dep.Version, &devConfig, lockfile, constraints, true, ""); err != nil {
			return err
		}
	}
	if err := manifest.SaveLockfile(projectRoot, lockfile); err != nil {
		return err
	}
	printSuccess("All dependencies installed successfully")
	return nil
}

func installPackageRecursive(cachePath, repoPath, versionConstraint string, devConfig *manifest.DevConfig, lockfile *manifest.Lockfile, constraints map[string][]string, isDirect bool, parentPath string) error {
	if !contains(constraints[repoPath], versionConstraint) {
		constraints[repoPath] = append(constraints[repoPath], versionConstraint)
	}

	if entry, exists := lockfile.GetDependency(repoPath); exists {
		matches, err := packages.MatchesConstraint(entry.Version, versionConstraint)
		if err != nil || !matches {
			return fmt.Errorf("version conflict for %s: %s does not satisfy %s", repoPath, entry.Version, versionConstraint)
		}
		if parentPath != "" && !contains(entry.UsedBy, parentPath) {
			lockfile.AddUsedBy(repoPath, parentPath)
		}
		return nil
	}

	availableVersions, err := packages.ListAvailableVersions(repoPath, devConfig)
	if err != nil {
		return fmt.Errorf("list versions for %s: %w", repoPath, err)
	}
	version, err := packages.FindBestMatchMultipleConstraints(availableVersions, constraints[repoPath])
	if err != nil {
		return err
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

	lockfile.SetDependency(repoPath, manifest.LockfileEntry{
		Version:      version,
		ResolvedURL:  repoPath,
		Direct:       isDirect,
		Description:  packageManifest.Package.Name,
		Dependencies: transitiveDeps,
	})
	if isDirect {
		lockfile.AddDirectDependency(repoPath)
	}
	if parentPath != "" {
		lockfile.AddUsedBy(repoPath, parentPath)
	}

	for _, dep := range packageManifest.Dependencies {
		if dep.Type != manifest.DependencyRemote {
			continue
		}
		printTransitive(dep.Path, dep.Version)
		if err := installPackageRecursive(cachePath, dep.Path, dep.Version, devConfig, lockfile, constraints, false, repoPath); err != nil {
			return err
		}
	}
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
		if err := installPackageRecursive(cachePath, dep.Path, dep.Version, &devConfig, lockfile, constraints, true, ""); err != nil {
			return err
		}
		if err := manifest.SaveLockfile(projectRoot, lockfile); err != nil {
			return err
		}
	}

	file.Dependencies[depName] = dep
	if err := manifest.Save(manifestPath, file); err != nil {
		return err
	}
	printSuccess(fmt.Sprintf("Installed %s", dep.Path))
	return nil
}
