package context_v2

import (
	"path/filepath"
	"strings"
	"testing"

	"compiler/internal/frontend/ast"
	"compiler/internal/phase"
	"compiler/internal/semantics/symbols"
	"compiler/internal/semantics/table"
	"compiler/internal/types"
)

func TestNewContext(t *testing.T) {
	config := &Config{
		ProjectName: "testproject",
		ProjectRoot: "/test/path",
		Extension:   ".fer",
	}

	ctx := New(config, false)

	if ctx == nil {
		t.Fatal("Expected non-nil context")
	}

	if ctx.Config.ProjectName != "testproject" {
		t.Errorf("Expected project name 'testproject', got '%s'", ctx.Config.ProjectName)
	}

	if ctx.Universe == nil {
		t.Fatal("Expected universe scope to be initialized")
	}

	// Verify built-in types are registered
	if _, ok := ctx.Universe.Lookup("i32"); !ok {
		t.Error("Expected i32 type to be registered in universe")
	}

	if _, ok := ctx.Universe.Lookup("bool"); !ok {
		t.Error("Expected bool type to be registered in universe")
	}

	if _, ok := ctx.Universe.Lookup("true"); !ok {
		t.Error("Expected true constant to be registered")
	}
	if sym, ok := ctx.Universe.Lookup("stdout"); !ok || sym.Kind != symbols.SymbolConstant {
		t.Error("Expected stdout builtin constant to be registered")
	}
	if sym, ok := ctx.Universe.Lookup("write"); !ok || sym.Kind != symbols.SymbolFunction || !sym.IsNative {
		t.Error("Expected write builtin function to be registered as native")
	}

	for _, handleName := range CompilerBuiltinHandleTypeNames() {
		sym, ok := ctx.Universe.Lookup(handleName)
		if !ok {
			t.Errorf("Expected %s type to be registered in universe", handleName)
			continue
		}
		if sym.Kind != symbols.SymbolType {
			t.Errorf("Expected %s to be a type symbol", handleName)
			continue
		}
		isResource := types.IsResourceType(sym.Type)
		if IsCompilerResourceHandleTypeName(handleName) && !isResource {
			t.Errorf("Expected %s to be marked as resource type", handleName)
		}
		if !IsCompilerResourceHandleTypeName(handleName) && isResource {
			t.Errorf("Expected %s to be a non-resource builtin handle type", handleName)
		}
	}
}

func TestAddModule(t *testing.T) {
	ctx := New(&Config{Extension: ".fer"}, false)

	module := &Module{
		FilePath: "/test/module.fer",
		Type:     ModuleLocal,
		Phase:    phase.PhaseParsed,
	}

	ctx.AddModule("test/module", module)

	if !ctx.HasModule("test/module") {
		t.Error("Expected module to be registered")
	}

	retrieved, ok := ctx.GetModule("test/module")
	if !ok {
		t.Fatal("Expected to retrieve module")
	}

	if retrieved.ImportPath != "test/module" {
		t.Errorf("Expected import path 'test/module', got '%s'", retrieved.ImportPath)
	}

	if retrieved.Phase != phase.PhaseParsed {
		t.Errorf("Expected phase PhaseParsed, got %v", retrieved.Phase)
	}
}

func TestModulePhaseTracking(t *testing.T) {
	ctx := New(&Config{Extension: ".fer"}, false)

	module := &Module{
		FilePath: "/test/module.fer",
		Type:     ModuleLocal,
		Phase:    phase.PhaseNotStarted,
	}

	ctx.AddModule("test/module", module)

	// Test phase progression
	phases := []phase.ModulePhase{
		phase.PhaseNotStarted,
		phase.PhaseLexed,
		phase.PhaseParsed,
		phase.PhaseCollected,
		phase.PhaseResolved,
		phase.PhaseTypeChecked,
		phase.PhaseHIRGenerated,
		phase.PhaseCFGAnalyzed,
		phase.PhaseHIRLowered,
		phase.PhaseMIRGenerated,
		phase.PhaseCodeGen,
	}

	for i, phase := range phases {
		currentPhase := ctx.GetModulePhase("test/module")
		if currentPhase != phase {
			t.Errorf("Expected phase %v, got %v", phase, currentPhase)
		}

		// Test CanProcessPhase
		if i < len(phases)-1 {
			nextPhase := phases[i+1]
			if !ctx.CanProcessPhase("test/module", nextPhase) {
				t.Errorf("Expected to be able to process next phase %v", nextPhase)
			}
		}

		// Advance to next phase
		if i < len(phases)-1 {
			ctx.SetModulePhase("test/module", phases[i+1])
		}
	}
}

