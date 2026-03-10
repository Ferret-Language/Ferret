package ownership_test

import (
	"os"
	"path/filepath"
	"testing"

	compilerapi "compiler/internal/compiler"
	"compiler/internal/diagnostics"
	"compiler/internal/phase"
)

func TestOwnershipPhaseReportsUseAfterMove(t *testing.T) {
	root := t.TempDir()
	mustWriteOwnership(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X i32 = 0
}

fn main() i32 {
    let p: Point = .{ .X = 1 }
    let q = p
    return p.X
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase != phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	if result.Entry.HIR == nil {
		t.Fatal("expected lowered HIR")
	}
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected ownership diagnostic")
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove {
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrUseAfterMove, result.Diagnostics.Diagnostics())
}

func mustWriteOwnership(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
