package typechecker

import (
	"fmt"
	"slices"

	"compiler/internal/context"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/phase"
	"compiler/internal/semantics/binding"
	"compiler/internal/semantics/symbols"
	"compiler/internal/semantics/typeinfo"
	"compiler/internal/source"
	"compiler/internal/tokens"
	"compiler/internal/utils/numeric"
)

type valueInfo struct {
	typ      typeinfo.Type
	mutable  bool
	constant bool
}

type valueScope struct {
	parent *valueScope
	values map[string]*valueInfo
}

func newValueScope(parent *valueScope) *valueScope {
	return &valueScope{parent: parent, values: make(map[string]*valueInfo)}
}

func (s *valueScope) Declare(name string, info valueInfo) *valueInfo {
	if s == nil || name == "" {
		return nil
	}
	slot := &valueInfo{
		typ:      info.typ,
		mutable:  info.mutable,
		constant: info.constant,
	}
	s.values[name] = slot
	return slot
}

func (s *valueScope) Lookup(name string) (*valueInfo, bool) {
	for scope := s; scope != nil; scope = scope.parent {
		if info, ok := scope.values[name]; ok {
			return info, true
		}
	}
	return nil, false
}

type checker struct {
	ctx           *context.CompilerContext
	mod           *context.Module
	info          *typeinfo.ModuleInfo
	currentResult typeinfo.Type
	unsafeDepth   int
	deferDepth    int
}

func CheckModule(ctx *context.CompilerContext, mod *context.Module) {
	if ctx == nil || mod == nil || mod.AST == nil || mod.Bindings == nil {
		return
	}
	c := &checker{
		ctx:  ctx,
		mod:  mod,
		info: typeinfo.NewModuleInfo(),
	}

	for _, sym := range mod.ModuleScope.Symbols() {
		c.info.BindSymbol(sym, c.typeOfSymbol(sym))
	}
	for _, members := range mod.TypeMembers {
		for _, sym := range members {
			c.info.BindSymbol(sym, c.typeOfSymbol(sym))
		}
	}

	for _, decl := range mod.AST.Decls {
		c.checkDecl(decl)
	}

	mod.Types = c.info
	mod.Phase = phase.PhaseTypeChecked
}

func (c *checker) checkDecl(decl ast.Decl) {
	switch d := decl.(type) {
	case *ast.LetDecl:
		var declared typeinfo.Type
		if d.Type != nil {
			declared = c.typeFromSyntax(c.mod, d.Type)
			c.info.BindNode(d.Type, declared)
		}
		var value typeinfo.Type
		if d.Value != nil {
			value = c.typeOfExpr(nil, d.Value, declared)
		}
		finalType := declared
		if finalType == nil {
			finalType = value
		}
		if finalType == nil {
			finalType = typeinfo.UnknownType{}
		}
		if declared != nil && d.Value != nil && !typeinfo.Assignable(declared, value) {
			c.reportTypeMismatch(d.Value.Loc(), declared, value)
		}
		if sym, ok := c.mod.ModuleScope.LookupLocal(d.Name.Text()); ok {
			c.info.BindSymbol(sym, finalType)
		}
	case *ast.ConstDecl:
		var declared typeinfo.Type
		if d.Type != nil {
			declared = c.typeFromSyntax(c.mod, d.Type)
			c.info.BindNode(d.Type, declared)
		}
		var value typeinfo.Type
		if d.Value != nil {
			value = c.typeOfExpr(nil, d.Value, declared)
		}
		finalType := declared
		if finalType == nil {
			finalType = value
		}
		if finalType == nil {
			finalType = typeinfo.UnknownType{}
		}
		if declared != nil && d.Value != nil && !typeinfo.Assignable(declared, value) {
			c.reportTypeMismatch(d.Value.Loc(), declared, value)
		}
		c.requireConstExpr(nil, d.Value, "constant initializer must be compile-time evaluable")
		if sym, ok := c.mod.ModuleScope.LookupLocal(d.Name.Text()); ok {
			c.info.BindSymbol(sym, finalType)
		}
	case *ast.TypeDecl:
		c.checkTypeDecl(d)
	case *ast.FuncDecl:
		c.checkFuncDecl(d)
	}
}

func (c *checker) checkTypeDecl(d *ast.TypeDecl) {
	if d == nil {
		return
	}
	declType := c.typeFromSyntax(c.mod, d.Type)
	if d.Type != nil {
		c.info.BindNode(d.Type, declType)
	}
	switch t := d.Type.(type) {
	case *ast.StructType:
		for _, field := range t.Fields {
			if field == nil {
				continue
			}
			fieldType := c.typeFromSyntax(c.mod, field.Type)
			if field.Type != nil {
				c.info.BindNode(field.Type, fieldType)
			}
			if field.Default != nil {
				valueType := c.typeOfExpr(nil, field.Default, fieldType)
				if !typeinfo.Assignable(fieldType, valueType) {
					c.reportTypeMismatch(field.Default.Loc(), fieldType, valueType)
				}
			}
		}
		for _, field := range t.StaticFields {
			if field == nil {
				continue
			}
			fieldType := c.typeFromSyntax(c.mod, field.Type)
			if field.Type != nil {
				c.info.BindNode(field.Type, fieldType)
			}
			if field.Default != nil {
				valueType := c.typeOfExpr(nil, field.Default, fieldType)
				if !typeinfo.Assignable(fieldType, valueType) {
					c.reportTypeMismatch(field.Default.Loc(), fieldType, valueType)
				}
				c.requireConstExpr(nil, field.Default, "static field initializer must be compile-time evaluable")
			}
		}
	case *ast.InterfaceType:
		for _, method := range t.Methods {
			if method == nil {
				continue
			}
			for _, param := range method.Params {
				paramType := c.typeFromSyntax(c.mod, param.Type)
				if param.Type != nil {
					c.info.BindNode(param.Type, paramType)
				}
			}
			if method.Result != nil {
				c.info.BindNode(method.Result, c.typeFromSyntax(c.mod, method.Result))
			}
		}
	case *ast.UnionType:
		for _, member := range t.Members {
			if member != nil {
				c.info.BindNode(member, c.typeFromSyntax(c.mod, member))
			}
		}
	}
}

func (c *checker) checkFuncDecl(d *ast.FuncDecl) {
	if d == nil {
		return
	}
	funcScope := newValueScope(nil)
	if d.Receiver != nil {
		recvType := c.typeFromSyntax(c.mod, d.Receiver.Type)
		if d.Receiver.Type != nil {
			c.info.BindNode(d.Receiver.Type, recvType)
		}
		funcScope.Declare(d.Receiver.Name.Text(), valueInfo{typ: recvType, mutable: true})
		c.info.BindNode(d.Receiver, recvType)
	}
	for _, param := range d.Params {
		paramType := c.typeFromSyntax(c.mod, param.Type)
		if param.Type != nil {
			c.info.BindNode(param.Type, paramType)
		}
		funcScope.Declare(param.Name.Text(), valueInfo{typ: paramType, mutable: false})
	}
	prevResult := c.currentResult
	c.currentResult = c.funcResultType(c.mod, d)
	if d.Result != nil {
		c.info.BindNode(d.Result, c.currentResult)
	}
	if d.Body != nil {
		c.checkStmt(funcScope, d.Body)
	}
	c.currentResult = prevResult
}

