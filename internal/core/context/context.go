package context

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	cfg "compiler/internal/analysis/cfg/model"
	layout "compiler/internal/analysis/layout/model"
	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/table"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/phase"
	"compiler/internal/frontend/ast"
	"compiler/internal/ir/hir"
	"compiler/internal/ir/mir"
	"compiler/internal/tokens"
)

type Config struct {
	RootDir         string
	Extension       string
	StdlibRoot      string
	DependencyRoots map[string]string
	TargetOS        string
	TargetArch      string
	TargetBackend   string
	BuildDebug      bool
}

type ModuleOrigin string

const (
	ModuleOriginLocal      ModuleOrigin = "local"
	ModuleOriginStdlib     ModuleOrigin = "stdlib"
	ModuleOriginDependency ModuleOrigin = "dependency"
)

type ResolvedImport struct {
	Key             string
	ImportPath      string
	FilePath        string
	Origin          ModuleOrigin
	DependencyAlias string
}

type Module struct {
	Key          string
	ImportPath   string
	FilePath     string
	IsEntry      bool
	Origin       ModuleOrigin
	Dependency   string
	Content      string
	ContentHash  string
	Tokens       []tokens.Token
	AST          *ast.Module
	HIR          *hir.Module
	LoweredHIR   *hir.Module
	CFG          *cfg.Module
	MIR          *mir.Module
	Layout       *layout.Module
	Phase        phase.ModulePhase
	Dependencies []string
	ModuleScope  *table.Scope
	MethodSets   map[typeinfo.ReceiverKey]map[string]*symbols.Symbol
	TypeMembers  map[string]map[string]*symbols.Symbol
	Bindings     *binding.ModuleInfo
	Types        *typeinfo.ModuleInfo
}

type CompilerContext struct {
	Config      Config
	Diagnostics *diagnostics.DiagnosticBag
	Universe    *table.Scope
	Prelude     *Module

	mu           sync.RWMutex
	modules      map[string]*Module
	fileIndex    map[string]string
	dependencies map[string]map[string]struct{}
}

func New(rootDir, extension string, diag *diagnostics.DiagnosticBag) *CompilerContext {
	return NewWithConfig(Config{
		RootDir:         filepath.Clean(rootDir),
		Extension:       extension,
		DependencyRoots: make(map[string]string),
	}, diag)
}

func NewWithConfig(cfg Config, diag *diagnostics.DiagnosticBag) *CompilerContext {
	if diag == nil {
		diag = diagnostics.NewDiagnosticBag("")
	}
	if cfg.Extension == "" {
		cfg.Extension = ".fer"
	}
	if cfg.RootDir == "" {
		cfg.RootDir = "."
	}
	if cfg.TargetOS == "" {
		cfg.TargetOS = runtime.GOOS
	}
	if cfg.TargetArch == "" {
		cfg.TargetArch = runtime.GOARCH
	}
	if cfg.TargetBackend == "" {
		cfg.TargetBackend = "qbe"
	}
	cfg.RootDir = filepath.Clean(cfg.RootDir)
	if cfg.DependencyRoots == nil {
		cfg.DependencyRoots = make(map[string]string)
	}
	universe := predeclaredScope()
	return &CompilerContext{
		Config:       cfg,
		Diagnostics:  diag,
		Universe:     universe,
		modules:      make(map[string]*Module),
		fileIndex:    make(map[string]string),
		dependencies: make(map[string]map[string]struct{}),
	}
}

func predeclaredScope() *table.Scope {
	scope := table.New(nil)
	declarePredeclaredConst(scope, "true")
	declarePredeclaredConst(scope, "false")
	declarePredeclaredConst(scope, "none")
	declarePredeclaredConst(scope, "undefined")
	return scope
}

func declarePredeclaredConst(scope *table.Scope, name string) {
	if scope == nil || name == "" {
		return
	}
	sym := symbols.New(name, symbols.SymbolConst, nil)
	sym.IsPub = true
	_ = scope.Declare(sym)
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

func (ctx *CompilerContext) ResolveLocalModule(filePath string) (ResolvedImport, error) {
	importPath, err := ctx.ImportPathForFile(filePath)
	if err != nil {
		return ResolvedImport{}, err
	}
	if err := ctx.validateLocalImportPath(importPath); err != nil {
		return ResolvedImport{}, err
	}
	return ResolvedImport{
		Key:        moduleKey(ModuleOriginLocal, importPath),
		ImportPath: importPath,
		FilePath:   filepath.Clean(filePath),
		Origin:     ModuleOriginLocal,
	}, nil
}

func (ctx *CompilerContext) ResolveImport(from *Module, spec string) (ResolvedImport, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ResolvedImport{}, fmt.Errorf("empty import path")
	}

	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") {
		return ResolvedImport{}, fmt.Errorf("relative imports are not allowed; use package-root-relative paths")
	}

	spec = strings.ReplaceAll(spec, "::", "/")
	importPath, err := ctx.NormalizeSourceImportPath(spec)
	if err != nil {
		return ResolvedImport{}, err
	}

	origin, depAlias, err := ctx.classifyImportPath(importPath)
	if err != nil {
		return ResolvedImport{}, err
	}
	filePath, err := ctx.filePathForResolvedImport(origin, depAlias, importPath)
	if err != nil {
		return ResolvedImport{}, err
	}

	return ResolvedImport{
		Key:             moduleKey(origin, importPath),
		ImportPath:      importPath,
		FilePath:        filePath,
		Origin:          origin,
		DependencyAlias: depAlias,
	}, nil
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

