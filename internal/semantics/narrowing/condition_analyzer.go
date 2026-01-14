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
			// For OR: right is evaluated only if left is false.
			leftThen, leftElse := analyzeConditionRecursive(ctx, mod, cond.X, parent)
			rightThen, rightElse := analyzeConditionRecursive(ctx, mod, cond.Y, leftElse)
			thenNarrowing := mergeNarrowings(leftThen, rightThen)
			return thenNarrowing, rightElse
		case tokens.AND_TOKEN:
			// For AND: right is evaluated only if left is true.
			leftThen, leftElse := analyzeConditionRecursive(ctx, mod, cond.X, parent)
			rightThen, rightElse := analyzeConditionRecursive(ctx, mod, cond.Y, leftThen)
			elseNarrowing := mergeNarrowings(leftElse, rightElse)
			return rightThen, elseNarrowing
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
	entriesA := make(map[string]*NarrowingEntry)
	entriesB := make(map[string]*NarrowingEntry)
	collectEntries(a, entriesA)
	collectEntries(b, entriesB)

	for key, entryA := range entriesA {
		if entryB, ok := entriesB[key]; ok {
			if mergedEntry := mergeEntry(entryA, entryB); mergedEntry != nil {
				merged.Narrow(key, mergedEntry)
			}
			continue
		}
		if entryA != nil {
			merged.Narrow(key, entryA)
		}
	}
	for key, entryB := range entriesB {
		if _, ok := entriesA[key]; ok {
			continue
		}
		if entryB != nil {
			merged.Narrow(key, entryB)
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
	entriesA := make(map[string]*NarrowingEntry)
	entriesB := make(map[string]*NarrowingEntry)
	collectEntries(a, entriesA)
	collectEntries(b, entriesB)

	for key, entryA := range entriesA {
		entryB, ok := entriesB[key]
		if !ok {
			continue
		}
		if mergedEntry := intersectEntry(entryA, entryB); mergedEntry != nil {
			intersected.Narrow(key, mergedEntry)
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

func collectEntries(nc *NarrowingContext, out map[string]*NarrowingEntry) {
	if nc == nil || out == nil {
		return
	}
	if nc.Parent != nil {
		collectEntries(nc.Parent, out)
	}
	for key, entry := range nc.Entries {
		out[key] = entry
	}
}

func mergeEntry(a, b *NarrowingEntry) *NarrowingEntry {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.NarrowedType == nil || b.NarrowedType == nil {
		return nil
	}
	if a.NarrowedType.Equals(b.NarrowedType) {
		return a
	}
	if a.Kind != b.Kind {
		return nil
	}
	if a.Kind == NarrowingUnion {
		mergedType := unionTypes(a.NarrowedType, b.NarrowedType)
		if mergedType == nil {
			return nil
		}
		return &NarrowingEntry{
			Kind:         NarrowingUnion,
			VarName:      a.VarName,
			OriginalType: preferOriginalType(a, b),
			NarrowedType: mergedType,
			VariantIndex: -1,
		}
	}
	return nil
}

func intersectEntry(a, b *NarrowingEntry) *NarrowingEntry {
	if a == nil || b == nil {
		return nil
	}
	if a.NarrowedType == nil || b.NarrowedType == nil {
		return nil
	}
	if a.NarrowedType.Equals(b.NarrowedType) {
		return a
	}
	if a.Kind != b.Kind {
		return nil
	}
	if a.Kind == NarrowingUnion {
		intersected := intersectTypes(a.NarrowedType, b.NarrowedType)
		if intersected == nil {
			return nil
		}
		return &NarrowingEntry{
			Kind:         NarrowingUnion,
			VarName:      a.VarName,
			OriginalType: preferOriginalType(a, b),
			NarrowedType: intersected,
			VariantIndex: -1,
		}
	}
	return nil
}

func preferOriginalType(a, b *NarrowingEntry) types.SemType {
	if a != nil && a.OriginalType != nil {
		return a.OriginalType
	}
	if b != nil {
		return b.OriginalType
	}
	return nil
}
