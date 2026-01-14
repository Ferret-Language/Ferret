package analysis

import (
	"compiler/internal/context_v2"
	"compiler/internal/diagnostics"
	"compiler/internal/hir"
	"compiler/internal/hir/consteval"
	"compiler/internal/source"
	"fmt"
)

// AnalyzeModule performs control flow analysis on a HIR module.
// This runs after type checking and validates:
// - All code paths return appropriate values
// - Loops have escape paths (no infinite loops)
// - Unreachable code detection
// - Dead code detection (constant conditions)
func AnalyzeModule(ctx *context_v2.CompilerContext, mod *context_v2.Module, hirMod *hir.Module) {
	if hirMod == nil {
		return
	}

	// Populate constant values and run const-eval dependent checks on HIR.
	propagateConstants(ctx, mod, hirMod)

	// First, check for constant conditions (dead code detection).
	checkConstantConditions(ctx, mod, hirMod)

	// Analyze control flow for all function and method declarations.
	for _, node := range hirMod.Items {
		switch decl := node.(type) {
		case *hir.FuncDecl:
			analyzeFuncDecl(ctx, mod, decl)
		case *hir.MethodDecl:
			analyzeMethodDecl(ctx, mod, decl)
		}
	}

	// Check catch handlers that rely on returning without fallback values.
	checkCatchHandlers(ctx, mod, hirMod)

	// Enforce borrow rules for references.
	checkBorrowRules(ctx, mod, hirMod)
}

// checkConstantConditions checks for constant conditions in if statements and while loops.
// This detects dead code (branches that never execute).
func checkConstantConditions(ctx *context_v2.CompilerContext, mod *context_v2.Module, hirMod *hir.Module) {
	for _, node := range hirMod.Items {
		checkNodeForConstantConditions(ctx, mod, node)
	}
}

// checkNodeForConstantConditions recursively checks nodes for constant conditions.
func checkNodeForConstantConditions(ctx *context_v2.CompilerContext, mod *context_v2.Module, node hir.Node) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *hir.FuncDecl:
		if n.Body != nil {
			checkBlockForConstantConditions(ctx, mod, n.Body)
		}
	case *hir.MethodDecl:
		if n.Body != nil {
			checkBlockForConstantConditions(ctx, mod, n.Body)
		}
	case *hir.Block:
		checkBlockForConstantConditions(ctx, mod, n)
	}
}

// checkBlockForConstantConditions checks a block for constant conditions.
func checkBlockForConstantConditions(ctx *context_v2.CompilerContext, mod *context_v2.Module, block *hir.Block) {
	if block == nil {
		return
	}

	for _, node := range block.Nodes {
		switch n := node.(type) {
		case *hir.IfStmt:
			checkIfStmtConstantCondition(ctx, mod, n)
			// Recursively check nested blocks.
			if n.Body != nil {
				checkBlockForConstantConditions(ctx, mod, n.Body)
			}
			if n.Else != nil {
				if elseBlock, ok := n.Else.(*hir.Block); ok {
					checkBlockForConstantConditions(ctx, mod, elseBlock)
				} else if elseIf, ok := n.Else.(*hir.IfStmt); ok {
					checkIfStmtConstantCondition(ctx, mod, elseIf)
					if elseIf.Body != nil {
						checkBlockForConstantConditions(ctx, mod, elseIf.Body)
					}
					if elseIf.Else != nil {
						if elseBlock, ok := elseIf.Else.(*hir.Block); ok {
							checkBlockForConstantConditions(ctx, mod, elseBlock)
						}
					}
				}
			}
		case *hir.WhileStmt:
			checkWhileStmtConstantCondition(ctx, mod, n)
			if n.Body != nil {
				checkBlockForConstantConditions(ctx, mod, n.Body)
			}
		case *hir.ForStmt:
			if n.Body != nil {
				checkBlockForConstantConditions(ctx, mod, n.Body)
			}
		case *hir.MatchStmt:
			checkMatchStmtCases(ctx, n)
			for _, clause := range n.Cases {
				checkBlockForConstantConditions(ctx, mod, clause.Body)
			}
		case *hir.Block:
			checkBlockForConstantConditions(ctx, mod, n)
		}
	}
}

