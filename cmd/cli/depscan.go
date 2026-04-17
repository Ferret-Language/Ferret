package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"compiler/internal/core/manifest"
	"compiler/internal/packages"
)

type updatePlan struct {
	Alias          string
	RepoPath       string
	CurrentVersion string
	TargetVersion  string
}

type orphanCandidate struct {
	PackageID string
	Path      string
	InLock    bool
}

func collectUpdatePlans(file *manifest.File, lockfile *manifest.Lockfile, devConfig *manifest.DevConfig, filter map[string]bool) ([]updatePlan, int, error) {
	if file == nil {
		return nil, 0, nil
	}
	plans := make([]updatePlan, 0)
	checked := 0
	for alias, dep := range file.Dependencies {
		if dep.Type != manifest.DependencyRemote {
			continue
		}
		if len(filter) > 0 && !filter[alias] && !filter[dep.Path] {
			continue
		}
		packageID, ok := lockfile.GetDirectDependency(alias)
		if !ok {
			var err error
			packageID, _, ok, err = findBestLockedPackageID(lockfile, dep.Path, []string{dep.Version})
			if err != nil {
				return nil, checked, err
			}
			if !ok {
				continue
			}
		}
		entry, ok := lockfile.GetDependency(packageID)
		if !ok || entry.Version == "" {
			continue
		}
		checked++
		constraint := updateConstraint(dep.Version)
		available, err := packages.ListAvailableVersions(dep.Path, devConfig)
		if err != nil {
			printWarning(fmt.Sprintf("%s: %v", dep.Path, err))
			continue
		}
		target, err := packages.FindBestMatch(available, constraint)
		if err != nil {
			continue
		}
		currentParsed, currentErr := packages.ParseVersion(entry.Version)
		targetParsed, targetErr := packages.ParseVersion(target)
		if currentErr != nil || targetErr != nil || targetParsed.Compare(currentParsed) <= 0 {
			continue
		}
		plans = append(plans, updatePlan{
			Alias:          alias,
			RepoPath:       dep.Path,
			CurrentVersion: entry.Version,
			TargetVersion:  target,
		})
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].Alias < plans[j].Alias })
	return plans, checked, nil
}

func updateConstraint(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return "latest"
	}
	if isExactVersion(version) {
		// Exact resolved tags in fer.ret should still allow "ferret update".
		return "latest"
	}
	return version
}

func isExactVersion(version string) bool {
	if version == "" || version == "latest" || version == "*" {
		return false
	}
	for _, prefix := range []string{">=", "<=", ">", "<", "^", "~", "="} {
		if strings.HasPrefix(version, prefix) {
			return false
		}
	}
	_, err := packages.ParseVersion(version)
	return err == nil
}

func listOrphanCandidates(cachePath string, lockfile *manifest.Lockfile) ([]orphanCandidate, error) {
	candidates := make(map[string]orphanCandidate)

	lockOrphans := lockfile.GetUnusedDependencies()
	for _, packageID := range lockOrphans {
		candidates[packageID] = orphanCandidate{
			PackageID: packageID,
			Path:      filepath.Join(cachePath, filepath.FromSlash(packageID)),
			InLock:    true,
		}
	}

	cached, err := discoverCachedPackagePaths(cachePath)
	if err != nil {
		return nil, err
	}

	referenced := make(map[string]struct{})
	for packageID := range lockfile.Packages {
		referenced[packageID] = struct{}{}
	}
	if len(referenced) == 0 {
		for packageID := range lockfile.Dependencies {
			referenced[packageID] = struct{}{}
		}
	}

	for packageID, path := range cached {
		if _, ok := referenced[packageID]; ok {
			continue
		}
		if existing, ok := candidates[packageID]; ok {
			existing.Path = path
			candidates[packageID] = existing
			continue
		}
		candidates[packageID] = orphanCandidate{
			PackageID: packageID,
			Path:      path,
			InLock:    false,
		}
	}

	out := make([]orphanCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PackageID < out[j].PackageID })
	return out, nil
}

func discoverCachedPackagePaths(cachePath string) (map[string]string, error) {
	out := make(map[string]string)
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		return out, nil
	}
	err := filepath.WalkDir(cachePath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		manifestPath := filepath.Join(path, manifest.FileName)
		if _, statErr := os.Stat(manifestPath); statErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(cachePath, path)
		if relErr != nil {
			return relErr
		}
		packageID := filepath.ToSlash(rel)
		out[packageID] = path
		return filepath.SkipDir
	})
	return out, err
}

func pruneUnusedDependencies(lockfile *manifest.Lockfile, cachePath string) []string {
	if lockfile == nil {
		return nil
	}
	removed := make([]string, 0)
	seen := make(map[string]struct{})
	for {
		unused := lockfile.GetUnusedDependencies()
		progress := false
		for _, packageID := range unused {
			if _, ok := seen[packageID]; ok {
				continue
			}
			entry, ok := lockfile.GetDependency(packageID)
			if !ok {
				seen[packageID] = struct{}{}
				continue
			}
			repo := entry.ResolvedURL
			version := entry.Version
			if parsedRepo, parsedVersion, parsed := manifest.SplitPackageID(packageID); parsed {
				repo = parsedRepo
				version = parsedVersion
			}
			if repo != "" && version != "" {
				_ = packages.DeleteModule(cachePath, repo, version)
			}
			lockfile.RemoveDependency(packageID)
			seen[packageID] = struct{}{}
			removed = append(removed, packageID)
			progress = true
		}
		if !progress {
			break
		}
	}
	sort.Strings(removed)
	return removed
}
