package collector_test

import (
	"os"
	"path/filepath"
	"testing"

	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/phase"
	compiler "compiler/internal/driver"
)

func TestCollectorBuildsModuleScopeAndMethodSets(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
let mut GlobalCount: i32 = 0
const BuildMode = "debug"

type Point struct {
    X i32 = 0
    static Origin Point = .{}
}

type Color enum {
    Red
}

fn Build() i32 {
    return 1
}

fn (p *Point) Shift(dx i32) {
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil {
		t.Fatal("expected entry module")
	}
	if result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected CFG-analyzed phase, got %s", result.Entry.Phase)
	}
	scope := result.Entry.ModuleScope
	if scope == nil {
		t.Fatal("expected module scope")
	}
	globalSym, ok := scope.LookupLocal("GlobalCount")
	if !ok || globalSym.Kind != symbols.SymbolVar {
		t.Fatalf("expected global let symbol, got %#v", globalSym)
	}
	if _, ok := scope.LookupLocal("BuildMode"); !ok {
		t.Fatal("expected const symbol")
	}
	typeSym, ok := scope.LookupLocal("Point")
	if !ok || typeSym.Kind != symbols.SymbolType {
		t.Fatalf("expected type symbol, got %#v", typeSym)
	}
	funcSym, ok := scope.LookupLocal("Build")
	if !ok || funcSym.Kind != symbols.SymbolFunc || !funcSym.Exported {
		t.Fatalf("expected exported function symbol, got %#v", funcSym)
	}
	methods := result.Entry.MethodSets["*Point"]
	if len(methods) != 1 {
		t.Fatalf("expected one method for *Point, got %#v", methods)
	}
	if methods["Shift"].Kind != symbols.SymbolMethod {
		t.Fatalf("expected method symbol, got %#v", methods["Shift"])
	}
	if result.Entry.TypeMembers["Point"]["Origin"].Kind != symbols.SymbolStatic {
		t.Fatalf("expected static member symbol, got %#v", result.Entry.TypeMembers["Point"]["Origin"])
	}
	if !result.Entry.TypeMembers["Point"]["Origin"].Mutable {
		t.Fatalf("expected static member to be mutable by default, got %#v", result.Entry.TypeMembers["Point"]["Origin"])
	}
	if result.Entry.TypeMembers["Color"]["Red"].Kind != symbols.SymbolVariant {
		t.Fatalf("expected enum variant symbol, got %#v", result.Entry.TypeMembers["Color"]["Red"])
	}
}

func TestCollectorReportsDuplicateTopLevelSymbols(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
const Build = 1
fn Build() i32 {
    return 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected duplicate symbol diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrRedeclaredSymbol {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrRedeclaredSymbol, result.Diagnostics.Diagnostics())
	}
}

func TestCollectorReportsDuplicateMethodsPerReceiver(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
type Point struct {}

fn (p *Point) Len() i32 {
    return 1
}

fn (p *Point) Len() i32 {
    return 2
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected duplicate method diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrRedeclaredSymbol {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrRedeclaredSymbol, result.Diagnostics.Diagnostics())
	}
}

func TestCollectorKeepsReceiverFormMethodSetsSeparate(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X i32 = 0
}

fn (p Point) Copy() i32 {
    return p.X
}

fn (p &Point) Read() i32 {
    return p.X
}

fn (p &mut Point) Bump() i32 {
    return p.X + 1
}

fn (p *Point) Take() i32 {
    return p.X
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", result.Diagnostics.Diagnostics())
	}
	methodSets := result.Entry.MethodSets
	if methodSets["Point"]["Copy"].Kind != symbols.SymbolMethod {
		t.Fatalf("expected value receiver method set, got %#v", methodSets["Point"])
	}
	if methodSets["&Point"]["Read"].Kind != symbols.SymbolMethod {
		t.Fatalf("expected immutable ref receiver method set, got %#v", methodSets["&Point"])
	}
	if methodSets["&mut Point"]["Bump"].Kind != symbols.SymbolMethod {
		t.Fatalf("expected mutable ref receiver method set, got %#v", methodSets["&mut Point"])
	}
	if methodSets["*Point"]["Take"].Kind != symbols.SymbolMethod {
		t.Fatalf("expected owning receiver method set, got %#v", methodSets["*Point"])
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
