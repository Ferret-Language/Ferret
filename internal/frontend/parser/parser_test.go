package parser

import (
	"strings"
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

func hasDiagnosticMessage(diag *diagnostics.Bag, substr string) bool {
	for _, d := range diag.All() {
		if strings.Contains(d.Message, substr) {
			return true
		}
	}
	return false
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
	typ, ok := mod.Decls[0].(*ast.TypeDecl)
	if !ok {
		t.Fatalf("expected type decl, got %T", mod.Decls[0])
	}
	st, ok := typ.Type.(*ast.StructType)
	if !ok {
		t.Fatalf("expected struct type, got %T", typ.Type)
	}
	if len(st.Fields) != 2 || len(st.StaticFields) != 1 {
		t.Fatalf("expected 2 instance fields and 1 static field, got %#v", st)
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
let mut GlobalCount: i32 = 0
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
	if len(mod.Decls) != 3 {
		t.Fatalf("expected 3 decls, got %d", len(mod.Decls))
	}
	ldecl, ok := mod.Decls[0].(*ast.LetDecl)
	if !ok || ldecl.Name != "GlobalCount" || !ldecl.IsMut {
		t.Fatalf("expected top-level let decl, got %#v", mod.Decls[0])
	}
	cdecl, ok := mod.Decls[1].(*ast.ConstDecl)
	if !ok || cdecl.Name != "BuildMode" {
		t.Fatalf("expected top-level const decl, got %#v", mod.Decls[1])
	}
	fn, ok := mod.Decls[2].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", mod.Decls[2])
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

func TestParseCastExpression(t *testing.T) {
	src := `
fn CastIt(x i32) i8 {
    return x as i8
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn := mod.Decls[0].(*ast.FuncDecl)
	ret := fn.Body.Stmts[0].(*ast.ReturnStmt)
	cast, ok := ret.Value.(*ast.CastExpr)
	if !ok {
		t.Fatalf("expected cast expr, got %T", ret.Value)
	}
	if _, ok := cast.Left.(*ast.Ident); !ok {
		t.Fatalf("expected cast lhs ident, got %T", cast.Left)
	}
	if typ, ok := cast.Type.(*ast.NamedType); !ok || len(typ.Path) != 1 || typ.Path[0] != "i8" {
		t.Fatalf("expected cast target i8, got %#v", cast.Type)
	}
}

func TestParseAssignmentAndWhileLoop(t *testing.T) {
	src := `
fn run() i32 {
    let mut x: i32 = 0
    while x < 10 {
        x = x + 1
        if x == 5 {
            break
        }
        continue
    }
    return x
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
	if len(fn.Body.Stmts) != 3 {
		t.Fatalf("expected 3 statements, got %d", len(fn.Body.Stmts))
	}
	loop, ok := fn.Body.Stmts[1].(*ast.WhileStmt)
	if !ok {
		t.Fatalf("expected while stmt, got %T", fn.Body.Stmts[1])
	}
	if len(loop.Body.Stmts) != 3 {
		t.Fatalf("expected 3 loop statements, got %d", len(loop.Body.Stmts))
	}
	assign, ok := loop.Body.Stmts[0].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("expected assignment stmt, got %T", loop.Body.Stmts[0])
	}
	if _, ok := assign.Left.(*ast.Ident); !ok {
		t.Fatalf("expected assignment lhs ident, got %T", assign.Left)
	}
	ifStmt, ok := loop.Body.Stmts[1].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected if stmt, got %T", loop.Body.Stmts[1])
	}
	if _, ok := ifStmt.Then.Stmts[0].(*ast.BreakStmt); !ok {
		t.Fatalf("expected break stmt in if body, got %T", ifStmt.Then.Stmts[0])
	}
	if _, ok := loop.Body.Stmts[2].(*ast.ContinueStmt); !ok {
		t.Fatalf("expected continue stmt, got %T", loop.Body.Stmts[2])
	}
}

func TestParseForLoopForms(t *testing.T) {
	src := `
fn run() i32 {
    let mut sum: i32 = 0

    for {
        break
    }

    for sum < 10 {
        sum = sum + 1
    }

    for let mut i: i32 = 0; i < 3; i = i + 1 {
        sum = sum + i
    }

    return sum
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
	if len(fn.Body.Stmts) != 5 {
		t.Fatalf("expected 5 statements, got %d", len(fn.Body.Stmts))
	}

	loop0, ok := fn.Body.Stmts[1].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected first loop to be for, got %T", fn.Body.Stmts[1])
	}
	if loop0.Init != nil || loop0.Cond != nil || loop0.Post != nil {
		t.Fatalf("expected infinite loop form, got %#v", loop0)
	}

	loop1, ok := fn.Body.Stmts[2].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected second loop to be for, got %T", fn.Body.Stmts[2])
	}
	if loop1.Cond == nil || loop1.Init != nil || loop1.Post != nil {
		t.Fatalf("expected condition-only loop, got %#v", loop1)
	}

	loop2, ok := fn.Body.Stmts[3].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected third loop to be for, got %T", fn.Body.Stmts[3])
	}
	if _, ok := loop2.Init.(*ast.LetStmt); !ok {
		t.Fatalf("expected for init let stmt, got %T", loop2.Init)
	}
	if loop2.Cond == nil {
		t.Fatalf("expected for condition")
	}
	if _, ok := loop2.Post.(*ast.AssignStmt); !ok {
		t.Fatalf("expected for post assignment, got %T", loop2.Post)
	}
}

func TestParserRejectsInvalidForConditionForm(t *testing.T) {
	src := `
fn run() void {
    for let mut x: i32 = 0 {
        return
    }
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(got), got)
	}
	if !hasDiagnosticMessage(diag, "expected for condition or ';' after for initializer") {
		t.Fatalf("expected invalid for header diagnostic, got %v", diag.All())
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", mod.Decls[0])
	}
	loop, ok := fn.Body.Stmts[0].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected for stmt, got %T", fn.Body.Stmts[0])
	}
	if loop.Body == nil || len(loop.Body.Stmts) != 1 {
		t.Fatalf("expected loop body to remain parsed, got %#v", loop.Body)
	}
	if _, ok := loop.Body.Stmts[0].(*ast.ReturnStmt); !ok {
		t.Fatalf("expected recovered return stmt, got %T", loop.Body.Stmts[0])
	}
}

