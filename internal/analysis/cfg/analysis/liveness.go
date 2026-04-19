package cfganalysis

import (
	cfg "compiler/internal/analysis/cfg/model"
	"compiler/internal/ir/hir"
)

func computeLiveness(fn *cfg.Function) {
	if fn == nil {
		return
	}
	locals := collectFunctionLocals(fn.Source)
	fn.Locals = locals.Clone()
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		block.Use = cfg.NewLocalSet()
		block.Def = cfg.NewLocalSet()
		block.LiveIn = cfg.NewLocalSet()
		block.LiveOut = cfg.NewLocalSet()
		if !block.Reachable {
			continue
		}
		computeBlockUseDef(block, locals)
	}

	changed := true
	for changed {
		changed = false
		for i := len(fn.Blocks) - 1; i >= 0; i-- {
			block := fn.Blocks[i]
			if block == nil || !block.Reachable {
				continue
			}
			liveOut := cfg.NewLocalSet()
			if block.Terminator != nil {
				for _, succ := range block.Terminator.Successors() {
					if succ == nil || !succ.Reachable {
						continue
					}
					liveOut = liveOut.Union(succ.LiveIn)
				}
			}
			liveIn := block.Use.Union(liveOut.Difference(block.Def))
			if !block.LiveOut.Equal(liveOut) {
				block.LiveOut = liveOut
				changed = true
			}
			if !block.LiveIn.Equal(liveIn) {
				block.LiveIn = liveIn
				changed = true
			}
		}
	}
}

func collectFunctionLocals(fn *hir.Func) cfg.LocalSet {
	locals := cfg.NewLocalSet()
	if fn == nil || fn.LocalCount <= 0 {
		return locals
	}
	for id := 0; id < fn.LocalCount; id++ {
		locals.Add(id)
	}
	return locals
}

func computeBlockUseDef(block *cfg.Block, locals cfg.LocalSet) {
	for _, stmt := range block.Stmts {
		accumulateStmtUseDef(block.Use, block.Def, locals, stmt)
	}
	switch term := block.Terminator.(type) {
	case *cfg.BranchTerm:
		accumulateExprUses(block.Use, block.Def, locals, term.Cond)
	case *cfg.SwitchTerm:
		accumulateExprUses(block.Use, block.Def, locals, term.Value)
		for _, edge := range term.Cases {
			accumulateExprUses(block.Use, block.Def, locals, edge.Expr)
		}
	}
}

func accumulateStmtUseDef(use, def, locals cfg.LocalSet, stmt hir.Stmt) {
	switch s := stmt.(type) {
	case nil:
		return
	case *hir.LetStmt:
		accumulateExprUses(use, def, locals, s.Value)
		def.Add(s.LocalID)
	case *hir.ConstStmt:
		accumulateExprUses(use, def, locals, s.Value)
		// Consts don't allocate runtime locals.
	case *hir.ReturnStmt:
		accumulateExprUses(use, def, locals, s.Value)
	case *hir.PanicStmt:
		accumulateExprUses(use, def, locals, s.Value)
	case *hir.ExprStmt:
		accumulateExprUses(use, def, locals, s.Value)
	case *hir.AssignStmt:
		accumulateExprUses(use, def, locals, s.Right)
		accumulateAssignTarget(use, def, locals, s.Left)
	case *hir.DeferStmt:
		accumulateDeferredUses(use, def, locals, s.Body)
	case *hir.LockStmt:
		accumulateExprUses(use, def, locals, s.Value)
		def.Add(s.LocalID)
	case *hir.BreakStmt, *hir.ContinueStmt, *hir.UnsafeStmt:
		return
	}
}

func accumulateAssignTarget(use, def, locals cfg.LocalSet, expr hir.Expr) {
	switch e := expr.(type) {
	case nil:
		return
	case *hir.Ident:
		if e.LocalID >= 0 && locals.Has(e.LocalID) {
			def.Add(e.LocalID)
		}
	default:
		accumulateExprUses(use, def, locals, expr)
	}
}