func (ctx *CompilerContext) NormalizeSourceImportPath(importPath string) (string, error) {
	importPath = strings.TrimSpace(importPath)
	importPath = strings.TrimSuffix(importPath, ctx.Config.Extension)
	importPath = strings.ReplaceAll(importPath, "\\", "/")
	importPath = strings.Trim(importPath, "/")
	if importPath == "" {
		return "", fmt.Errorf("empty import path")
	}
	parts := strings.Split(importPath, "/")
	for _, part := range parts {
		switch part {
		case "", ".", "..":
			return "", fmt.Errorf("invalid import path %q", importPath)
		}
	}
	return strings.Join(parts, "/"), nil
}

func (ctx *CompilerContext) UpsertModule(resolved ResolvedImport) *Module {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if mod, ok := ctx.modules[resolved.Key]; ok {
		mod.ImportPath = resolved.ImportPath
		mod.FilePath = filepath.Clean(resolved.FilePath)
		mod.Origin = resolved.Origin
		mod.Dependency = resolved.DependencyAlias
		return mod
	}
	mod := &Module{
		Key:        resolved.Key,
		ImportPath: resolved.ImportPath,
		FilePath:   filepath.Clean(resolved.FilePath),
		Origin:     resolved.Origin,
		Dependency: resolved.DependencyAlias,
	}
	ctx.modules[resolved.Key] = mod
	ctx.fileIndex[filepath.Clean(mod.FilePath)] = resolved.Key
	return mod
}

func (ctx *CompilerContext) GetModule(key string) (*Module, bool) {
	ctx.mu.RLock()
	defer ctx.mu.RUnlock()
	mod, ok := ctx.modules[key]
	return mod, ok
}

func (ctx *CompilerContext) Modules() []*Module {
	ctx.mu.RLock()
	defer ctx.mu.RUnlock()
	mods := make([]*Module, 0, len(ctx.modules))
	for _, mod := range ctx.modules {
		mods = append(mods, mod)
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].Key < mods[j].Key })
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
	ctx.fileIndex[mod.FilePath] = mod.Key
	return changed
}

func (ctx *CompilerContext) classifyImportPath(importPath string) (ModuleOrigin, string, error) {
	if importPath == "std" || strings.HasPrefix(importPath, "std/") {
		if importPath == "std" {
			return "", "", fmt.Errorf("stdlib imports must include a module path")
		}
		return ModuleOriginStdlib, "", nil
	}

	first, _, _ := strings.Cut(importPath, "/")
	if root, ok := ctx.Config.DependencyRoots[first]; ok {
		if root == "" {
			return "", "", fmt.Errorf("dependency alias %q has no configured root", first)
		}
		if importPath == first {
			return "", "", fmt.Errorf("dependency imports must include a module path after alias %q", first)
		}
		return ModuleOriginDependency, first, nil
	}

	return ModuleOriginLocal, "", nil
}

func (ctx *CompilerContext) filePathForResolvedImport(origin ModuleOrigin, depAlias, importPath string) (string, error) {
	switch origin {
	case ModuleOriginLocal:
		return filepath.Join(ctx.Config.RootDir, filepath.FromSlash(importPath)+ctx.Config.Extension), nil
	case ModuleOriginStdlib:
		if ctx.Config.StdlibRoot == "" {
			return "", fmt.Errorf("stdlib root is not configured")
		}
		subpath := strings.TrimPrefix(importPath, "std/")
		return filepath.Join(ctx.Config.StdlibRoot, filepath.FromSlash(subpath)+ctx.Config.Extension), nil
	case ModuleOriginDependency:
		root := ctx.Config.DependencyRoots[depAlias]
		if root == "" {
			return "", fmt.Errorf("dependency alias %q has no configured root", depAlias)
		}
		_, subpath, _ := strings.Cut(importPath, "/")
		if subpath == "" {
			return "", fmt.Errorf("dependency imports must include a module path after alias %q", depAlias)
		}
		return filepath.Join(root, filepath.FromSlash(subpath)+ctx.Config.Extension), nil
	default:
		return "", fmt.Errorf("unknown module origin %q", origin)
	}
}

func moduleKey(origin ModuleOrigin, importPath string) string {
	return string(origin) + ":" + importPath
}

func (ctx *CompilerContext) validateLocalImportPath(importPath string) error {
	first, _, _ := strings.Cut(importPath, "/")
	switch first {
	case "std":
		return fmt.Errorf("local module path %q uses reserved top-level name %q", importPath, first)
	}
	if _, ok := ctx.Config.DependencyRoots[first]; ok {
		return fmt.Errorf("local module path %q conflicts with dependency alias %q", importPath, first)
	}
	return nil
}

func hashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
