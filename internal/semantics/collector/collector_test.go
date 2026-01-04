package collector

import (
	"compiler/internal/context_v2"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/lexer"
	"compiler/internal/frontend/parser"
	"compiler/internal/semantics/table"
	"strings"
	"testing"
)

// Helper to create a test context
func createTestContext() *context_v2.CompilerContext {
	return &context_v2.CompilerContext{
		Diagnostics: diagnostics.NewDiagnosticBag("test.fer"),
		Universe:    table.NewSymbolTable(nil),
	}
}

// Helper to parse and collect symbols from source
func parseAndCollect(t *testing.T, src string) (*context_v2.Module, *context_v2.CompilerContext) {
	t.Helper()

	ctx := createTestContext()
	mod := &context_v2.Module{
		FilePath:       "test.fer",
		Content:        src,
		ModuleScope:    table.NewSymbolTable(ctx.Universe),
		ImportPath:     "test",
		Imports:        make(map[string]*context_v2.Import),
		ImportAliasMap: make(map[string]string),
	}
	mod.CurrentScope = mod.ModuleScope

	diag := diagnostics.NewDiagnosticBag(mod.FilePath)

	lex := lexer.New(mod.FilePath, src, diag)
	tokens := lex.Tokenize(false)

	astMod := parser.Parse(tokens, mod.FilePath, diag)
	mod.AST = astMod

	// Run collector
	CollectModule(ctx, mod)

	return mod, ctx
}

// Test no duplicate aliases - should pass
func TestImportAliasNoDuplicates(t *testing.T) {
	src := `
import "std/math";
import "std/io";
import "test_project/utils";
`

	mod, ctx := parseAndCollect(t, src)

	// Should have 3 imports
	if len(mod.Imports) != 3 {
		t.Errorf("Expected 3 imports, got %d", len(mod.Imports))
	}

	// Should have 3 aliases in the map
	if len(mod.ImportAliasMap) != 3 {
		t.Errorf("Expected 3 aliases in map, got %d", len(mod.ImportAliasMap))
	}

	// Verify the aliases map to correct paths
	expectedMappings := map[string]string{
		"math":  "std/math",
		"io":    "std/io",
		"utils": "test_project/utils",
	}

	for alias, expectedPath := range expectedMappings {
		if actualPath, ok := mod.ImportAliasMap[alias]; !ok {
			t.Errorf("Alias %q not found in map", alias)
		} else if actualPath != expectedPath {
			t.Errorf("Alias %q maps to %q, expected %q", alias, actualPath, expectedPath)
		}
	}

	// Should have no diagnostics
	diags := ctx.Diagnostics.Diagnostics()
	if len(diags) > 0 {
		t.Errorf("Expected no diagnostics, got %d", len(diags))
		for _, d := range diags {
			t.Logf("  %s", d.Message)
		}
	}
}

// Test explicit alias duplicates - should error
func TestImportAliasDuplicateExplicit(t *testing.T) {
	src := `
import "std/math" as m;
import "std/io" as m;  // duplicate alias 'm'
`

	mod, ctx := parseAndCollect(t, src)

	// Should have 2 imports collected
	if len(mod.Imports) != 2 {
		t.Errorf("Expected 2 imports, got %d", len(mod.Imports))
	}

	// Should have only 1 alias in the map (first one wins, second is skipped)
	if len(mod.ImportAliasMap) != 1 {
		t.Errorf("Expected 1 alias in map (duplicate rejected), got %d", len(mod.ImportAliasMap))
	}

	// Should have exactly 1 diagnostic about duplicate alias
	diags := ctx.Diagnostics.Diagnostics()
	if len(diags) != 1 {
		t.Fatalf("Expected 1 diagnostic, got %d", len(diags))
	}

	diag := diags[0]
	if !strings.Contains(diag.Message, "duplicate import alias") {
		t.Errorf("Expected 'duplicate import alias' error, got: %s", diag.Message)
	}

	if !strings.Contains(diag.Message, "'m'") {
		t.Errorf("Expected error to mention alias 'm', got: %s", diag.Message)
	}

	// Verify the error has the correct code
	if diag.Code != diagnostics.ErrRedeclaredSymbol {
		t.Errorf("Expected error code %s, got: %s", diagnostics.ErrRedeclaredSymbol, diag.Code)
	}
}

