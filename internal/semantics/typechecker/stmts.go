package typechecker

import (
	"fmt"

	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/semantics/binding"
	"compiler/internal/semantics/symbols"
	"compiler/internal/semantics/typeinfo"
)

func (c *checker) checkStmt(scope *refineScope, stmt ast.Stmt) {
	switch s := stmt.(type) {
	case nil:
		return
	case *ast.BlockStmt:
		for _, child := range s.Stmts {
			c.checkStmt(scope, child)
		}
	case *ast.LetStmt:
		var declared typeinfo.Type
		if s.Type != nil {
			declared = c.typeFromSyntax(c.mod, s.Type)
			c.info.BindNode(s.Type, declared)
		}
		var value typeinfo.Type
		if s.Value != nil {
			value = c.typeOfExpr(scope, s.Value, declared)
		}
		finalType := declared
		if finalType == nil {
			finalType = value
		}
		if finalType == nil {
			finalType = typeinfo.UnknownType{}
		}
		if declared != nil && s.Value != nil && !c.checkAssignable(s.Value.Loc(), declared, value) {
		}
		c.bindDeclSymbol(s.Name, finalType)
		// No base-type environment: locals/params are typed via Bindings+Types.
	case *ast.ConstStmt:
		var declared typeinfo.Type
		if s.Type != nil {
			declared = c.typeFromSyntax(c.mod, s.Type)
			c.info.BindNode(s.Type, declared)
		}
		value := c.typeOfExpr(scope, s.Value, declared)
		finalType := declared
		if finalType == nil {
			finalType = value
		}
		if finalType == nil {
			finalType = typeinfo.UnknownType{}
		}
		if declared != nil && s.Value != nil && !c.checkAssignable(s.Value.Loc(), declared, value) {
		}
		c.requireConstExpr(scope, s.Value, "constant initializer must be compile-time evaluable")
		c.bindDeclSymbol(s.Name, finalType)
		// No base-type environment: locals/params are typed via Bindings+Types.
	case *ast.ReturnStmt:
		c.checkReturn(scope, s)
	case *ast.ExprStmt:
		c.typeOfExpr(scope, s.Value, nil)
	case *ast.AssignStmt:
		leftType := c.typeOfAssignmentTargetExpr(scope, s.Left)
		rightType := c.typeOfExpr(scope, s.Right, leftType)
		if !c.checkAssignable(s.Right.Loc(), leftType, rightType) {
		}
		c.checkAssignmentTarget(scope, s.Left)
	case *ast.IfStmt:
		condType := c.typeOfExpr(scope, s.Cond, nil)
		c.requireBool(s.Cond.Loc(), condType)
		thenScope := c.narrowedScopeForCondition(scope, s.Cond, true)
		elseScope := c.narrowedScopeForCondition(scope, s.Cond, false)
		c.checkStmt(thenScope, s.Then)
		c.checkStmt(elseScope, s.Else)
	case *ast.MatchStmt:
		valueType := c.typeOfExpr(scope, s.Value, nil)
		hasWildcard := false
		for _, arm := range s.Arms {
			if arm == nil {
				continue
			}
			armScope := scope
			if arm.Wildcard {
				hasWildcard = true
			} else if arm.TypePattern != nil {
				target := c.typeFromSyntax(c.mod, arm.TypePattern)
				c.info.BindNode(arm.TypePattern, target)
				_ = c.typeOfIs(scope, &ast.IsExpr{Left: s.Value, Type: arm.TypePattern, Location: arm.Location})
				armScope = c.narrowedMatchTypeArmScope(scope, s.Value, target)
			} else {
				patternType := c.typeOfExpr(scope, arm.Pattern, valueType)
				if !typeinfo.Assignable(valueType, patternType) && !typeinfo.Assignable(patternType, valueType) {
					c.reportTypeMismatch(arm.Pattern.Loc(), valueType, patternType)
				}
			}
			c.checkStmt(armScope, arm.Body)
		}
		if !hasWildcard && len(s.Arms) > 0 {
			// fallback remains possible; CFG handles missing-return paths later
		}
	case *ast.WhileStmt:
		condType := c.typeOfExpr(scope, s.Cond, nil)
		c.requireBool(s.Cond.Loc(), condType)
		bodyScope := c.narrowedScopeForCondition(scope, s.Cond, true)
		c.checkStmt(bodyScope, s.Body)
	case *ast.ForStmt:
		iterType := c.typeOfExpr(scope, s.Iterable, nil)
		indexType, valueType := c.forBindingTypes(iterType)
		if indexType == nil {
			indexType = typeinfo.UnknownType{}
		}
		if valueType == nil {
			valueType = typeinfo.UnknownType{}
		}
		if s.Index != nil {
			c.bindDeclSymbol(s.Index, indexType)
			// No base-type environment: locals/params are typed via Bindings+Types.
		}
		if s.Value != nil {
			c.bindDeclSymbol(s.Value, valueType)
			// No base-type environment: locals/params are typed via Bindings+Types.
		}
		c.checkStmt(scope, s.Body)
	case *ast.LabelStmt:
		c.checkStmt(scope, s.Stmt)
	case *ast.DeferStmt:
		c.deferDepth++
		c.checkStmt(scope, s.Body)
		c.deferDepth--
	case *ast.ReleaseStmt:
		c.typeOfExpr(scope, s.Value, nil)
	case *ast.PanicStmt:
		if s.Value == nil {
			loc := s.Location
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("panic requires a payload").
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(&loc, "provide a panic payload"),
			)
			return
		}
		c.typeOfExpr(scope, s.Value, nil)
	case *ast.LockStmt:
		valueType := c.typeOfExpr(scope, s.Value, nil)
		if valueType == nil {
			valueType = typeinfo.UnknownType{}
		}
		if s.Name != nil {
			c.bindDeclSymbol(s.Name, valueType)
			// No base-type environment: locals/params are typed via Bindings+Types.
		}
		c.checkStmt(scope, s.Body)
	case *ast.UnsafeStmt:
		c.unsafeDepth++
		c.checkStmt(scope, s.Body)
		c.unsafeDepth--
	}
}

