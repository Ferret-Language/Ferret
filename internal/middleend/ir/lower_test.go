package ir_test

import (
	"os"
	"path/filepath"
	"testing"

	compilerapi "compiler/internal/compiler"
	"compiler/internal/diagnostics"
	midir "compiler/internal/middleend/ir"
	"compiler/internal/phase"
)

func TestPipelineGeneratesBackendIndependentIR(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X i32 = 0
    Y i32 = 0
}

let mut GlobalPoint: Point = .{ .X = 1, .Y = 2 }

fn main() i32 {
    let mut p = copy GlobalPoint
    if p.X > 0 {
        p.X = p.X + 1
    }
    return p.X
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.Phase != phase.PhaseIRGenerated {
		t.Fatalf("expected ir generated phase, got %#v", result.Entry)
	}
	if result.Entry.IR == nil {
		t.Fatal("expected IR module")
	}
	if len(result.Entry.IR.Functions) != 1 {
		t.Fatalf("expected one ir function, got %#v", result.Entry.IR.Functions)
	}
	fn := result.Entry.IR.Functions[0]
	if fn.EntryID < 0 || fn.ExitID < 0 {
		t.Fatalf("expected valid entry/exit ids, got %#v", fn)
	}
	foundStore := false
	foundBranch := false
	for _, block := range fn.Blocks {
		for _, instr := range block.Instructions {
			if _, ok := instr.(*midir.StoreInstr); ok {
				foundStore = true
			}
		}
		if _, ok := block.Terminator.(*midir.BranchTerm); ok {
			foundBranch = true
		}
	}
	if !foundStore {
		t.Fatal("expected store instruction in lowered IR")
	}
	if !foundBranch {
		t.Fatal("expected branch terminator in lowered IR")
	}
}

func mustWriteIR(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
