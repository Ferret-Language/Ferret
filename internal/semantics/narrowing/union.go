package narrowing

import (
	"compiler/internal/context_v2"
	"compiler/internal/frontend/ast"
	"compiler/internal/types"
)

// analyzeIsCondition handles 'x is Type' narrowing for unions and interfaces.
func analyzeIsCondition(mod *context_v2.Module, binExpr *ast.BinaryExpr, parent *NarrowingContext) (*NarrowingContext, *NarrowingContext) {
	thenNarrowing := NewNarrowingContext(parent)
	elseNarrowing := NewNarrowingContext(parent)

	ident, ok := binExpr.X.(*ast.IdentifierExpr)
	if !ok {
		return thenNarrowing, elseNarrowing
	}
	varName := ident.Name

	sym, ok := mod.CurrentScope.Lookup(varName)
	if !ok || sym == nil || sym.Type == nil {
		return thenNarrowing, elseNarrowing
	}

	originalType := sym.Type
	unwrapped := types.UnwrapType(originalType)

	// Try union narrowing first
	if unionType, ok := unwrapped.(*types.UnionType); ok {
		return analyzeUnionIsCondition(varName, originalType, unionType, binExpr, thenNarrowing, elseNarrowing, mod)
	}

	// Try interface narrowing
	if interfaceType, ok := unwrapped.(*types.InterfaceType); ok && isEmptyInterface(interfaceType) {
		return analyzeInterfaceIsCondition(varName, originalType, binExpr, thenNarrowing, elseNarrowing, mod)
	}

	return thenNarrowing, elseNarrowing
}

// analyzeUnionIsCondition handles 'x is Type' for union types.
func analyzeUnionIsCondition(varName string, originalType types.SemType, unionType *types.UnionType, binExpr *ast.BinaryExpr, thenNarrowing, elseNarrowing *NarrowingContext, mod *context_v2.Module) (*NarrowingContext, *NarrowingContext) {
	targetType := getTargetTypeFromRHS(mod, binExpr.Y)
	if targetType == nil {
		return thenNarrowing, elseNarrowing
	}

	// Find which variant matches the target type
	for i, variant := range unionType.Variants {
		if targetType.Equals(variant) {
			// Then branch: narrow to the matched variant
			thenNarrowing.Narrow(varName, &NarrowingEntry{
				Kind:         NarrowingUnion,
				VarName:      varName,
				OriginalType: originalType,
				NarrowedType: variant,
				VariantIndex: i,
			})

			// Else branch: narrow to remaining variants
			otherVariants := excludeVariant(unionType.Variants, i)
			if len(otherVariants) == 1 {
				elseNarrowing.Narrow(varName, &NarrowingEntry{
					Kind:         NarrowingUnion,
					VarName:      varName,
					OriginalType: originalType,
					NarrowedType: otherVariants[0],
					VariantIndex: -1,
				})
			} else if len(otherVariants) > 1 {
				elseNarrowing.Narrow(varName, &NarrowingEntry{
					Kind:         NarrowingUnion,
					VarName:      varName,
					OriginalType: originalType,
					NarrowedType: types.NewUnion(otherVariants),
					VariantIndex: -1,
				})
			}
			break
		}
	}

	return thenNarrowing, elseNarrowing
}

// analyzeInterfaceIsCondition handles 'x is Type' for interface{} types.
func analyzeInterfaceIsCondition(varName string, originalType types.SemType, binExpr *ast.BinaryExpr, thenNarrowing, elseNarrowing *NarrowingContext, mod *context_v2.Module) (*NarrowingContext, *NarrowingContext) {
	targetType := getTargetTypeFromRHS(mod, binExpr.Y)
	if targetType == nil {
		return thenNarrowing, elseNarrowing
	}

	// Then branch: narrow to the target type
	thenNarrowing.Narrow(varName, &NarrowingEntry{
		Kind:         NarrowingInterface,
		VarName:      varName,
		OriginalType: originalType,
		NarrowedType: targetType,
		VariantIndex: -1,
	})
	// Else branch: can't narrow (still interface{})

	return thenNarrowing, elseNarrowing
}

// getTargetTypeFromRHS extracts the target type from the RHS of an 'is' expression.
func getTargetTypeFromRHS(mod *context_v2.Module, rhs ast.Expression) types.SemType {
	// Handle TypeExpr (parser format: x is []interface{})
	if typeExpr, ok := rhs.(*ast.TypeExpr); ok {
		return getTypeFromTypeNode(mod, typeExpr.Type)
	}

	// Handle IdentifierExpr (old format: x is str)
	if rhsIdent, ok := rhs.(*ast.IdentifierExpr); ok {
		return getPrimitiveTypeByName(rhsIdent.Name)
	}

	return nil
}

// excludeVariant returns the variants excluding the one at index i.
func excludeVariant(variants []types.SemType, i int) []types.SemType {
	result := make([]types.SemType, 0, len(variants)-1)
	for j, v := range variants {
		if j != i {
			result = append(result, v)
		}
	}
	return result
}

// isEmptyInterface checks if an interface type has no methods (is interface{}).
func isEmptyInterface(interfaceType *types.InterfaceType) bool {
	return interfaceType != nil && len(interfaceType.Methods) == 0
}