func (c *checker) checkStmt(scope *valueScope, stmt ast.Stmt) {
	switch s := stmt.(type) {
	case nil:
		return
	case *ast.BlockStmt:
		block := newValueScope(scope)
		for _, child := range s.Stmts {
			c.checkStmt(block, child)
		}
	case *ast.LetStmt:
		var declared typeinfo.Type
		if s.Type != nil {
			declared = c.typeFromSyntax(c.mod, s.Type)
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
		if declared != nil && s.Value != nil && !typeinfo.Assignable(declared, value) {
			c.reportTypeMismatch(s.Value.Loc(), declared, value)
		}
		scope.Declare(s.Name.Text(), valueInfo{typ: finalType, mutable: s.IsMut})
	case *ast.ConstStmt:
		var declared typeinfo.Type
		if s.Type != nil {
			declared = c.typeFromSyntax(c.mod, s.Type)
		}
		value := c.typeOfExpr(scope, s.Value, declared)
		finalType := declared
		if finalType == nil {
			finalType = value
		}
		if finalType == nil {
			finalType = typeinfo.UnknownType{}
		}
		if declared != nil && s.Value != nil && !typeinfo.Assignable(declared, value) {
			c.reportTypeMismatch(s.Value.Loc(), declared, value)
		}
		c.requireConstExpr(scope, s.Value, "constant initializer must be compile-time evaluable")
		scope.Declare(s.Name.Text(), valueInfo{typ: finalType, constant: true})
	case *ast.ReturnStmt:
		c.checkReturn(scope, s)
	case *ast.ExprStmt:
		c.typeOfExpr(scope, s.Value, nil)
	case *ast.AssignStmt:
		leftType := c.typeOfAssignmentTargetExpr(scope, s.Left)
		rightType := c.typeOfExpr(scope, s.Right, leftType)
		if !typeinfo.Assignable(leftType, rightType) {
			c.reportTypeMismatch(s.Right.Loc(), leftType, rightType)
		}
		c.checkAssignmentTarget(scope, s.Left)
	case *ast.IfStmt:
		condType := c.typeOfExpr(scope, s.Cond, nil)
		c.requireBool(s.Cond.Loc(), condType)
		c.checkStmt(scope, s.Then)
		c.checkStmt(scope, s.Else)
	case *ast.MatchStmt:
		valueType := c.typeOfExpr(scope, s.Value, nil)
		hasWildcard := false
		for _, arm := range s.Arms {
			if arm == nil {
				continue
			}
			if arm.Wildcard {
				hasWildcard = true
			} else {
				patternType := c.typeOfExpr(scope, arm.Pattern, valueType)
				if !typeinfo.Assignable(valueType, patternType) && !typeinfo.Assignable(patternType, valueType) {
					c.reportTypeMismatch(arm.Pattern.Loc(), valueType, patternType)
				}
			}
			c.checkStmt(scope, arm.Body)
		}
		if !hasWildcard && len(s.Arms) > 0 {
			// fallback remains possible; CFG handles missing-return paths later
		}
	case *ast.WhileStmt:
		condType := c.typeOfExpr(scope, s.Cond, nil)
		c.requireBool(s.Cond.Loc(), condType)
		c.checkStmt(newValueScope(scope), s.Body)
	case *ast.ForStmt:
		loopScope := newValueScope(scope)
		iterType := c.typeOfExpr(loopScope, s.Iterable, nil)
		indexType, valueType := c.forBindingTypes(iterType)
		if s.Index != nil {
			loopScope.Declare(s.Index.Text(), valueInfo{typ: indexType, mutable: false})
		}
		loopScope.Declare(s.Value.Text(), valueInfo{typ: valueType, mutable: false})
		c.checkStmt(loopScope, s.Body)
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
		lockScope := newValueScope(scope)
		lockScope.Declare(s.Name.Text(), valueInfo{typ: valueType, mutable: true})
		c.checkStmt(lockScope, s.Body)
	case *ast.UnsafeStmt:
		c.unsafeDepth++
		c.checkStmt(scope, s.Body)
		c.unsafeDepth--
	}
}

func (c *checker) checkReturn(scope *valueScope, stmt *ast.ReturnStmt) {
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
	if !typeinfo.Assignable(expected, got) {
		c.reportTypeMismatch(stmt.Value.Loc(), expected, got)
	}
}

func (c *checker) typeOfAssignmentTargetExpr(scope *valueScope, expr ast.Expr) typeinfo.Type {
	switch e := expr.(type) {
	case *ast.Ident:
		if identType, ok := c.typeOfLocalIdent(scope, e); ok {
			c.info.BindNode(e, identType)
			return identType
		}
		return c.typeOfIdent(scope, e, nil)
	default:
		return c.typeOfExpr(scope, expr, nil)
	}
}

func (c *checker) checkAssignmentTarget(scope *valueScope, left ast.Expr) {
	switch e := left.(type) {
	case *ast.Ident:
		if len(e.Path) == 1 {
			if info, ok := scope.Lookup(e.Path[0]); ok {
				if info.constant || !info.mutable {
					c.ctx.Diagnostics.Add(
						diagnostics.NewError(fmt.Sprintf("cannot assign to %q", e.Path[0])).
							WithCode(diagnostics.ErrConstantReassignment).
							WithPrimaryLabel(&e.Location, "this value is not assignable"),
					)
				}
				return
			}
		}
		res := c.lookupResolution(e)
		if res == nil || res.Symbol == nil {
			return
		}
		if res.Symbol.Kind == symbols.SymbolConst {
			loc := e.Location
			c.ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("cannot assign to constant %q", res.Symbol.Name)).
					WithCode(diagnostics.ErrConstantReassignment).
					WithPrimaryLabel(&loc, "constants are not assignable"),
			)
		}
	}
}

func (c *checker) typeOfExpr(scope *valueScope, expr ast.Expr, expected typeinfo.Type) typeinfo.Type {
	switch e := expr.(type) {
	case nil:
		return nil
	case *ast.BadExpr:
		return typeinfo.InvalidType{}
	case *ast.NumberLit:
		typ := c.inferNumberLiteralType(e, expected)
		c.info.BindNode(e, typ)
		return typ
	case *ast.StringLit:
		// String literals are *i8 — a pointer to a null-terminated const byte array.
		typ := &typeinfo.PointerType{Inner: &typeinfo.BuiltinType{Name: "i8"}}
		c.info.BindNode(e, typ)
		return typ
	case *ast.NoneLit:
		if _, ok := expected.(*typeinfo.OptionalType); ok {
			c.info.BindNode(e, expected)
			return expected
		}

		loc := e.Location
		c.ctx.Diagnostics.Add(
			diagnostics.NewError("`none` requires an optional context").
				WithCode(diagnostics.ErrTypeMismatch).
				WithPrimaryLabel(&loc, "cannot infer an optional type here"),
		)
		return typeinfo.InvalidType{}
	case *ast.Ident:
		return c.typeOfIdent(scope, e, expected)
	case *ast.PrefixExpr:
		return c.typeOfPrefix(scope, e, expected)
	case *ast.BinaryExpr:
		return c.typeOfBinary(scope, e)
	case *ast.CatchExpr:
		return c.typeOfCatch(scope, e)
	case *ast.PostfixExpr:
		return c.typeOfPostfix(scope, e)
	case *ast.CallExpr:
		return c.typeOfCall(scope, e)
	case *ast.SelectorExpr:
		return c.typeOfSelector(scope, e)
	case *ast.CastExpr:
		return c.typeOfCast(scope, e)
	case *ast.CompositeLit:
		return c.typeOfComposite(scope, e, expected)
	case *ast.IndexExpr:
		return c.typeOfIndex(scope, e)
	default:
		return typeinfo.UnknownType{}
	}
}