func TestParseLabelsAndLabeledLoopControl(t *testing.T) {
	src := `
fn run() void {
    outer: for {
        inner: while true {
            break outer
        }
        continue outer
    }
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
	outer, ok := fn.Body.Stmts[0].(*ast.LabelStmt)
	if !ok || outer.Name != "outer" {
		t.Fatalf("expected outer label stmt, got %#v", fn.Body.Stmts[0])
	}
	outerLoop, ok := outer.Stmt.(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected labeled for stmt, got %T", outer.Stmt)
	}
	inner, ok := outerLoop.Body.Stmts[0].(*ast.LabelStmt)
	if !ok || inner.Name != "inner" {
		t.Fatalf("expected inner label stmt, got %#v", outerLoop.Body.Stmts[0])
	}
	innerLoop, ok := inner.Stmt.(*ast.WhileStmt)
	if !ok {
		t.Fatalf("expected labeled while stmt, got %T", inner.Stmt)
	}
	breakStmt, ok := innerLoop.Body.Stmts[0].(*ast.BreakStmt)
	if !ok || breakStmt.Label != "outer" {
		t.Fatalf("expected labeled break outer, got %#v", innerLoop.Body.Stmts[0])
	}
	continueStmt, ok := outerLoop.Body.Stmts[1].(*ast.ContinueStmt)
	if !ok || continueStmt.Label != "outer" {
		t.Fatalf("expected labeled continue outer, got %#v", outerLoop.Body.Stmts[1])
	}
}

func TestParserRecoversLabeledStatementBody(t *testing.T) {
	src := `
fn run() void {
    outer: x =
    return
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(got), got)
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", mod.Decls[0])
	}
	if len(fn.Body.Stmts) != 2 {
		t.Fatalf("expected 2 statements after recovery, got %d", len(fn.Body.Stmts))
	}
	labelStmt, ok := fn.Body.Stmts[0].(*ast.LabelStmt)
	if !ok || labelStmt.Name != "outer" {
		t.Fatalf("expected labeled stmt, got %#v", fn.Body.Stmts[0])
	}
	assign, ok := labelStmt.Stmt.(*ast.AssignStmt)
	if !ok {
		t.Fatalf("expected labeled assignment stmt, got %T", labelStmt.Stmt)
	}
	if _, ok := assign.Right.(*ast.BadExpr); !ok {
		t.Fatalf("expected bad expr on invalid labeled assignment, got %T", assign.Right)
	}
	if _, ok := fn.Body.Stmts[1].(*ast.ReturnStmt); !ok {
		t.Fatalf("expected recovered return stmt, got %T", fn.Body.Stmts[1])
	}
}

