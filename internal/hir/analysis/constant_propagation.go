package analysis

import (
	"compiler/internal/context_v2"
	"compiler/internal/diagnostics"
	"compiler/internal/hir"
	"compiler/internal/hir/consteval"
	"compiler/internal/semantics/symbols"
	"compiler/internal/tokens"
	"compiler/internal/types"
	"fmt"
	"math/big"
)

// Track array literal lengths for variables assigned from array literals
var arrayLiteralLengths = make(map[*symbols.Symbol]int)
var rangeExprLengths = make(map[*symbols.Symbol]int)

func propagateConstants(ctx *context_v2.CompilerContext, mod *context_v2.Module, hirMod *hir.Module) {
	if hirMod == nil {
		return
	}
	for _, node := range hirMod.Items {
		walkNodeConstEval(ctx, mod, node)
	}
}

func walkNodeConstEval(ctx *context_v2.CompilerContext, mod *context_v2.Module, node hir.Node) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *hir.FuncDecl:
		walkBlockConstEval(ctx, mod, n.Body)
	case *hir.MethodDecl:
		walkBlockConstEval(ctx, mod, n.Body)
	case *hir.Block:
		walkBlockConstEval(ctx, mod, n)
	case *hir.DeclStmt:
		walkNodeConstEval(ctx, mod, n.Decl)
	case *hir.VarDecl:
		walkDeclItemsConstEval(ctx, mod, n.Decls)
	case *hir.ConstDecl:
		walkDeclItemsConstEval(ctx, mod, n.Decls)
	case *hir.AssignStmt:
		walkAssignConstEval(ctx, mod, n)
	case *hir.ExprStmt:
		walkExprConstEval(ctx, mod, n.X)
	case *hir.ReturnStmt:
		walkExprConstEval(ctx, mod, n.Result)
	case *hir.IfStmt:
		walkExprConstEval(ctx, mod, n.Cond)
		walkBlockConstEval(ctx, mod, n.Body)
		walkNodeConstEval(ctx, mod, n.Else)
	case *hir.ForStmt:
		walkNodeConstEval(ctx, mod, n.Iterator)
		walkExprConstEval(ctx, mod, n.Range)
		walkBlockConstEval(ctx, mod, n.Body)
	case *hir.WhileStmt:
		walkExprConstEval(ctx, mod, n.Cond)
		walkBlockConstEval(ctx, mod, n.Body)
	case *hir.MatchStmt:
		walkExprConstEval(ctx, mod, n.Expr)
		for _, clause := range n.Cases {
			walkExprConstEval(ctx, mod, clause.Pattern)
			walkBlockConstEval(ctx, mod, clause.Body)
		}
	case *hir.DeferStmt:
		walkExprConstEval(ctx, mod, n.Call)
	default:
		if expr, ok := node.(hir.Expr); ok {
			walkExprConstEval(ctx, mod, expr)
		}
	}
}

func walkBlockConstEval(ctx *context_v2.CompilerContext, mod *context_v2.Module, block *hir.Block) {
	if block == nil {
		return
	}
	for _, node := range block.Nodes {
		walkNodeConstEval(ctx, mod, node)
	}
}

func walkDeclItemsConstEval(ctx *context_v2.CompilerContext, mod *context_v2.Module, items []hir.DeclItem) {
	for _, item := range items {
		walkExprConstEval(ctx, mod, item.Value)
		updateConstValue(item.Name, consteval.EvaluateHIRExpr(ctx, mod, item.Value))

		// Track array literal lengths for dynamic arrays
		if item.Name != nil && item.Name.Symbol != nil {
			if lit, ok := item.Value.(*hir.CompositeLit); ok {
				if arrType := arrayTypeOf(item.Value); arrType != nil && arrType.Length < 0 {
					// Dynamic array literal - store its length
					arrayLiteralLengths[item.Name.Symbol] = len(lit.Elts)
				}
			}
			if rng, ok := item.Value.(*hir.RangeExpr); ok {
				if arrType := arrayTypeOf(item.Value); arrType != nil && arrType.Length < 0 {
					if length, ok := rangeConstLength(ctx, mod, rng); ok {
						rangeExprLengths[item.Name.Symbol] = length
					}
				}
			}
		}
	}
}