func (c *checker) typeOfIdent(scope *valueScope, ident *ast.Ident, expected typeinfo.Type) typeinfo.Type {
	if ident == nil {
		return typeinfo.UnknownType{}
	}
	if len(ident.Path) == 1 {
		if localType, ok := c.typeOfLocalIdent(scope, ident); ok {
			c.info.BindNode(ident, localType)
			return localType
		}
	}
	res := c.lookupResolution(ident)
	if res == nil {
		return typeinfo.InvalidType{}
	}
	if res.Kind == binding.ResolutionSymbol && res.Symbol != nil {
		if res.Symbol.Kind == symbols.SymbolConst && res.Symbol.Node == nil && res.Symbol.Name == "undefined" {
			if expected != nil {
				c.info.BindNode(ident, expected)
				return expected
			}
			typ := typeinfo.UndefinedType{}
			c.info.BindNode(ident, typ)
			return typ
		}
		typ := c.typeOfSymbol(res.Symbol)
		c.info.BindNode(ident, typ)
		return typ
	}
	loc := ident.Location
	c.ctx.Diagnostics.Add(
		diagnostics.NewError("module name is not a value").
			WithCode(diagnostics.ErrInvalidType).
			WithPrimaryLabel(&loc, "expected a value here"),
	)
	return typeinfo.InvalidType{}
}

func (c *checker) typeOfLocalIdent(scope *valueScope, ident *ast.Ident) (typeinfo.Type, bool) {
	if ident == nil || len(ident.Path) != 1 || scope == nil {
		return nil, false
	}
	info, ok := scope.Lookup(ident.Path[0])
	if !ok || info == nil {
		return nil, false
	}
	return info.typ, true
}

func (c *checker) typeOfPrefix(scope *valueScope, expr *ast.PrefixExpr, expected typeinfo.Type) typeinfo.Type {
	right := c.typeOfExpr(scope, expr.Right, expected)
	switch expr.Op {
	case "copy":
		c.info.BindNode(expr, right)
		return right
	case "take":
		c.info.BindNode(expr, right)
		return right
	case "comptime":
		c.requireConstExpr(scope, expr.Right, "`comptime` expression must be compile-time evaluable")
		c.info.BindNode(expr, right)
		return right
	case "&":
		typ := &typeinfo.PointerType{Inner: right}
		c.info.BindNode(expr, typ)
		return typ
	case "&mut":
		typ := &typeinfo.PointerType{IsMut: true, Inner: right}
		c.info.BindNode(expr, typ)
		return typ
	case "*":
		if ptr, ok := right.(*typeinfo.PointerType); ok {
			if ptr.IsRaw && c.unsafeDepth == 0 {
				loc := expr.Location
				c.ctx.Diagnostics.Add(
					diagnostics.NewError("raw pointer dereference requires unsafe block").
						WithCode(diagnostics.ErrInvalidOperation).
						WithPrimaryLabel(&loc, "wrap this dereference in `unsafe { ... }`"),
				)
			}
			c.info.BindNode(expr, ptr.Inner)
			return ptr.Inner
		}
	case "-":
		if typeinfo.IsNumeric(right) {
			c.info.BindNode(expr, right)
			return right
		}
	case "!":
		if c.requireBool(expr.Right.Loc(), right) {
			typ := &typeinfo.BuiltinType{Name: "bool"}
			c.info.BindNode(expr, typ)
			return typ
		}
	case "?":
		return right
	}
	loc := expr.Location
	c.ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("invalid unary operator %q for %s", expr.Op, right.String())).
			WithCode(diagnostics.ErrInvalidOperation).
			WithPrimaryLabel(&loc, "invalid unary operation"),
	)
	return typeinfo.InvalidType{}
}

func (c *checker) typeOfBinary(scope *valueScope, expr *ast.BinaryExpr) typeinfo.Type {
	left := c.typeOfExpr(scope, expr.Left, nil)
	right := c.typeOfExpr(scope, expr.Right, left)
	switch expr.Op {
	case "+", "-", "*", "/", "%":
		if result := typeinfo.CommonNumericType(left, right); result != nil {
			c.info.BindNode(expr, result)
			return result
		}
	case "==", "!=":
		if typeinfo.Assignable(left, right) || typeinfo.Assignable(right, left) || typeinfo.CommonNumericType(left, right) != nil {
			typ := &typeinfo.BuiltinType{Name: "bool"}
			c.info.BindNode(expr, typ)
			return typ
		}
	case "<", "<=", ">", ">=":
		if typeinfo.CommonNumericType(left, right) != nil {
			typ := &typeinfo.BuiltinType{Name: "bool"}
			c.info.BindNode(expr, typ)
			return typ
		}
	case "&&", "||":
		if c.requireBool(expr.Left.Loc(), left) && c.requireBool(expr.Right.Loc(), right) {
			typ := &typeinfo.BuiltinType{Name: "bool"}
			c.info.BindNode(expr, typ)
			return typ
		}
	case "??":
		if opt, ok := left.(*typeinfo.OptionalType); ok {
			if !typeinfo.Assignable(opt.Inner, right) {
				c.reportTypeMismatch(expr.Right.Loc(), opt.Inner, right)
			}
			c.info.BindNode(expr, opt.Inner)
			return opt.Inner
		}
	}
	loc := expr.Location
	c.ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("invalid binary operation %s %q %s", left.String(), expr.Op, right.String())).
			WithCode(diagnostics.ErrInvalidOperation).
			WithPrimaryLabel(&loc, "invalid binary operation"),
	)
	return typeinfo.InvalidType{}
}

func (c *checker) inferNumberLiteralType(lit *ast.NumberLit, expected typeinfo.Type) typeinfo.Type {
	if lit == nil {
		return typeinfo.UnknownType{}
	}
	if expected != nil && typeinfo.IsNumeric(expected) {
		if c.numericLiteralFits(expected, lit.Value) {
			return expected
		}
		c.reportNumericLiteralOverflow(lit.Location, expected, lit.Value)
		return typeinfo.InvalidType{}
	}
	if numeric.IsFloat(lit.Value) {
		defaultType := typeinfo.DefaultFloatType()
		if !c.numericLiteralFits(defaultType, lit.Value) {
			c.reportNumericDefaultOverflow(lit.Location, defaultType, lit.Value)
			return typeinfo.InvalidType{}
		}
		return defaultType
	}
	defaultType := typeinfo.DefaultIntType()
	if !c.numericLiteralFits(defaultType, lit.Value) {
		c.reportNumericDefaultOverflow(lit.Location, defaultType, lit.Value)
		return typeinfo.InvalidType{}
	}
	return defaultType
}