func TestNestedFunctionDeclDisallowed(t *testing.T) {
	src := `
fn outer() {
	fn inner() { }
}
`

	_, ctx := parseAndCollect(t, src)

	diags := ctx.Diagnostics.Diagnostics()
	if len(diags) == 0 {
		t.Fatalf("expected diagnostic for nested function declaration")
	}

	found := false
	for _, d := range diags {
		if d.Severity == diagnostics.Error && strings.Contains(d.Message, "only allowed at top level") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected nested function declaration error, got %d diagnostics", len(diags))
	}
}

// Test default name collision - should error
func TestImportAliasDefaultNameCollision(t *testing.T) {
	src := `
import "std/math";
import "other_lib/math";  // both default to 'math'
`

	mod, ctx := parseAndCollect(t, src)

	// Should have 2 imports collected
	if len(mod.Imports) != 2 {
		t.Errorf("Expected 2 imports, got %d", len(mod.Imports))
	}

	// Should have only 1 alias in the map (first one wins)
	if len(mod.ImportAliasMap) != 1 {
		t.Errorf("Expected 1 alias in map (duplicate rejected), got %d", len(mod.ImportAliasMap))
	}

	// Should have exactly 1 diagnostic
	diags := ctx.Diagnostics.Diagnostics()
	if len(diags) != 1 {
		t.Fatalf("Expected 1 diagnostic, got %d", len(diags))
	}

	diag := diags[0]
	if !strings.Contains(diag.Message, "duplicate import alias") {
		t.Errorf("Expected 'duplicate import alias' error, got: %s", diag.Message)
	}

	if !strings.Contains(diag.Message, "'math'") {
		t.Errorf("Expected error to mention 'math', got: %s", diag.Message)
	}
}

// Test mixed collision (explicit alias conflicts with default name)
func TestImportAliasMixedCollision(t *testing.T) {
	src := `
import "std/math";          // defaults to 'math'
import "std/io" as math;    // explicit alias 'math' conflicts
`

	mod, ctx := parseAndCollect(t, src)

	// Should have 2 imports
	if len(mod.Imports) != 2 {
		t.Errorf("Expected 2 imports, got %d", len(mod.Imports))
	}

	// Should have only 1 alias in the map
	if len(mod.ImportAliasMap) != 1 {
		t.Errorf("Expected 1 alias in map (duplicate rejected), got %d", len(mod.ImportAliasMap))
	}

	// Should have exactly 1 diagnostic
	diags := ctx.Diagnostics.Diagnostics()
	if len(diags) != 1 {
		t.Fatalf("Expected 1 diagnostic, got %d", len(diags))
	}

	diag := diags[0]
	if !strings.Contains(diag.Message, "duplicate import alias") {
		t.Errorf("Expected 'duplicate import alias' error, got: %s", diag.Message)
	}
}

// Test multiple duplicates
func TestImportAliasMultipleDuplicates(t *testing.T) {
	src := `
import "std/math" as m;
import "std/io" as m;      // duplicate 'm'
import "std/net" as n;
import "std/http" as n;    // duplicate 'n'
`

	mod, ctx := parseAndCollect(t, src)

	// Should have 4 imports
	if len(mod.Imports) != 4 {
		t.Errorf("Expected 4 imports, got %d", len(mod.Imports))
	}

	// Should have only 2 aliases in map (first 'm' and first 'n')
	if len(mod.ImportAliasMap) != 2 {
		t.Errorf("Expected 2 aliases in map, got %d", len(mod.ImportAliasMap))
	}

	// Should have 2 diagnostics
	diags := ctx.Diagnostics.Diagnostics()
	if len(diags) != 2 {
		t.Fatalf("Expected 2 diagnostics, got %d", len(diags))
	}

	// Both should be duplicate alias errors
	for i, diag := range diags {
		if !strings.Contains(diag.Message, "duplicate import alias") {
			t.Errorf("Diagnostic %d: Expected 'duplicate import alias' error, got: %s", i, diag.Message)
		}
	}
}

