package parser

import (
	"testing"

	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/frontend/lexer"
)

func parseTestModule(t *testing.T, src string) (*ast.Module, *diagnostics.Bag) {
	t.Helper()
	diag := diagnostics.NewBag()
	toks := lexer.New("test.ferr", src, diag).Lex()
	mod := New("test.ferr", toks, diag).ParseModule()
	return mod, diag
}

func TestParseMethodReceiverAndLiteral(t *testing.T) {
	src := `
type Point struct {
    x i32 = 0
    y i32 = 0
    static origin Point = .{}
}

fn (p Point) len2() i32 {
    if p == .{ .x = 1, .y = 2 } {
        return 1
    }
    return 0
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	if len(mod.Decls) != 2 {
		t.Fatalf("expected 2 decls, got %d", len(mod.Decls))
	}
	fn, ok := mod.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", mod.Decls[1])
	}
	if fn.Receiver == nil || fn.Receiver.Name != "p" {
		t.Fatalf("expected receiver p, got %#v", fn.Receiver)
	}
}

func TestParsePointerQualifiers(t *testing.T) {
	src := `
type Buf struct {
    data own *u8
    ptr raw *mut u8
    maybe ?own *u8
}

fn (b *mut Buf) grow(n usize) Oom!own *u8 {
    return b.ptr
}
`

	_, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
}

func TestParseLetMutConstAndComptime(t *testing.T) {
	src := `
const BuildMode = "debug"

fn add(comptime T Type, x T) T {
    let mut a: i32
    let b = comptime 1 + 2
    const local = 3
    return x
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	if len(mod.Decls) != 2 {
		t.Fatalf("expected 2 decls, got %d", len(mod.Decls))
	}
	cdecl, ok := mod.Decls[0].(*ast.ConstDecl)
	if !ok || cdecl.Name != "BuildMode" {
		t.Fatalf("expected top-level const decl, got %#v", mod.Decls[0])
	}
	fn, ok := mod.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", mod.Decls[1])
	}
	if len(fn.Params) != 2 || !fn.Params[0].IsComptime {
		t.Fatalf("expected first param to be comptime, got %#v", fn.Params)
	}
	if len(fn.Body.Stmts) != 4 {
		t.Fatalf("expected 4 statements, got %d", len(fn.Body.Stmts))
	}
	letStmt, ok := fn.Body.Stmts[0].(*ast.LetStmt)
	if !ok || !letStmt.IsMut || letStmt.Value != nil {
		t.Fatalf("expected `let mut` without initializer, got %#v", fn.Body.Stmts[0])
	}
	exprLet, ok := fn.Body.Stmts[1].(*ast.LetStmt)
	if !ok {
		t.Fatalf("expected second stmt to be let, got %T", fn.Body.Stmts[1])
	}
	bin, ok := exprLet.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected comptime prefix expr, got %#v", exprLet.Value)
	}
	prefix, ok := bin.Left.(*ast.PrefixExpr)
	if !ok || prefix.Op != "comptime" {
		t.Fatalf("expected comptime prefix on left side, got %#v", exprLet.Value)
	}
	localConst, ok := fn.Body.Stmts[2].(*ast.ConstStmt)
	if !ok || localConst.Name != "local" {
		t.Fatalf("expected local const stmt, got %#v", fn.Body.Stmts[2])
	}
}

func TestParseCopyPrefixExpression(t *testing.T) {
	src := `
fn ClonePoint(p Point) Point {
    return copy p
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", mod.Decls[0])
	}
	ret, ok := fn.Body.Stmts[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", fn.Body.Stmts[0])
	}
	prefix, ok := ret.Value.(*ast.PrefixExpr)
	if !ok || prefix.Op != "copy" {
		t.Fatalf("expected `copy` prefix expr, got %#v", ret.Value)
	}
}
