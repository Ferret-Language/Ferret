package typechecker

import (
	"compiler/internal/frontend/ast"
	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/typeinfo"
)

func (c *checker) narrowedScopeForCondition(scope *refineScope, cond ast.Expr, truth bool) *refineScope {
	if scope == nil || cond == nil {
		return scope
	}
	switch e := cond.(type) {
	case *ast.PrefixExpr:
		if e.Op == "!" {
			return c.narrowedScopeForCondition(scope, e.Right, !truth)
		}
	case *ast.IsExpr:
		return c.narrowedScopeForIs(scope, e, truth)
	}
	return scope
}

func (c *checker) narrowedScopeForIs(scope *refineScope, expr *ast.IsExpr, truth bool) *refineScope {
	if expr == nil {
		return scope
	}
	ident, ok := expr.Left.(*ast.Ident)
	if !ok || ident == nil || len(ident.Path) != 1 {
		return scope
	}
	res := c.lookupResolution(ident)
	if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
		return scope
	}
	baseType := c.typeOfSymbol(res.Symbol)
	if baseType == nil || typeinfo.IsInvalid(baseType) || typeinfo.IsUnknown(baseType) {
		return scope
	}
	target, ok := c.info.Nodes[expr.Type]
	if !ok || target == nil {
		target = c.typeFromSyntax(c.mod, expr.Type)
	}
	unionType, ok := c.underlying(baseType).(*typeinfo.UnionType)
	if ok && unionType != nil {
		if truth {
			if !c.unionTypeMayMatch(unionType, target) {
				return scope
			}
			narrowed := newRefineScope(scope)
			narrowed.Set(res.Symbol, target)
			return narrowed
		}
		remaining := c.unionMembersWithoutExactMatch(unionType, target)
		if len(remaining) == 0 || len(remaining) == len(unionType.Members) {
			return scope
		}
		narrowed := newRefineScope(scope)
		narrowed.Set(res.Symbol, c.narrowedTypeFromMembers(remaining))
		return narrowed
	}
	if opt, ok := c.underlying(baseType).(*typeinfo.OptionalType); ok && opt != nil {
		if !typeinfo.Equal(opt.Inner, target) {
			return scope
		}
		narrowed := newRefineScope(scope)
		if truth {
			narrowed.Set(res.Symbol, target)
			return narrowed
		}
		narrowed.Set(res.Symbol, baseType)
		return narrowed
	}
	return scope
}

func (c *checker) unionMembersWithoutExactMatch(unionType *typeinfo.UnionType, target typeinfo.Type) []typeinfo.Type {
	if unionType == nil || target == nil {
		return nil
	}
	remaining := make([]typeinfo.Type, 0, len(unionType.Members))
	for _, member := range unionType.Members {
		if typeinfo.Equal(member, target) {
			continue
		}
		remaining = append(remaining, member)
	}
	return remaining
}

func (c *checker) narrowedTypeFromMembers(members []typeinfo.Type) typeinfo.Type {
	switch len(members) {
	case 0:
		return typeinfo.UnknownType{}
	case 1:
		return members[0]
	default:
		out := &typeinfo.UnionType{Members: make([]typeinfo.Type, len(members))}
		copy(out.Members, members)
		return out
	}
}

func (c *checker) narrowedMatchTypeArmScope(scope *refineScope, value ast.Expr, target typeinfo.Type) *refineScope {
	if scope == nil || value == nil || target == nil {
		return scope
	}
	ident, ok := value.(*ast.Ident)
	if !ok || ident == nil || len(ident.Path) != 1 {
		return scope
	}
	res := c.lookupResolution(ident)
	if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
		return scope
	}
	baseType := c.typeOfSymbol(res.Symbol)
	if baseType == nil || typeinfo.IsInvalid(baseType) || typeinfo.IsUnknown(baseType) {
		return scope
	}
	if unionType, ok := c.underlying(baseType).(*typeinfo.UnionType); ok && unionType != nil && c.unionTypeMayMatch(unionType, target) {
		narrowed := newRefineScope(scope)
		narrowed.Set(res.Symbol, target)
		return narrowed
	}
	if opt, ok := c.underlying(baseType).(*typeinfo.OptionalType); ok && opt != nil && typeinfo.Equal(opt.Inner, target) {
		narrowed := newRefineScope(scope)
		narrowed.Set(res.Symbol, target)
		return narrowed
	}
	if typeinfo.Equal(baseType, target) {
		return scope
	}
	return scope
}

func (c *checker) unionTypeMayMatch(unionType *typeinfo.UnionType, target typeinfo.Type) bool {
	if unionType == nil || target == nil {
		return false
	}
	for _, member := range unionType.Members {
		if typeinfo.Equal(member, target) || typeinfo.Assignable(member, target) || typeinfo.Assignable(target, member) {
			return true
		}
	}
	return false
}
