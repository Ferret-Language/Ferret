package table

import "compiler/internal/semantics/symbols"

type Scope struct {
	parent  *Scope
	symbols map[string]*symbols.Symbol
	order   []*symbols.Symbol
}

func New(parent *Scope) *Scope {
	return &Scope{
		parent:  parent,
		symbols: make(map[string]*symbols.Symbol),
		order:   make([]*symbols.Symbol, 0),
	}
}

func (s *Scope) Declare(sym *symbols.Symbol) bool {
	if s == nil || sym == nil {
		return false
	}
	if _, exists := s.symbols[sym.Name]; exists {
		return false
	}
	s.symbols[sym.Name] = sym
	s.order = append(s.order, sym)
	return true
}

func (s *Scope) LookupLocal(name string) (*symbols.Symbol, bool) {
	if s == nil {
		return nil, false
	}
	sym, ok := s.symbols[name]
	return sym, ok
}

func (s *Scope) Lookup(name string) (*symbols.Symbol, bool) {
	for scope := s; scope != nil; scope = scope.parent {
		if sym, ok := scope.symbols[name]; ok {
			return sym, true
		}
	}
	return nil, false
}

func (s *Scope) Symbols() []*symbols.Symbol {
	if s == nil {
		return nil
	}
	out := make([]*symbols.Symbol, len(s.order))
	copy(out, s.order)
	return out
}
