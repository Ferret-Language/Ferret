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
		finalType := c.resolveBindingValueType(nil, declared, value, d.Value, d.IsMut)
		if finalType == nil {
			finalType = typeinfo.UnknownType{}
		}
		if d.Type != nil && declared != nil && !typeinfo.Equal(declared, finalType) {
			c.info.BindNode(d.Type, finalType)
		}
		if declared != nil && d.Value != nil {
			c.checkExprAssignable(nil, d.Value, declared, value)
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
			if constValue, ok := c.constExpr(c.mod, d.Value, nil); ok {
				c.info.BindConstValue(d, constValue)
			} else {
				c.requireConstExpr(nil, d.Value, "constant initializer must be compile-time evaluable")
			}
		}
		finalType := c.resolveBindingValueType(nil, declared, value, d.Value, false)
		if finalType == nil {
			finalType = typeinfo.UnknownType{}
		}
		if d.Type != nil && declared != nil && !typeinfo.Equal(declared, finalType) {
			c.info.BindNode(d.Type, finalType)
		}
		if declared != nil && d.Value != nil {
			c.checkExprAssignable(nil, d.Value, declared, value)
		}
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
	if len(d.TypeParams) > 0 && d.Type != nil {
		c.checkCanonicalGenericSelfUse(c.mod, d, d.Type)
	}
	for i, param := range d.TypeParams {
		if i >= len(typeParams) {
			continue
		}
		c.info.BindNode(param.Name, typeParams[i])
	}
	resolveDeclType := func() typeinfo.Type { return c.typeFromSyntax(c.mod, d.Type) }
	declType := resolveDeclType()
	if d.Type != nil {
		c.info.BindNode(d.Type, declType)
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
				c.checkExprAssignable(nil, field.Default, fieldType, valueType)
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

func (c *checker) resolveBindingValueType(scope *refineScope, declared, value typeinfo.Type, valueExpr ast.Expr, bindingMutable bool) typeinfo.Type {
	if arr, ok := declared.(*typeinfo.ArrayType); ok && arr != nil && arr.Len == typeinfo.ArrayLenInferred {
		if concrete, ok := value.(*typeinfo.ArrayType); ok && concrete != nil && typeinfo.Equal(arr.Inner, concrete.Inner) {
			declared = concrete
		}
	}
	if declaredSlice, ok := c.underlying(declared).(*typeinfo.SliceType); ok && declaredSlice != nil {
		mutable := false
		switch got := c.underlying(value).(type) {
		case *typeinfo.SliceType:
			mutable = got.Mutable
		case *typeinfo.ArrayType:
			addressable, writable := c.exprAccess(scope, valueExpr)
			mutable = addressable && writable
		case nil:
			mutable = bindingMutable
		}
		return &typeinfo.SliceType{Mutable: bindingMutable && mutable, Inner: declaredSlice.Inner}
	}
	if declared != nil {
		return declared
	}
	if inferredSlice, ok := c.underlying(value).(*typeinfo.SliceType); ok && inferredSlice != nil {
		return &typeinfo.SliceType{Mutable: bindingMutable && inferredSlice.Mutable, Inner: inferredSlice.Inner}
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
	if d.OwnerType != nil && ownerDecl != nil && len(ownerDecl.TypeParams) > 0 && !canonicalGenericUseMatchesDecl(d.OwnerType, ownerDecl) {
		c.reportInvalidGenericUse(d.OwnerType, ownerDecl, fmt.Sprintf("attached methods for generic type %q must use the declaration type parameters", ownerDecl.Name.Text()))
	}
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
		paramType := c.paramTypeForSyntax(funcScope, c.mod, param, selfType)
		if param.Type != nil {
			c.info.BindNode(param.Type, paramType)
		}
		if param.Default != nil && param.Type != nil {
			valueType := c.typeOfExpr(funcScope, param.Default, paramType)
			c.checkExprAssignable(funcScope, param.Default, paramType, valueType)
		}
		c.bindDeclSymbol(param.Name, paramType)
		if sym := c.declSymbol(param.Name); sym != nil {
			if param.IsMut {
				sym.Flags |= semmeta.FlagMutable
			}
			funcScope.Set(sym, paramType)
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
		typ := typeinfo.Type(&typeinfo.ArrayType{
			Inner: &typeinfo.BuiltinType{Name: "u8"},
			Len:   int64(len(e.Value)),
		})
		for current := expected; current != nil; {
			switch t := c.underlying(current).(type) {
			case *typeinfo.StringType, *typeinfo.InterfaceType:
				typ = &typeinfo.StringType{}
				current = nil
			case *typeinfo.OptionalType:
				current = t.Inner
			case *typeinfo.ApproxType:
				current = t.Inner
			default:
				current = nil
			}
		}
		c.info.BindNode(e, typ)
		return typ
	case *ast.CharLit:
		typ := typeinfo.Type(&typeinfo.BuiltinType{Name: "char"})
		if e.IsByte {
			typ = &typeinfo.BuiltinType{Name: "u8"}
		}
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
		return c.getTypeOfIdent(scope, e)
	case *ast.PrefixExpr:
		return c.getTypeOfPrefix(scope, e, expected)
	case *ast.SpreadExpr:
		typ := c.typeOfExpr(scope, e.Right, expected)
		c.info.BindNode(e, typ)
		return typ
	case *ast.BinaryExpr:
		return c.typeOfBinary(scope, e)
	case *ast.RangeExpr:
		return c.typeOfRange(scope, e)
	case *ast.CatchExpr:
		return c.typeOfCatch(scope, e)
	case *ast.PostfixExpr:
		return c.typeOfPostfix(scope, e)
	case *ast.CallExpr:
		return c.typeOfCall(scope, e, expected)
	case *ast.LambdaExpr:
		return c.typeOfLambda(scope, e, expected)
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

func (c *checker) getTypeOfIdent(scope *refineScope, ident *ast.Ident) typeinfo.Type {
	if ident == nil {
		return typeinfo.UnknownType{}
	}
	res := c.lookupResolution(ident)
	if res == nil {
		return typeinfo.InvalidType{}
	}
	if res.Kind == binding.ResolutionSymbol && res.Symbol != nil {
		c.reportLambdaCapture(ident, res.Symbol)
		if scope != nil && len(ident.Path) == 1 {
			if typ, ok := c.lookupRefinedType(scope, ident); ok && typ != nil {
				c.info.BindNode(ident, typ)
				return typ
			}
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

func (c *checker) getTypeOfPrefix(scope *refineScope, expr *ast.PrefixExpr, expected typeinfo.Type) typeinfo.Type {
	if expr != nil && expr.Op == "unsafe" {
		c.unsafeDepth++
		unsafeType := c.typeOfExpr(scope, expr.Right, expected)
		c.unsafeDepth--
		c.info.BindNode(expr, unsafeType)
		return unsafeType
	}
	if expr != nil && expr.Op == "comptime" {
		c.comptimeDepth++
		comptimeType := c.typeOfExpr(scope, expr.Right, expected)
		c.comptimeDepth--
		if !c.hasDeferredComptimeInputs(scope, expr.Right) {
			c.evalComptimeExpr(c.mod, expr, expr)
		}
		c.info.BindNode(expr, comptimeType)
		return comptimeType
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
		} else if rangePattern, ok := arm.Pattern.(*ast.RangeExpr); ok {
			c.checkRangePatternAgainstMatchValue(scope, valueType, rangePattern)
		} else {
			patternType := c.typeOfExpr(scope, arm.Pattern, valueType)
			if !typeinfo.Assignable(valueType, patternType) && !typeinfo.Assignable(patternType, valueType) {
				c.reportTypeMismatch(arm.Pattern.Loc(), valueType, patternType)
			}
		}
		armType, diverges := c.blockValueType(
			armScope,
			arm.Body,
			expected,
			"match arm must yield a value or exit",
			"add a final expression or terminate this arm",
			"match arm must end with a value expression or exit",
			"this arm does not produce a value",
		)
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

func (c *checker) blockValueType(
	scope *refineScope,
	block *ast.BlockStmt,
	expected typeinfo.Type,
	emptyMsg string,
	emptyLabel string,
	finalMsg string,
	finalLabel string,
) (typeinfo.Type, bool) {
	if block == nil || len(block.Stmts) == 0 {
		loc := source.Location{}
		if block != nil {
			loc = block.Location
		}
		c.ctx.Diagnostics.Add(
			diagnostics.NewError(emptyMsg).
				WithCode(diagnostics.ErrInvalidExpression).
				WithPrimaryLabel(&loc, emptyLabel),
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
		diagnostics.NewError(finalMsg).
			WithCode(diagnostics.ErrInvalidExpression).
			WithPrimaryLabel(&loc, finalLabel),
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

func (c *checker) typeOfRange(scope *refineScope, expr *ast.RangeExpr) typeinfo.Type {
	startType := c.typeOfExpr(scope, expr.Start, nil)
	endType := c.typeOfExpr(scope, expr.End, startType)
	startBase := c.underlying(startType)
	endBase := c.underlying(endType)

	elem := startBase
	if common := typeinfo.CommonNumericType(startBase, endBase); common != nil {
		elem = common
	} else if !typeinfo.Equal(startBase, endBase) {
		loc := expr.Location
		c.ctx.Diagnostics.Add(
			diagnostics.NewError("range endpoints must have compatible integer types").
				WithCode(diagnostics.ErrTypeMismatch).
				WithPrimaryLabel(&loc, "use matching integer endpoint types"),
		)
		typ := typeinfo.InvalidType{}
		c.info.BindNode(expr, typ)
		return typ
	}
	if !c.isIntegerType(elem) {
		loc := expr.Location
		msg := "range endpoints must be integers"
		label := "use integer-typed range endpoints"
		if family, _, ok := typeinfo.NumericInfo(elem); ok && family == typeinfo.NumericFloat {
			label = "floating-point ranges are not supported"
		}
		c.ctx.Diagnostics.Add(
			diagnostics.NewError(msg).
				WithCode(diagnostics.ErrTypeMismatch).
				WithPrimaryLabel(&loc, label),
		)
		typ := typeinfo.InvalidType{}
		c.info.BindNode(expr, typ)
		return typ
	}

	if expr.Step != nil {
		stepType := c.typeOfExpr(scope, expr.Step, elem)
		stepBase := c.underlying(stepType)
		if !c.isIntegerType(stepType) {
			loc := expr.Step.Loc()
			label := "use an integer step value"
			if family, _, ok := typeinfo.NumericInfo(stepBase); ok && family == typeinfo.NumericFloat {
				label = "floating-point step is not supported"
			}
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("range step must be an integer").
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(&loc, label),
			)
			typ := typeinfo.InvalidType{}
			c.info.BindNode(expr, typ)
			return typ
		}
		if lit, ok := expr.Step.(*ast.NumberLit); ok && lit != nil && lit.Value == "0" {
			loc := expr.Step.Loc()
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("range step cannot be zero").
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(&loc, "use a non-zero step"),
			)
			typ := typeinfo.InvalidType{}
			c.info.BindNode(expr, typ)
			return typ
		}
	}

	typ := &typeinfo.RangeType{Elem: elem}
	c.info.BindNode(expr, typ)
	return typ
}

func (c *checker) checkRangePatternAgainstMatchValue(scope *refineScope, valueType typeinfo.Type, pattern *ast.RangeExpr) {
	if pattern == nil {
		return
	}
	patternType := c.typeOfRange(scope, pattern)
	rangeType, ok := patternType.(*typeinfo.RangeType)
	if !ok || rangeType == nil {
		return
	}
	if !c.isIntegerType(valueType) {
		loc := pattern.Loc()
		c.ctx.Diagnostics.Add(
			diagnostics.NewError("range patterns require an integer match value").
				WithCode(diagnostics.ErrTypeMismatch).
				WithPrimaryLabel(&loc, "this match value is not an integer"),
		)
		return
	}
	if typeinfo.CommonNumericType(valueType, rangeType.Elem) != nil {
		return
	}
	if typeinfo.Assignable(valueType, rangeType.Elem) || typeinfo.Assignable(rangeType.Elem, valueType) {
		return
	}
	c.reportTypeMismatch(pattern.Loc(), valueType, rangeType.Elem)
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
	case *typeinfo.MapType:
		return c.containsTypeParam(t.Key) || c.containsTypeParam(t.Value)
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
	if err := numeric.ValidateLiteral(lit.Value); err != nil {
		loc := lit.Location
		c.ctx.Diagnostics.Add(
			diagnostics.NewError(err.Error()).
				WithCode(diagnostics.ErrInvalidNumber).
				WithPrimaryLabel(&loc, "use a valid numeric literal here"),
		)
		if expected != nil && typeinfo.IsNumeric(expected) {
			return expected
		}
		if numeric.LooksFloatLike(lit.Value) {
			return typeinfo.DefaultFloatType()
		}
		return typeinfo.DefaultIntType()
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
		return numeric.FitsIntegerLiteralInFloat(raw, bits)
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
	if c.isForeignLenCall(c.mod, expr.Callee) {
		return c.typeOfBuiltinLen(scope, expr)
	}
	if c.isCompileErrorCall(expr.Callee) && c.comptimeDepth == 0 {
		loc := expr.Callee.Loc()
		c.ctx.Diagnostics.Add(
			diagnostics.NewError("compile_error can only be used in comptime context").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(&loc, "wrap this call in `comptime { ... }`"),
		)
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
	if res := c.lookupResolution(expr.Callee); res != nil && res.Kind == binding.ResolutionSymbol && res.Symbol != nil {
		targetMod, targetDecl := c.callTargetForSymbol(res.Symbol)
		c.expandCallDefaults(expr, targetMod, targetDecl)
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

func (c *checker) typeOfLambda(scope *refineScope, expr *ast.LambdaExpr, expected typeinfo.Type) typeinfo.Type {
	var expectedFn *typeinfo.FuncType
	if fn, ok := c.underlying(expected).(*typeinfo.FuncType); ok {
		expectedFn = fn
	}

	if expectedFn != nil && len(expectedFn.Params) != len(expr.Params) {
		loc := expr.Location
		c.ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("expected %d lambda parameters, got %d", len(expectedFn.Params), len(expr.Params))).
				WithCode(diagnostics.ErrTypeMismatch).
				WithPrimaryLabel(&loc, "lambda parameter count does not match the expected function type"),
		)
	}

	lambdaScope := newRefineScope(scope)
	c.pushLambdaScope()
	defer c.popLambdaScope()
	params := make([]typeinfo.ParamSpec, 0, len(expr.Params))
	for i, param := range expr.Params {
		var paramType typeinfo.Type
		if param.Type != nil {
			paramType = c.syntaxType(c.mod, param.Type)
			c.info.BindNode(param.Type, paramType)
		} else if expectedFn != nil && i < len(expectedFn.Params) {
			paramType = expectedFn.Params[i].Type
		} else {
			loc := param.Location
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("lambda parameter type cannot be inferred here").
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(&loc, "add an explicit parameter type or use this lambda in a typed function context"),
			)
			paramType = typeinfo.UnknownType{}
		}

		if param.Name != nil {
			c.bindDeclSymbol(param.Name, paramType)
			if sym := c.declSymbol(param.Name); sym != nil {
				if param.IsMut {
					sym.Flags |= semmeta.FlagMutable
				}
				lambdaScope.Set(sym, paramType)
			}
		}

		flags := paramFlags(param)
		params = append(params, typeinfo.ParamSpec{
			Name:  ast.ExprText(param.Name),
			Type:  paramType,
			Flags: flags,
		})

		if expectedFn != nil && i < len(expectedFn.Params) && !c.checkAssignable(param.Location, expectedFn.Params[i].Type, paramType) {
			paramType = typeinfo.InvalidType{}
		}
	}

	var resultType typeinfo.Type
	expectedResult := typeinfo.Type(nil)
	if expectedFn != nil {
		expectedResult = expectedFn.Result
	}
	if expr.BodyExpr != nil {
		resultType = c.typeOfExpr(lambdaScope, expr.BodyExpr, expectedResult)
	} else if expr.BodyBlock != nil {
		if expectedResult == nil {
			if len(expr.BodyBlock.Stmts) == 0 {
				resultType = &typeinfo.BuiltinType{Name: "void"}
			} else if last, ok := expr.BodyBlock.Stmts[len(expr.BodyBlock.Stmts)-1].(*ast.ExprStmt); ok {
				for i := 0; i < len(expr.BodyBlock.Stmts)-1; i++ {
					c.checkStmt(lambdaScope, expr.BodyBlock.Stmts[i])
				}
				resultType = c.typeOfExpr(lambdaScope, last.Value, nil)
			} else {
				c.checkStmt(lambdaScope, expr.BodyBlock)
				resultType = &typeinfo.BuiltinType{Name: "void"}
			}
		} else if typeinfo.IsBuiltinNamed(expectedResult, "void") {
			c.checkStmt(lambdaScope, expr.BodyBlock)
			resultType = expectedResult
		} else {
			resultType, _ = c.blockValueType(
				lambdaScope,
				expr.BodyBlock,
				expectedResult,
				"lambda body must yield a value or exit",
				"add a final expression or terminate this lambda body",
				"lambda body must end with a value expression or exit",
				"this lambda body does not produce a value",
			)
		}
	}
	if resultType == nil {
		resultType = &typeinfo.BuiltinType{Name: "void"}
	}
	if expectedResult != nil && !c.checkAssignable(expr.Location, expectedResult, resultType) {
		resultType = typeinfo.InvalidType{}
	}

	fnType := &typeinfo.FuncType{Params: params, Result: resultType}
	c.info.BindNode(expr, fnType)
	return fnType
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
		argBase := c.underlying(argType)
		if ref, ok := argBase.(*typeinfo.RefType); ok && ref != nil {
			argBase = c.underlying(ref.Inner)
		}
		switch argBase.(type) {
		case *typeinfo.ArrayType, *typeinfo.SliceType, *typeinfo.StringType:
			// ok
		default:
			loc := expr.Args[0].Loc()
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("len expects an array, slice, or str argument").
					WithCode(diagnostics.ErrInvalidType).
					WithPrimaryLabel(&loc, "this value is not an array, slice, or str"),
			)
		}
	}
	c.info.BindNode(expr, result)
	return result
}

func (c *checker) isForeignLenCall(mod *context.Module, callee ast.Expr) bool {
	res := c.lookupTypeResolution(mod, callee)
	if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
		return false
	}
	if res.Symbol.Name != "len" {
		return false
	}
	fn, ok := res.Symbol.Node.(*ast.FuncDecl)
	return ok && fn != nil && fn.IsExtern
}

func (c *checker) isCompileErrorCall(callee ast.Expr) bool {
	res := c.lookupResolution(callee)
	if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
		return false
	}
	return res.Symbol.Name == "compile_error"
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
		handlerType, diverges := c.blockValueType(
			scope,
			expr.Handler,
			errUnion.Value,
			"catch handler must yield a fallback value or exit",
			"add a final fallback expression or terminate this handler",
			"catch handler must end with a fallback expression or exit",
			"this catch handler does not produce a fallback value",
		)
		if !diverges && !typeinfo.Assignable(errUnion.Value, handlerType) {
			c.reportTypeMismatch(expr.Handler.Stmts[len(expr.Handler.Stmts)-1].Loc(), errUnion.Value, handlerType)
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
		addressable, mutable := c.exprAccess(scope, selector.Left)
		if iface.MethodReceivers[selector.Name.Text()] == typeinfo.ReceiverRefMut && addressable && !mutable {
			loc := selector.Location
			c.ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("cannot call method %q on immutable %s", selector.Name.Text(), receiverType.String())).
					WithCode(diagnostics.ErrMethodNotFound).
					WithPrimaryLabel(&loc, "this method requires mutable receiver access").
					WithNote("declare the variable with `let mut` to allow mutable method calls"),
			)
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
	methodMod, methodDecl := c.callTargetForSymbol(methodSym)
	c.expandCallDefaults(call, methodMod, methodDecl)
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
	args := c.callArgs(call)
	variadicIndex, variadicElem, variadic := c.variadicParamInfo(fnType)
	fixedParams := len(fnType.Params)
	if variadic {
		fixedParams = variadicIndex
	}
	requiredParams := fixedParams
	for requiredParams > 0 && fnType.Params[requiredParams-1].HasDefault {
		requiredParams--
	}
	if variadic {
		if len(call.Args) < requiredParams {
			c.reportWrongArgCountAtLeast(call.Location, requiredParams, len(call.Args))
			invalid = true
		}
	} else if len(call.Args) < requiredParams || len(call.Args) > len(fnType.Params) {
		if requiredParams == len(fnType.Params) {
			c.reportWrongArgCount(call.Location, len(fnType.Params), len(call.Args))
		} else {
			c.reportWrongArgCountRange(call.Location, requiredParams, len(fnType.Params), len(call.Args))
		}
		invalid = true
	}
	for i, arg := range args {
		value := arg
		spread := false
		if spreadArg, ok := arg.(*ast.SpreadExpr); ok {
			value = spreadArg.Right
			spread = true
		}
		var expected typeinfo.Type
		if variadic && i >= variadicIndex {
			if spread {
				expected = fnType.Params[variadicIndex].Type
				if i != variadicIndex {
					loc := arg.Loc()
					c.ctx.Diagnostics.Add(
						diagnostics.NewError("spread argument must start the variadic tail").
							WithCode(diagnostics.ErrInvalidOperation).
							WithPrimaryLabel(&loc, "use `arr...` in place of the variadic tail"),
					)
					invalid = true
				}
			} else {
				expected = variadicElem
			}
		} else if i < len(fnType.Params) {
			expected = fnType.Params[i].Type
		}
		if spread && !variadic {
			loc := arg.Loc()
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("spread argument requires a variadic parameter").
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(&loc, "this call target is not variadic"),
			)
			invalid = true
		}
		if spread && variadic && i < variadicIndex {
			loc := arg.Loc()
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("spread argument is only valid for variadic parameters").
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(&loc, "use spread at the variadic parameter position"),
			)
			invalid = true
		}
		argType := c.lookupOrTypeExpr(scope, value, expected, argTypes, i)
		if spread {
			c.info.BindNode(arg, argType)
		}
		if expected != nil {
			if !c.checkReferenceArg(scope, value, expected, argType) {
				invalid = true
				continue
			}
			if !c.checkExprAssignable(scope, value, expected, argType) {
				invalid = true
			}
		}
	}
	return invalid
}

func (c *checker) callArgs(call *ast.CallExpr) []ast.Expr {
	if call == nil {
		return nil
	}
	if c != nil && c.info != nil {
		if args, ok := c.info.LookupCallArgs(call); ok {
			return args
		}
	}
	return call.Args
}

func paramFlags(param ast.Param) typeinfo.ValueFlags {
	flags := typeinfo.ValueFlags(0)
	if param.IsMut {
		flags |= typeinfo.FlagMutable
	}
	if param.IsVariadic {
		flags |= typeinfo.FlagVariadic
	}
	return flags
}

func (c *checker) paramSpecFromSyntax(mod *context.Module, param ast.Param, selfType typeinfo.Type) typeinfo.ParamSpec {
	name := ""
	if param.Name != nil {
		name = param.Name.Text()
	}
	return typeinfo.ParamSpec{
		Name:       name,
		Type:       c.instantiateSelfType(c.syntaxType(mod, param.Type), selfType),
		Flags:      paramFlags(param),
		HasDefault: param.Default != nil,
	}
}

func (c *checker) paramTypeForSyntax(scope *refineScope, mod *context.Module, param ast.Param, selfType typeinfo.Type) typeinfo.Type {
	paramType := c.instantiateSelfType(c.syntaxType(mod, param.Type), selfType)
	if slice, ok := c.underlying(paramType).(*typeinfo.SliceType); ok && slice != nil && param.IsMut {
		paramType = &typeinfo.SliceType{Mutable: true, Inner: slice.Inner}
	}
	if paramType == nil && param.Default != nil {
		paramType = c.typeOfExpr(scope, param.Default, nil)
	}
	if paramType == nil {
		paramType = typeinfo.UnknownType{}
	}
	return paramType
}

func (c *checker) callTargetForSymbol(sym *symbols.Symbol) (*context.Module, *ast.FuncDecl) {
	if sym == nil {
		return nil, nil
	}
	fn, _ := sym.Node.(*ast.FuncDecl)
	if fn == nil {
		return nil, nil
	}
	mod := c.findModuleForSymbol(sym)
	if mod == nil {
		mod = c.mod
	}
	return mod, fn
}

func (c *checker) expandCallDefaults(call *ast.CallExpr, targetMod *context.Module, targetDecl *ast.FuncDecl) {
	if c == nil || c.info == nil || call == nil || targetMod == nil || targetDecl == nil {
		return
	}
	fixedParams := len(targetDecl.Params)
	if fixedParams == 0 || len(call.Args) >= fixedParams {
		return
	}
	if targetDecl.Params[fixedParams-1].IsVariadic {
		fixedParams--
		if len(call.Args) >= fixedParams {
			return
		}
	}

	expanded := make([]ast.Expr, 0, fixedParams)
	expanded = append(expanded, call.Args...)
	paramArgs := make(map[symbols.SymbolID]ast.Expr, fixedParams)
	for i := 0; i < len(expanded) && i < fixedParams; i++ {
		if sym := c.paramSymbol(targetMod, targetDecl.Params[i]); sym != nil {
			paramArgs[sym.ID] = expanded[i]
		}
	}

	for i := len(expanded); i < fixedParams; i++ {
		param := targetDecl.Params[i]
		if param.Default == nil {
			return
		}
		defaultExpr, mapping := ast.CloneExprWithNodeMapAndSubstitute(param.Default, func(node ast.Node) ast.Expr {
			ident, ok := node.(*ast.Ident)
			if !ok || targetMod.Bindings == nil {
				return nil
			}
			res := targetMod.Bindings.Nodes[ident]
			if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
				return nil
			}
			return paramArgs[res.Symbol.ID]
		})
		if defaultExpr == nil {
			return
		}
		if c.mod != nil && c.mod.Bindings != nil && targetMod.Bindings != nil {
			for src, dst := range mapping {
				if src == nil || dst == nil {
					continue
				}
				if res := targetMod.Bindings.Nodes[src]; res != nil {
					c.mod.Bindings.BindNode(dst, res)
				}
			}
		}
		expanded = append(expanded, defaultExpr)
		if sym := c.paramSymbol(targetMod, param); sym != nil {
			paramArgs[sym.ID] = defaultExpr
		}
	}

	if len(expanded) != len(call.Args) {
		c.info.BindCallArgs(call, expanded)
	}
}

func (c *checker) paramSymbol(mod *context.Module, param ast.Param) *symbols.Symbol {
	if mod == nil || mod.Bindings == nil || param.Name == nil {
		return nil
	}
	res := mod.Bindings.Nodes[param.Name]
	if res == nil || res.Kind != binding.ResolutionSymbol {
		return nil
	}
	return res.Symbol
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

	args := c.callArgs(call)
	argTypes := make([]typeinfo.Type, 0, len(args))
	inferFromArgs := len(call.TypeArgs) == 0 || len(call.TypeArgs) < len(fnType.TypeParams) || len(genericParams) > len(fnType.TypeParams)
	variadicIndex, variadicElem, variadic := c.variadicParamInfo(fnType)
	for i, arg := range args {
		value := arg
		spread := false
		if spreadArg, ok := arg.(*ast.SpreadExpr); ok {
			value = spreadArg.Right
			spread = true
		}
		var pattern typeinfo.Type
		if variadic && i >= variadicIndex {
			if spread {
				pattern = fnType.Params[variadicIndex].Type
			} else {
				pattern = variadicElem
			}
		} else if i < len(fnType.Params) {
			pattern = fnType.Params[i].Type
		}
		expectedArg := typeinfo.Type(nil)
		if inferFromArgs && pattern != nil {
			expectedArg = c.substituteTypeParams(pattern, bindings)
			if typeinfo.IsUnknown(expectedArg) || typeinfo.IsInvalid(expectedArg) || c.containsTypeParam(expectedArg) {
				expectedArg = nil
			}
		}
		argType := c.typeOfExpr(scope, value, expectedArg)
		argTypes = append(argTypes, argType)
		if inferFromArgs && pattern != nil {
			c.inferTypeParamBindings(pattern, argType, bindings)
		}
	}
	if inferFromArgs && expected != nil {
		expectedBindings := make(map[*typeinfo.TypeParam]typeinfo.Type, len(genericParams))
		c.inferTypeParamBindings(fnType.Result, expected, expectedBindings)
		for _, param := range genericParams {
			if bindings[param] != nil {
				continue
			}
			if bound := typeinfo.LookupTypeParamBinding(expectedBindings, param); bound != nil {
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
		instantiated.Params = append(instantiated.Params, typeinfo.WithParamType(param, c.substituteTypeParams(param.Type, bindings)))
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

func (c *checker) variadicParamInfo(fnType *typeinfo.FuncType) (index int, elem typeinfo.Type, ok bool) {
	if fnType == nil || len(fnType.Params) == 0 {
		return -1, nil, false
	}
	last := len(fnType.Params) - 1
	param := fnType.Params[last]
	if !param.Flags.Variadic() {
		return -1, nil, false
	}
	if slice, isSlice := c.underlying(param.Type).(*typeinfo.SliceType); isSlice {
		return last, slice.Inner, true
	}
	return last, param.Type, true
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
	case *typeinfo.MapType:
		c.collectTypeParams(t.Key, visit)
		c.collectTypeParams(t.Value, visit)
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
		if got, ok := actual.(*typeinfo.SliceType); ok && (!p.Mutable || got.Mutable) {
			c.inferTypeParamBindings(p.Inner, got.Inner, bindings)
		} else if got, ok := actual.(*typeinfo.ArrayType); ok {
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
	case *typeinfo.MapType:
		got, ok := actual.(*typeinfo.MapType)
		if !ok {
			return
		}
		c.inferTypeParamBindings(p.Key, got.Key, bindings)
		c.inferTypeParamBindings(p.Value, got.Value, bindings)
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
	return typeinfo.RewriteType(typ, func(t typeinfo.Type) typeinfo.Type {
		param, ok := t.(*typeinfo.TypeParam)
		if !ok {
			return nil
		}
		if bound := typeinfo.LookupTypeParamBinding(bindings, param); bound != nil {
			return bound
		}
		return typeinfo.UnknownType{}
	}, nil)
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
		if _, ok := constraint.(*typeinfo.ComparableConstraint); ok {
			if c.isComparableType(actual) {
				continue
			}
			c.reportTypeMismatch(loc, constraint, actual)
			continue
		}
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
	typ := field.Type
	if refined, ok := c.lookupRefinedType(scope, expr); ok && refined != nil {
		typ = refined
	}
	c.info.BindNode(expr, typ)
	return typ
}

func (c *checker) typeOfCast(scope *refineScope, expr *ast.CastExpr) typeinfo.Type {
	if unionTarget, memberTarget, ok := c.selectedUnionMemberTarget(c.mod, expr.Type); ok {
		sourceType := c.typeOfExpr(scope, expr.Left, memberTarget)
		if c.checkExprAssignable(scope, expr.Left, memberTarget, sourceType) {
			c.info.BindNode(expr, unionTarget)
			return unionTarget
		}
		return typeinfo.InvalidType{}
	}

	target := c.typeFromSyntax(c.mod, expr.Type)
	expected := target
	if comp, ok := expr.Left.(*ast.CompositeLit); ok && comp != nil && comp.Type == nil && len(comp.Items) == 2 {
		rawParts := true
		for _, item := range comp.Items {
			if item.Name != nil {
				rawParts = false
				break
			}
		}
		if rawParts {
			if targetSlice, ok := c.underlying(target).(*typeinfo.SliceType); ok && targetSlice != nil {
				expected = &typeinfo.TupleType{Elems: []typeinfo.Type{
					&typeinfo.RawPtrType{Const: true, Inner: targetSlice.Inner},
					&typeinfo.BuiltinType{Name: "usize"},
				}}
			} else if _, ok := c.underlying(target).(*typeinfo.StringType); ok {
				expected = &typeinfo.TupleType{Elems: []typeinfo.Type{
					&typeinfo.RawPtrType{Const: true, Inner: &typeinfo.BuiltinType{Name: "u8"}},
					&typeinfo.BuiltinType{Name: "usize"},
				}}
			}
		}
	}
	sourceType := c.typeOfExpr(scope, expr.Left, expected)
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
	rawPartsToString := false
	if _, ok := c.underlying(target).(*typeinfo.StringType); ok {
		if sourceTuple, ok := c.underlying(sourceType).(*typeinfo.TupleType); ok && sourceTuple != nil && len(sourceTuple.Elems) == 2 {
			if ptrType, ok := c.underlying(sourceTuple.Elems[0]).(*typeinfo.RawPtrType); ok && ptrType != nil && typeinfo.IsBuiltinNamed(ptrType.Inner, "u8") {
				rawPartsToString = typeinfo.Equal(c.underlying(sourceTuple.Elems[1]), &typeinfo.BuiltinType{Name: "usize"})
			}
		}
	}
	if c.isSliceToRawCast(scope, expr.Left, target, sourceType) || c.isRawPartsToSliceCast(target, sourceType) || rawPartsToString {
		if targetSlice, ok := c.underlying(target).(*typeinfo.SliceType); ok && targetSlice != nil {
			switch src := c.underlying(sourceType).(type) {
			case *typeinfo.SliceType:
				target = &typeinfo.SliceType{Mutable: src.Mutable, Inner: targetSlice.Inner}
			case *typeinfo.TupleType:
				var ptrType *typeinfo.RawPtrType
				if comp, ok := expr.Left.(*ast.CompositeLit); ok && comp != nil && len(comp.Items) > 0 && comp.Items[0].Value != nil {
					if actual, ok := c.info.Nodes[comp.Items[0].Value]; ok {
						ptrType, _ = c.underlying(actual).(*typeinfo.RawPtrType)
					}
				}
				if ptrType == nil && len(src.Elems) == 2 {
					ptrType, _ = c.underlying(src.Elems[0]).(*typeinfo.RawPtrType)
				}
				if ptrType != nil {
					target = &typeinfo.SliceType{Mutable: !ptrType.Const, Inner: targetSlice.Inner}
				}
			}
		}
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
	if _, ok := c.underlying(sourceType).(*typeinfo.InterfaceType); ok {
		if _, targetIsInterface := c.underlying(target).(*typeinfo.InterfaceType); !targetIsInterface {
			c.info.BindNode(expr, target)
			return target
		}
	}
	// Raw-pointer reinterpretation: ^T → ^S (including ^void).
	// Any cast where either side is a raw pointer requires an unsafe block.
	srcUnderlying := c.underlying(sourceType)
	dstUnderlying := c.underlying(target)
	_, srcIsRawPtr := srcUnderlying.(*typeinfo.RawPtrType)
	_, dstIsRawPtr := dstUnderlying.(*typeinfo.RawPtrType)
	_, srcIsOwnerPtr := srcUnderlying.(*typeinfo.PointerType)
	_, dstIsOwnerPtr := dstUnderlying.(*typeinfo.PointerType)
	if (srcIsRawPtr && dstIsOwnerPtr) || (srcIsOwnerPtr && dstIsRawPtr) {
		loc := expr.Location
		c.ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("cannot cast %s to %s", sourceType.String(), target.String())).
				WithCode(diagnostics.ErrInvalidCast).
				WithPrimaryLabel(&loc, "conversion between raw pointers and owning pointers is disallowed; use unsafe std/mem::Adopt or std/mem::Expose"),
		)
		return typeinfo.InvalidType{}
	}
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

func (c *checker) isSliceToRawCast(scope *refineScope, source ast.Expr, target, sourceType typeinfo.Type) bool {
	targetRaw, ok := c.underlying(target).(*typeinfo.RawPtrType)
	if !ok || targetRaw == nil || targetRaw.Inner == nil {
		return false
	}
	switch src := c.underlying(sourceType).(type) {
	case *typeinfo.SliceType:
		if !typeinfo.Equal(targetRaw.Inner, src.Inner) {
			return false
		}
		if targetRaw.Const {
			return true
		}
		return src.Mutable
	case *typeinfo.ArrayType:
		if !typeinfo.Equal(targetRaw.Inner, src.Inner) {
			return false
		}
		if targetRaw.Const {
			return true
		}
		addressable, mutable := c.exprAccess(scope, source)
		return addressable && mutable
	default:
		return false
	}
}

func (c *checker) isRawPartsToSliceCast(target, sourceType typeinfo.Type) bool {
	targetSlice, ok := c.underlying(target).(*typeinfo.SliceType)
	if !ok || targetSlice == nil {
		return false
	}
	sourceTuple, ok := c.underlying(sourceType).(*typeinfo.TupleType)
	if !ok || sourceTuple == nil || len(sourceTuple.Elems) != 2 {
		return false
	}
	ptrType, ok := c.underlying(sourceTuple.Elems[0]).(*typeinfo.RawPtrType)
	if !ok || ptrType == nil || ptrType.Inner == nil {
		return false
	}
	if !typeinfo.Equal(ptrType.Inner, targetSlice.Inner) {
		return false
	}
	if targetSlice.Mutable && ptrType.Const {
		return false
	}
	return typeinfo.Equal(c.underlying(sourceTuple.Elems[1]), &typeinfo.BuiltinType{Name: "usize"})
}

func (c *checker) typeOfIs(scope *refineScope, expr *ast.IsExpr) typeinfo.Type {
	left := c.typeOfExpr(scope, expr.Left, nil)
	target := c.typeFromSyntax(c.mod, expr.Type)
	if typeinfo.IsInvalid(left) || typeinfo.IsInvalid(target) || typeinfo.IsUnknown(left) || typeinfo.IsUnknown(target) {
		return typeinfo.InvalidType{}
	}
	c.info.BindNode(expr.Type, target)
	result, static, ok := c.classifyTypeTest(left, target)
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
	if ok && unionType != nil {
		for _, member := range unionType.Members {
			if typeinfo.Equal(member, target) {
				return true
			}
		}
		return false
	}
	errUnion, ok := c.underlying(source).(*typeinfo.ErrorUnionType)
	if !ok || errUnion == nil {
		return false
	}
	return typeinfo.Equal(errUnion.Error, target) || typeinfo.Equal(errUnion.Value, target)
}

func (c *checker) classifyTypeTest(left, target typeinfo.Type) (bool, bool, bool) {
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
	if errUnion, ok := c.underlying(left).(*typeinfo.ErrorUnionType); ok && errUnion != nil {
		if typeinfo.Equal(errUnion.Error, target) || typeinfo.Equal(errUnion.Value, target) {
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
		return false, false, true
	}
	return false, true, true
}

func (c *checker) typeOfIndex(scope *refineScope, expr *ast.IndexExpr) typeinfo.Type {
	baseTyp := c.typeOfExpr(scope, expr.Left, nil)
	base := c.underlying(baseTyp)
	indexExpected := typeinfo.Type(&typeinfo.BuiltinType{Name: "usize"})
	var elem typeinfo.Type
	switch t := base.(type) {
	case *typeinfo.MapType:
		indexExpected = t.Key
		elem = t.Value
	case *typeinfo.ArrayType:
		if idx, ok := c.constExpr(c.mod, expr.Index, nil); ok {
			if index, ok := idx.NonNegativeInt64(); ok && t.Len >= 0 && index >= t.Len {
				loc := expr.Index.Loc()
				c.ctx.Diagnostics.Add(
					diagnostics.NewError("array index out of bounds").
						WithCode(diagnostics.ErrInvalidOperation).
						WithPrimaryLabel(&loc, "this array does not have that element index"),
				)
				return typeinfo.InvalidType{}
			}
		}
		elem = t.Inner
	case *typeinfo.SliceType:
		elem = t.Inner
	case *typeinfo.TupleType:
		idx, ok := c.constExpr(c.mod, expr.Index, nil)
		if !ok {
			loc := expr.Index.Loc()
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("tuple index must be a non-negative compile-time integer").
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(&loc, "use a compile-time tuple index like 0 or 1"),
			)
			return typeinfo.InvalidType{}
		}
		index, ok := idx.NonNegativeInt64()
		if !ok {
			loc := expr.Index.Loc()
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("tuple index must be a non-negative compile-time integer").
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(&loc, "use a compile-time tuple index like 0 or 1"),
			)
			return typeinfo.InvalidType{}
		}
		if int(index) >= len(t.Elems) {
			loc := expr.Index.Loc()
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("tuple index out of bounds").
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(&loc, "this tuple does not have that element index"),
			)
			return typeinfo.InvalidType{}
		}
		elem = t.Elems[int(index)]
	case *typeinfo.StringType:
		elem = &typeinfo.BuiltinType{Name: "char"}
	case *typeinfo.PointerType:
		elem = t.Inner
	case *typeinfo.RawPtrType:
		if c.unsafeDepth == 0 {
			loc := expr.Location
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("raw pointer indexing requires unsafe block").
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(&loc, "wrap this indexing operation in `unsafe { ... }`"),
			)
		}
		if t.Inner == nil {
			loc := expr.Location
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("cannot index into untyped raw pointer").
					WithCode(diagnostics.ErrInvalidOperation).
					WithPrimaryLabel(&loc, "cast this raw pointer to a typed pointer first"),
			)
			return typeinfo.InvalidType{}
		}
		elem = t.Inner
	default:
		loc := expr.Location
		c.ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("cannot index into %s", baseTyp.String())).
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(&loc, "not a map, array, slice, or pointer type"),
		)
		return typeinfo.InvalidType{}
	}
	indexType := c.typeOfExpr(scope, expr.Index, indexExpected)
	if indexExpected != nil {
		c.checkExprAssignable(scope, expr.Index, indexExpected, indexType)
	}
	if refined, ok := c.lookupRefinedType(scope, expr); ok && refined != nil {
		elem = refined
	}
	c.info.BindNode(expr, elem)
	return elem
}

func (c *checker) typeOfComposite(scope *refineScope, expr *ast.CompositeLit, expected typeinfo.Type) typeinfo.Type {
	if expr != nil && expr.Type != nil {
		expected = c.resolveCompositeLiteralType(scope, expr, expected)
	}
	if expr != nil && expr.Tuple && expr.Type == nil {
		if _, ok := c.underlying(expected).(*typeinfo.TupleType); expected == nil || !ok {
			elems := make([]typeinfo.Type, 0, len(expr.Items))
			for _, item := range expr.Items {
				itemExpected := typeinfo.Type(nil)
				if _, ok := item.Value.(*ast.StringLit); ok {
					itemExpected = &typeinfo.StringType{}
				}
				elems = append(elems, c.typeOfExpr(scope, item.Value, itemExpected))
			}
			inferred := &typeinfo.TupleType{Elems: elems}
			c.info.BindNode(expr, inferred)
			return inferred
		}
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
	if errUnion, ok := c.underlying(expected).(*typeinfo.ErrorUnionType); ok && errUnion != nil && errUnion.Value != nil {
		expected = errUnion.Value
	}
	base := c.underlying(expected)
	// Array literal: positional elements matching element type.
	if arrType, ok := base.(*typeinfo.ArrayType); ok {
		actual := arrType
		if arrType.Len == typeinfo.ArrayLenInferred {
			actual = &typeinfo.ArrayType{Inner: arrType.Inner, Len: int64(len(expr.Items))}
		}
		for i, item := range expr.Items {
			if item.Name != nil || item.Key != nil {
				loc := item.Value.Loc()
				if item.Name != nil {
					loc = item.Name.Location
				} else if item.Key != nil {
					loc = item.Key.Loc()
				}
				c.ctx.Diagnostics.Add(
					diagnostics.NewError("array literal does not support keyed or named elements").
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
			c.checkExprAssignable(scope, item.Value, actual.Inner, got)
		}
		if _, ok := expected.(*typeinfo.NamedType); ok {
			c.info.BindNode(expr, expected)
			return expected
		}
		c.info.BindNode(expr, actual)
		return actual
	}
	if sliceType, ok := base.(*typeinfo.SliceType); ok {
		for _, item := range expr.Items {
			if item.Name != nil || item.Key != nil {
				loc := item.Value.Loc()
				if item.Name != nil {
					loc = item.Name.Location
				} else if item.Key != nil {
					loc = item.Key.Loc()
				}
				c.ctx.Diagnostics.Add(
					diagnostics.NewError("slice literal does not support keyed or named elements").
						WithCode(diagnostics.ErrInvalidType).
						WithPrimaryLabel(&loc, "use positional elements"),
				)
				continue
			}
			got := c.typeOfExpr(scope, item.Value, sliceType.Inner)
			c.checkExprAssignable(scope, item.Value, sliceType.Inner, got)
		}
		actual := &typeinfo.SliceType{Mutable: true, Inner: sliceType.Inner}
		if _, ok := expected.(*typeinfo.NamedType); ok {
			c.info.BindNode(expr, expected)
			return expected
		}
		c.info.BindNode(expr, actual)
		return actual
	}
	if mapType, ok := base.(*typeinfo.MapType); ok {
		for _, item := range expr.Items {
			switch {
			case item.Name != nil:
				loc := item.Name.Location
				c.ctx.Diagnostics.Add(
					diagnostics.NewError("map literal does not support named fields").
						WithCode(diagnostics.ErrInvalidType).
						WithPrimaryLabel(&loc, "use `key => value` entries"),
				)
			case item.Key == nil:
				loc := item.Value.Loc()
				c.ctx.Diagnostics.Add(
					diagnostics.NewError("map literal requires `key => value` entries").
						WithCode(diagnostics.ErrInvalidType).
						WithPrimaryLabel(&loc, "add a key before this value"),
				)
			default:
				gotKey := c.typeOfExpr(scope, item.Key, mapType.Key)
				c.checkExprAssignable(scope, item.Key, mapType.Key, gotKey)
				gotValue := c.typeOfExpr(scope, item.Value, mapType.Value)
				c.checkExprAssignable(scope, item.Value, mapType.Value, gotValue)
			}
		}
		c.info.BindNode(expr, expected)
		return expected
	}
	if tupleType, ok := base.(*typeinfo.TupleType); ok {
		for i, item := range expr.Items {
			if item.Name != nil || item.Key != nil {
				loc := item.Value.Loc()
				if item.Name != nil {
					loc = item.Name.Location
				} else if item.Key != nil {
					loc = item.Key.Loc()
				}
				c.ctx.Diagnostics.Add(
					diagnostics.NewError("tuple literal does not support keyed or named elements").
						WithCode(diagnostics.ErrInvalidType).
						WithPrimaryLabel(&loc, "use positional tuple elements"),
				)
				continue
			}
			if i >= len(tupleType.Elems) {
				loc := item.Value.Loc()
				c.ctx.Diagnostics.Add(
					diagnostics.NewError("too many elements in tuple literal").
						WithCode(diagnostics.ErrExtraField).
						WithPrimaryLabel(&loc, "extra tuple element"),
				)
				continue
			}
			got := c.typeOfExpr(scope, item.Value, tupleType.Elems[i])
			c.checkExprAssignable(scope, item.Value, tupleType.Elems[i], got)
		}
		if len(expr.Items) < len(tupleType.Elems) {
			loc := expr.Location
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("missing required tuple element(s) in composite literal").
					WithCode(diagnostics.ErrMissingField).
					WithPrimaryLabel(&loc, "provide values for all tuple elements"),
			)
		}
		c.info.BindNode(expr, expected)
		return expected
	}
	if _, ok := base.(*typeinfo.PointerType); ok {
		loc := expr.Location
		c.ctx.Diagnostics.Add(
			diagnostics.NewError("composite literal cannot initialize an owning pointer").
				WithCode(diagnostics.ErrInvalidType).
				WithPrimaryLabel(&loc, "composite literals produce values, not owned pointers"),
		)
		c.info.BindNode(expr, expected)
		return expected
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
			c.checkExprAssignable(scope, item.Value, field.Type, got)
			provided[fieldName] = struct{}{}
			continue
		}
		if item.Key != nil {
			loc := item.Key.Loc()
			c.ctx.Diagnostics.Add(
				diagnostics.NewError("struct literal does not support keyed entries").
					WithCode(diagnostics.ErrInvalidType).
					WithPrimaryLabel(&loc, "use named fields or positional values"),
			)
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
		c.checkExprAssignable(scope, item.Value, field.Type, got)
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
	targetType := c.typeFromSyntax(owner, decl.Type)
	bindings := make(map[*typeinfo.TypeParam]typeinfo.Type, len(typeParams))
	if namedExpected, ok := c.underlying(fallback).(*typeinfo.NamedType); ok && namedExpected != nil && namedExpected.Name == decl.Name.Text() && len(namedExpected.TypeArgs) == len(typeParams) {
		for i, param := range typeParams {
			bindings[param] = namedExpected.TypeArgs[i]
		}
	}
	c.inferCompositeLiteralTypeParamBindings(scope, expr, targetType, bindings)

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

func (c *checker) inferCompositeLiteralTypeParamBindings(scope *refineScope, expr *ast.CompositeLit, target typeinfo.Type, bindings map[*typeinfo.TypeParam]typeinfo.Type) {
	if c == nil || expr == nil || target == nil {
		return
	}
	switch t := c.underlying(target).(type) {
	case *typeinfo.ArrayType:
		for _, item := range expr.Items {
			if item.Value == nil || item.Name != nil || item.Key != nil {
				continue
			}
			c.inferTypeParamBindings(t.Inner, c.inferredCompositeItemType(scope, item.Value), bindings)
		}
	case *typeinfo.SliceType:
		for _, item := range expr.Items {
			if item.Value == nil || item.Name != nil || item.Key != nil {
				continue
			}
			c.inferTypeParamBindings(t.Inner, c.inferredCompositeItemType(scope, item.Value), bindings)
		}
	case *typeinfo.MapType:
		for _, item := range expr.Items {
			if item.Key != nil {
				c.inferTypeParamBindings(t.Key, c.inferredCompositeItemType(scope, item.Key), bindings)
			}
			if item.Value != nil {
				c.inferTypeParamBindings(t.Value, c.inferredCompositeItemType(scope, item.Value), bindings)
			}
		}
	case *typeinfo.TupleType:
		for i, item := range expr.Items {
			if item.Value == nil || item.Name != nil || item.Key != nil || i >= len(t.Elems) {
				continue
			}
			c.inferTypeParamBindings(t.Elems[i], c.inferredCompositeItemType(scope, item.Value), bindings)
		}
	case *typeinfo.StructType:
		fields := c.orderedStructFields(t)
		fieldIndex := 0
		for _, item := range expr.Items {
			if item.Value == nil || item.Key != nil {
				continue
			}
			var fieldType typeinfo.Type
			if item.Name != nil {
				field := t.Fields[item.Name.Text()]
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
			c.inferTypeParamBindings(fieldType, c.inferredCompositeItemType(scope, item.Value), bindings)
		}
	}
}

func (c *checker) inferredCompositeItemType(scope *refineScope, expr ast.Expr) typeinfo.Type {
	if expr == nil {
		return nil
	}
	expected := typeinfo.Type(nil)
	if _, ok := expr.(*ast.StringLit); ok {
		expected = &typeinfo.StringType{}
	}
	return c.typeOfExpr(scope, expr, expected)
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
	if c.isStringMethodCoercion(target, source) {
		return true
	}
	if c.isStringType(source) && (c.isByteSliceType(target) || c.isCharSliceType(target)) {
		return true
	}
	return false
}

func (c *checker) isStringMethodCoercion(target, source typeinfo.Type) bool {
	if !c.isStringType(target) || source == nil {
		return false
	}
	if c.isStringType(source) {
		return false
	}
	if iface, ok := c.interfaceView(source); ok && iface != nil {
		method := iface.Methods["String"]
		if method == nil || iface.MethodStatic["String"] || iface.MethodReceivers["String"] != typeinfo.ReceiverValue {
			return false
		}
		return !method.IsUnsafe && len(method.Params) == 0 && typeinfo.Equal(method.Result, &typeinfo.StringType{})
	}
	_, method := c.lookupMethodWithReceiver(source, typeinfo.ReceiverValue, "String")
	if method != nil {
		return !method.IsUnsafe && len(method.Params) == 0 && typeinfo.Equal(method.Result, &typeinfo.StringType{})
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
		leftType, ok := c.info.Nodes[e.Left]
		if !ok {
			leftType = c.typeOfExpr(scope, e.Left, nil)
		}
		if _, ok := c.underlying(leftType).(*typeinfo.StringType); ok {
			return true, false
		}
		if sliceType, ok := c.underlying(leftType).(*typeinfo.SliceType); ok {
			_, leftMutable := c.exprAccess(scope, e.Left)
			return true, leftMutable && sliceType.Mutable
		}
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

func (c *checker) hasDeferredComptimeInputs(scope *refineScope, expr ast.Expr) bool {
	switch e := expr.(type) {
	case nil:
		return false
	case *ast.BadExpr, *ast.NumberLit, *ast.StringLit, *ast.NoneLit:
		return false
	case *ast.Ident:
		return !c.isConstIdent(scope, e)
	case *ast.PrefixExpr:
		return c.hasDeferredComptimeInputs(scope, e.Right)
	case *ast.BinaryExpr:
		return c.hasDeferredComptimeInputs(scope, e.Left) || c.hasDeferredComptimeInputs(scope, e.Right)
	case *ast.CallExpr:
		for _, arg := range e.Args {
			if c.hasDeferredComptimeInputs(scope, arg) {
				return true
			}
		}
		if selector, ok := e.Callee.(*ast.SelectorExpr); ok && selector != nil {
			return c.hasDeferredComptimeInputs(scope, selector.Left)
		}
		return false
	case *ast.CastExpr:
		return c.hasDeferredComptimeInputs(scope, e.Left)
	case *ast.CompositeLit:
		for _, item := range e.Items {
			if c.hasDeferredComptimeInputs(scope, item.Value) {
				return true
			}
		}
		return false
	case *ast.IndexExpr:
		return c.hasDeferredComptimeInputs(scope, e.Left) || c.hasDeferredComptimeInputs(scope, e.Index)
	case *ast.SelectorExpr:
		return c.hasDeferredComptimeInputs(scope, e.Left)
	default:
		return true
	}
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
	case *ast.CallExpr:
		for _, arg := range e.Args {
			if !c.isConstExpr(scope, arg) {
				return false
			}
		}
		if c.isForeignLenCall(c.mod, e.Callee) {
			return true
		}
		res := c.lookupResolution(e.Callee)
		if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
			return false
		}
		fn, ok := res.Symbol.Node.(*ast.FuncDecl)
		if !ok || fn == nil || fn.IsExtern {
			return false
		}
		if selector, ok := e.Callee.(*ast.SelectorExpr); ok && selector != nil && !fn.IsStatic {
			return c.isConstExpr(scope, selector.Left)
		}
		return true
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
		}
	}
	res := c.lookupResolution(ident)
	if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
		return false
	}
	owner := c.findModuleForSymbol(res.Symbol)
	if owner == nil {
		owner = c.mod
	}
	info := c.info
	if owner != nil && owner != c.mod {
		info = owner.Types
	}
	if info != nil && allowsConstValueCache(res.Symbol.Node) {
		if _, ok := info.LookupConstValue(res.Symbol.Node); ok {
			return true
		}
	}
	if res.Symbol.Kind != symbols.SymbolConst && res.Symbol.Kind != symbols.SymbolVariant && res.Symbol.Kind != symbols.SymbolError {
		return false
	}
	if res.Symbol.Node == nil {
		return true
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

func (c *checker) checkExprAssignable(scope *refineScope, expr ast.Expr, expected, got typeinfo.Type) bool {
	if expr != nil {
		if iface, ok := c.underlying(expected).(*typeinfo.InterfaceType); ok && iface != nil && c.implementsInterface(got, iface) {
			needsMutable := false
			for _, method := range iface.OrderedMethods {
				if method != nil && !method.Static && method.Receiver == typeinfo.ReceiverRefMut {
					needsMutable = true
					break
				}
			}
			if needsMutable {
				addressable, mutable := c.exprAccess(scope, expr)
				if !addressable || !mutable {
					loc := expr.Loc()
					c.ctx.Diagnostics.Add(
						diagnostics.NewError(fmt.Sprintf("cannot use immutable %s as %s", got.String(), expected.String())).
							WithCode(diagnostics.ErrTypeMismatch).
							WithPrimaryLabel(&loc, "this interface requires mutable receiver access").
							WithNote("declare the value with `let mut` before passing it here"),
					)
					return false
				}
			}
		}
	}
	if expr != nil && c.assignableFromExpr(scope, expr, expected, got) {
		return true
	}
	if expr == nil {
		return c.checkAssignable(source.Location{}, expected, got)
	}
	return c.checkAssignable(expr.Loc(), expected, got)
}

func (c *checker) assignableFromExpr(scope *refineScope, expr ast.Expr, expected, got typeinfo.Type) bool {
	if c.assignable(expected, got) {
		return true
	}
	expectedSlice, ok := c.underlying(expected).(*typeinfo.SliceType)
	if !ok {
		return false
	}
	gotArray, ok := c.underlying(got).(*typeinfo.ArrayType)
	if !ok || !typeinfo.Equal(expectedSlice.Inner, gotArray.Inner) {
		return false
	}
	addressable, mutable := c.exprAccess(scope, expr)
	if !addressable {
		return false
	}
	if expectedSlice.Mutable && !mutable {
		return false
	}
	return true
}

func (c *checker) checkAssignable(loc source.Location, expected, got typeinfo.Type) bool {
	if c.assignable(expected, got) {
		return true
	}
	if expectedSlice, ok := c.underlying(expected).(*typeinfo.SliceType); ok && expectedSlice != nil {
		if gotSlice, ok := c.underlying(got).(*typeinfo.SliceType); ok && gotSlice != nil && typeinfo.Equal(expectedSlice.Inner, gotSlice.Inner) && expectedSlice.Mutable && !gotSlice.Mutable {
			c.ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("slice value is not writable in this context: expected %s", expected.String())).
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(&loc, "this operation requires a mutable slice binding or mutable source view"),
			)
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
	if typeinfo.IsBuiltinNamed(got, "void") {
		return typeinfo.IsBuiltinNamed(expected, "void")
	}
	if typeinfo.Assignable(expected, got) {
		return true
	}
	if c.isStringMethodCoercion(expected, got) {
		return true
	}
	if errUnion, ok := c.underlying(expected).(*typeinfo.ErrorUnionType); ok && errUnion != nil {
		return c.assignable(errUnion.Error, got) || c.assignable(errUnion.Value, got)
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
		params = append(params, typeinfo.WithParamType(param, c.instantiateSelfType(param.Type, selfType)))
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
	return typeinfo.RewriteType(typ, func(t typeinfo.Type) typeinfo.Type {
		if _, ok := t.(*typeinfo.SelfType); ok {
			return selfType
		}
		return nil
	}, nil)
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

func (c *checker) reportWrongArgCountAtLeast(loc source.Location, expectedMin, got int) {
	c.ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("wrong argument count: expected at least %d, got %d", expectedMin, got)).
			WithCode(diagnostics.ErrWrongArgumentCount).
			WithPrimaryLabel(&loc, "argument count does not match"),
	)
}

func (c *checker) reportWrongArgCountRange(loc source.Location, expectedMin, expectedMax, got int) {
	if expectedMin == expectedMax {
		c.reportWrongArgCount(loc, expectedMax, got)
		return
	}
	c.ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("wrong argument count: expected between %d and %d, got %d", expectedMin, expectedMax, got)).
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
	case *typeinfo.SliceType:
		return indexType, t.Inner
	case *typeinfo.RangeType:
		return indexType, t.Elem
	}
	return indexType, typeinfo.UnknownType{}
}

func (c *checker) isIntegerType(typ typeinfo.Type) bool {
	family, _, ok := typeinfo.NumericInfo(c.underlying(typ))
	return ok && family != typeinfo.NumericFloat
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
