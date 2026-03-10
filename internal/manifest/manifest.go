package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"compiler/internal/toml"
)

const FileName = "fer.ret"

type DependencyType int

const (
	DependencyRemote DependencyType = iota
	DependencyNeighbor
)

type PackageInfo struct {
	Name            string
	Version         string
	CompilerVersion string
}

type Dependency struct {
	Type    DependencyType
	Version string
	Path    string
}

type File struct {
	Package      PackageInfo
	Dependencies map[string]Dependency
	FilePath     string
}

var (
	identifierPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	versionPattern    = regexp.MustCompile(`^[A-Za-z0-9._+-]+$`)
)

func Find(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	for {
		manifestPath := filepath.Join(dir, FileName)
		if _, err := os.Stat(manifestPath); err == nil {
			return manifestPath, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no %s found", FileName)
		}
		dir = parent
	}
}

func Load(path string) (*File, error) {
	data, err := toml.ParseFile(path)
	if err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	manifest := &File{
		Dependencies: make(map[string]Dependency),
		FilePath:     path,
	}
	pkg, ok := data.Sections["package"]
	if !ok {
		return nil, fmt.Errorf("missing [package] section")
	}
	name, ok := pkg["name"].(string)
	if !ok || name == "" {
		return nil, fmt.Errorf("package.name is required")
	}
	if !identifierPattern.MatchString(name) {
		return nil, fmt.Errorf("invalid package.name %q", name)
	}
	manifest.Package.Name = name
	if version, ok := pkg["version"].(string); ok {
		if version != "" && !versionPattern.MatchString(version) {
			return nil, fmt.Errorf("invalid package.version %q", version)
		}
		manifest.Package.Version = version
	}
	if compilerVersion, ok := pkg["compiler"].(string); ok {
		manifest.Package.CompilerVersion = compilerVersion
	}

	if deps, ok := data.Sections["dependencies"]; ok {
		for alias, raw := range deps {
			if !identifierPattern.MatchString(alias) {
				return nil, fmt.Errorf("invalid dependency alias %q", alias)
			}
			if alias == "std" {
				return nil, fmt.Errorf("dependency alias %q is reserved", alias)
			}
			dep, err := parseDependency(raw)
			if err != nil {
				return nil, fmt.Errorf("invalid dependency %q: %w", alias, err)
			}
			if dep.Type == DependencyRemote && dep.Version != "" && !versionPattern.MatchString(dep.Version) {
				return nil, fmt.Errorf("invalid dependency %q version %q", alias, dep.Version)
			}
			manifest.Dependencies[alias] = dep
		}
	}
	return manifest, nil
}

func parseDependency(raw toml.Value) (Dependency, error) {
	switch value := raw.(type) {
	case string:
		return parseDependencyString(value)
	case toml.Table:
		return parseDependencyTable(value)
	default:
		return Dependency{}, fmt.Errorf("unsupported dependency format")
	}
}

func parseDependencyString(value string) (Dependency, error) {
	value = strings.TrimSpace(value)
	switch {
	case strings.HasPrefix(value, "../") || strings.HasPrefix(value, "./"):
		return Dependency{Type: DependencyNeighbor, Path: filepath.Clean(value)}, nil
	case isRemoteRepo(value):
		dep := Dependency{Type: DependencyRemote, Path: value, Version: "latest"}
		if before, after, ok := strings.Cut(value, "@"); ok {
			if after == "" {
				return Dependency{}, fmt.Errorf("missing version after '@'")
			}
			dep.Path = before
			dep.Version = after
		}
		return dep, nil
	default:
		return Dependency{}, fmt.Errorf("dependency must be a relative neighbor path or remote repo path")
	}
}

func parseDependencyTable(table toml.Table) (Dependency, error) {
	typeName, _ := table["type"].(string)
	path, _ := table["path"].(string)
	repo, _ := table["repo"].(string)
	version, _ := table["version"].(string)

	switch typeName {
	case "", "neighbor":
		switch {
		case path == "":
			return Dependency{}, fmt.Errorf("neighbor dependency requires path")
		case repo != "":
			return Dependency{}, fmt.Errorf("neighbor dependency cannot define repo")
		case version != "":
			return Dependency{}, fmt.Errorf("neighbor dependency cannot define version")
		}
		if !strings.HasPrefix(path, "../") && !strings.HasPrefix(path, "./") {
			return Dependency{}, fmt.Errorf("neighbor dependency path must be relative")
		}
		return Dependency{Type: DependencyNeighbor, Path: filepath.Clean(path)}, nil
	case "remote":
		if repo == "" {
			repo = path
		}
		switch {
		case repo == "":
			return Dependency{}, fmt.Errorf("remote dependency requires repo")
		case !isRemoteRepo(repo):
			return Dependency{}, fmt.Errorf("remote dependency repo %q is invalid", repo)
		}
		if version == "" {
			version = "latest"
		}
		return Dependency{Type: DependencyRemote, Path: repo, Version: version}, nil
	default:
		return Dependency{}, fmt.Errorf("unknown dependency type %q", typeName)
	}
}

func isRemoteRepo(path string) bool {
	return strings.HasPrefix(path, "github.com/") || strings.HasPrefix(path, "gitlab.com/") || strings.HasPrefix(path, "bitbucket.org/")
}
