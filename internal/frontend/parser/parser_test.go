package parser

import (
	"strings"
	"testing"

	"compiler/internal/core/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/frontend/lexer"
)

func parseTestModule(t *testing.T, src string) (*ast.Module, *diagnostics.Bag) {
	t.Helper()
	diag := diagnostics.NewBag()
	toks := lexer.New("test.ferr", src, diag).Tokenize()
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

func findDiagnosticWithMessage(diag *diagnostics.Bag, substr string) *diagnostics.Diagnostic {
	for _, d := range diag.All() {
		if strings.Contains(d.Message, substr) {
			return d
		}
	}
	return nil
}

func TestParseMethodReceiverAndLiteral(t *testing.T) {
	src := `
type Point struct {
    x: i32 = 0
    y: i32 = 0
}
fn Point::len2(self) i32 {
    if self == .{ .x = 1, .y = 2 } {
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
	if len(st.Fields) != 2 {
		t.Fatalf("expected 2 instance fields, got %#v", st)
	}
	fn, ok := mod.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", mod.Decls[1])
	}
	if fn.OwnerType == nil || len(fn.OwnerType.Path) == 0 || fn.OwnerType.Path[len(fn.OwnerType.Path)-1] != "Point" {
		t.Fatalf("expected owner type Point, got %#v", fn.OwnerType)
	}
	if fn.IsStatic {
		t.Fatalf("expected instance method")
	}
	if fn.Receiver == nil || fn.Receiver.Name.Text() != "self" {
		t.Fatalf("expected receiver self, got %#v", fn.Receiver)
	}
}

func TestParserRejectsLegacyReceiverSyntax(t *testing.T) {
	src := `
type Point struct {}

fn (p: Point) Len() i32 {
    return 0
}
`
	_, diag := parseTestModule(t, src)
	if !hasDiagnosticMessage(diag, "legacy receiver syntax has been removed") {
		t.Fatalf("expected legacy receiver rejection diagnostic, got %v", diag.All())
	}
}

func TestParseInterfaceReceiverModifiers(t *testing.T) {
	src := `
type Reader interface {
    read(self, buf: []u8) i32
    peek(&self) u8
    refill(&mut self) i32
    close(*self) void
    New() Reader
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	typ, ok := mod.Decls[0].(*ast.TypeDecl)
	if !ok {
		t.Fatalf("expected type decl, got %T", mod.Decls[0])
	}
	iface, ok := typ.Type.(*ast.InterfaceType)
	if !ok {
		t.Fatalf("expected interface type, got %T", typ.Type)
	}
	if got := iface.Methods[0].Receiver; got != "" {
		t.Fatalf("expected value receiver, got %q", got)
	}
	if got := iface.Methods[1].Receiver; got != "&" {
		t.Fatalf("expected & receiver, got %q", got)
	}
	if got := iface.Methods[2].Receiver; got != "&mut " {
		t.Fatalf("expected &mut receiver, got %q", got)
	}
	if got := iface.Methods[3].Receiver; got != "*" {
		t.Fatalf("expected * receiver, got %q", got)
	}
	if !iface.Methods[4].Static {
		t.Fatalf("expected static method")
	}
}

func TestParseIfAttributeOnTypeDecl(t *testing.T) {
	src := `
#[if(target_os, "linux")]
type Handle enum {
    stdin,
    stdout,
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	typ, ok := mod.Decls[0].(*ast.TypeDecl)
	if !ok {
		t.Fatalf("expected type decl, got %T", mod.Decls[0])
	}
	if len(typ.Attrs) != 1 || typ.Attrs[0].Name != "if" {
		t.Fatalf("expected #[if(...)] attr on type decl, got %#v", typ.Attrs)
	}
}

func TestParseLetMutConstAndComptime(t *testing.T) {
	src := `
let mut GlobalCount: i32 = 0
const BuildMode = "debug"

fn add(comptime T: Type, x: T) T {
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
	if !ok || ldecl.Name.Text() != "GlobalCount" || !ldecl.IsMut {
		t.Fatalf("expected top-level let decl, got %#v", mod.Decls[0])
	}
	cdecl, ok := mod.Decls[1].(*ast.ConstDecl)
	if !ok || cdecl.Name.Text() != "BuildMode" {
		t.Fatalf("expected top-level const decl, got %#v", mod.Decls[1])
	}
	fn, ok := mod.Decls[2].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", mod.Decls[2])
	}
	if len(fn.Params) != 2 || !fn.Params[0].IsComptime {
		t.Fatalf("expected first param to be comptime, got %#v", fn.Params)
	}
	if fn.Params[1].IsMut {
		t.Fatalf("did not expect second param to be mutable, got %#v", fn.Params[1])
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
	if !ok || localConst.Name.Text() != "local" {
		t.Fatalf("expected local const stmt, got %#v", fn.Body.Stmts[2])
	}
}

func TestParseMutableParameter(t *testing.T) {
	src := `
fn set(mut x: i32, y: i32) i32 {
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
	if len(fn.Params) != 2 || !fn.Params[0].IsMut || fn.Params[1].IsMut {
		t.Fatalf("expected only first param to be mutable, got %#v", fn.Params)
	}
}

func TestParseDeclarationTypeParams(t *testing.T) {
	src := `
type Box<T> struct {
    Value: T
}

fn Map<T, U: any>(value: T) U {
    return value as U
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	box, ok := mod.Decls[0].(*ast.TypeDecl)
	if !ok {
		t.Fatalf("expected type decl, got %T", mod.Decls[0])
	}
	if len(box.TypeParams) != 1 || box.TypeParams[0].Name.Text() != "T" {
		t.Fatalf("expected Box<T>, got %#v", box.TypeParams)
	}
	fn, ok := mod.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", mod.Decls[1])
	}
	if len(fn.TypeParams) != 2 {
		t.Fatalf("expected 2 type params, got %#v", fn.TypeParams)
	}
	if fn.TypeParams[0].Name.Text() != "T" {
		t.Fatalf("expected first type param T, got %#v", fn.TypeParams[0])
	}
	constraint, ok := fn.TypeParams[1].Constraint.(*ast.NamedType)
	if !ok || len(constraint.Path) != 1 || constraint.Path[0] != "any" {
		t.Fatalf("expected U constraint any, got %#v", fn.TypeParams[1].Constraint)
	}
}

func TestParseGenericCallWithAngleTypeArgs(t *testing.T) {
	src := `
fn add<T>(a: T, b: T) T {
    return a
}

fn main() i32 {
    return add<i32>(1, 2)
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn, ok := mod.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected main function, got %T", mod.Decls[1])
	}
	ret, ok := fn.Body.Stmts[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return statement, got %T", fn.Body.Stmts[0])
	}
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected call expression, got %T", ret.Value)
	}
	if len(call.TypeArgs) != 1 {
		t.Fatalf("expected 1 type argument, got %d", len(call.TypeArgs))
	}
	arg, ok := call.TypeArgs[0].(*ast.NamedType)
	if !ok || len(arg.Path) != 1 || arg.Path[0] != "i32" {
		t.Fatalf("expected type argument i32, got %#v", call.TypeArgs[0])
	}
}

func TestParseStaticMethodCallWithGenericOwnerTypeArgs(t *testing.T) {
	src := `
type Circle<T> struct {
    Rad: T
}

fn Circle<T>::New(v: T) Self {
    return .{ .Rad = v }
}

fn main() Circle<i32> {
    return Circle<i32>::New(1)
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn, ok := mod.Decls[2].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected main function, got %T", mod.Decls[2])
	}
	ret, ok := fn.Body.Stmts[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return statement, got %T", fn.Body.Stmts[0])
	}
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected call expression, got %T", ret.Value)
	}
	if len(call.TypeArgs) != 0 {
		t.Fatalf("expected no function type args, got %d", len(call.TypeArgs))
	}
	callee, ok := call.Callee.(*ast.Ident)
	if !ok {
		t.Fatalf("expected callee ident, got %T", call.Callee)
	}
	if got := callee.Text(); got != "Circle<i32>::New" {
		t.Fatalf("expected callee Circle<i32>::New, got %q", got)
	}
	if len(callee.TypeArgs) != 1 {
		t.Fatalf("expected 1 owner type arg, got %d", len(callee.TypeArgs))
	}
	arg, ok := callee.TypeArgs[0].(*ast.NamedType)
	if !ok || len(arg.Path) != 1 || arg.Path[0] != "i32" {
		t.Fatalf("expected owner type arg i32, got %#v", callee.TypeArgs[0])
	}
}

func TestParseCopyPrefixExpression(t *testing.T) {
	src := `
fn ClonePoint(p: Point) Point {
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

func TestParseBuiltinDeclarationWithDocComment(t *testing.T) {
	src := `
/// Returns the current panic payload as a string.
/// Returns an empty string when there is no active panic.
#[builtin]
fn recover() string;
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	if len(mod.Decls) != 1 {
		t.Fatalf("expected 1 decl, got %d", len(mod.Decls))
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", mod.Decls[0])
	}
	if fn.Doc == nil {
		t.Fatal("expected doc comment to be attached to builtin declaration")
	}
	if !strings.Contains(fn.Doc.Text, "panic payload") {
		t.Fatalf("unexpected doc text: %q", fn.Doc.Text)
	}
	if len(fn.Attrs) != 1 || fn.Attrs[0].Name != "builtin" {
		t.Fatalf("expected #[builtin] attribute, got %#v", fn.Attrs)
	}
	if !fn.IsExtern {
		t.Fatal("expected builtin declaration to also be marked foreign/extern")
	}
	if fn.ExternName != "" {
		t.Fatalf("expected builtin declaration without link override to use default mangling, got %q", fn.ExternName)
	}
	if fn.Body != nil {
		t.Fatalf("expected builtin declaration to have no body, got %#v", fn.Body)
	}
}

func TestParseDeclarationDocsFromLineAndBlockComments(t *testing.T) {
	src := `
// Point docs.
type Point struct {}

// mutable binding docs
let mut x = 1

/* constant docs */
const Y = 2

// Adds one.
// Returns incremented value.
fn addOne(v: i32) i32 {
    return v + 1
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	if len(mod.Decls) != 4 {
		t.Fatalf("expected 4 decls, got %d", len(mod.Decls))
	}

	typ, ok := mod.Decls[0].(*ast.TypeDecl)
	if !ok || typ.Doc == nil || !strings.Contains(typ.Doc.Text, "Point docs.") {
		t.Fatalf("expected type doc from // comment, got %#v", mod.Decls[0])
	}
	letDecl, ok := mod.Decls[1].(*ast.LetDecl)
	if !ok || letDecl.Doc == nil || !strings.Contains(letDecl.Doc.Text, "mutable binding docs") {
		t.Fatalf("expected let doc from // comment, got %#v", mod.Decls[1])
	}
	constDecl, ok := mod.Decls[2].(*ast.ConstDecl)
	if !ok || constDecl.Doc == nil || !strings.Contains(constDecl.Doc.Text, "constant docs") {
		t.Fatalf("expected const doc from /* */ comment, got %#v", mod.Decls[2])
	}
	fn, ok := mod.Decls[3].(*ast.FuncDecl)
	if !ok || fn.Doc == nil {
		t.Fatalf("expected function doc from // comments, got %#v", mod.Decls[3])
	}
	if !strings.Contains(fn.Doc.Text, "Adds one.") || !strings.Contains(fn.Doc.Text, "Returns incremented value.") {
		t.Fatalf("unexpected function doc text: %q", fn.Doc.Text)
	}
}

func TestParseDocCommentGapDoesNotAttach(t *testing.T) {
	src := `
// this should not attach due to gap

fn noDoc() void {}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", mod.Decls[0])
	}
	if fn.Doc != nil {
		t.Fatalf("expected no doc attachment across blank line, got %q", fn.Doc.Text)
	}
}

func TestParseRegularCommentsRemainIgnored(t *testing.T) {
	src := `
fn main() void
// comment between signature and body should be ignored
{
    let x = 1
    // local comment should be ignored
    x
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
	if fn.Doc != nil {
		t.Fatalf("expected function to have no doc, got %q", fn.Doc.Text)
	}
}

func TestParseBlockDocCommentPreservesLines(t *testing.T) {
	src := `
/**
 * block line 1
 * block line 2
 */
fn f() void {}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok || fn.Doc == nil {
		t.Fatalf("expected function doc from block comment, got %#v", mod.Decls[0])
	}
	if !strings.Contains(fn.Doc.Text, "block line 1") || !strings.Contains(fn.Doc.Text, "block line 2") {
		t.Fatalf("unexpected block doc text: %q", fn.Doc.Text)
	}
	if !strings.Contains(fn.Doc.Text, "\n") {
		t.Fatalf("expected multiline block doc text, got %q", fn.Doc.Text)
	}
}

func TestParseLocalBindingDocs(t *testing.T) {
	src := `
fn main() void {
    // local mutable binding doc
    let mut x = 1

    /* local constant doc */
    const y = 2

    x
    y
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
	if len(fn.Body.Stmts) < 2 {
		t.Fatalf("expected at least 2 statements, got %d", len(fn.Body.Stmts))
	}
	letStmt, ok := fn.Body.Stmts[0].(*ast.LetStmt)
	if !ok || letStmt.Doc == nil || !strings.Contains(letStmt.Doc.Text, "local mutable binding doc") {
		t.Fatalf("expected doc on local let binding, got %#v", fn.Body.Stmts[0])
	}
	constStmt, ok := fn.Body.Stmts[1].(*ast.ConstStmt)
	if !ok || constStmt.Doc == nil || !strings.Contains(constStmt.Doc.Text, "local constant doc") {
		t.Fatalf("expected doc on local const binding, got %#v", fn.Body.Stmts[1])
	}
}

func TestParseExternDeclarationWithLinkName(t *testing.T) {
	src := `
/// Writes a string to stdout.
#[extern("ferret_io_println")]
fn Println(text: string) void;
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", mod.Decls[0])
	}
	if !fn.IsExtern || fn.ExternName != "ferret_io_println" {
		t.Fatalf("expected extern declaration with link name, got %#v", fn)
	}
	if fn.Body != nil {
		t.Fatalf("expected extern declaration to have no body, got %#v", fn.Body)
	}
	if len(fn.Attrs) != 1 || len(fn.Attrs[0].Args) != 1 || fn.Attrs[0].Args[0] != "ferret_io_println" {
		t.Fatalf("unexpected extern attribute payload: %#v", fn.Attrs)
	}
}

