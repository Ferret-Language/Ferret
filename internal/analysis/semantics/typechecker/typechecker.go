package typechecker

import (
	"fmt"
	"slices"
	"strings"

	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/semmeta"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/source"
	"compiler/internal/frontend/ast"
	"compiler/internal/utils/numeric"
)

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
		finalType := c.resolveDeclaredValueType(declared, value)
		if finalType == nil {
			finalType = typeinfo.UnknownType{}
		}
		if d.Type != nil && declared != nil && !typeinfo.Equal(declared, finalType) {
			c.info.BindNode(d.Type, finalType)
		}
		if declared != nil && d.Value != nil {
			c.checkAssignable(d.Value.Loc(), declared, value)
		}
		c.checkModuleBindingType(d.Name.Loc(), finalType)
		c.bindDeclSymbol(d.Name, finalType)
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
		finalType := c.resolveDeclaredValueType(declared, value)
		if finalType == nil {
			finalType = typeinfo.UnknownType{}
		}
		if d.Type != nil && declared != nil && !typeinfo.Equal(declared, finalType) {
			c.info.BindNode(d.Type, finalType)
		}
		if declared != nil && d.Value != nil {
			c.checkAssignable(d.Value.Loc(), declared, value)
		}
		c.requireConstExpr(nil, d.Value, "constant initializer must be compile-time evaluable")
		c.checkModuleBindingType(d.Name.Loc(), finalType)
		c.bindDeclSymbol(d.Name, finalType)
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
	typeParams := c.pushTypeParams(c.mod, d, d.TypeParams)
	defer c.popTypeParams()
	for i, param := range d.TypeParams {
		if i >= len(typeParams) {
			continue
		}
		c.info.BindNode(param.Name, typeParams[i])
	}
	resolveDeclType := func() typeinfo.Type { return c.typeFromSyntax(c.mod, d.Type) }
	var declType typeinfo.Type
	if d.IsConstraint {
		declType = c.withConstraintContext(resolveDeclType)
	} else {
		declType = resolveDeclType()
	}
	if d.Type != nil {
		c.info.BindNode(d.Type, declType)
	}
	if d.IsConstraint {
		return
	}
	switch t := d.Type.(type) {
	case *ast.StructType:
		c.checkHeapStoredReference(d.Name.Loc(), &typeinfo.PointerType{Inner: declType})
		for _, field := range t.Fields {
			if field == nil {
				continue
			}
			fieldType := c.typeFromSyntax(c.mod, field.Type)
			if field.Type != nil {
				c.info.BindNode(field.Type, fieldType)
			}
			c.checkHeapStoredReference(field.Type.Loc(), fieldType)
			if field.Default != nil {
				valueType := c.typeOfExpr(nil, field.Default, fieldType)
				c.checkAssignable(field.Default.Loc(), fieldType, valueType)
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
		c.checkHeapStoredReference(d.Name.Loc(), &typeinfo.PointerType{Inner: declType})
		for _, member := range t.Members {
			if member != nil {
				c.info.BindNode(member, c.typeFromSyntax(c.mod, member))
			}
		}
	case *ast.TupleType, *ast.ArrayType, *ast.OptionalType, *ast.ErrorUnionType:
		c.checkHeapStoredReference(d.Name.Loc(), &typeinfo.PointerType{Inner: declType})
	}
}

func (c *checker) resolveDeclaredValueType(declared, value typeinfo.Type) typeinfo.Type {
	if arr, ok := declared.(*typeinfo.ArrayType); ok && arr != nil && arr.Len == -2 {
		if concrete, ok := value.(*typeinfo.ArrayType); ok && concrete != nil && typeinfo.Equal(arr.Inner, concrete.Inner) {
			return concrete
		}
	}
	if declared != nil {
		return declared
	}
	return value
}

func (c *checker) checkModuleBindingType(loc source.Location, typ typeinfo.Type) {
	if c == nil || typ == nil {
		return
	}
	if _, ok := typ.(*typeinfo.RefType); ok {
		c.ctx.Diagnostics.Add(
			diagnostics.NewError("module-level bindings cannot have reference type").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(&loc, "references are stack-only and cannot escape to module scope"),
		)
	}
}

func (c *checker) checkHeapStoredReference(loc source.Location, typ typeinfo.Type) {
	if c == nil || typ == nil {
		return
	}
	ptr, ok := typ.(*typeinfo.PointerType)
	if !ok || ptr == nil {
		return
	}
	if c.typeContainsReference(ptr.Inner) {
		c.ctx.Diagnostics.Add(
			diagnostics.NewError("owning heap types cannot contain references").
				WithCode(diagnostics.ErrInvalidType).
				WithPrimaryLabel(&loc, "heap storage cannot contain `&T` or `&mut T`"),
		)
	}
}

func (c *checker) typeContainsReference(typ typeinfo.Type) bool {
	return c.typeContainsReferenceSeen(typ, map[typeinfo.Type]struct{}{}, map[string]struct{}{})
}

func (c *checker) typeContainsReferenceSeen(typ typeinfo.Type, seen map[typeinfo.Type]struct{}, seenNamed map[string]struct{}) bool {
	if typ == nil {
		return false
	}
	if named, ok := typ.(*typeinfo.NamedType); ok && named != nil {
		key := named.ModuleKey + "::" + named.Name
		if _, ok := seenNamed[key]; ok {
			return false
		}
		seenNamed[key] = struct{}{}
	}
	if _, ok := seen[typ]; ok {
		return false
	}
	seen[typ] = struct{}{}
	base := c.underlying(typ)
	if base != nil {
		if base != typ {
			if _, ok := seen[base]; ok {
				return false
			}
			seen[base] = struct{}{}
		}
	}
	switch t := base.(type) {
	case *typeinfo.RefType:
		return true
	case *typeinfo.PointerType:
		return c.typeContainsReferenceSeen(t.Inner, seen, seenNamed)
	case *typeinfo.RawPtrType:
		return false
	case *typeinfo.OptionalType:
		return c.typeContainsReferenceSeen(t.Inner, seen, seenNamed)
	case *typeinfo.ErrorUnionType:
		return c.typeContainsReferenceSeen(t.Error, seen, seenNamed) || c.typeContainsReferenceSeen(t.Value, seen, seenNamed)
	case *typeinfo.ArrayType:
		return c.typeContainsReferenceSeen(t.Inner, seen, seenNamed)
	case *typeinfo.SliceType:
		return c.typeContainsReferenceSeen(t.Inner, seen, seenNamed)
	case *typeinfo.TupleType:
		for _, elem := range t.Elems {
			if c.typeContainsReferenceSeen(elem, seen, seenNamed) {
				return true
			}
		}
		return false
	case *typeinfo.StructType:
		for _, field := range t.OrderedFields {
			if field != nil && c.typeContainsReferenceSeen(field.Type, seen, seenNamed) {
				return true
			}
		}
		return false
	case *typeinfo.UnionType:
		for _, member := range t.Members {
			if c.typeContainsReferenceSeen(member, seen, seenNamed) {
				return true
			}
		}
		return false
	case *typeinfo.InterfaceType, *typeinfo.BuiltinType, *typeinfo.StringType, *typeinfo.EnumType, *typeinfo.ErrorSetType:
		return false
	default:
		return false
	}
}

func (c *checker) checkFuncDecl(d *ast.FuncDecl) {
	if d == nil {
		return
	}
	ownerMod, ownerDecl := c.ownerTypeDeclForFunc(c.mod, d)
	if ownerDecl != nil && len(ownerDecl.TypeParams) > 0 {
		c.pushTypeParams(ownerMod, ownerDecl, ownerDecl.TypeParams)
		defer c.popTypeParams()
	}
	typeParams := c.pushTypeParams(c.mod, d, d.TypeParams)
	defer c.popTypeParams()
	prevGenericFunc := c.currentGenericFunc
	prevGenericRequirements := c.currentGenericRequirements
	if len(d.TypeParams) > 0 || (ownerDecl != nil && len(ownerDecl.TypeParams) > 0) {
		c.currentGenericFunc = c.declSymbol(d.Name)
		if c.currentGenericFunc == nil && c.mod != nil && c.mod.Bindings != nil {
			c.currentGenericFunc = c.mod.Bindings.FunctionSymbols[d]
		}
		c.currentGenericRequirements = nil
	}
	defer func() {
		if c.currentGenericFunc != nil {
			c.info.BindGenericRequirements(c.currentGenericFunc, append([]*typeinfo.GenericRequirement(nil), c.currentGenericRequirements...))
		}
		c.currentGenericFunc = prevGenericFunc
		c.currentGenericRequirements = prevGenericRequirements
	}()
	for i, param := range d.TypeParams {
		if i >= len(typeParams) {
			continue
		}
		c.info.BindNode(param.Name, typeParams[i])
	}
	var selfType typeinfo.Type
	funcScope := newRefineScope(nil)
	var ownerType typeinfo.Type
	if d.Receiver != nil {
		recvType := c.syntaxType(c.mod, d.Receiver.Type)
		if base, ok := typeinfo.ReceiverBaseNamedType(recvType); ok {
			selfType = base
		}
		if d.Receiver.Type != nil {
			c.info.BindNode(d.Receiver.Type, recvType)
		}
		// No base-type environment: locals/params are typed via Bindings+Types.
		if recvType == nil {
			recvType = typeinfo.UnknownType{}
		}
		if named, ok := typeinfo.ReceiverBaseNamedType(recvType); ok {
			if owner := c.findModuleForType(named); owner != nil && owner.Key != c.mod.Key {
				loc := d.Receiver.Type.Loc()
				c.ctx.Diagnostics.Add(
					diagnostics.NewError("cross-module method declarations are not allowed").
						WithCode(diagnostics.ErrInvalidOperation).
						WithPrimaryLabel(&loc, "declare methods in the same module as their receiver type"),
				)
			}
		}
		c.bindDeclSymbol(d.Receiver.Name, recvType)
		c.info.BindNode(d.Receiver, recvType)
		if d.OwnerType != nil && selfType != nil {
			ownerType = selfType
			c.info.BindNode(d.OwnerType, ownerType)
		}
	}
	if d.Receiver == nil && d.OwnerType != nil {
		ownerType = c.syntaxType(c.mod, d.OwnerType)
		c.info.BindNode(d.OwnerType, ownerType)
		selfType = ownerType
	}
	if d.IsStatic && ownerType != nil {
		if named, ok := ownerType.(*typeinfo.NamedType); ok {
			if owner := c.findModuleForType(named); owner != nil && owner.Key != c.mod.Key {
				loc := d.OwnerType.Loc()
				c.ctx.Diagnostics.Add(
					diagnostics.NewError("cross-module method declarations are not allowed").
						WithCode(diagnostics.ErrInvalidOperation).
						WithPrimaryLabel(&loc, "declare static methods in the same module as their owner type"),
				)
			}
		}
	}
	for _, param := range d.Params {
		paramType := c.instantiateSelfType(c.syntaxType(c.mod, param.Type), selfType)
		if param.Type != nil {
			c.info.BindNode(param.Type, paramType)
		}
		if paramType == nil {
			paramType = typeinfo.UnknownType{}
		}
		c.bindDeclSymbol(param.Name, paramType)
		if sym := c.declSymbol(param.Name); sym != nil {
			if param.IsMut {
				sym.Flags |= semmeta.FlagMutable
			}
			if param.IsComptime {
				sym.Flags |= semmeta.FlagComptime
			}
		}
		// No base-type environment: locals/params are typed via Bindings+Types.
	}
	prevResult := c.currentResult
	c.currentResult = c.instantiateSelfType(c.funcResultType(c.mod, d), selfType)
	if d.Result != nil {
		c.info.BindNode(d.Result, c.currentResult)
	}
	if d.Body != nil {
		c.checkStmt(funcScope, d.Body)
	}
	c.currentResult = prevResult
}

func (c *checker) typeOfExpr(scope *refineScope, expr ast.Expr, expected typeinfo.Type) typeinfo.Type {
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
		// String literals have dedicated type `str`.
		typ := &typeinfo.StringType{}
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
		return c.typeOfCall(scope, e, expected)
	case *ast.SelectorExpr:
		return c.typeOfSelector(scope, e)
	case *ast.CastExpr:
		return c.typeOfCast(scope, e)
	case *ast.IsExpr:
		return c.typeOfIs(scope, e)
	case *ast.MatchExpr:
		return c.typeOfMatchExpr(scope, e, expected)
	case *ast.CompositeLit:
		return c.typeOfComposite(scope, e, expected)
	case *ast.IndexExpr:
		return c.typeOfIndex(scope, e)
	default:
		return typeinfo.UnknownType{}
	}
}