func TestParserRecoversAfterIncompleteAssignment(t *testing.T) {
	src := `
fn run() i32 {
    let mut x: i32 = 0
    while x < 10 {
        x = x +
        if x == 5 {
            break
        }
        continue
    }
    return x
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(got), got)
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", mod.Decls[0])
	}
	if len(fn.Body.Stmts) != 3 {
		t.Fatalf("expected function body to recover fully, got %d statements", len(fn.Body.Stmts))
	}
	loop, ok := fn.Body.Stmts[1].(*ast.WhileStmt)
	if !ok {
		t.Fatalf("expected while stmt, got %T", fn.Body.Stmts[1])
	}
	if len(loop.Body.Stmts) != 3 {
		t.Fatalf("expected loop body to recover fully, got %d statements", len(loop.Body.Stmts))
	}
	if _, ok := loop.Body.Stmts[1].(*ast.IfStmt); !ok {
		t.Fatalf("expected recovered if stmt, got %T", loop.Body.Stmts[1])
	}
	if _, ok := loop.Body.Stmts[2].(*ast.ContinueStmt); !ok {
		t.Fatalf("expected recovered continue stmt, got %T", loop.Body.Stmts[2])
	}
}

func TestParseImportAliasAndDestructor(t *testing.T) {
	src := `
import "json/parser" as json

type Conn struct {}

fn (c *mut Conn) Conn(fd i32) {
}

fn (c own *Conn) ~Conn() {
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	if len(mod.Imports) != 1 || mod.Imports[0].Alias != "json" {
		t.Fatalf("expected aliased import, got %#v", mod.Imports)
	}
	ctor, ok := mod.Decls[1].(*ast.FuncDecl)
	if !ok || !ctor.IsConstructor || ctor.Name != "Conn" {
		t.Fatalf("expected constructor method, got %#v", mod.Decls[1])
	}
	fn, ok := mod.Decls[2].(*ast.FuncDecl)
	if !ok || !fn.IsDestructor || fn.Name != "Conn" {
		t.Fatalf("expected destructor method, got %#v", mod.Decls[2])
	}
}

func TestParseElseIfDeferLockUnsafeAndBuiltins(t *testing.T) {
	src := `
fn run(m Mutex, cond bool) void {
    if cond {
        defer recover()
    } else if panic("bad") {
        unsafe {
            lock m as g {
                defer panic("boom")
            }
        }
    } else {
        return
    }
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", mod.Decls[0])
	}
	ifStmt, ok := fn.Body.Stmts[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected if stmt, got %T", fn.Body.Stmts[0])
	}
	if _, ok := ifStmt.Then.Stmts[0].(*ast.DeferStmt); !ok {
		t.Fatalf("expected defer in then branch, got %T", ifStmt.Then.Stmts[0])
	}
	elseIf, ok := ifStmt.Else.(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected else-if stmt, got %T", ifStmt.Else)
	}
	unsafeStmt, ok := elseIf.Then.Stmts[0].(*ast.UnsafeStmt)
	if !ok {
		t.Fatalf("expected unsafe stmt, got %T", elseIf.Then.Stmts[0])
	}
	lockStmt, ok := unsafeStmt.Body.Stmts[0].(*ast.LockStmt)
	if !ok || lockStmt.Name != "g" {
		t.Fatalf("expected lock stmt with guard g, got %#v", unsafeStmt.Body.Stmts[0])
	}
	deferStmt, ok := lockStmt.Body.Stmts[0].(*ast.DeferStmt)
	if !ok {
		t.Fatalf("expected nested defer stmt, got %T", lockStmt.Body.Stmts[0])
	}
	exprStmt, ok := deferStmt.Body.(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected defer expr stmt, got %T", deferStmt.Body)
	}
	call, ok := exprStmt.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected panic call, got %T", exprStmt.Value)
	}
	callee, ok := call.Callee.(*ast.Ident)
	if !ok || len(callee.Path) != 1 || callee.Path[0] != "panic" {
		t.Fatalf("expected panic callee, got %#v", call.Callee)
	}
	if _, ok := elseIf.Else.(*ast.BlockStmt); !ok {
		t.Fatalf("expected else block, got %T", elseIf.Else)
	}
}

func TestParseUnsafeExpression(t *testing.T) {
	src := `
fn run(ptr raw *i32) i32 {
    let x = unsafe *ptr
    unsafe panic("bad")
    return x
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", mod.Decls[0])
	}
	letStmt, ok := fn.Body.Stmts[0].(*ast.LetStmt)
	if !ok {
		t.Fatalf("expected let stmt, got %T", fn.Body.Stmts[0])
	}
	if _, ok := letStmt.Value.(*ast.UnsafeExpr); !ok {
		t.Fatalf("expected unsafe expr in let init, got %T", letStmt.Value)
	}
	exprStmt, ok := fn.Body.Stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected expr stmt, got %T", fn.Body.Stmts[1])
	}
	if _, ok := exprStmt.Value.(*ast.UnsafeExpr); !ok {
		t.Fatalf("expected unsafe expr stmt, got %T", exprStmt.Value)
	}
}

