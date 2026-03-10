package lexer

import (
	"testing"

	"compiler/internal/diagnostics"
	"compiler/internal/tokens"
)

func TestLexCompositeLiteralAndReceiverTokens(t *testing.T) {
	src := `fn (p *mut Point) shift(dx i32) { let q: Point = .{ .x = 1 } }`
	diag := diagnostics.NewBag()
	out := New("test.ferr", src, diag).Lex()
	if len(diag.All()) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diag.All())
	}
	foundDoubleDotStyle := false
	for i := 0; i < len(out)-1; i++ {
		if out[i].Kind == tokens.DOT && out[i+1].Kind == tokens.LBRACE {
			foundDoubleDotStyle = true
			break
		}
	}
	if !foundDoubleDotStyle {
		t.Fatalf("expected .{ token sequence")
	}
}

func TestLexAllNumberForms(t *testing.T) {
	cases := []string{
		`1234`,
		`1_234`,
		`1234.567`,
		`1_234.567_89`,
		`1e10`,
		`1_234.567_89e-10`,
		`0xDEAD_BEEF`,
		`0o1_234_567`,
		`0b1010_1010`,
		`42i`,
	}

	for _, src := range cases {
		diag := diagnostics.NewBag()
		out := New("test.ferr", src, diag).Lex()
		if len(diag.All()) != 0 {
			t.Fatalf("%s: unexpected diagnostics: %v", src, diag.All())
		}
		if len(out) < 2 {
			t.Fatalf("%s: expected token and EOF, got %d tokens", src, len(out))
		}
		if out[0].Kind != tokens.NUMBER {
			t.Fatalf("%s: expected NUMBER token, got %s (%q)", src, out[0].Kind, out[0].Literal)
		}
	}
}

func TestLexLoopControlKeywords(t *testing.T) {
	src := `while cond { break; continue; }`
	diag := diagnostics.NewBag()
	out := New("test.ferr", src, diag).Lex()
	if len(diag.All()) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diag.All())
	}
	if len(out) < 6 {
		t.Fatalf("expected several tokens, got %d", len(out))
	}
	if out[0].Kind != tokens.WHILE {
		t.Fatalf("expected WHILE token, got %s", out[0].Kind)
	}
	if out[3].Kind != tokens.BREAK {
		t.Fatalf("expected BREAK token, got %s", out[3].Kind)
	}
	if out[5].Kind != tokens.CONTINUE {
		t.Fatalf("expected CONTINUE token, got %s", out[5].Kind)
	}
}
