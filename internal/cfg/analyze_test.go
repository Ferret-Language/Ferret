package cfg_test

import (
	"os"
	"path/filepath"
	"testing"

	"compiler/internal/cfg"
	compilerapi "compiler/internal/compiler"
	"compiler/internal/diagnostics"
	"compiler/internal/phase"
)

func TestCFGReportsMissingReturn(t *testing.T) {
	root := t.TempDir()
	mustWriteCFG(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    if true {
        return 1
    }
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase != phase.PhaseIRGenerated {
		t.Fatalf("expected ir generated phase, got %#v", result.Entry)
	}
	if result.Entry.CFG == nil {
		t.Fatal("expected CFG")
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrMissingReturn {
			if len(diag.Labels) < 2 {
				t.Fatalf("expected branch-specific secondary label, got %#v", diag.Labels)
			}
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrMissingReturn, result.Diagnostics.Diagnostics())
}

func TestCFGReportsUnreachableCode(t *testing.T) {
	root := t.TempDir()
	mustWriteCFG(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    return 1
    let x = 2
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase != phase.PhaseIRGenerated {
		t.Fatalf("expected ir generated phase, got %#v", result.Entry)
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.WarnUnreachableCode {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s warning, got %#v", diagnostics.WarnUnreachableCode, result.Diagnostics.Diagnostics())
	}
}

func TestCFGAllowsSwitchFallbackReturn(t *testing.T) {
	root := t.TempDir()
	mustWriteCFG(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    switch 1 {
    case 0 {
        return 10
    }
    case 1 {
        return 20
    }
    }
    return 30
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase != phase.PhaseIRGenerated {
		t.Fatalf("expected ir generated phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrMissingReturn || diag.Code == diagnostics.WarnUnreachableCode {
			t.Fatalf("unexpected CFG diagnostic %#v", diag)
		}
	}
}

func TestCFGReportsMissingReturnInSwitchFallback(t *testing.T) {
	root := t.TempDir()
	mustWriteCFG(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    switch 1 {
    case 0 {
        return 10
    }
    case 1 {
        return 20
    }
    }
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase != phase.PhaseIRGenerated {
		t.Fatalf("expected ir generated phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrMissingReturn {
			if len(diag.Labels) < 2 {
				t.Fatalf("expected switch fallback secondary label, got %#v", diag.Labels)
			}
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrMissingReturn, result.Diagnostics.Diagnostics())
}

func TestCFGLivenessTracksStraightLineLocals(t *testing.T) {
	root := t.TempDir()
	mustWriteCFG(t, filepath.Join(root, "main.ferr"), `
fn main(a i32) i32 {
    let b = a
    let c = b
    return c
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.CFG == nil || len(result.Entry.CFG.Functions) != 1 {
		t.Fatalf("expected one CFG function, got %#v", result.Entry)
	}
	fn := result.Entry.CFG.Functions[0]
	if !fn.Locals.Has("a") || !fn.Locals.Has("b") || !fn.Locals.Has("c") {
		t.Fatalf("expected function locals a, b, c; got %#v", fn.Locals.Sorted())
	}
	entry := fn.Entry
	if !entry.Use.Equal(cfg.NewNameSet("a")) {
		t.Fatalf("expected entry use {a}, got %#v", entry.Use.Sorted())
	}
	if !entry.Def.Equal(cfg.NewNameSet("b", "c")) {
		t.Fatalf("expected entry def {b,c}, got %#v", entry.Def.Sorted())
	}
	if !entry.LiveIn.Equal(cfg.NewNameSet("a")) {
		t.Fatalf("expected entry live-in {a}, got %#v", entry.LiveIn.Sorted())
	}
	if len(entry.LiveOut) != 0 {
		t.Fatalf("expected empty entry live-out, got %#v", entry.LiveOut.Sorted())
	}
}

func TestCFGLivenessFlowsAcrossIfBranches(t *testing.T) {
	root := t.TempDir()
	mustWriteCFG(t, filepath.Join(root, "main.ferr"), `
fn main(a i32, b i32) i32 {
    let x = a
    if b > 0 {
        return x
    }
    return x
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	fn := result.Entry.CFG.Functions[0]
	if !fn.Entry.LiveIn.Equal(cfg.NewNameSet("a", "b")) {
		t.Fatalf("expected entry live-in {a,b}, got %#v", fn.Entry.LiveIn.Sorted())
	}
	thenBlock := findBlockByKind(fn, "if")
	elseBlock := findBlockByKind(fn, "else")
	if thenBlock == nil || elseBlock == nil {
		t.Fatalf("expected if/else blocks, got %#v", fn.Blocks)
	}
	if !thenBlock.LiveIn.Equal(cfg.NewNameSet("x")) {
		t.Fatalf("expected then live-in {x}, got %#v", thenBlock.LiveIn.Sorted())
	}
	if !elseBlock.LiveIn.Equal(cfg.NewNameSet("x")) {
		t.Fatalf("expected else live-in {x}, got %#v", elseBlock.LiveIn.Sorted())
	}
}

func TestCFGLivenessHandlesLoopBackEdge(t *testing.T) {
	root := t.TempDir()
	mustWriteCFG(t, filepath.Join(root, "main.ferr"), `
fn main(n i32) i32 {
    let mut m: i32 = n
    let mut x: i32 = 0
    while m > 0 {
        x = x + 1
        m = m - 1
    }
    return x
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	fn := result.Entry.CFG.Functions[0]
	var condBlock *cfg.Block
	for _, block := range fn.Blocks {
		if branch, ok := block.Terminator.(*cfg.BranchTerm); ok && branch.Cond != nil && block != fn.Entry {
			condBlock = block
			break
		}
	}
	if condBlock == nil {
		t.Fatalf("expected loop condition block, got %#v", fn.Blocks)
	}
	if !condBlock.LiveIn.Equal(cfg.NewNameSet("m", "x")) {
		t.Fatalf("expected loop cond live-in {m,x}, got %#v", condBlock.LiveIn.Sorted())
	}
}

func mustWriteCFG(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findBlockByKind(fn *cfg.Function, kind string) *cfg.Block {
	if fn == nil {
		return nil
	}
	for _, block := range fn.Blocks {
		if block != nil && block.BranchKind == kind {
			return block
		}
	}
	return nil
}
