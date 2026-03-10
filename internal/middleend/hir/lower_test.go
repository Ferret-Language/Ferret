package hir_test

import (
	"os"
	"path/filepath"
	"testing"

	compilerapi "compiler/internal/compiler"
	"compiler/internal/diagnostics"
	"compiler/internal/middleend/hir"
	"compiler/internal/phase"
)

func TestLoweringNormalizesLoops(t *testing.T) {
	root := t.TempDir()
	mustWriteLower(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let mut x: i32 = 0
    while x < 5 {
        x = x + 1
    }
    for x = 0; x < 3; x = x + 1 {
        x = x + 1
    }
    return x
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.Phase != phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	if result.Entry.LoweredHIR == nil {
		t.Fatal("expected lowered HIR")
	}
	if result.Entry.CFG == nil {
		t.Fatal("expected CFG")
	}
	fn := result.Entry.LoweredHIR.Functions[0]
	if _, ok := fn.Body.Stmts[1].(*hir.LoopStmt); !ok {
		t.Fatalf("expected while to lower to loop, got %T", fn.Body.Stmts[1])
	}
	if _, ok := fn.Body.Stmts[2].(*hir.LoopStmt); !ok {
		t.Fatalf("expected for to lower to loop, got %T", fn.Body.Stmts[2])
	}
}

func mustWriteLower(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