func checkMatchStmtCases(ctx *context_v2.CompilerContext, stmt *hir.MatchStmt) {
	if ctx == nil || stmt == nil {
		return
	}

	hasDefault := false
	for i, clause := range stmt.Cases {
		if clause.Pattern == nil {
			if hasDefault {
				ctx.Diagnostics.Add(
					diagnostics.NewError("multiple default cases in match statement").
						WithPrimaryLabel(&clause.Location, "duplicate default case").
						WithNote("only one default case (_) is allowed per match statement"),
				)
			}
			hasDefault = true
		}

		if hasDefault && i < len(stmt.Cases)-1 {
			next := stmt.Cases[i+1]
			ctx.Diagnostics.Add(
				diagnostics.NewWarning("unreachable case after default case").
					WithPrimaryLabel(&next.Location, "this case will never be reached").
					WithNote("default case matches all remaining values"),
			)
		}
	}
}

// checkIfStmtConstantCondition checks if an if statement has a constant condition.
func checkIfStmtConstantCondition(ctx *context_v2.CompilerContext, mod *context_v2.Module, stmt *hir.IfStmt) {
	if stmt.Cond == nil {
		return
	}

	// Evaluate condition as constant.
	if constVal := consteval.EvaluateHIRExpr(ctx, mod, stmt.Cond); constVal != nil {
		if boolVal, ok := constVal.AsBool(); ok {
			if boolVal {
				// Condition is always true - else branch is dead code.
				if stmt.Else != nil {
					ctx.Diagnostics.Add(
						diagnostics.NewWarning("condition is always true").
							WithCode(diagnostics.WarnConstantConditionTrue).
							WithPrimaryLabel(stmt.Cond.Loc(), "this condition is always true").
							WithSecondaryLabel(stmt.Else.Loc(), "this branch will never execute").
							WithHelp("remove the if statement or the unreachable else branch"),
					)
				}
			} else {
				// Condition is always false - then branch is dead code.
				ctx.Diagnostics.Add(
					diagnostics.NewWarning("condition is always false").
						WithCode(diagnostics.WarnConstantConditionFalse).
						WithPrimaryLabel(stmt.Cond.Loc(), "this condition is always false").
						WithSecondaryLabel(stmt.Body.Loc(), "this branch will never execute").
						WithHelp("remove the if statement or fix the condition"),
				)
			}
		}
	}
}

// checkWhileStmtConstantCondition checks if a while statement has a constant condition.
func checkWhileStmtConstantCondition(ctx *context_v2.CompilerContext, mod *context_v2.Module, stmt *hir.WhileStmt) {
	if stmt.Cond == nil {
		return
	}

	// Evaluate condition as constant.
	if constVal := consteval.EvaluateHIRExpr(ctx, mod, stmt.Cond); constVal != nil {
		if boolVal, ok := constVal.AsBool(); ok {
			// Only warn if condition is always false - loop never executes.
			// Always-true conditions are often intentional (infinite loops with break).
			if !boolVal {
				ctx.Diagnostics.Add(
					diagnostics.NewWarning("condition is always false").
						WithCode(diagnostics.WarnConstantConditionFalse).
						WithPrimaryLabel(stmt.Cond.Loc(), "this condition is always false").
						WithSecondaryLabel(stmt.Body.Loc(), "this loop will never execute").
						WithHelp("remove the while loop or fix the condition"),
				)
			}
		}
	}
}

// checkCatchHandlers ensures handler-only catches return on all paths.
func checkCatchHandlers(ctx *context_v2.CompilerContext, mod *context_v2.Module, hirMod *hir.Module) {
	for _, node := range hirMod.Items {
		checkNodeForCatchHandlers(ctx, mod, node)
	}
}