func TestParseExternDeclarationWithoutLinkNameUsesDefaultMangle(t *testing.T) {
	src := `
#[extern]
fn Tick() void;
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", mod.Decls[0])
	}
	if !fn.IsExtern {
		t.Fatalf("expected extern declaration, got %#v", fn)
	}
	if fn.ExternName != "" {
		t.Fatalf("expected extern declaration without link name override, got %q", fn.ExternName)
	}
}

func TestParseCastExpression(t *testing.T) {
	src := `
fn CastIt(x: i32) i8 {
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

    for items |v| {
        sum = sum + v
    }

    for items |i, v| {
        sum = sum + i
        sum = sum + v
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
	if len(fn.Body.Stmts) != 4 {
		t.Fatalf("expected 4 statements, got %d", len(fn.Body.Stmts))
	}

	loop0, ok := fn.Body.Stmts[1].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected first loop to be for, got %T", fn.Body.Stmts[1])
	}
	if loop0.Index != nil || loop0.Value.Text() != "v" || loop0.Iterable == nil {
		t.Fatalf("expected value-only loop, got %#v", loop0)
	}

	loop1, ok := fn.Body.Stmts[2].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected second loop to be for, got %T", fn.Body.Stmts[2])
	}
	if loop1.Index.Text() != "i" || loop1.Value.Text() != "v" || loop1.Iterable == nil {
		t.Fatalf("expected index+value loop, got %#v", loop1)
	}
}

