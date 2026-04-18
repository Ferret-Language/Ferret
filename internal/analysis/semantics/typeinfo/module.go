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
	LambdaCaptures      map[*ast.LambdaExpr][]*symbols.Symbol
	Bools               map[ast.Node]bool
	ConstValues         map[ast.Node]ConstValue
	MethodReceivers     map[ast.Node]Type
	CallArgs            map[*ast.CallExpr][]ast.Expr
	GenericRequirements map[symbols.SymbolID][]*GenericRequirement
}

func NewModuleInfo() *ModuleInfo {
	return &ModuleInfo{
		Nodes:               make(map[ast.Node]Type),
		Symbols:             make(map[symbols.SymbolID]Type),
		SymbolIndex:         make(map[symbols.SymbolID]*symbols.Symbol),
		LambdaCaptures:      make(map[*ast.LambdaExpr][]*symbols.Symbol),
		Bools:               make(map[ast.Node]bool),
		ConstValues:         make(map[ast.Node]ConstValue),
		MethodReceivers:     make(map[ast.Node]Type),
		CallArgs:            make(map[*ast.CallExpr][]ast.Expr),
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

func (m *ModuleInfo) BindConstValue(node ast.Node, value ConstValue) {
	if m == nil || node == nil || !value.Valid() {
		return
	}
	m.ConstValues[node] = value
}

func (m *ModuleInfo) LookupConstValue(node ast.Node) (ConstValue, bool) {
	if m == nil || node == nil {
		return ConstValue{}, false
	}
	value, ok := m.ConstValues[node]
	return value, ok
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

func (m *ModuleInfo) BindCallArgs(call *ast.CallExpr, args []ast.Expr) {
	if m == nil || call == nil || len(args) == 0 {
		return
	}
	m.CallArgs[call] = args
}

func (m *ModuleInfo) LookupCallArgs(call *ast.CallExpr) ([]ast.Expr, bool) {
	if m == nil || call == nil {
		return nil, false
	}
	args, ok := m.CallArgs[call]
	return args, ok
}

func (m *ModuleInfo) BindLambdaCaptures(expr *ast.LambdaExpr, captures []*symbols.Symbol) {
	if m == nil || expr == nil {
		return
	}
	if len(captures) == 0 {
		delete(m.LambdaCaptures, expr)
		return
	}
	out := make([]*symbols.Symbol, 0, len(captures))
	for _, sym := range captures {
		if sym == nil {
			continue
		}
		out = append(out, sym)
	}
	if len(out) == 0 {
		delete(m.LambdaCaptures, expr)
		return
	}
	m.LambdaCaptures[expr] = out
}

func (m *ModuleInfo) LookupLambdaCaptures(expr *ast.LambdaExpr) ([]*symbols.Symbol, bool) {
	if m == nil || expr == nil {
		return nil, false
	}
	captures, ok := m.LambdaCaptures[expr]
	return captures, ok
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