func (c *checker) numericLiteralFits(target typeinfo.Type, raw string) bool {
	family, bits, ok := builtinNumericInfo(target)
	if !ok {
		return false
	}
	if numeric.IsFloat(raw) {
		if family != typeinfo.NumericFloat {
			return false
		}
		return numeric.FitsFloatLiteral(raw, bits)
	}
	if family == typeinfo.NumericFloat {
		return false
	}
	return numeric.FitsIntegerLiteral(raw, bits, family == typeinfo.NumericSigned)
}

func builtinNumericInfo(t typeinfo.Type) (typeinfo.NumericFamily, int, bool) {
	return typeinfo.NumericInfo(t)
}

func (c *checker) typeOfPostfix(scope *valueScope, expr *ast.PostfixExpr) typeinfo.Type {
	left := c.typeOfExpr(scope, expr.Left, nil)
	if expr.Op == "!!" {
		if errUnion, ok := left.(*typeinfo.ErrorUnionType); ok {
			c.info.BindNode(expr, errUnion.Value)
			return errUnion.Value
		}
	}
	loc := expr.Location
	c.ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("invalid postfix operator %q", expr.Op)).
			WithCode(diagnostics.ErrInvalidErrorPropagate).
			WithPrimaryLabel(&loc, "invalid postfix operation"),
	)
	return typeinfo.InvalidType{}
}

func (c *checker) typeOfCall(scope *valueScope, expr *ast.CallExpr) typeinfo.Type {
	if selector, ok := expr.Callee.(*ast.SelectorExpr); ok {
		if typ, handled := c.typeOfMethodCall(scope, expr, selector); handled {
			return typ
		}
	}

	calleeType := c.typeOfExpr(scope, expr.Callee, nil)
	if typeinfo.IsInvalid(calleeType) || typeinfo.IsUnknown(calleeType) {
		return typeinfo.InvalidType{}
	}
	fnType, ok := calleeType.(*typeinfo.FuncType)
	if !ok {
		loc := expr.Callee.Loc()
		c.ctx.Diagnostics.Add(
			diagnostics.NewError("expression is not callable").
				WithCode(diagnostics.ErrNotCallable).
				WithPrimaryLabel(&loc, "cannot call this expression"),
		)
		return typeinfo.InvalidType{}
	}
	if fnType.IsUnsafe && c.unsafeDepth == 0 {
		loc := expr.Callee.Loc()
		c.ctx.Diagnostics.Add(
			diagnostics.NewError("unsafe function call requires unsafe block").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(&loc, "wrap this call in `unsafe { ... }`"),
		)
	}
	c.typecheckCallArgs(scope, expr, fnType)
	c.info.BindNode(expr, fnType.Result)
	return fnType.Result
}

func (c *checker) typeOfCatch(scope *valueScope, expr *ast.CatchExpr) typeinfo.Type {
	left := c.typeOfExpr(scope, expr.Left, nil)
	errUnion, ok := left.(*typeinfo.ErrorUnionType)
	if !ok {
		loc := expr.Location
		c.ctx.Diagnostics.Add(
			diagnostics.NewError("catch requires an error union value").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(&loc, "this expression is not an error union"),
		)
		return typeinfo.InvalidType{}
	}
	if expr.Handler != nil {
		handlerScope := newValueScope(scope)
		handlerScope.Declare(expr.Payload.Text(), valueInfo{typ: errUnion.Error, mutable: false})
		c.checkStmt(handlerScope, expr.Handler)
		if !stmtDefinitelyExits(expr.Handler) {
			loc := expr.Handler.Location
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("catch handler block must exit early").
					WithCode(diagnostics.ErrInvalidReturn).
					WithPrimaryLabel(&loc, "this catch handler can continue without producing a value").
					WithHelp("return, panic, break, or continue from every path, or use `catch <fallback>`"),
			)
		}
		c.info.BindNode(expr, errUnion.Value)
		return errUnion.Value
	}
	fallbackType := c.typeOfExpr(scope, expr.Fallback, errUnion.Value)
	if !typeinfo.Assignable(errUnion.Value, fallbackType) {
		c.reportTypeMismatch(expr.Fallback.Loc(), errUnion.Value, fallbackType)
	}
	c.info.BindNode(expr, errUnion.Value)
	return errUnion.Value
}

func (c *checker) typeOfMethodCall(scope *valueScope, call *ast.CallExpr, selector *ast.SelectorExpr) (typeinfo.Type, bool) {
	receiverType := c.typeOfExpr(scope, selector.Left, nil)
	if typeinfo.IsInvalid(receiverType) || typeinfo.IsUnknown(receiverType) {
		return typeinfo.InvalidType{}, true
	}

	if field := c.lookupStructField(receiverType, selector.Name.Text()); field != nil {
		return nil, false
	}

	if iface, ok := c.underlying(receiverType).(*typeinfo.InterfaceType); ok {
		method := iface.Methods[selector.Name.Text()]
		if method == nil {
			c.reportMethodNotFound(selector.Location, receiverType, selector.Name.Text())
			return typeinfo.InvalidType{}, true
		}
		c.info.BindNode(selector, method)
		c.typecheckCallArgs(scope, call, method)
		c.info.BindNode(call, method.Result)
		return method.Result, true
	}

	addressable, mutable := c.exprAccess(scope, selector.Left)
	_, methodType := c.lookupMethod(receiverType, selector.Name.Text(), addressable, mutable)
	if methodType == nil {
		if c.canHaveMethods(receiverType) {
			c.reportMethodNotFound(selector.Location, receiverType, selector.Name.Text())
			return typeinfo.InvalidType{}, true
		}
		return nil, false
	}

	c.info.BindNode(selector, methodType)
	c.typecheckCallArgs(scope, call, methodType)
	c.info.BindNode(call, methodType.Result)
	return methodType.Result, true
}

func (c *checker) typecheckCallArgs(scope *valueScope, call *ast.CallExpr, fnType *typeinfo.FuncType) {
	if fnType == nil {
		return
	}
	if len(call.Args) != len(fnType.Params) {
		c.reportWrongArgCount(call.Location, len(fnType.Params), len(call.Args))
	}
	for i, arg := range call.Args {
		var expected typeinfo.Type
		if i < len(fnType.Params) {
			expected = fnType.Params[i]
		}
		argType := c.typeOfExpr(scope, arg, expected)
		if expected != nil && !typeinfo.Assignable(expected, argType) {
			c.reportTypeMismatch(arg.Loc(), expected, argType)
		}
		if i < len(fnType.ComptimeParams) && fnType.ComptimeParams[i] {
			c.requireConstExpr(scope, arg, "argument to comptime parameter must be compile-time evaluable")
		}
	}
}

