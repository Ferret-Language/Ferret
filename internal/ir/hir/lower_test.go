package hir_test

import (
	"os"
	"path/filepath"
	"testing"

	"compiler/internal/core/diagnostics"
	"compiler/internal/core/phase"
	compiler "compiler/internal/driver"
	"compiler/internal/ir/hir"
)

func TestLoweringNormalizesLoops(t *testing.T) {
	root := t.TempDir()
	mustWriteLower(t, filepath.Join(root, "main.ferr"), `
fn main(items: [3]i32) i32 {
    let mut x: i32 = 0
    while x < 5 {
        x = x + 1
    }
    for items |v| {
        x = x + 1
        x = x + v
    }
    return x
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	if result.Entry.LoweredHIR == nil {
		t.Fatal("expected lowered HIR")
	}
	if result.Entry.CFG == nil {
		t.Fatal("expected CFG")
	}
	fn := result.Entry.LoweredHIR.Functions[0]
	loops := 0
	var walkStmt func(hir.Stmt)
	walkStmt = func(stmt hir.Stmt) {
		switch s := stmt.(type) {
		case nil:
			return
		case *hir.BlockStmt:
			for _, child := range s.Stmts {
				walkStmt(child)
			}
		case *hir.ForStmt:
			t.Fatalf("expected lowered HIR to eliminate for loops, got %T", s)
		case *hir.LoopStmt:
			loops++
			walkStmt(s.Init)
			walkStmt(s.Post)
			walkStmt(s.Body)
		case *hir.IfStmt:
			walkStmt(s.Then)
			walkStmt(s.Else)
		case *hir.LabelStmt:
			walkStmt(s.Stmt)
		case *hir.DeferStmt:
			walkStmt(s.Body)
		case *hir.LockStmt:
			walkStmt(s.Body)
		case *hir.UnsafeStmt:
			walkStmt(s.Body)
		}
	}
	walkStmt(fn.Body)
	if loops < 2 {
		t.Fatalf("expected while and for to lower to loops, found %d loop(s)", loops)
	}
}

func TestLoweringEliminatesMatchExpr(t *testing.T) {
	root := t.TempDir()
	mustWriteLower(t, filepath.Join(root, "main.ferr"), `
type Token union {
    i32,
    i64,
}

fn main() i32 {
    let value: Token = 1
    let out: i32 = match value {
        is i32 => {
            value + value
        }
        _ => {
            0
        }
    }
    return out
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	fn := result.Entry.LoweredHIR.Functions[0]
	var walkExpr func(hir.Expr)
	walkExpr = func(expr hir.Expr) {
		switch e := expr.(type) {
		case nil:
			return
		case *hir.MatchExpr:
			t.Fatal("lowered HIR still contains match expr")
		case *hir.PrefixExpr:
			walkExpr(e.Right)
		case *hir.BinaryExpr:
			walkExpr(e.Left)
			walkExpr(e.Right)
		case *hir.PostfixExpr:
			walkExpr(e.Left)
		case *hir.CallExpr:
			walkExpr(e.Callee)
			for _, arg := range e.Args {
				walkExpr(arg)
			}
		case *hir.SelectorExpr:
			walkExpr(e.Left)
		case *hir.CastExpr:
			walkExpr(e.Left)
		case *hir.IsExpr:
			walkExpr(e.Left)
		case *hir.CatchExpr:
			walkExpr(e.Left)
			walkExpr(e.Fallback)
		case *hir.CompositeLit:
			for _, item := range e.Items {
				walkExpr(item.Value)
			}
		case *hir.IndexExpr:
			walkExpr(e.Left)
			walkExpr(e.Index)
		}
	}
	var walkStmt func(hir.Stmt)
	walkStmt = func(stmt hir.Stmt) {
		switch s := stmt.(type) {
		case nil:
			return
		case *hir.BlockStmt:
			for _, child := range s.Stmts {
				walkStmt(child)
			}
		case *hir.LetStmt:
			walkExpr(s.Value)
		case *hir.ConstStmt:
			walkExpr(s.Value)
		case *hir.ReturnStmt:
			walkExpr(s.Value)
		case *hir.ExprStmt:
			walkExpr(s.Value)
		case *hir.AssignStmt:
			walkExpr(s.Left)
			walkExpr(s.Right)
		case *hir.IfStmt:
			walkExpr(s.Cond)
			walkStmt(s.Then)
			walkStmt(s.Else)
		case *hir.MatchStmt:
			walkExpr(s.Value)
			for _, arm := range s.Arms {
				if arm != nil {
					walkStmt(arm.Body)
				}
			}
		case *hir.WhileStmt:
			walkExpr(s.Cond)
			walkStmt(s.Body)
		case *hir.ForStmt:
			walkExpr(s.Iterable)
			walkStmt(s.Body)
		case *hir.LoopStmt:
			walkStmt(s.Init)
			walkExpr(s.Cond)
			walkStmt(s.Post)
			walkStmt(s.Body)
		case *hir.LabelStmt:
			walkStmt(s.Stmt)
		case *hir.DeferStmt:
			walkStmt(s.Body)
		case *hir.ReleaseStmt:
			walkExpr(s.Value)
		case *hir.PanicStmt:
			walkExpr(s.Value)
		case *hir.LockStmt:
			walkExpr(s.Value)
			walkStmt(s.Body)
		case *hir.UnsafeStmt:
			walkStmt(s.Body)
		}
	}
	walkStmt(fn.Body)
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