func (c *checker) checkReturn(scope *refineScope, stmt *ast.ReturnStmt) {
	expected := c.currentResult
	if expected == nil || typeinfo.IsBuiltinNamed(expected, "void") {
		if stmt.Value != nil {
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("void function cannot return a value").
					WithCode(diagnostics.ErrInvalidReturn).
					WithPrimaryLabel(&stmt.Location, "remove this return value"),
			)
			c.typeOfExpr(scope, stmt.Value, nil)
		}
		return
	}
	if stmt.Value == nil {
		c.ctx.Diagnostics.Add(
			diagnostics.NewError("non-void function must return a value").
				WithCode(diagnostics.ErrInvalidReturn).
				WithPrimaryLabel(&stmt.Location, "expected a return value here"),
		)
		return
	}
	got := c.typeOfExpr(scope, stmt.Value, expected)
	if !c.checkAssignable(stmt.Value.Loc(), expected, got) {
	}
}

func (c *checker) typeOfAssignmentTargetExpr(scope *refineScope, expr ast.Expr) typeinfo.Type {
	switch e := expr.(type) {
	case *ast.Ident:
		return c.typeOfIdent(scope, e, nil)
	default:
		return c.typeOfExpr(scope, expr, nil)
	}
}

func (c *checker) checkAssignmentTarget(scope *refineScope, left ast.Expr) {
	switch e := left.(type) {
	case *ast.Ident:
		res := c.lookupResolution(e)
		if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
			return
		}
		if res.Symbol.Kind == symbols.SymbolConst {
			loc := e.Location
			c.ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("cannot assign to constant %q", res.Symbol.Name)).
					WithCode(diagnostics.ErrConstantReassignment).
					WithPrimaryLabel(&loc, "constants are not assignable"),
			)
			return
		}
		if !c.symbolMutable(res.Symbol) {
			loc := e.Location
			c.ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("cannot assign to immutable symbol %q", res.Symbol.Name)).
					WithCode(diagnostics.ErrConstantReassignment).
					WithPrimaryLabel(&loc, "symbol must be mutable").
					WithCodeReplacement(&res.Symbol.Location, res.Symbol.Name, "mut "+res.Symbol.Name),
			)
		}
	}
}