func walkAssignConstEval(ctx *context_v2.CompilerContext, mod *context_v2.Module, stmt *hir.AssignStmt) {
	if stmt == nil {
		return
	}

	walkExprConstEval(ctx, mod, stmt.Lhs)
	walkExprConstEval(ctx, mod, stmt.Rhs)

	ident, ok := stmt.Lhs.(*hir.Ident)
	if !ok || ident == nil || ident.Symbol == nil {
		return
	}

	if stmt.Op != nil && stmt.Op.Kind != tokens.EQUALS_TOKEN {
		ident.Symbol.ConstValue = nil
		delete(arrayLiteralLengths, ident.Symbol)
		return
	}

	updateConstValue(ident, consteval.EvaluateHIRExpr(ctx, mod, stmt.Rhs))

	// Track array literal lengths for dynamic arrays
	if lit, ok := stmt.Rhs.(*hir.CompositeLit); ok {
		if arrType := arrayTypeOf(stmt.Rhs); arrType != nil && arrType.Length < 0 {
			// Dynamic array literal - store its length
			arrayLiteralLengths[ident.Symbol] = len(lit.Elts)
		} else {
			// Not a dynamic array literal, clear any previous length
			delete(arrayLiteralLengths, ident.Symbol)
		}
		delete(rangeExprLengths, ident.Symbol)
	} else if rng, ok := stmt.Rhs.(*hir.RangeExpr); ok {
		if arrType := arrayTypeOf(stmt.Rhs); arrType != nil && arrType.Length < 0 {
			if length, ok := rangeConstLength(ctx, mod, rng); ok {
				rangeExprLengths[ident.Symbol] = length
			} else {
				delete(rangeExprLengths, ident.Symbol)
			}
		} else {
			delete(rangeExprLengths, ident.Symbol)
		}
		delete(arrayLiteralLengths, ident.Symbol)
	} else {
		// Not a literal, clear any previous length
		delete(arrayLiteralLengths, ident.Symbol)
		delete(rangeExprLengths, ident.Symbol)
	}
}

