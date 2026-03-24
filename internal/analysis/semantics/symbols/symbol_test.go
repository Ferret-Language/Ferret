package symbols

import (
	"testing"

	"compiler/internal/core/source"
	"compiler/internal/frontend/ast"
)

func TestIsPubName(t *testing.T) {
	if !IsPubName("Point") {
		t.Fatalf("expected uppercase identifier to be public")
	}
	if IsPubName("point") {
		t.Fatalf("expected lowercase identifier to be private")
	}
	if IsPubName("") {
		t.Fatalf("expected empty identifier to be private")
	}
}

func TestNewSymbolAssignsLocationAndID(t *testing.T) {
	loc := source.NewLocation("main.fer", source.NewPosition(), source.NewPosition())
	node := &ast.Ident{Path: []string{"X"}, Location: loc}

	s1 := New("X", SymbolVar, node)
	s2 := New("y", SymbolVar, nil)
	if s1 == nil || s2 == nil {
		t.Fatalf("expected non-nil symbols")
	}
	if s1.ID == 0 || s2.ID == 0 || s1.ID == s2.ID {
		t.Fatalf("expected unique non-zero symbol ids")
	}
	if s1.Location.Start == nil {
		t.Fatalf("expected location copied from node")
	}
	if s2.Location.Start != nil {
		t.Fatalf("expected zero location for nil node")
	}
	if !s1.IsPub || s2.IsPub {
		t.Fatalf("publicness mismatch: s1=%v s2=%v", s1.IsPub, s2.IsPub)
	}
}
