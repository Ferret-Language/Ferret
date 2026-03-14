package cfganalysis

import (
	"testing"

	"compiler/internal/analysis/cfg/model"
	"compiler/internal/ir/hir"
)

func TestCollectFunctionLocalsUsesLocalCount(t *testing.T) {
	fn := &hir.Func{LocalCount: 4}
	locals := collectFunctionLocals(fn)
	want := cfg.NewLocalSet(0, 1, 2, 3)
	if !locals.Equal(want) {
		t.Fatalf("expected locals %v, got %v", want.Sorted(), locals.Sorted())
	}
}

func TestComputeLivenessPropagatesUseDefAcrossBlocks(t *testing.T) {
	entry := &cfg.Block{
		ID:        0,
		Reachable: true,
		Stmts: []hir.Stmt{
			&hir.ExprStmt{Value: &hir.Ident{LocalID: 0}},
			&hir.LetStmt{LocalID: 1, Value: &hir.Ident{LocalID: 0}},
		},
	}
	exit := &cfg.Block{
		ID:        1,
		Reachable: true,
		Stmts: []hir.Stmt{
			&hir.ReturnStmt{Value: &hir.Ident{LocalID: 1}},
		},
	}
	entry.Terminator = &cfg.JumpTerm{Target: exit}

	fn := &cfg.Function{
		Source: &hir.Func{LocalCount: 2},
		Entry:  entry,
		Exit:   exit,
		Blocks: []*cfg.Block{entry, exit},
	}

	computeLiveness(fn)

	if !entry.Use.Equal(cfg.NewLocalSet(0)) {
		t.Fatalf("entry.Use: expected {0}, got %v", entry.Use.Sorted())
	}
	if !entry.Def.Equal(cfg.NewLocalSet(1)) {
		t.Fatalf("entry.Def: expected {1}, got %v", entry.Def.Sorted())
	}
	if !entry.LiveOut.Equal(cfg.NewLocalSet(1)) {
		t.Fatalf("entry.LiveOut: expected {1}, got %v", entry.LiveOut.Sorted())
	}
	if !entry.LiveIn.Equal(cfg.NewLocalSet(0)) {
		t.Fatalf("entry.LiveIn: expected {0}, got %v", entry.LiveIn.Sorted())
	}

	if !exit.Use.Equal(cfg.NewLocalSet(1)) {
		t.Fatalf("exit.Use: expected {1}, got %v", exit.Use.Sorted())
	}
	if len(exit.Def) != 0 {
		t.Fatalf("exit.Def: expected empty, got %v", exit.Def.Sorted())
	}
	if !exit.LiveIn.Equal(cfg.NewLocalSet(1)) {
		t.Fatalf("exit.LiveIn: expected {1}, got %v", exit.LiveIn.Sorted())
	}
	if len(exit.LiveOut) != 0 {
		t.Fatalf("exit.LiveOut: expected empty, got %v", exit.LiveOut.Sorted())
	}
}

func TestComputeLivenessSkipsUnreachableBlocks(t *testing.T) {
	entry := &cfg.Block{ID: 0, Reachable: true, Terminator: &cfg.JumpTerm{}}
	dead := &cfg.Block{
		ID:        1,
		Reachable: false,
		Stmts: []hir.Stmt{
			&hir.ExprStmt{Value: &hir.Ident{LocalID: 0}},
		},
	}
	entry.Terminator = &cfg.JumpTerm{Target: dead}

	fn := &cfg.Function{
		Source: &hir.Func{LocalCount: 1},
		Entry:  entry,
		Exit:   dead,
		Blocks: []*cfg.Block{entry, dead},
	}

	computeLiveness(fn)

	if len(dead.Use) != 0 || len(dead.Def) != 0 || len(dead.LiveIn) != 0 || len(dead.LiveOut) != 0 {
		t.Fatalf("expected unreachable block sets to stay empty, got use=%v def=%v in=%v out=%v", dead.Use.Sorted(), dead.Def.Sorted(), dead.LiveIn.Sorted(), dead.LiveOut.Sorted())
	}
}
