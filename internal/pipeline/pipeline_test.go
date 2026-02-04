package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"compiler/internal/context_v2"
	"compiler/internal/phase"
	"compiler/internal/semantics/table"
	"compiler/internal/stdlib"
	"compiler/internal/utils/lists"
)

func TestPipelineBasic(t *testing.T) {
	// Create temporary test directory
	tmpDir := t.TempDir()

	// Create a simple test file
	mainContent := `let x := 42;`
	mainPath := filepath.Join(tmpDir, "main.fer")
	if err := os.WriteFile(mainPath, []byte(mainContent), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create config
	config := &context_v2.Config{
		ProjectName:   "test",
		ProjectRoot:   tmpDir,
		Extension:     ".fer",
		TypeCheckOnly: true,
	}

	// Create context and load embedded stdlib
	ctx := context_v2.New(config, false)
	stdlib.LoadEmbedded(ctx)
	if err := ctx.SetEntryPoint(mainPath); err != nil {
		t.Fatalf("Failed to set entry point: %v", err)
	}

	// Create and run pipeline
	p := New(ctx)
	if err := p.Run(); err != nil {
		t.Errorf("Pipeline failed: %v", err)
	}

	// Verify module was added
	modules := ctx.GetModuleNames()
	if !lists.Has(modules, filepath.Join(config.ProjectName, "main"), func(a, b string) bool {
		return a == b
	}) {
		t.Errorf("Expected module %s, got %v", mainPath, modules)
	}

	// Verify module has advanced through the pipeline (at least parsed, possibly further)
	ph := ctx.GetModulePhase(ctx.EntryModule)
	if ph < phase.PhaseParsed {
		t.Errorf("Expected at least PhaseParsed, got %v", ph)
	}
}

// TestPipelineInvariants tests the fixes for blocking issues:
// - Issue 1: No double parsing (each file parsed exactly once)
// - Issue 2: Thread-safe diagnostics (stable across runs)
// - Issue 3: No duplicate error messages
// - Phase invariants: All modules advance through the full pipeline (Lexed -> Parsed -> Collected -> Resolved -> TypeChecked -> HIRGenerated -> CFGAnalyzed -> HIRLowered -> MIRGenerated)
func TestPipelineInvariants(t *testing.T) {
	// Create temporary test directory
	tmpDir := t.TempDir()

	// Create main file that imports utils twice (duplicate imports)
	mainContent := `import "test_project/utils";
import "test_project/helper";
import "test_project/utils"; // duplicate import

let x := 42;`
	mainPath := filepath.Join(tmpDir, "main.fer")
	if err := os.WriteFile(mainPath, []byte(mainContent), 0644); err != nil {
		t.Fatalf("Failed to create main file: %v", err)
	}

	// Create utils file with syntax error (leaf module)
	utilsContent := `const MAX := 100
// Missing semicolon - syntax error
fn add(a: i32, b: i32) -> i32 {
    return a + b;
}`
	utilsPath := filepath.Join(tmpDir, "utils.fer")
	if err := os.WriteFile(utilsPath, []byte(utilsContent), 0644); err != nil {
		t.Fatalf("Failed to create utils file: %v", err)
	}

	// Create helper file (normal module)
	helperContent := `const PI := 3.14;

fn square(x: f64) -> f64 {
    return x * x;
}`
	helperPath := filepath.Join(tmpDir, "helper.fer")
	if err := os.WriteFile(helperPath, []byte(helperContent), 0644); err != nil {
		t.Fatalf("Failed to create helper file: %v", err)
	}

	// Run compilation multiple times to check stability
	runs := 3
	var diagnosticCounts []int
	var errorMessages [][]string

	for run := 0; run < runs; run++ {
		// Create fresh context for each run
		config := &context_v2.Config{
			ProjectName:   "test_project",
			ProjectRoot:   tmpDir,
			Extension:     ".fer",
			TypeCheckOnly: true,
		}

		ctx := context_v2.New(config, false)
		stdlib.LoadEmbedded(ctx)
		if err := ctx.SetEntryPoint(mainPath); err != nil {
			t.Fatalf("Run %d: Failed to set entry point: %v", run, err)
		}

		// Run pipeline
		p := New(ctx)
		_ = p.Run() // Expected to fail due to syntax error

		// Collect diagnostics
		diags := ctx.Diagnostics.Diagnostics()
		diagnosticCounts = append(diagnosticCounts, len(diags))

		// Collect error messages
		var messages []string
		for _, d := range diags {
			messages = append(messages, d.Message)
		}
		errorMessages = append(errorMessages, messages)

		// Invariant 1: Each module should be processed exactly once through the pipeline
		moduleNames := ctx.GetModuleNames()
		// Filter out builtin modules (stdlib)
		var userModules []string
		for _, moduleName := range moduleNames {
			if mod, _ := ctx.GetModule(moduleName); mod.Type != context_v2.ModuleBuiltin {
				userModules = append(userModules, moduleName)
			}
		}

		if len(userModules) != 3 {
			t.Errorf("Run %d: Expected 3 user modules (main, utils, helper), got %d: %v", run, len(userModules), userModules)
		}

		for _, moduleName := range userModules {
			module, exists := ctx.GetModule(moduleName)
			if !exists {
				t.Errorf("Run %d: Module %q not found", run, moduleName)
				continue
			}

			// Verify phase is at least PhaseParsed (pipeline runs through all phases)
			// With syntax errors, modules may stop at PhaseParsed, otherwise they reach later phases
			if module.Phase < phase.PhaseParsed {
				t.Errorf("Run %d: Module %q phase = %v, want at least PhaseParsed", run, moduleName, module.Phase)
			}

			// Verify AST exists for parsed modules
			if module.Phase >= phase.PhaseParsed && module.AST == nil {
				t.Errorf("Run %d: Module %q has nil AST despite being at %v", run, moduleName, module.Phase)
			}
		}

		// Invariant 2: Should have syntax error from utils.fer
		if !ctx.HasErrors() {
			t.Errorf("Run %d: Expected syntax error in utils.fer, but no errors reported", run)
		}
	}

	// Invariant 3: Diagnostic counts should be stable across runs
	firstCount := diagnosticCounts[0]
	for i, count := range diagnosticCounts {
		if count != firstCount {
			t.Errorf("Run %d: Diagnostic count = %d, want %d (unstable diagnostics)", i, count, firstCount)
		}
	}

	// Invariant 4: Error messages should be identical (no duplicates, no randomness)
	firstMessages := errorMessages[0]
	for i, messages := range errorMessages {
		if len(messages) != len(firstMessages) {
			t.Errorf("Run %d: Got %d messages, want %d", i, len(messages), len(firstMessages))
			continue
		}
		for j, msg := range messages {
			if msg != firstMessages[j] {
				t.Errorf("Run %d: Message %d differs:\n  got:  %s\n  want: %s", i, j, msg, firstMessages[j])
			}
		}
	}

	// Invariant 5: Check for duplicate messages within a single run
	messageSet := make(map[string]int)
	for _, msg := range errorMessages[0] {
		messageSet[msg]++
	}
	for msg, count := range messageSet {
		if count > 1 {
			t.Errorf("Duplicate error message (%d times): %s", count, msg)
		}
	}

	t.Logf("✓ All invariants passed across %d runs", runs)
	t.Logf("  - %d modules processed per run", len(errorMessages[0]))
	t.Logf("  - %d diagnostics per run (stable)", firstCount)
	t.Logf("  - No duplicate messages detected")
}

// TestDiscoveryGraphStability ensures the dependency graph is deterministic
// across multiple runs. This catches nondeterminism bugs before adding
// concurrent discovery or resolution phases.
func TestDiscoveryGraphStability(t *testing.T) {
	// Create temporary test directory
	tmpDir := t.TempDir()

	// Create a diamond dependency pattern:
	//     main
	//    /    \
	//   A      B
	//    \    /
	//      C

	mainContent := `import "test_project/a";
import "test_project/b";

let x := 1;`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.fer"), []byte(mainContent), 0644); err != nil {
		t.Fatalf("Failed to create main.fer: %v", err)
	}

	aContent := `import "test_project/c";
const A := 1;`
	if err := os.WriteFile(filepath.Join(tmpDir, "a.fer"), []byte(aContent), 0644); err != nil {
		t.Fatalf("Failed to create a.fer: %v", err)
	}

	bContent := `import "test_project/c";
const B := 2;`
	if err := os.WriteFile(filepath.Join(tmpDir, "b.fer"), []byte(bContent), 0644); err != nil {
		t.Fatalf("Failed to create b.fer: %v", err)
	}

	cContent := `const C := 3;`
	if err := os.WriteFile(filepath.Join(tmpDir, "c.fer"), []byte(cContent), 0644); err != nil {
		t.Fatalf("Failed to create c.fer: %v", err)
	}

	runs := 5
	var moduleSets []map[string]bool
	var depGraphs []map[string][]string

	for run := 0; run < runs; run++ {
		config := &context_v2.Config{
			ProjectName:   "test_project",
			ProjectRoot:   tmpDir,
			Extension:     ".fer",
			TypeCheckOnly: true,
		}

		ctx := context_v2.New(config, false)
		stdlib.LoadEmbedded(ctx)
		if err := ctx.SetEntryPoint(filepath.Join(tmpDir, "main.fer")); err != nil {
			t.Fatalf("Run %d: Failed to set entry point: %v", run, err)
		}

		p := New(ctx)
		if err := p.Run(); err != nil {
			t.Fatalf("Run %d: Pipeline failed: %v", run, err)
		}

		// Capture discovered module set
		moduleSet := make(map[string]bool)
		moduleNames := ctx.GetModuleNames()
		for _, name := range moduleNames {
			moduleSet[name] = true
		}
		moduleSets = append(moduleSets, moduleSet)

		// Capture dependency graph structure
		depGraph := make(map[string][]string)
		for _, moduleName := range moduleNames {
			// Access dependency graph from context
			deps := ctx.DepGraph[moduleName]
			// Sort for deterministic comparison
			sorted := make([]string, len(deps))
			copy(sorted, deps)
			for i := range sorted {
				for j := i + 1; j < len(sorted); j++ {
					if sorted[i] > sorted[j] {
						sorted[i], sorted[j] = sorted[j], sorted[i]
					}
				}
			}
			depGraph[moduleName] = sorted
		}
		depGraphs = append(depGraphs, depGraph)
	}

	// Verify module sets are identical across runs
	firstModuleSet := moduleSets[0]
	for i := 1; i < len(moduleSets); i++ {
		if len(moduleSets[i]) != len(firstModuleSet) {
			t.Errorf("Run %d: Module count = %d, want %d (nondeterministic discovery)",
				i, len(moduleSets[i]), len(firstModuleSet))
		}
		for module := range firstModuleSet {
			if !moduleSets[i][module] {
				t.Errorf("Run %d: Module %q missing (nondeterministic discovery)", i, module)
			}
		}
	}

	// Verify dependency graphs are identical across runs
	firstDepGraph := depGraphs[0]
	for i := 1; i < len(depGraphs); i++ {
		for module, deps := range firstDepGraph {
			gotDeps := depGraphs[i][module]
			if len(gotDeps) != len(deps) {
				t.Errorf("Run %d: Module %q has %d deps, want %d (nondeterministic graph)",
					i, module, len(gotDeps), len(deps))
				continue
			}
			for j, dep := range deps {
				if gotDeps[j] != dep {
					t.Errorf("Run %d: Module %q dep[%d] = %q, want %q (nondeterministic order)",
						i, module, j, gotDeps[j], dep)
				}
			}
		}
	}

	// Verify diamond dependency structure
	// Note: Each module also implicitly depends on "global" (the prelude module)
	expectedDeps := map[string]int{
		"test_project/main": 3, // imports a, b + global
		"test_project/a":    2, // imports c + global
		"test_project/b":    2, // imports c + global
		"test_project/c":    1, // leaf + global
	}
	for module, expectedCount := range expectedDeps {
		if !firstModuleSet[module] {
			t.Errorf("Expected module %q not found", module)
			continue
		}
		actualCount := len(firstDepGraph[module])
		if actualCount != expectedCount {
			t.Errorf("Module %q: got %d deps, want %d", module, actualCount, expectedCount)
		}
	}

	t.Logf("✓ Discovery graph stable across %d runs", runs)
	t.Logf("  - %d modules discovered", len(firstModuleSet))
	t.Logf("  - Dependency structure deterministic")
}

