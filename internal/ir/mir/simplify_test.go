package mir_test

import (
	"path/filepath"
	"strings"
	"testing"

	"compiler/internal/core/diagnostics"
	"compiler/internal/driver"
	"compiler/internal/ir/mir"
)

func TestPipelineSimplifiesConstantArithmeticInMIR(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let value = 1 + 2 * 3
    return value
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil || len(result.Entry.MIR.Functions) != 1 {
		t.Fatalf("expected one MIR function, got %#v", result.Entry)
	}

	text := mir.FormatModule(result.Entry.MIR)
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

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil || len(result.Entry.MIR.Functions) != 1 {
		t.Fatalf("expected one MIR function, got %#v", result.Entry)
	}

	fn := result.Entry.MIR.Functions[0]
	text := mir.FormatModule(result.Entry.MIR)

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

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	text := mir.FormatModule(result.Entry.MIR)
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

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	text := mir.FormatModule(result.Entry.MIR)
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

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
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

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
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

func TestPipelineSimplifiesStaticIsCondition(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.ferr"), `
type Stringer interface {
    String() str
}

type Name struct {
    value i32 = 0
}

fn (n Name) String() str {
    return 1 as str
}

fn main() i32 {
    let n: Name = .{ .value = 1 }
    if n is Stringer {
        return 1
    }
    return 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	text := mir.FormatModule(result.Entry.MIR)
	if strings.Contains(text, "if n is") || strings.Contains(text, "branch ") {
		t.Fatalf("expected static is-condition to fold away in MIR, got %q", text)
	}
	if !strings.Contains(text, "return 1") {
		t.Fatalf("expected folded true branch in MIR, got %q", text)
	}
}
