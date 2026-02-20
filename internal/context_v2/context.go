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
	"compiler/internal/manifest"
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
	InErrorPropagate          bool             // True when type-checking inner expr of !! (skip UncaughtError)

	Imports           map[string]*Import               // Resolved imports
	ImportAliasMap    map[string]string                // alias/name -> import path mapping for module access
	ExprTypes         map[ast.Expression]types.SemType // Type of each expression (filled during type checking)
	NarrowedExprTypes map[string]types.SemType         // Narrowed type for expression keys during type checking
	CallResolvedArgs  map[*ast.CallExpr][]ast.Expression
	PipeResolvedArgs  map[*ast.PipeExpr][]ast.Expression
	GenericCallInfo   map[*ast.CallExpr]*GenericCallInfo
	PipeGenericCalls  map[*ast.PipeExpr]*GenericCallInfo
	GenericFuncInsts  map[string]*GenericFunctionInstantiation

	// Source metadata
	Content string // Raw source code (for diagnostics)

	// Artifacts for downstream phases (codegen, etc.)
	Artifacts map[string]any

	// Concurrency control
	Mu sync.Mutex // Protects field updates during parallel parsing
}

// GenericCallInfo describes how a generic call was instantiated at type-check time.
type GenericCallInfo struct {
	TargetName string
	FuncType   *types.FunctionType
}

