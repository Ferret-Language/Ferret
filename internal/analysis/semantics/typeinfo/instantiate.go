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
	return instantiateType(typ, bindings, make(map[Type]Type))
}

func instantiateType(typ Type, bindings map[*TypeParam]Type, seen map[Type]Type) Type {
	switch t := typ.(type) {
	case nil:
		return nil
	case *TypeParam:
		if bound := LookupTypeParamBinding(bindings, t); bound != nil {
			return bound
		}
		return t
	case *PointerType:
		if cached := seen[t]; cached != nil {
			return cached
		}
		out := &PointerType{}
		seen[t] = out
		out.Inner = instantiateType(t.Inner, bindings, seen)
		return out
	case *RefType:
		if cached := seen[t]; cached != nil {
			return cached
		}
		out := &RefType{Mutable: t.Mutable}
		seen[t] = out
		out.Inner = instantiateType(t.Inner, bindings, seen)
		return out
	case *RawPtrType:
		if cached := seen[t]; cached != nil {
			return cached
		}
		out := &RawPtrType{Const: t.Const}
		seen[t] = out
		out.Inner = instantiateType(t.Inner, bindings, seen)
		return out
	case *OptionalType:
		if cached := seen[t]; cached != nil {
			return cached
		}
		out := &OptionalType{}
		seen[t] = out
		out.Inner = instantiateType(t.Inner, bindings, seen)
		return out
	case *ApproxType:
		if cached := seen[t]; cached != nil {
			return cached
		}
		out := &ApproxType{}
		seen[t] = out
		out.Inner = instantiateType(t.Inner, bindings, seen)
		return out
	case *ErrorUnionType:
		if cached := seen[t]; cached != nil {
			return cached
		}
		out := &ErrorUnionType{}
		seen[t] = out
		out.Error = instantiateType(t.Error, bindings, seen)
		out.Value = instantiateType(t.Value, bindings, seen)
		return out
	case *ArrayType:
		if cached := seen[t]; cached != nil {
			return cached
		}
		out := &ArrayType{Len: t.Len}
		seen[t] = out
		out.Inner = instantiateType(t.Inner, bindings, seen)
		return out
	case *SliceType:
		if cached := seen[t]; cached != nil {
			return cached
		}
		out := &SliceType{Mutable: t.Mutable}
		seen[t] = out
		out.Inner = instantiateType(t.Inner, bindings, seen)
		return out
	case *TupleType:
		if cached := seen[t]; cached != nil {
			return cached
		}
		out := &TupleType{Elems: make([]Type, 0, len(t.Elems))}
		seen[t] = out
		elems := make([]Type, 0, len(t.Elems))
		for _, elem := range t.Elems {
			elems = append(elems, instantiateType(elem, bindings, seen))
		}
		out.Elems = elems
		return out
	case *NamedType:
		if cached := seen[t]; cached != nil {
			return cached
		}
		out := &NamedType{
			ModuleKey: t.ModuleKey,
			Name:      t.Name,
			Decl:      t.Decl,
		}
		seen[t] = out
		if len(t.TypeArgs) > 0 {
			out.TypeArgs = make([]Type, 0, len(t.TypeArgs))
			for _, arg := range t.TypeArgs {
				out.TypeArgs = append(out.TypeArgs, instantiateType(arg, bindings, seen))
			}
		}
		return out
	case *StructType:
		if cached := seen[t]; cached != nil {
			return cached
		}
		out := &StructType{
			Fields:        make(map[string]*StructField, len(t.Fields)),
			OrderedFields: make([]*StructField, 0, len(t.OrderedFields)),
		}
		seen[t] = out
		fields := make(map[string]*StructField, len(t.Fields))
		ordered := make([]*StructField, 0, len(t.OrderedFields))
		for _, field := range t.OrderedFields {
			if field == nil {
				continue
			}
			copy := &StructField{
				Name:       field.Name,
				IsPub:      field.IsPub,
				Type:       instantiateType(field.Type, bindings, seen),
				HasDefault: field.HasDefault,
			}
			fields[copy.Name] = copy
			ordered = append(ordered, copy)
		}
		out.Fields = fields
		out.OrderedFields = ordered
		return out
	case *InterfaceType:
		if cached := seen[t]; cached != nil {
			return cached
		}
		out := &InterfaceType{
			Methods:         make(map[string]*FuncType, len(t.Methods)),
			MethodReceivers: make(map[string]ReceiverKind, len(t.MethodReceivers)),
			MethodStatic:    make(map[string]bool, len(t.MethodStatic)),
			OrderedMethods:  make([]*InterfaceMethod, 0, len(t.OrderedMethods)),
		}
		seen[t] = out
		methods := make(map[string]*FuncType, len(t.Methods))
		methodReceivers := make(map[string]ReceiverKind, len(t.MethodReceivers))
		methodStatic := make(map[string]bool, len(t.MethodStatic))
		ordered := make([]*InterfaceMethod, 0, len(t.OrderedMethods))
		for _, method := range t.OrderedMethods {
			if method == nil {
				continue
			}
			fn := instantiateFuncType(method.Type, bindings, seen)
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
		out.Methods = methods
		out.MethodReceivers = methodReceivers
		out.MethodStatic = methodStatic
		out.OrderedMethods = ordered
		return out
	case *UnionType:
		if cached := seen[t]; cached != nil {
			return cached
		}
		out := &UnionType{Members: make([]Type, 0, len(t.Members))}
		seen[t] = out
		members := make([]Type, 0, len(t.Members))
		for _, member := range t.Members {
			members = append(members, instantiateType(member, bindings, seen))
		}
		out.Members = members
		return out
	case *FuncType:
		return instantiateFuncType(t, bindings, seen)
	default:
		return typ
	}
}

func InstantiateFuncType(fn *FuncType, bindings map[*TypeParam]Type) *FuncType {
	return instantiateFuncType(fn, bindings, make(map[Type]Type))
}

func instantiateFuncType(fn *FuncType, bindings map[*TypeParam]Type, seen map[Type]Type) *FuncType {
	if fn == nil {
		return nil
	}
	if cached := seen[fn]; cached != nil {
		if out, ok := cached.(*FuncType); ok {
			return out
		}
	}
	out := &FuncType{
		IsUnsafe: fn.IsUnsafe,
		Params:   make([]ParamSpec, 0, len(fn.Params)),
	}
	seen[fn] = out
	out.Result = instantiateType(fn.Result, bindings, seen)
	for _, param := range fn.Params {
		out.Params = append(out.Params, WithParamType(param, instantiateType(param.Type, bindings, seen)))
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
			copy.Constraint = instantiateType(param.Constraint, bindings, seen)
			out.TypeParams = append(out.TypeParams, &copy)
		}
	}
	return out
}