func (c *checker) typeOfIdent(scope *refineScope, ident *ast.Ident, expected typeinfo.Type) typeinfo.Type {
	if ident == nil {
		return typeinfo.UnknownType{}
	}
	res := c.lookupResolution(ident)
	if res == nil {
		return typeinfo.InvalidType{}
	}
	if res.Kind == binding.ResolutionSymbol && res.Symbol != nil {
		if scope != nil && len(ident.Path) == 1 {
			if typ, ok := scope.Lookup(res.Symbol); ok && typ != nil {
				c.info.BindNode(ident, typ)
				return typ
			}
		}
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
		if fnType, ok := typ.(*typeinfo.FuncType); ok && len(ident.TypeArgs) > 0 {
			fnDecl, _ := res.Symbol.Node.(*ast.FuncDecl)
			if fnDecl != nil && fnDecl.OwnerType != nil {
				symMod := c.findModuleForSymbol(res.Symbol)
				if symMod == nil {
					symMod = c.mod
				}
				ownerMod, ownerDecl := c.ownerTypeDeclForFunc(symMod, fnDecl)
				if ownerMod == nil {
					ownerMod = c.mod
				}
				if ownerDecl != nil && len(ownerDecl.TypeParams) > 0 {
					if len(ident.TypeArgs) != len(ownerDecl.TypeParams) {
						loc := ident.Location
						c.ctx.Diagnostics.Add(
							diagnostics.NewError(fmt.Sprintf("expected %d owner type arguments, got %d", len(ownerDecl.TypeParams), len(ident.TypeArgs))).
								WithCode(diagnostics.ErrInvalidType).
								WithPrimaryLabel(&loc, "owner type argument count does not match"),
						)
					} else {
						typeArgs := make([]typeinfo.Type, 0, len(ident.TypeArgs))
						for _, arg := range ident.TypeArgs {
							argType := c.typeFromSyntax(ownerMod, arg)
							if arg != nil {
								c.info.BindNode(arg, argType)
							}
							typeArgs = append(typeArgs, argType)
						}
						c.checkTypeParamDeclConstraintsAt(ident.Location, ownerMod, ownerDecl, typeArgs)
						ownerNamed := &typeinfo.NamedType{
							ModuleKey: ownerMod.Key,
							Name:      ownerDecl.Name.Text(),
							Decl:      ownerDecl,
							TypeArgs:  typeArgs,
						}
						typ = c.instantiateOwnerMethodType(ownerNamed, res.Symbol, fnType)
					}
				}
			}
		}
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

func (c *checker) typeOfPrefix(scope *refineScope, expr *ast.PrefixExpr, expected typeinfo.Type) typeinfo.Type {
	if expr != nil && expr.Op == "unsafe" {
		c.unsafeDepth++
		unsafeType := c.typeOfExpr(scope, expr.Right, expected)
		c.unsafeDepth--
		c.info.BindNode(expr, unsafeType)
		return unsafeType
	}
	right := c.typeOfExpr(scope, expr.Right, expected)
	switch expr.Op {
	case "copy":
		loc := expr.Loc()
		c.ctx.Diagnostics.Add(
			diagnostics.NewError("`copy` is not yet implemented").
				WithCode(diagnostics.ErrInvalidCopy).
				WithPrimaryLabel(&loc, "deep clone support has not been implemented yet"),
		)
		c.info.BindNode(expr, right)
		return right
	case "comptime":
		c.requireConstExpr(scope, expr.Right, "`comptime` expression must be compile-time evaluable")
		c.info.BindNode(expr, right)
		return right
	case "&":
		if _, ok := c.underlying(expected).(*typeinfo.RawPtrType); ok {
			if c.unsafeDepth == 0 {
				loc := expr.Location
				c.ctx.Diagnostics.Add(
					diagnostics.NewError("raw address operator requires unsafe block").
						WithCode(diagnostics.ErrInvalidOperation).
						WithPrimaryLabel(&loc, "wrap this address operation in `unsafe { ... }`"),
				)
			}
			typ := &typeinfo.RawPtrType{Const: true, Inner: right}
			c.info.BindNode(expr, typ)
			return typ
		}
		typ := &typeinfo.RefType{Inner: right}
		c.info.BindNode(expr, typ)
		return typ
	case "&mut":
		addressable, mutable := c.exprAccess(scope, expr.Right)
		if !addressable || !mutable {
			loc := expr.Location
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("cannot create mutable reference from immutable value").
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(&loc, "`&mut` requires mutable, addressable access"),
			)
		}
		if raw, ok := c.underlying(expected).(*typeinfo.RawPtrType); ok {
			if c.unsafeDepth == 0 {
				loc := expr.Location
				c.ctx.Diagnostics.Add(
					diagnostics.NewError("raw address operator requires unsafe block").
						WithCode(diagnostics.ErrInvalidOperation).
						WithPrimaryLabel(&loc, "wrap this address operation in `unsafe { ... }`"),
				)
			}
			typ := &typeinfo.RawPtrType{Const: raw.Const, Inner: right}
			c.info.BindNode(expr, typ)
			return typ
		}
		typ := &typeinfo.RefType{Mutable: true, Inner: right}
		c.info.BindNode(expr, typ)
		return typ
	case "*":
		switch ptr := right.(type) {
		case *typeinfo.RefType:
			c.info.BindNode(expr, ptr.Inner)
			return ptr.Inner
		case *typeinfo.RawPtrType:
			if c.unsafeDepth == 0 {
				loc := expr.Location
				c.ctx.Diagnostics.Add(
					diagnostics.NewError("raw pointer dereference requires unsafe block").
						WithCode(diagnostics.ErrInvalidOperation).
						WithPrimaryLabel(&loc, "wrap this dereference in `unsafe { ... }`"),
				)
			}
			if ptr.Inner == nil {
				loc := expr.Location
				c.ctx.Diagnostics.Add(
					diagnostics.NewError("cannot dereference untyped raw pointer").
						WithCode(diagnostics.ErrInvalidOperation).
						WithPrimaryLabel(&loc, "cast this raw pointer to a typed pointer first"),
				)
				return typeinfo.InvalidType{}
			}
			c.info.BindNode(expr, ptr.Inner)
			return ptr.Inner
		case *typeinfo.PointerType:
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

func (c *checker) typeOfMatchExpr(scope *refineScope, expr *ast.MatchExpr, expected typeinfo.Type) typeinfo.Type {
	if expr == nil {
		return typeinfo.UnknownType{}
	}
	valueType := c.typeOfExpr(scope, expr.Value, nil)
	hasWildcard := false
	var resultType typeinfo.Type
	seenValueArm := false

	for _, arm := range expr.Arms {
		if arm == nil {
			continue
		}
		armScope := scope
		if arm.Wildcard {
			hasWildcard = true
		} else if arm.TypePattern != nil {
			target := c.typeFromSyntax(c.mod, arm.TypePattern)
			c.info.BindNode(arm.TypePattern, target)
			_ = c.typeOfIs(scope, &ast.IsExpr{Left: expr.Value, Type: arm.TypePattern, Location: arm.Location})
			armScope = c.narrowedMatchTypeArmScope(scope, expr.Value, target)
		} else {
			patternType := c.typeOfExpr(scope, arm.Pattern, valueType)
			if !typeinfo.Assignable(valueType, patternType) && !typeinfo.Assignable(patternType, valueType) {
				c.reportTypeMismatch(arm.Pattern.Loc(), valueType, patternType)
			}
		}
		armType, diverges := c.blockValueType(armScope, arm.Body, expected)
		if diverges || typeinfo.IsInvalid(armType) || typeinfo.IsUnknown(armType) {
			continue
		}
		if expected != nil {
			if c.checkAssignable(arm.Body.Loc(), expected, armType) {
				resultType = expected
				seenValueArm = true
			}
			continue
		}
		if !seenValueArm {
			resultType = armType
			seenValueArm = true
			continue
		}
		if unified := c.unifyMatchArmTypes(resultType, armType); unified != nil {
			resultType = unified
			continue
		}
		c.reportTypeMismatch(arm.Body.Loc(), resultType, armType)
		resultType = typeinfo.InvalidType{}
	}

	if !hasWildcard {
		loc := expr.Location
		c.ctx.Diagnostics.Add(
			diagnostics.NewError("match expression requires a wildcard arm for now").
				WithCode(diagnostics.ErrNonExhaustiveMatch).
				WithPrimaryLabel(&loc, "add `_ => { ... }` to handle the remaining cases"),
		)
		if !seenValueArm {
			return typeinfo.InvalidType{}
		}
	}
	if seenValueArm {
		c.info.BindNode(expr, resultType)
		return resultType
	}
	if expected != nil {
		c.info.BindNode(expr, expected)
		return expected
	}
	return typeinfo.UnknownType{}
}

func (c *checker) blockValueType(scope *refineScope, block *ast.BlockStmt, expected typeinfo.Type) (typeinfo.Type, bool) {
	if block == nil || len(block.Stmts) == 0 {
		loc := source.Location{}
		if block != nil {
			loc = block.Location
		}
		c.ctx.Diagnostics.Add(
			diagnostics.NewError("match arm must yield a value or exit").
				WithCode(diagnostics.ErrInvalidExpression).
				WithPrimaryLabel(&loc, "add a final expression or terminate this arm"),
		)
		return typeinfo.InvalidType{}, false
	}
	for i := 0; i < len(block.Stmts)-1; i++ {
		c.checkStmt(scope, block.Stmts[i])
	}
	last := block.Stmts[len(block.Stmts)-1]
	if stmtDefinitelyExits(last) {
		c.checkStmt(scope, last)
		return nil, true
	}
	if exprStmt, ok := last.(*ast.ExprStmt); ok {
		return c.typeOfExpr(scope, exprStmt.Value, expected), false
	}
	c.checkStmt(scope, last)
	loc := last.Loc()
	c.ctx.Diagnostics.Add(
		diagnostics.NewError("match arm must end with a value expression or exit").
			WithCode(diagnostics.ErrInvalidExpression).
			WithPrimaryLabel(&loc, "this arm does not produce a value"),
	)
	return typeinfo.InvalidType{}, false
}

func (c *checker) unifyMatchArmTypes(left, right typeinfo.Type) typeinfo.Type {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	if typeinfo.Equal(left, right) {
		return left
	}
	if common := typeinfo.CommonNumericType(left, right); common != nil {
		return common
	}
	if typeinfo.Assignable(left, right) {
		return left
	}
	if typeinfo.Assignable(right, left) {
		return right
	}
	return nil
}

func (c *checker) typeOfBinary(scope *refineScope, expr *ast.BinaryExpr) typeinfo.Type {
	left := c.typeOfExpr(scope, expr.Left, nil)
	right := c.typeOfExpr(scope, expr.Right, left)
	if c.isUnionValue(left) || c.isUnionValue(right) {
		loc := expr.Location
		c.ctx.Diagnostics.Add(
			diagnostics.NewError("union values do not support direct binary operations").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(&loc, "extract a concrete union member before using this operator"),
		)
		return typeinfo.InvalidType{}
	}
	if result, handled := c.binaryResult(expr.Op, left, right); handled {
		if result != nil {
			c.info.BindNode(expr, result)
			return result
		}
		if c.deferGenericBinaryRequirement(expr, left, right) {
			typ := typeinfo.UnknownType{}
			c.info.BindNode(expr, typ)
			return typ
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

func (c *checker) binaryResult(op string, left, right typeinfo.Type) (typeinfo.Type, bool) {
	switch op {
	case "+", "-", "*", "/", "%":
		if result := typeinfo.CommonNumericType(left, right); result != nil {
			return result, true
		}
		return nil, true
	case "==", "!=":
		if typeinfo.Assignable(left, right) || typeinfo.Assignable(right, left) || typeinfo.CommonNumericType(left, right) != nil {
			return &typeinfo.BuiltinType{Name: "bool"}, true
		}
		return nil, true
	case "<", "<=", ">", ">=":
		if typeinfo.CommonNumericType(left, right) != nil {
			return &typeinfo.BuiltinType{Name: "bool"}, true
		}
		return nil, true
	case "&&", "||":
		if typeinfo.IsBuiltinNamed(left, "bool") && typeinfo.IsBuiltinNamed(right, "bool") {
			return &typeinfo.BuiltinType{Name: "bool"}, true
		}
		return nil, true
	case "??":
		if opt, ok := left.(*typeinfo.OptionalType); ok {
			if typeinfo.Assignable(opt.Inner, right) {
				return opt.Inner, true
			}
			return nil, true
		}
		return nil, true
	default:
		return nil, false
	}
}

func (c *checker) deferGenericBinaryRequirement(expr *ast.BinaryExpr, left, right typeinfo.Type) bool {
	if expr == nil || c.currentGenericFunc == nil || !c.containsTypeParam(left) && !c.containsTypeParam(right) {
		return false
	}
	c.currentGenericRequirements = append(c.currentGenericRequirements, &typeinfo.GenericRequirement{
		Kind:     typeinfo.GenericRequirementBinaryOp,
		Location: expr.Location,
		Op:       expr.Op,
		Left:     left,
		Right:    right,
	})
	return true
}

func (c *checker) containsTypeParam(typ typeinfo.Type) bool {
	switch t := typ.(type) {
	case nil:
		return false
	case *typeinfo.TypeParam:
		return true
	case *typeinfo.PointerType:
		return c.containsTypeParam(t.Inner)
	case *typeinfo.RefType:
		return c.containsTypeParam(t.Inner)
	case *typeinfo.RawPtrType:
		return c.containsTypeParam(t.Inner)
	case *typeinfo.OptionalType:
		return c.containsTypeParam(t.Inner)
	case *typeinfo.ApproxType:
		return c.containsTypeParam(t.Inner)
	case *typeinfo.ErrorUnionType:
		return c.containsTypeParam(t.Error) || c.containsTypeParam(t.Value)
	case *typeinfo.ArrayType:
		return c.containsTypeParam(t.Inner)
	case *typeinfo.SliceType:
		return c.containsTypeParam(t.Inner)
	case *typeinfo.TupleType:
		return slices.ContainsFunc(t.Elems, c.containsTypeParam)
	case *typeinfo.IntersectionType:
		return slices.ContainsFunc(t.Members, c.containsTypeParam)
	default:
		return false
	}
}

func (c *checker) isUnionValue(typ typeinfo.Type) bool {
	if typ == nil {
		return false
	}
	_, ok := c.underlying(typ).(*typeinfo.UnionType)
	return ok
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
	family, bits, ok := typeinfo.NumericInfo(target)
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

func (c *checker) typeOfPostfix(scope *refineScope, expr *ast.PostfixExpr) typeinfo.Type {
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

func (c *checker) typeOfCall(scope *refineScope, expr *ast.CallExpr, expected typeinfo.Type) typeinfo.Type {
	if c.isForeignLenCall(expr.Callee) {
		return c.typeOfBuiltinLen(scope, expr)
	}

	if selector, ok := expr.Callee.(*ast.SelectorExpr); ok {
		if typ, handled := c.typeOfMethodCall(scope, expr, selector, expected); handled {
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
	instantiated, argTypes, bindings, invalid := c.instantiateCallFuncType(scope, expr, expr.Callee, fnType, expected)
	if instantiated == nil {
		instantiated = fnType
	}
	if invalid {
		typ := typeinfo.InvalidType{}
		c.info.BindNode(expr, typ)
		return typ
	}
	callInvalid := c.typecheckCallArgs(scope, expr, instantiated, argTypes)
	if !callInvalid && c.checkInstantiatedGenericRequirements(expr, expr.Callee, bindings) {
		callInvalid = true
	}
	if callInvalid {
		typ := typeinfo.InvalidType{}
		c.info.BindNode(expr, typ)
		return typ
	}
	c.info.BindNode(expr, instantiated.Result)
	return instantiated.Result
}

func (c *checker) typeOfBuiltinLen(scope *refineScope, expr *ast.CallExpr) typeinfo.Type {
	result := &typeinfo.BuiltinType{Name: "usize"}
	if expr == nil {
		return result
	}
	// Keep callee type info available for tooling hover (e.g. on `len`).
	if expr.Callee != nil {
		_ = c.typeOfExpr(scope, expr.Callee, nil)
	}
	if len(expr.Args) != 1 {
		c.reportWrongArgCount(expr.Location, 1, len(expr.Args))
	}
	if len(expr.Args) > 0 {
		argType := c.typeOfExpr(scope, expr.Args[0], nil)
		switch c.underlying(argType).(type) {
		case *typeinfo.ArrayType, *typeinfo.SliceType:
			// ok
		default:
			loc := expr.Args[0].Loc()
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("len expects an array or slice argument").
					WithCode(diagnostics.ErrInvalidType).
					WithPrimaryLabel(&loc, "this value is not an array or slice"),
			)
		}
	}
	c.info.BindNode(expr, result)
	return result
}

func (c *checker) isForeignLenCall(callee ast.Expr) bool {
	res := c.lookupResolution(callee)
	if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
		return false
	}
	if res.Symbol.Name != "len" {
		return false
	}
	fn, ok := res.Symbol.Node.(*ast.FuncDecl)
	return ok && fn != nil && fn.IsExtern
}

func (c *checker) typeOfCatch(scope *refineScope, expr *ast.CatchExpr) typeinfo.Type {
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
		if expr.Payload != nil {
			payloadType := errUnion.Error
			if payloadType == nil {
				payloadType = typeinfo.UnknownType{}
			}
			c.bindDeclSymbol(expr.Payload, payloadType)
			// No base-type environment: locals/params are typed via Bindings+Types.
		}
		c.checkStmt(scope, expr.Handler)
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

func (c *checker) typeOfMethodCall(scope *refineScope, call *ast.CallExpr, selector *ast.SelectorExpr, expected typeinfo.Type) (typeinfo.Type, bool) {
	receiverType := c.typeOfExpr(scope, selector.Left, nil)
	if typeinfo.IsInvalid(receiverType) || typeinfo.IsUnknown(receiverType) {
		return typeinfo.InvalidType{}, true
	}

	if field := c.lookupStructField(receiverType, selector.Name.Text()); field != nil {
		return nil, false
	}

	if iface, ok := c.interfaceView(receiverType); ok {
		method := iface.Methods[selector.Name.Text()]
		if method == nil {
			c.reportMethodNotFound(selector.Location, receiverType, selector.Name.Text())
			return typeinfo.InvalidType{}, true
		}
		if methodReceiver := typeinfo.ApplyReceiverShape(receiverType, iface.MethodReceivers[selector.Name.Text()]); methodReceiver != nil {
			c.info.BindMethodReceiver(call, methodReceiver)
			c.info.BindMethodReceiver(selector, methodReceiver)
		}
		instantiated, argTypes, bindings, invalid := c.instantiateCallFuncType(scope, call, selector, method, expected)
		if instantiated == nil {
			instantiated = method
		}
		if instantiated.IsUnsafe && c.unsafeDepth == 0 {
			loc := selector.Location
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("unsafe function call requires unsafe block").
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(&loc, "wrap this call in `unsafe { ... }`"),
			)
		}
		if invalid {
			typ := typeinfo.InvalidType{}
			c.info.BindNode(call, typ)
			return typ, true
		}
		callInvalid := c.typecheckCallArgs(scope, call, instantiated, argTypes)
		if !callInvalid && c.checkInstantiatedGenericRequirements(call, selector, bindings) {
			callInvalid = true
		}
		if callInvalid {
			typ := typeinfo.InvalidType{}
			c.info.BindNode(call, typ)
			return typ, true
		}
		c.info.BindNode(call, instantiated.Result)
		return instantiated.Result, true
	}

	addressable, mutable := c.exprAccess(scope, selector.Left)
	methodSym, methodType, methodReceiver := c.lookupMethodDetailed(receiverType, selector.Name.Text(), addressable, mutable)
	if methodType == nil {
		if c.canHaveMethods(receiverType) {
			// If the method exists but only on a mutable receiver and the variable
			// is immutable, emit a more helpful diagnostic instead of the
			// generic "has no method" message.
			if !mutable && addressable {
				if _, mutMethodType := c.lookupMethod(receiverType, selector.Name.Text(), true, true); mutMethodType != nil {
					loc := selector.Location
					c.ctx.Diagnostics.Add(
						diagnostics.NewError(fmt.Sprintf("cannot call method %q on immutable %s", selector.Name.Text(), receiverType.String())).
							WithCode(diagnostics.ErrMethodNotFound).
							WithPrimaryLabel(&loc, "this method requires mutable receiver access").
							WithNote("declare the variable with `let mut` to allow mutable method calls"),
					)
					return typeinfo.InvalidType{}, true
				}
			}
			c.reportMethodNotFound(selector.Location, receiverType, selector.Name.Text())
			return typeinfo.InvalidType{}, true
		}
		return nil, false
	}
	if methodReceiver != nil {
		c.info.BindMethodReceiver(call, methodReceiver)
		c.info.BindMethodReceiver(selector, methodReceiver)
	}
	c.bindNodeSymbolResolution(selector, methodSym)
	instantiated, argTypes, bindings, invalid := c.instantiateCallFuncType(scope, call, selector, methodType, expected)
	if instantiated == nil {
		instantiated = methodType
	}
	if instantiated.IsUnsafe && c.unsafeDepth == 0 {
		loc := selector.Location
		c.ctx.Diagnostics.Add(
			diagnostics.NewError("unsafe function call requires unsafe block").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(&loc, "wrap this call in `unsafe { ... }`"),
		)
	}
	if invalid {
		typ := typeinfo.InvalidType{}
		c.info.BindNode(call, typ)
		return typ, true
	}
	callInvalid := c.typecheckCallArgs(scope, call, instantiated, argTypes)
	if !callInvalid && c.checkInstantiatedGenericRequirements(call, selector, bindings) {
		callInvalid = true
	}
	if callInvalid {
		typ := typeinfo.InvalidType{}
		c.info.BindNode(call, typ)
		return typ, true
	}
	c.info.BindNode(call, instantiated.Result)
	return instantiated.Result, true
}

func (c *checker) typecheckCallArgs(scope *refineScope, call *ast.CallExpr, fnType *typeinfo.FuncType, argTypes []typeinfo.Type) bool {
	invalid := false
	if fnType == nil {
		return false
	}
	if len(call.Args) != len(fnType.Params) {
		c.reportWrongArgCount(call.Location, len(fnType.Params), len(call.Args))
		invalid = true
	}
	for i, arg := range call.Args {
		var expected typeinfo.Type
		if i < len(fnType.Params) {
			expected = fnType.Params[i].Type
		}
		argType := c.lookupOrTypeExpr(scope, arg, expected, argTypes, i)
		if expected != nil {
			if !c.checkReferenceArg(scope, arg, expected, argType) {
				invalid = true
				continue
			}
			if i < len(fnType.Params) && fnType.Params[i].Flags.Mutable() {
				addressable, mutable := c.exprAccess(scope, arg)
				if !addressable || !mutable {
					loc := arg.Loc()
					c.ctx.Diagnostics.Add(
						diagnostics.NewError("mutable parameter requires mutable argument binding").
							WithCode(diagnostics.ErrTypeMismatch).
							WithPrimaryLabel(&loc, "pass a mutable binding here"),
					)
					invalid = true
					continue
				}
			}
			if !c.checkAssignable(arg.Loc(), expected, argType) {
				invalid = true
			}
		}
		if i < len(fnType.Params) && fnType.Params[i].Flags.Comptime() {
			c.requireConstExpr(scope, arg, "argument to comptime parameter must be compile-time evaluable")
		}
	}
	return invalid
}

func (c *checker) lookupOrTypeExpr(scope *refineScope, expr ast.Expr, expected typeinfo.Type, precomputed []typeinfo.Type, index int) typeinfo.Type {
	if expected == nil && index >= 0 && index < len(precomputed) && precomputed[index] != nil {
		return precomputed[index]
	}
	return c.typeOfExpr(scope, expr, expected)
}

func (c *checker) instantiateCallFuncType(scope *refineScope, call *ast.CallExpr, callee ast.Node, fnType *typeinfo.FuncType, expected typeinfo.Type) (*typeinfo.FuncType, []typeinfo.Type, map[*typeinfo.TypeParam]typeinfo.Type, bool) {
	if fnType == nil {
		return nil, nil, nil, false
	}
	genericParams := c.collectCallTypeParams(fnType)
	if len(genericParams) == 0 {
		if len(call.TypeArgs) > 0 {
			c.bindCallTypeArgs(call)
			loc := call.Location
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("type arguments are only valid on generic functions").
					WithCode(diagnostics.ErrInvalidType).
					WithPrimaryLabel(&loc, "this call target is not generic"),
			)
		}
		if callee != nil {
			c.info.BindNode(callee, fnType)
		}
		return fnType, nil, nil, false
	}

	bindings := make(map[*typeinfo.TypeParam]typeinfo.Type, len(genericParams))
	if len(call.TypeArgs) > 0 {
		c.bindExplicitTypeArgs(call, fnType, bindings)
	}

	argTypes := make([]typeinfo.Type, 0, len(call.Args))
	inferFromArgs := len(call.TypeArgs) == 0 || len(call.TypeArgs) < len(fnType.TypeParams) || len(genericParams) > len(fnType.TypeParams)
	for i, arg := range call.Args {
		argType := c.typeOfExpr(scope, arg, nil)
		argTypes = append(argTypes, argType)
		if inferFromArgs && i < len(fnType.Params) {
			c.inferTypeParamBindings(fnType.Params[i].Type, argType, bindings)
		}
	}
	if inferFromArgs && expected != nil {
		expectedBindings := make(map[*typeinfo.TypeParam]typeinfo.Type, len(genericParams))
		c.inferTypeParamBindings(fnType.Result, expected, expectedBindings)
		for _, param := range genericParams {
			if bindings[param] != nil {
				continue
			}
			if bound := c.lookupTypeParamBinding(expectedBindings, param); bound != nil {
				bindings[param] = bound
			}
		}
	}

	missing := false
	for _, param := range genericParams {
		if typeinfo.IsInvalid(bindings[param]) {
			missing = true
			loc := call.Location
			c.ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("conflicting type inference for %s", param.Name)).
					WithCode(diagnostics.ErrInvalidType).
					WithPrimaryLabel(&loc, "arguments infer incompatible concrete types"),
			)
			bindings[param] = typeinfo.UnknownType{}
			continue
		}
		if bindings[param] != nil {
			continue
		}
		missing = true
		loc := call.Location
		c.ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("cannot infer type argument for %s", param.Name)).
				WithCode(diagnostics.ErrInvalidType).
				WithPrimaryLabel(&loc, "add an explicit type argument here"),
		)
		bindings[param] = typeinfo.UnknownType{}
	}
	c.checkCallTypeParamConstraints(call, genericParams, bindings)

	instantiated := &typeinfo.FuncType{
		IsUnsafe: fnType.IsUnsafe,
		Params:   make([]typeinfo.ParamSpec, 0, len(fnType.Params)),
		Result:   c.substituteTypeParams(fnType.Result, bindings),
	}
	for _, param := range fnType.Params {
		instantiated.Params = append(instantiated.Params, typeinfo.ParamSpec{
			Name:  param.Name,
			Type:  c.substituteTypeParams(param.Type, bindings),
			Flags: param.Flags,
		})
	}
	if callee != nil {
		c.info.BindNode(callee, instantiated)
	}
	if missing {
		return instantiated, argTypes, bindings, false
	}
	return instantiated, argTypes, bindings, false
}

func (c *checker) collectCallTypeParams(fnType *typeinfo.FuncType) []*typeinfo.TypeParam {
	if fnType == nil {
		return nil
	}
	params := make([]*typeinfo.TypeParam, 0, len(fnType.TypeParams))
	add := func(param *typeinfo.TypeParam) {
		if param == nil {
			return
		}
		for _, existing := range params {
			if typeinfo.Equal(existing, param) {
				return
			}
		}
		params = append(params, param)
	}
	for _, param := range fnType.TypeParams {
		add(param)
	}
	for _, param := range fnType.Params {
		c.collectTypeParams(param.Type, add)
	}
	c.collectTypeParams(fnType.Result, add)
	return params
}

func (c *checker) collectTypeParams(typ typeinfo.Type, visit func(*typeinfo.TypeParam)) {
	switch t := typ.(type) {
	case nil:
		return
	case *typeinfo.TypeParam:
		visit(t)
	case *typeinfo.PointerType:
		c.collectTypeParams(t.Inner, visit)
	case *typeinfo.RefType:
		c.collectTypeParams(t.Inner, visit)
	case *typeinfo.RawPtrType:
		c.collectTypeParams(t.Inner, visit)
	case *typeinfo.OptionalType:
		c.collectTypeParams(t.Inner, visit)
	case *typeinfo.ApproxType:
		c.collectTypeParams(t.Inner, visit)
	case *typeinfo.ErrorUnionType:
		c.collectTypeParams(t.Error, visit)
		c.collectTypeParams(t.Value, visit)
	case *typeinfo.ArrayType:
		c.collectTypeParams(t.Inner, visit)
	case *typeinfo.SliceType:
		c.collectTypeParams(t.Inner, visit)
	case *typeinfo.TupleType:
		for _, elem := range t.Elems {
			c.collectTypeParams(elem, visit)
		}
	case *typeinfo.NamedType:
		for _, arg := range t.TypeArgs {
			c.collectTypeParams(arg, visit)
		}
	case *typeinfo.StructType:
		for _, field := range t.OrderedFields {
			if field == nil {
				continue
			}
			c.collectTypeParams(field.Type, visit)
		}
	case *typeinfo.InterfaceType:
		for _, method := range t.OrderedMethods {
			if method == nil || method.Type == nil {
				continue
			}
			c.collectTypeParams(method.Type, visit)
		}
	case *typeinfo.UnionType:
		for _, member := range t.Members {
			c.collectTypeParams(member, visit)
		}
	case *typeinfo.IntersectionType:
		for _, member := range t.Members {
			c.collectTypeParams(member, visit)
		}
	case *typeinfo.FuncType:
		for _, param := range t.TypeParams {
			visit(param)
		}
		for _, param := range t.Params {
			c.collectTypeParams(param.Type, visit)
		}
		c.collectTypeParams(t.Result, visit)
	}
}

func (c *checker) bindCallTypeArgs(call *ast.CallExpr) {
	for _, arg := range call.TypeArgs {
		if arg != nil {
			c.info.BindNode(arg, c.typeFromSyntax(c.mod, arg))
		}
	}
}

func (c *checker) bindExplicitTypeArgs(call *ast.CallExpr, fnType *typeinfo.FuncType, bindings map[*typeinfo.TypeParam]typeinfo.Type) {
	c.bindCallTypeArgs(call)
	if len(call.TypeArgs) != len(fnType.TypeParams) {
		loc := call.Location
		c.ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("expected %d type arguments, got %d", len(fnType.TypeParams), len(call.TypeArgs))).
				WithCode(diagnostics.ErrInvalidType).
				WithPrimaryLabel(&loc, "type argument count does not match"),
		)
	}
	limit := min(len(call.TypeArgs), len(fnType.TypeParams))
	for i := range limit {
		bindings[fnType.TypeParams[i]] = c.typeFromSyntax(c.mod, call.TypeArgs[i])
	}
}

func (c *checker) inferTypeParamBindings(pattern, actual typeinfo.Type, bindings map[*typeinfo.TypeParam]typeinfo.Type) {
	switch p := pattern.(type) {
	case nil:
		return
	case *typeinfo.TypeParam:
		if actual == nil {
			return
		}
		if bound := bindings[p]; bound != nil {
			if !typeinfo.Equal(bound, actual) && !typeinfo.IsUnknown(bound) {
				bindings[p] = typeinfo.InvalidType{}
			}
			return
		}
		bindings[p] = actual
	case *typeinfo.PointerType:
		if got, ok := actual.(*typeinfo.PointerType); ok {
			c.inferTypeParamBindings(p.Inner, got.Inner, bindings)
		}
	case *typeinfo.RefType:
		if got, ok := actual.(*typeinfo.RefType); ok && got.Mutable == p.Mutable {
			c.inferTypeParamBindings(p.Inner, got.Inner, bindings)
		}
	case *typeinfo.RawPtrType:
		if got, ok := actual.(*typeinfo.RawPtrType); ok {
			c.inferTypeParamBindings(p.Inner, got.Inner, bindings)
		}
	case *typeinfo.OptionalType:
		if got, ok := actual.(*typeinfo.OptionalType); ok {
			c.inferTypeParamBindings(p.Inner, got.Inner, bindings)
		}
	case *typeinfo.ApproxType:
		c.inferTypeParamBindings(p.Inner, actual, bindings)
	case *typeinfo.ErrorUnionType:
		if got, ok := actual.(*typeinfo.ErrorUnionType); ok {
			c.inferTypeParamBindings(p.Error, got.Error, bindings)
			c.inferTypeParamBindings(p.Value, got.Value, bindings)
		}
	case *typeinfo.ArrayType:
		if got, ok := actual.(*typeinfo.ArrayType); ok && (p.Len < 0 || p.Len == got.Len) {
			c.inferTypeParamBindings(p.Inner, got.Inner, bindings)
		}
	case *typeinfo.SliceType:
		if got, ok := actual.(*typeinfo.SliceType); ok {
			c.inferTypeParamBindings(p.Inner, got.Inner, bindings)
		}
	case *typeinfo.TupleType:
		got, ok := actual.(*typeinfo.TupleType)
		if !ok || len(p.Elems) != len(got.Elems) {
			return
		}
		for i := range p.Elems {
			c.inferTypeParamBindings(p.Elems[i], got.Elems[i], bindings)
		}
	case *typeinfo.NamedType:
		got, ok := actual.(*typeinfo.NamedType)
		if !ok || p.Name != got.Name || p.ModuleKey != got.ModuleKey || len(p.TypeArgs) != len(got.TypeArgs) {
			return
		}
		for i := range p.TypeArgs {
			c.inferTypeParamBindings(p.TypeArgs[i], got.TypeArgs[i], bindings)
		}
	}
}

func (c *checker) substituteTypeParams(typ typeinfo.Type, bindings map[*typeinfo.TypeParam]typeinfo.Type) typeinfo.Type {
	switch t := typ.(type) {
	case nil:
		return nil
	case *typeinfo.TypeParam:
		if bound := c.lookupTypeParamBinding(bindings, t); bound != nil {
			return bound
		}
		return typeinfo.UnknownType{}
	case *typeinfo.PointerType:
		return &typeinfo.PointerType{Inner: c.substituteTypeParams(t.Inner, bindings)}
	case *typeinfo.RefType:
		return &typeinfo.RefType{Mutable: t.Mutable, Inner: c.substituteTypeParams(t.Inner, bindings)}
	case *typeinfo.RawPtrType:
		return &typeinfo.RawPtrType{Const: t.Const, Inner: c.substituteTypeParams(t.Inner, bindings)}
	case *typeinfo.OptionalType:
		return &typeinfo.OptionalType{Inner: c.substituteTypeParams(t.Inner, bindings)}
	case *typeinfo.ApproxType:
		return &typeinfo.ApproxType{Inner: c.substituteTypeParams(t.Inner, bindings)}
	case *typeinfo.ErrorUnionType:
		return &typeinfo.ErrorUnionType{
			Error: c.substituteTypeParams(t.Error, bindings),
			Value: c.substituteTypeParams(t.Value, bindings),
		}
	case *typeinfo.ArrayType:
		return &typeinfo.ArrayType{Inner: c.substituteTypeParams(t.Inner, bindings), Len: t.Len}
	case *typeinfo.SliceType:
		return &typeinfo.SliceType{Inner: c.substituteTypeParams(t.Inner, bindings)}
	case *typeinfo.TupleType:
		elems := make([]typeinfo.Type, 0, len(t.Elems))
		for _, elem := range t.Elems {
			elems = append(elems, c.substituteTypeParams(elem, bindings))
		}
		return &typeinfo.TupleType{Elems: elems}
	case *typeinfo.NamedType:
		out := &typeinfo.NamedType{
			ModuleKey: t.ModuleKey,
			Name:      t.Name,
			Decl:      t.Decl,
		}
		if len(t.TypeArgs) > 0 {
			out.TypeArgs = make([]typeinfo.Type, 0, len(t.TypeArgs))
			for _, arg := range t.TypeArgs {
				out.TypeArgs = append(out.TypeArgs, c.substituteTypeParams(arg, bindings))
			}
		}
		return out
	case *typeinfo.StructType:
		fields := make(map[string]*typeinfo.StructField, len(t.Fields))
		ordered := make([]*typeinfo.StructField, 0, len(t.OrderedFields))
		for _, field := range t.OrderedFields {
			if field == nil {
				continue
			}
			copy := &typeinfo.StructField{
				Name:       field.Name,
				IsPub:      field.IsPub,
				Type:       c.substituteTypeParams(field.Type, bindings),
				HasDefault: field.HasDefault,
			}
			fields[copy.Name] = copy
			ordered = append(ordered, copy)
		}
		return &typeinfo.StructType{Fields: fields, OrderedFields: ordered}
	case *typeinfo.InterfaceType:
		methods := make(map[string]*typeinfo.FuncType, len(t.Methods))
		methodReceivers := make(map[string]typeinfo.ReceiverKind, len(t.MethodReceivers))
		methodStatic := make(map[string]bool, len(t.MethodStatic))
		ordered := make([]*typeinfo.InterfaceMethod, 0, len(t.OrderedMethods))
		for _, method := range t.OrderedMethods {
			if method == nil {
				continue
			}
			fn, _ := c.substituteTypeParams(method.Type, bindings).(*typeinfo.FuncType)
			copy := &typeinfo.InterfaceMethod{
				Receiver: method.Receiver,
				Static:   method.Static,
				Name:     method.Name,
				Type:     fn,
			}
			methods[copy.Name] = fn
			methodReceivers[copy.Name] = copy.Receiver
			methodStatic[copy.Name] = copy.Static
			ordered = append(ordered, copy)
		}
		return &typeinfo.InterfaceType{
			Methods:         methods,
			MethodReceivers: methodReceivers,
			MethodStatic:    methodStatic,
			OrderedMethods:  ordered,
		}
	case *typeinfo.UnionType:
		members := make([]typeinfo.Type, 0, len(t.Members))
		for _, member := range t.Members {
			members = append(members, c.substituteTypeParams(member, bindings))
		}
		return &typeinfo.UnionType{Members: members}
	case *typeinfo.IntersectionType:
		members := make([]typeinfo.Type, 0, len(t.Members))
		for _, member := range t.Members {
			members = append(members, c.substituteTypeParams(member, bindings))
		}
		return &typeinfo.IntersectionType{Members: members}
	case *typeinfo.FuncType:
		out := &typeinfo.FuncType{
			IsUnsafe: t.IsUnsafe,
			Result:   c.substituteTypeParams(t.Result, bindings),
		}
		for _, param := range t.Params {
			out.Params = append(out.Params, typeinfo.ParamSpec{
				Name:  param.Name,
				Type:  c.substituteTypeParams(param.Type, bindings),
				Flags: param.Flags,
			})
		}
		return out
	default:
		return typ
	}
}

func (c *checker) lookupTypeParamBinding(bindings map[*typeinfo.TypeParam]typeinfo.Type, target *typeinfo.TypeParam) typeinfo.Type {
	if target == nil {
		return nil
	}
	if bound := bindings[target]; bound != nil {
		return bound
	}
	for param, bound := range bindings {
		if typeinfo.Equal(param, target) {
			return bound
		}
	}
	return nil
}

func (c *checker) checkCallTypeParamConstraints(call *ast.CallExpr, params []*typeinfo.TypeParam, bindings map[*typeinfo.TypeParam]typeinfo.Type) {
	if call == nil {
		return
	}
	c.checkTypeParamConstraintsAt(call.Location, params, bindings)
}

func (c *checker) checkTypeParamDeclConstraintsAt(loc source.Location, mod *context.Module, decl *ast.TypeDecl, args []typeinfo.Type) {
	if c == nil || decl == nil || len(decl.TypeParams) == 0 {
		return
	}
	typeParams := c.pushTypeParams(mod, decl, decl.TypeParams)
	defer c.popTypeParams()
	if len(typeParams) == 0 || len(typeParams) != len(args) {
		return
	}
	bindings := make(map[*typeinfo.TypeParam]typeinfo.Type, len(typeParams))
	for i, param := range typeParams {
		bindings[param] = args[i]
	}
	c.checkTypeParamConstraintsAt(loc, typeParams, bindings)
}

func (c *checker) checkTypeParamConstraintsAt(loc source.Location, params []*typeinfo.TypeParam, bindings map[*typeinfo.TypeParam]typeinfo.Type) {
	for _, param := range params {
		if param == nil || param.Constraint == nil {
			continue
		}
		actual := bindings[param]
		if actual == nil || typeinfo.IsInvalid(actual) || typeinfo.IsUnknown(actual) || c.containsTypeParam(actual) {
			continue
		}
		constraint := c.substituteTypeParams(param.Constraint, bindings)
		if c.assignable(constraint, actual) {
			continue
		}
		if iface, ok := c.interfaceView(constraint); ok {
			c.reportInterfaceMismatch(loc, constraint, actual, iface)
			continue
		}
		c.reportTypeMismatch(loc, constraint, actual)
	}
}

func (c *checker) checkInstantiatedGenericRequirements(call *ast.CallExpr, callee ast.Node, bindings map[*typeinfo.TypeParam]typeinfo.Type) bool {
	sym := c.resolvedFunctionSymbol(callee)
	if sym == nil {
		return false
	}
	owner := c.findModuleForSymbol(sym)
	info := c.info
	if owner != nil && owner != c.mod {
		info = owner.Types
	}
	if info == nil {
		return false
	}
	reqs, ok := info.LookupGenericRequirements(sym)
	if !ok || len(reqs) == 0 {
		return false
	}
	failed := false
	for _, req := range reqs {
		if req == nil {
			continue
		}
		switch req.Kind {
		case typeinfo.GenericRequirementBinaryOp:
			left := c.substituteTypeParams(req.Left, bindings)
			right := c.substituteTypeParams(req.Right, bindings)
			if left == nil || right == nil || typeinfo.IsInvalid(left) || typeinfo.IsInvalid(right) || typeinfo.IsUnknown(left) || typeinfo.IsUnknown(right) {
				continue
			}
			if result, handled := c.binaryResult(req.Op, left, right); handled && result != nil {
				continue
			}
			loc := call.Location
			c.ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("instantiated generic call requires valid operation %s %q %s", left.String(), req.Op, right.String())).
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(&loc, "this call instantiates unsupported operand types"),
			)
			failed = true
		}
	}
	return failed
}