// GenericFunctionInstantiation describes one concrete specialization that must be emitted.
type GenericFunctionInstantiation struct {
	Name     string
	Decl     *ast.FuncDecl
	TypeArgs []types.SemType
	FuncType *types.FunctionType
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

// SetCallResolvedArgs records the effective argument list for a call after
// default-parameter resolution.
func (mod *Module) SetCallResolvedArgs(call *ast.CallExpr, args []ast.Expression) {
	if mod == nil || call == nil {
		return
	}

	mod.Mu.Lock()
	defer mod.Mu.Unlock()

	if mod.CallResolvedArgs == nil {
		mod.CallResolvedArgs = make(map[*ast.CallExpr][]ast.Expression)
	}
	if args == nil {
		delete(mod.CallResolvedArgs, call)
		return
	}
	copied := make([]ast.Expression, len(args))
	copy(copied, args)
	mod.CallResolvedArgs[call] = copied
}

// CallArgs returns the resolved argument list for a call, if any.
func (mod *Module) CallArgs(call *ast.CallExpr) ([]ast.Expression, bool) {
	if mod == nil || call == nil {
		return nil, false
	}

	mod.Mu.Lock()
	defer mod.Mu.Unlock()

	if mod.CallResolvedArgs == nil {
		return nil, false
	}
	args, ok := mod.CallResolvedArgs[call]
	if !ok {
		return nil, false
	}
	copied := make([]ast.Expression, len(args))
	copy(copied, args)
	return copied, true
}

// SetPipeResolvedArgs records the effective lowered call arguments for a pipe expression.
func (mod *Module) SetPipeResolvedArgs(pipe *ast.PipeExpr, args []ast.Expression) {
	if mod == nil || pipe == nil {
		return
	}

	mod.Mu.Lock()
	defer mod.Mu.Unlock()

	if mod.PipeResolvedArgs == nil {
		mod.PipeResolvedArgs = make(map[*ast.PipeExpr][]ast.Expression)
	}
	if args == nil {
		delete(mod.PipeResolvedArgs, pipe)
		return
	}
	copied := make([]ast.Expression, len(args))
	copy(copied, args)
	mod.PipeResolvedArgs[pipe] = copied
}

// PipeArgs returns the resolved lowered-call arguments for a pipe expression, if any.
func (mod *Module) PipeArgs(pipe *ast.PipeExpr) ([]ast.Expression, bool) {
	if mod == nil || pipe == nil {
		return nil, false
	}

	mod.Mu.Lock()
	defer mod.Mu.Unlock()

	if mod.PipeResolvedArgs == nil {
		return nil, false
	}
	args, ok := mod.PipeResolvedArgs[pipe]
	if !ok {
		return nil, false
	}
	copied := make([]ast.Expression, len(args))
	copy(copied, args)
	return copied, true
}

// SetPipeGenericCallInfo records generic call rewrite/type info for pipe expressions
// that lower through a synthesized call shape.
func (mod *Module) SetPipeGenericCallInfo(pipe *ast.PipeExpr, info *GenericCallInfo) {
	if mod == nil || pipe == nil {
		return
	}

	mod.Mu.Lock()
	defer mod.Mu.Unlock()

	if mod.PipeGenericCalls == nil {
		mod.PipeGenericCalls = make(map[*ast.PipeExpr]*GenericCallInfo)
	}
	if info == nil {
		delete(mod.PipeGenericCalls, pipe)
		return
	}
	cp := *info
	mod.PipeGenericCalls[pipe] = &cp
}

// PipeGenericCall returns generic call rewrite/type info for a pipe expression, if any.
func (mod *Module) PipeGenericCall(pipe *ast.PipeExpr) (*GenericCallInfo, bool) {
	if mod == nil || pipe == nil {
		return nil, false
	}

	mod.Mu.Lock()
	defer mod.Mu.Unlock()

	if mod.PipeGenericCalls == nil {
		return nil, false
	}
	info, ok := mod.PipeGenericCalls[pipe]
	if !ok || info == nil {
		return nil, false
	}
	cp := *info
	return &cp, true
}

// SetGenericCallInfo records call-site rewrite/type info for a generic call.
func (mod *Module) SetGenericCallInfo(call *ast.CallExpr, info *GenericCallInfo) {
	if mod == nil || call == nil {
		return
	}

	mod.Mu.Lock()
	defer mod.Mu.Unlock()

	if mod.GenericCallInfo == nil {
		mod.GenericCallInfo = make(map[*ast.CallExpr]*GenericCallInfo)
	}
	if info == nil {
		delete(mod.GenericCallInfo, call)
		return
	}
	cp := *info
	mod.GenericCallInfo[call] = &cp
}

// GenericCall returns generic call-site info, if present.
func (mod *Module) GenericCall(call *ast.CallExpr) (*GenericCallInfo, bool) {
	if mod == nil || call == nil {
		return nil, false
	}

	mod.Mu.Lock()
	defer mod.Mu.Unlock()

	if mod.GenericCallInfo == nil {
		return nil, false
	}
	info, ok := mod.GenericCallInfo[call]
	if !ok || info == nil {
		return nil, false
	}
	cp := *info
	return &cp, true
}

// RegisterGenericFunctionInstantiation records one concrete specialization to emit.
func (mod *Module) RegisterGenericFunctionInstantiation(inst *GenericFunctionInstantiation) {
	if mod == nil || inst == nil || inst.Name == "" {
		return
	}

	mod.Mu.Lock()
	defer mod.Mu.Unlock()

	if mod.GenericFuncInsts == nil {
		mod.GenericFuncInsts = make(map[string]*GenericFunctionInstantiation)
	}
	if _, exists := mod.GenericFuncInsts[inst.Name]; exists {
		return
	}
	cp := *inst
	if inst.TypeArgs != nil {
		cp.TypeArgs = make([]types.SemType, len(inst.TypeArgs))
		copy(cp.TypeArgs, inst.TypeArgs)
	}
	mod.GenericFuncInsts[inst.Name] = &cp
}

// GenericFunctionInstantiations returns all concrete specializations for this module.
func (mod *Module) GenericFunctionInstantiations() []*GenericFunctionInstantiation {
	if mod == nil {
		return nil
	}

	mod.Mu.Lock()
	defer mod.Mu.Unlock()

	if len(mod.GenericFuncInsts) == 0 {
		return nil
	}

	keys := make([]string, 0, len(mod.GenericFuncInsts))
	for name := range mod.GenericFuncInsts {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	out := make([]*GenericFunctionInstantiation, 0, len(keys))
	for _, name := range keys {
		inst := mod.GenericFuncInsts[name]
		if inst == nil {
			continue
		}
		cp := *inst
		if inst.TypeArgs != nil {
			cp.TypeArgs = make([]types.SemType, len(inst.TypeArgs))
			copy(cp.TypeArgs, inst.TypeArgs)
		}
		out = append(out, &cp)
	}

	return out
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

	// Package manifest
	Manifest *manifest.PackageManifest // Loaded from fer.toml

	// Universe scope: built-in types and functions
	Universe *table.SymbolTable

	// Diagnostics: centralized error collection
	Diagnostics *diagnostics.DiagnosticBag

	// Track emitted shadowing warnings to avoid duplicates
	shadowingWarned map[string]bool

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
	ProjectName string // Name of the project
	ProjectRoot string // Root directory of the project

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
		Modules:         make(map[string]*Module),
		sortedModules:   []string{},
		Universe:        universe,
		Diagnostics:     diagnostics.NewDiagnosticBag(""),
		DepGraph:        make(map[string][]string),
		Config:          config,
		shadowingWarned: make(map[string]bool),
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

	// Compiler-owned resource handle types preserve nominal identity while
	// keeping an i64 ABI layout. Stream handles are copyable (non-resource).
	for _, name := range CompilerBuiltinHandleTypeNames() {
		handleType := types.NewNamed(name, types.TypeI64)
		if IsCompilerResourceHandleTypeName(name) {
			handleType = types.NewResourceNamed(name, types.TypeI64)
		}
		universe.Declare(name, &symbols.Symbol{
			Name:     name,
			Kind:     symbols.SymbolType,
			Type:     handleType,
			Exported: false,
		})
	}

	streamSym, ok := universe.Lookup("__stream")
	if ok && streamSym != nil && streamSym.Kind == symbols.SymbolType && streamSym.Type != nil {
		streamType := streamSym.Type
		byteSliceType := types.NewArray(types.TypeByte, -1)

		declareBuiltinConst(universe, "stdin", streamType, "0")
		declareBuiltinConst(universe, "stdout", streamType, "1")
		declareBuiltinConst(universe, "stderr", streamType, "2")

		declareBuiltinNativeFunc(
			universe,
			"write",
			"ferret_global_write",
			[]types.ParamType{
				{Name: "stream", Type: streamType},
				{Name: "data", Type: byteSliceType},
			},
			types.NewResult(types.TypeI32, types.TypeString),
		)
		declareBuiltinNativeFunc(
			universe,
			"read",
			"ferret_global_read",
			[]types.ParamType{
				{Name: "stream", Type: streamType},
				{Name: "maxBytes", Type: types.TypeI32},
			},
			types.NewResult(byteSliceType, types.TypeString),
		)
		declareBuiltinNativeFunc(
			universe,
			"flush",
			"ferret_global_flush",
			[]types.ParamType{{Name: "stream", Type: streamType}},
			types.NewResult(types.TypeBool, types.TypeString),
		)
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

type builtinConstLiteral struct {
	literal string
}

func (c builtinConstLiteral) IsConstant() bool {
	return true
}

func (c builtinConstLiteral) String() string {
	return c.literal
}

func declareBuiltinConst(universe *table.SymbolTable, name string, typ types.SemType, literal string) {
	if universe == nil || name == "" || typ == nil {
		return
	}
	_ = universe.Declare(name, &symbols.Symbol{
		Name:       name,
		Kind:       symbols.SymbolConstant,
		Type:       typ,
		Exported:   true,
		ConstValue: builtinConstLiteral{literal: literal},
	})
}

func declareBuiltinNativeFunc(universe *table.SymbolTable, name, nativeName string, params []types.ParamType, ret types.SemType) {
	if universe == nil || name == "" || nativeName == "" || ret == nil {
		return
	}
	_ = universe.Declare(name, &symbols.Symbol{
		Name:       name,
		Kind:       symbols.SymbolFunction,
		Type:       types.NewFunction(params, ret),
		Exported:   true,
		IsNative:   true,
		NativeName: nativeName,
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

	// Try to load package manifest (fer.toml)
	if err := ctx.LoadManifest(); err == nil {
		// Manifest found - use package name from manifest
		if ctx.Manifest != nil && ctx.Manifest.Package.Name != "" {
			ctx.Config.ProjectName = ctx.Manifest.Package.Name
		}
	}
	// If no manifest found, that's okay - continue without it

	// Derive import path from file path
	ctx.EntryModule = ctx.FilePathToImportPath(absPath)

	return nil
}

// LoadManifest attempts to load fer.toml from the project directory
func (ctx *CompilerContext) LoadManifest() error {
	// Find fer.toml starting from entry point's directory
	startDir := filepath.Dir(ctx.EntryPoint)
	manifestPath, err := manifest.FindManifest(startDir)
	if err != nil {
		return err // No manifest found
	}

	// Load and parse manifest
	m, err := manifest.LoadManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	ctx.Manifest = m

	// Update project root to manifest's directory
	ctx.Config.ProjectRoot = filepath.Dir(manifestPath)

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

// FilePathToImportPath converts a file path to a logical import path.
// Returns the file path without extension for file-based imports.
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
		// If not under project root, use the filename without extension
		return strings.TrimSuffix(filepath.Base(filePath), ctx.Config.Extension)
	}

	relPath = filepath.ToSlash(relPath)

	// Remove extension
	relPath = strings.TrimSuffix(relPath, ctx.Config.Extension)

	// Build import path: projectname/path/to/file
	if ctx.Config.ProjectName != "" {
		return ctx.Config.ProjectName + "/" + relPath
	}
	return relPath
}

// CheckModuleShadowing warns if a local module would shadow a stdlib module.
// This is called when we find a stdlib module and want to check if the user
// has a local file with the same import path that is being shadowed.
func (ctx *CompilerContext) CheckModuleShadowing(importPath string, loc *source.Location) {
	// Skip if already warned about this path
	ctx.mu.Lock()
	if ctx.shadowingWarned[importPath] {
		ctx.mu.Unlock()
		return
	}
	ctx.shadowingWarned[importPath] = true
	ctx.mu.Unlock()

	// Check if project name matches the first part of the import path
	// e.g., project "os" importing "os/path" - check if local os/path.fer exists
	packageName := fs.FirstPart(importPath)
	if packageName != ctx.Config.ProjectName || ctx.Config.ProjectName == "" {
		return
	}

	// Strip the project prefix to get the relative path
	cleanPath := strings.TrimPrefix(importPath, packageName+"/")
	if cleanPath == importPath {
		// No prefix stripped, this is a top-level module like "os"
		cleanPath = ""
	}

	// Check if a local file exists with this path
	var localPath string
	if cleanPath != "" {
		localPath = filepath.Join(ctx.Config.ProjectRoot, cleanPath+ctx.Config.Extension)
	} else {
		// Top-level module - check for project_name.fer in project root
		localPath = filepath.Join(ctx.Config.ProjectRoot, ctx.Config.ProjectName+ctx.Config.Extension)
	}

	if fs.IsValidFile(localPath) {
		warning := diagnostics.NewWarning(fmt.Sprintf(
			"local module %q is overridden by language module with the same import path",
			importPath,
		))
		if loc != nil {
			warning = warning.WithPrimaryLabel(loc, "language module takes priority")
		}
		ctx.Diagnostics.Add(warning)
	}
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
		if imported == GlobalModuleImport {
			// Allow implicit global prelude dependency to be skipped when it would
			// create a cycle with modules that global itself imports.
			return nil
		}
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
