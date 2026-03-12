package cfganalysis

import (
	"fmt"

	"compiler/internal/cfg"
	"compiler/internal/context"
	"compiler/internal/diagnostics"
	"compiler/internal/middleend/hir"
	"compiler/internal/phase"
	"compiler/internal/semantics/typeinfo"
	"compiler/internal/source"
)

type loopContext struct {
	label        string
	breakTarget  *cfg.Block
	continueTerm *cfg.Block
	deferDepth   int
}

type builder struct {
	ctx        *context.CompilerContext
	mod        *context.Module
	fn         *cfg.Function
	nextID     int
	loopStack  []loopContext
	deferStack []hir.Stmt
}

func AnalyzeModule(ctx *context.CompilerContext, mod *context.Module) {
	if ctx == nil || mod == nil || mod.LoweredHIR == nil {
		return
	}
	module := &cfg.Module{Functions: make([]*cfg.Function, 0, len(mod.LoweredHIR.Functions))}
	for _, fn := range mod.LoweredHIR.Functions {
		module.Functions = append(module.Functions, buildFunction(ctx, mod, fn))
	}
	mod.CFG = module
	for _, fn := range module.Functions {
		if fn != nil {
			analyzeFunction(ctx, fn)
		}
	}
	mod.Phase = phase.PhaseCFGAnalyzed
}

func buildFunction(ctx *context.CompilerContext, mod *context.Module, sourceFn *hir.Func) *cfg.Function {
	if sourceFn == nil {
		return nil
	}
	if sourceFn.Body == nil {
		return &cfg.Function{Name: sourceFn.Name, Source: sourceFn}
	}
	b := &builder{ctx: ctx, mod: mod}
	fn := &cfg.Function{Name: sourceFn.Name, Source: sourceFn}
	b.fn = fn
	fn.Entry = b.newBlock()
	fn.Exit = b.newBlock()
	nextBlock := b.buildBlock(sourceFn.Body, fn.Entry)
	if nextBlock != nil && nextBlock.Terminator == nil {
		nextBlock.Terminator = &cfg.JumpTerm{Target: fn.Exit}
	}
	return fn
}

func (b *builder) newBlock() *cfg.Block {
	block := &cfg.Block{ID: b.nextID, Stmts: make([]hir.Stmt, 0)}
	b.nextID++
	b.fn.Blocks = append(b.fn.Blocks, block)
	return block
}

func (b *builder) buildBlock(block *hir.BlockStmt, current *cfg.Block) *cfg.Block {
	if block == nil {
		return current
	}
	baseDepth := len(b.deferStack)
	defer func() {
		b.deferStack = b.deferStack[:baseDepth]
	}()
	nextBlock := current
	var dead *cfg.Block
	for _, stmt := range block.Stmts {
		if nextBlock == nil {
			if dead == nil {
				dead = b.newBlock()
			}
			dead = b.buildStmt(stmt, dead, "")
			continue
		}
		nextBlock = b.buildStmt(stmt, nextBlock, "")
	}
	if nextBlock != nil && len(b.deferStack) > baseDepth {
		cont := b.newBlock()
		nextBlock.Terminator = b.jumpWithCleanups(cont, baseDepth, nextBlock.Location)
		return cont
	}
	return nextBlock
}

