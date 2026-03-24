package attrfilter

import (
	"testing"

	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/source"
	"compiler/internal/frontend/ast"
)

func TestFilterModuleIfAndIfNot(t *testing.T) {
	diag := diagnostics.NewDiagnosticBag("")
	ctx := context.NewWithConfig(context.Config{
		TargetOS:      "linux",
		TargetArch:    "amd64",
		TargetBackend: "llvm",
		BuildDebug:    true,
	}, diag)
	mod := &context.Module{
		AST: &ast.Module{
			Decls: []ast.Decl{
				&ast.FuncDecl{Name: &ast.Ident{Path: []string{"keepDebug"}}, Attrs: []ast.Attribute{{Name: "if", Args: []string{"debug"}}}},
				&ast.FuncDecl{Name: &ast.Ident{Path: []string{"dropOnLinux"}}, Attrs: []ast.Attribute{{Name: "ifnot", Args: []string{"target_os", "linux"}}}},
				&ast.FuncDecl{Name: &ast.Ident{Path: []string{"keepAlways"}}},
			},
		},
	}

	FilterModule(ctx, mod)
	if len(mod.AST.Decls) != 2 {
		t.Fatalf("expected 2 declarations after filter, got %d", len(mod.AST.Decls))
	}
}

func TestFilterModuleInvalidAttrReportsDiagnostic(t *testing.T) {
	diag := diagnostics.NewDiagnosticBag("")
	ctx := context.NewWithConfig(context.Config{}, diag)
	loc := source.NewLocation("main.fer", source.NewPosition(), source.NewPosition())
	mod := &context.Module{
		AST: &ast.Module{
			Decls: []ast.Decl{
				&ast.FuncDecl{
					Name:  &ast.Ident{Path: []string{"bad"}},
					Attrs: []ast.Attribute{{Name: "if", Args: []string{"unknown"}, Location: loc}},
				},
			},
		},
	}
	FilterModule(ctx, mod)
	if len(mod.AST.Decls) != 0 {
		t.Fatalf("expected declaration to be dropped for invalid attr")
	}
	if len(diag.Diagnostics()) == 0 {
		t.Fatalf("expected diagnostic for invalid attr")
	}
}

func TestExplainConfig(t *testing.T) {
	if ExplainConfig(nil) != "" {
		t.Fatalf("expected empty config explanation for nil context")
	}
	ctx := context.NewWithConfig(context.Config{TargetOS: "linux", TargetArch: "amd64", TargetBackend: "qbe", BuildDebug: true}, diagnostics.NewDiagnosticBag(""))
	text := ExplainConfig(ctx)
	if text == "" || text == `target_os="", target_arch="", target_backend="", debug=false` {
		t.Fatalf("unexpected explanation: %q", text)
	}
}
