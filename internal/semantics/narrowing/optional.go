package narrowing

import (
	"compiler/internal/context_v2"
	"compiler/internal/frontend/ast"
	"compiler/internal/types"
)

// analyzeEqualityCondition handles 'x == none' or 'x != none' narrowing for optionals.
func analyzeEqualityCondition(mod *context_v2.Module, binExpr *ast.BinaryExpr, parent *NarrowingContext, isEqual bool) (*NarrowingContext, *NarrowingContext) {
	thenNarrowing := NewNarrowingContext(parent)
	elseNarrowing := NewNarrowingContext(parent)

	expr, isNoneCheck := noneComparisonExpr(binExpr)
	if !isNoneCheck || expr == nil {
		return thenNarrowing, elseNarrowing
	}

	key, ok := ExprKey(expr)
	if !ok || key == "" {
		return thenNarrowing, elseNarrowing
	}

	originalType, optType := optionalTypeFromExpr(mod, expr, parent)
	if optType == nil || optType.Inner == nil || originalType == nil {
		return thenNarrowing, elseNarrowing
	}

	var currentType types.SemType
	if parent != nil {
		if parentEntry, ok := parent.GetEntry(key); ok && parentEntry != nil && parentEntry.NarrowedType != nil {
			currentType = parentEntry.NarrowedType
		}
	}

	if currentType != nil && currentType.Equals(types.TypeNone) {
		entry := optionalEntry(key, originalType, types.TypeNone)
		if isEqual {
			thenNarrowing.Narrow(key, entry)
		} else {
			elseNarrowing.Narrow(key, entry)
		}
		return thenNarrowing, elseNarrowing
	}

	// x == none: then gets none, else gets inner type
	// x != none: then gets inner type, else gets none
	if isEqual {
		thenNarrowing.Narrow(key, optionalEntry(key, originalType, types.TypeNone))
		elseNarrowing.Narrow(key, optionalEntry(key, originalType, optType.Inner))
	} else {
		thenNarrowing.Narrow(key, optionalEntry(key, originalType, optType.Inner))
		elseNarrowing.Narrow(key, optionalEntry(key, originalType, types.TypeNone))
	}

	return thenNarrowing, elseNarrowing
}

// noneComparisonExpr checks if a binary expression is comparing an expression to "none".
// Returns the non-none expression and true if it's a none comparison.
func noneComparisonExpr(binExpr *ast.BinaryExpr) (ast.Expression, bool) {
	if binExpr == nil {
		return nil, false
	}

	// expr == none / expr != none
	if isNoneLiteral(binExpr.Y) && !isNoneLiteral(binExpr.X) {
		return binExpr.X, true
	}

	// none == expr / none != expr
	if isNoneLiteral(binExpr.X) && !isNoneLiteral(binExpr.Y) {
		return binExpr.Y, true
	}

	return nil, false
}

// isNoneLiteral checks if an expression is the "none" literal.
func isNoneLiteral(expr ast.Expression) bool {
	if ident, ok := expr.(*ast.IdentifierExpr); ok {
		return ident.Name == "none"
	}
	return false
}

func optionalTypeFromExpr(mod *context_v2.Module, expr ast.Expression, parent *NarrowingContext) (types.SemType, *types.OptionalType) {
	if expr == nil || mod == nil {
		return nil, nil
	}

	if key, ok := ExprKey(expr); ok && parent != nil {
		if entry, ok := parent.GetEntry(key); ok && entry != nil && entry.OriginalType != nil {
			if opt, ok := types.UnwrapType(entry.OriginalType).(*types.OptionalType); ok {
				return entry.OriginalType, opt
			}
		}
	}

	if exprType, ok := mod.ExprType(expr); ok && exprType != nil && !exprType.Equals(types.TypeUnknown) {
		if opt, ok := types.UnwrapType(exprType).(*types.OptionalType); ok {
			return exprType, opt
		}
	}

	if ident, ok := expr.(*ast.IdentifierExpr); ok {
		if sym, ok := mod.CurrentScope.Lookup(ident.Name); ok && sym != nil {
			if sym.OriginalType != nil {
				if opt, ok := types.UnwrapType(sym.OriginalType).(*types.OptionalType); ok {
					return sym.OriginalType, opt
				}
			}
			if opt, ok := types.UnwrapType(sym.Type).(*types.OptionalType); ok {
				return sym.Type, opt
			}
		}
	}

	return nil, nil
}

func optionalEntry(key string, original types.SemType, narrowed types.SemType) *NarrowingEntry {
	if key == "" || original == nil || narrowed == nil {
		return nil
	}
	return &NarrowingEntry{
		Kind:         NarrowingOptional,
		VarName:      key,
		OriginalType: original,
		NarrowedType: narrowed,
		VariantIndex: -1,
	}
}