func TestIsModuleParsed(t *testing.T) {
	ctx := New(&Config{Extension: ".fer"}, false)

	module := &Module{
		FilePath: "/test/module.fer",
		Type:     ModuleLocal,
		Phase:    phase.PhaseNotStarted,
	}

	ctx.AddModule("test/module", module)

	if ctx.IsModuleParsed("test/module") {
		t.Error("Expected module not to be parsed yet")
	}

	ctx.SetModulePhase("test/module", phase.PhaseParsed)

	if !ctx.IsModuleParsed("test/module") {
		t.Error("Expected module to be parsed")
	}

	// Should still return true for later phases
	ctx.SetModulePhase("test/module", phase.PhaseResolved)

	if !ctx.IsModuleParsed("test/module") {
		t.Error("Expected module to still be considered parsed")
	}
}

func TestCycleDetection(t *testing.T) {
	ctx := New(&Config{Extension: ".fer"}, false)

	// Add modules
	modules := map[string]*Module{
		"a": {FilePath: "/test/a.fer", Type: ModuleLocal, Phase: phase.PhaseParsed},
		"b": {FilePath: "/test/b.fer", Type: ModuleLocal, Phase: phase.PhaseParsed},
		"c": {FilePath: "/test/c.fer", Type: ModuleLocal, Phase: phase.PhaseParsed},
	}

	for path, mod := range modules {
		ctx.AddModule(path, mod)
	}

	// Add valid dependencies: a -> b -> c
	if err := ctx.AddDependency("a", "b"); err != nil {
		t.Errorf("Expected no error for valid dependency, got: %v", err)
	}

	if err := ctx.AddDependency("b", "c"); err != nil {
		t.Errorf("Expected no error for valid dependency, got: %v", err)
	}

	// Try to create cycle: c -> a (would create: a -> b -> c -> a)
	err := ctx.AddDependency("c", "a")
	if err == nil {
		t.Error("Expected error for circular dependency")
	}

	if err != nil && !strings.Contains(err.Error(), "circular import") {
		t.Errorf("Expected 'circular import' in error message, got: %v", err)
	}
}

func TestSymbolTable(t *testing.T) {
	parent := table.NewSymbolTable(nil)
	child := table.NewSymbolTable(parent)

	// Add symbol to parent
	parentSym := &symbols.Symbol{
		Name:     "x",
		Kind:     symbols.SymbolVariable,
		Type:     types.TypeI32,
		Exported: true,
	}

	err := parent.Declare("x", parentSym)
	if err != nil {
		t.Errorf("Expected no error declaring symbol, got: %v", err)
	}

	// Lookup from parent
	found, ok := parent.Lookup("x")
	if !ok {
		t.Error("Expected to find symbol in parent")
	}

	if found.Name != "x" {
		t.Errorf("Expected symbol name 'x', got '%s'", found.Name)
	}

	// Lookup from child (should find in parent)
	_, ok = child.Lookup("x")
	if !ok {
		t.Error("Expected to find parent symbol from child")
	}

	// Try to redeclare in parent (should fail)
	err = parent.Declare("x", &symbols.Symbol{Name: "x", Kind: symbols.SymbolConstant})
	if err == nil {
		t.Error("Expected error when redeclaring symbol")
	}

	// Add symbol to child (shadowing)
	childSym := &symbols.Symbol{
		Name:     "x",
		Kind:     symbols.SymbolConstant,
		Type:     types.TypeI64,
		Exported: false,
	}

	err = child.Declare("x", childSym)
	if err != nil {
		t.Errorf("Expected no error declaring in child scope, got: %v", err)
	}

	// Lookup from child should find child's version
	found, ok = child.Lookup("x")
	if !ok {
		t.Error("Expected to find symbol in child")
	}

	if found.Kind != symbols.SymbolConstant {
		t.Errorf("Expected SymbolConstant, got %v", found.Kind)
	}

	// Parent lookup should still find parent's version
	found, ok = parent.Lookup("x")
	if !ok {
		t.Error("Expected to find symbol in parent")
	}

	if found.Kind != symbols.SymbolVariable {
		t.Errorf("Expected SymbolVariable, got %v", found.Kind)
	}
}