func (c *checker) typeOfSelector(scope *valueScope, expr *ast.SelectorExpr) typeinfo.Type {
	left := c.typeOfExpr(scope, expr.Left, nil)
	base := c.derefForSelector(left)
	structType, ok := c.structView(base)
	if !ok {
		loc := expr.Location
		c.ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("type %s has no field %q", left.String(), expr.Name.Text())).
				WithCode(diagnostics.ErrFieldNotFound).
				WithPrimaryLabel(&loc, "invalid field access"),
		)
		return typeinfo.InvalidType{}
	}
	field := structType.Fields[expr.Name.Text()]
	if field == nil {
		loc := expr.Location
		c.ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("unknown field %q", expr.Name.Text())).
				WithCode(diagnostics.ErrFieldNotFound).
				WithPrimaryLabel(&loc, "field does not exist on this type"),
		)
		return typeinfo.InvalidType{}
	}
	c.info.BindNode(expr, field.Type)
	return field.Type
}

func (c *checker) typeOfCast(scope *valueScope, expr *ast.CastExpr) typeinfo.Type {
	target := c.typeFromSyntax(c.mod, expr.Type)
	sourceType := c.typeOfExpr(scope, expr.Left, nil)
	if typeinfo.Equal(target, sourceType) {
		c.info.BindNode(expr, target)
		return target
	}
	if typeinfo.IsNumeric(target) && typeinfo.IsNumeric(sourceType) {
		c.info.BindNode(expr, target)
		return target
	}
	loc := expr.Location
	c.ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("cannot cast %s to %s", sourceType.String(), target.String())).
			WithCode(diagnostics.ErrInvalidCast).
			WithPrimaryLabel(&loc, "invalid explicit cast"),
	)
	return typeinfo.InvalidType{}
}

func (c *checker) typeOfIndex(scope *valueScope, expr *ast.IndexExpr) typeinfo.Type {
	baseTyp := c.typeOfExpr(scope, expr.Left, nil)
	// typecheck the index as usize
	usize := &typeinfo.BuiltinType{Name: "usize"}
	c.typeOfExpr(scope, expr.Index, usize)
	base := c.underlying(baseTyp)
	if arr, ok := base.(*typeinfo.ArrayType); ok {
		c.info.BindNode(expr, arr.Inner)
		return arr.Inner
	}
	// Pointer indexing: *T[i] → T
	if ptr, ok := base.(*typeinfo.PointerType); ok {
		c.info.BindNode(expr, ptr.Inner)
		return ptr.Inner
	}
	loc := expr.Location
	c.ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("cannot index into %s", baseTyp.String())).
			WithCode(diagnostics.ErrInvalidOperation).
			WithPrimaryLabel(&loc, "not an array or pointer type"),
	)
	return typeinfo.InvalidType{}
}

func (c *checker) typeOfComposite(scope *valueScope, expr *ast.CompositeLit, expected typeinfo.Type) typeinfo.Type {
	if expected == nil {
		loc := expr.Location
		c.ctx.Diagnostics.Add(
			diagnostics.NewError("composite literal requires a known target type").
				WithCode(diagnostics.ErrInvalidType).
				WithPrimaryLabel(&loc, "add an explicit type or typed context"),
		)
		return typeinfo.InvalidType{}
	}
	base := c.underlying(expected)
	// Array literal: positional elements matching element type.
	if arrType, ok := base.(*typeinfo.ArrayType); ok {
		for i, item := range expr.Items {
			if item.Name != nil {
				loc := item.Name.Location
				c.ctx.Diagnostics.Add(
					diagnostics.NewError("array literal does not support named elements").
						WithCode(diagnostics.ErrInvalidType).
						WithPrimaryLabel(&loc, "use positional elements"),
				)
				continue
			}
			if arrType.Len >= 0 && int64(i) >= arrType.Len {
				loc := expr.Location
				c.ctx.Diagnostics.Add(
					diagnostics.NewError("too many elements in array literal").
						WithCode(diagnostics.ErrExtraField).
						WithPrimaryLabel(&loc, "excess element"),
				)
				break
			}
			got := c.typeOfExpr(scope, item.Value, arrType.Inner)
			if !typeinfo.Assignable(arrType.Inner, got) {
				c.reportTypeMismatch(item.Value.Loc(), arrType.Inner, got)
			}
		}
		c.info.BindNode(expr, expected)
		return expected
	}
	structType, ok := base.(*typeinfo.StructType)
	if !ok {
		c.info.BindNode(expr, expected)
		return expected
	}
	var positional int
	for _, item := range expr.Items {
		if item.Name != nil {
			fieldName := item.Name.Text()
			field := structType.Fields[fieldName]
			if field == nil {
				loc := expr.Location
				c.ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("unknown field %q", fieldName)).
						WithCode(diagnostics.ErrUnknownField).
						WithPrimaryLabel(&loc, "field does not exist on this type"),
				)
				continue
			}
			got := c.typeOfExpr(scope, item.Value, field.Type)
			if !typeinfo.Assignable(field.Type, got) {
				c.reportTypeMismatch(item.Value.Loc(), field.Type, got)
			}
			continue
		}
		fields := c.orderedStructFields(structType)
		if positional >= len(fields) {
			loc := expr.Location
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("too many composite literal elements").
					WithCode(diagnostics.ErrExtraField).
					WithPrimaryLabel(&loc, "extra element in composite literal"),
			)
			continue
		}
		field := fields[positional]
		got := c.typeOfExpr(scope, item.Value, field.Type)
		if !typeinfo.Assignable(field.Type, got) {
			c.reportTypeMismatch(item.Value.Loc(), field.Type, got)
		}
		positional++
	}
	c.info.BindNode(expr, expected)
	return expected
}

func (c *checker) typeOfSymbol(sym *symbols.Symbol) typeinfo.Type {
	if sym == nil {
		return typeinfo.InvalidType{}
	}
	if typ, ok := c.info.Symbols[sym]; ok {
		return typ
	}
	if owner := c.findModuleForSymbol(sym); owner != nil && owner != c.mod && owner.Types != nil {
		if typ, ok := owner.Types.Symbols[sym]; ok {
			return typ
		}
	}

	owner := c.findModuleForSymbol(sym)
	if owner == nil {
		owner = c.mod
	}
	typ := c.synthesizeSymbolType(owner, sym)
	if owner == c.mod {
		c.info.BindSymbol(sym, typ)
	} else {
		if owner.Types == nil {
			owner.Types = typeinfo.NewModuleInfo()
		}
		owner.Types.BindSymbol(sym, typ)
	}
	return typ
}

