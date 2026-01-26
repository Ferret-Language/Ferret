package analysis_test

import (
	"compiler/internal/context_v2"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/lexer"
	"compiler/internal/frontend/parser"
	"compiler/internal/hir/analysis"
	"compiler/internal/hir/gen"
	"compiler/internal/semantics/collector"
	"compiler/internal/semantics/table"
	"compiler/internal/semantics/typechecker"
	"strings"
	"testing"
)

func parseCollectCheckAndAnalyze(t *testing.T, src string) (*context_v2.Module, *context_v2.CompilerContext) {
	t.Helper()

	cfg := &context_v2.Config{
		ProjectName: "test",
		ProjectRoot: t.TempDir(),
		Extension:   ".fer",
	}
	ctx := context_v2.New(cfg, false)
	mod := &context_v2.Module{
		FilePath:       "test.fer",
		Content:        src,
		ModuleScope:    table.NewSymbolTable(ctx.Universe),
		ImportPath:     "test",
		Imports:        make(map[string]*context_v2.Import),
		ImportAliasMap: make(map[string]string),
	}
	mod.CurrentScope = mod.ModuleScope

	diag := diagnostics.NewDiagnosticBag(mod.FilePath)
	lex := lexer.New(mod.FilePath, src, diag)
	tokens := lex.Tokenize(false)
	mod.AST = parser.Parse(tokens, mod.FilePath, diag)

	ctx.AddModule(mod.ImportPath, mod)

	collector.CollectModule(ctx, mod)
	typechecker.TypeCheckTopLevelSignatures(ctx, mod)
	typechecker.CheckModule(ctx, mod)

	hirMod := gen.New(ctx, mod).GenerateModule()
	analysis.AnalyzeModule(ctx, mod, hirMod)
	return mod, ctx
}

func TestIfConstantConditions(t *testing.T) {
	tests := []struct {
		name          string
		code          string
		expectWarning bool
		warningCode   string
		contains      string
	}{
		{
			name:          "if_true_literal",
			code:          "fn main() { if true { let x := 1; } else { let y := 2; } }",
			expectWarning: true,
			warningCode:   diagnostics.WarnConstantConditionTrue,
			contains:      "always true",
		},
		{
			name:          "if_false_literal",
			code:          "fn main() { if false { let x := 1; } else { let y := 2; } }",
			expectWarning: true,
			warningCode:   diagnostics.WarnConstantConditionFalse,
			contains:      "always false",
		},
		{
			name:          "if_const_identifier",
			code:          "fn main() { const FLAG := true; if FLAG { let x := 1; } else { let y := 2; } }",
			expectWarning: true,
			warningCode:   diagnostics.WarnConstantConditionTrue,
			contains:      "always true",
		},
		{
			name:          "if_arithmetic_comparison",
			code:          "fn main() { if 1 + 1 == 2 { let x := 1; } else { let y := 2; } }",
			expectWarning: true,
			warningCode:   diagnostics.WarnConstantConditionTrue,
			contains:      "always true",
		},
		{
			name:          "if_constant_propagation",
			code:          "fn main() { let a := 10; let b := 20; if a < b { let x := 1; } else { let y := 2; } }",
			expectWarning: true,
			warningCode:   diagnostics.WarnConstantConditionTrue,
			contains:      "always true",
		},
		{
			name:          "if_function_call_no_warning",
			code:          "fn getCondition() -> bool { return true; } fn main() { if getCondition() { let x := 1; } else { let y := 2; } }",
			expectWarning: false,
		},
		{
			name:          "if_no_else_branch",
			code:          "fn main() { if true { let x := 1; } }",
			expectWarning: true,
			warningCode:   diagnostics.WarnConstantConditionTrue,
			contains:      "always true",
		},
		{
			name:          "if_false_warns_then_dead",
			code:          "fn main() { if false { let x := 1; } }",
			expectWarning: true,
			warningCode:   diagnostics.WarnConstantConditionFalse,
			contains:      "always false",
		},
		{
			name:          "if_logical_and",
			code:          "fn main() { if true && false { let x := 1; } else { let y := 2; } }",
			expectWarning: true,
			warningCode:   diagnostics.WarnConstantConditionFalse,
			contains:      "always false",
		},
		{
			name:          "if_logical_or",
			code:          "fn main() { if true || false { let x := 1; } else { let y := 2; } }",
			expectWarning: true,
			warningCode:   diagnostics.WarnConstantConditionTrue,
			contains:      "always true",
		},
		{
			name:          "if_negation",
			code:          "fn main() { if !false { let x := 1; } else { let y := 2; } }",
			expectWarning: true,
			warningCode:   diagnostics.WarnConstantConditionTrue,
			contains:      "always true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ctx := parseCollectCheckAndAnalyze(t, tt.code)
			diags := ctx.Diagnostics.Diagnostics()

			if tt.expectWarning {
				if len(diags) == 0 {
					t.Errorf("Expected warning but got none")
					return
				}

				foundWarning := false
				for _, d := range diags {
					if d.Severity == diagnostics.Warning &&
						d.Code == tt.warningCode &&
						strings.Contains(d.Message, tt.contains) {
						foundWarning = true
						break
					}
				}

				if !foundWarning {
					t.Errorf("Expected warning with code %s containing %q, but got:", tt.warningCode, tt.contains)
					for _, d := range diags {
						t.Logf("  - [%s] %s: %s", d.Code, d.Severity, d.Message)
					}
				}
			} else {
				for _, d := range diags {
					if d.Severity == diagnostics.Warning {
						t.Errorf("Expected no warning but got: [%s] %s", d.Code, d.Message)
					}
				}
			}
		})
	}
}

