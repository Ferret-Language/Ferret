package typeinfo

// RewriteType recursively rebuilds a type graph through one shared traversal.
// `pre` may replace the current node before descending; `post` may adjust the
// rewritten node after its children have been processed.
func RewriteType(typ Type, pre, post func(Type) Type) Type {
	return rewriteType(typ, pre, post, make(map[Type]Type))
}

func rewriteType(typ Type, pre, post func(Type) Type, seen map[Type]Type) Type {
	if typ == nil {
		return nil
	}
	if pre != nil {
		if rewritten := pre(typ); rewritten != nil {
			return rewritten
		}
	}
	if cached := seen[typ]; cached != nil {
		return cached
	}

	var out Type
	switch t := typ.(type) {
	case *TypeParam:
		if t.Constraint == nil {
			out = t
			break
		}
		copy := &TypeParam{Name: t.Name, Owner: t.Owner}
		seen[typ] = copy
		copy.Constraint = rewriteType(t.Constraint, pre, post, seen)
		out = copy
	case *PointerType:
		copy := &PointerType{}
		seen[typ] = copy
		copy.Inner = rewriteType(t.Inner, pre, post, seen)
		out = copy
	case *RefType:
		copy := &RefType{Mutable: t.Mutable}
		seen[typ] = copy
		copy.Inner = rewriteType(t.Inner, pre, post, seen)
		out = copy
	case *RawPtrType:
		copy := &RawPtrType{Const: t.Const}
		seen[typ] = copy
		copy.Inner = rewriteType(t.Inner, pre, post, seen)
		out = copy
	case *OptionalType:
		copy := &OptionalType{}
		seen[typ] = copy
		copy.Inner = rewriteType(t.Inner, pre, post, seen)
		out = copy
	case *ApproxType:
		copy := &ApproxType{}
		seen[typ] = copy
		copy.Inner = rewriteType(t.Inner, pre, post, seen)
		out = copy
	case *ErrorUnionType:
		copy := &ErrorUnionType{}
		seen[typ] = copy
		copy.Error = rewriteType(t.Error, pre, post, seen)
		copy.Value = rewriteType(t.Value, pre, post, seen)
		out = copy
	case *ArrayType:
		copy := &ArrayType{Len: t.Len}
		seen[typ] = copy
		copy.Inner = rewriteType(t.Inner, pre, post, seen)
		out = copy
	case *SliceType:
		copy := &SliceType{Mutable: t.Mutable}
		seen[typ] = copy
		copy.Inner = rewriteType(t.Inner, pre, post, seen)
		out = copy
	case *RangeType:
		copy := &RangeType{}
		seen[typ] = copy
		copy.Elem = rewriteType(t.Elem, pre, post, seen)
		out = copy
	case *TupleType:
		copy := &TupleType{Elems: make([]Type, 0, len(t.Elems))}
		seen[typ] = copy
		for _, elem := range t.Elems {
			copy.Elems = append(copy.Elems, rewriteType(elem, pre, post, seen))
		}
		out = copy
	case *MapType:
		copy := &MapType{}
		seen[typ] = copy
		copy.Key = rewriteType(t.Key, pre, post, seen)
		copy.Value = rewriteType(t.Value, pre, post, seen)
		out = copy
	case *NamedType:
		copy := &NamedType{ModuleKey: t.ModuleKey, Name: t.Name, Decl: t.Decl}
		seen[typ] = copy
		if len(t.TypeArgs) > 0 {
			copy.TypeArgs = make([]Type, 0, len(t.TypeArgs))
			for _, arg := range t.TypeArgs {
				copy.TypeArgs = append(copy.TypeArgs, rewriteType(arg, pre, post, seen))
			}
		}
		out = copy
	case *StructType:
		copy := &StructType{
			Fields:        make(map[string]*StructField, len(t.Fields)),
			OrderedFields: make([]*StructField, 0, len(t.OrderedFields)),
		}
		seen[typ] = copy
		for _, field := range t.OrderedFields {
			if field == nil {
				continue
			}
			fieldCopy := &StructField{
				Name:       field.Name,
				IsPub:      field.IsPub,
				Type:       rewriteType(field.Type, pre, post, seen),
				HasDefault: field.HasDefault,
			}
			copy.Fields[fieldCopy.Name] = fieldCopy
			copy.OrderedFields = append(copy.OrderedFields, fieldCopy)
		}
		out = copy
	case *InterfaceType:
		copy := &InterfaceType{
			Methods:         make(map[string]*FuncType, len(t.Methods)),
			MethodReceivers: make(map[string]ReceiverKind, len(t.MethodReceivers)),
			MethodStatic:    make(map[string]bool, len(t.MethodStatic)),
			OrderedMethods:  make([]*InterfaceMethod, 0, len(t.OrderedMethods)),
		}
		seen[typ] = copy
		for _, method := range t.OrderedMethods {
			if method == nil {
				continue
			}
			fn, _ := rewriteType(method.Type, pre, post, seen).(*FuncType)
			methodCopy := &InterfaceMethod{
				Receiver: method.Receiver,
				Static:   method.Static,
				Name:     method.Name,
				Type:     fn,
			}
			copy.Methods[methodCopy.Name] = fn
			copy.MethodReceivers[methodCopy.Name] = methodCopy.Receiver
			copy.MethodStatic[methodCopy.Name] = methodCopy.Static
			copy.OrderedMethods = append(copy.OrderedMethods, methodCopy)
		}
		out = copy
	case *UnionType:
		copy := &UnionType{Members: make([]Type, 0, len(t.Members))}
		seen[typ] = copy
		for _, member := range t.Members {
			copy.Members = append(copy.Members, rewriteType(member, pre, post, seen))
		}
		out = copy
	case *FuncType:
		copy := &FuncType{
			IsUnsafe:   t.IsUnsafe,
			TypeParams: make([]*TypeParam, 0, len(t.TypeParams)),
			Params:     make([]ParamSpec, 0, len(t.Params)),
		}
		seen[typ] = copy
		for _, param := range t.TypeParams {
			if param == nil {
				copy.TypeParams = append(copy.TypeParams, nil)
				continue
			}
			paramCopy := &TypeParam{Name: param.Name, Owner: param.Owner}
			if param.Constraint != nil {
				paramCopy.Constraint = rewriteType(param.Constraint, pre, post, seen)
			}
			copy.TypeParams = append(copy.TypeParams, paramCopy)
		}
		for _, param := range t.Params {
			copy.Params = append(copy.Params, WithParamType(param, rewriteType(param.Type, pre, post, seen)))
		}
		copy.Result = rewriteType(t.Result, pre, post, seen)
		out = copy
	default:
		out = typ
	}

	if post != nil {
		if rewritten := post(out); rewritten != nil {
			out = rewritten
		}
	}
	seen[typ] = out
	return out
}
