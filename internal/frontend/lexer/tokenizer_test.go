package lexer

import (
	"compiler/internal/diagnostics"
	"compiler/internal/tokens"
	"testing"
)

func TestNumberTokenization(t *testing.T) {
	tests := []struct {
		input         string
		desc          string
		expectedFirst tokens.TOKEN
		expectedCount int
	}{
		{"1234", "Integer", tokens.NUMBER_TOKEN, 1},
		{"-1234", "Negative integer", tokens.MINUS_TOKEN, 2}, // MINUS_TOKEN + NUMBER_TOKEN
		{"1234.567", "Float", tokens.NUMBER_TOKEN, 1},
		{"-1234.567", "Negative float", tokens.MINUS_TOKEN, 2}, // MINUS_TOKEN + NUMBER_TOKEN
		{"1_234.567_89", "Float with underscores", tokens.NUMBER_TOKEN, 1},
		{"1_234", "Integer with underscores", tokens.NUMBER_TOKEN, 1},
		{"0xDEAD_BEEF", "Hex with underscores", tokens.NUMBER_TOKEN, 1},
		{"0o1_234_567", "Octal with underscores", tokens.NUMBER_TOKEN, 1},
		{"0b1010_1010", "Binary with underscores", tokens.NUMBER_TOKEN, 1},
		{"1_234.567_89e-10", "Scientific notation with underscores", tokens.NUMBER_TOKEN, 1},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {

			diag := diagnostics.NewDiagnosticBag("test")

			lex := New(tt.desc, tt.input, diag)
			toks := lex.Tokenize(false)

			if len(toks) < tt.expectedCount {
				t.Errorf("%s: expected at least %d tokens, got %d", tt.desc, tt.expectedCount, len(toks))
				return
			}

			if toks[0].Kind != tt.expectedFirst {
				t.Errorf("%s: expected %v as first token, got %v", tt.desc, tt.expectedFirst, toks[0].Kind)
			}

			// For negative numbers, verify the second token is NUMBER_TOKEN
			if tt.expectedCount == 2 && len(toks) >= 2 {
				if toks[1].Kind != tokens.NUMBER_TOKEN {
					t.Errorf("%s: expected NUMBER_TOKEN as second token, got %v", tt.desc, toks[1].Kind)
				}
			}
		})
	}
}