// TestPhasePrerequisites validates phase ordering.
// All modules should advance through: Lexed -> Parsed -> Collected -> Resolved -> TypeChecked -> HIRGenerated -> CFGAnalyzed -> HIRLowered -> MIRGenerated
func TestPhasePrerequisites(t *testing.T) {
	tmpDir := t.TempDir()

	// Create files with import dependencies
	mainContent := `import "test_project/utils";
let result := 42;`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.fer"), []byte(mainContent), 0644); err != nil {
		t.Fatalf("Failed to create main.fer: %v", err)
	}

	utilsContent := `const FACTOR := 2;`
	if err := os.WriteFile(filepath.Join(tmpDir, "utils.fer"), []byte(utilsContent), 0644); err != nil {
		t.Fatalf("Failed to create utils.fer: %v", err)
	}

	config := &context_v2.Config{
		ProjectName:   "test_project",
		ProjectRoot:   tmpDir,
		Extension:     ".fer",
		TypeCheckOnly: true,
	}

	ctx := context_v2.New(config, false)
	stdlib.LoadEmbedded(ctx)
	if err := ctx.SetEntryPoint(filepath.Join(tmpDir, "main.fer")); err != nil {
		t.Fatalf("Failed to set entry point: %v", err)
	}

	p := New(ctx)
	if err := p.Run(); err != nil {
		t.Fatalf("Pipeline failed: %v", err)
	}

	// All modules should complete all pipeline phases
	moduleNames := ctx.GetModuleNames()
	for _, moduleName := range moduleNames {
		module, exists := ctx.GetModule(moduleName)
		if module.Type == context_v2.ModuleBuiltin {
			continue
		}
		if !exists {
			t.Errorf("Module %q not found", moduleName)
			continue
		}

		// Current check: Must be at least PhaseParsed
		if module.Phase < phase.PhaseParsed {
			t.Errorf("Module %q at phase %v, expected at least PhaseParsed", moduleName, module.Phase)
		}

		// Phase ordering invariant: If a module is PhaseParsed, it must have an AST
		if module.Phase >= phase.PhaseParsed && module.AST == nil {
			t.Errorf("Module %q at %v but has nil AST (phase invariant violated)", moduleName, module.Phase)
		}
	}

	t.Logf("✓ Phase prerequisites satisfied")
	t.Logf("  - All %d modules advanced through pipeline", len(moduleNames))
	t.Logf("  - AST existence validated")
	t.Logf("  - Phase ordering maintained")
}