func (c *checker) synthesizeSymbolType(mod *context.Module, sym *symbols.Symbol) typeinfo.Type {
	switch sym.Kind {
	case symbols.SymbolType:
		decl, _ := sym.Node.(*ast.TypeDecl)
		return &typeinfo.NamedType{ModuleKey: mod.Key, Name: sym.Name, Decl: decl}
	case symbols.SymbolFunc, symbols.SymbolMethod:
		decl, _ := sym.Node.(*ast.FuncDecl)
		return c.funcType(mod, decl)
	case symbols.SymbolVar, symbols.SymbolConst:
		switch n := sym.Node.(type) {
		case *ast.LetDecl:
			if n.Type != nil {
				return c.typeFromSyntax(mod, n.Type)
			}
			if n.Value != nil {
				return c.typeOfExpr(nil, n.Value, nil)
			}
		case *ast.ConstDecl:
			if n.Type != nil {
				return c.typeFromSyntax(mod, n.Type)
			}
			if n.Value != nil {
				return c.typeOfExpr(nil, n.Value, nil)
			}
		case *ast.ConstStmt:
			if n.Type != nil {
				return c.typeFromSyntax(mod, n.Type)
			}
			if n.Value != nil {
				return c.typeOfExpr(nil, n.Value, nil)
			}
		case *ast.LetStmt:
			if n.Type != nil {
				return c.typeFromSyntax(mod, n.Type)
			}
			if n.Value != nil {
				return c.typeOfExpr(nil, n.Value, nil)
			}
		}
		if sym.Node == nil {
			switch sym.Name {
			case "true", "false":
				return &typeinfo.BuiltinType{Name: "bool"}
			case "none":
				return typeinfo.UnknownType{}
			case "undefined":
				return typeinfo.UndefinedType{}
			}
		}
	case symbols.SymbolStatic:
		if field, ok := sym.Node.(*ast.StaticFieldDecl); ok {
			return c.typeFromSyntax(mod, field.Type)
		}
	case symbols.SymbolVariant, symbols.SymbolError:
		if ownerName, ok := c.findTypeMemberOwner(mod, sym); ok {
			return &typeinfo.NamedType{
				ModuleKey: mod.Key,
				Name:      ownerName,
				Decl:      c.findTypeDecl(mod, ownerName),
			}
		}
	}
	return typeinfo.UnknownType{}
}

func (c *checker) funcType(mod *context.Module, fn *ast.FuncDecl) *typeinfo.FuncType {
	if fn == nil {
		return &typeinfo.FuncType{Result: &typeinfo.BuiltinType{Name: "void"}}
	}
	params := make([]typeinfo.Type, 0, len(fn.Params))
	comptimeParams := make([]bool, 0, len(fn.Params))
	for _, param := range fn.Params {
		params = append(params, c.typeFromSyntax(mod, param.Type))
		comptimeParams = append(comptimeParams, param.IsComptime)
	}
	return &typeinfo.FuncType{
		IsUnsafe:       fn.IsUnsafe,
		Params:         params,
		ComptimeParams: comptimeParams,
		Result:         c.funcResultType(mod, fn),
	}
}

func (c *checker) funcResultType(mod *context.Module, fn *ast.FuncDecl) typeinfo.Type {
	if fn == nil || fn.Result == nil {
		return &typeinfo.BuiltinType{Name: "void"}
	}
	return c.typeFromSyntax(mod, fn.Result)
}

func (c *checker) typeFromSyntax(mod *context.Module, expr ast.TypeExpr) typeinfo.Type {
	switch t := expr.(type) {
	case nil:
		return nil
	case *ast.NamedType:
		if len(t.Path) == 1 && tokens.IsBuiltinType(t.Path[0]) {
			return &typeinfo.BuiltinType{Name: t.Path[0]}
		}
		resolution := c.lookupTypeResolution(mod, t)
		if resolution == nil || resolution.Symbol == nil {
			return typeinfo.InvalidType{}
		}
		owner := c.findModuleForSymbol(resolution.Symbol)
		if owner == nil {
			owner = mod
		}
		decl, _ := resolution.Symbol.Node.(*ast.TypeDecl)
		return &typeinfo.NamedType{ModuleKey: owner.Key, Name: resolution.Symbol.Name, Decl: decl}
	case *ast.PointerType:
		return &typeinfo.PointerType{IsOwn: t.IsOwn, IsRaw: t.IsRaw, IsMut: t.IsMut, Inner: c.typeFromSyntax(mod, t.Inner)}
	case *ast.OptionalType:
		return &typeinfo.OptionalType{Inner: c.typeFromSyntax(mod, t.Inner)}
	case *ast.ErrorUnionType:
		return &typeinfo.ErrorUnionType{Error: c.typeFromSyntax(mod, t.Error), Value: c.typeFromSyntax(mod, t.Value)}
	case *ast.ArrayType:
		return &typeinfo.ArrayType{Inner: c.typeFromSyntax(mod, t.Inner), Len: c.arrayLength(t.Size)}
	case *ast.TupleType:
		elems := make([]typeinfo.Type, 0, len(t.Elems))
		for _, elem := range t.Elems {
			elems = append(elems, c.typeFromSyntax(mod, elem))
		}
		return &typeinfo.TupleType{Elems: elems}
	case *ast.StructType:
		fields := make(map[string]*typeinfo.StructField)
		orderedFields := make([]*typeinfo.StructField, 0, len(t.Fields))
		staticFields := make(map[string]*typeinfo.StructField)
		orderedStaticFields := make([]*typeinfo.StructField, 0, len(t.StaticFields))
		for _, field := range t.Fields {
			if field == nil {
				continue
			}
			fieldName := field.Name.Text()
			structField := &typeinfo.StructField{Name: fieldName, Type: c.typeFromSyntax(mod, field.Type)}
			fields[fieldName] = structField
			orderedFields = append(orderedFields, structField)
		}
		for _, field := range t.StaticFields {
			if field == nil {
				continue
			}
			fieldName := field.Name.Text()
			structField := &typeinfo.StructField{Name: fieldName, Type: c.typeFromSyntax(mod, field.Type)}
			staticFields[fieldName] = structField
			orderedStaticFields = append(orderedStaticFields, structField)
		}
		return &typeinfo.StructType{Fields: fields, OrderedFields: orderedFields, StaticFields: staticFields, OrderedStaticFields: orderedStaticFields}
	case *ast.EnumType:
		variants := make(map[string]struct{}, len(t.Variants))
		orderedVariants := make([]string, 0, len(t.Variants))
		for _, variant := range t.Variants {
			if variant != nil {
				name := variant.Name.Text()
				variants[name] = struct{}{}
				orderedVariants = append(orderedVariants, name)
			}
		}
		return &typeinfo.EnumType{Variants: variants, OrderedVariants: orderedVariants}
	case *ast.ErrorType:
		members := make(map[string]struct{}, len(t.Members))
		orderedMembers := make([]string, 0, len(t.Members))
		for _, member := range t.Members {
			if member != nil {
				name := member.Name.Text()
				members[name] = struct{}{}
				orderedMembers = append(orderedMembers, name)
			}
		}
		return &typeinfo.ErrorSetType{Members: members, OrderedMembers: orderedMembers}
	case *ast.UnionType:
		members := make([]typeinfo.Type, 0, len(t.Members))
		for _, member := range t.Members {
			members = append(members, c.typeFromSyntax(mod, member))
		}
		return &typeinfo.UnionType{Members: members}
	case *ast.InterfaceType:
		methods := make(map[string]*typeinfo.FuncType)
		orderedMethods := make([]*typeinfo.InterfaceMethod, 0, len(t.Methods))
		for _, method := range t.Methods {
			if method == nil {
				continue
			}
			params := make([]typeinfo.Type, 0, len(method.Params))
			comptimeParams := make([]bool, 0, len(method.Params))
			for _, param := range method.Params {
				params = append(params, c.typeFromSyntax(mod, param.Type))
				comptimeParams = append(comptimeParams, param.IsComptime)
			}
			fnType := &typeinfo.FuncType{Params: params, ComptimeParams: comptimeParams, Result: c.typeFromSyntax(mod, method.Result)}
			name := method.Name.Text()
			methods[name] = fnType
			orderedMethods = append(orderedMethods, &typeinfo.InterfaceMethod{Name: name, Type: fnType})
		}
		return &typeinfo.InterfaceType{Methods: methods, OrderedMethods: orderedMethods}
	default:
		return typeinfo.UnknownType{}
	}
}

