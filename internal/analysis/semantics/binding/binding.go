package binding

import (
	"strings"

	"compiler/internal/frontend/ast"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/core/source"
)

type ResolutionKind string

const (
	ResolutionSymbol ResolutionKind = "symbol"
	ResolutionModule ResolutionKind = "module"
)

type Resolution struct {
	Kind       ResolutionKind
	Symbol     *symbols.Symbol
	ModuleKey  string
	ImportPath string
	Remaining  []string
}

type ImportBinding struct {
	Name       string
	ImportPath string
	ModuleKey  string
	Segments   []string
	Location   source.Location
}

func (b *ImportBinding) Key() string {
	if b == nil {
		return ""
	}
	return strings.Join(b.Segments, "::")
}

type LabelBinding struct {
	Name     string
	Stmt     ast.Stmt
	Location source.Location
}

type ModuleInfo struct {
	Imports         []*ImportBinding
	Nodes           map[ast.Node]*Resolution
	Labels          map[ast.Node]*LabelBinding
	FunctionSymbols map[*ast.FuncDecl]*symbols.Symbol
	FunctionLocals  map[*ast.FuncDecl][]*symbols.Symbol
}

func NewModuleInfo() *ModuleInfo {
	return &ModuleInfo{
		Imports:         make([]*ImportBinding, 0),
		Nodes:           make(map[ast.Node]*Resolution),
		Labels:          make(map[ast.Node]*LabelBinding),
		FunctionSymbols: make(map[*ast.FuncDecl]*symbols.Symbol),
		FunctionLocals:  make(map[*ast.FuncDecl][]*symbols.Symbol),
	}
}

func (m *ModuleInfo) BindNode(node ast.Node, resolution *Resolution) {
	if m == nil || node == nil || resolution == nil {
		return
	}
	m.Nodes[node] = resolution
}

func (m *ModuleInfo) BindLabel(node ast.Node, label *LabelBinding) {
	if m == nil || node == nil || label == nil {
		return
	}
	m.Labels[node] = label
}

func (m *ModuleInfo) BindFunctionSymbol(fn *ast.FuncDecl, sym *symbols.Symbol) {
	if m == nil || fn == nil || sym == nil {
		return
	}
	m.FunctionSymbols[fn] = sym
}

func (m *ModuleInfo) AddFunctionLocal(fn *ast.FuncDecl, sym *symbols.Symbol) {
	if m == nil || fn == nil || sym == nil {
		return
	}
	m.FunctionLocals[fn] = append(m.FunctionLocals[fn], sym)
}
