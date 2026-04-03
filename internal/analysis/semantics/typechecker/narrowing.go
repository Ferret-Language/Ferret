package typechecker

import (
	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/frontend/ast"
	"fmt"
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
	case *ast.BinaryExpr:
		if e.Op != "==" && e.Op != "!=" {
			return scope
		}
		var narrowedExpr ast.Expr
		if _, ok := e.Right.(*ast.NoneLit); ok {
			narrowedExpr = e.Left
		} else if _, ok := e.Left.(*ast.NoneLit); ok {
			narrowedExpr = e.Right
		}
		if narrowedExpr == nil {
			return scope
		}
		baseType, ok := c.refinableExprType(scope, narrowedExpr)
		if !ok {
			return scope
		}
		opt, ok := c.underlying(baseType).(*typeinfo.OptionalType)
		if !ok || opt == nil {
			return scope
		}
		narrowsToInner := truth
		if e.Op == "==" {
			narrowsToInner = !truth
		}
		if !narrowsToInner {
			return scope
		}
		return c.refineExprType(scope, narrowedExpr, opt.Inner)
	case *ast.IsExpr:
		return c.narrowedScopeForIs(scope, e, truth)
	}
	return scope
}

func (c *checker) narrowedScopeForIs(scope *refineScope, expr *ast.IsExpr, truth bool) *refineScope {
	if expr == nil {
		return scope
	}
	baseType, ok := c.refinableExprType(scope, expr.Left)
	if !ok {
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
			return c.refineExprType(scope, expr.Left, target)
		}
		remaining := c.unionMembersWithoutExactMatch(unionType, target)
		if len(remaining) == 0 || len(remaining) == len(unionType.Members) {
			return scope
		}
		return c.refineExprType(scope, expr.Left, c.narrowedTypeFromMembers(remaining))
	}
	if opt, ok := c.underlying(baseType).(*typeinfo.OptionalType); ok && opt != nil {
		if !typeinfo.Equal(opt.Inner, target) {
			return scope
		}
		if truth {
			return c.refineExprType(scope, expr.Left, target)
		}
		return c.refineExprType(scope, expr.Left, baseType)
	}
	if _, ok := c.underlying(baseType).(*typeinfo.InterfaceType); ok {
		if !truth || typeinfo.Equal(baseType, target) {
			return scope
		}
		return c.refineExprType(scope, expr.Left, target)
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
	baseType, ok := c.refinableExprType(scope, value)
	if !ok {
		return scope
	}
	if unionType, ok := c.underlying(baseType).(*typeinfo.UnionType); ok && unionType != nil && c.unionTypeMayMatch(unionType, target) {
		return c.refineExprType(scope, value, target)
	}
	if opt, ok := c.underlying(baseType).(*typeinfo.OptionalType); ok && opt != nil && typeinfo.Equal(opt.Inner, target) {
		return c.refineExprType(scope, value, target)
	}
	if _, ok := c.underlying(baseType).(*typeinfo.InterfaceType); ok {
		return c.refineExprType(scope, value, target)
	}
	if typeinfo.Equal(baseType, target) {
		return scope
	}
	return scope
}

func (c *checker) refinableExprType(scope *refineScope, expr ast.Expr) (typeinfo.Type, bool) {
	if expr == nil {
		return nil, false
	}
	baseType, ok := c.info.Nodes[expr]
	if !ok || baseType == nil {
		baseType = c.typeOfExpr(scope, expr, nil)
	}
	if baseType == nil || typeinfo.IsInvalid(baseType) || typeinfo.IsUnknown(baseType) {
		return nil, false
	}
	return baseType, true
}

func (c *checker) refineExprType(scope *refineScope, expr ast.Expr, typ typeinfo.Type) *refineScope {
	if scope == nil || expr == nil || typ == nil {
		return scope
	}
	narrowed := newRefineScope(scope)
	changed := false
	if ident, ok := expr.(*ast.Ident); ok && ident != nil && len(ident.Path) == 1 {
		res := c.lookupResolution(ident)
		if res != nil && res.Kind == binding.ResolutionSymbol && res.Symbol != nil {
			narrowed.Set(res.Symbol, typ)
			changed = true
		}
	}
	if key, ok := c.refinementKey(expr); ok {
		narrowed.SetPath(key, typ)
		changed = true
	}
	if !changed {
		return scope
	}
	return narrowed
}

func (c *checker) lookupRefinedType(scope *refineScope, expr ast.Expr) (typeinfo.Type, bool) {
	if scope == nil || expr == nil {
		return nil, false
	}
	if ident, ok := expr.(*ast.Ident); ok && ident != nil && len(ident.Path) == 1 {
		res := c.lookupResolution(ident)
		if res != nil && res.Kind == binding.ResolutionSymbol && res.Symbol != nil {
			if typ, ok := scope.Lookup(res.Symbol); ok && typ != nil {
				return typ, true
			}
		}
	}
	if key, ok := c.refinementKey(expr); ok {
		return scope.LookupPath(key)
	}
	return nil, false
}

func (c *checker) refinementKey(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		if e == nil || len(e.Path) != 1 {
			return "", false
		}
		res := c.lookupResolution(e)
		if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
			return "", false
		}
		return fmt.Sprintf("sym:%d", res.Symbol.ID), true
	case *ast.SelectorExpr:
		left, ok := c.refinementKey(e.Left)
		if !ok || e.Name == nil || e.Name.Text() == "" {
			return "", false
		}
		return left + "." + e.Name.Text(), true
	case *ast.IndexExpr:
		left, ok := c.refinementKey(e.Left)
		if !ok {
			return "", false
		}
		index := ast.ExprString(e.Index)
		if index == "" || index == "_" {
			return "", false
		}
		return left + "[" + index + "]", true
	default:
		return "", false
	}
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