func checkNodeForCatchHandlers(ctx *context_v2.CompilerContext, mod *context_v2.Module, node hir.Node) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *hir.FuncDecl:
		checkBlockForCatchHandlers(ctx, mod, n.Body)
	case *hir.MethodDecl:
		checkBlockForCatchHandlers(ctx, mod, n.Body)
	case *hir.Block:
		checkBlockForCatchHandlers(ctx, mod, n)
	case *hir.DeclStmt:
		checkNodeForCatchHandlers(ctx, mod, n.Decl)
	case *hir.VarDecl:
		for _, item := range n.Decls {
			checkExprForCatchHandlers(ctx, mod, item.Value)
		}
	case *hir.ConstDecl:
		for _, item := range n.Decls {
			checkExprForCatchHandlers(ctx, mod, item.Value)
		}
	case *hir.AssignStmt:
		checkExprForCatchHandlers(ctx, mod, n.Lhs)
		checkExprForCatchHandlers(ctx, mod, n.Rhs)
	case *hir.ReturnStmt:
		checkExprForCatchHandlers(ctx, mod, n.Result)
	case *hir.ExprStmt:
		checkExprForCatchHandlers(ctx, mod, n.X)
	case *hir.IfStmt:
		checkExprForCatchHandlers(ctx, mod, n.Cond)
		checkBlockForCatchHandlers(ctx, mod, n.Body)
		checkNodeForCatchHandlers(ctx, mod, n.Else)
	case *hir.ForStmt:
		checkNodeForCatchHandlers(ctx, mod, n.Iterator)
		checkExprForCatchHandlers(ctx, mod, n.Range)
		checkBlockForCatchHandlers(ctx, mod, n.Body)
	case *hir.WhileStmt:
		checkExprForCatchHandlers(ctx, mod, n.Cond)
		checkBlockForCatchHandlers(ctx, mod, n.Body)
	case *hir.MatchStmt:
		checkExprForCatchHandlers(ctx, mod, n.Expr)
		for _, clause := range n.Cases {
			checkExprForCatchHandlers(ctx, mod, clause.Pattern)
			checkBlockForCatchHandlers(ctx, mod, clause.Body)
		}
	case *hir.DeferStmt:
		checkExprForCatchHandlers(ctx, mod, n.Call)
	default:
		if expr, ok := node.(hir.Expr); ok {
			checkExprForCatchHandlers(ctx, mod, expr)
		}
	}
}

func checkBlockForCatchHandlers(ctx *context_v2.CompilerContext, mod *context_v2.Module, block *hir.Block) {
	if block == nil {
		return
	}

	for _, node := range block.Nodes {
		checkNodeForCatchHandlers(ctx, mod, node)
	}
}

func checkExprForCatchHandlers(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr hir.Expr) {
	if expr == nil {
		return
	}

	switch e := expr.(type) {
	case *hir.CallExpr:
		checkExprForCatchHandlers(ctx, mod, e.Fun)
		for _, arg := range e.Args {
			checkExprForCatchHandlers(ctx, mod, arg)
		}
		if e.Catch != nil {
			if e.Catch.Handler != nil {
				checkCatchHandlerReturns(ctx, mod, e.Catch)
				checkBlockForCatchHandlers(ctx, mod, e.Catch.Handler)
			}
			if e.Catch.Fallback != nil {
				checkExprForCatchHandlers(ctx, mod, e.Catch.Fallback)
			}
		}
	case *hir.BinaryExpr:
		checkExprForCatchHandlers(ctx, mod, e.X)
		checkExprForCatchHandlers(ctx, mod, e.Y)
	case *hir.UnaryExpr:
		checkExprForCatchHandlers(ctx, mod, e.X)
	case *hir.PrefixExpr:
		checkExprForCatchHandlers(ctx, mod, e.X)
	case *hir.PostfixExpr:
		checkExprForCatchHandlers(ctx, mod, e.X)
	case *hir.SelectorExpr:
		checkExprForCatchHandlers(ctx, mod, e.X)
	case *hir.ScopeResolutionExpr:
		checkExprForCatchHandlers(ctx, mod, e.X)
	case *hir.RangeExpr:
		checkExprForCatchHandlers(ctx, mod, e.Start)
		checkExprForCatchHandlers(ctx, mod, e.End)
		checkExprForCatchHandlers(ctx, mod, e.Incr)
	case *hir.IndexExpr:
		checkExprForCatchHandlers(ctx, mod, e.X)
		checkExprForCatchHandlers(ctx, mod, e.Index)
	case *hir.CastExpr:
		checkExprForCatchHandlers(ctx, mod, e.X)
	case *hir.CoalescingExpr:
		checkExprForCatchHandlers(ctx, mod, e.Cond)
		checkExprForCatchHandlers(ctx, mod, e.Default)
	case *hir.ForkExpr:
		checkExprForCatchHandlers(ctx, mod, e.Call)
	case *hir.ParenExpr:
		checkExprForCatchHandlers(ctx, mod, e.X)
	case *hir.CompositeLit:
		for _, elt := range e.Elts {
			checkExprForCatchHandlers(ctx, mod, elt)
		}
	case *hir.KeyValueExpr:
		checkExprForCatchHandlers(ctx, mod, e.Key)
		checkExprForCatchHandlers(ctx, mod, e.Value)
	case *hir.FuncLit:
		checkBlockForCatchHandlers(ctx, mod, e.Body)
	case *hir.OptionalSome:
		checkExprForCatchHandlers(ctx, mod, e.Value)
	case *hir.OptionalIsSome:
		checkExprForCatchHandlers(ctx, mod, e.Value)
	case *hir.OptionalIsNone:
		checkExprForCatchHandlers(ctx, mod, e.Value)
	case *hir.OptionalUnwrap:
		checkExprForCatchHandlers(ctx, mod, e.Value)
		checkExprForCatchHandlers(ctx, mod, e.Default)
	case *hir.ResultOk:
		checkExprForCatchHandlers(ctx, mod, e.Value)
	case *hir.ResultErr:
		checkExprForCatchHandlers(ctx, mod, e.Value)
	case *hir.ResultUnwrap:
		checkExprForCatchHandlers(ctx, mod, e.Value)
		if e.Catch != nil {
			checkCatchHandlerReturns(ctx, mod, e.Catch)
			checkBlockForCatchHandlers(ctx, mod, e.Catch.Handler)
			checkExprForCatchHandlers(ctx, mod, e.Catch.Fallback)
		}
	}
}

