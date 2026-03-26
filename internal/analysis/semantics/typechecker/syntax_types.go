package typechecker

import (
	"compiler/internal/analysis/semantics/semmeta"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/tokens"
	"fmt"
)

func (c *checker) typeFromSyntax(mod *context.Module, expr ast.TypeExpr) typeinfo.Type {
	switch t := expr.(type) {
	case nil:
		return nil
	case *ast.NamedType:
		if len(t.TypeArgs) > 0 && len(t.Path) == 1 {
			if tokens.IsBuiltinType(t.Path[0]) {
				loc := t.Loc()
				c.ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("type %q is not generic", t.Path[0])).
						WithCode(diagnostics.ErrTypeMismatch).
						WithPrimaryLabel(&loc, "remove type arguments from this non-generic type"),
				)
				return typeinfo.InvalidType{}
			}
			if _, ok := c.lookupTypeParam(t.Path[0]); ok {
				loc := t.Loc()
				c.ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("type parameter %q cannot take type arguments", t.Path[0])).
						WithCode(diagnostics.ErrTypeMismatch).
						WithPrimaryLabel(&loc, "remove type arguments from this type parameter"),
				)
				return typeinfo.InvalidType{}
			}
		}
		if len(t.Path) == 1 && tokens.IsBuiltinType(t.Path[0]) {
			if t.Path[0] == "str" {
				return &typeinfo.StringType{}
			}
			return &typeinfo.BuiltinType{Name: t.Path[0]}
		}
		if len(t.Path) == 1 {
			if typeParam, ok := c.lookupTypeParam(t.Path[0]); ok {
				return typeParam
			}
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
		named := &typeinfo.NamedType{ModuleKey: owner.Key, Name: resolution.Symbol.Name, Decl: decl}
		if len(t.TypeArgs) == 0 {
			if decl != nil && len(decl.TypeParams) > 0 {
				loc := t.Loc()
				c.ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("missing type arguments for generic type %q", resolution.Symbol.Name)).
						WithCode(diagnostics.ErrTypeMismatch).
						WithPrimaryLabel(&loc, "supply concrete type arguments here"),
				)
				return typeinfo.InvalidType{}
			}
			return named
		}
		if decl == nil {
			return typeinfo.InvalidType{}
		}
		if len(decl.TypeParams) == 0 {
			loc := t.Loc()
			c.ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("type %q is not generic", resolution.Symbol.Name)).
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(&loc, "remove type arguments from this non-generic type"),
			)
			return typeinfo.InvalidType{}
		}
		args := make([]typeinfo.Type, 0, len(t.TypeArgs))
		for _, arg := range t.TypeArgs {
			argType := c.typeFromSyntax(mod, arg)
			c.info.BindNode(arg, argType)
			args = append(args, argType)
		}
		if len(args) != len(decl.TypeParams) {
			loc := t.Loc()
			c.ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("expected %d type arguments for %s, got %d", len(decl.TypeParams), resolution.Symbol.Name, len(args))).
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(&loc, "fix the number of type arguments"),
			)
			return typeinfo.InvalidType{}
		}
		c.checkTypeParamDeclConstraintsAt(t.Loc(), owner, decl, args)
		named.TypeArgs = args
		return named
	case *ast.SelfType:
		return &typeinfo.SelfType{}
	case *ast.PointerType:
		inner := c.typeFromSyntax(mod, t.Inner)
		return &typeinfo.PointerType{Inner: inner}
	case *ast.RefType:
		return &typeinfo.RefType{Mutable: t.Mutable, Inner: c.typeFromSyntax(mod, t.Inner)}
	case *ast.RawPtrType:
		inner := c.typeFromSyntax(mod, t.Inner)
		if t.Inner == nil || typeinfo.IsBuiltinNamed(inner, "void") {
			inner = nil
		}
		return &typeinfo.RawPtrType{Const: t.Const, Inner: inner}
	case *ast.OptionalType:
		return &typeinfo.OptionalType{Inner: c.typeFromSyntax(mod, t.Inner)}
	case *ast.ApproxType:
		return &typeinfo.ApproxType{Inner: c.typeFromSyntax(mod, t.Inner)}
	case *ast.ErrorUnionType:
		return &typeinfo.ErrorUnionType{Error: c.typeFromSyntax(mod, t.Error), Value: c.typeFromSyntax(mod, t.Value)}
	case *ast.ArrayType:
		return &typeinfo.ArrayType{Inner: c.typeFromSyntax(mod, t.Inner), Len: c.arrayLength(mod, t.Size), SizeExpr: t.Size}
	case *ast.SliceType:
		return &typeinfo.SliceType{Mutable: t.Mutable, Inner: c.typeFromSyntax(mod, t.Inner)}
	case *ast.TupleType:
		elems := make([]typeinfo.Type, 0, len(t.Elems))
		for _, elem := range t.Elems {
			elems = append(elems, c.typeFromSyntax(mod, elem))
		}
		return &typeinfo.TupleType{Elems: elems}
	case *ast.StructType:
		fields := make(map[string]*typeinfo.StructField)
		orderedFields := make([]*typeinfo.StructField, 0, len(t.Fields))
		for _, field := range t.Fields {
			if field == nil {
				continue
			}
			fieldName := field.Name.Text()
			structField := &typeinfo.StructField{Name: fieldName, IsPub: symbols.IsPubName(fieldName), Type: c.typeFromSyntax(mod, field.Type), HasDefault: field.Default != nil}
			fields[fieldName] = structField
			orderedFields = append(orderedFields, structField)
		}
		return &typeinfo.StructType{Fields: fields, OrderedFields: orderedFields}
	case *ast.EnumType:
		variants := make(map[string]struct{}, len(t.Variants))
		orderedVariants := make([]string, 0, len(t.Variants))
		variantOrdinals := make(map[string]int, len(t.Variants))
		for _, variant := range t.Variants {
			if variant != nil {
				name := variant.Name.Text()
				variants[name] = struct{}{}
				variantOrdinals[name] = len(orderedVariants)
				orderedVariants = append(orderedVariants, name)
			}
		}
		return &typeinfo.EnumType{Variants: variants, OrderedVariants: orderedVariants, VariantOrdinals: variantOrdinals}
	case *ast.ErrorType:
		members := make(map[string]struct{}, len(t.Members))
		orderedMembers := make([]string, 0, len(t.Members))
		memberOrdinals := make(map[string]int, len(t.Members))
		for _, member := range t.Members {
			if member != nil {
				name := member.Name.Text()
				members[name] = struct{}{}
				memberOrdinals[name] = len(orderedMembers)
				orderedMembers = append(orderedMembers, name)
			}
		}
		return &typeinfo.ErrorSetType{Members: members, OrderedMembers: orderedMembers, MemberOrdinals: memberOrdinals}
	case *ast.UnionType:
		members := make([]typeinfo.Type, 0, len(t.Members))
		for _, member := range t.Members {
			members = append(members, c.typeFromSyntax(mod, member))
		}
		return &typeinfo.UnionType{Members: members}
	case *ast.InterfaceType:
		methods := make(map[string]*typeinfo.FuncType)
		methodReceivers := make(map[string]typeinfo.ReceiverKind)
		methodStatic := make(map[string]bool)
		orderedMethods := make([]*typeinfo.InterfaceMethod, 0, len(t.Methods))
		for _, method := range t.Methods {
			if method == nil {
				continue
			}
			params := make([]typeinfo.ParamSpec, 0, len(method.Params))
			for _, param := range method.Params {
				params = append(params, c.paramSpecFromSyntax(mod, param, nil))
			}
			result := c.typeFromSyntax(mod, method.Result)
			if result == nil {
				result = &typeinfo.BuiltinType{Name: "void"}
			}
			fnType := &typeinfo.FuncType{Params: params, Result: result}
			name := method.Name.Text()
			receiverKind := semmeta.ReceiverKindFromSyntax(method.Receiver)
			methods[name] = fnType
			methodReceivers[name] = receiverKind
			methodStatic[name] = method.Static
			orderedMethods = append(orderedMethods, &typeinfo.InterfaceMethod{Name: name, Receiver: receiverKind, Static: method.Static, Type: fnType})
		}
		return &typeinfo.InterfaceType{Methods: methods, MethodReceivers: methodReceivers, MethodStatic: methodStatic, OrderedMethods: orderedMethods}
	default:
		return typeinfo.UnknownType{}
	}
}

