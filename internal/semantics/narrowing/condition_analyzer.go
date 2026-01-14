package narrowing

import (
	"compiler/internal/context_v2"
	"compiler/internal/frontend/ast"
	"compiler/internal/tokens"
	"compiler/internal/types"
)

// ConditionAnalyzer provides unified type narrowing analysis for optionals, unions, and interfaces.
type ConditionAnalyzer struct{}

// NewConditionAnalyzer creates a new narrowing analyzer.
func NewConditionAnalyzer() *ConditionAnalyzer {
	return &ConditionAnalyzer{}
}

// AnalyzeCondition recursively analyzes a condition to determine type narrowings.
// Returns separate narrowing contexts for the then-branch and else-branch.
func (a *ConditionAnalyzer) AnalyzeCondition(ctx *context_v2.CompilerContext, mod *context_v2.Module, condition ast.Expression, parent *NarrowingContext) (*NarrowingContext, *NarrowingContext) {
	return analyzeConditionRecursive(ctx, mod, condition, parent)
}

// analyzeConditionRecursive recursively analyzes conditions for narrowing.
func analyzeConditionRecursive(ctx *context_v2.CompilerContext, mod *context_v2.Module, condition ast.Expression, parent *NarrowingContext) (*NarrowingContext, *NarrowingContext) {
	switch cond := condition.(type) {
	case *ast.BinaryExpr:
		switch cond.Op.Kind {
		case tokens.IS_TOKEN:
			// Union/interface narrowing: x is Type
			return analyzeIsCondition(mod, cond, parent)
		case tokens.DOUBLE_EQUAL_TOKEN:
			// Optional narrowing: x == none
			return analyzeEqualityCondition(mod, cond, parent, true)
		case tokens.NOT_EQUAL_TOKEN:
			// Optional narrowing: x != none
			return analyzeEqualityCondition(mod, cond, parent, false)
		case tokens.OR_TOKEN:
			// For OR: then = merge(left, right), else = intersect(left, right)
			leftThen, leftElse := analyzeConditionRecursive(ctx, mod, cond.X, parent)
			rightThen, rightElse := analyzeConditionRecursive(ctx, mod, cond.Y, parent)
			thenNarrowing := mergeNarrowings(leftThen, rightThen)
			elseNarrowing := intersectNarrowings(leftElse, rightElse)
			return thenNarrowing, elseNarrowing
		case tokens.AND_TOKEN:
			// For AND: then = intersect(left, right), else = merge(left, right)
			leftThen, leftElse := analyzeConditionRecursive(ctx, mod, cond.X, parent)
			rightThen, rightElse := analyzeConditionRecursive(ctx, mod, cond.Y, parent)
			thenNarrowing := intersectNarrowings(leftThen, rightThen)
			elseNarrowing := mergeNarrowings(leftElse, rightElse)
			return thenNarrowing, elseNarrowing
		default:
			return parent, parent
		}
	case *ast.UnaryExpr:
		if cond.Op.Kind == tokens.NOT_TOKEN {
			// Negation swaps then/else narrowings
			thenNarrowing, elseNarrowing := analyzeConditionRecursive(ctx, mod, cond.X, parent)
			return elseNarrowing, thenNarrowing
		}
		return parent, parent
	default:
		return parent, parent
	}
}

// mergeNarrowings merges two narrowing contexts (for OR conditions).
// Result contains all narrowings from both contexts.
func mergeNarrowings(a, b *NarrowingContext) *NarrowingContext {
	// Handle nil inputs
	if a == nil && b == nil {
		return NewNarrowingContext(nil)
	}
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}

	merged := NewNarrowingContext(nil)
	for varName := range a.NarrowedTypes {
		if aType, ok := a.GetNarrowedType(varName); ok {
			if bType, ok := b.GetNarrowedType(varName); ok {
				merged.Narrow(varName, unionTypes(aType, bType))
			} else {
				merged.Narrow(varName, aType)
			}
		}
	}
	for varName := range b.NarrowedTypes {
		if _, ok := a.GetNarrowedType(varName); !ok {
			if bType, ok := b.GetNarrowedType(varName); ok {
				merged.Narrow(varName, bType)
			}
		}
	}
	return merged
}

// intersectNarrowings intersects two narrowing contexts (for AND conditions).
// Result only contains narrowings present in both contexts.
func intersectNarrowings(a, b *NarrowingContext) *NarrowingContext {
	// Handle nil inputs
	if a == nil && b == nil {
		return NewNarrowingContext(nil)
	}
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}

	intersected := NewNarrowingContext(nil)
	for varName := range a.NarrowedTypes {
		if aType, ok := a.GetNarrowedType(varName); ok {
			if bType, ok := b.GetNarrowedType(varName); ok {
				if intersectedType := intersectTypes(aType, bType); intersectedType != nil {
					intersected.Narrow(varName, intersectedType)
				}
			}
		}
	}
	return intersected
}

// unionTypes returns the union of two types.
func unionTypes(a, b types.SemType) types.SemType {
	if a.Equals(b) {
		return a
	}
	variants := []types.SemType{a}
	if !containsType(variants, b) {
		variants = append(variants, b)
	}
	if len(variants) == 1 {
		return variants[0]
	}
	return types.NewUnion(variants)
}

// intersectTypes returns the intersection of two types.
func intersectTypes(a, b types.SemType) types.SemType {
	if a.Equals(b) {
		return a
	}
	// For unions, intersection would be complex; for now, return nil if no overlap
	return nil
}

// containsType checks if a slice contains a type.
func containsType(typeList []types.SemType, t types.SemType) bool {
	for _, typ := range typeList {
		if typ.Equals(t) {
			return true
		}
	}
	return false
}
