package context

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/phase"
	"compiler/internal/tokens"
)

type Config struct {
	RootDir   string
	Extension string
}

type Module struct {
	ImportPath   string
	FilePath     string
	Content      string
	ContentHash  string
	Tokens       []tokens.Token
	AST          *ast.Module
	Phase        phase.ModulePhase
	Dependencies []string
}

type CompilerContext struct {
	Config      Config
	Diagnostics *diagnostics.Bag

	mu           sync.RWMutex
	modules      map[string]*Module
	fileIndex    map[string]string
	dependencies map[string]map[string]struct{}
}

func New(rootDir, extension string, diag *diagnostics.Bag) *CompilerContext {
	if diag == nil {
		diag = diagnostics.NewBag()
	}
	if extension == "" {
		extension = ".ferr"
	}
	return &CompilerContext{
		Config: Config{
			RootDir:   filepath.Clean(rootDir),
			Extension: extension,
		},
		Diagnostics:  diag,
		modules:      make(map[string]*Module),
		fileIndex:    make(map[string]string),
		dependencies: make(map[string]map[string]struct{}),
	}
}

func (ctx *CompilerContext) ImportPathForFile(filePath string) (string, error) {
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(ctx.Config.RootDir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootAbs, absFile)
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, "../") || rel == ".." {
		return "", fmt.Errorf("file %s is outside root %s", absFile, rootAbs)
	}
	return ctx.NormalizeImportPath(strings.TrimSuffix(rel, ctx.Config.Extension)), nil
}

func (ctx *CompilerContext) FilePathForImport(importPath string) string {
	clean := ctx.NormalizeImportPath(importPath)
	if clean == "" {
		return filepath.Clean(ctx.Config.RootDir)
	}
	return filepath.Join(ctx.Config.RootDir, filepath.FromSlash(clean)+ctx.Config.Extension)
}

func (ctx *CompilerContext) ResolveImport(from *Module, spec string) (string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", fmt.Errorf("empty import path")
	}

	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") {
		if from == nil || from.FilePath == "" {
			return "", fmt.Errorf("relative import %q requires a source module", spec)
		}
		base := filepath.Dir(from.FilePath)
		target := filepath.Clean(filepath.Join(base, filepath.FromSlash(spec)))
		return ctx.ImportPathForFile(target + ctx.Config.Extension)
	}

	spec = strings.ReplaceAll(spec, "::", "/")
	return ctx.NormalizeImportPath(spec), nil
}

func (ctx *CompilerContext) NormalizeImportPath(importPath string) string {
	importPath = strings.TrimSpace(importPath)
	importPath = strings.TrimSuffix(importPath, ctx.Config.Extension)
	importPath = strings.ReplaceAll(importPath, "\\", "/")
	importPath = strings.TrimPrefix(importPath, "./")
	importPath = strings.Trim(importPath, "/")
	parts := strings.Split(importPath, "/")
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(clean) > 0 {
				clean = clean[:len(clean)-1]
			}
		default:
			clean = append(clean, part)
		}
	}
	return strings.Join(clean, "/")
}

func (ctx *CompilerContext) UpsertModule(importPath string) *Module {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if mod, ok := ctx.modules[importPath]; ok {
		return mod
	}
	mod := &Module{ImportPath: importPath, FilePath: ctx.FilePathForImport(importPath)}
	ctx.modules[importPath] = mod
	ctx.fileIndex[filepath.Clean(mod.FilePath)] = importPath
	return mod
}

func (ctx *CompilerContext) GetModule(importPath string) (*Module, bool) {
	ctx.mu.RLock()
	defer ctx.mu.RUnlock()
	mod, ok := ctx.modules[importPath]
	return mod, ok
}

func (ctx *CompilerContext) Modules() []*Module {
	ctx.mu.RLock()
	defer ctx.mu.RUnlock()
	mods := make([]*Module, 0, len(ctx.modules))
	for _, mod := range ctx.modules {
		mods = append(mods, mod)
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].ImportPath < mods[j].ImportPath })
	return mods
}

func (ctx *CompilerContext) DiscoverModules() ([]string, error) {
	root := ctx.Config.RootDir
	files := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ctx.Config.Extension {
			return nil
		}
		files = append(files, filepath.Clean(path))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func (ctx *CompilerContext) HasFile(filePath string) bool {
	info, err := os.Stat(filePath)
	return err == nil && !info.IsDir()
}

func (ctx *CompilerContext) ResetDependencies(importPath string) {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.dependencies[importPath] = make(map[string]struct{})
	if mod, ok := ctx.modules[importPath]; ok {
		mod.Dependencies = mod.Dependencies[:0]
	}
}

func (ctx *CompilerContext) AddDependency(from, to string) {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	deps := ctx.dependencies[from]
	if deps == nil {
		deps = make(map[string]struct{})
		ctx.dependencies[from] = deps
	}
	if _, ok := deps[to]; ok {
		return
	}
	deps[to] = struct{}{}
	if mod, ok := ctx.modules[from]; ok {
		mod.Dependencies = append(mod.Dependencies, to)
		sort.Strings(mod.Dependencies)
	}
}

func (ctx *CompilerContext) DependencyList(importPath string) []string {
	ctx.mu.RLock()
	defer ctx.mu.RUnlock()
	deps := ctx.dependencies[importPath]
	out := make([]string, 0, len(deps))
	for dep := range deps {
		out = append(out, dep)
	}
	sort.Strings(out)
	return out
}

func (ctx *CompilerContext) StoreModuleContent(mod *Module, content string) bool {
	hash := hashContent(content)
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	changed := mod.ContentHash != hash
	mod.Content = content
	mod.ContentHash = hash
	mod.FilePath = filepath.Clean(mod.FilePath)
	ctx.fileIndex[mod.FilePath] = mod.ImportPath
	return changed
}

func hashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
