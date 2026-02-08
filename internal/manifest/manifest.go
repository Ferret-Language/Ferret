package manifest

import (
	"compiler/toml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PackageManifest represents a fer.ret file
type PackageManifest struct {
	Package      PackageInfo
	Dependencies map[string]Dependency
	Dev          DevConfig // Development configuration
	FilePath     string    // Path to the fer.ret file
}

// PackageInfo contains package metadata
type PackageInfo struct {
	Name            string
	Version         string
	CompilerVersion string
}

// DevConfig contains development/testing configuration
type DevConfig struct {
	MockRemote bool   // If true, treat local paths as remote packages
	MockPath   string // Path to mock packages directory
}

// Dependency represents a package dependency
type Dependency struct {
	Type    DependencyType // remote, neighbor
	Version string         // Version constraint (for remote)
	Path    string         // Path (for neighbor)
}

// DependencyType indicates how a dependency is resolved
type DependencyType int

const (
	DependencyRemote DependencyType = iota
	DependencyNeighbor
)

// LoadManifest loads and parses a fer.ret file
func LoadManifest(manifestPath string) (*PackageManifest, error) {
	data, err := toml.ParseTOMLFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	manifest := &PackageManifest{
		Dependencies: make(map[string]Dependency),
		FilePath:     manifestPath,
	}

	// Parse [package] section
	if packageSection, ok := data.Sections["package"]; ok {
		if name, ok := packageSection["name"].(string); ok {
			manifest.Package.Name = name
		} else {
			return nil, fmt.Errorf("package.name is required")
		}

		if version, ok := packageSection["version"].(string); ok {
			manifest.Package.Version = version
		}

		if compilerVer, ok := packageSection["compiler"].(string); ok {
			manifest.Package.CompilerVersion = compilerVer
		}
	} else {
		return nil, fmt.Errorf("missing [package] section")
	}

	// Parse [dependencies] section
	if depSection, ok := data.Sections["dependencies"]; ok {
		for name, value := range depSection {
			dep, err := parseDependency(value)
			if err != nil {
				return nil, fmt.Errorf("invalid dependency '%s': %w", name, err)
			}
			manifest.Dependencies[name] = dep
		}
	}

	// Parse [dev] section (optional)
	if devSection, ok := data.Sections["dev"]; ok {
		if mockRemote, ok := devSection["mock_remote"].(bool); ok {
			manifest.Dev.MockRemote = mockRemote
		}
		if mockPath, ok := devSection["mock_path"].(string); ok {
			manifest.Dev.MockPath = mockPath
		}
	}

	return manifest, nil
}

// parseDependency converts a TOML value to a Dependency
func parseDependency(value toml.TOMLValue) (Dependency, error) {
	dep := Dependency{}

	switch v := value.(type) {
	case string:
		// Simple string format: "../path" for neighbor or "github.com/user/repo@version" for remote
		if strings.HasPrefix(v, "../") || strings.HasPrefix(v, "./") {
			// Neighbor path (relative to current project)
			dep.Type = DependencyNeighbor
			dep.Path = v
		} else if strings.Contains(v, "github.com/") || strings.Contains(v, "gitlab.com/") || strings.Contains(v, "bitbucket.org/") {
			// Remote package: "github.com/user/repo@version" or "github.com/user/repo"
			dep.Type = DependencyRemote

			// Split on @ to separate repo from version
			if idx := strings.Index(v, "@"); idx != -1 {
				dep.Path = v[:idx]      // github.com/user/repo
				dep.Version = v[idx+1:] // version constraint
			} else {
				dep.Path = v           // github.com/user/repo
				dep.Version = "latest" // default to latest
			}
		} else {
			// Unknown format
			return dep, fmt.Errorf("invalid dependency format: must be relative path (../) or remote URL (github.com/...)")
		}
	case map[string]interface{}:
		// Object format with explicit type
		if depType, ok := v["type"].(string); ok {
			switch depType {
			case "remote":
				dep.Type = DependencyRemote
				if version, ok := v["version"].(string); ok {
					dep.Version = version
				}
			case "neighbor":
				dep.Type = DependencyNeighbor
				if path, ok := v["path"].(string); ok {
					dep.Path = path
				}
			default:
				return dep, fmt.Errorf("unknown dependency type: %s", depType)
			}
		}
	default:
		return dep, fmt.Errorf("invalid dependency format")
	}

	return dep, nil
}

// ParseDependency parses a dependency string into a Dependency struct
// Supports formats like "github.com/user/repo@v1.0.0" or "../neighbor"
func ParseDependency(spec string) (Dependency, error) {
	return parseDependency(spec)
}

// FindManifest searches for fer.ret starting from the given directory upwards
func FindManifest(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}

	for {
		manifestPath := filepath.Join(dir, "fer.ret")
		if _, err := os.Stat(manifestPath); err == nil {
			return manifestPath, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root
			return "", fmt.Errorf("no fer.ret found")
		}
		dir = parent
	}
}