func (c *checker) resolvedFunctionSymbol(node ast.Node) *symbols.Symbol {
	res := c.lookupResolution(node)
	if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
		return nil
	}
	switch res.Symbol.Kind {
	case symbols.SymbolFunc, symbols.SymbolMethod:
		return res.Symbol
	default:
		return nil
	}
}

func (c *checker) checkReferenceArg(scope *refineScope, arg ast.Expr, expected, got typeinfo.Type) bool {
	refExpected, ok := expected.(*typeinfo.RefType)
	if !ok || refExpected == nil {
		return true
	}
	refGot, gotRef := got.(*typeinfo.RefType)
	if gotRef {
		if refExpected.Mutable && !refGot.Mutable {
			loc := arg.Loc()
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("mutable borrow parameter requires `&mut` argument").
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(&loc, "pass a mutable reference here"),
			)
			return false
		}
		return true
	}
	loc := arg.Loc()
	msg := "borrow parameter requires explicit reference argument"
	label := "pass `&value` here"
	if refExpected.Mutable {
		msg = "mutable borrow parameter requires explicit `&mut` argument"
		label = "pass `&mut value` here"
		addressable, mutable := c.exprAccess(scope, arg)
		if !addressable || !mutable {
			label = "`&mut` arguments require mutable, addressable access"
		}
	}
	c.ctx.Diagnostics.Add(
		diagnostics.NewError(msg).
			WithCode(diagnostics.ErrTypeMismatch).
			WithPrimaryLabel(&loc, label),
	)
	return false
}