// Test triple duplicate (same alias used three times)
func TestImportAliasTripleDuplicate(t *testing.T) {
	src := `
import "std/math" as utils;
import "std/io" as utils;      // duplicate
import "test_project/utils";   // also defaults to 'utils' - duplicate
`

	mod, ctx := parseAndCollect(t, src)

	// Should have 3 imports
	if len(mod.Imports) != 3 {
		t.Errorf("Expected 3 imports, got %d", len(mod.Imports))
	}

	// Should have only 1 alias in map (first one)
	if len(mod.ImportAliasMap) != 1 {
		t.Errorf("Expected 1 alias in map, got %d", len(mod.ImportAliasMap))
	}

	// Should have 2 diagnostics (second and third are duplicates)
	diags := ctx.Diagnostics.Diagnostics()
	if len(diags) != 2 {
		t.Fatalf("Expected 2 diagnostics, got %d", len(diags))
	}
}

// Test valid use of explicit aliases to avoid collision
func TestImportAliasValidExplicitAliases(t *testing.T) {
	src := `
import "std/math";
import "other_lib/math" as math2;  // valid: uses explicit alias to avoid collision
import "third_lib/math" as math3;
`

	mod, ctx := parseAndCollect(t, src)

	// Should have 3 imports
	if len(mod.Imports) != 3 {
		t.Errorf("Expected 3 imports, got %d", len(mod.Imports))
	}

	// Should have 3 aliases in map
	if len(mod.ImportAliasMap) != 3 {
		t.Errorf("Expected 3 aliases in map, got %d", len(mod.ImportAliasMap))
	}

	// Verify correct mappings
	expectedMappings := map[string]string{
		"math":  "std/math",
		"math2": "other_lib/math",
		"math3": "third_lib/math",
	}

	for alias, expectedPath := range expectedMappings {
		if actualPath, ok := mod.ImportAliasMap[alias]; !ok {
			t.Errorf("Alias %q not found in map", alias)
		} else if actualPath != expectedPath {
			t.Errorf("Alias %q maps to %q, expected %q", alias, actualPath, expectedPath)
		}
	}

	// Should have no diagnostics
	diags := ctx.Diagnostics.Diagnostics()
	if len(diags) > 0 {
		t.Errorf("Expected no diagnostics, got %d", len(diags))
		for _, d := range diags {
			t.Logf("  %s", d.Message)
		}
	}
}

// Test that diagnostic has proper labels (primary and secondary)
func TestImportAliasDuplicateDiagnosticLabels(t *testing.T) {
	src := `
import "std/math" as m;
import "std/io" as m;
`

	_, ctx := parseAndCollect(t, src)

	diags := ctx.Diagnostics.Diagnostics()
	if len(diags) != 1 {
		t.Fatalf("Expected 1 diagnostic, got %d", len(diags))
	}

	diag := diags[0]

	// Check that it has labels
	if len(diag.Labels) < 2 {
		t.Errorf("Expected at least 2 labels (primary and secondary), got %d", len(diag.Labels))
	}

	// Verify primary and secondary labels exist
	hasPrimary := false
	hasSecondary := false
	for _, label := range diag.Labels {
		switch label.Style {
		case diagnostics.Primary:
			hasPrimary = true
		case diagnostics.Secondary:
			hasSecondary = true
		}
	}

	if !hasPrimary {
		t.Error("Expected primary label in diagnostic")
	}

	if !hasSecondary {
		t.Error("Expected secondary label in diagnostic")
	}

	// Check for help message
	if len(diag.Help) == 0 {
		t.Error("Expected help message in diagnostic")
	}

	if !strings.Contains(diag.Help, "use a different alias") {
		t.Errorf("Expected helpful suggestion in help message, got: %s", diag.Help)
	}
}