func (c *checker) selectedUnionMemberTarget(mod *context.Module, expr ast.TypeExpr) (typeinfo.Type, typeinfo.Type, bool) {
	named, ok := expr.(*ast.NamedType)
	if !ok || named == nil {
		return nil, nil, false
	}
	resolution := c.lookupTypeResolution(mod, named)
	if resolution == nil || resolution.Symbol == nil || len(resolution.Remaining) != 1 {
		return nil, nil, false
	}
	decl, ok := resolution.Symbol.Node.(*ast.TypeDecl)
	if !ok || decl == nil {
		return nil, nil, false
	}
	unionDecl, ok := decl.Type.(*ast.UnionType)
	if !ok || unionDecl == nil {
		return nil, nil, false
	}
	owner := c.findModuleForSymbol(resolution.Symbol)
	if owner == nil {
		owner = mod
	}
	memberName := resolution.Remaining[0]
	memberType, ok := c.lookupNamedUnionMemberType(owner, unionDecl, memberName)
	if !ok {
		return nil, nil, false
	}
	return &typeinfo.NamedType{ModuleKey: owner.Key, Name: resolution.Symbol.Name, Decl: decl}, memberType, true
}

func (c *checker) lookupNamedUnionMemberType(mod *context.Module, unionDecl *ast.UnionType, name string) (typeinfo.Type, bool) {
	if unionDecl == nil {
		return nil, false
	}
	for _, member := range unionDecl.Members {
		named, ok := member.(*ast.NamedType)
		if !ok || named == nil || len(named.Path) != 1 {
			continue
		}
		if named.Path[0] == name {
			return c.typeFromSyntax(mod, member), true
		}
	}
	return nil, false
}

func (c *checker) arrayLength(mod *context.Module, expr ast.Expr) int64 {
	if expr == nil {
		return typeinfo.ArrayLenUnknown
	}
	if ident, ok := expr.(*ast.Ident); ok && ident.Text() == "_" {
		return typeinfo.ArrayLenInferred
	}
	value, ok := c.constExpr(mod, expr, nil)
	if !ok {
		loc := expr.Loc()
		c.ctx.Diagnostics.Add(
			diagnostics.NewError("array length must be a non-negative compile-time integer").
				WithCode(diagnostics.ErrTypeMismatch).
				WithPrimaryLabel(&loc, "this array length is not compile-time known"),
		)
		return typeinfo.ArrayLenUnknown
	}
	length, ok := value.NonNegativeInt64()
	if !ok {
		loc := expr.Loc()
		c.ctx.Diagnostics.Add(
			diagnostics.NewError("array length must be a non-negative compile-time integer").
				WithCode(diagnostics.ErrTypeMismatch).
				WithPrimaryLabel(&loc, "this array length is not compile-time known"),
		)
		return typeinfo.ArrayLenUnknown
	}
	return length
}
