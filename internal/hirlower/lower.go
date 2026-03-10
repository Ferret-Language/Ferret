package hirlower

import (
	"compiler/internal/context"
	"compiler/internal/hir"
	"compiler/internal/phase"
)

func Lower(mod *context.Module) *hir.Module {
	if mod == nil || mod.HIR == nil {
		return nil
	}
	out := &hir.Module{
		Key:        mod.HIR.Key,
		ImportPath: mod.HIR.ImportPath,
		FilePath:   mod.HIR.FilePath,
		Source:     mod.HIR.Source,
		Globals:    append([]*hir.Global(nil), mod.HIR.Globals...),
		Functions:  make([]*hir.Func, 0, len(mod.HIR.Functions)),
	}
	for _, fn := range mod.HIR.Functions {
		out.Functions = append(out.Functions, lowerFunc(fn))
	}
	mod.LoweredHIR = out
	mod.Phase = phase.PhaseHIRLowered
	return out
}

func lowerFunc(fn *hir.Func) *hir.Func {
	if fn == nil {
		return nil
	}
	out := *fn
	out.Body = lowerBlock(fn.Body)
	return &out
}

func lowerBlock(block *hir.BlockStmt) *hir.BlockStmt {
	if block == nil {
		return nil
	}
	out := &hir.BlockStmt{Stmts: make([]hir.Stmt, 0, len(block.Stmts))}
	hir.SetStmtLocation(out, block.Loc())
	for _, stmt := range block.Stmts {
		if lowered := lowerStmt(stmt); lowered != nil {
			out.Stmts = append(out.Stmts, lowered)
		}
	}
	return out
}

func lowerStmt(stmt hir.Stmt) hir.Stmt {
	switch s := stmt.(type) {
	case nil:
		return nil
	case *hir.BlockStmt:
		return lowerBlock(s)
	case *hir.IfStmt:
		out := &hir.IfStmt{Cond: s.Cond, Then: lowerBlock(s.Then), Else: lowerStmt(s.Else)}
		hir.SetStmtLocation(out, s.Loc())
		if out.Else == nil {
			empty := &hir.BlockStmt{}
			hir.SetStmtLocation(empty, s.Loc())
			out.Else = empty
		}
		return out
	case *hir.SwitchStmt:
		out := &hir.SwitchStmt{Value: s.Value, Cases: make([]*hir.SwitchCase, 0, len(s.Cases))}
		hir.SetStmtLocation(out, s.Loc())
		for _, kase := range s.Cases {
			if kase == nil {
				continue
			}
			out.Cases = append(out.Cases, &hir.SwitchCase{Expr: kase.Expr, Body: lowerBlock(kase.Body)})
		}
		return out
	case *hir.WhileStmt:
		out := &hir.LoopStmt{Cond: s.Cond, Body: lowerBlock(s.Body)}
		hir.SetStmtLocation(out, s.Loc())
		return out
	case *hir.ForStmt:
		out := &hir.LoopStmt{Init: lowerStmt(s.Init), Cond: s.Cond, Post: lowerStmt(s.Post), Body: lowerBlock(s.Body)}
		hir.SetStmtLocation(out, s.Loc())
		return out
	case *hir.LabelStmt:
		out := &hir.LabelStmt{Name: s.Name, Stmt: lowerStmt(s.Stmt)}
		hir.SetStmtLocation(out, s.Loc())
		return out
	case *hir.DeferStmt:
		out := &hir.DeferStmt{Body: lowerStmt(s.Body)}
		hir.SetStmtLocation(out, s.Loc())
		return out
	case *hir.LockStmt:
		out := &hir.LockStmt{Value: s.Value, Name: s.Name, Body: lowerBlock(s.Body)}
		hir.SetStmtLocation(out, s.Loc())
		return out
	case *hir.UnsafeStmt:
		out := &hir.UnsafeStmt{Body: lowerBlock(s.Body)}
		hir.SetStmtLocation(out, s.Loc())
		return out
	default:
		return stmt
	}
}