func walkExprConstEval(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr hir.Expr) {
	if expr == nil {
		return
	}

	switch e := expr.(type) {
	case *hir.Ident, *hir.Literal, *hir.Invalid:
		return
	case *hir.OptionalNone:
		return
	case *hir.OptionalSome:
		walkExprConstEval(ctx, mod, e.Value)
	case *hir.OptionalIsSome:
		walkExprConstEval(ctx, mod, e.Value)
	case *hir.OptionalIsNone:
		walkExprConstEval(ctx, mod, e.Value)
	case *hir.OptionalUnwrap:
		walkExprConstEval(ctx, mod, e.Value)
		walkExprConstEval(ctx, mod, e.Default)
	case *hir.ResultOk:
		walkExprConstEval(ctx, mod, e.Value)
	case *hir.ResultErr:
		walkExprConstEval(ctx, mod, e.Value)
	case *hir.ResultUnwrap:
		walkExprConstEval(ctx, mod, e.Value)
		walkCatchClauseConstEval(ctx, mod, e.Catch)
	case *hir.BinaryExpr:
		walkExprConstEval(ctx, mod, e.X)
		walkExprConstEval(ctx, mod, e.Y)
	case *hir.UnaryExpr:
		walkExprConstEval(ctx, mod, e.X)
		// Clear constant value when address is taken (&x or &mut x)
		// The variable can be modified through the reference
		if e.Op.Kind == tokens.BIT_AND_TOKEN || e.Op.Kind == tokens.MUT_TOKEN {
			clearConstValue(identFromExpr(e.X))
		}
	case *hir.PrefixExpr:
		walkExprConstEval(ctx, mod, e.X)
		clearConstValue(identFromExpr(e.X))
	case *hir.PostfixExpr:
		walkExprConstEval(ctx, mod, e.X)
		clearConstValue(identFromExpr(e.X))
	case *hir.CallExpr:
		walkExprConstEval(ctx, mod, e.Fun)
		for _, arg := range e.Args {
			walkExprConstEval(ctx, mod, arg)
		}
		walkCatchClauseConstEval(ctx, mod, e.Catch)
	case *hir.SelectorExpr:
		walkExprConstEval(ctx, mod, e.X)
	case *hir.ScopeResolutionExpr:
		walkExprConstEval(ctx, mod, e.X)
	case *hir.IndexExpr:
		walkExprConstEval(ctx, mod, e.X)
		walkExprConstEval(ctx, mod, e.Index)
		checkArrayBounds(ctx, mod, e)
	case *hir.CastExpr:
		walkExprConstEval(ctx, mod, e.X)
	case *hir.CoalescingExpr:
		walkExprConstEval(ctx, mod, e.Cond)
		walkExprConstEval(ctx, mod, e.Default)
	case *hir.ForkExpr:
		walkExprConstEval(ctx, mod, e.Call)
	case *hir.ParenExpr:
		walkExprConstEval(ctx, mod, e.X)
	case *hir.CompositeLit:
		for _, elt := range e.Elts {
			walkExprConstEval(ctx, mod, elt)
		}
	case *hir.KeyValueExpr:
		walkExprConstEval(ctx, mod, e.Key)
		walkExprConstEval(ctx, mod, e.Value)
	case *hir.FuncLit:
		walkBlockConstEval(ctx, mod, e.Body)
	case *hir.RangeExpr:
		walkExprConstEval(ctx, mod, e.Start)
		walkExprConstEval(ctx, mod, e.End)
		walkExprConstEval(ctx, mod, e.Incr)
		checkRangeExpr(ctx, mod, e)
	case *hir.ArrayLenExpr:
		walkExprConstEval(ctx, mod, e.X)
	case *hir.StringLenExpr:
		walkExprConstEval(ctx, mod, e.X)
	case *hir.MapIterInitExpr:
		walkExprConstEval(ctx, mod, e.Map)
	case *hir.MapIterNextExpr:
		walkExprConstEval(ctx, mod, e.Map)
		walkExprConstEval(ctx, mod, e.Iter)
	default:
		return
	}
}

func walkCatchClauseConstEval(ctx *context_v2.CompilerContext, mod *context_v2.Module, clause *hir.CatchClause) {
	if clause == nil {
		return
	}
	walkBlockConstEval(ctx, mod, clause.Handler)
	walkExprConstEval(ctx, mod, clause.Fallback)
}

func updateConstValue(ident *hir.Ident, val *consteval.ConstValue) {
	if ident == nil || ident.Symbol == nil {
		return
	}
	if val != nil && val.IsConstant() {
		ident.Symbol.ConstValue = val
		return
	}
	ident.Symbol.ConstValue = nil
}

func clearConstValue(ident *hir.Ident) {
	if ident == nil || ident.Symbol == nil {
		return
	}
	ident.Symbol.ConstValue = nil
}

func identFromExpr(expr hir.Expr) *hir.Ident {
	if ident, ok := expr.(*hir.Ident); ok {
		return ident
	}
	return nil
}