func (c *checker) typeOfSelector(scope *refineScope, expr *ast.SelectorExpr) typeinfo.Type {
	left := c.typeOfExpr(scope, expr.Left, nil)
	base := c.derefForSelector(left)
	if raw, ok := c.underlying(left).(*typeinfo.RawPtrType); ok {
		if c.unsafeDepth == 0 {
			loc := expr.Location
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("raw pointer field access requires unsafe block").
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(&loc, "wrap this field access in `unsafe { ... }`"),
			)
		}
		if raw.Inner == nil {
			loc := expr.Location
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("cannot access field through untyped raw pointer").
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(&loc, "cast this raw pointer to a typed pointer first"),
			)
			return typeinfo.InvalidType{}
		}
		base = raw.Inner
	}
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
	if !c.canAccessStructField(base, field) {
		owner := c.structFieldOwnerName(base)
		c.reportNotExportedFromType(expr.Location, expr.Name.Text(), owner)
		return typeinfo.InvalidType{}
	}
	c.info.BindNode(expr, field.Type)
	return field.Type
}

func (c *checker) typeOfCast(scope *refineScope, expr *ast.CastExpr) typeinfo.Type {
	if unionTarget, memberTarget, ok := c.selectedUnionMemberTarget(c.mod, expr.Type); ok {
		sourceType := c.typeOfExpr(scope, expr.Left, memberTarget)
		if c.checkAssignable(expr.Left.Loc(), memberTarget, sourceType) {
			c.info.BindNode(expr, unionTarget)
			return unionTarget
		}
		return typeinfo.InvalidType{}
	}

	target := c.typeFromSyntax(c.mod, expr.Type)
	sourceType := c.typeOfExpr(scope, expr.Left, target)
	if c.unionContainsExactMember(sourceType, target) {
		c.info.BindNode(expr, target)
		return target
	}
	if typeinfo.Equal(target, sourceType) {
		c.info.BindNode(expr, target)
		return target
	}
	if typeinfo.IsNumeric(target) && typeinfo.IsNumeric(sourceType) {
		c.info.BindNode(expr, target)
		return target
	}
	if c.isExplicitEnumCast(target, sourceType) {
		c.info.BindNode(expr, target)
		return target
	}
	if opt, ok := c.underlying(target).(*typeinfo.OptionalType); ok && opt != nil && typeinfo.Assignable(opt.Inner, sourceType) {
		c.info.BindNode(expr, target)
		return target
	}
	if c.isExplicitStringCast(target, sourceType) {
		c.info.BindNode(expr, target)
		return target
	}
	// Raw-pointer reinterpretation: ^T → ^S (including ^void).
	// Any cast where either side is a raw pointer requires an unsafe block.
	srcUnderlying := c.underlying(sourceType)
	dstUnderlying := c.underlying(target)
	_, srcIsRawPtr := srcUnderlying.(*typeinfo.RawPtrType)
	_, dstIsRawPtr := dstUnderlying.(*typeinfo.RawPtrType)
	if srcIsRawPtr || dstIsRawPtr {
		if c.unsafeDepth > 0 {
			c.info.BindNode(expr, target)
			return target
		}
		loc := expr.Location
		c.ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("cannot cast %s to %s outside unsafe block", sourceType.String(), target.String())).
				WithCode(diagnostics.ErrInvalidCast).
				WithPrimaryLabel(&loc, "raw pointer cast requires 'unsafe'"),
		)
		return typeinfo.InvalidType{}
	}
	loc := expr.Location
	c.ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("cannot cast %s to %s", sourceType.String(), target.String())).
			WithCode(diagnostics.ErrInvalidCast).
			WithPrimaryLabel(&loc, "invalid explicit cast"),
	)
	return typeinfo.InvalidType{}
}

