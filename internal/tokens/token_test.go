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
	if !IsBuiltinType("i128") || !IsBuiltinType("u1024") {
		t.Fatalf("expected arbitrary-width builtin integers to be recognized")
	}
	if IsBuiltinType("i24") || IsBuiltinType("u04") || IsBuiltinType("i3") {
		t.Fatalf("expected invalid builtin integer widths to be rejected")
	}
	s := (Token{Kind: IDENT, Literal: "x"}).String()
	if !strings.Contains(s, "IDENT") || !strings.Contains(s, "\"x\"") {
		t.Fatalf("unexpected token string: %q", s)
	}
}

func TestParseIntegerBuiltin(t *testing.T) {
	tests := []struct {
		name   string
		signed bool
		bits   int
		ok     bool
	}{
		{name: "i128", signed: true, bits: 128, ok: true},
		{name: "u1024", signed: false, bits: 1024, ok: true},
		{name: "isize", signed: true, bits: 64, ok: true},
		{name: "usize", signed: false, bits: 64, ok: true},
		{name: "i24", ok: false},
		{name: "u04", ok: false},
		{name: "foo", ok: false},
	}
	for _, tt := range tests {
		signed, bits, ok := ParseIntegerBuiltin(tt.name)
		if signed != tt.signed || bits != tt.bits || ok != tt.ok {
			t.Fatalf("ParseIntegerBuiltin(%q) = (%v, %d, %v)", tt.name, signed, bits, ok)
		}
	}
}