func TestParserRejectsInvalidForBindingForm(t *testing.T) {
	src := `
fn run() void {
    for items {
        return
    }
}
`

	_, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) == 0 {
		t.Fatalf("expected diagnostic, got none")
	}
	if !hasDiagnosticMessage(diag, "expected '|' after for iterable") {
		t.Fatalf("expected invalid for binding diagnostic, got %v", diag.All())
	}
}

func TestParseLabelsAndLabeledLoopControl(t *testing.T) {
	src := `
fn run() void {
    outer: for items |v| {
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
	if !ok || outer.Name.Text() != "outer" {
		t.Fatalf("expected outer label stmt, got %#v", fn.Body.Stmts[0])
	}
	outerLoop, ok := outer.Stmt.(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected labeled for stmt, got %T", outer.Stmt)
	}
	inner, ok := outerLoop.Body.Stmts[0].(*ast.LabelStmt)
	if !ok || inner.Name.Text() != "inner" {
		t.Fatalf("expected inner label stmt, got %#v", outerLoop.Body.Stmts[0])
	}
	innerLoop, ok := inner.Stmt.(*ast.WhileStmt)
	if !ok {
		t.Fatalf("expected labeled while stmt, got %T", inner.Stmt)
	}
	breakStmt, ok := innerLoop.Body.Stmts[0].(*ast.BreakStmt)
	if !ok || breakStmt.Label.Text() != "outer" {
		t.Fatalf("expected labeled break outer, got %#v", innerLoop.Body.Stmts[0])
	}
	continueStmt, ok := outerLoop.Body.Stmts[1].(*ast.ContinueStmt)
	if !ok || continueStmt.Label.Text() != "outer" {
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
	if !ok || labelStmt.Name.Text() != "outer" {
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

func TestParseImportAliasAndRejectRemovedDestructorSyntax(t *testing.T) {
	src := `
import "json/parser" as json

type Conn struct {}

fn Conn::Conn(*self, fd: i32) {
}

fn Conn::~Conn(*self) {
}
`

	mod, diag := parseTestModule(t, src)
	if len(mod.Imports) != 1 || mod.Imports[0].Alias.Text() != "json" {
		t.Fatalf("expected aliased import, got %#v", mod.Imports)
	}
	if got := diag.All(); len(got) == 0 {
		t.Fatal("expected removed destructor syntax diagnostic")
	}
	if !hasDiagnosticMessage(diag, "special destructor syntax has been removed") {
		t.Fatalf("expected removed destructor syntax diagnostic, got %v", diag.All())
	}
}

func TestParseElseIfDeferLockUnsafeAndBuiltins(t *testing.T) {
	src := `
fn run(m: Mutex, cond: bool) void {
    if cond {
        defer release m
    } else if cond {
        lock m as g {
            defer release g
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
	lockStmt, ok := elseIf.Then.Stmts[0].(*ast.LockStmt)
	if !ok || lockStmt.Name.Text() != "g" {
		t.Fatalf("expected lock stmt with guard g, got %#v", elseIf.Then.Stmts[0])
	}
	deferStmt, ok := lockStmt.Body.Stmts[0].(*ast.DeferStmt)
	if !ok {
		t.Fatalf("expected nested defer stmt, got %T", lockStmt.Body.Stmts[0])
	}
	if _, ok := deferStmt.Body.(*ast.ReleaseStmt); !ok {
		t.Fatalf("expected deferred release stmt, got %T", deferStmt.Body)
	}
	if _, ok := elseIf.Else.(*ast.BlockStmt); !ok {
		t.Fatalf("expected else block, got %T", elseIf.Else)
	}
}

func TestParseUnsafeBlockAndUnsafeFunctionCall(t *testing.T) {
	src := `
unsafe fn Read(ptr: ^i32) i32 {
    return *ptr
}

fn run(ptr: ^i32) i32 {
    let x: i32
    unsafe {
        x = Read(ptr)
        panic "bad"
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
		t.Fatalf("expected unsafe func decl, got %T", mod.Decls[0])
	}
	if !fn.IsUnsafe {
		t.Fatalf("expected first function to be unsafe")
	}
	runFn, ok := mod.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected second func decl, got %T", mod.Decls[1])
	}
	letStmt, ok := runFn.Body.Stmts[0].(*ast.LetStmt)
	if !ok {
		t.Fatalf("expected let stmt, got %T", runFn.Body.Stmts[0])
	}
	if letStmt.Value != nil {
		t.Fatalf("expected let declaration without initializer, got %T", letStmt.Value)
	}
	unsafeStmt, ok := runFn.Body.Stmts[1].(*ast.UnsafeStmt)
	if !ok {
		t.Fatalf("expected unsafe stmt, got %T", runFn.Body.Stmts[1])
	}
	assignStmt, ok := unsafeStmt.Body.Stmts[0].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("expected assignment stmt in unsafe block, got %T", unsafeStmt.Body.Stmts[0])
	}
	call, ok := assignStmt.Right.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected call expr on assignment rhs, got %T", assignStmt.Right)
	}
	callee, ok := call.Callee.(*ast.Ident)
	if !ok || len(callee.Path) != 1 || callee.Path[0] != "Read" {
		t.Fatalf("expected Read call, got %#v", call.Callee)
	}
	panicStmt, ok := unsafeStmt.Body.Stmts[1].(*ast.PanicStmt)
	if !ok {
		t.Fatalf("expected panic stmt in unsafe block, got %T", unsafeStmt.Body.Stmts[1])
	}
	if _, ok := panicStmt.Value.(*ast.StringLit); !ok {
		t.Fatalf("expected panic string payload, got %T", panicStmt.Value)
	}
}

func TestParseCatchExpressionForms(t *testing.T) {
	src := `
fn run(path: string) i32 {
    let file = open(path) catch |err| {
        log(err)
        return 0
    }
    return div(10, 0) catch -1
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn := mod.Decls[0].(*ast.FuncDecl)
	letStmt := fn.Body.Stmts[0].(*ast.LetStmt)
	blockCatch, ok := letStmt.Value.(*ast.CatchExpr)
	if !ok || blockCatch.Payload.Text() != "err" || blockCatch.Handler == nil {
		t.Fatalf("expected block catch expr, got %#v", letStmt.Value)
	}
	ret := fn.Body.Stmts[1].(*ast.ReturnStmt)
	fallbackCatch, ok := ret.Value.(*ast.CatchExpr)
	if !ok || fallbackCatch.Fallback == nil {
		t.Fatalf("expected fallback catch expr, got %#v", ret.Value)
	}
}

func TestParseIsExpression(t *testing.T) {
	src := `
fn run(x: i32) bool {
    return x is i32
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn := mod.Decls[0].(*ast.FuncDecl)
	ret := fn.Body.Stmts[0].(*ast.ReturnStmt)
	isExpr, ok := ret.Value.(*ast.IsExpr)
	if !ok {
		t.Fatalf("expected is expr, got %T", ret.Value)
	}
	left, ok := isExpr.Left.(*ast.Ident)
	if !ok || left.Text() != "x" {
		t.Fatalf("expected left ident x, got %#v", isExpr.Left)
	}
	target, ok := isExpr.Type.(*ast.NamedType)
	if !ok || len(target.Path) != 1 || target.Path[0] != "i32" {
		t.Fatalf("expected target type i32, got %#v", isExpr.Type)
	}
}

func TestParseMatchTypeArm(t *testing.T) {
	src := `
fn run(value: Token) i32 {
    match value {
        is i32 => {
            return value
        }
        _ => {
            return 0
        }
    }
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn := mod.Decls[0].(*ast.FuncDecl)
	exprStmt := fn.Body.Stmts[0].(*ast.ExprStmt)
	matchExpr := exprStmt.Value.(*ast.MatchExpr)
	arm := matchExpr.Arms[0]
	if arm.TypePattern == nil {
		t.Fatalf("expected type pattern arm, got %#v", arm)
	}
	target, ok := arm.TypePattern.(*ast.NamedType)
	if !ok || len(target.Path) != 1 || target.Path[0] != "i32" {
		t.Fatalf("expected target type i32, got %#v", arm.TypePattern)
	}
}

func TestParseMatchExpression(t *testing.T) {
	src := `
fn run(value: Token) i32 {
    let out = match value {
        is i32 => value
        _ => 0
    }
    return out
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn := mod.Decls[0].(*ast.FuncDecl)
	letStmt := fn.Body.Stmts[0].(*ast.LetStmt)
	matchExpr, ok := letStmt.Value.(*ast.MatchExpr)
	if !ok {
		t.Fatalf("expected match expr, got %T", letStmt.Value)
	}
	if len(matchExpr.Arms) != 2 {
		t.Fatalf("expected 2 match arms, got %d", len(matchExpr.Arms))
	}
	if matchExpr.Arms[0].Body == nil || len(matchExpr.Arms[0].Body.Stmts) != 1 {
		t.Fatalf("expected implicit single-expression arm body, got %#v", matchExpr.Arms[0].Body)
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
    x: i32
    x: i32
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
	if got := diag.All(); len(got) != 5 {
		t.Fatalf("expected 5 diagnostics, got %d: %v", len(got), got)
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

func TestParserRejectsStaticStructFields(t *testing.T) {
	src := `
type Point struct {
    static: x i32
}
`

	_, diag := parseTestModule(t, src)
	if !hasDiagnosticMessage(diag, "static struct fields are not supported") {
		t.Fatalf("expected static-field rejection diagnostic, got %v", diag.All())
	}
}

func TestParserRecoversStructBodyMembers(t *testing.T) {
	src := `
type Point struct {
    x: =
    y: i32 = 1
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
	if st.Fields[1].Name.Text() != "y" {
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
    foo("a" "b")
    return
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) < 1 {
		t.Fatalf("expected at least 1 diagnostic, got %d: %v", len(got), got)
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

func TestParserMissingCallCloserAnchorsAtInsertionPoint(t *testing.T) {
	src := `
fn run() void {
    print(Point::Counter
    let mut maybe : ?i32 = none
    return
}
`

	_, diag := parseTestModule(t, src)
	d := findDiagnosticWithMessage(diag, "expected ',' or ')' after argument")
	if d == nil {
		t.Fatalf("expected missing call closer diagnostic, got %v", diag.All())
	}
	if len(d.Labels) == 0 || d.Labels[0].Location == nil || d.Labels[0].Location.Start == nil || d.Labels[0].Location.End == nil {
		t.Fatalf("expected diagnostic label location, got %#v", d.Labels)
	}
	start := d.Labels[0].Location.Start
	end := d.Labels[0].Location.End
	if start.Line != 3 || start.Column != 25 {
		t.Fatalf("expected insertion anchor at line 3 col 25, got %d:%d", start.Line, start.Column)
	}
	if end.Line != start.Line || end.Column != start.Column {
		t.Fatalf("expected zero-width insertion range, got %d:%d-%d:%d", start.Line, start.Column, end.Line, end.Column)
	}
}

func TestParserMissingCallCloserDoesNotCascadeExpectedRBrace(t *testing.T) {
	src := `
fn main() {
    io::Println("Hello world"
}
`

	_, diag := parseTestModule(t, src)
	if !hasDiagnosticMessage(diag, "expected ',' or ')' after argument") {
		t.Fatalf("expected missing call closer diagnostic, got %v", diag.All())
	}
	if hasDiagnosticMessage(diag, "expected '}'") {
		t.Fatalf("unexpected cascading expected '}' diagnostic: %v", diag.All())
	}
}

func TestParseNewReferenceAndRawTypes(t *testing.T) {
	src := `
type Node struct {
    next: *Node
    view: &Node
    edit: &mut Node
    data: ^u8
}

fn Node::peek(&self) &Node {
    return self.view
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	typ, ok := mod.Decls[0].(*ast.TypeDecl)
	if !ok {
		t.Fatalf("expected type decl, got %T", mod.Decls[0])
	}
	st, ok := typ.Type.(*ast.StructType)
	if !ok {
		t.Fatalf("expected struct type, got %T", typ.Type)
	}
	if _, ok := st.Fields[0].Type.(*ast.PointerType); !ok {
		t.Fatalf("expected owning pointer type, got %T", st.Fields[0].Type)
	}
	ref, ok := st.Fields[1].Type.(*ast.RefType)
	if !ok || ref.Mutable {
		t.Fatalf("expected immutable ref type, got %#v", st.Fields[1].Type)
	}
	mutRef, ok := st.Fields[2].Type.(*ast.RefType)
	if !ok || !mutRef.Mutable {
		t.Fatalf("expected mutable ref type, got %#v", st.Fields[2].Type)
	}
	if _, ok := st.Fields[3].Type.(*ast.RawPtrType); !ok {
		t.Fatalf("expected raw ptr type, got %T", st.Fields[3].Type)
	}
	fn, ok := mod.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", mod.Decls[1])
	}
	if fn.OwnerType == nil || len(fn.OwnerType.Path) == 0 || fn.OwnerType.Path[len(fn.OwnerType.Path)-1] != "Node" {
		t.Fatalf("expected owner type Node, got %#v", fn.OwnerType)
	}
	recv, ok := fn.Receiver.Type.(*ast.RefType)
	if !ok || recv.Mutable {
		t.Fatalf("expected immutable receiver ref type, got %#v", fn.Receiver.Type)
	}
	ret, ok := fn.Result.(*ast.RefType)
	if !ok || ret.Mutable {
		t.Fatalf("expected immutable ref result type, got %#v", fn.Result)
	}
}

func TestParseInferredArrayLengthType(t *testing.T) {
	src := `
fn main() {
    let values: [_]i32 = [_]i32{1, 2, 3}
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn := mod.Decls[0].(*ast.FuncDecl)
	letStmt := fn.Body.Stmts[0].(*ast.LetStmt)
	arr, ok := letStmt.Type.(*ast.ArrayType)
	if !ok {
		t.Fatalf("expected array type, got %T", letStmt.Type)
	}
	size, ok := arr.Size.(*ast.Ident)
	if !ok || size.Text() != "_" {
		t.Fatalf("expected inferred array length marker, got %#v", arr.Size)
	}
	lit, ok := letStmt.Value.(*ast.CompositeLit)
	if !ok {
		t.Fatalf("expected composite literal value, got %T", letStmt.Value)
	}
	litType, ok := lit.Type.(*ast.ArrayType)
	if !ok {
		t.Fatalf("expected typed array literal, got %#v", lit.Type)
	}
	litSize, ok := litType.Size.(*ast.Ident)
	if !ok || litSize.Text() != "_" {
		t.Fatalf("expected inferred array length marker on literal, got %#v", litType.Size)
	}
}

func TestParseStaticAttachedMethod(t *testing.T) {
	src := `
type Point struct {}

fn Point::New() Point {
    return .{}
}
`
	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn := mod.Decls[1].(*ast.FuncDecl)
	if fn.OwnerType == nil || len(fn.OwnerType.Path) == 0 || fn.OwnerType.Path[len(fn.OwnerType.Path)-1] != "Point" || !fn.IsStatic {
		t.Fatalf("expected static attached method, got %#v", fn)
	}
	if fn.Receiver != nil {
		t.Fatalf("expected no receiver for static method, got %#v", fn.Receiver)
	}
}

func TestParseSelfTypeAndTypedCompositeLiteral(t *testing.T) {
	src := `
type Shape interface {
    New() Self
    Draw(&self)
}

type Point struct {
    X: i32 = 0
}

fn Point::Clone(&self) Self {
    return .Point{}
}
`
	mod, diag := parseTestModule(t, src)
	if got := diag.All(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	iface := mod.Decls[0].(*ast.TypeDecl).Type.(*ast.InterfaceType)
	if _, ok := iface.Methods[0].Result.(*ast.SelfType); !ok {
		t.Fatalf("expected Self return type, got %#v", iface.Methods[0].Result)
	}
	fn := mod.Decls[2].(*ast.FuncDecl)
	if _, ok := fn.Result.(*ast.SelfType); !ok {
		t.Fatalf("expected Self method result, got %#v", fn.Result)
	}
	ret := fn.Body.Stmts[0].(*ast.ReturnStmt)
	lit, ok := ret.Value.(*ast.CompositeLit)
	if !ok {
		t.Fatalf("expected composite literal, got %T", ret.Value)
	}
	named, ok := lit.Type.(*ast.NamedType)
	if !ok || len(named.Path) != 1 || named.Path[0] != "Point" {
		t.Fatalf("expected typed composite literal .Point{}, got %#v", lit.Type)
	}
}

func TestParseAttachedMethodWarnsOnNonSelfName(t *testing.T) {
	src := `
type Point struct {}

fn Point::Len(this) i32 {
    return 0
}
`
	_, diag := parseTestModule(t, src)
	found := false
	for _, d := range diag.All() {
		if d.Code == diagnostics.WarnNonSelfReceiverName {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected non-self receiver warning, got %v", diag.All())
	}
}

func TestParseMutBorrowPrefixExpression(t *testing.T) {
	src := `
fn main() {
    let mut p = 1
    let m = &mut p
    m
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
	letStmt, ok := fn.Body.Stmts[1].(*ast.LetStmt)
	if !ok {
		t.Fatalf("expected second stmt to be let, got %T", fn.Body.Stmts[1])
	}
	prefix, ok := letStmt.Value.(*ast.PrefixExpr)
	if !ok {
		t.Fatalf("expected prefix expr, got %T", letStmt.Value)
	}
	if prefix.Op != "&mut" {
		t.Fatalf("expected op &mut, got %q", prefix.Op)
	}
	ident, ok := prefix.Right.(*ast.Ident)
	if !ok || len(ident.Path) != 1 || ident.Path[0] != "p" {
		t.Fatalf("expected right operand p, got %#v", prefix.Right)
	}
}