func accumulateDeferredUses(use, def, locals cfg.LocalSet, stmt hir.Stmt) {
	switch s := stmt.(type) {
	case nil:
		return
	case *hir.BlockStmt:
		for _, child := range s.Stmts {
			accumulateDeferredUses(use, def, locals, child)
		}
	case *hir.LetStmt:
		accumulateExprUses(use, def, locals, s.Value)
	case *hir.ConstStmt:
		accumulateExprUses(use, def, locals, s.Value)
	case *hir.ReturnStmt:
		accumulateExprUses(use, def, locals, s.Value)
	case *hir.PanicStmt:
		accumulateExprUses(use, def, locals, s.Value)
	case *hir.ExprStmt:
		accumulateExprUses(use, def, locals, s.Value)
	case *hir.AssignStmt:
		accumulateExprUses(use, def, locals, s.Right)
		accumulateExprUses(use, def, locals, s.Left)
	case *hir.IfStmt:
		accumulateExprUses(use, def, locals, s.Cond)
		accumulateDeferredUses(use, def, locals, s.Then)
		accumulateDeferredUses(use, def, locals, s.Else)
	case *hir.MatchStmt:
		accumulateExprUses(use, def, locals, s.Value)
		for _, arm := range s.Arms {
			if arm == nil {
				continue
			}
			if arm.TypePattern == nil && !arm.Wildcard {
				accumulateExprUses(use, def, locals, arm.Pattern)
			}
			accumulateDeferredUses(use, def, locals, arm.Body)
		}
	case *hir.WhileStmt:
		accumulateExprUses(use, def, locals, s.Cond)
		accumulateDeferredUses(use, def, locals, s.Body)
	case *hir.ForStmt:
		accumulateExprUses(use, def, locals, s.Iterable)
		if s.IndexID >= 0 {
			def.Add(s.IndexID)
		}
		if s.ValueID >= 0 {
			def.Add(s.ValueID)
		}
		accumulateDeferredUses(use, def, locals, s.Body)
	case *hir.LoopStmt:
		accumulateDeferredUses(use, def, locals, s.Init)
		accumulateExprUses(use, def, locals, s.Cond)
		accumulateDeferredUses(use, def, locals, s.Post)
		accumulateDeferredUses(use, def, locals, s.Body)
	case *hir.LabelStmt:
		accumulateDeferredUses(use, def, locals, s.Stmt)
	case *hir.LockStmt:
		accumulateExprUses(use, def, locals, s.Value)
		accumulateDeferredUses(use, def, locals, s.Body)
	case *hir.UnsafeStmt:
		accumulateDeferredUses(use, def, locals, s.Body)
	}
}

func accumulateExprUses(use, def, locals cfg.LocalSet, expr hir.Expr) {
	switch e := expr.(type) {
	case nil:
		return
	case *hir.Ident:
		if e.LocalID >= 0 && locals.Has(e.LocalID) && !def.Has(e.LocalID) {
			use.Add(e.LocalID)
		}
	case *hir.PrefixExpr:
		accumulateExprUses(use, def, locals, e.Right)
	case *hir.BinaryExpr:
		accumulateExprUses(use, def, locals, e.Left)
		accumulateExprUses(use, def, locals, e.Right)
	case *hir.RangeExpr:
		accumulateExprUses(use, def, locals, e.Start)
		accumulateExprUses(use, def, locals, e.End)
		accumulateExprUses(use, def, locals, e.Step)
	case *hir.PostfixExpr:
		accumulateExprUses(use, def, locals, e.Left)
	case *hir.CallExpr:
		accumulateExprUses(use, def, locals, e.Callee)
		for _, arg := range e.Args {
			accumulateExprUses(use, def, locals, arg)
		}
	case *hir.ClosureLit:
		for _, capture := range e.Captures {
			accumulateExprUses(use, def, locals, capture)
		}
	case *hir.SelectorExpr:
		accumulateExprUses(use, def, locals, e.Left)
	case *hir.CastExpr:
		accumulateExprUses(use, def, locals, e.Left)
	case *hir.CompositeLit:
		for _, item := range e.Items {
			accumulateExprUses(use, def, locals, item.Value)
		}
	}
}