func (b *builder) buildStmt(stmt hir.Stmt, current *cfg.Block, label string) *cfg.Block {
	switch s := stmt.(type) {
	case nil:
		return current
	case *hir.BlockStmt:
		return b.buildBlock(s, current)
	case *hir.LetStmt, *hir.ConstStmt, *hir.AssignStmt:
		current.Stmts = append(current.Stmts, stmt)
		return current
	case *hir.DeferStmt:
		current.Stmts = append(current.Stmts, stmt)
		if s.Body != nil {
			b.deferStack = append(b.deferStack, s.Body)
		}
		return current
	case *hir.ExprStmt:
		current.Stmts = append(current.Stmts, s)
		return current
	case *hir.PanicStmt:
		current.Stmts = append(current.Stmts, s)
		current.Terminator = b.panicWithCleanups(s.Value, 0, s.Loc())
		return nil
	case *hir.ReturnStmt:
		current.Stmts = append(current.Stmts, s)
		current.Terminator = b.returnWithCleanups(s.Value, 0, s.Loc())
		current.Returns = true
		return nil
	case *hir.IfStmt:
		thenBlock := b.newBlock()
		elseBlock := b.newBlock()
		join := b.newBlock()
		thenBlock.Location = s.Loc()
		thenBlock.BranchKind = "if"
		elseBlock.Location = s.Loc()
		elseBlock.BranchKind = "else"
		current.Terminator = &cfg.BranchTerm{Cond: s.Cond, True: thenBlock, False: elseBlock}
		thenEnd := b.buildBlock(s.Then, thenBlock)
		thenFallsThrough := thenEnd != nil
		if thenEnd != nil && thenEnd.Terminator == nil {
			thenEnd.Terminator = &cfg.JumpTerm{Target: join}
		}
		elseEnd := b.buildStmt(s.Else, elseBlock, "")
		elseFallsThrough := elseEnd != nil
		if elseEnd != nil && elseEnd.Terminator == nil {
			elseEnd.Terminator = &cfg.JumpTerm{Target: join}
		}
		if !thenFallsThrough && !elseFallsThrough {
			return nil
		}
		return join
	case *hir.MatchStmt:
		join := b.newBlock()
		join.Location = s.Loc()
		join.BranchKind = "match-fallback"
		edges := make([]cfg.SwitchEdge, 0, len(s.Arms))
		defaultTarget := join
		for _, arm := range s.Arms {
			if arm == nil {
				continue
			}
			armBlock := b.newBlock()
			armBlock.Location = arm.Body.Loc()
			armBlock.BranchKind = "match-arm"
			if arm.Wildcard {
				defaultTarget = armBlock
			} else {
				edges = append(edges, cfg.SwitchEdge{Expr: arm.Pattern, Target: armBlock})
			}
			armEnd := b.buildBlock(arm.Body, armBlock)
			if armEnd != nil && armEnd.Terminator == nil {
				armEnd.Terminator = &cfg.JumpTerm{Target: join}
			}
		}
		current.Terminator = &cfg.SwitchTerm{Value: s.Value, Cases: edges, Default: defaultTarget}
		return join
	case *hir.WhileStmt:
		return b.buildStmt(&hir.LoopStmt{Cond: s.Cond, Body: s.Body}, current, label)
	case *hir.ForStmt:
		condBlock := b.newBlock()
		bodyBlock := b.newBlock()
		exitBlock := b.newBlock()
		current.Terminator = &cfg.JumpTerm{Target: condBlock}
		b.loopStack = append(b.loopStack, loopContext{label: label, breakTarget: exitBlock, continueTerm: condBlock, deferDepth: len(b.deferStack)})
		condBlock.Terminator = &cfg.BranchTerm{Cond: s.Iterable, True: bodyBlock, False: exitBlock}
		bodyEnd := b.buildBlock(s.Body, bodyBlock)
		if bodyEnd != nil && bodyEnd.Terminator == nil {
			bodyEnd.Terminator = &cfg.JumpTerm{Target: condBlock}
		}
		b.loopStack = b.loopStack[:len(b.loopStack)-1]
		return exitBlock
	case *hir.LoopStmt:
		loopEntry := current
		if s.Init != nil {
			loopEntry = b.buildStmt(s.Init, current, "")
			if loopEntry == nil {
				return nil
			}
		}
		condBlock := b.newBlock()
		bodyBlock := b.newBlock()
		exitBlock := b.newBlock()
		loopEntry.Terminator = &cfg.JumpTerm{Target: condBlock}
		continueTarget := condBlock
		if s.Post != nil {
			continueTarget = b.newBlock()
		}
		b.loopStack = append(b.loopStack, loopContext{label: label, breakTarget: exitBlock, continueTerm: continueTarget, deferDepth: len(b.deferStack)})
		if s.Cond != nil {
			condBlock.Terminator = &cfg.BranchTerm{Cond: s.Cond, True: bodyBlock, False: exitBlock}
		} else {
			condBlock.Terminator = &cfg.JumpTerm{Target: bodyBlock}
		}
		bodyEnd := b.buildBlock(s.Body, bodyBlock)
		if bodyEnd != nil && bodyEnd.Terminator == nil {
			bodyEnd.Terminator = &cfg.JumpTerm{Target: continueTarget}
		}
		if s.Post != nil {
			postEnd := b.buildStmt(s.Post, continueTarget, "")
			if postEnd != nil && postEnd.Terminator == nil {
				postEnd.Terminator = &cfg.JumpTerm{Target: condBlock}
			}
		}
		b.loopStack = b.loopStack[:len(b.loopStack)-1]
		return exitBlock
	case *hir.LabelStmt:
		return b.buildStmt(s.Stmt, current, s.Name)
	case *hir.BreakStmt:
		current.Stmts = append(current.Stmts, s)
		if target, depth := b.loopTarget(s.Label, true); target != nil {
			current.Terminator = b.jumpWithCleanups(target, depth, s.Loc())
		}
		return nil
	case *hir.ContinueStmt:
		current.Stmts = append(current.Stmts, s)
		if target, depth := b.loopTarget(s.Label, false); target != nil {
			current.Terminator = b.jumpWithCleanups(target, depth, s.Loc())
		}
		return nil
	case *hir.LockStmt:
		current.Stmts = append(current.Stmts, s)
		return b.buildBlock(s.Body, current)
	case *hir.UnsafeStmt:
		current.Stmts = append(current.Stmts, s)
		return b.buildBlock(s.Body, current)
	default:
		current.Stmts = append(current.Stmts, stmt)
		return current
	}
}

