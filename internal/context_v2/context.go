// Package context_v2 provides the central compilation context for the Ferret compiler.
//
// ARCHITECTURE:
// This implements a per-module phase tracking system where each module (source file)
// progresses through compilation phases independently. This design:
// - Handles multi-file imports correctly (same file imported multiple times)
// - Enables incremental compilation
// - Prevents redundant parsing/analysis
// - Matches production compiler architectures (Rustc, TypeScript, Zig)
//
// DESIGN PRINCIPLES:
// 1. Import paths are semantic identifiers (not file system paths)
// 2. Each module tracks its own compilation phase
// 3. Cycle detection prevents circular imports
// 4. Module types (Local, Builtin, Remote) enable flexible resolution
package context_v2

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/phase"
	"compiler/internal/semantics/symbols"
	"compiler/internal/semantics/table"
	"compiler/internal/source"
	"compiler/internal/types"
	"compiler/internal/utils/fs"
)

// ModuleType categorizes how a module is resolved
type ModuleType int

const (
	ModuleLocal    ModuleType = iota // Local project module
	ModuleBuiltin                    // Standard library module
	ModuleRemote                     // Remote dependency (github.com/...)
	ModuleNeighbor                   // Neighbor project module
	ModuleUnknown                    // Unknown/unresolved
)

func (mt ModuleType) String() string {
	switch mt {
	case ModuleLocal:
		return "Local"
	case ModuleBuiltin:
		return "Builtin"
	case ModuleRemote:
		return "Remote"
	case ModuleNeighbor:
		return "Neighbor"
	default:
		return "Unknown"
	}
}

// Module represents a single compiled module with its semantic information
type Module struct {
	// Core data
	ImportPath string      // Logical import path (e.g., "myproject/utils/math")
	FilePath   string      // Physical file path
	Type       ModuleType  // How this module was resolved
	AST        *ast.Module // Parsed syntax tree

	// Compilation state
	Phase phase.ModulePhase // Current compilation phase

	// Semantic data
	ModuleScope  *table.SymbolTable // Module-level symbols
	CurrentScope *table.SymbolTable // Current scope during scope switching

	// Type checking context
	CurrentFunctionReturnType types.SemType    // Expected return type for current function being checked
	CurrentDeferredStmts      []*ast.DeferStmt // Deferred statements in current function (LIFO order)
	InDeferContext            bool             // True when type-checking a defer statement (skip Result validation)

	Imports           map[string]*Import               // Resolved imports
	ImportAliasMap    map[string]string                // alias/name -> import path mapping for module access
	ExprTypes         map[ast.Expression]types.SemType // Type of each expression (filled during type checking)
	NarrowedExprTypes map[string]types.SemType         // Narrowed type for expression keys during type checking

	// Source metadata
	Content string // Raw source code (for diagnostics)

	// Artifacts for downstream phases (codegen, etc.)
	Artifacts map[string]any

	// Concurrency control
	Mu sync.Mutex // Protects field updates during parallel parsing
}

// EnterScope switches to a new scope and returns a function to restore the old scope.
// Use with defer to ensure scope is always restored:
//
//	defer EnterScope(mod, newScope)()
func (mod *Module) EnterScope(newScope *table.SymbolTable) func() {
	oldScope := mod.CurrentScope
	mod.CurrentScope = newScope
	return func() {
		mod.CurrentScope = oldScope
	}
}

// SetExprType records the resolved type for an expression.
func (mod *Module) SetExprType(expr ast.Expression, typ types.SemType) {
	if mod == nil || expr == nil {
		return
	}
	if typ == nil {
		typ = types.TypeUnknown
	}

	mod.Mu.Lock()
	defer mod.Mu.Unlock()

	if mod.ExprTypes == nil {
		mod.ExprTypes = make(map[ast.Expression]types.SemType)
	}

	mod.ExprTypes[expr] = typ
}

// ExprType returns the resolved type for an expression, if any.
func (mod *Module) ExprType(expr ast.Expression) (types.SemType, bool) {
	if mod == nil || expr == nil {
		return types.TypeUnknown, false
	}

	mod.Mu.Lock()
	defer mod.Mu.Unlock()

	if mod.ExprTypes == nil {
		return types.TypeUnknown, false
	}

	typ, ok := mod.ExprTypes[expr]
	if !ok || typ == nil {
		return types.TypeUnknown, ok
	}

	return typ, ok
}