func (c *checker) typeOfIs(scope *refineScope, expr *ast.IsExpr) typeinfo.Type {
	left := c.typeOfExpr(scope, expr.Left, nil)
	target := c.typeFromSyntax(c.mod, expr.Type)
	if typeinfo.IsInvalid(left) || typeinfo.IsInvalid(target) || typeinfo.IsUnknown(left) || typeinfo.IsUnknown(target) {
		return typeinfo.InvalidType{}
	}
	c.info.BindNode(expr.Type, target)
	result, static, ok := c.classifyTypeTest(expr.Location, left, target)
	if !ok {
		return typeinfo.InvalidType{}
	}
	typ := &typeinfo.BuiltinType{Name: "bool"}
	if static {
		c.info.BindBool(expr, result)
	}
	c.info.BindNode(expr, typ)
	return typ
}

func (c *checker) unionContainsExactMember(source, target typeinfo.Type) bool {
	unionType, ok := c.underlying(source).(*typeinfo.UnionType)
	if !ok || unionType == nil {
		return false
	}
	for _, member := range unionType.Members {
		if typeinfo.Equal(member, target) {
			return true
		}
	}
	return false
}

func (c *checker) classifyTypeTest(loc source.Location, left, target typeinfo.Type) (bool, bool, bool) {
	if typeinfo.Equal(left, target) {
		return true, true, true
	}
	if unionType, ok := c.underlying(left).(*typeinfo.UnionType); ok {
		if unionType == nil {
			return false, false, false
		}
		if c.unionTypeMayMatch(unionType, target) {
			return false, false, true
		}
		return false, true, true
	}
	if opt, ok := c.underlying(left).(*typeinfo.OptionalType); ok && opt != nil {
		if typeinfo.Equal(opt.Inner, target) {
			return false, false, true
		}
		return false, true, true
	}
	if targetIface, ok := c.underlying(target).(*typeinfo.InterfaceType); ok {
		if srcIface, ok := c.underlying(left).(*typeinfo.InterfaceType); ok {
			return c.interfaceSatisfies(srcIface, targetIface), true, true
		}
		return c.implementsInterface(left, targetIface), true, true
	}
	if _, ok := c.underlying(left).(*typeinfo.InterfaceType); ok {
		result, ok := c.reportUnsupportedTypeTest(loc, "runtime interface type tests are not implemented yet", "only exact or interface-to-interface static checks work right now")
		return result, false, ok
	}
	return false, true, true
}

