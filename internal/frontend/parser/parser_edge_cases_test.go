package parser

import (
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/lexer"
	"strings"
	"testing"
)

// TestParserEdgeCases tests edge cases and error handling in the parser
func TestParserEdgeCases(t *testing.T) {
	tests := []struct {
		name          string
		source        string
		expectError   bool
		errorContains string
	}{
		{
			name:          "Anonymous function",
			source:        "fn(){}",
			expectError:   true,
			errorContains: "expected ';'",
		},
		{
			name:          "Missing semicolon after variable declaration",
			source:        "let x := 42",
			expectError:   true,
			errorContains: "expected ';'",
		},
		{
			name:          "Missing semicolon after const declaration",
			source:        "const PI := 3.14",
			expectError:   true,
			errorContains: "expected ';'",
		},
		{
			name:          "Unclosed string literal",
			source:        `let s := "hello`,
			expectError:   true,
			errorContains: "", // Lexer error
		},
		{
			name:          "Unclosed parenthesis in expression",
			source:        "let x := (42 + 10;",
			expectError:   true,
			errorContains: "",
		},
		{
			name:          "Unclosed brace in block",
			source:        "fn test() { let x := 42;",
			expectError:   true,
			errorContains: "",
		},
		{
			name:          "Import after non-import statement",
			source:        `let x := 42; import "std/math";`,
			expectError:   true,
			errorContains: "import statements must appear before",
		},
		{
			name:        "Multiple imports followed by code (valid)",
			source:      `import "a"; import "b"; let x := 42;`,
			expectError: false,
		},
		{
			name:        "Empty function body",
			source:      "fn test() -> i32 { }",
			expectError: false, // Semantic error, not parse error
		},
		{
			name:        "Function with no return type",
			source:      "fn test() { let x := 42; }",
			expectError: false,
		},
		{
			name:        "Nested blocks",
			source:      "fn test() { { { let x := 42; } } }",
			expectError: false,
		},
		{
			name:        "String literal with escaped quotes",
			source:      `let s := "{\"a\":\"b\"}";`,
			expectError: false,
		},
		{
			name:          "Invalid token at top level",
			source:        "+++",
			expectError:   true,
			errorContains: "unexpected token",
		},
		{
			name:          "Type declaration with missing semicolon",
			source:        "type Point struct { .x: i32 }",
			expectError:   true,
			errorContains: "expected ';'",
		},
		{
			name:        "Struct literal without type annotation",
			source:      "let p := { .x = 1, .y = 2 };",
			expectError: false, // Valid anonymous struct
		},
		{
			name:        "Multiple variable declarations",
			source:      "let x := 1, y := 2, z := 3;",
			expectError: false,
		},
		{
			name:        "Heap type annotation",
			source:      "let x: #i32 = #1;",
			expectError: false,
		},
		{
			name:          "Invalid binary expression (missing operand)",
			source:        "let x := 42 +;",
			expectError:   true,
			errorContains: "",
		},
		{
			name:          "Consecutive operators",
			source:        "let x := 42 + + 10;",
			expectError:   true, // Parser doesn't handle double unary well
			errorContains: "",
		},
		{
			name:          "Missing return type with arrow",
			source:        "fn test() -> { }",
			expectError:   true,
			errorContains: "",
		},
		{
			name:        "Double assignment",
			source:      "let x := 42; x = 10;",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diag := diagnostics.NewDiagnosticBag("test.fer")
			lex := lexer.New("test.fer", tt.source, diag)
			tokens := lex.Tokenize(false)

			module := Parse(tokens, "test.fer", diag)

			hasError := diag.HasErrors()

			if tt.expectError && !hasError {
				t.Errorf("Expected error but got none for: %s", tt.source)
			}

			if !tt.expectError && hasError {
				t.Errorf("Expected no error but got error for: %s\nErrors: %d", tt.source, diag.ErrorCount())
				for _, d := range diag.Diagnostics() {
					t.Logf("  %s: %s", d.Severity, d.Message)
				}
			}

			if tt.errorContains != "" && hasError {
				found := false
				for _, d := range diag.Diagnostics() {
					if d.Severity == diagnostics.Error && strings.Contains(d.Message, tt.errorContains) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected error containing %q but didn't find it", tt.errorContains)
					for _, d := range diag.Diagnostics() {
						t.Logf("  Got: %s", d.Message)
					}
				}
			}

			// Ensure we got some AST even with errors (for error recovery testing)
			if module == nil {
				t.Error("Parser returned nil module")
			}
		})
	}
}

// TestParserRecovery tests that parser continues after errors
func TestParserRecovery(t *testing.T) {
	source := `
	let x := 42
	let y := "hello";
	const Z := 3.14;
	fn test() -> i32 { return 42; }
	`

	diag := diagnostics.NewDiagnosticBag("test.fer")
	lex := lexer.New("test.fer", source, diag)
	tokens := lex.Tokenize(false)

	module := Parse(tokens, "test.fer", diag)

	// Should have error for missing semicolon on first let
	if !diag.HasErrors() {
		t.Error("Expected error for missing semicolon")
	}

	// But should still parse other statements
	if module == nil {
		t.Fatal("Parser should return module even with errors")
	}

	// Should have parsed at least some statements
	if len(module.Nodes) < 3 {
		t.Errorf("Expected to parse multiple statements despite error, got %d", len(module.Nodes))
	}
}

// TestParserValidPrograms tests that valid programs parse without errors
func TestParserValidPrograms(t *testing.T) {
	validPrograms := []string{
		"let x := 42;",
		"const PI := 3.14;",
		"fn add(a: i32, b: i32) -> i32 { return a + b; }",
		"type Point struct { .x: i32, .y: i32 };",
		"type Color enum { Red, Green, Blue };",
		"constraint numeric = union { i32, i64, i128, i256 };",
		"constraint signed_like = union { ~i32, ~i64, ~i128, ~i256 };",
		"constraint writer = interface { Write(buf: []byte) -> i32 };",
		"constraint numeric_writer = numeric & writer;",
		"type Box<T> struct { .value: T };",
		"type Pair<T, U> struct { .left: T, .right: U };",
		"fn id<T>(x: T) -> T { return x; }",
		"fn test() { let a := id<i32>(1); }",
		"fn test() { let a := id(1); }",
		"type Point struct { .x: i32 }; fn (p: Point) apply<T>(x: T) -> T { return x; }",
		"fn test() { let ok := 1 < 2; }",
		`import "std/math"; let x := 42;`,
		"fn test() { let x := 1; if x > 0 { let y := 1; } }",
		"fn test() { for i in 0..10 { let x := i; } }",
		"fn test() { while true { let x := 1; } }",
		"let p := { .x = 1, .y = 2 } as Point;",
		"fn (p: Point) distance() -> f64 { return 0.0; }",
		"type Handle struct { .id: i32 }; fn (h: Handle) Close();",
		"fn test() { let x := 0; x = x + 1; }",
	}

	for _, source := range validPrograms {
		t.Run(source, func(t *testing.T) {
			diag := diagnostics.NewDiagnosticBag("test.fer")
			lex := lexer.New("test.fer", source, diag)
			tokens := lex.Tokenize(false)

			module := Parse(tokens, "test.fer", diag)

			if diag.HasErrors() {
				t.Errorf("Valid program should not have errors: %s", source)
				for _, d := range diag.Diagnostics() {
					if d.Severity == diagnostics.Error {
						t.Logf("  Error: %s", d.Message)
					}
				}
			}

			if module == nil {
				t.Error("Parser returned nil for valid program")
			}
		})
	}
}
