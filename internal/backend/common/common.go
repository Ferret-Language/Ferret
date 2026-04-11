package common

import (
	"fmt"
	"strconv"
	"strings"

	layout "compiler/internal/analysis/layout/model"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/backend"
	"compiler/internal/frontend/ast"
	"compiler/internal/ir/mir"
)

func FindLocalByName(fn *mir.Function, name string) *mir.Local {
	if fn == nil {
		return nil
	}
	for _, local := range fn.Locals {
		if local != nil && local.Name == name {
			return local
		}
	}
	return nil
}

func FindLocalByID(fn *mir.Function, id int) *mir.Local {
	if fn == nil {
		return nil
	}
	for _, local := range fn.Locals {
		if local != nil && local.ID == id {
			return local
		}
	}
	return nil
}

func LocalNameByID(fn *mir.Function, id int) string {
	if fn == nil {
		return fmt.Sprintf("t%d", id)
	}
	for _, local := range fn.Locals {
		if local != nil && local.ID == id {
			return local.Name
		}
	}
	return fmt.Sprintf("t%d", id)
}

func LocalTypeByID(fn *mir.Function, id int) typeinfo.Type {
	if fn == nil {
		return typeinfo.UnknownType{}
	}
	for _, local := range fn.Locals {
		if local != nil && local.ID == id {
			return local.Type
		}
	}
	return typeinfo.UnknownType{}
}

func GlobalTypeByPath(current *mir.Module, modules map[string]*mir.Module, path []string) typeinfo.Type {
	if len(path) == 0 {
		return typeinfo.UnknownType{}
	}
	mod := current
	name := path[len(path)-1]
	if len(path) > 1 && modules != nil {
		if owner := modules[strings.Join(path[:len(path)-1], "/")]; owner != nil {
			mod = owner
		}
	}
	if mod == nil {
		return typeinfo.UnknownType{}
	}
	for _, global := range mod.Globals {
		if global != nil && global.Name == name {
			return global.Type
		}
	}
	return typeinfo.UnknownType{}
}

func StoredValueType(current *mir.Module, fn *mir.Function, modules map[string]*mir.Module, value mir.Value) typeinfo.Type {
	switch v := value.(type) {
	case *mir.LocalValue:
		return LocalTypeByID(fn, v.LocalID)
	case *mir.NameValue:
		if len(v.Path) == 1 {
			if local := FindLocalByName(fn, v.Path[0]); local != nil {
				return local.Type
			}
		}
		return GlobalTypeByPath(current, modules, v.Path)
	default:
		return typeinfo.UnknownType{}
	}
}

func LocalIDForPlace(fn *mir.Function, place mir.Place) (int, bool) {
	switch p := place.(type) {
	case *mir.LocalPlace:
		return p.LocalID, true
	case *mir.DerefPlace:
		addr, ok := p.Pointer.(*mir.AddrOfValue)
		if !ok {
			return 0, false
		}
		switch src := addr.Source.(type) {
		case *mir.LocalValue:
			return src.LocalID, true
		case *mir.NameValue:
			if len(src.Path) != 1 {
				return 0, false
			}
			local := FindLocalByName(fn, src.Path[0])
			if local == nil {
				return 0, false
			}
			return local.ID, true
		default:
			return 0, false
		}
	default:
		return 0, false
	}
}