func (c *checker) typeOfIndex(scope *refineScope, expr *ast.IndexExpr) typeinfo.Type {
	baseTyp := c.typeOfExpr(scope, expr.Left, nil)
	// typecheck the index as usize
	usize := &typeinfo.BuiltinType{Name: "usize"}
	c.typeOfExpr(scope, expr.Index, usize)
	base := c.underlying(baseTyp)
	if arr, ok := base.(*typeinfo.ArrayType); ok {
		c.info.BindNode(expr, arr.Inner)
		return arr.Inner
	}
	if sl, ok := base.(*typeinfo.SliceType); ok {
		c.info.BindNode(expr, sl.Inner)
		return sl.Inner
	}
	// Pointer indexing: *T[i] → T
	if ptr, ok := base.(*typeinfo.PointerType); ok {
		c.info.BindNode(expr, ptr.Inner)
		return ptr.Inner
	}
	if ptr, ok := base.(*typeinfo.RawPtrType); ok {
		if c.unsafeDepth == 0 {
			loc := expr.Location
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("raw pointer indexing requires unsafe block").
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(&loc, "wrap this indexing operation in `unsafe { ... }`"),
			)
		}
		if ptr.Inner == nil {
			loc := expr.Location
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("cannot index into untyped raw pointer").
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(&loc, "cast this raw pointer to a typed pointer first"),
			)
			return typeinfo.InvalidType{}
		}
		c.info.BindNode(expr, ptr.Inner)
		return ptr.Inner
	}
	loc := expr.Location
	c.ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("cannot index into %s", baseTyp.String())).
			WithCode(diagnostics.ErrInvalidOperation).
			WithPrimaryLabel(&loc, "not an array, slice, or pointer type"),
	)
	return typeinfo.InvalidType{}
}

func (c *checker) typeOfComposite(scope *refineScope, expr *ast.CompositeLit, expected typeinfo.Type) typeinfo.Type {
	if expr != nil && expr.Type != nil {
		expected = c.resolveCompositeLiteralType(scope, expr, expected)
	}
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
		actual := arrType
		if arrType.Len == -2 {
			actual = &typeinfo.ArrayType{Inner: arrType.Inner, Len: int64(len(expr.Items))}
		}
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
			if actual.Len >= 0 && int64(i) >= actual.Len {
				loc := expr.Location
				c.ctx.Diagnostics.Add(
					diagnostics.NewError("too many elements in array literal").
						WithCode(diagnostics.ErrExtraField).
						WithPrimaryLabel(&loc, "excess element"),
				)
				break
			}
			got := c.typeOfExpr(scope, item.Value, actual.Inner)
			c.checkAssignable(item.Value.Loc(), actual.Inner, got)
		}
		c.info.BindNode(expr, actual)
		return actual
	}
	if _, ok := base.(*typeinfo.SliceType); ok {
		loc := expr.Location
		c.ctx.Diagnostics.Add(
			diagnostics.NewError("slice literals are not yet implemented").
				WithCode(diagnostics.ErrInvalidType).
				WithPrimaryLabel(&loc, "use an array literal or a slice-producing function for now"),
		)
		c.info.BindNode(expr, expected)
		return typeinfo.InvalidType{}
	}
	structType, ok := base.(*typeinfo.StructType)
	if !ok {
		c.info.BindNode(expr, expected)
		return expected
	}
	ownerName := c.structFieldOwnerName(expected)
	provided := make(map[string]struct{}, len(expr.Items))
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
			if !c.canAccessStructField(expected, field) {
				loc := item.Name.Location
				c.reportNotExportedFromType(loc, fieldName, ownerName)
				continue
			}
			got := c.typeOfExpr(scope, item.Value, field.Type)
			c.checkAssignable(item.Value.Loc(), field.Type, got)
			provided[fieldName] = struct{}{}
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
		if !c.canAccessStructField(expected, field) {
			loc := expr.Location
			c.reportNotExportedFromType(loc, field.Name, ownerName)
			positional++
			continue
		}
		got := c.typeOfExpr(scope, item.Value, field.Type)
		c.checkAssignable(item.Value.Loc(), field.Type, got)
		provided[field.Name] = struct{}{}
		positional++
	}
	for _, field := range c.orderedStructFields(structType) {
		if field == nil || field.HasDefault {
			continue
		}
		if _, ok := provided[field.Name]; ok {
			continue
		}
		loc := expr.Location
		c.ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("missing required field %q in composite literal", field.Name)).
				WithCode(diagnostics.ErrMissingField).
				WithPrimaryLabel(&loc, "provide a value for this field or give it a default"),
		)
	}
	c.info.BindNode(expr, expected)
	return expected
}

