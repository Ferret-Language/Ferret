package typechecker

import (
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/typeinfo"
)

func (c *checker) canHaveMethods(typ typeinfo.Type) bool {
	if typ == nil {
		return false
	}
	if _, ok := c.receiverBaseNamedType(typ); ok {
		return true
	}
	_, ok := c.underlying(typ).(*typeinfo.InterfaceType)
	return ok
}

func (c *checker) lookupMethod(receiverType typeinfo.Type, name string, addressable bool, mutable bool) (*symbols.Symbol, *typeinfo.FuncType) {
	baseNamed, ok := c.receiverBaseNamedType(receiverType)
	if !ok {
		return nil, nil
	}
	owner := c.findModuleForType(baseNamed)
	if owner == nil || owner.MethodSets == nil {
		return nil, nil
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
		return sym, fnType
	}
	return nil, nil
}

func (c *checker) methodCandidateKeys(receiverType typeinfo.Type, baseName string, addressable bool, mutable bool) []string {
	keys := make([]string, 0, 4)
	seen := make(map[string]struct{})
	add := func(key string) {
		if key == "" {
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
		add(baseName)
		if addressable {
			add("*" + baseName)
			if mutable {
				add("*mut " + baseName)
			}
		}
	case *typeinfo.PointerType:
		if exact, ok := c.receiverKeyFromType(t); ok {
			add(exact)
		}
		if t.IsOwn {
			add("*mut " + baseName)
			add("*" + baseName)
		} else if t.IsMut {
			add("*" + baseName)
		}
	}
	return keys
}

func (c *checker) receiverBaseNamedType(typ typeinfo.Type) (*typeinfo.NamedType, bool) {
	switch t := typ.(type) {
	case *typeinfo.NamedType:
		return t, true
	case *typeinfo.PointerType:
		named, ok := t.Inner.(*typeinfo.NamedType)
		return named, ok
	default:
		return nil, false
	}
}

func (c *checker) receiverKeyFromType(typ typeinfo.Type) (string, bool) {
	switch t := typ.(type) {
	case *typeinfo.NamedType:
		return t.Name, true
	case *typeinfo.PointerType:
		named, ok := t.Inner.(*typeinfo.NamedType)
		if !ok {
			return "", false
		}
		prefix := "*"
		if t.IsOwn {
			prefix += "own "
		}
		if t.IsRaw {
			prefix += "raw "
		}
		if t.IsMut {
			prefix += "mut "
		}
		return prefix + named.Name, true
	default:
		return "", false
	}
}