func TupleIndexFromValue(value mir.Value) (int, bool) {
	num, ok := value.(*mir.NumberValue)
	if !ok || num == nil {
		return 0, false
	}
	raw := strings.ReplaceAll(strings.TrimSpace(num.Value), "_", "")
	if raw == "" || strings.ContainsAny(raw, ".eEiI") {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 0, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return int(n), true
}

// IndexElementType resolves the element type selected by an index operation
// over aggregate/pointer-like containers. It returns nil when the element type
// cannot be determined from the provided base/index pair.
func IndexElementType(base typeinfo.Type, index mir.Value) typeinfo.Type {
	switch t := backend.UnwrapNamed(base).(type) {
	case *typeinfo.ArrayType:
		return t.Inner
	case *typeinfo.SliceType:
		return t.Inner
	case *typeinfo.RawPtrType:
		return t.Inner
	case *typeinfo.TupleType:
		i, ok := TupleIndexFromValue(index)
		if !ok || i < 0 || i >= len(t.Elems) {
			return nil
		}
		return t.Elems[i]
	default:
		return nil
	}
}

type BuiltinMapCallKind uint8

const (
	BuiltinMapCallNone BuiltinMapCallKind = iota
	BuiltinMapCallSize
	BuiltinMapCallCap
	BuiltinMapCallGet
	BuiltinMapCallSet
)

func BuiltinMapCall(call *mir.CallValue) (BuiltinMapCallKind, *typeinfo.MapType, bool) {
	if call == nil {
		return BuiltinMapCallNone, nil, false
	}
	callee, ok := call.Callee.(*mir.NameValue)
	if !ok || callee == nil {
		return BuiltinMapCallNone, nil, false
	}
	name := ""
	if len(callee.Path) != 0 {
		name = callee.Path[len(callee.Path)-1]
	}
	if callee.LinkName != "" {
		switch callee.LinkName {
		case "ferret_global_size":
			name = "size"
		case "ferret_global_cap":
			name = "cap"
		case "ferret_global_get":
			name = "get"
		case "ferret_global_set":
			name = "set"
		case "ferret_global_Size":
			name = "size"
		case "ferret_global_Cap":
			name = "cap"
		case "ferret_global_Get":
			name = "get"
		case "ferret_global_Set":
			name = "set"
		}
	}
	if name == "" {
		return BuiltinMapCallNone, nil, false
	}
	mapArgIndex := 0
	switch name {
	case "size", "Size":
		mapArgIndex = 0
	case "cap", "Cap":
		mapArgIndex = 0
	case "get", "Get":
		mapArgIndex = 0
	case "set", "Set":
		mapArgIndex = 0
	default:
		return BuiltinMapCallNone, nil, false
	}
	if len(call.Args) <= mapArgIndex {
		return BuiltinMapCallNone, nil, false
	}
	mapType, ok := MapArgType(call.Args[mapArgIndex].Type())
	if !ok {
		return BuiltinMapCallNone, nil, false
	}
	switch name {
	case "size", "Size":
		return BuiltinMapCallSize, mapType, true
	case "cap", "Cap":
		return BuiltinMapCallCap, mapType, true
	case "get", "Get":
		return BuiltinMapCallGet, mapType, true
	case "set", "Set":
		return BuiltinMapCallSet, mapType, true
	default:
		return BuiltinMapCallNone, nil, false
	}
}

func MapArgType(typ typeinfo.Type) (*typeinfo.MapType, bool) {
	return backend.ResolveMapType(typ)
}

func BuiltinMapRuntimeSymbol(kind BuiltinMapCallKind) string {
	switch kind {
	case BuiltinMapCallSize:
		return "ferret_global_map_size"
	case BuiltinMapCallCap:
		return "ferret_global_map_cap"
	case BuiltinMapCallGet:
		return "ferret_global_map_get"
	case BuiltinMapCallSet:
		return "ferret_global_map_set"
	default:
		return ""
	}
}

func SanitizePath(path string) string {
	parts := strings.FieldsFunc(path, func(r rune) bool {
		switch r {
		case '/', ':', '.', '-', ' ':
			return true
		}
		return false
	})
	if len(parts) == 0 {
		return "mod"
	}
	for i := range parts {
		parts[i] = SanitizeIdent(parts[i])
	}
	return strings.Join(parts, "__")
}

// BuildModuleSymbolTables returns the backend-local symbol tables used for
// fast function/global lookups and static owner method resolution.
func BuildModuleSymbolTables(mod *mir.Module) (modulePrefix string, functions, globals map[string]struct{}) {
	functions = make(map[string]struct{})
	globals = make(map[string]struct{})
	importPath := ""
	if mod != nil {
		importPath = mod.ImportPath
	}
	modulePrefix = SanitizePath(importPath)
	localPrefix := LocalModulePrefix(importPath)
	if mod == nil {
		return modulePrefix, functions, globals
	}
	for _, fn := range mod.Functions {
		if fn == nil {
			continue
		}
		functions[fn.Name] = struct{}{}
		AddLinkLeafAlias(functions, localPrefix, fn.LinkName)
	}
	for _, g := range mod.Globals {
		if g != nil {
			globals[g.Name] = struct{}{}
		}
	}
	return modulePrefix, functions, globals
}

func SanitizeType(typ typeinfo.Type) string {
	format := "void"
	if typ != nil {
		format = typeinfo.FormatType(typ)
	}
	return SanitizeIdent(strings.NewReplacer(
		"local:", "",
		"::", "__",
		"*", "ptr_",
		" ", "_",
		"?", "opt_",
		"!", "_",
		"/", "__",
	).Replace(format))
}

func RuntimeTypeKey(typ typeinfo.Type) string {
	switch t := typ.(type) {
	case nil:
		return "void"
	case *typeinfo.NamedType:
		var b strings.Builder
		if t.ModuleKey != "" {
			b.WriteString(SanitizePath(strings.TrimPrefix(t.ModuleKey, "local:")))
			b.WriteString("__")
		}
		b.WriteString(SanitizeIdent(t.Name))
		for _, arg := range t.TypeArgs {
			b.WriteString("__")
			b.WriteString(RuntimeTypeKey(arg))
		}
		return b.String()
	case *typeinfo.BuiltinType:
		return SanitizeIdent(t.Name)
	case *typeinfo.StringType:
		return "str"
	case *typeinfo.PointerType:
		return "ptr__" + RuntimeTypeKey(t.Inner)
	case *typeinfo.RefType:
		if t.Mutable {
			return "ref_mut__" + RuntimeTypeKey(t.Inner)
		}
		return "ref__" + RuntimeTypeKey(t.Inner)
	case *typeinfo.RawPtrType:
		return "rawptr__" + RuntimeTypeKey(t.Inner)
	case *typeinfo.SliceType:
		if t.Mutable {
			return "slice_mut__" + RuntimeTypeKey(t.Inner)
		}
		return "slice__" + RuntimeTypeKey(t.Inner)
	case *typeinfo.ArrayType:
		return "array_" + strconv.FormatInt(t.Len, 10) + "__" + RuntimeTypeKey(t.Inner)
	case *typeinfo.OptionalType:
		return "opt__" + RuntimeTypeKey(t.Inner)
	case *typeinfo.TupleType:
		parts := make([]string, 0, len(t.Elems))
		for _, elem := range t.Elems {
			parts = append(parts, RuntimeTypeKey(elem))
		}
		return "tuple__" + strings.Join(parts, "__")
	case *typeinfo.UnionType:
		parts := make([]string, 0, len(t.Members))
		for _, member := range t.Members {
			parts = append(parts, RuntimeTypeKey(member))
		}
		return "union__" + strings.Join(parts, "__")
	case *typeinfo.InterfaceType:
		return "iface__" + SanitizeIdent(typeinfo.FormatType(t))
	default:
		return SanitizeType(typ)
	}
}

func SanitizeIdent(s string) string {
	if s == "" {
		return "_"
	}
	var b strings.Builder
	for i, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "_"
	}
	return out
}