func TestParserRejectsMixedCompositeLiteralForms(t *testing.T) {
	src := `
fn build() Point {
    let p: Point = .{ .x = 1, 2 }
    return p
}
`

	_, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(got), got)
	}
	if !hasDiagnosticMessage(diag, "cannot mix named and positional composite elements") {
		t.Fatalf("expected mixed composite literal diagnostic, got %v", diag.All())
	}
}

func TestParserRejectsDuplicateTypeMembers(t *testing.T) {
	src := `
type Point struct {
    x i32
    static x i32
    x i32
}

type Shape interface {
    area() i32
    area() i32
}

type Color enum {
    red,
    red,
}

type Io error {
    denied,
    denied,
}

type Token union {
    i32,
    i32,
}
`

	_, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 6 {
		t.Fatalf("expected 6 diagnostics, got %d: %v", len(got), got)
	}
	for _, substr := range []string{
		`duplicate field "x"`,
		`duplicate interface method "area"`,
		`duplicate enum variant "red"`,
		`duplicate error member "denied"`,
		`duplicate union member "named:[i32]"`,
	} {
		if !hasDiagnosticMessage(diag, substr) {
			t.Fatalf("expected diagnostic containing %q, got %v", substr, diag.All())
		}
	}
}

func TestParserRecoversStructBodyMembers(t *testing.T) {
	src := `
type Point struct {
    x =
    y i32 = 1
}

fn build() i32 {
    return 1
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(got), got)
	}
	typ, ok := mod.Decls[0].(*ast.TypeDecl)
	if !ok {
		t.Fatalf("expected type decl, got %T", mod.Decls[0])
	}
	st, ok := typ.Type.(*ast.StructType)
	if !ok {
		t.Fatalf("expected struct type, got %T", typ.Type)
	}
	if len(st.Fields) != 2 {
		t.Fatalf("expected struct recovery to keep both fields, got %d", len(st.Fields))
	}
	if st.Fields[1].Name != "y" {
		t.Fatalf("expected second recovered field y, got %#v", st.Fields[1])
	}
	if len(mod.Decls) != 2 {
		t.Fatalf("expected next top-level declaration to survive, got %d decls", len(mod.Decls))
	}
	if _, ok := mod.Decls[1].(*ast.FuncDecl); !ok {
		t.Fatalf("expected recovered function decl, got %T", mod.Decls[1])
	}
}

func TestParserRecoversMissingCompositeComma(t *testing.T) {
	src := `
fn build() Point {
    let p: Point = .{ .x = 1 .y = 2 }
    return p
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(got), got)
	}
	if !hasDiagnosticMessage(diag, "expected ',' or '}' after composite literal element") {
		t.Fatalf("expected missing comma diagnostic, got %v", diag.All())
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", mod.Decls[0])
	}
	if len(fn.Body.Stmts) != 2 {
		t.Fatalf("expected full function body recovery, got %d statements", len(fn.Body.Stmts))
	}
	letStmt, ok := fn.Body.Stmts[0].(*ast.LetStmt)
	if !ok {
		t.Fatalf("expected let stmt, got %T", fn.Body.Stmts[0])
	}
	lit, ok := letStmt.Value.(*ast.CompositeLit)
	if !ok {
		t.Fatalf("expected composite literal, got %T", letStmt.Value)
	}
	if len(lit.Items) != 2 {
		t.Fatalf("expected recovered composite literal with 2 items, got %d", len(lit.Items))
	}
	if _, ok := fn.Body.Stmts[1].(*ast.ReturnStmt); !ok {
		t.Fatalf("expected recovered return stmt, got %T", fn.Body.Stmts[1])
	}
}

func TestParserRecoversMissingArgumentComma(t *testing.T) {
	src := `
fn run() void {
    panic("a" "b")
    return
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(got), got)
	}
	if !hasDiagnosticMessage(diag, "expected ',' or ')' after argument") {
		t.Fatalf("expected missing argument comma diagnostic, got %v", diag.All())
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", mod.Decls[0])
	}
	if len(fn.Body.Stmts) != 2 {
		t.Fatalf("expected full function body recovery, got %d statements", len(fn.Body.Stmts))
	}
	exprStmt, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected expr stmt, got %T", fn.Body.Stmts[0])
	}
	call, ok := exprStmt.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected call expr, got %T", exprStmt.Value)
	}
	if len(call.Args) != 2 {
		t.Fatalf("expected recovered call with 2 args, got %d", len(call.Args))
	}
	if _, ok := fn.Body.Stmts[1].(*ast.ReturnStmt); !ok {
		t.Fatalf("expected recovered return stmt, got %T", fn.Body.Stmts[1])
	}
}
