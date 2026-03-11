package cfganalysis

import (
	"compiler/internal/cfg"
	"compiler/internal/middleend/hir"
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
		block.Use = cfg.NewNameSet()
		block.Def = cfg.NewNameSet()
		block.LiveIn = cfg.NewNameSet()
		block.LiveOut = cfg.NewNameSet()
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
			liveOut := cfg.NewNameSet()
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

func collectFunctionLocals(fn *hir.Func) cfg.NameSet {
	locals := cfg.NewNameSet()
	if fn == nil {
		return locals
	}
	if fn.Receiver != nil {
		locals.Add(fn.Receiver.Name)
	}
	for _, param := range fn.Params {
		if param != nil {
			locals.Add(param.Name)
		}
	}
	collectBlockLocals(locals, fn.Body)
	return locals
}

func collectBlockLocals(locals cfg.NameSet, block *hir.BlockStmt) {
	if locals == nil || block == nil {
		return
	}
	for _, stmt := range block.Stmts {
		collectStmtLocals(locals, stmt)
	}
}

func collectStmtLocals(locals cfg.NameSet, stmt hir.Stmt) {
	switch s := stmt.(type) {
	case nil:
		return
	case *hir.BlockStmt:
		collectBlockLocals(locals, s)
	case *hir.LetStmt:
		locals.Add(s.Name)
	case *hir.ConstStmt:
		locals.Add(s.Name)
	case *hir.IfStmt:
		collectBlockLocals(locals, s.Then)
		collectStmtLocals(locals, s.Else)
	case *hir.MatchStmt:
		for _, arm := range s.Arms {
			if arm != nil {
				collectBlockLocals(locals, arm.Body)
			}
		}
	case *hir.WhileStmt:
		collectBlockLocals(locals, s.Body)
	case *hir.ForStmt:
		if s.IndexName != "" {
			locals.Add(s.IndexName)
		}
		if s.ValueName != "" {
			locals.Add(s.ValueName)
		}
		collectBlockLocals(locals, s.Body)
	case *hir.LoopStmt:
		collectStmtLocals(locals, s.Init)
		collectStmtLocals(locals, s.Post)
		collectBlockLocals(locals, s.Body)
	case *hir.LabelStmt:
		collectStmtLocals(locals, s.Stmt)
	case *hir.DeferStmt:
		collectStmtLocals(locals, s.Body)
	case *hir.LockStmt:
		locals.Add(s.Name)
		collectBlockLocals(locals, s.Body)
	case *hir.UnsafeStmt:
		collectBlockLocals(locals, s.Body)
	}
}

func computeBlockUseDef(block *cfg.Block, locals cfg.NameSet) {
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

func accumulateStmtUseDef(use, def, locals cfg.NameSet, stmt hir.Stmt) {
	switch s := stmt.(type) {
	case nil:
		return
	case *hir.LetStmt:
		accumulateExprUses(use, def, locals, s.Value)
		def.Add(s.Name)
	case *hir.ConstStmt:
		accumulateExprUses(use, def, locals, s.Value)
		def.Add(s.Name)
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
		def.Add(s.Name)
	case *hir.BreakStmt, *hir.ContinueStmt, *hir.UnsafeStmt:
		return
	}
}

func accumulateAssignTarget(use, def, locals cfg.NameSet, expr hir.Expr) {
	switch e := expr.(type) {
	case nil:
		return
	case *hir.Ident:
		if len(e.Path) == 1 && locals.Has(e.Path[0]) {
			def.Add(e.Path[0])
		}
	default:
		accumulateExprUses(use, def, locals, expr)
	}
}

func accumulateDeferredUses(use, def, locals cfg.NameSet, stmt hir.Stmt) {
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
			if !arm.Wildcard {
				accumulateExprUses(use, def, locals, arm.Pattern)
			}
			accumulateDeferredUses(use, def, locals, arm.Body)
		}
	case *hir.WhileStmt:
		accumulateExprUses(use, def, locals, s.Cond)
		accumulateDeferredUses(use, def, locals, s.Body)
	case *hir.ForStmt:
		accumulateExprUses(use, def, locals, s.Iterable)
		if s.IndexName != "" {
			def.Add(s.IndexName)
		}
		if s.ValueName != "" {
			def.Add(s.ValueName)
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

func accumulateExprUses(use, def, locals cfg.NameSet, expr hir.Expr) {
	switch e := expr.(type) {
	case nil:
		return
	case *hir.Ident:
		if len(e.Path) == 1 && locals.Has(e.Path[0]) && !def.Has(e.Path[0]) {
			use.Add(e.Path[0])
		}
	case *hir.PrefixExpr:
		accumulateExprUses(use, def, locals, e.Right)
	case *hir.BinaryExpr:
		accumulateExprUses(use, def, locals, e.Left)
		accumulateExprUses(use, def, locals, e.Right)
	case *hir.PostfixExpr:
		accumulateExprUses(use, def, locals, e.Left)
	case *hir.CallExpr:
		accumulateExprUses(use, def, locals, e.Callee)
		for _, arg := range e.Args {
			accumulateExprUses(use, def, locals, arg)
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