func checkArrayBounds(ctx *context_v2.CompilerContext, mod *context_v2.Module, indexExpr *hir.IndexExpr) {
	if ctx == nil || indexExpr == nil {
		return
	}

	arrType := arrayTypeOf(indexExpr.X)
	if arrType == nil {
		return
	}

	// Get array length: from type for fixed arrays, from literal for dynamic arrays
	var arrayLength int
	var lengthKnown bool
	if arrType.Length >= 0 {
		// Fixed-size array: length is known from type
		arrayLength = arrType.Length
		lengthKnown = true
	} else {
		// Dynamic array: check if it's a literal or variable assigned from literal
		if lit, ok := indexExpr.X.(*hir.CompositeLit); ok {
			// It's an array literal, we can get the length at compile time
			arrayLength = len(lit.Elts)
			lengthKnown = true
		} else if rng, ok := indexExpr.X.(*hir.RangeExpr); ok {
			if length, ok := rangeConstLength(ctx, mod, rng); ok {
				arrayLength = length
				lengthKnown = true
			}
		} else if ident, ok := indexExpr.X.(*hir.Ident); ok && ident.Symbol != nil {
			// It's a variable - check if it was assigned from an array literal
			if len, found := arrayLiteralLengths[ident.Symbol]; found {
				arrayLength = len
				lengthKnown = true
			} else if len, found := rangeExprLengths[ident.Symbol]; found {
				arrayLength = len
				lengthKnown = true
			}
		}

		if !lengthKnown {
			// Can't determine length at compile time
			return
		}
	}

	indexValue, isConstant := evaluateIndexAsInt(ctx, mod, indexExpr.Index)
	if !isConstant {
		// For fixed arrays, require constant index
		if arrType.Length >= 0 {
			ctx.Diagnostics.Add(
				diagnostics.NewError("fixed array index must be a compile-time constant").
					WithCode(diagnostics.ErrArrayIndexNotConst).
					WithPrimaryLabel(indexExpr.Index.Loc(), "non-constant index").
					WithHelp("use a constant index or switch to a dynamic array []T for runtime indexing"),
			)
		}
		// For dynamic arrays with non-constant index, just skip compile-time checking
		return
	}

	originalIndex := indexValue
	if indexValue < 0 {
		indexValue = int64(arrayLength) + indexValue
	}

	if indexValue < 0 || indexValue >= int64(arrayLength) {
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("array index out of bounds: index %d, array length %d", originalIndex, arrayLength)).
				WithCode(diagnostics.ErrArrayOutOfBounds).
				WithPrimaryLabel(indexExpr.Index.Loc(), "index out of range").
				WithNote(fmt.Sprintf("valid indices are 0 to %d, or -%d to -1 for reverse access", arrayLength-1, arrayLength)).
				WithHelp("check the array length before accessing"),
		)
	}
}

func evaluateIndexAsInt(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr hir.Expr) (int64, bool) {
	val := consteval.EvaluateHIRExpr(ctx, mod, expr)
	if val == nil {
		return 0, false
	}
	return val.AsInt64()
}

func rangeConstValues(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *hir.RangeExpr) (*consteval.ConstValue, *consteval.ConstValue, *consteval.ConstValue, bool) {
	if expr == nil {
		return nil, nil, nil, false
	}

	startConst := consteval.EvaluateHIRExpr(ctx, mod, expr.Start)
	endConst := consteval.EvaluateHIRExpr(ctx, mod, expr.End)
	if startConst == nil || !startConst.IsConstant() {
		return nil, nil, nil, false
	}
	if endConst == nil || !endConst.IsConstant() {
		return nil, nil, nil, false
	}

	if expr.Incr != nil {
		incrConst := consteval.EvaluateHIRExpr(ctx, mod, expr.Incr)
		if incrConst == nil || !incrConst.IsConstant() {
			return nil, nil, nil, false
		}
		return startConst, endConst, incrConst, true
	}

	if consteval.NumericIsFloat(startConst) || consteval.NumericIsFloat(endConst) {
		stepType := types.FromTypeName(types.DEFAULT_FLOAT_TYPE)
		if startConst.Type != nil && consteval.NumericIsFloat(startConst) {
			stepType = startConst.Type
		} else if endConst.Type != nil && consteval.NumericIsFloat(endConst) {
			stepType = endConst.Type
		}
		return startConst, endConst, consteval.NewBigFloatValue(big.NewFloat(1), stepType), true
	}
	stepType := types.TypeUnknown
	if startConst.Type != nil && !consteval.NumericIsFloat(startConst) {
		stepType = startConst.Type
	} else if endConst.Type != nil && !consteval.NumericIsFloat(endConst) {
		stepType = endConst.Type
	}
	return startConst, endConst, consteval.NewBigIntValue(big.NewInt(1), stepType), true
}