func (c *checker) resolveCompositeLiteralType(scope *refineScope, expr *ast.CompositeLit, fallback typeinfo.Type) typeinfo.Type {
	if expr == nil || expr.Type == nil {
		return fallback
	}
	named, ok := expr.Type.(*ast.NamedType)
	if !ok || named == nil || len(named.TypeArgs) > 0 {
		explicit := c.typeFromSyntax(c.mod, expr.Type)
		c.info.BindNode(expr.Type, explicit)
		return explicit
	}
	resolution := c.lookupTypeResolution(c.mod, named)
	if resolution == nil || resolution.Symbol == nil {
		explicit := c.typeFromSyntax(c.mod, expr.Type)
		c.info.BindNode(expr.Type, explicit)
		return explicit
	}
	decl, _ := resolution.Symbol.Node.(*ast.TypeDecl)
	if decl == nil || len(decl.TypeParams) == 0 {
		explicit := c.typeFromSyntax(c.mod, expr.Type)
		c.info.BindNode(expr.Type, explicit)
		return explicit
	}
	owner := c.findModuleForSymbol(resolution.Symbol)
	if owner == nil {
		owner = c.mod
	}
	inferred, ok := c.inferCompositeNamedType(scope, expr, owner, decl, fallback)
	if !ok {
		loc := named.Loc()
		c.ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("missing type arguments for generic type %q", resolution.Symbol.Name)).
				WithCode(diagnostics.ErrTypeMismatch).
				WithPrimaryLabel(&loc, "supply concrete type arguments here"),
		)
		c.info.BindNode(expr.Type, typeinfo.InvalidType{})
		return typeinfo.InvalidType{}
	}
	c.info.BindNode(expr.Type, inferred)
	return inferred
}

func (c *checker) inferCompositeNamedType(scope *refineScope, expr *ast.CompositeLit, owner *context.Module, decl *ast.TypeDecl, fallback typeinfo.Type) (*typeinfo.NamedType, bool) {
	if c == nil || expr == nil || decl == nil || owner == nil || len(decl.TypeParams) == 0 {
		return nil, false
	}
	typeParams := c.pushTypeParams(owner, decl, decl.TypeParams)
	defer c.popTypeParams()
	if len(typeParams) == 0 {
		return nil, false
	}
	structType, ok := c.typeFromSyntax(owner, decl.Type).(*typeinfo.StructType)
	if !ok || structType == nil {
		return nil, false
	}
	bindings := make(map[*typeinfo.TypeParam]typeinfo.Type, len(typeParams))
	if namedExpected, ok := c.underlying(fallback).(*typeinfo.NamedType); ok && namedExpected != nil && namedExpected.Name == decl.Name.Text() && len(namedExpected.TypeArgs) == len(typeParams) {
		for i, param := range typeParams {
			bindings[param] = namedExpected.TypeArgs[i]
		}
	}

	fields := c.orderedStructFields(structType)
	fieldIndex := 0
	for _, item := range expr.Items {
		if item.Value == nil {
			continue
		}
		var fieldType typeinfo.Type
		if item.Name != nil {
			field := structType.Fields[item.Name.Text()]
			if field == nil {
				continue
			}
			fieldType = field.Type
		} else {
			if fieldIndex >= len(fields) {
				continue
			}
			field := fields[fieldIndex]
			fieldIndex++
			if field == nil {
				continue
			}
			fieldType = field.Type
		}
		valueType := c.typeOfExpr(scope, item.Value, nil)
		c.inferTypeParamBindings(fieldType, valueType, bindings)
	}

	args := make([]typeinfo.Type, 0, len(typeParams))
	for _, param := range typeParams {
		bound := bindings[param]
		if bound == nil || typeinfo.IsInvalid(bound) || typeinfo.IsUnknown(bound) {
			return nil, false
		}
		args = append(args, bound)
	}
	c.checkTypeParamConstraintsAt(expr.Location, typeParams, bindings)
	return &typeinfo.NamedType{
		ModuleKey: owner.Key,
		Name:      decl.Name.Text(),
		Decl:      decl,
		TypeArgs:  args,
	}, true
}

func (c *checker) isExplicitEnumCast(target, source typeinfo.Type) bool {
	if target == nil || source == nil {
		return false
	}
	targetBase := c.underlying(target)
	sourceBase := c.underlying(source)
	_, targetIsNumeric := targetBase.(*typeinfo.BuiltinType)
	_, sourceIsNumeric := sourceBase.(*typeinfo.BuiltinType)
	_, targetIsEnum := targetBase.(*typeinfo.EnumType)
	_, sourceIsEnum := sourceBase.(*typeinfo.EnumType)
	_, targetIsError := targetBase.(*typeinfo.ErrorSetType)
	_, sourceIsError := sourceBase.(*typeinfo.ErrorSetType)
	if targetIsNumeric && (sourceIsEnum || sourceIsError) {
		return true
	}
	if sourceIsNumeric && (targetIsEnum || targetIsError) {
		return true
	}
	return false
}

func (c *checker) isExplicitStringCast(target, source typeinfo.Type) bool {
	if c.isStringType(target) && (c.isByteSliceType(source) || c.isCharSliceType(source) || typeinfo.IsNumeric(source)) {
		return true
	}
	if c.isStringType(source) && (c.isByteSliceType(target) || c.isCharSliceType(target)) {
		return true
	}
	return false
}

func (c *checker) isStringType(typ typeinfo.Type) bool {
	_, ok := c.underlying(typ).(*typeinfo.StringType)
	return ok
}

func (c *checker) isByteSliceType(typ typeinfo.Type) bool {
	sl, ok := c.underlying(typ).(*typeinfo.SliceType)
	return ok && typeinfo.IsBuiltinNamed(sl.Inner, "u8")
}

func (c *checker) isCharSliceType(typ typeinfo.Type) bool {
	sl, ok := c.underlying(typ).(*typeinfo.SliceType)
	return ok && typeinfo.IsBuiltinNamed(sl.Inner, "char")
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
	return c.underlyingSeen(typ, make(map[*ast.TypeDecl]struct{}))
}