func (c *checker) arrayLength(expr ast.Expr) int64 {
	if expr == nil {
		return -1
	}
	lit, ok := expr.(*ast.NumberLit)
	if !ok {
		return -1
	}
	value, err := numeric.StringToBigInt(lit.Value)
	if err != nil || value.Sign() < 0 || !value.IsInt64() {
		return -1
	}
	return value.Int64()
}

func (c *checker) lookupResolution(node ast.Node) *binding.Resolution {
	if c.mod == nil || c.mod.Bindings == nil || node == nil {
		return nil
	}
	return c.mod.Bindings.Nodes[node]
}

func (c *checker) lookupTypeResolution(mod *context.Module, node ast.Node) *binding.Resolution {
	if mod == nil || mod.Bindings == nil || node == nil {
		return nil
	}
	return mod.Bindings.Nodes[node]
}

func (c *checker) underlying(typ typeinfo.Type) typeinfo.Type {
	if named, ok := typ.(*typeinfo.NamedType); ok && named.Decl != nil {
		owner := c.findModuleForType(named)
		if owner == nil {
			owner = c.mod
		}
		return c.typeFromSyntax(owner, named.Decl.Type)
	}
	return typ
}

func (c *checker) findModuleForType(typ *typeinfo.NamedType) *context.Module {
	if typ == nil {
		return nil
	}
	if c.ctx.Prelude != nil && typ.ModuleKey == c.ctx.Prelude.Key {
		return c.ctx.Prelude
	}
	if mod, ok := c.ctx.GetModule(typ.ModuleKey); ok {
		return mod
	}
	return nil
}

func (c *checker) structView(typ typeinfo.Type) (*typeinfo.StructType, bool) {
	base := c.underlying(typ)
	st, ok := base.(*typeinfo.StructType)
	return st, ok
}

func (c *checker) derefForSelector(typ typeinfo.Type) typeinfo.Type {
	if ptr, ok := typ.(*typeinfo.PointerType); ok {
		return ptr.Inner
	}
	return typ
}

func (c *checker) orderedStructFields(st *typeinfo.StructType) []*typeinfo.StructField {
	if st == nil {
		return nil
	}
	return st.OrderedFields
}

func (c *checker) lookupStructField(typ typeinfo.Type, name string) *typeinfo.StructField {
	structType, ok := c.structView(c.derefForSelector(typ))
	if !ok || structType == nil {
		return nil
	}
	return structType.Fields[name]
}

func (c *checker) canHaveMethods(typ typeinfo.Type) bool {
	if typ == nil {
		return false
	}
	if _, ok := c.receiverBaseNamedType(typ); ok {
		return true
	}
	_, ok := c.underlying(typ).(*typeinfo.InterfaceType)
	return ok
}

func (c *checker) lookupMethod(receiverType typeinfo.Type, name string, addressable bool, mutable bool) (*symbols.Symbol, *typeinfo.FuncType) {
	baseNamed, ok := c.receiverBaseNamedType(receiverType)
	if !ok {
		return nil, nil
	}
	owner := c.findModuleForType(baseNamed)
	if owner == nil || owner.MethodSets == nil {
		return nil, nil
	}
	for _, key := range c.methodCandidateKeys(receiverType, baseNamed.Name, addressable, mutable) {
		methods := owner.MethodSets[key]
		if methods == nil {
			continue
		}
		sym := methods[name]
		if sym == nil {
			continue
		}
		fnType, _ := c.typeOfSymbol(sym).(*typeinfo.FuncType)
		return sym, fnType
	}
	return nil, nil
}

func (c *checker) methodCandidateKeys(receiverType typeinfo.Type, baseName string, addressable bool, mutable bool) []string {
	keys := make([]string, 0, 4)
	seen := make(map[string]struct{})
	add := func(key string) {
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}

	switch t := receiverType.(type) {
	case *typeinfo.NamedType:
		add(baseName)
		if addressable {
			add("*" + baseName)
			if mutable {
				add("*mut " + baseName)
			}
		}
	case *typeinfo.PointerType:
		if exact, ok := c.receiverKeyFromType(t); ok {
			add(exact)
		}
		if t.IsOwn {
			add("*mut " + baseName)
			add("*" + baseName)
		} else if t.IsMut {
			add("*" + baseName)
		}
	}
	return keys
}

func (c *checker) receiverBaseNamedType(typ typeinfo.Type) (*typeinfo.NamedType, bool) {
	switch t := typ.(type) {
	case *typeinfo.NamedType:
		return t, true
	case *typeinfo.PointerType:
		named, ok := t.Inner.(*typeinfo.NamedType)
		return named, ok
	default:
		return nil, false
	}
}

func (c *checker) receiverKeyFromType(typ typeinfo.Type) (string, bool) {
	switch t := typ.(type) {
	case *typeinfo.NamedType:
		return t.Name, true
	case *typeinfo.PointerType:
		named, ok := t.Inner.(*typeinfo.NamedType)
		if !ok {
			return "", false
		}
		prefix := "*"
		if t.IsOwn {
			prefix += "own "
		}
		if t.IsRaw {
			prefix += "raw "
		}
		if t.IsMut {
			prefix += "mut "
		}
		return prefix + named.Name, true
	default:
		return "", false
	}
}

func (c *checker) exprAccess(scope *valueScope, expr ast.Expr) (addressable bool, mutable bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		if len(e.Path) == 1 {
			if info, ok := scope.Lookup(e.Path[0]); ok {
				return true, info.mutable && !info.constant
			}
		}
		res := c.lookupResolution(e)
		if res == nil || res.Symbol == nil {
			return false, false
		}
		switch node := res.Symbol.Node.(type) {
		case *ast.LetDecl:
			return true, node.IsMut
		case *ast.LetStmt:
			return true, node.IsMut
		case *ast.ConstDecl, *ast.ConstStmt:
			return true, false
		default:
			return true, res.Symbol.Kind == symbols.SymbolVar
		}
	case *ast.SelectorExpr:
		return c.exprAccess(scope, e.Left)
	default:
		return false, false
	}
}

func (c *checker) findModuleForSymbol(sym *symbols.Symbol) *context.Module {
	if sym == nil {
		return nil
	}
	if mod := c.ctx.Prelude; mod != nil {
		if mod.ModuleScope != nil {
			if slices.Contains(mod.ModuleScope.Symbols(), sym) {
				return mod
			}
		}
		for _, methods := range mod.MethodSets {
			for _, candidate := range methods {
				if candidate == sym {
					return mod
				}
			}
		}
		for _, members := range mod.TypeMembers {
			for _, candidate := range members {
				if candidate == sym {
					return mod
				}
			}
		}
	}
	for _, mod := range c.ctx.Modules() {
		if mod == nil {
			continue
		}
		if mod.ModuleScope != nil {
			if slices.Contains(mod.ModuleScope.Symbols(), sym) {
				return mod
			}
		}
		for _, methods := range mod.MethodSets {
			for _, candidate := range methods {
				if candidate == sym {
					return mod
				}
			}
		}
		for _, members := range mod.TypeMembers {
			for _, candidate := range members {
				if candidate == sym {
					return mod
				}
			}
		}
	}
	return nil
}

