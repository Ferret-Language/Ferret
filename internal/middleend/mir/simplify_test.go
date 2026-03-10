package mir_test

import (
	"path/filepath"
	"strings"
	"testing"

	compilerapi "compiler/internal/compiler"
	"compiler/internal/diagnostics"
	midmir "compiler/internal/middleend/mir"
)

func TestPipelineSimplifiesConstantArithmeticInMIR(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let value = 1 + 2 * 3
    return value
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil || len(result.Entry.MIR.Functions) != 1 {
		t.Fatalf("expected one MIR function, got %#v", result.Entry)
	}

	text := midmir.FormatModule(result.Entry.MIR)
	if !strings.Contains(text, "return 7") {
		t.Fatalf("expected folded constant return in MIR, got %q", text)
	}
	if strings.Contains(text, " add ") || strings.Contains(text, " mul ") {
		t.Fatalf("expected folded arithmetic to remove add/mul ops, got %q", text)
	}
}

func TestPipelineSimplifiesConstantBranchAndRemovesDeadBlocks(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let cond = 1 < 2
    if cond {
        return 1
    }
    return 2
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil || len(result.Entry.MIR.Functions) != 1 {
		t.Fatalf("expected one MIR function, got %#v", result.Entry)
	}

	fn := result.Entry.MIR.Functions[0]
	text := midmir.FormatModule(result.Entry.MIR)

	if strings.Contains(text, "branch ") {
		t.Fatalf("expected constant branch to simplify to jump, got %q", text)
	}
	if strings.Contains(text, "return 2") {
		t.Fatalf("expected dead else path to be removed, got %q", text)
	}
	if len(fn.Blocks) != 2 {
		t.Fatalf("expected entry + return block after dead block elimination, got %d blocks: %q", len(fn.Blocks), text)
	}
}

func TestPipelineElidesLocalConstBindingsInMIR(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    const x = 1 + 2
    return x
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	text := midmir.FormatModule(result.Entry.MIR)
	if strings.Contains(text, "x:") {
		t.Fatalf("expected local const to be elided from MIR locals, got %q", text)
	}
	if strings.Contains(text, "const x") {
		t.Fatalf("expected local const instruction to be elided from MIR, got %q", text)
	}
	if !strings.Contains(text, "return 3") {
		t.Fatalf("expected inlined/folded local const value in MIR, got %q", text)
	}
}

func TestPipelineInlinesImportedConstIntoMIR(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "util.ferr"), `
const Value = 4 + 5
`)
	mustWriteIR(t, filepath.Join(root, "main.ferr"), `
import "util"

fn main() i32 {
    return util::Value
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	text := midmir.FormatModule(result.Entry.MIR)
	if strings.Contains(text, "util::Value") {
		t.Fatalf("expected imported const to be inlined in MIR, got %q", text)
	}
	if !strings.Contains(text, "return 9") {
		t.Fatalf("expected inlined/folded imported const in MIR, got %q", text)
	}
}

func TestPipelineWarnsOnConstantTrueCondition(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    if 1 < 2 {
        return 1
    }
    return 0
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	found := false
	for _, diag := range result.Diagnostics.All() {
		if diag == nil {
			continue
		}
		if diag.Code == diagnostics.WarnConstantConditionTrue && diag.Message == "condition is always true" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected constant true warning, got %#v", result.Diagnostics.All())
	}
}

func TestPipelineWarnsOnConstDrivenConstantFalseCondition(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.ferr"), `
const Flag = false

fn main() i32 {
    if Flag {
        return 1
    }
    return 0
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	found := false
	for _, diag := range result.Diagnostics.All() {
		if diag == nil {
			continue
		}
		if diag.Code == diagnostics.WarnConstantConditionFalse && diag.Message == "condition is always false" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected constant false warning, got %#v", result.Diagnostics.All())
	}
}
