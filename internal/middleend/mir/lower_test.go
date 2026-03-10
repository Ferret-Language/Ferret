package mir_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	compilerapi "compiler/internal/compiler"
	"compiler/internal/diagnostics"
	midmir "compiler/internal/middleend/mir"
	"compiler/internal/phase"
)

func TestPipelineGeneratesMIR(t *testing.T) {
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
	if result.Entry == nil || result.Entry.Phase != phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	if result.Entry.MIR == nil {
		t.Fatal("expected MIR module")
	}
	if len(result.Entry.MIR.Types) != 1 {
		t.Fatalf("expected one mir type decl, got %#v", result.Entry.MIR.Types)
	}
	if len(result.Entry.MIR.Functions) != 1 {
		t.Fatalf("expected one mir function, got %#v", result.Entry.MIR.Functions)
	}
	fn := result.Entry.MIR.Functions[0]
	if fn.EntryID < 0 {
		t.Fatalf("expected valid entry id, got %#v", fn)
	}
	foundStore := false
	foundBranch := false
	foundCompute := false
	for _, block := range fn.Blocks {
		for _, instr := range block.Instructions {
			if _, ok := instr.(*midmir.StoreFieldInstr); ok {
				foundStore = true
			}
			if _, ok := instr.(*midmir.ComputeInstr); ok {
				foundCompute = true
			}
		}
		if _, ok := block.Terminator.(*midmir.BranchTerm); ok {
			foundBranch = true
		}
	}
	if !foundStore {
		t.Fatal("expected store_field instruction in lowered MIR")
	}
	if !foundBranch {
		t.Fatal("expected branch terminator in lowered MIR")
	}
	if !foundCompute {
		t.Fatal("expected compute instruction in normalized MIR")
	}
	for _, block := range fn.Blocks {
		switch term := block.Terminator.(type) {
		case *midmir.BranchTerm:
			if _, ok := term.Cond.(*midmir.LocalValue); !ok {
				t.Fatalf("expected branch condition temp, got %T", term.Cond)
			}
		case *midmir.ReturnTerm:
			if term.Value != nil {
				switch term.Value.(type) {
				case *midmir.LocalValue, *midmir.NameValue, *midmir.NumberValue, *midmir.StringValue, *midmir.NoneValue:
				default:
					t.Fatalf("expected normalized simple return value, got %T", term.Value)
				}
			}
		}
	}
	text := midmir.FormatModule(result.Entry.MIR)
	if !strings.Contains(text, "type Point struct") {
		t.Fatalf("expected type declaration in mir dump, got %q", text)
	}
	if !strings.Contains(text, "X i32 = 0") || !strings.Contains(text, "Y i32 = 0") {
		t.Fatalf("expected field defaults in mir dump, got %q", text)
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