func LookupInterfaceDecl(current *mir.Module, modules map[string]*mir.Module, typ typeinfo.Type, backendName string) (*mir.InterfaceTypeDecl, *typeinfo.NamedType, error) {
	named, ok := typ.(*typeinfo.NamedType)
	if !ok || named == nil {
		return nil, nil, fmt.Errorf("interface type must be named")
	}
	if named.Decl != nil {
		if ifaceDecl, ok := named.Decl.Type.(*ast.InterfaceType); ok && ifaceDecl != nil && len(ifaceDecl.Methods) == 0 {
			return &mir.InterfaceTypeDecl{Methods: nil}, named, nil
		}
	}
	mod := current
	if modules != nil {
		if owner := modules[named.ModuleKey]; owner != nil {
			mod = owner
		}
	}
	if mod == nil {
		return nil, nil, fmt.Errorf("module for interface %s is not available", named.String())
	}
	for _, decl := range mod.Types {
		if decl != nil && decl.Named != nil && decl.Named.Name == named.Name && decl.Interface != nil {
			return decl.Interface, decl.Named, nil
		}
	}
	return nil, nil, fmt.Errorf("interface type %s is not available in %s backend", named.String(), backendName)
}

func LookupInterfaceMethodDecl(current *mir.Module, modules map[string]*mir.Module, typ typeinfo.Type, name string, backendName string) (*mir.InterfaceMethodDecl, int, error) {
	iface, _, err := LookupInterfaceDecl(current, modules, typ, backendName)
	if err != nil {
		return nil, -1, err
	}
	for i, method := range iface.Methods {
		if method != nil && method.Name == name {
			return method, i, nil
		}
	}
	return nil, -1, fmt.Errorf("interface method %s not found", name)
}

func LookupNamedLayoutFromState(layouts map[string]*layout.Module, currentLayout *layout.Module, currentModule *mir.Module, named *typeinfo.NamedType, backendName string) (*layout.TypeLayout, error) {
	currentModuleKey := ""
	if currentModule != nil {
		currentModuleKey = currentModule.Key
	}
	return backend.LookupNamedLayout(layouts, currentLayout, currentModuleKey, named, backendName)
}

func LookupStructLayoutFromState(layouts map[string]*layout.Module, currentLayout *layout.Module, currentModule *mir.Module, typ typeinfo.Type, backendName string) (*layout.StructLayout, error) {
	return backend.LookupStructLayout(func(named *typeinfo.NamedType) (*layout.TypeLayout, error) {
		return LookupNamedLayoutFromState(layouts, currentLayout, currentModule, named, backendName)
	}, typ)
}
