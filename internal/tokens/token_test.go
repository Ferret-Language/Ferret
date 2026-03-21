package tokens

import (
	"strings"
	"testing"
)

func TestLookupIdentAndKeywordHelpers(t *testing.T) {
	if got := LookupIdent("if"); got != IF {
		t.Fatalf("LookupIdent(if) = %v", got)
	}
	if got := LookupIdent("custom"); got != IDENT {
		t.Fatalf("LookupIdent(custom) = %v", got)
	}
	if !IsKeyword("while") || IsKeyword("custom") {
		t.Fatalf("IsKeyword results unexpected")
	}
}

func TestBuiltinTypeAndStringer(t *testing.T) {
	if !IsBuiltinType("i32") || IsBuiltinType("Point") {
		t.Fatalf("IsBuiltinType results unexpected")
	}
	s := (Token{Kind: IDENT, Literal: "x"}).String()
	if !strings.Contains(s, "IDENT") || !strings.Contains(s, "\"x\"") {
		t.Fatalf("unexpected token string: %q", s)
	}
}
