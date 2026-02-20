package tokens

import (
	"testing"

	"compiler/internal/source"
)

func TestIsKeyword(t *testing.T) {
	tests := []struct {
		token    string
		expected bool
	}{
		{"let", true},
		{"const", true},
		{"if", true},
		{"else", true},
		{"for", true},
		{"type", true},
		{"constraint", true},
		{"priv", true},
		{"interface", true},
		{"struct", true},
		{"fn", true},
		{"return", true},
		{"in", true},
		{"unknown", false},
	}

	for _, test := range tests {
		result := IsKeyword(test.token)
		if result != test.expected {
			t.Errorf("IsKeyword(%s) = %v; want %v", test.token, result, test.expected)
		}
	}
}

func TestNewToken(t *testing.T) {
	kind := LET_TOKEN
	value := "let"
	start := source.Position{Line: 1, Column: 1}
	end := source.Position{Line: 1, Column: 4}
	token := NewToken(kind, value, start, end)

	if token.Kind != kind {
		t.Errorf("NewToken().Kind = %v; want %v", token.Kind, kind)
	}
	if token.Value != value {
		t.Errorf("NewToken().Value = %v; want %v", token.Value, value)
	}
	if token.Start != start {
		t.Errorf("NewToken().Start = %v; want %v", token.Start, start)
	}
	if token.End != end {
		t.Errorf("NewToken().End = %v; want %v", token.End, end)
	}
}
