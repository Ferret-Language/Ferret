package cfg_test

import (
	"os"
	"path/filepath"
	"testing"

	cfg "compiler/internal/analysis/cfg/model"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/phase"
	compiler "compiler/internal/driver"
	"compiler/internal/ir/hir"
)

func TestCFGReportsMissingReturn(t *testing.T) {
	root := t.TempDir()
	mustWriteCFG(t, filepath.Join(root, "main.ferr"), `
fn main() -> i32 {
    if true {
        return 1
    }
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
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
fn main() -> i32 {
    return 1
    let x = 2
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
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

func TestCFGAllowsMatchFallbackReturn(t *testing.T) {
	root := t.TempDir()
	mustWriteCFG(t, filepath.Join(root, "main.ferr"), `
fn main() -> i32 {
    match 1 {
    0 => {
        return 10
    }
    1 => {
        return 20
    }
    }
    return 30
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrMissingReturn || diag.Code == diagnostics.WarnUnreachableCode {
			t.Fatalf("unexpected CFG diagnostic %#v", diag)
		}
	}
}

func TestCFGTreatsPanicAsTerminator(t *testing.T) {
	root := t.TempDir()
	mustWriteCFG(t, filepath.Join(root, "main.ferr"), `
fn fail() -> void {
    panic "bad"
    let x = 1
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	if result.Entry.CFG == nil || len(result.Entry.CFG.Functions) != 1 {
		t.Fatalf("expected CFG functions, got %#v", result.Entry)
	}
	var fn *cfg.Function
	for _, candidate := range result.Entry.CFG.Functions {
		if candidate.Name == "fail" {
			fn = candidate
			break
		}
	}
	if fn == nil {
		t.Fatalf("expected CFG function fail, got %#v", result.Entry.CFG.Functions)
	}
	foundPanicEval := false
	for _, block := range fn.Blocks {
		if len(block.Stmts) > 0 {
			foundPanicEval = true
			break
		}
	}
	if !foundPanicEval {
		t.Fatalf("expected lowered panic statements in CFG, got %#v", fn.Blocks)
	}
}

func TestCFGBuildsCleanupEdgeForDeferredPanic(t *testing.T) {
	root := t.TempDir()
	mustWriteCFG(t, filepath.Join(root, "main.ferr"), `
fn close() -> void {}

fn fail() -> void {
    defer close()
    panic "bad"
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	var fn *cfg.Function
	for _, candidate := range result.Entry.CFG.Functions {
		if candidate.Name == "fail" {
			fn = candidate
			break
		}
	}
	if fn == nil {
		t.Fatalf("expected CFG function fail, got %#v", result.Entry.CFG.Functions)
	}
	foundCleanup := false
	for _, block := range fn.Blocks {
		if block.BranchKind == "cleanup" {
			if len(block.Stmts) == 0 {
				t.Fatalf("expected cleanup block statements, got %#v", block)
			}
			foundCleanup = true
			break
		}
	}
	if !foundCleanup {
		t.Fatalf("expected cleanup block in CFG, got %#v", fn.Blocks)
	}
}

func TestCFGReportsMissingReturnInMatchFallback(t *testing.T) {
	root := t.TempDir()
	mustWriteCFG(t, filepath.Join(root, "main.ferr"), `
fn main() -> i32 {
    match 1 {
    0 => {
        return 10
    }
    1 => {
        return 20
    }
    }
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrMissingReturn {
			if len(diag.Labels) < 2 {
				t.Fatalf("expected match fallback secondary label, got %#v", diag.Labels)
			}
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrMissingReturn, result.Diagnostics.Diagnostics())
}

func TestCFGLivenessTracksStraightLineLocals(t *testing.T) {
	root := t.TempDir()
	mustWriteCFG(t, filepath.Join(root, "main.ferr"), `
fn main(a: i32) -> i32 {
    let b = a
    let c = b
    return c
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.CFG == nil || len(result.Entry.CFG.Functions) != 1 {
		t.Fatalf("expected one CFG function, got %#v", result.Entry)
	}
	fn := result.Entry.CFG.Functions[0]
	if !fn.Locals.Has(mustLocalID(t, fn, "a")) || !fn.Locals.Has(mustLocalID(t, fn, "b")) || !fn.Locals.Has(mustLocalID(t, fn, "c")) {
		t.Fatalf("expected function locals a, b, c; got %#v", fn.Locals.Sorted())
	}
	entry := fn.Entry
	if !entry.Use.Equal(localSet(t, fn, "a")) {
		t.Fatalf("expected entry use {a}, got %#v", entry.Use.Sorted())
	}
	if !entry.Def.Equal(localSet(t, fn, "b", "c")) {
		t.Fatalf("expected entry def {b,c}, got %#v", entry.Def.Sorted())
	}
	if !entry.LiveIn.Equal(localSet(t, fn, "a")) {
		t.Fatalf("expected entry live-in {a}, got %#v", entry.LiveIn.Sorted())
	}
	if len(entry.LiveOut) != 0 {
		t.Fatalf("expected empty entry live-out, got %#v", entry.LiveOut.Sorted())
	}
}

func TestCFGLivenessFlowsAcrossIfBranches(t *testing.T) {
	root := t.TempDir()
	mustWriteCFG(t, filepath.Join(root, "main.ferr"), `
fn main(a: i32, b: i32) -> i32 {
    let x = a
    if b > 0 {
        return x
    }
    return x
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	fn := result.Entry.CFG.Functions[0]
	if !fn.Entry.LiveIn.Equal(localSet(t, fn, "a", "b")) {
		t.Fatalf("expected entry live-in {a,b}, got %#v", fn.Entry.LiveIn.Sorted())
	}
	thenBlock := findBlockByKind(fn, "if")
	elseBlock := findBlockByKind(fn, "else")
	if thenBlock == nil || elseBlock == nil {
		t.Fatalf("expected if/else blocks, got %#v", fn.Blocks)
	}
	if !thenBlock.LiveIn.Equal(localSet(t, fn, "x")) {
		t.Fatalf("expected then live-in {x}, got %#v", thenBlock.LiveIn.Sorted())
	}
	if !elseBlock.LiveIn.Equal(localSet(t, fn, "x")) {
		t.Fatalf("expected else live-in {x}, got %#v", elseBlock.LiveIn.Sorted())
	}
}

func TestCFGLivenessHandlesLoopBackEdge(t *testing.T) {
	root := t.TempDir()
	mustWriteCFG(t, filepath.Join(root, "main.ferr"), `
fn main(n: i32) -> i32 {
    let mut m: i32 = n
    let mut x: i32 = 0
    while m > 0 {
        x = x + 1
        m = m - 1
    }
    return x
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
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
	if !condBlock.LiveIn.Equal(localSet(t, fn, "m", "x")) {
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

func localIDByName(fn *hir.Func, name string) (int, bool) {
	if fn == nil || name == "" {
		return -1, false
	}
	if fn.Receiver != nil && fn.Receiver.Name == name {
		return fn.Receiver.LocalID, true
	}
	for _, param := range fn.Params {
		if param != nil && param.Name == name {
			return param.LocalID, true
		}
	}
	var walk func(stmt hir.Stmt) (int, bool)
	walk = func(stmt hir.Stmt) (int, bool) {
		switch s := stmt.(type) {
		case nil:
			return -1, false
		case *hir.BlockStmt:
			for _, child := range s.Stmts {
				if id, ok := walk(child); ok {
					return id, true
				}
			}
		case *hir.LetStmt:
			if s.Name == name {
				return s.LocalID, true
			}
		case *hir.ForStmt:
			if s.IndexName == name {
				return s.IndexID, true
			}
			if s.ValueName == name {
				return s.ValueID, true
			}
			return walk(s.Body)
		case *hir.LockStmt:
			if s.Name == name {
				return s.LocalID, true
			}
			return walk(s.Body)
		case *hir.IfStmt:
			if id, ok := walk(s.Then); ok {
				return id, true
			}
			return walk(s.Else)
		case *hir.MatchStmt:
			for _, arm := range s.Arms {
				if arm == nil {
					continue
				}
				if id, ok := walk(arm.Body); ok {
					return id, true
				}
			}
		case *hir.WhileStmt:
			return walk(s.Body)
		case *hir.LoopStmt:
			if id, ok := walk(s.Init); ok {
				return id, true
			}
			if id, ok := walk(s.Body); ok {
				return id, true
			}
			return walk(s.Post)
		case *hir.LabelStmt:
			return walk(s.Stmt)
		case *hir.DeferStmt:
			return walk(s.Body)
		case *hir.UnsafeStmt:
			return walk(s.Body)
		}
		return -1, false
	}
	return walk(fn.Body)
}

func mustLocalID(t *testing.T, fn *cfg.Function, name string) int {
	t.Helper()
	if fn == nil || fn.Source == nil {
		t.Fatalf("missing CFG function source when looking up local %q", name)
	}
	id, ok := localIDByName(fn.Source, name)
	if !ok || id < 0 {
		t.Fatalf("could not resolve local id for %q", name)
	}
	return id
}

func localSet(t *testing.T, fn *cfg.Function, names ...string) cfg.LocalSet {
	t.Helper()
	out := cfg.NewLocalSet()
	for _, name := range names {
		out.Add(mustLocalID(t, fn, name))
	}
	return out
}