func TestFilePathToImportPath(t *testing.T) {
	ctx := New(&Config{
		ProjectName: "myproject",
		ProjectRoot: "/home/user/projects/myproject",
		Extension:   ".fer",
	}, false)

	tests := []struct {
		name     string
		filePath string
		expected string
	}{
		{
			name:     "module in src directory",
			filePath: "/home/user/projects/myproject/src/utils/math.fer",
			expected: "myproject/src/utils/math",
		},
		{
			name:     "module in root",
			filePath: "/home/user/projects/myproject/main.fer",
			expected: "myproject/main",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ctx.FilePathToImportPath(tt.filePath)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestModuleTypeString(t *testing.T) {
	tests := []struct {
		modType  ModuleType
		expected string
	}{
		{ModuleLocal, "Local"},
		{ModuleBuiltin, "Builtin"},
		{ModuleRemote, "Remote"},
		{ModuleNeighbor, "Neighbor"},
		{ModuleUnknown, "Unknown"},
	}

	for _, tt := range tests {
		if tt.modType.String() != tt.expected {
			t.Errorf("Expected %s, got %s", tt.expected, tt.modType.String())
		}
	}
}

func TestModulePhaseString(t *testing.T) {
	tests := []struct {
		phase    phase.ModulePhase
		expected string
	}{
		{phase.PhaseNotStarted, "NotStarted"},
		{phase.PhaseLexed, "Lexed"},
		{phase.PhaseParsed, "Parsed"},
		{phase.PhaseCollected, "Collected"},
		{phase.PhaseResolved, "Resolved"},
		{phase.PhaseTypeChecked, "TypeChecked"},
		{phase.PhaseHIRGenerated, "HIRGenerated"},
		{phase.PhaseHIRLowered, "HIRLowered"},
		{phase.PhaseMIRGenerated, "MIRGenerated"},
		{phase.PhaseCodeGen, "CodeGen"},
	}

	for _, tt := range tests {
		if tt.phase.String() != tt.expected {
			t.Errorf("Expected %s, got %s", tt.expected, tt.phase.String())
		}
	}
}

func TestAddModuleWithAST(t *testing.T) {
	ctx := New(&Config{Extension: ".fer"}, false)

	astModule := &ast.Module{
		FullPath: "/test/module.fer",
		Nodes:    []ast.Node{},
	}

	module := &Module{
		FilePath:    "/test/module.fer",
		Type:        ModuleLocal,
		Phase:       phase.PhaseParsed,
		AST:         astModule,
		ModuleScope: table.NewSymbolTable(ctx.Universe),
	}

	ctx.AddModule("test/module", module)

	retrieved, ok := ctx.GetModule("test/module")
	if !ok {
		t.Fatal("Expected to retrieve module")
	}

	if retrieved.AST == nil {
		t.Error("Expected AST to be set")
	}

	if retrieved.ModuleScope == nil {
		t.Error("Expected symbol table to be set")
	}
}

func TestIsRemoteImport(t *testing.T) {
	ctx := New(&Config{Extension: ".fer"}, false)

	tests := []struct {
		path     string
		expected bool
	}{
		{"github.com/user/repo", true},
		{"gitlab.com/user/repo", true},
		{"bitbucket.org/user/repo", true},
		{"local/module", false},
		{"std/io", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := ctx.isRemoteImport(tt.path)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestDepGraphNormalization(t *testing.T) {
	ctx := New(&Config{Extension: ".fer"}, false)

	// Add modules with different path separators
	ctx.AddModule("a", &Module{FilePath: "C:\\test\\a.fer", Phase: phase.PhaseParsed})
	ctx.AddModule("b", &Module{FilePath: "C:/test/b.fer", Phase: phase.PhaseParsed})

	// Both should work and be normalized
	err := ctx.AddDependency("C:\\test\\a.fer", "C:/test/b.fer")
	if err != nil {
		t.Errorf("Expected no error with mixed separators, got: %v", err)
	}

	// Check that dependency was added with normalized paths
	normalizedA := filepath.ToSlash("C:\\test\\a.fer")
	if deps, ok := ctx.DepGraph[normalizedA]; !ok || len(deps) != 1 {
		t.Error("Expected dependency to be added with normalized path")
	}
}

func TestComputeTopologicalOrder(t *testing.T) {
	ctx := New(&Config{Extension: ".fer"}, false)

	// Add modules
	ctx.AddModule("a", &Module{FilePath: "a.fer", Phase: phase.PhaseParsed})
	ctx.AddModule("b", &Module{FilePath: "b.fer", Phase: phase.PhaseParsed})
	ctx.AddModule("c", &Module{FilePath: "c.fer", Phase: phase.PhaseParsed})
	ctx.AddModule("d", &Module{FilePath: "d.fer", Phase: phase.PhaseParsed})

	// Add dependencies:
	// a -> b, a -> c, b -> d, c -> d
	ctx.AddDependency("a", "b")
	ctx.AddDependency("a", "c")
	ctx.AddDependency("b", "d")
	ctx.AddDependency("c", "d")

	ctx.ComputeTopologicalOrder()
	order := ctx.GetModuleNames()

	// The only valid topological orders are:
	// d before b and c, b and c before a
	index := func(name string) int {
		for i, v := range order {
			if v == name {
				return i
			}
		}
		return -1
	}

	if !(index("d") < index("b") && index("d") < index("c") &&
		index("b") < index("a") && index("c") < index("a")) {
		t.Errorf("Topological order incorrect: %v", order)
	}

	// All modules should be present
	expected := map[string]bool{"a": true, "b": true, "c": true, "d": true}
	for _, modName := range order {
		if !expected[modName] {
			// may be built-in,
			// check if it's a built-in module
			if mod, ok := ctx.GetModule(modName); ok && mod.Type == ModuleBuiltin {
				continue
			}
			t.Errorf("Unexpected module in order: %s", modName)
		}
		delete(expected, modName)
	}
	for missing := range expected {
		t.Errorf("Missing module in order: %s", missing)
	}
}

func TestComputeTopologicalOrder_NoDependencies(t *testing.T) {
	ctx := New(&Config{Extension: ".fer"}, false)
	ctx.AddModule("x", &Module{FilePath: "x.fer", Phase: phase.PhaseParsed})
	ctx.AddModule("y", &Module{FilePath: "y.fer", Phase: phase.PhaseParsed})

	ctx.ComputeTopologicalOrder()
	order := ctx.GetModuleNames()

	// skip built-in modules
	for i, modName := range order {
		if mod, ok := ctx.GetModule(modName); ok && mod.Type == ModuleBuiltin {
			order = append(order[:i], order[i+1:]...)
		}
	}

	if len(order) != 2 {
		t.Fatalf("Expected 2 modules in order, got %d", len(order))
	}
	found := map[string]bool{}
	for _, mod := range order {
		found[mod] = true
	}
	if !found["x"] || !found["y"] {
		t.Errorf("Expected both 'x' and 'y' in order, got %v", order)
	}
}

func TestComputeTopologicalOrder_SingleModule(t *testing.T) {
	ctx := New(&Config{Extension: ".fer"}, false)
	ctx.AddModule("main", &Module{FilePath: "main.fer", Phase: phase.PhaseParsed})

	ctx.ComputeTopologicalOrder()
	order := ctx.GetModuleNames()

	// skip built-in modules
	for i, modName := range order {
		if mod, ok := ctx.GetModule(modName); ok && mod.Type == ModuleBuiltin {
			order = append(order[:i], order[i+1:]...)
		}
	}

	if len(order) != 1 || order[0] != "main" {
		t.Errorf("Expected ['main'], got %v", order)
	}
}

func TestComputeTopologicalOrder_CycleIgnored(t *testing.T) {
	ctx := New(&Config{Extension: ".fer"}, false)
	ctx.AddModule("a", &Module{FilePath: "a.fer", Phase: phase.PhaseParsed})
	ctx.AddModule("b", &Module{FilePath: "b.fer", Phase: phase.PhaseParsed})

	// Add a dependency a -> b
	if err := ctx.AddDependency("a", "b"); err != nil {
		t.Fatalf("Expected no error for valid dependency a -> b, got: %v", err)
	}

	// Try to add a cycle b -> a (should be rejected)
	err := ctx.AddDependency("b", "a")
	if err == nil {
		t.Error("Expected error when adding cycle b -> a")
	}
	if err != nil && !strings.Contains(err.Error(), "circular import") {
		t.Errorf("Expected 'circular import' error, got: %v", err)
	}

	ctx.ComputeTopologicalOrder()
	order := ctx.GetModuleNames()

	// Verify b appears before a (since a depends on b)
	index := func(name string) int {
		for i, v := range order {
			if v == name {
				return i
			}
		}
		return -1
	}

	if index("b") >= index("a") {
		t.Errorf("Expected 'b' before 'a' in topological order, got %v", order)
	}
}