func rangeConstLength(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *hir.RangeExpr) (int, bool) {
	startVal, endVal, stepVal, ok := rangeConstValues(ctx, mod, expr)
	if !ok || consteval.NumericIsFloat(startVal) || consteval.NumericIsFloat(endVal) || consteval.NumericIsFloat(stepVal) {
		return 0, false
	}
	stepInt, ok := consteval.NumericToBigInt(stepVal)
	if !ok || stepInt == nil || stepInt.Sign() == 0 {
		return 0, false
	}

	startInt, ok := consteval.NumericToBigInt(startVal)
	if !ok || startInt == nil {
		return 0, false
	}
	endInt, ok := consteval.NumericToBigInt(endVal)
	if !ok || endInt == nil {
		return 0, false
	}

	length, ok := consteval.RangeLengthFromInts(startInt, endInt, stepInt, expr.Inclusive)
	if !ok {
		return 0, false
	}
	return length, true
}

func checkRangeExpr(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *hir.RangeExpr) {
	if ctx == nil || expr == nil {
		return
	}

	startVal, endVal, stepVal, ok := rangeConstValues(ctx, mod, expr)
	if !ok {
		return
	}

	// Note: Float ranges are now rejected at type-checking phase, so we only handle integers here.

	stepSign, ok := consteval.NumericSign(stepVal)
	if !ok {
		return
	}
	if stepSign == 0 {
		ctx.Diagnostics.Add(
			diagnostics.NewError("invalid range: step cannot be zero").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(expr.Loc(), "step is zero").
				WithHelp("use a non-zero step value"),
		)
		return
	}

	cmp, ok := consteval.NumericCompare(startVal, endVal)
	if !ok {
		return
	}
	if (cmp < 0 && stepSign < 0) || (cmp > 0 && stepSign > 0) {
		ctx.Diagnostics.Add(
			diagnostics.NewError("invalid range: step must move toward the end value").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(expr.Loc(), "step moves away from the end").
				WithHelp("use a positive step when start < end, or a negative step when start > end"),
		)
	}
}

func arrayTypeOf(expr hir.Expr) *types.ArrayType {
	baseType := exprType(expr)
	if baseType == nil {
		return nil
	}
	baseType = types.UnwrapType(baseType)
	if ref, ok := baseType.(*types.ReferenceType); ok {
		baseType = types.UnwrapType(ref.Inner)
	}
	arrType, _ := baseType.(*types.ArrayType)
	return arrType
}

func exprType(expr hir.Expr) types.SemType {
	switch e := expr.(type) {
	case *hir.Literal:
		return e.Type
	case *hir.Ident:
		return e.Type
	case *hir.OptionalNone:
		return e.Type
	case *hir.OptionalSome:
		return e.Type
	case *hir.OptionalIsSome:
		return e.Type
	case *hir.OptionalIsNone:
		return e.Type
	case *hir.OptionalUnwrap:
		return e.Type
	case *hir.ResultOk:
		return e.Type
	case *hir.ResultErr:
		return e.Type
	case *hir.ResultUnwrap:
		return e.Type
	case *hir.BinaryExpr:
		return e.Type
	case *hir.UnaryExpr:
		return e.Type
	case *hir.PrefixExpr:
		return e.Type
	case *hir.PostfixExpr:
		return e.Type
	case *hir.CallExpr:
		return e.Type
	case *hir.IndexExpr:
		return e.Type
	case *hir.SelectorExpr:
		return e.Type
	case *hir.ScopeResolutionExpr:
		return e.Type
	case *hir.ParenExpr:
		return e.Type
	case *hir.RangeExpr:
		return e.Type
	case *hir.ArrayLenExpr:
		return e.Type
	case *hir.StringLenExpr:
		return e.Type
	case *hir.MapIterInitExpr:
		return e.Type
	case *hir.MapIterNextExpr:
		return e.Type
	case *hir.CastExpr:
		return e.Type
	case *hir.CoalescingExpr:
		return e.Type
	case *hir.ForkExpr:
		return e.Type
	case *hir.CompositeLit:
		return e.Type
	case *hir.FuncLit:
		return e.Type
	default:
		return nil
	}
}