func (b *builder) loopTarget(label string, breakTarget bool) (*cfg.Block, int) {
	for i := len(b.loopStack) - 1; i >= 0; i-- {
		entry := b.loopStack[i]
		if label != "" && entry.label != label {
			continue
		}
		if breakTarget {
			return entry.breakTarget, entry.deferDepth
		}
		return entry.continueTerm, entry.deferDepth
	}
	return nil, len(b.deferStack)
}

func (b *builder) jumpWithCleanups(target *cfg.Block, minDepth int, loc source.Location) cfg.Terminator {
	first := b.buildCleanupChain(minDepth, target, nil, loc)
	if first != nil && first != target {
		return &cfg.JumpTerm{Target: first}
	}
	return &cfg.JumpTerm{Target: target}
}

func (b *builder) returnWithCleanups(value hir.Expr, minDepth int, loc source.Location) cfg.Terminator {
	if len(b.deferStack) <= minDepth {
		return &cfg.ReturnTerm{Value: value}
	}
	final := b.newBlock()
	final.Location = loc
	final.BranchKind = "cleanup-final-return"
	final.Terminator = &cfg.ReturnTerm{Value: value}
	first := b.buildCleanupChain(minDepth, final, value, loc)
	return &cfg.ReturnTerm{Value: value, Cleanup: first}
}

func (b *builder) panicWithCleanups(value hir.Expr, minDepth int, loc source.Location) cfg.Terminator {
	if len(b.deferStack) <= minDepth {
		return &cfg.PanicTerm{Value: value}
	}
	final := b.newBlock()
	final.Location = loc
	final.BranchKind = "cleanup-final-panic"
	final.Terminator = &cfg.PanicTerm{Value: value}
	first := b.buildCleanupChain(minDepth, final, value, loc)
	return &cfg.PanicTerm{Value: value, Cleanup: first}
}

func (b *builder) buildCleanupChain(minDepth int, tail *cfg.Block, panicValue hir.Expr, loc source.Location) *cfg.Block {
	next := tail
	for i := len(b.deferStack) - 1; i >= minDepth; i-- {
		body := b.deferStack[i]
		cleanup := b.newBlock()
		cleanup.Location = body.Loc()
		cleanup.BranchKind = "cleanup"
		end := b.buildStmt(body, cleanup, "")
		if end != nil && end.Terminator == nil {
			end.Terminator = &cfg.JumpTerm{Target: next}
		}
		if end == nil && cleanup.Terminator == nil {
			if panicValue != nil {
				cleanup.Terminator = &cfg.PanicTerm{Value: panicValue}
			} else {
				cleanup.Terminator = &cfg.JumpTerm{Target: next}
			}
		}
		next = cleanup
	}
	return next
}