// Import represents a resolved import statement
type Import struct {
	Path     string // Import path as written in source
	Alias    string
	Location *source.Location
	IsUsed   bool
}

// CompilerContext is the central compilation state manager
type CompilerContext struct {
	// Module registry: import path -> Module
	// This is the authoritative source for all compiled modules
	Modules map[string]*Module
	mu      sync.RWMutex // protects Modules and DepGraph during parallel parse

	// Sorted module keys in topological order
	sortedModules []string

	// Entry point
	EntryPoint  string // Full path to entry file
	EntryModule string // Import path of entry module

	// Universe scope: built-in types and functions
	Universe *table.SymbolTable

	// Diagnostics: centralized error collection
	Diagnostics *diagnostics.DiagnosticBag

	// Dependency graph: import path -> list of imported paths
	// Used for cycle detection and build ordering
	DepGraph map[string][]string

	// Configuration
	Config *Config

	// CodegenOutput holds in-memory output for backends that emit bytes (e.g., wasm).
	CodegenOutput []byte
}

// Config holds compiler configuration
type Config struct {
	TypeCheckOnly bool // If true, just do typecheck
	// Codegen backend to use ("none", "qbe")
	CodegenBackend string
	// Project information
	ProjectName   string // Name of the project
	ProjectPrefix string // Internal prefix for project import root. It's used so project name do not colide with any module
	ProjectRoot   string // Root directory of the project

	// Build configuration
	OutputPath   string // Where to write compiled output
	Extension    string // Source file extension (default: ".fer")
	KeepGenFiles bool   // Keep generated files after compilation
	Debug        bool   // Debug mode
	SaveAST      bool   // Save AST to file
	PointerSize  int    // Target pointer size in bytes (0 = default)

	// Module resolution
	RuntimePath string // Path to runtime/stdlib library directory (relative to executable)

	// Remote modules (future)
	RemoteCachePath string // Cache directory for remote dependencies (.ferret)
}

// New creates a new compiler context
func New(config *Config, debug bool) *CompilerContext {
	if config == nil {
		config = &Config{
			Extension: ".fer",
		}
	}
	config.Debug = debug

	// Set up remote cache path if not specified
	if config.RemoteCachePath == "" && config.ProjectRoot != "" {
		config.RemoteCachePath = filepath.Join(config.ProjectRoot, ".ferret")
		os.MkdirAll(config.RemoteCachePath, 0755)
	}

	// Create universe scope with built-in types
	universe := table.NewSymbolTable(nil)
	registerBuiltins(universe)

	ctx := &CompilerContext{
		Modules:       make(map[string]*Module),
		sortedModules: []string{},
		Universe:      universe,
		Diagnostics:   diagnostics.NewDiagnosticBag(""),
		DepGraph:      make(map[string][]string),
		Config:        config,
	}

	return ctx
}

// registerBuiltins populates the universe scope with built-in types
func registerBuiltins(universe *table.SymbolTable) {
	builtinTypes := types.BuiltinTypesList()

	for _, typ := range builtinTypes {
		universe.Declare(typ.String(), &symbols.Symbol{
			Name:     typ.String(),
			Kind:     symbols.SymbolType,
			Type:     typ,
			Exported: true,
		})
	}

	// Built-in constants
	universe.Declare("true", &symbols.Symbol{
		Name:     "true",
		Kind:     symbols.SymbolConstant,
		Type:     types.TypeBool,
		Exported: true,
	})
	universe.Declare("false", &symbols.Symbol{
		Name:     "false",
		Kind:     symbols.SymbolConstant,
		Type:     types.TypeBool,
		Exported: true,
	})
	universe.Declare("none", &symbols.Symbol{
		Name:     "none",
		Kind:     symbols.SymbolConstant,
		Type:     types.TypeNone, // Special none type for optional unwrapping
		Exported: true,
	})
}

// GetUniverse returns the universe scope.
func (ctx *CompilerContext) GetUniverse() *table.SymbolTable {
	return ctx.Universe
}

// SetEntryPoint sets the entry point for compilation
func (ctx *CompilerContext) SetEntryPoint(filePath string) error {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return fmt.Errorf("failed to resolve entry point: %w", err)
	}

	if !fs.IsValidFile(absPath) {
		return fmt.Errorf("entry point does not exist: %s", absPath)
	}

	ctx.EntryPoint = filepath.ToSlash(absPath)

	// Derive import path from file path
	ctx.EntryModule = ctx.FilePathToImportPath(absPath)

	return nil
}

