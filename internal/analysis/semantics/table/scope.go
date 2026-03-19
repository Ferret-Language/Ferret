package table

import "compiler/internal/analysis/semantics/symbols"

type Scope struct {
	parent *Scope
	byName map[string]symbols.SymbolID
	byID   map[symbols.SymbolID]*symbols.Symbol
	order  []symbols.SymbolID
}

func New(parent *Scope) *Scope {
	return &Scope{
		parent: parent,
		byName: make(map[string]symbols.SymbolID),
		byID:   make(map[symbols.SymbolID]*symbols.Symbol),
		order:  make([]symbols.SymbolID, 0),
	}
}

func (s *Scope) Declare(sym *symbols.Symbol) bool {
	if s == nil || sym == nil {
		return false
	}
	if _, exists := s.byName[sym.Name]; exists {
		return false
	}
	s.byName[sym.Name] = sym.ID
	s.byID[sym.ID] = sym
	s.order = append(s.order, sym.ID)
	return true
}

func (s *Scope) LookupLocal(name string) (*symbols.Symbol, bool) {
	if s == nil {
		return nil, false
	}
	id, ok := s.byName[name]
	if !ok {
		return nil, false
	}
	sym := s.byID[id]
	return sym, sym != nil
}

func (s *Scope) Lookup(name string) (*symbols.Symbol, bool) {
	for scope := s; scope != nil; scope = scope.parent {
		if id, ok := scope.byName[name]; ok {
			sym := scope.byID[id]
			if sym != nil {
				return sym, true
			}
			return nil, false
		}
	}
	return nil, false
}

func (s *Scope) Symbols() []*symbols.Symbol {
	if s == nil {
		return nil
	}
	out := make([]*symbols.Symbol, 0, len(s.order))
	for _, id := range s.order {
		if sym := s.byID[id]; sym != nil {
			out = append(out, sym)
		}
	}
	return out
}
