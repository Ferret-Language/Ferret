package ast

// WalkType visits typ and all nested child type expressions depth-first.
// Returning false skips recursion into the current node's children.
func WalkType(typ TypeExpr, visit func(TypeExpr) bool) {
	if typ == nil || visit == nil {
		return
	}
	if !visit(typ) {
		return
	}
	switch t := typ.(type) {
	case *NamedType:
		for _, arg := range t.TypeArgs {
			WalkType(arg, visit)
		}
	case *FuncType:
		for _, param := range t.Params {
			WalkType(param.Type, visit)
		}
		WalkType(t.Result, visit)
	case *PointerType:
		WalkType(t.Inner, visit)
	case *RefType:
		WalkType(t.Inner, visit)
	case *AtomicType:
		WalkType(t.Inner, visit)
	case *RawPtrType:
		WalkType(t.Inner, visit)
	case *OptionalType:
		WalkType(t.Inner, visit)
	case *ApproxType:
		WalkType(t.Inner, visit)
	case *ErrorUnionType:
		WalkType(t.Error, visit)
		WalkType(t.Value, visit)
	case *ArrayType:
		WalkType(t.Inner, visit)
	case *SliceType:
		WalkType(t.Inner, visit)
	case *MapType:
		WalkType(t.Key, visit)
		WalkType(t.Value, visit)
	case *TupleType:
		for _, elem := range t.Elems {
			WalkType(elem, visit)
		}
	case *StructType:
		for _, field := range t.Fields {
			if field != nil {
				WalkType(field.Type, visit)
			}
		}
	case *InterfaceType:
		for _, method := range t.Methods {
			if method == nil {
				continue
			}
			for _, param := range method.Params {
				WalkType(param.Type, visit)
			}
			WalkType(method.Result, visit)
		}
	case *UnionType:
		for _, member := range t.Members {
			WalkType(member, visit)
		}
	}
}
