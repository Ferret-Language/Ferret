package typeinfo

import (
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/core/source"
	"compiler/internal/frontend/ast"
)

type GenericRequirementKind string

const (
	GenericRequirementBinaryOp GenericRequirementKind = "binary_op"
)

type GenericRequirement struct {
	Kind     GenericRequirementKind
	Location source.Location
	Op       string
	Left     Type
	Right    Type
}

type ModuleInfo struct {
	Nodes               map[ast.Node]Type
	Symbols             map[symbols.SymbolID]Type
	SymbolIndex         map[symbols.SymbolID]*symbols.Symbol
	Bools               map[ast.Node]bool
	MethodReceivers     map[ast.Node]Type
	GenericRequirements map[symbols.SymbolID][]*GenericRequirement
}

func NewModuleInfo() *ModuleInfo {
	return &ModuleInfo{
		Nodes:               make(map[ast.Node]Type),
		Symbols:             make(map[symbols.SymbolID]Type),
		SymbolIndex:         make(map[symbols.SymbolID]*symbols.Symbol),
		Bools:               make(map[ast.Node]bool),
		MethodReceivers:     make(map[ast.Node]Type),
		GenericRequirements: make(map[symbols.SymbolID][]*GenericRequirement),
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
	m.Symbols[sym.ID] = typ
	m.SymbolIndex[sym.ID] = sym
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

func (m *ModuleInfo) BindGenericRequirements(sym *symbols.Symbol, requirements []*GenericRequirement) {
	if m == nil || sym == nil {
		return
	}
	if len(requirements) == 0 {
		delete(m.GenericRequirements, sym.ID)
		return
	}
	m.GenericRequirements[sym.ID] = requirements
}

func (m *ModuleInfo) LookupGenericRequirements(sym *symbols.Symbol) ([]*GenericRequirement, bool) {
	if m == nil || sym == nil {
		return nil, false
	}
	reqs, ok := m.GenericRequirements[sym.ID]
	return reqs, ok
}