func analyzeFunction(ctx *context.CompilerContext, fn *cfg.Function) {
	if ctx == nil || fn == nil || fn.Entry == nil {
		return
	}
	reachable := make(map[int]bool)
	markReachable(fn.Entry, reachable)
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		block.Reachable = reachable[block.ID]
	}
	rebuildPredecessors(fn)
	computeLiveness(fn)
	for _, block := range fn.Blocks {
		if block == nil || block.Reachable || len(block.Stmts) == 0 {
			continue
		}
		loc := unreachableRangeLocation(block)
		ctx.Diagnostics.Add(
			diagnostics.NewWarning("unreachable code").
				WithCode(diagnostics.WarnUnreachableCode).
				WithPrimaryLabel(&loc, "this code is unreachable").
				WithHelp("remove this code or restructure control flow"),
		)
	}
	if fn.Exit.Reachable && !isVoidType(fn.Source.Result) {
		loc := fn.Source.Location
		diag := diagnostics.NewError(fmt.Sprintf("non-void function %q does not return on all paths", fn.Name)).
			WithCode(diagnostics.ErrMissingReturn).
			WithPrimaryLabel(&loc, "not all control-flow paths return a value")
		for _, branch := range findMissingReturnBranches(fn) {
			if branch == nil || branch.Location.Start == nil {
				continue
			}
			msg := "missing return in this branch"
			switch branch.BranchKind {
			case "else":
				msg = fmt.Sprintf("missing return in else branch at line %d", branch.Location.Start.Line)
			case "if":
				msg = fmt.Sprintf("missing return in if branch at line %d", branch.Location.Start.Line)
			case "match-arm":
				msg = fmt.Sprintf("missing return in match arm at line %d", branch.Location.Start.Line)
			case "match-fallback":
				msg = fmt.Sprintf("missing return in match fallback path at line %d", branch.Location.Start.Line)
			}
			diag.WithSecondaryLabel(&branch.Location, msg)
		}
		diag.WithHelp("make sure every branch returns, or add a final return at the end of the function")
		ctx.Diagnostics.Add(diag)
	}
}

func markReachable(block *cfg.Block, seen map[int]bool) {
	if block == nil || seen[block.ID] {
		return
	}
	seen[block.ID] = true
	if block.Terminator == nil {
		return
	}
	for _, succ := range block.Terminator.Successors() {
		markReachable(succ, seen)
	}
}

func isVoidType(result typeinfo.Type) bool {
	if result == nil {
		return true
	}
	return typeinfo.IsBuiltinNamed(result, "void")
}

func rebuildPredecessors(fn *cfg.Function) {
	if fn == nil {
		return
	}
	for _, block := range fn.Blocks {
		if block != nil {
			block.Predecessors = block.Predecessors[:0]
		}
	}
	for _, block := range fn.Blocks {
		if block == nil || block.Terminator == nil {
			continue
		}
		for _, succ := range block.Terminator.Successors() {
			if succ != nil {
				succ.Predecessors = append(succ.Predecessors, block)
			}
		}
	}
}

func unreachableRangeLocation(block *cfg.Block) source.Location {
	if block == nil || len(block.Stmts) == 0 {
		return source.Location{}
	}
	start := block.Stmts[0].Loc()
	end := block.Stmts[len(block.Stmts)-1].Loc()
	if start.Start == nil || end.End == nil {
		return start
	}
	return source.NewLocation(filenameOf(start, end), *start.Start, *end.End)
}

func filenameOf(a, b source.Location) string {
	if a.Filename != nil {
		return *a.Filename
	}
	if b.Filename != nil {
		return *b.Filename
	}
	return ""
}

func findMissingReturnBranches(fn *cfg.Function) []*cfg.Block {
	if fn == nil || fn.Entry == nil || fn.Exit == nil {
		return nil
	}
	reachesExit := make(map[*cfg.Block]bool)
	visited := make(map[*cfg.Block]bool)
	var walk func(*cfg.Block) bool
	walk = func(block *cfg.Block) bool {
		if block == nil {
			return false
		}
		if block == fn.Exit {
			return true
		}
		if block.Returns {
			return false
		}
		if done, ok := visited[block]; ok {
			return done
		}
		visited[block] = false
		hitsExit := false
		if block.Terminator != nil {
			for _, succ := range block.Terminator.Successors() {
				if walk(succ) {
					hitsExit = true
				}
			}
		}
		visited[block] = hitsExit
		if hitsExit {
			reachesExit[block] = true
		}
		return hitsExit
	}
	_ = walk(fn.Entry)
	found := make([]*cfg.Block, 0)
	seen := make(map[*cfg.Block]bool)
	for block := range reachesExit {
		if block.BranchKind != "" {
			if !seen[block] {
				found = append(found, block)
				seen[block] = true
			}
			continue
		}
		queue := append([]*cfg.Block(nil), block.Predecessors...)
		traceSeen := make(map[*cfg.Block]bool)
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			if cur == nil || traceSeen[cur] {
				continue
			}
			traceSeen[cur] = true
			if cur.BranchKind != "" {
				if !seen[cur] {
					found = append(found, cur)
					seen[cur] = true
				}
				continue
			}
			queue = append(queue, cur.Predecessors...)
		}
	}
	return found
}
