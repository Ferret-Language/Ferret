package typeinfo

import (
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/frontend/ast"
)

type ModuleInfo struct {
	Nodes           map[ast.Node]Type
	Symbols         map[*symbols.Symbol]Type
	Bools           map[ast.Node]bool
	MethodReceivers map[ast.Node]Type
}

func NewModuleInfo() *ModuleInfo {
	return &ModuleInfo{
		Nodes:           make(map[ast.Node]Type),
		Symbols:         make(map[*symbols.Symbol]Type),
		Bools:           make(map[ast.Node]bool),
		MethodReceivers: make(map[ast.Node]Type),
	}
}

func (m *ModuleInfo) BindNode(node ast.Node, typ Type) {
	if m == nil || node == nil || typ == nil {
		return
	}
	m.Nodes[node] = typ
}

func (m *ModuleInfo) BindSymbol(sym *symbols.Symbol, typ Type) {
	if m == nil || sym == nil || typ == nil {
		return
	}
	m.Symbols[sym] = typ
}

func (m *ModuleInfo) BindBool(node ast.Node, value bool) {
	if m == nil || node == nil {
		return
	}
	m.Bools[node] = value
}

func (m *ModuleInfo) LookupBool(node ast.Node) (bool, bool) {
	if m == nil || node == nil {
		return false, false
	}
	value, ok := m.Bools[node]
	return value, ok
}

func (m *ModuleInfo) BindMethodReceiver(node ast.Node, typ Type) {
	if m == nil || node == nil || typ == nil {
		return
	}
	m.MethodReceivers[node] = typ
}

func (m *ModuleInfo) LookupMethodReceiver(node ast.Node) (Type, bool) {
	if m == nil || node == nil {
		return nil, false
	}
	typ, ok := m.MethodReceivers[node]
	return typ, ok
}