// SetEntryPointWithCode sets the entry point with in-memory code (for WASM/playground)
func (ctx *CompilerContext) SetEntryPointWithCode(code, moduleName string) error {
	// For in-memory compilation, use a virtual path
	virtualPath := filepath.Join(ctx.Config.ProjectRoot, moduleName+ctx.Config.Extension)
	ctx.EntryPoint = filepath.ToSlash(virtualPath)

	// Derive import path consistently with file mode
	ctx.EntryModule = ctx.FilePathToImportPath(virtualPath)

	// Add source content to diagnostic bag's cache so it can display source lines
	ctx.Diagnostics.AddSourceContent(virtualPath, code)

	modScope := table.NewSymbolTable(ctx.Universe)

	// Create the entry module directly with the provided code
	module := &Module{
		ImportPath:   ctx.EntryModule,
		FilePath:     virtualPath,
		Type:         ModuleLocal,
		Phase:        phase.PhaseNotStarted,
		ModuleScope:  modScope,
		CurrentScope: modScope,
		Content:      code,
		Artifacts:    make(map[string]any),
	}

	ctx.AddModule(ctx.EntryModule, module)
	return nil
}

// SetEntryPointWithFiles sets up multi-file in-memory compilation (for WASM playground)
// files is a map of filename -> content (e.g. {"main.fer": "...", "utils.fer": "..."})
// entryModule is the name without extension (e.g. "main" for main.fer)
func (ctx *CompilerContext) SetEntryPointWithFiles(files map[string]string, entryModule string) error {
	if len(files) == 0 {
		return fmt.Errorf("no files provided")
	}

	// Validate entry module exists
	entryFileName := entryModule + ctx.Config.Extension
	if _, exists := files[entryFileName]; !exists {
		return fmt.Errorf("entry file %s not found in provided files", entryFileName)
	}

	// Set entry point
	virtualEntryPath := filepath.Join(ctx.Config.ProjectRoot, entryFileName)
	ctx.EntryPoint = filepath.ToSlash(virtualEntryPath)
	ctx.EntryModule = ctx.FilePathToImportPath(virtualEntryPath)

	// Create modules for all files
	for filename, content := range files {
		virtualPath := filepath.Join(ctx.Config.ProjectRoot, filename)
		virtualPath = filepath.ToSlash(virtualPath)
		importPath := ctx.FilePathToImportPath(virtualPath)

		// Add source content to diagnostics cache
		ctx.Diagnostics.AddSourceContent(virtualPath, content)

		modScope := table.NewSymbolTable(ctx.Universe)
		module := &Module{
			ImportPath:   importPath,
			FilePath:     virtualPath,
			Type:         ModuleLocal,
			Phase:        phase.PhaseNotStarted,
			ModuleScope:  modScope,
			CurrentScope: modScope,
			Content:      content,
			Artifacts:    make(map[string]any),
		}

		ctx.AddModule(importPath, module)
	}

	return nil
}

// FilePathToImportPath converts a file path to a logical import path
func (ctx *CompilerContext) FilePathToImportPath(filePath string) string {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return ""
	}

	absPath = filepath.ToSlash(absPath)
	projectRoot := filepath.ToSlash(ctx.Config.ProjectRoot)

	// Get relative path from project root
	relPath, err := filepath.Rel(projectRoot, absPath)
	if err != nil {
		// If not under project root, use filename
		return strings.TrimSuffix(filepath.Base(filePath), ctx.Config.Extension)
	}

	relPath = filepath.ToSlash(relPath)
	// Remove extension
	relPath = strings.TrimSuffix(relPath, ctx.Config.Extension)

	// Build import path: projectname/path/to/module
	if ctx.Config.ProjectName != "" {
		return ctx.Config.ProjectName + "/" + relPath
	}
	return relPath
}

