package common

import (
	"fmt"
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
