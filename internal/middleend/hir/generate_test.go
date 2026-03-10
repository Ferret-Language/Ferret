package hir_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	compilerapi "compiler/internal/compiler"
	"compiler/internal/diagnostics"
	"compiler/internal/middleend/hir"
	"compiler/internal/phase"
)

func TestPipelineGeneratesHIR(t *testing.T) {
	root := t.TempDir()
	mustWriteHIR(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X i32 = 0
}

let mut GlobalPoint: Point = .{ .X = 1 }

fn main() i32 {
    let p = copy GlobalPoint
    return p.X
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	if result.Entry.HIR == nil {
		t.Fatal("expected HIR module")
	}
	if result.Entry.LoweredHIR == nil {
		t.Fatal("expected lowered HIR module")
	}
	if result.Entry.CFG == nil {
		t.Fatal("expected CFG module")
	}
	if len(result.Entry.HIR.Types) != 1 {
		t.Fatalf("expected one lowered type decl, got %#v", result.Entry.HIR.Types)
	}
	if len(result.Entry.HIR.Globals) != 1 {
		t.Fatalf("expected one lowered global, got %#v", result.Entry.HIR.Globals)
	}
	if len(result.Entry.HIR.Functions) != 1 {
		t.Fatalf("expected one lowered function, got %#v", result.Entry.HIR.Functions)
	}
	fn := result.Entry.HIR.Functions[0]
	if fn.Name != "main" {
		t.Fatalf("expected main function, got %#v", fn.Name)
	}
	ret, ok := fn.Body.Stmts[1].(*hir.ReturnStmt)
	if !ok {
		t.Fatalf("expected lowered return stmt, got %T", fn.Body.Stmts[1])
	}
	if ret.Value == nil || ret.Value.Type() == nil || ret.Value.Type().String() != "i32" {
		t.Fatalf("expected typed lowered return value, got %#v", ret.Value)
	}
	text := hir.FormatModule(result.Entry.HIR)
	if text == "" || !contains(text, "type Point struct") {
		t.Fatalf("expected type declaration in hir dump, got %q", text)
	}
	if !contains(text, "X i32 = 0") {
		t.Fatalf("expected field default in hir dump, got %q", text)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func mustWriteHIR(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