func checkCatchHandlerReturns(ctx *context_v2.CompilerContext, mod *context_v2.Module, catch *hir.CatchClause) {
	if catch == nil || catch.Handler == nil {
		return
	}

	cfgBuilder := NewCFGBuilder(ctx, mod)
	cfg := cfgBuilder.BuildBlockCFG(catch.Handler)

	if catch.Fallback != nil || AllPathsReturn(cfg) {
		return
	}

	diag := diagnostics.NewError("catch handler must return early or provide a fallback value").
		WithPrimaryLabel(catch.Loc(), "handler can fall through without returning")

	if handlerLoc := catch.Handler.Loc(); handlerLoc != nil && handlerLoc.End != nil && handlerLoc.Filename != nil {
		fallbackLoc := source.NewLocation(handlerLoc.Filename, handlerLoc.End, handlerLoc.End)
		diag.WithSecondaryLabel(fallbackLoc, "add fallback value here")
	}

	missingBranches := MissingReturnBranches(cfg)
	for _, block := range missingBranches {
		if block == nil || block.Location == nil {
			continue
		}
		msg := "catch handler can reach end without returning"
		if block.BranchKind == "else" {
			msg = fmt.Sprintf("catch handler can reach end in else at line %d", block.Location.Start.Line)
		} else if block.BranchKind == "if" {
			msg = fmt.Sprintf("catch handler can reach end in if at line %d", block.Location.Start.Line)
		} else if block.BranchKind != "" {
			msg = fmt.Sprintf("catch handler can reach end in %s at line %d", block.BranchKind, block.Location.Start.Line)
		}
		diag.WithSecondaryLabel(block.Location, msg)
	}

	diag.WithHelp("add return statements to every branch or provide a fallback value after the catch block")
	ctx.Diagnostics.Add(diag)
}

// analyzeFuncDecl performs CFG analysis on a function declaration.
func analyzeFuncDecl(ctx *context_v2.CompilerContext, mod *context_v2.Module, decl *hir.FuncDecl) {
	if decl.Body == nil {
		return
	}

	// Build control flow graph for the function.
	cfgBuilder := NewCFGBuilder(ctx, mod)
	cfg := cfgBuilder.BuildFunctionCFG(decl)

	// Analyze return paths.
	AnalyzeReturns(ctx, mod, decl, cfg)
}

// analyzeMethodDecl performs CFG analysis on a method declaration.
func analyzeMethodDecl(ctx *context_v2.CompilerContext, mod *context_v2.Module, decl *hir.MethodDecl) {
	if decl.Body == nil {
		return
	}

	// Convert method to function for CFG analysis.
	tempFuncDecl := &hir.FuncDecl{
		Name:     decl.Name,
		Type:     decl.Type,
		Body:     decl.Body,
		Location: decl.Location,
	}

	// Build control flow graph for the method.
	cfgBuilder := NewCFGBuilder(ctx, mod)
	cfg := cfgBuilder.BuildFunctionCFG(tempFuncDecl)

	// Analyze return paths.
	AnalyzeReturns(ctx, mod, tempFuncDecl, cfg)
}
