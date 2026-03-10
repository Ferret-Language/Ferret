package typeinfo

import (
	"compiler/internal/frontend/ast"
	"compiler/internal/semantics/symbols"
)

type ModuleInfo struct {
	Nodes   map[ast.Node]Type
	Symbols map[*symbols.Symbol]Type
}

func NewModuleInfo() *ModuleInfo {
	return &ModuleInfo{
		Nodes:   make(map[ast.Node]Type),
		Symbols: make(map[*symbols.Symbol]Type),
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