func (c *checker) findTypeMemberOwner(mod *context.Module, sym *symbols.Symbol) (string, bool) {
	if mod == nil || sym == nil {
		return "", false
	}
	for typeName, members := range mod.TypeMembers {
		for _, candidate := range members {
			if candidate == sym {
				return typeName, true
			}
		}
	}
	return "", false
}

func (c *checker) findTypeDecl(mod *context.Module, name string) *ast.TypeDecl {
	if mod == nil || mod.AST == nil {
		return nil
	}
	for _, decl := range mod.AST.Decls {
		typeDecl, ok := decl.(*ast.TypeDecl)
		if ok && typeDecl.Name.Text() == name {
			return typeDecl
		}
	}
	return nil
}

func (c *checker) requireBool(loc source.Location, typ typeinfo.Type) bool {
	if typeinfo.IsBuiltinNamed(typ, "bool") {
		return true
	}
	c.ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("expected bool, got %s", typ.String())).
			WithCode(diagnostics.ErrTypeMismatch).
			WithPrimaryLabel(&loc, "condition must be bool"),
	)
	return false
}

func (c *checker) reportMethodNotFound(loc source.Location, receiver typeinfo.Type, name string) {
	c.ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("type %s has no method %q", receiver.String(), name)).
			WithCode(diagnostics.ErrMethodNotFound).
			WithPrimaryLabel(&loc, "cannot resolve this method call"),
	)
}

func (c *checker) requireConstExpr(scope *valueScope, expr ast.Expr, message string) {
	if expr == nil || c.isConstExpr(scope, expr) {
		return
	}
	loc := expr.Loc()
	c.ctx.Diagnostics.Add(
		diagnostics.NewError(message).
			WithCode(diagnostics.ErrTypeMismatch).
			WithPrimaryLabel(&loc, "this expression is not compile-time evaluable"),
	)
}

func (c *checker) isConstExpr(scope *valueScope, expr ast.Expr) bool {
	switch e := expr.(type) {
	case nil:
		return true
	case *ast.BadExpr:
		return false
	case *ast.NumberLit, *ast.StringLit, *ast.NoneLit:
		return true
	case *ast.Ident:
		return c.isConstIdent(scope, e)
	case *ast.PrefixExpr:
		switch e.Op {
		case "comptime", "copy", "-", "!", "?":
			return c.isConstExpr(scope, e.Right)
		default:
			return false
		}
	case *ast.BinaryExpr:
		return c.isConstExpr(scope, e.Left) && c.isConstExpr(scope, e.Right)
	case *ast.CastExpr:
		return c.isConstExpr(scope, e.Left)
	case *ast.CompositeLit:
		for _, item := range e.Items {
			if !c.isConstExpr(scope, item.Value) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func (c *checker) isConstIdent(scope *valueScope, ident *ast.Ident) bool {
	if ident == nil || len(ident.Path) == 0 {
		return false
	}
	if len(ident.Path) == 1 && scope != nil {
		if info, ok := scope.Lookup(ident.Path[0]); ok && info != nil && info.constant {
			return true
		}
	}
	if len(ident.Path) == 1 {
		switch ident.Path[0] {
		case "true", "false", "none":
			return true
		case "undefined":
			return false
		}
	}
	res := c.lookupResolution(ident)
	if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
		return false
	}
	if res.Symbol.Kind != symbols.SymbolConst {
		return false
	}
	if res.Symbol.Node == nil {
		return res.Symbol.Name != "undefined"
	}
	return true
}

func (c *checker) reportTypeMismatch(loc source.Location, expected, got typeinfo.Type) {
	if msg, ok := numericMismatchMessage(expected, got); ok {
		c.ctx.Diagnostics.Add(
			diagnostics.NewError(msg).
				WithCode(diagnostics.ErrTypeMismatch).
				WithPrimaryLabel(&loc, "explicit conversion is required"),
		)
		return
	}
	c.ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("type mismatch: expected %s, got %s", expected.String(), got.String())).
			WithCode(diagnostics.ErrTypeMismatch).
			WithPrimaryLabel(&loc, "type mismatch"),
	)
}

func (c *checker) reportWrongArgCount(loc source.Location, expected, got int) {
	c.ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("wrong argument count: expected %d, got %d", expected, got)).
			WithCode(diagnostics.ErrWrongArgumentCount).
			WithPrimaryLabel(&loc, "argument count does not match"),
	)
}

func (c *checker) reportNumericLiteralOverflow(loc source.Location, target typeinfo.Type, raw string) {
	c.ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("numeric literal %s does not fit in %s", raw, target.String())).
			WithCode(diagnostics.ErrTypeMismatch).
			WithPrimaryLabel(&loc, "value exceeds the target type's range"),
	)
}

func (c *checker) reportNumericDefaultOverflow(loc source.Location, defaultType typeinfo.Type, raw string) {
	c.ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("numeric literal %s does not fit in default numeric type %s", raw, defaultType.String())).
			WithCode(diagnostics.ErrTypeMismatch).
			WithPrimaryLabel(&loc, "add an explicit wider type if this value is intentional"),
	)
}

func numericMismatchMessage(expected, got typeinfo.Type) (string, bool) {
	expectedFamily, expectedBits, okExpected := typeinfo.NumericInfo(expected)
	gotFamily, gotBits, okGot := typeinfo.NumericInfo(got)
	if !okExpected || !okGot {
		return "", false
	}
	if expectedFamily == gotFamily && expectedBits < gotBits {
		return fmt.Sprintf("cannot implicitly narrow %s to %s", got.String(), expected.String()), true
	}
	if expectedFamily != gotFamily {
		return fmt.Sprintf("cannot implicitly convert %s to %s", got.String(), expected.String()), true
	}
	return "", false
}

func (c *checker) forBindingTypes(iterable typeinfo.Type) (typeinfo.Type, typeinfo.Type) {
	indexType := &typeinfo.BuiltinType{Name: "usize"}
	switch t := iterable.(type) {
	case *typeinfo.ArrayType:
		return indexType, t.Inner
	}
	return indexType, typeinfo.UnknownType{}
}

func stmtDefinitelyExits(stmt ast.Stmt) bool {
	switch s := stmt.(type) {
	case nil:
		return false
	case *ast.BlockStmt:
		if len(s.Stmts) == 0 {
			return false
		}
		return stmtDefinitelyExits(s.Stmts[len(s.Stmts)-1])
	case *ast.ReturnStmt, *ast.PanicStmt, *ast.BreakStmt, *ast.ContinueStmt:
		return true
	case *ast.IfStmt:
		return stmtDefinitelyExits(s.Then) && stmtDefinitelyExits(s.Else)
	case *ast.MatchStmt:
		if len(s.Arms) == 0 {
			return false
		}
		hasWildcard := false
		for _, arm := range s.Arms {
			if arm == nil || !stmtDefinitelyExits(arm.Body) {
				return false
			}
			if arm.Wildcard {
				hasWildcard = true
			}
		}
		return hasWildcard
	default:
		return false
	}
}
