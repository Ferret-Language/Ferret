package typechecker

import (
	"compiler/internal/core/context"
	"compiler/internal/frontend/ast"
	"compiler/internal/core/phase"
	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/typeinfo"
)

// refineScope is a narrow, per-branch overlay that can override the type of a
// resolved symbol (used for `is` narrowing and match-type arms).
//
// Base types always live in `Module.Types` and are keyed by `*symbols.Symbol`.
type refineScope struct {
	parent *refineScope
	types  map[*symbols.Symbol]typeinfo.Type
}

func newRefineScope(parent *refineScope) *refineScope {
	return &refineScope{parent: parent, types: make(map[*symbols.Symbol]typeinfo.Type)}
}

func (s *refineScope) Set(sym *symbols.Symbol, typ typeinfo.Type) {
	if s == nil || sym == nil || typ == nil {
		return
	}
	s.types[sym] = typ
}

func (s *refineScope) Lookup(sym *symbols.Symbol) (typeinfo.Type, bool) {
	if sym == nil {
		return nil, false
	}
	for scope := s; scope != nil; scope = scope.parent {
		if typ, ok := scope.types[sym]; ok {
			return typ, true
		}
	}
	return nil, false
}

type checker struct {
	ctx           *context.CompilerContext
	mod           *context.Module
	info          *typeinfo.ModuleInfo
	currentResult typeinfo.Type
	unsafeDepth   int
	deferDepth    int
}

func (c *checker) symbolMutable(sym *symbols.Symbol) bool {
	if sym == nil {
		return false
	}
	if sym.Kind == symbols.SymbolConst {
		return false
	}
	switch node := sym.Node.(type) {
	case *ast.LetDecl:
		return node != nil && node.IsMut
	case *ast.LetStmt:
		return node != nil && node.IsMut
	case *ast.ConstDecl, *ast.ConstStmt:
		return false
	default:
		return sym.Mutable
	}
}

func (c *checker) bindDeclSymbol(node ast.Node, typ typeinfo.Type) {
	if c == nil || c.info == nil || node == nil || typ == nil {
		return
	}
	res := c.lookupResolution(node)
	if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
		return
	}
	c.info.BindSymbol(res.Symbol, typ)
}

func (c *checker) declSymbol(node ast.Node) *symbols.Symbol {
	if c == nil || node == nil {
		return nil
	}
	res := c.lookupResolution(node)
	if res == nil || res.Kind != binding.ResolutionSymbol {
		return nil
	}
	return res.Symbol
}

func CheckModule(ctx *context.CompilerContext, mod *context.Module) {
	if ctx == nil || mod == nil || mod.AST == nil || mod.Bindings == nil {
		return
	}
	c := &checker{
		ctx:  ctx,
		mod:  mod,
		info: typeinfo.NewModuleInfo(),
	}

	for _, sym := range mod.ModuleScope.Symbols() {
		c.info.BindSymbol(sym, c.typeOfSymbol(sym))
	}
	for _, members := range mod.TypeMembers {
		for _, sym := range members {
			c.info.BindSymbol(sym, c.typeOfSymbol(sym))
		}
	}

	for _, decl := range mod.AST.Decls {
		c.checkDecl(decl)
	}

	mod.Types = c.info
	mod.Phase = phase.PhaseTypeChecked
}
