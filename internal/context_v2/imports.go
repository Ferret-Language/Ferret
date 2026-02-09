package context_v2

import (
	"fmt"
	"path/filepath"
	"strings"

	"compiler/internal/manifest"
	"compiler/internal/packages"
	"compiler/internal/utils/fs"
)

// ImportPathToFilePath converts an import path to a file path.
// Returns the specific file path for file-based imports.
// Resolution priority:
// 1. Stdlib (embedded or libs)
// 2. Current package (local project files)
// 3. Neighbor packages (from fer.ret dependencies with ../ paths)
// 4. Remote packages (from cache)
func (ctx *CompilerContext) ImportPathToFilePath(importPath string) (string, ModuleType, error) {
	// Normalize import path to ensure consistent lookup
	importPath = fs.NormalizePath(importPath)

	// 1. Check if module already exists (embedded stdlib or previously loaded)
	if mod, exists := ctx.GetModule(importPath); exists {
		if mod.Content != "" || mod.FilePath != "" {
			return mod.FilePath, mod.Type, nil
		}
	}

	// 2. Check stdlib/runtime on disk (libs directory)
	if ctx.Config.RuntimePath != "" {
		relPath := filepath.FromSlash(importPath) + ctx.Config.Extension
		filePath := filepath.Join(ctx.Config.RuntimePath, relPath)

		if fs.IsValidFile(filePath) {
			return filepath.ToSlash(filePath), ModuleBuiltin, nil
		}
	}

	// 3. Check if it's a local project module
	packageName := fs.FirstPart(importPath)

	if packageName == ctx.Config.ProjectName && ctx.Config.ProjectName != "" {
		cleanPath := strings.TrimPrefix(importPath, packageName+"/")

		// If cleanPath equals importPath, it means there was no "/" after packageName
		if cleanPath == importPath {
			cleanPath = ""
		}

		// Try as file
		var filePath string
		if cleanPath == "" {
			// Root module: projectRoot/projectName.fer
			filePath = filepath.Join(ctx.Config.ProjectRoot, packageName+ctx.Config.Extension)
		} else {
			filePath = filepath.Join(ctx.Config.ProjectRoot, cleanPath+ctx.Config.Extension)
		}

		if fs.IsValidFile(filePath) {
			return filepath.ToSlash(filePath), ModuleLocal, nil
		}

		// Try relative to entry file's directory
		if ctx.EntryPoint != "" {
			entryDir := filepath.Dir(ctx.EntryPoint)

			if cleanPath == "" {
				filePath = filepath.Join(entryDir, packageName+ctx.Config.Extension)
			} else {
				filePath = filepath.Join(entryDir, cleanPath+ctx.Config.Extension)
			}

			if fs.IsValidFile(filePath) {
				return filepath.ToSlash(filePath), ModuleLocal, nil
			}
		}
	}

	// For non-stdlib/builtin imports, manifest is required
	// If we reach here and there's no manifest, it means the import is not:
	// - Already loaded (step 1)
	// - A builtin module from RuntimePath (step 2)
	// - A local project module (step 3)
	// The only exception is "global" which is implicitly loaded
	if ctx.Manifest == nil && importPath != "global" {
		return "", ModuleUnknown, fmt.Errorf(
			"manifest file (fer.ret) is required for non-stdlib imports.\n\nImport: %s\n\nTo fix this issue:\n  1. Run: ferret init\n  2. Add dependencies to fer.ret\n  3. Run: ferret get",
			importPath,
		)
	}

	// 4. Check neighbor packages (from fer.ret dependencies)
	if ctx.Manifest != nil && len(ctx.Manifest.Dependencies) > 0 {
		// Try to resolve from dependencies
		filePath, moduleType, err := ctx.resolveFromDependencies(importPath)
		if err == nil {
			return filePath, moduleType, nil
		}
	}

	// 5. Check if it's a remote module (future implementation)
	if ctx.isRemoteImport(importPath) {
		// TODO: Implement remote module resolution from cache
		return "", ModuleRemote, fmt.Errorf("remote imports not yet implemented: %s", importPath)
	}

	return "", ModuleUnknown, fmt.Errorf("cannot resolve import: %s", importPath)
}

// resolveFromDependencies tries to resolve an import from manifest dependencies
func (ctx *CompilerContext) resolveFromDependencies(importPath string) (string, ModuleType, error) {
	// Extract package name from import path (first part)
	parts := strings.Split(importPath, "/")
	if len(parts) == 0 {
		return "", ModuleUnknown, fmt.Errorf("invalid import path: %s", importPath)
	}

	packageName := parts[0]

	// Look for dependency with matching package name
	for depName, dep := range ctx.Manifest.Dependencies {
		if dep.Type == manifest.DependencyNeighbor {
			// Resolve neighbor package path
			neighborPath, err := packages.ResolveNeighborPackage(ctx.Config.ProjectRoot, dep.Path)
			if err != nil {
				continue
			}

			// Load neighbor manifest to get its package name
			neighborManifestPath := filepath.Join(neighborPath, "fer.ret")
			neighborManifest, err := manifest.LoadManifest(neighborManifestPath)
			if err != nil {
				continue
			}

			// Check if package name matches
			if neighborManifest.Package.Name == packageName {
				// Build file path within neighbor package
				// Remove package name from import path
				relativePath := strings.TrimPrefix(importPath, packageName+"/")
				if relativePath == importPath {
					relativePath = ""
				}

				var filePath string
				if relativePath == "" {
					// Root module of neighbor package
					filePath = filepath.Join(neighborPath, packageName+ctx.Config.Extension)
				} else {
					filePath = filepath.Join(neighborPath, relativePath+ctx.Config.Extension)
				}

				if fs.IsValidFile(filePath) {
					return filepath.ToSlash(filePath), ModuleNeighbor, nil
				}
			}
		} else if dep.Type == manifest.DependencyRemote {
			// Check if import path starts with dependency name (e.g., github.com/user/repo)
			if strings.HasPrefix(importPath, depName) {
				// Try to resolve from cache
				cachePath := filepath.Join(ctx.Config.ProjectRoot, ".ferret", "modules")
				modulePath, err := packages.ResolveRemotePackage(cachePath, depName, dep.Version)
				if err != nil {
					return "", ModuleRemote, fmt.Errorf("remote package not in cache: %s@%s (run: ferret get)", depName, dep.Version)
				}

				// Build file path within remote package
				relativePath := strings.TrimPrefix(importPath, depName+"/")
				if relativePath == importPath {
					relativePath = ""
				}

				var filePath string
				if relativePath == "" {
					filePath = filepath.Join(modulePath, filepath.Base(depName)+ctx.Config.Extension)
				} else {
					filePath = filepath.Join(modulePath, relativePath+ctx.Config.Extension)
				}

				if fs.IsValidFile(filePath) {
					return filepath.ToSlash(filePath), ModuleRemote, nil
				}
			}
		}
	}

	return "", ModuleUnknown, fmt.Errorf("dependency not found for import: %s", importPath)
}

// isRemoteImport checks if an import path is a remote module
func (ctx *CompilerContext) isRemoteImport(importPath string) bool {
	return strings.HasPrefix(importPath, "github.com/") ||
		strings.HasPrefix(importPath, "gitlab.com/") ||
		strings.HasPrefix(importPath, "bitbucket.org/")
}
