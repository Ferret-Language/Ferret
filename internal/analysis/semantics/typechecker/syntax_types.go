package typechecker

import (
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/context"
	"compiler/internal/frontend/ast"
	"compiler/internal/tokens"
	"compiler/internal/utils/numeric"
)

func (c *checker) typeFromSyntax(mod *context.Module, expr ast.TypeExpr) typeinfo.Type {
	switch t := expr.(type) {
	case nil:
		return nil
	case *ast.NamedType:
		if len(t.Path) == 1 && tokens.IsBuiltinType(t.Path[0]) {
			if t.Path[0] == "str" {
				return &typeinfo.StringType{}
			}
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
		inner := c.typeFromSyntax(mod, t.Inner)
		if t.IsRaw && (t.Inner == nil || typeinfo.IsBuiltinNamed(inner, "void")) {
			inner = nil
		}
		return &typeinfo.PointerType{IsOwn: t.IsOwn, IsRaw: t.IsRaw, IsMut: t.IsMut, Inner: inner}
	case *ast.OptionalType:
		return &typeinfo.OptionalType{Inner: c.typeFromSyntax(mod, t.Inner)}
	case *ast.ErrorUnionType:
		return &typeinfo.ErrorUnionType{Error: c.typeFromSyntax(mod, t.Error), Value: c.typeFromSyntax(mod, t.Value)}
	case *ast.ArrayType:
		return &typeinfo.ArrayType{Inner: c.typeFromSyntax(mod, t.Inner), Len: c.arrayLength(t.Size)}
	case *ast.SliceType:
		return &typeinfo.SliceType{Inner: c.typeFromSyntax(mod, t.Inner)}
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
			structField := &typeinfo.StructField{Name: fieldName, Type: c.typeFromSyntax(mod, field.Type), HasDefault: field.Default != nil}
			fields[fieldName] = structField
			orderedFields = append(orderedFields, structField)
		}
		for _, field := range t.StaticFields {
			if field == nil {
				continue
			}
			fieldName := field.Name.Text()
			structField := &typeinfo.StructField{Name: fieldName, Type: c.typeFromSyntax(mod, field.Type), HasDefault: field.Default != nil}
			staticFields[fieldName] = structField
			orderedStaticFields = append(orderedStaticFields, structField)
		}
		return &typeinfo.StructType{Fields: fields, OrderedFields: orderedFields, StaticFields: staticFields, OrderedStaticFields: orderedStaticFields}
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
			result := c.typeFromSyntax(mod, method.Result)
			if result == nil {
				result = &typeinfo.BuiltinType{Name: "void"}
			}
			fnType := &typeinfo.FuncType{Params: params, ComptimeParams: comptimeParams, Result: result}
			name := method.Name.Text()
			methods[name] = fnType
			orderedMethods = append(orderedMethods, &typeinfo.InterfaceMethod{Name: name, Type: fnType})
		}
		return &typeinfo.InterfaceType{Methods: methods, OrderedMethods: orderedMethods}
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