func TestWhileConstantConditions(t *testing.T) {
	tests := []struct {
		name          string
		code          string
		expectWarning bool
		warningCode   string
		contains      string
	}{
		{
			name:          "while_false_literal",
			code:          "fn main() { while false { let x := 1; } }",
			expectWarning: true,
			warningCode:   diagnostics.WarnConstantConditionFalse,
			contains:      "always false",
		},
		{
			name:          "while_true_no_warning",
			code:          "fn main() { while true { return; } }", // Has escape via return
			expectWarning: false,
		},
		{
			name:          "while_const_false",
			code:          "fn main() { const FLAG := false; while FLAG { let x := 1; } }",
			expectWarning: true,
			warningCode:   diagnostics.WarnConstantConditionFalse,
			contains:      "always false",
		},
		{
			name:          "while_arithmetic_false",
			code:          "fn main() { while 1 > 2 { let x := 1; } }",
			expectWarning: true,
			warningCode:   diagnostics.WarnConstantConditionFalse,
			contains:      "always false",
		},
		{
			name:          "while_logical_and_false",
			code:          "fn main() { while true && false { let x := 1; } }",
			expectWarning: true,
			warningCode:   diagnostics.WarnConstantConditionFalse,
			contains:      "always false",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ctx := parseCollectCheckAndAnalyze(t, tt.code)
			diags := ctx.Diagnostics.Diagnostics()

			if tt.expectWarning {
				foundWarning := false
				for _, d := range diags {
					if d.Severity == diagnostics.Warning &&
						d.Code == tt.warningCode &&
						strings.Contains(d.Message, tt.contains) {
						foundWarning = true
						break
					}
				}

				if !foundWarning {
					t.Errorf("Expected warning with code %s containing %q, but got:", tt.warningCode, tt.contains)
					for _, d := range diags {
						t.Logf("  - [%s] %s: %s", d.Code, d.Severity, d.Message)
					}
				}
			} else {
				for _, d := range diags {
					if d.Severity == diagnostics.Warning &&
						d.Code == diagnostics.WarnConstantConditionTrue ||
						d.Code == diagnostics.WarnConstantConditionFalse {
						t.Errorf("Expected no constant condition warning but got: [%s] %s", d.Code, d.Message)
					}
				}
			}
		})
	}
}
