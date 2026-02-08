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
	FilePath     string // Path to the fer.ret file
}

// PackageInfo contains package metadata
type PackageInfo struct {
	Name            string
	Version         string
	CompilerVersion string
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
	if packageSection, ok := data["package"]; ok {
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
	if depSection, ok := data["dependencies"]; ok {
		for name, value := range depSection {
			dep, err := parseDependency(value)
			if err != nil {
				return nil, fmt.Errorf("invalid dependency '%s': %w", name, err)
			}
			manifest.Dependencies[name] = dep
		}
	}

	return manifest, nil
}

// parseDependency converts a TOML value to a Dependency
func parseDependency(value toml.TOMLValue) (Dependency, error) {
	dep := Dependency{}

	switch v := value.(type) {
	case string:
		// Simple string format: "version" for remote or "../path" for neighbor
		if strings.HasPrefix(v, "../") {
			// Neighbor path (outside project)
			dep.Type = DependencyNeighbor
			dep.Path = v
		} else {
			// Version string for remote
			dep.Type = DependencyRemote
			dep.Version = v
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