// ImportPathToFilePath converts an import path to a file path.
// Resolution priority: stdlib (libs) first, then local project files.
func (ctx *CompilerContext) ImportPathToFilePath(importPath string) (string, ModuleType, error) {
	// Normalize import path to ensure consistent lookup
	importPath = fs.NormalizePath(importPath)

	// 1. First check stdlib/runtime (libs take priority)
	if ctx.Config.RuntimePath != "" {
		relPath := filepath.FromSlash(importPath) + ctx.Config.Extension
		filePath := filepath.Join(ctx.Config.RuntimePath, relPath)
		if fs.IsValidFile(filePath) {
			return filepath.ToSlash(filePath), ModuleBuiltin, nil
		}
	}

	// 2. Check for in-memory modules (playground/WASM)
	if mod, exists := ctx.Modules[importPath]; exists && mod.Content != "" {
		return mod.FilePath, ModuleLocal, nil
	}

	// 3. Check if it's a local project module
	packageName := fs.FirstPart(importPath)
	if packageName == ctx.Config.ProjectName && ctx.Config.ProjectName != "" {
		cleanPath := strings.TrimPrefix(importPath, packageName+"/")

		// Try relative to project root
		filePath := filepath.Join(ctx.Config.ProjectRoot, cleanPath+ctx.Config.Extension)
		if fs.IsValidFile(filePath) {
			return filepath.ToSlash(filePath), ModuleLocal, nil
		}

		// Try relative to entry file's directory
		if ctx.EntryPoint != "" {
			entryDir := filepath.Dir(ctx.EntryPoint)
			filePath = filepath.Join(entryDir, cleanPath+ctx.Config.Extension)
			if fs.IsValidFile(filePath) {
				return filepath.ToSlash(filePath), ModuleLocal, nil
			}
		}
	}

	// 4. Check if it's a remote module (future implementation)
	if ctx.isRemoteImport(importPath) {
		// TODO: Implement remote module resolution
		return "", ModuleRemote, fmt.Errorf("remote imports not yet implemented: %s", importPath)
	}

	return "", ModuleUnknown, fmt.Errorf("cannot resolve import: %s", importPath)
}

// isRemoteImport checks if an import path is a remote module
func (ctx *CompilerContext) isRemoteImport(importPath string) bool {
	return strings.HasPrefix(importPath, "github.com/") ||
		strings.HasPrefix(importPath, "gitlab.com/") ||
		strings.HasPrefix(importPath, "bitbucket.org/")
}

// AddModule registers a module in the context.
func (ctx *CompilerContext) AddModule(importPath string, module *Module) {
	if module == nil {
		panic(fmt.Sprintf("cannot add nil module for %q", importPath))
	}

	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	// Don't overwrite existing modules
	if _, exists := ctx.Modules[importPath]; exists {
		return
	}

	mod := module
	if mod.Artifacts == nil {
		mod.Artifacts = make(map[string]any)
	}
	mod.ImportPath = importPath
	ctx.Modules[importPath] = mod
}

// GetModule retrieves a module by import path.
func (ctx *CompilerContext) GetModule(importPath string) (*Module, bool) {
	ctx.mu.RLock()
	defer ctx.mu.RUnlock()
	module, exists := ctx.Modules[importPath]
	return module, exists
}

// HasModule checks if a module exists in the context
func (ctx *CompilerContext) HasModule(importPath string) bool {
	ctx.mu.RLock()
	defer ctx.mu.RUnlock()
	_, exists := ctx.Modules[importPath]
	return exists
}

// GetModulePhase returns the current phase of a module
func (ctx *CompilerContext) GetModulePhase(importPath string) phase.ModulePhase {
	ctx.mu.RLock()
	defer ctx.mu.RUnlock()
	if module, exists := ctx.Modules[importPath]; exists {
		return module.Phase
	}
	return phase.PhaseNotStarted
}

// SetModulePhase updates the phase of a module
func (ctx *CompilerContext) SetModulePhase(importPath string, phase phase.ModulePhase) {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if module, exists := ctx.Modules[importPath]; exists {
		module.Mu.Lock()
		module.Phase = phase
		module.Mu.Unlock()
	}
}

// AdvanceModulePhase advances a module to the next phase with validation
// Returns false if the phase transition is invalid (prerequisites not met)
func (ctx *CompilerContext) AdvanceModulePhase(importPath string, targetPhase phase.ModulePhase) bool {
	if !ctx.CanProcessPhase(importPath, targetPhase) {
		return false
	}
	ctx.SetModulePhase(importPath, targetPhase)
	return true
}

// CanProcessPhase checks if a module is ready for a specific phase
// Uses explicit prerequisite map for safe phase transitions
func (ctx *CompilerContext) CanProcessPhase(importPath string, requiredPhase phase.ModulePhase) bool {
	currentPhase := ctx.GetModulePhase(importPath)
	prerequisite, exists := phase.PhasePrerequisites[requiredPhase]
	if !exists {
		// Unknown phase - cannot process
		return false
	}
	return currentPhase == prerequisite
}

