package diagnostics

import (
	"testing"

	"compiler/colors"
)

func TestSyntaxHighlighterKeepsPrefixedAndScientificNumbersTogether(t *testing.T) {
	sh := NewSyntaxHighlighter(true)
	tokens := sh.Highlight(`0b4234 0x1f 0o7 1.5e2`)
	wantText := []string{"0b4234", " ", "0x1f", " ", "0o7", " ", "1.5e2"}
	wantColor := []colors.COLOR{
		colors.LIGHT_ORANGE,
		colors.WHITE,
		colors.LIGHT_ORANGE,
		colors.WHITE,
		colors.LIGHT_ORANGE,
		colors.WHITE,
		colors.LIGHT_ORANGE,
	}
	if len(tokens) != len(wantText) {
		t.Fatalf("expected %d tokens, got %d: %#v", len(wantText), len(tokens), tokens)
	}
	for i, tok := range tokens {
		if tok.Text != wantText[i] || tok.Color != wantColor[i] {
			t.Fatalf("token %d = %#v, want text=%q color=%v", i, tok, wantText[i], wantColor[i])
		}
	}
}