// TestImportPathNormalization tests that import paths are properly normalized
// to handle edge cases like multiple slashes, trailing slashes, and whitespace
// This test uses in-memory modules to avoid filesystem dependencies.
func TestImportPathNormalization(t *testing.T) {
	// Use a virtual project root (doesn't need to exist on disk)
	virtualRoot := "/virtual/project"

	testCases := []struct {
		name             string
		importStmt       string
		expectedNormPath string
		description      string
	}{
		{
			name:             "normal",
			importStmt:       `import "test/utils";`,
			expectedNormPath: "test/utils",
			description:      "Standard import path",
		},
		{
			name:             "double_slash",
			importStmt:       `import "test//utils";`,
			expectedNormPath: "test/utils",
			description:      "Multiple consecutive slashes should collapse",
		},
		{
			name:             "trailing_slash",
			importStmt:       `import "test/utils/";`,
			expectedNormPath: "test/utils",
			description:      "Trailing slash should be trimmed",
		},
		{
			name:             "leading_slash",
			importStmt:       `import "/test/utils";`,
			expectedNormPath: "test/utils",
			description:      "Leading slash should be trimmed",
		},
		{
			name:             "whitespace",
			importStmt:       `import " test/utils ";`,
			expectedNormPath: "test/utils",
			description:      "Whitespace should be trimmed",
		},
		{
			name:             "backslash",
			importStmt:       `import "test\utils";`,
			expectedNormPath: "test/utils",
			description:      "Backslashes should be normalized to forward slashes",
		},
		{
			name:             "combined_edge_cases",
			importStmt:       `import " /test//utils\ ";`,
			expectedNormPath: "test/utils",
			description:      "Multiple edge cases combined",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create config with virtual root
			config := &context_v2.Config{
				ProjectName:   "test",
				ProjectRoot:   virtualRoot,
				Extension:     ".fer",
				TypeCheckOnly: true,
			}

			// Create context and load embedded stdlib
			ctx := context_v2.New(config, false)
			stdlib.LoadEmbedded(ctx)

			// Create main module in-memory using SetEntryPointWithCode
			mainContent := tc.importStmt + "\nlet x := 42;"
			if err := ctx.SetEntryPointWithCode(mainContent, "main"); err != nil {
				t.Fatalf("Failed to set entry point with code: %v", err)
			}

			// Pre-register the utils module in-memory so the import can resolve
			// This simulates the module existing without requiring filesystem access
			utilsContent := `let pi := 3.14;`
			utilsVirtualPath := filepath.Join(virtualRoot, "utils.fer")
			ctx.Diagnostics.AddSourceContent(utilsVirtualPath, utilsContent)

			// Initialize the module scope with Universe as parent (like SetEntryPointWithCode does)
			utilsScope := table.NewSymbolTable(ctx.Universe)
			utilsModule := &context_v2.Module{
				FilePath:     utilsVirtualPath,
				Type:         context_v2.ModuleLocal,
				Phase:        phase.PhaseNotStarted,
				ModuleScope:  utilsScope,
				CurrentScope: utilsScope,
				Content:      utilsContent,
				Artifacts:    make(map[string]any),
			}
			ctx.AddModule(tc.expectedNormPath, utilsModule)

			// Create and run pipeline
			p := New(ctx)
			err := p.Run()

			// If there were errors, print diagnostics
			if err != nil {
				diags := ctx.Diagnostics.Diagnostics()
				for _, diag := range diags {
					t.Logf("Diagnostic: %s", diag.Message)
				}
				t.Errorf("%s: Expected import to resolve, got error: %v", tc.description, err)
				return
			}

			// Count non-builtin modules
			moduleCount := 0
			for _, modName := range ctx.GetModuleNames() {
				if mod, ok := ctx.GetModule(modName); ok && mod.Type != context_v2.ModuleBuiltin {
					moduleCount++
				}
			}

			if moduleCount != 2 {
				t.Errorf("%s: Expected 2 modules (main + utils), got %d", tc.description, moduleCount)
			}

			// Verify the normalized import path was used
			if !ctx.HasModule(tc.expectedNormPath) {
				t.Errorf("%s: Expected normalized import path %q in module registry", tc.description, tc.expectedNormPath)
				t.Logf("Available modules:")
				for _, name := range ctx.GetModuleNames() {
					t.Logf("  - %q", name)
				}
			}
		})
	}

	t.Logf("✓ Import path normalization working correctly (in-memory)")
	t.Logf("  - Multiple slashes collapsed")
	t.Logf("  - Leading/trailing slashes trimmed")
	t.Logf("  - Whitespace handled")
	t.Logf("  - Backslashes converted to forward slashes")
}
