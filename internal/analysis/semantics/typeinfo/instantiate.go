package typeinfo

// OwnerTypeBindings returns bindings from an instantiated named type's owner
// type parameters to its concrete type arguments.
func OwnerTypeBindings(named *NamedType) map[*TypeParam]Type {
	if named == nil || named.Decl == nil || len(named.Decl.TypeParams) == 0 || len(named.TypeArgs) == 0 {
		return nil
	}
	if len(named.Decl.TypeParams) != len(named.TypeArgs) {
		return nil
	}
	bindings := make(map[*TypeParam]Type, len(named.TypeArgs))
	for i, param := range named.Decl.TypeParams {
		if param.Name == nil || named.TypeArgs[i] == nil {
			continue
		}
		bindings[&TypeParam{Name: param.Name.Text(), Owner: named.Decl}] = named.TypeArgs[i]
	}
	if len(bindings) == 0 {
		return nil
	}
	return bindings
}

// LookupTypeParamBinding resolves a type parameter binding by identity or by
// semantic equality (name + owner).
func LookupTypeParamBinding(bindings map[*TypeParam]Type, target *TypeParam) Type {
	if target == nil {
		return nil
	}
	if bound := bindings[target]; bound != nil {
		return bound
	}
	for param, bound := range bindings {
		if Equal(param, target) {
			return bound
		}
	}
	return nil
}

// InstantiateType applies type parameter bindings while preserving unbound
// type params for display and downstream generic handling.
func InstantiateType(typ Type, bindings map[*TypeParam]Type) Type {
	switch t := typ.(type) {
	case nil:
		return nil
	case *TypeParam:
		if bound := LookupTypeParamBinding(bindings, t); bound != nil {
			return bound
		}
		return t
	case *PointerType:
		return &PointerType{Inner: InstantiateType(t.Inner, bindings)}
	case *RefType:
		return &RefType{Mutable: t.Mutable, Inner: InstantiateType(t.Inner, bindings)}
	case *RawPtrType:
		return &RawPtrType{Inner: InstantiateType(t.Inner, bindings)}
	case *OptionalType:
		return &OptionalType{Inner: InstantiateType(t.Inner, bindings)}
	case *ErrorUnionType:
		return &ErrorUnionType{
			Error: InstantiateType(t.Error, bindings),
			Value: InstantiateType(t.Value, bindings),
		}
	case *ArrayType:
		return &ArrayType{Inner: InstantiateType(t.Inner, bindings), Len: t.Len}
	case *SliceType:
		return &SliceType{Inner: InstantiateType(t.Inner, bindings)}
	case *TupleType:
		elems := make([]Type, 0, len(t.Elems))
		for _, elem := range t.Elems {
			elems = append(elems, InstantiateType(elem, bindings))
		}
		return &TupleType{Elems: elems}
	case *NamedType:
		out := &NamedType{
			ModuleKey: t.ModuleKey,
			Name:      t.Name,
			Decl:      t.Decl,
		}
		if len(t.TypeArgs) > 0 {
			out.TypeArgs = make([]Type, 0, len(t.TypeArgs))
			for _, arg := range t.TypeArgs {
				out.TypeArgs = append(out.TypeArgs, InstantiateType(arg, bindings))
			}
		}
		return out
	case *StructType:
		fields := make(map[string]*StructField, len(t.Fields))
		ordered := make([]*StructField, 0, len(t.OrderedFields))
		for _, field := range t.OrderedFields {
			if field == nil {
				continue
			}
			copy := &StructField{
				Name:       field.Name,
				IsPub:      field.IsPub,
				Type:       InstantiateType(field.Type, bindings),
				HasDefault: field.HasDefault,
			}
			fields[copy.Name] = copy
			ordered = append(ordered, copy)
		}
		return &StructType{Fields: fields, OrderedFields: ordered}
	case *InterfaceType:
		methods := make(map[string]*FuncType, len(t.Methods))
		methodReceivers := make(map[string]ReceiverKind, len(t.MethodReceivers))
		methodStatic := make(map[string]bool, len(t.MethodStatic))
		ordered := make([]*InterfaceMethod, 0, len(t.OrderedMethods))
		for _, method := range t.OrderedMethods {
			if method == nil {
				continue
			}
			fn := InstantiateFuncType(method.Type, bindings)
			copy := &InterfaceMethod{
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
		return &InterfaceType{
			Methods:         methods,
			MethodReceivers: methodReceivers,
			MethodStatic:    methodStatic,
			OrderedMethods:  ordered,
		}
	case *UnionType:
		members := make([]Type, 0, len(t.Members))
		for _, member := range t.Members {
			members = append(members, InstantiateType(member, bindings))
		}
		return &UnionType{Members: members}
	case *FuncType:
		return InstantiateFuncType(t, bindings)
	default:
		return typ
	}
}

func InstantiateFuncType(fn *FuncType, bindings map[*TypeParam]Type) *FuncType {
	if fn == nil {
		return nil
	}
	out := &FuncType{
		IsUnsafe: fn.IsUnsafe,
		Result:   InstantiateType(fn.Result, bindings),
		Params:   make([]ParamSpec, 0, len(fn.Params)),
	}
	for _, param := range fn.Params {
		out.Params = append(out.Params, ParamSpec{
			Name:  param.Name,
			Type:  InstantiateType(param.Type, bindings),
			Flags: param.Flags,
		})
	}
	if len(fn.TypeParams) > 0 {
		out.TypeParams = make([]*TypeParam, 0, len(fn.TypeParams))
		for _, param := range fn.TypeParams {
			if param == nil {
				continue
			}
			if LookupTypeParamBinding(bindings, param) != nil {
				continue
			}
			copy := *param
			copy.Constraint = InstantiateType(param.Constraint, bindings)
			out.TypeParams = append(out.TypeParams, &copy)
		}
	}
	return out
}