// GetString safely retrieves a string value from TOML table
func GetString(table toml.TOMLTable, key string) (string, bool) {
	if val, ok := table[key]; ok {
		if str, ok := val.(string); ok {
			return str, true
		}
	}
	return "", false
}

// SaveManifest writes a manifest back to fer.ret file
func SaveManifest(manifestPath string, manifest *PackageManifest) error {
	// Build TOML data structure
	data := toml.NewTOMLData()

	// [package] section
	packageSection := make(toml.TOMLTable)
	packageSection["name"] = manifest.Package.Name
	packageSection["version"] = manifest.Package.Version
	if manifest.Package.CompilerVersion != "" {
		packageSection["compiler"] = manifest.Package.CompilerVersion
	}
	data.Sections["package"] = packageSection
	data.SectionOrder = append(data.SectionOrder, "package")
	data.KeyOrder["package"] = []string{"name", "version", "compiler"}

	// [dependencies] section
	if len(manifest.Dependencies) > 0 {
		depsSection := make(toml.TOMLTable)
		var depNames []string
		for name, dep := range manifest.Dependencies {
			depNames = append(depNames, name)
			if dep.Type == DependencyNeighbor {
				depsSection[name] = dep.Path
			} else if dep.Type == DependencyRemote {
				// Format: "github.com/user/repo@version"
				if dep.Version != "" && dep.Version != "latest" {
					depsSection[name] = dep.Path + "@" + dep.Version
				} else {
					depsSection[name] = dep.Path
				}
			}
		}
		data.Sections["dependencies"] = depsSection
		data.SectionOrder = append(data.SectionOrder, "dependencies")
		data.KeyOrder["dependencies"] = depNames
	}

	// [dev] section
	if manifest.Dev.MockRemote || manifest.Dev.MockPath != "" {
		devSection := make(toml.TOMLTable)
		devSection["mock_remote"] = manifest.Dev.MockRemote
		devSection["mock_path"] = manifest.Dev.MockPath
		data.Sections["dev"] = devSection
		data.SectionOrder = append(data.SectionOrder, "dev")
		data.KeyOrder["dev"] = []string{"mock_remote", "mock_path"}
	}

	// Write to file
	return toml.WriteTOMLFile(manifestPath, data, nil)
}

// RemoveDependencyFromManifest removes a dependency and saves the manifest
func RemoveDependencyFromManifest(manifestPath, depName string) error {
	// Load raw TOML data to preserve section order
	data, err := toml.ParseTOMLFile(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to parse manifest: %w", err)
	}

	// Remove dependency from dependencies section
	if depsSection, ok := data.Sections["dependencies"]; ok {
		delete(depsSection, depName)

		// Also remove from key order
		if keyOrder, ok := data.KeyOrder["dependencies"]; ok {
			for i, key := range keyOrder {
				if key == depName {
					data.KeyOrder["dependencies"] = append(keyOrder[:i], keyOrder[i+1:]...)
					break
				}
			}
		}

		// If dependencies section is now empty, remove it entirely
		if len(depsSection) == 0 {
			delete(data.Sections, "dependencies")
			delete(data.KeyOrder, "dependencies")
			// Remove from section order
			for i, section := range data.SectionOrder {
				if section == "dependencies" {
					data.SectionOrder = append(data.SectionOrder[:i], data.SectionOrder[i+1:]...)
					break
				}
			}
		}
	}

	// Write back
	return toml.WriteTOMLFile(manifestPath, data, nil)
}
