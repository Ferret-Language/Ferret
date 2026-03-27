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
	return RewriteType(typ, func(t Type) Type {
		param, ok := t.(*TypeParam)
		if !ok {
			return nil
		}
		if bound := LookupTypeParamBinding(bindings, param); bound != nil {
			return bound
		}
		return nil
	}, nil)
}

func InstantiateFuncType(fn *FuncType, bindings map[*TypeParam]Type) *FuncType {
	if fn == nil {
		return nil
	}
	out := &FuncType{
		IsUnsafe:   fn.IsUnsafe,
		TypeParams: make([]*TypeParam, 0, len(fn.TypeParams)),
		Params:     make([]ParamSpec, 0, len(fn.Params)),
		Result:     InstantiateType(fn.Result, bindings),
	}
	for _, param := range fn.Params {
		out.Params = append(out.Params, WithParamType(param, InstantiateType(param.Type, bindings)))
	}
	for _, param := range fn.TypeParams {
		if param == nil || LookupTypeParamBinding(bindings, param) != nil {
			continue
		}
		copy := &TypeParam{Name: param.Name, Owner: param.Owner}
		if param.Constraint != nil {
			copy.Constraint = InstantiateType(param.Constraint, bindings)
		}
		out.TypeParams = append(out.TypeParams, copy)
	}
	return out
}