func (c *checker) underlyingSeen(typ typeinfo.Type, seen map[*ast.TypeDecl]struct{}) typeinfo.Type {
	if named, ok := typ.(*typeinfo.NamedType); ok && named.Decl != nil {
		if _, ok := seen[named.Decl]; ok {
			return typ
		}
		seen[named.Decl] = struct{}{}
		owner := c.findModuleForType(named)
		if owner == nil {
			owner = c.mod
		}
		if len(named.TypeArgs) > 0 && len(named.Decl.TypeParams) > 0 {
			typeParams := c.pushTypeParams(owner, named.Decl, named.Decl.TypeParams)
			declType := c.typeFromSyntax(owner, named.Decl.Type)
			c.popTypeParams()
			if len(typeParams) == len(named.TypeArgs) {
				bindings := make(map[*typeinfo.TypeParam]typeinfo.Type, len(typeParams))
				for i, param := range typeParams {
					bindings[param] = named.TypeArgs[i]
				}
				return c.underlyingSeen(c.substituteTypeParams(declType, bindings), seen)
			}
		}
		return c.underlyingSeen(c.typeFromSyntax(owner, named.Decl.Type), seen)
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
	switch t := typ.(type) {
	case *typeinfo.PointerType:
		return t.Inner
	case *typeinfo.RefType:
		return t.Inner
	default:
		return typ
	}
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

func (c *checker) canAccessStructField(typ typeinfo.Type, field *typeinfo.StructField) bool {
	if field == nil || field.IsPub {
		return true
	}
	named, ok := typeinfo.ReceiverBaseNamedType(c.derefForSelector(typ))
	if !ok || named == nil {
		return true
	}
	return named.ModuleKey == c.mod.Key
}

func (c *checker) structFieldOwnerName(typ typeinfo.Type) string {
	named, ok := typeinfo.ReceiverBaseNamedType(c.derefForSelector(typ))
	if !ok || named == nil {
		return ""
	}
	if owner, ok := c.ctx.GetModule(named.ModuleKey); ok && owner != nil && owner.ImportPath != "" {
		return owner.ImportPath + "::" + named.Name
	}
	return named.Name
}

func (c *checker) exprAccess(scope *refineScope, expr ast.Expr) (addressable bool, mutable bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		res := c.lookupResolution(e)
		if res != nil && res.Kind == binding.ResolutionSymbol && res.Symbol != nil {
			return true, c.symbolMutable(res.Symbol)
		}
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
			return true, c.symbolMutable(res.Symbol)
		}
	case *ast.SelectorExpr:
		leftType, ok := c.info.Nodes[e.Left]
		if !ok {
			leftType = c.typeOfExpr(scope, e.Left, nil)
		}
		if raw, ok := c.underlying(leftType).(*typeinfo.RawPtrType); ok {
			if raw.Inner == nil {
				return false, false
			}
			if raw.Const {
				return true, false
			}
			_, leftMutable := c.exprAccess(scope, e.Left)
			return true, leftMutable
		}
		return c.exprAccess(scope, e.Left)
	case *ast.IndexExpr:
		return c.exprAccess(scope, e.Left)
	case *ast.PrefixExpr:
		if e.Op != "*" {
			return false, false
		}
		rightType := c.typeOfExpr(scope, e.Right, nil)
		switch t := c.underlying(rightType).(type) {
		case *typeinfo.RefType:
			return true, t.Mutable
		case *typeinfo.PointerType:
			_, rightMutable := c.exprAccess(scope, e.Right)
			return true, rightMutable
		case *typeinfo.RawPtrType:
			if t.Inner == nil {
				return false, false
			}
			if t.Const {
				return true, false
			}
			_, rightMutable := c.exprAccess(scope, e.Right)
			return true, rightMutable
		default:
			return false, false
		}
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
	if typeinfo.IsInvalid(typ) || typeinfo.IsUnknown(typ) {
		return false
	}
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

func (c *checker) reportNotExportedFromType(loc source.Location, name, owner string) {
	msg := fmt.Sprintf("symbol %q is not exported", name)
	if owner != "" {
		msg = fmt.Sprintf("symbol %q is not exported from %q", name, owner)
	}
	c.ctx.Diagnostics.Add(
		diagnostics.NewError(msg).
			WithCode(diagnostics.ErrSymbolNotExported).
			WithPrimaryLabel(&loc, "symbol is not exported by this type"),
	)
}

func (c *checker) requireConstExpr(scope *refineScope, expr ast.Expr, message string) {
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

func (c *checker) isConstExpr(scope *refineScope, expr ast.Expr) bool {
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

func (c *checker) isConstIdent(_ *refineScope, ident *ast.Ident) bool {
	if ident == nil || len(ident.Path) == 0 {
		return false
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
	if res.Symbol.Kind != symbols.SymbolConst && res.Symbol.Kind != symbols.SymbolVariant && res.Symbol.Kind != symbols.SymbolError {
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

func (c *checker) checkAssignable(loc source.Location, expected, got typeinfo.Type) bool {
	if c.assignable(expected, got) {
		return true
	}
	if inter, ok := c.underlying(expected).(*typeinfo.IntersectionType); ok && inter != nil {
		for _, member := range inter.Members {
			if c.assignable(member, got) {
				continue
			}
			if iface, ok := c.underlying(member).(*typeinfo.InterfaceType); ok {
				c.reportInterfaceMismatch(loc, member, got, iface)
				return false
			}
			c.reportTypeMismatch(loc, member, got)
			return false
		}
	}
	if members, matches := c.unionAssignableMembers(expected, got); members != nil {
		if matches == 0 {
			c.ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("type %s is not a valid member of %s", got.String(), expected.String())).
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(&loc, "value does not match any union member"),
			)
			return false
		}
		if matches > 1 {
			c.ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("type %s matches multiple members of %s", got.String(), expected.String())).
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(&loc, "add an explicit cast to choose the target union member"),
			)
			return false
		}
		_ = members
	}
	if iface, ok := c.underlying(expected).(*typeinfo.InterfaceType); ok {
		c.reportInterfaceMismatch(loc, expected, got, iface)
		return false
	}
	c.reportTypeMismatch(loc, expected, got)
	return false
}

func (c *checker) assignable(expected, got typeinfo.Type) bool {
	if typeinfo.Assignable(expected, got) {
		return true
	}
	if approx, ok := c.underlying(expected).(*typeinfo.ApproxType); ok && approx != nil {
		if c.assignable(approx.Inner, got) {
			return true
		}
		baseGot := c.underlying(got)
		if baseGot != got {
			return c.assignable(approx.Inner, baseGot)
		}
		return false
	}
	if inter, ok := c.underlying(expected).(*typeinfo.IntersectionType); ok && inter != nil {
		for _, member := range inter.Members {
			if !c.assignable(member, got) {
				return false
			}
		}
		return true
	}
	if iface, ok := c.underlying(expected).(*typeinfo.InterfaceType); ok && c.implementsInterface(got, iface) {
		return true
	}
	_, matches := c.unionAssignableMembers(expected, got)
	return matches == 1
}

func (c *checker) implementsInterface(src typeinfo.Type, iface *typeinfo.InterfaceType) bool {
	if iface == nil || src == nil {
		return false
	}
	if srcIface, ok := c.interfaceView(src); ok {
		return c.interfaceSatisfies(srcIface, iface)
	}
	for _, method := range iface.OrderedMethods {
		if method == nil || method.Type == nil {
			continue
		}
		want := c.instantiateSelfFuncType(method.Type, src)
		var got *typeinfo.FuncType
		if method.Static {
			_, got = c.lookupStaticMethod(src, method.Name)
		} else {
			_, got = c.lookupMethodWithReceiver(src, method.Receiver, method.Name)
		}
		if got == nil || !c.interfaceMethodCompatible(want, got) {
			return false
		}
	}
	return true
}

func (c *checker) reportInterfaceMismatch(loc source.Location, expected, got typeinfo.Type, iface *typeinfo.InterfaceType) {
	if iface == nil || got == nil {
		c.reportTypeMismatch(loc, expected, got)
		return
	}
	expectedName := expected.String()
	gotName := got.String()
	if srcIface, ok := c.interfaceView(got); ok {
		for _, method := range iface.OrderedMethods {
			if method == nil || method.Type == nil {
				continue
			}
			gotMethod := srcIface.Methods[method.Name]
			if gotMethod == nil {
				c.ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("type %s does not implement %s: missing method %s", gotName, expectedName, interfaceMethodString(method.Static, method.Receiver, method.Name, c.instantiateSelfFuncType(method.Type, got)))).
						WithCode(diagnostics.ErrTypeMismatch).
						WithPrimaryLabel(&loc, "this interface is missing a required method"),
				)
				return
			}
			want := c.instantiateSelfFuncType(method.Type, got)
			if srcIface.MethodReceivers[method.Name] != method.Receiver || srcIface.MethodStatic[method.Name] != method.Static || !c.interfaceMethodCompatible(want, gotMethod) {
				c.ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("type %s does not implement %s: method %s has incompatible signature", gotName, expectedName, method.Name)).
						WithCode(diagnostics.ErrTypeMismatch).
						WithPrimaryLabel(&loc, fmt.Sprintf("expected %s, got %s", interfaceMethodString(method.Static, method.Receiver, method.Name, want), interfaceMethodString(srcIface.MethodStatic[method.Name], srcIface.MethodReceivers[method.Name], method.Name, gotMethod))),
				)
				return
			}
		}
		c.reportTypeMismatch(loc, expected, got)
		return
	}
	for _, method := range iface.OrderedMethods {
		if method == nil || method.Type == nil {
			continue
		}
		want := c.instantiateSelfFuncType(method.Type, got)
		var gotMethod *typeinfo.FuncType
		if method.Static {
			_, gotMethod = c.lookupStaticMethod(got, method.Name)
		} else {
			_, gotMethod = c.lookupMethodWithReceiver(got, method.Receiver, method.Name)
		}
		if gotMethod == nil {
			c.ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("type %s does not implement %s: missing method %s", gotName, expectedName, interfaceMethodString(method.Static, method.Receiver, method.Name, want))).
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(&loc, "this type is missing a required method"),
			)
			return
		}
		if !c.interfaceMethodCompatible(want, gotMethod) {
			c.ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("type %s does not implement %s: method %s has incompatible signature", gotName, expectedName, method.Name)).
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(&loc, fmt.Sprintf("expected %s, got %s", interfaceMethodString(method.Static, method.Receiver, method.Name, want), interfaceMethodString(method.Static, method.Receiver, method.Name, gotMethod))),
			)
			return
		}
	}
	c.reportTypeMismatch(loc, expected, got)
}

func interfaceMethodString(isStatic bool, receiver typeinfo.ReceiverKind, name string, fn *typeinfo.FuncType) string {
	if fn == nil {
		if isStatic {
			return name + "()"
		}
		return fmt.Sprintf("%s(%sself)", name, receiver.Prefix())
	}
	params := make([]string, 0, len(fn.Params))
	for _, param := range fn.Params {
		params = append(params, param.Type.String())
	}
	sigParams := strings.Join(params, ", ")
	if !isStatic {
		selfParam := receiver.Prefix() + "self"
		if sigParams == "" {
			sigParams = selfParam
		} else {
			sigParams = selfParam + ", " + sigParams
		}
	}
	if fn.Result == nil || typeinfo.IsBuiltinNamed(fn.Result, "void") {
		return fmt.Sprintf("%s(%s)", name, sigParams)
	}
	return fmt.Sprintf("%s(%s) %s", name, sigParams, fn.Result.String())
}

func (c *checker) interfaceSatisfies(src, target *typeinfo.InterfaceType) bool {
	if src == nil || target == nil {
		return false
	}
	for _, method := range target.OrderedMethods {
		if method == nil || method.Type == nil {
			continue
		}
		got := src.Methods[method.Name]
		if got == nil || src.MethodReceivers[method.Name] != method.Receiver || src.MethodStatic[method.Name] != method.Static || !c.interfaceMethodCompatible(method.Type, got) {
			return false
		}
	}
	return true
}

func (c *checker) instantiateSelfFuncType(fn *typeinfo.FuncType, selfType typeinfo.Type) *typeinfo.FuncType {
	if fn == nil {
		return nil
	}
	params := make([]typeinfo.ParamSpec, 0, len(fn.Params))
	for _, param := range fn.Params {
		params = append(params, typeinfo.ParamSpec{
			Name:  param.Name,
			Type:  c.instantiateSelfType(param.Type, selfType),
			Flags: param.Flags,
		})
	}
	return &typeinfo.FuncType{
		IsUnsafe: fn.IsUnsafe,
		Params:   params,
		Result:   c.instantiateSelfType(fn.Result, selfType),
	}
}

func (c *checker) instantiateSelfType(typ, selfType typeinfo.Type) typeinfo.Type {
	if selfType == nil {
		return typ
	}
	switch t := typ.(type) {
	case *typeinfo.SelfType:
		return selfType
	case *typeinfo.PointerType:
		return &typeinfo.PointerType{Inner: c.instantiateSelfType(t.Inner, selfType)}
	case *typeinfo.RefType:
		return &typeinfo.RefType{Mutable: t.Mutable, Inner: c.instantiateSelfType(t.Inner, selfType)}
	case *typeinfo.RawPtrType:
		return &typeinfo.RawPtrType{Const: t.Const, Inner: c.instantiateSelfType(t.Inner, selfType)}
	case *typeinfo.OptionalType:
		return &typeinfo.OptionalType{Inner: c.instantiateSelfType(t.Inner, selfType)}
	case *typeinfo.ApproxType:
		return &typeinfo.ApproxType{Inner: c.instantiateSelfType(t.Inner, selfType)}
	case *typeinfo.ErrorUnionType:
		return &typeinfo.ErrorUnionType{Error: c.instantiateSelfType(t.Error, selfType), Value: c.instantiateSelfType(t.Value, selfType)}
	case *typeinfo.ArrayType:
		return &typeinfo.ArrayType{Inner: c.instantiateSelfType(t.Inner, selfType), Len: t.Len}
	case *typeinfo.SliceType:
		return &typeinfo.SliceType{Inner: c.instantiateSelfType(t.Inner, selfType)}
	case *typeinfo.TupleType:
		elems := make([]typeinfo.Type, 0, len(t.Elems))
		for _, elem := range t.Elems {
			elems = append(elems, c.instantiateSelfType(elem, selfType))
		}
		return &typeinfo.TupleType{Elems: elems}
	case *typeinfo.IntersectionType:
		members := make([]typeinfo.Type, 0, len(t.Members))
		for _, member := range t.Members {
			members = append(members, c.instantiateSelfType(member, selfType))
		}
		return &typeinfo.IntersectionType{Members: members}
	default:
		return typ
	}
}

func (c *checker) reportUnsupportedTypeTest(loc source.Location, msg, help string) (bool, bool) {
	c.ctx.Diagnostics.Add(
		diagnostics.NewError(msg).
			WithCode(diagnostics.ErrInvalidOperation).
			WithPrimaryLabel(&loc, "this type test needs runtime type information").
			WithHelp(help),
	)
	return false, false
}

func (c *checker) interfaceMethodCompatible(expected, got *typeinfo.FuncType) bool {
	if expected == nil || got == nil {
		return false
	}
	if expected.IsUnsafe != got.IsUnsafe {
		return false
	}
	if len(expected.Params) != len(got.Params) {
		return false
	}
	for i := range expected.Params {
		if !typeinfo.Equal(expected.Params[i].Type, got.Params[i].Type) || expected.Params[i].Flags != got.Params[i].Flags {
			return false
		}
	}
	return typeinfo.Equal(expected.Result, got.Result)
}

func (c *checker) unionAssignableMembers(expected, got typeinfo.Type) ([]typeinfo.Type, int) {
	unionType, ok := c.underlying(expected).(*typeinfo.UnionType)
	if !ok || unionType == nil {
		return nil, 0
	}
	exactMatches := 0
	assignableMatches := 0
	for _, member := range unionType.Members {
		if typeinfo.Equal(member, got) {
			exactMatches++
			continue
		}
		if typeinfo.Assignable(member, got) {
			assignableMatches++
		}
	}
	if exactMatches > 0 {
		return unionType.Members, exactMatches
	}
	return unionType.Members, assignableMatches
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
