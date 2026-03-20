package typechecker

import (
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/frontend/ast"
)

func (c *checker) canHaveMethods(typ typeinfo.Type) bool {
	if typ == nil {
		return false
	}
	if _, ok := typeinfo.ReceiverBaseNamedType(typ); ok {
		return true
	}
	_, ok := c.interfaceView(typ)
	return ok
}

func (c *checker) interfaceView(typ typeinfo.Type) (*typeinfo.InterfaceType, bool) {
	if typ == nil {
		return nil, false
	}
	if iface, ok := c.underlying(typ).(*typeinfo.InterfaceType); ok {
		return iface, true
	}
	typeParam, ok := typ.(*typeinfo.TypeParam)
	if !ok || typeParam == nil || typeParam.Constraint == nil {
		return nil, false
	}
	if iface, ok := c.underlying(typeParam.Constraint).(*typeinfo.InterfaceType); ok {
		return iface, true
	}
	return nil, false
}

func (c *checker) lookupMethod(receiverType typeinfo.Type, name string, addressable bool, mutable bool) (*symbols.Symbol, *typeinfo.FuncType) {
	sym, fnType, _ := c.lookupMethodDetailed(receiverType, name, addressable, mutable)
	return sym, fnType
}

func (c *checker) lookupMethodDetailed(receiverType typeinfo.Type, name string, addressable bool, mutable bool) (*symbols.Symbol, *typeinfo.FuncType, typeinfo.Type) {
	baseNamed, ok := typeinfo.ReceiverBaseNamedType(receiverType)
	if !ok {
		return nil, nil, nil
	}
	owner := c.findModuleForType(baseNamed)
	if owner == nil || owner.MethodSets == nil {
		return nil, nil, nil
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
		fnType = c.instantiateOwnerMethodType(baseNamed, sym, fnType)
		return sym, fnType, typeinfo.ReceiverTypeFromKey(baseNamed, key)
	}
	return nil, nil, nil
}

func (c *checker) lookupMethodWithReceiver(receiverType typeinfo.Type, receiver typeinfo.ReceiverKind, name string) (*symbols.Symbol, *typeinfo.FuncType) {
	baseNamed, ok := typeinfo.ReceiverBaseNamedType(receiverType)
	if !ok {
		return nil, nil
	}
	owner := c.findModuleForType(baseNamed)
	if owner == nil || owner.MethodSets == nil {
		return nil, nil
	}
	key := typeinfo.ReceiverKey{Kind: receiver, TypeName: baseNamed.Name}
	methods := owner.MethodSets[key]
	if methods == nil {
		return nil, nil
	}
	sym := methods[name]
	if sym == nil {
		return nil, nil
	}
	fnType, _ := c.typeOfSymbol(sym).(*typeinfo.FuncType)
	fnType = c.instantiateOwnerMethodType(baseNamed, sym, fnType)
	return sym, fnType
}

func (c *checker) lookupStaticMethod(ownerType typeinfo.Type, name string) (*symbols.Symbol, *typeinfo.FuncType) {
	named, ok := ownerType.(*typeinfo.NamedType)
	if !ok || named == nil {
		return nil, nil
	}
	owner := c.findModuleForType(named)
	if owner == nil || owner.TypeMembers == nil {
		return nil, nil
	}
	members := owner.TypeMembers[named.Name]
	if members == nil {
		return nil, nil
	}
	sym := members[name]
	if sym == nil || sym.Kind != symbols.SymbolFunc {
		return nil, nil
	}
	fnType, _ := c.typeOfSymbol(sym).(*typeinfo.FuncType)
	fnType = c.instantiateOwnerMethodType(named, sym, fnType)
	return sym, fnType
}

func (c *checker) instantiateOwnerMethodType(ownerNamed *typeinfo.NamedType, sym *symbols.Symbol, fnType *typeinfo.FuncType) *typeinfo.FuncType {
	if c == nil || ownerNamed == nil || sym == nil || fnType == nil {
		return fnType
	}
	fnDecl, _ := sym.Node.(*ast.FuncDecl)
	if fnDecl == nil || fnDecl.OwnerType == nil {
		return fnType
	}
	bindings := typeinfo.OwnerTypeBindings(ownerNamed)
	if len(bindings) == 0 {
		return fnType
	}
	out := typeinfo.InstantiateFuncType(fnType, bindings)
	if out == nil {
		return fnType
	}
	return out
}

func (c *checker) interfaceMethodReceiverType(receiverType typeinfo.Type, receiver typeinfo.ReceiverKind) typeinfo.Type {
	return typeinfo.ApplyReceiverShape(receiverType, receiver)
}

func (c *checker) methodCandidateKeys(receiverType typeinfo.Type, baseName string, addressable bool, mutable bool) []typeinfo.ReceiverKey {
	keys := make([]typeinfo.ReceiverKey, 0, 4)
	seen := make(map[typeinfo.ReceiverKey]struct{})
	add := func(key typeinfo.ReceiverKey) {
		if key.TypeName == "" {
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
		add(typeinfo.ReceiverKey{TypeName: baseName})
		if addressable {
			add(typeinfo.ReceiverKey{Kind: typeinfo.ReceiverRef, TypeName: baseName})
			if mutable {
				add(typeinfo.ReceiverKey{Kind: typeinfo.ReceiverRefMut, TypeName: baseName})
			}
		}
	case *typeinfo.RefType:
		if exact, ok := typeinfo.ReceiverKeyFromType(t); ok {
			add(exact)
		}
		if t.Mutable {
			add(typeinfo.ReceiverKey{Kind: typeinfo.ReceiverRef, TypeName: baseName})
		}
	case *typeinfo.PointerType:
		if exact, ok := typeinfo.ReceiverKeyFromType(t); ok {
			add(exact)
		}
	}
	return keys
}