// IsModuleParsed checks if a module has been parsed (at least)
func (ctx *CompilerContext) IsModuleParsed(importPath string) bool {
	return ctx.GetModulePhase(importPath) >= phase.PhaseParsed
}

// AddDependency registers an import relationship
// Returns error if adding this dependency would create a cycle
func (ctx *CompilerContext) AddDependency(importer, imported string) error {
	// Normalize paths
	importer = filepath.ToSlash(importer)
	imported = filepath.ToSlash(imported)

	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	// Check for cycle before adding
	if cycle := ctx.findCycle(imported, importer); cycle != nil {
		return fmt.Errorf("circular import detected: %s", formatCycle(cycle))
	}

	// Check if dependency already exists (deduplicate)
	for _, existing := range ctx.DepGraph[importer] {
		if existing == imported {
			return nil // Already added, skip
		}
	}

	// Add dependency
	ctx.DepGraph[importer] = append(ctx.DepGraph[importer], imported)
	return nil
}

// findCycle uses DFS to detect if adding edge (from -> to) creates a cycle
func (ctx *CompilerContext) findCycle(from, to string) []string {
	visited := make(map[string]bool)
	path := []string{}

	if ctx.hasCyclePath(from, to, visited, &path) {
		// Construct cycle path
		cycle := append([]string{to}, path...)
		cycle = append(cycle, to)
		return cycle
	}
	return nil
}

// hasCyclePath performs DFS to find path from start to target
func (ctx *CompilerContext) hasCyclePath(start, target string, visited map[string]bool, path *[]string) bool {
	if start == target {
		return true
	}

	if visited[start] {
		return false
	}

	visited[start] = true
	*path = append(*path, start)

	for _, dep := range ctx.DepGraph[start] {
		dep = filepath.ToSlash(dep)
		if ctx.hasCyclePath(dep, target, visited, path) {
			return true
		}
	}

	// Backtrack
	*path = (*path)[:len(*path)-1]
	return false
}

// formatCycle formats a cycle path for error messages
func formatCycle(cycle []string) string {
	parts := make([]string, len(cycle))
	for i, path := range cycle {
		parts[i] = filepath.Base(path)
	}
	return strings.Join(parts, " -> ")
}

// HasErrors returns true if any errors have been reported
func (ctx *CompilerContext) HasErrors() bool {
	return ctx.Diagnostics.HasErrors()
}

// ReportError adds an error diagnostic
func (ctx *CompilerContext) ReportError(message string, location *source.Location) {
	diag := &diagnostics.Diagnostic{
		Severity: diagnostics.Error,
		Message:  message,
		Labels: []diagnostics.Label{
			{Location: location, Message: "", Style: diagnostics.Primary},
		},
	}
	ctx.Diagnostics.Add(diag)
}

// EmitDiagnostics outputs all collected diagnostics
func (ctx *CompilerContext) EmitDiagnostics() {
	ctx.Diagnostics.EmitAll()
}

// ModuleCount returns the number of modules in the context
func (ctx *CompilerContext) ModuleCount() int {
	return len(ctx.Modules)
}

// GetModuleNames returns all module import paths
func (ctx *CompilerContext) GetModuleNames() []string {
	return ctx.sortedModules
}

// ComputeTopologicalOrder computes and stores the topological order of modules
func (ctx *CompilerContext) ComputeTopologicalOrder() {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	inDegree := make(map[string]int)

	for modulePath := range ctx.Modules {
		if _, exists := inDegree[modulePath]; !exists {
			inDegree[modulePath] = 0
		}
	}

	for importer, deps := range ctx.DepGraph {
		for range deps {
			inDegree[importer]++
		}
	}

	queue := make([]string, 0, len(ctx.Modules))
	for module := range ctx.Modules {
		if inDegree[module] == 0 {
			queue = append(queue, module)
		}
	}
	sort.Strings(queue)

	sorted := make([]string, 0, len(ctx.Modules))
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		sorted = append(sorted, current)

		next := make([]string, 0)
		for importer, deps := range ctx.DepGraph {
			for _, dep := range deps {
				if dep == current {
					inDegree[importer]--
					if inDegree[importer] == 0 {
						next = append(next, importer)
					}
				}
			}
		}
		if len(next) > 0 {
			sort.Strings(next)
			queue = append(queue, next...)
		}
	}

	ctx.sortedModules = sorted
}
