package parser

import (
	"strings"
	"testing"

	"compiler/internal/core/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/frontend/lexer"
)

func parseTestModule(t *testing.T, src string) (*ast.Module, *diagnostics.DiagnosticBag) {
	t.Helper()
	diag := diagnostics.NewDiagnosticBag("")
	toks := lexer.New("test.fer", src, diag).Tokenize()
	mod := New("test.fer", toks, diag).ParseModule()
	return mod, diag
}

func hasDiagnosticMessage(diag *diagnostics.DiagnosticBag, substr string) bool {
	for _, d := range diag.Diagnostics() {
		if strings.Contains(d.Message, substr) {
			return true
		}
	}
	return false
}

func findDiagnosticWithMessage(diag *diagnostics.DiagnosticBag, substr string) *diagnostics.Diagnostic {
	for _, d := range diag.Diagnostics() {
		if strings.Contains(d.Message, substr) {
			return d
		}
	}
	return nil
}

func hasDiagnosticCode(diag *diagnostics.DiagnosticBag, code string) bool {
	for _, d := range diag.Diagnostics() {
		if d.Code == code {
			return true
		}
	}
	return false
}

func countDiagnosticsExcludingCodes(diag *diagnostics.DiagnosticBag, codes ...string) int {
	excluded := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		excluded[code] = struct{}{}
	}
	count := 0
	for _, d := range diag.Diagnostics() {
		if _, skip := excluded[d.Code]; skip {
			continue
		}
		count++
	}
	return count
}

func TestParseMethodReceiverAndLiteral(t *testing.T) {
	src := `
type Point struct {
    x: i32 = 0
    y: i32 = 0
}
fn Point::len2(self) -> i32 {
    if self == .{ .x = 1, .y = 2 } {
        return 1
    }
    return 0
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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

func TestParseFunctionReturnTypeRequiresArrow(t *testing.T) {
	src := `
fn print(value: i32) void {}
`

	_, diag := parseTestModule(t, src)
	if !hasDiagnosticMessage(diag, "expected '->' before function return type") {
		t.Fatalf("expected missing arrow diagnostic, got %v", diag.Diagnostics())
	}
}

func TestParseFunctionWithoutExplicitReturnTypeNeedsNoArrow(t *testing.T) {
	src := `
fn print(value: i32) {}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", mod.Decls[0])
	}
	if fn.Result != nil {
		t.Fatalf("expected implicit return type, got %#v", fn.Result)
	}
}

func TestParseFunctionTypeSyntax(t *testing.T) {
	src := `
fn takefn(fun: fn(i32, ...str) -> i32) {}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", mod.Decls[0])
	}
	ft, ok := fn.Params[0].Type.(*ast.FuncType)
	if !ok {
		t.Fatalf("expected function type param, got %T", fn.Params[0].Type)
	}
	if len(ft.Params) != 2 {
		t.Fatalf("expected 2 function type params, got %d", len(ft.Params))
	}
	if ft.Params[1].IsVariadic != true {
		t.Fatalf("expected variadic function type param, got %#v", ft.Params[1])
	}
	if got := ast.TypeString(ft); got != "fn(i32, ...str) -> i32" {
		t.Fatalf("unexpected function type text: %q", got)
	}
}

func TestParseMapTypeSyntax(t *testing.T) {
	src := `
fn main(values: map[str]i32) {}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", mod.Decls[0])
	}
	mt, ok := fn.Params[0].Type.(*ast.MapType)
	if !ok {
		t.Fatalf("expected map type param, got %T", fn.Params[0].Type)
	}
	if got := ast.TypeString(mt); got != "map[str]i32" {
		t.Fatalf("unexpected map type text: %q", got)
	}
}

func TestParseMapLiteralSyntax(t *testing.T) {
	src := `
fn main() -> void {
    let values = map[str]i32{"a" => 1, "b" => 2}
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn := mod.Decls[0].(*ast.FuncDecl)
	letStmt := fn.Body.Stmts[0].(*ast.LetStmt)
	lit, ok := letStmt.Value.(*ast.CompositeLit)
	if !ok {
		t.Fatalf("expected composite literal, got %T", letStmt.Value)
	}
	if _, ok := lit.Type.(*ast.MapType); !ok {
		t.Fatalf("expected map literal type, got %T", lit.Type)
	}
	if len(lit.Items) != 2 || lit.Items[0].Key == nil || lit.Items[1].Key == nil {
		t.Fatalf("expected keyed map entries, got %#v", lit.Items)
	}
}

func TestParseMapLiteralFromContextSyntax(t *testing.T) {
	src := `
fn main() -> void {
    let values: map[str]i32 = {"a" => 1, "b" => 2}
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn := mod.Decls[0].(*ast.FuncDecl)
	letStmt := fn.Body.Stmts[0].(*ast.LetStmt)
	lit, ok := letStmt.Value.(*ast.CompositeLit)
	if !ok {
		t.Fatalf("expected composite literal, got %T", letStmt.Value)
	}
	if lit.Type != nil {
		t.Fatalf("expected context map literal without explicit type, got %T", lit.Type)
	}
	if len(lit.Items) != 2 || lit.Items[0].Key == nil || lit.Items[1].Key == nil {
		t.Fatalf("expected keyed map entries, got %#v", lit.Items)
	}
}

func TestParseMapLiteralFromNamedAliasSyntax(t *testing.T) {
	src := `
type MyMap map[str]str
fn main() -> void {
    let values = MyMap{"name" => "fuad"}
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn := mod.Decls[1].(*ast.FuncDecl)
	letStmt := fn.Body.Stmts[0].(*ast.LetStmt)
	lit, ok := letStmt.Value.(*ast.CompositeLit)
	if !ok {
		t.Fatalf("expected composite literal, got %T", letStmt.Value)
	}
	named, ok := lit.Type.(*ast.NamedType)
	if !ok || ast.TypeString(named) != "MyMap" {
		t.Fatalf("expected named type MyMap, got %#v", lit.Type)
	}
	if len(lit.Items) != 1 || lit.Items[0].Key == nil {
		t.Fatalf("expected keyed map entry, got %#v", lit.Items)
	}
}

func TestParseGeneralAliasAndContextCompositeSyntax(t *testing.T) {
	src := `
type Vec []i32
type Box struct { value: i32 = 0 }
fn main() -> void {
    let xs = Vec{1, 2, 3}
    let ys: Vec = {4, 5, 6}
    let box = Box{.value = 7}
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn := mod.Decls[2].(*ast.FuncDecl)
	for i, stmt := range fn.Body.Stmts {
		letStmt := stmt.(*ast.LetStmt)
		lit, ok := letStmt.Value.(*ast.CompositeLit)
		if !ok {
			t.Fatalf("stmt %d: expected composite literal, got %T", i, letStmt.Value)
		}
		if i == 0 || i == 2 {
			if lit.Type == nil {
				t.Fatalf("stmt %d: expected explicit alias type", i)
			}
		}
		if i == 1 && lit.Type != nil {
			t.Fatalf("stmt %d: expected contextual literal without explicit type", i)
		}
	}
}

func TestParseLambdaExprSyntax(t *testing.T) {
	src := `
fn main() -> void {
    let add = (a: i32, b: i32) => a + b
    let log = (msg: str) => {
        println(msg)
    }
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", mod.Decls[0])
	}
	body := fn.Body
	if body == nil || len(body.Stmts) != 2 {
		t.Fatalf("expected two statements, got %#v", body)
	}
	letAdd, ok := body.Stmts[0].(*ast.LetStmt)
	if !ok {
		t.Fatalf("expected let stmt, got %T", body.Stmts[0])
	}
	lambda, ok := letAdd.Value.(*ast.LambdaExpr)
	if !ok {
		t.Fatalf("expected lambda expr, got %T", letAdd.Value)
	}
	if len(lambda.Params) != 2 {
		t.Fatalf("expected 2 lambda params, got %d", len(lambda.Params))
	}
	if lambda.BodyExpr == nil || lambda.BodyBlock != nil {
		t.Fatalf("expected expression-bodied lambda, got %#v", lambda)
	}
	letLog, ok := body.Stmts[1].(*ast.LetStmt)
	if !ok {
		t.Fatalf("expected let stmt, got %T", body.Stmts[1])
	}
	blockLambda, ok := letLog.Value.(*ast.LambdaExpr)
	if !ok {
		t.Fatalf("expected lambda expr, got %T", letLog.Value)
	}
	if blockLambda.BodyBlock == nil || blockLambda.BodyExpr != nil {
		t.Fatalf("expected block-bodied lambda, got %#v", blockLambda)
	}
}

func TestParseTestDecl(t *testing.T) {
	src := `
test "allocator grows" {
    panic "boom"
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	if len(mod.Decls) != 1 {
		t.Fatalf("expected 1 decl, got %d", len(mod.Decls))
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl backing test, got %T", mod.Decls[0])
	}
	if !fn.IsTest || fn.TestName != "allocator grows" {
		t.Fatalf("expected parsed test metadata, got %#v", fn)
	}
	if fn.Name == nil || !strings.HasPrefix(fn.Name.Text(), "__ferret_test_") {
		t.Fatalf("expected synthetic test function name, got %#v", fn.Name)
	}
	if fn.Body == nil || len(fn.Body.Stmts) != 1 {
		t.Fatalf("expected test body to parse, got %#v", fn.Body)
	}
}

func TestParserRejectsLegacyReceiverSyntax(t *testing.T) {
	src := `
type Point struct {}

fn (p: Point) Len() -> i32 {
    return 0
}
`
	_, diag := parseTestModule(t, src)
	if !hasDiagnosticMessage(diag, "legacy receiver syntax has been removed") {
		t.Fatalf("expected legacy receiver rejection diagnostic, got %v", diag.Diagnostics())
	}
}

func TestParseInterfaceReceiverModifiers(t *testing.T) {
	src := `
type Reader interface {
    read(self, buf: []u8) -> i32
    peek(&self) -> u8
    refill(&mut self) -> i32
    close(*self) -> void
    raw(^self) -> void
    rawConst(^const self, buf: []u8) -> i32
    New() -> Reader
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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
	if got := iface.Methods[4].Receiver; got != "^" {
		t.Fatalf("expected ^ receiver, got %q", got)
	}
	if got := iface.Methods[5].Receiver; got != "^const " {
		t.Fatalf("expected ^const receiver, got %q", got)
	}
	if !iface.Methods[6].Static {
		t.Fatalf("expected static method")
	}
}

func TestParseSliceTypeRejectsLegacyMutMarker(t *testing.T) {
	src := `
fn fill(mut buf: []mut i32) -> void {}
`

	mod, diag := parseTestModule(t, src)
	if !hasDiagnosticMessage(diag, "slice type syntax is []T") {
		t.Fatalf("expected legacy mutable slice syntax diagnostic, got %v", diag.Diagnostics())
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", mod.Decls[0])
	}
	sliceType, ok := fn.Params[0].Type.(*ast.SliceType)
	if !ok {
		t.Fatalf("expected slice parameter type, got %T", fn.Params[0].Type)
	}
	if got := ast.TypeString(sliceType); got != "[]i32" {
		t.Fatalf("unexpected slice type string: %q", got)
	}
	if !fn.Params[0].IsMut {
		t.Fatal("expected parameter binding to remain mutable")
	}
}

func TestParseVariadicParams(t *testing.T) {
	src := `
fn collect(nums: ...i32) -> void {}
fn collectMut(mut nums: ...mut i32) -> void {}
`

	mod, diag := parseTestModule(t, src)
	if !hasDiagnosticMessage(diag, "variadic slice syntax is ...T") {
		t.Fatalf("expected legacy mutable variadic syntax diagnostic, got %v", diag.Diagnostics())
	}
	if len(mod.Decls) != 2 {
		t.Fatalf("expected 2 decls, got %d", len(mod.Decls))
	}

	fnA, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected first decl func, got %T", mod.Decls[0])
	}
	if len(fnA.Params) != 1 || !fnA.Params[0].IsVariadic {
		t.Fatalf("expected variadic param, got %#v", fnA.Params)
	}
	if got := ast.ParamString(fnA.Params[0]); got != "nums: ...i32" {
		t.Fatalf("unexpected variadic readonly param string %q", got)
	}

	fnB, ok := mod.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected second decl func, got %T", mod.Decls[1])
	}
	if len(fnB.Params) != 1 || !fnB.Params[0].IsVariadic {
		t.Fatalf("expected variadic mutable param, got %#v", fnB.Params)
	}
	if !fnB.Params[0].IsMut {
		t.Fatalf("expected mutable parameter binding, got %#v", fnB.Params[0])
	}
	if got := ast.ParamString(fnB.Params[0]); got != "mut nums: ...i32" {
		t.Fatalf("unexpected variadic mutable param string %q", got)
	}
}

func TestParseCharAndByteLiterals(t *testing.T) {
	src := `
fn main() -> void {
    let c = 'é'
    let b = b'h'
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	if len(mod.Decls) != 1 {
		t.Fatalf("expected 1 decl, got %d", len(mod.Decls))
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", mod.Decls[0])
	}
	if len(fn.Body.Stmts) != 2 {
		t.Fatalf("expected 2 statements, got %#v", fn.Body.Stmts)
	}

	charLet, ok := fn.Body.Stmts[0].(*ast.LetStmt)
	if !ok {
		t.Fatalf("expected first stmt let, got %T", fn.Body.Stmts[0])
	}
	charLit, ok := charLet.Value.(*ast.CharLit)
	if !ok {
		t.Fatalf("expected first let char literal, got %T", charLet.Value)
	}
	if charLit.IsByte {
		t.Fatalf("expected plain char literal, got %#v", charLit)
	}
	if charLit.Value != "é" {
		t.Fatalf("expected char literal value é, got %q", charLit.Value)
	}

	byteLet, ok := fn.Body.Stmts[1].(*ast.LetStmt)
	if !ok {
		t.Fatalf("expected second stmt let, got %T", fn.Body.Stmts[1])
	}
	byteLit, ok := byteLet.Value.(*ast.CharLit)
	if !ok {
		t.Fatalf("expected second let byte literal, got %T", byteLet.Value)
	}
	if !byteLit.IsByte {
		t.Fatalf("expected byte literal, got %#v", byteLit)
	}
	if byteLit.Value != "h" {
		t.Fatalf("expected byte literal value h, got %q", byteLit.Value)
	}
}

func TestParseDefaultParams(t *testing.T) {
	src := `
fn greet(name: str = "world", repeat: i32 = 1) -> void {}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", mod.Decls[0])
	}
	if len(fn.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(fn.Params))
	}
	if got := ast.ParamString(fn.Params[0]); got != "name: str = \"world\"" {
		t.Fatalf("unexpected defaulted param string %q", got)
	}
	if got := ast.ParamString(fn.Params[1]); got != "repeat: i32 = 1" {
		t.Fatalf("unexpected defaulted param string %q", got)
	}
}

func TestParseDefaultParamWithoutExplicitType(t *testing.T) {
	src := `
fn greet(name = "world") -> void {}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", mod.Decls[0])
	}
	if len(fn.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(fn.Params))
	}
	if fn.Params[0].Type != nil {
		t.Fatalf("expected omitted param type, got %#v", fn.Params[0].Type)
	}
	if fn.Params[0].Default == nil {
		t.Fatalf("expected parsed default expr, got %#v", fn.Params[0])
	}
}

func TestParserRejectsNonTrailingVariadicParam(t *testing.T) {
	src := `
fn bad(nums: ...i32, fallback: i32) -> void {}
`

	_, diag := parseTestModule(t, src)
	if !hasDiagnosticMessage(diag, "variadic parameter must be the last parameter") {
		t.Fatalf("expected non-trailing variadic diagnostic, got %v", diag.Diagnostics())
	}
}

func TestParserRejectsRequiredParamAfterDefault(t *testing.T) {
	src := `
fn bad(x: i32 = 1, y: i32) -> void {}
`

	_, diag := parseTestModule(t, src)
	if !hasDiagnosticMessage(diag, "parameter without default cannot follow parameter with default value") {
		t.Fatalf("expected default-order diagnostic, got %v", diag.Diagnostics())
	}
}

func TestParseSpreadArgument(t *testing.T) {
	src := `
fn add(nums: ...i32) -> void {}

fn main() -> void {
    let xs: []i32 = []i32{1, 2, 3}
    add(xs...)
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	mainFn, ok := mod.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected second decl to be main func, got %T", mod.Decls[1])
	}
	callStmt, ok := mainFn.Body.Stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected expr stmt call, got %T", mainFn.Body.Stmts[1])
	}
	call, ok := callStmt.Value.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		t.Fatalf("expected call with one arg, got %#v", callStmt.Value)
	}
	if _, ok := call.Args[0].(*ast.SpreadExpr); !ok {
		t.Fatalf("expected spread arg, got %T", call.Args[0])
	}
}

func TestParserRejectsNonTrailingSpreadArgument(t *testing.T) {
	src := `
fn add(nums: ...i32) -> void {}

fn main() -> void {
    let xs: []i32 = []i32{1, 2, 3}
    add(xs..., 1)
}
`
	_, diag := parseTestModule(t, src)
	if !hasDiagnosticMessage(diag, "spread argument must be the last argument") {
		t.Fatalf("expected non-trailing spread diagnostic, got %v", diag.Diagnostics())
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
	if got := countDiagnosticsExcludingCodes(diag, diagnostics.InfoTrailingComma); got != 0 {
		t.Fatalf("unexpected diagnostics: %v", diag.Diagnostics())
	}
	if !hasDiagnosticCode(diag, diagnostics.InfoTrailingComma) {
		t.Fatalf("expected trailing comma info diagnostic, got %v", diag.Diagnostics())
	}
	typ, ok := mod.Decls[0].(*ast.TypeDecl)
	if !ok {
		t.Fatalf("expected type decl, got %T", mod.Decls[0])
	}
	if len(typ.Attrs) != 1 || typ.Attrs[0].Name != "if" {
		t.Fatalf("expected #[if(...)] attr on type decl, got %#v", typ.Attrs)
	}
}

func TestParseLetMutConstAndComptimeExpr(t *testing.T) {
	src := `
let mut GlobalCount: i32 = 0
const BuildMode = "debug"

fn add(T: Type, x: T) -> T {
    let mut a: i32
    let b = comptime 1 + 2
    const local = 3
    return x
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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
	if len(fn.Params) != 2 {
		t.Fatalf("expected 2 params, got %#v", fn.Params)
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

func TestParseRejectsLegacyComptimeParamSyntax(t *testing.T) {
	src := `
fn add(comptime x: i32, y: i32) -> i32 {
    return x + y
}
`

	_, diag := parseTestModule(t, src)
	if len(diag.Diagnostics()) == 0 {
		t.Fatal("expected diagnostics for legacy comptime parameter syntax")
	}
}

func TestParseComptimeBlockPreservesBlockContext(t *testing.T) {
	src := `
fn main() -> void {
    comptime {
        assert(1 + 1 == 2, "math ok")
    }
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	if len(mod.Decls) != 1 {
		t.Fatalf("expected 1 decl, got %d", len(mod.Decls))
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", mod.Decls[0])
	}
	if len(fn.Body.Stmts) != 1 {
		t.Fatalf("expected one top-level stmt in body, got %d", len(fn.Body.Stmts))
	}
	block, ok := fn.Body.Stmts[0].(*ast.BlockStmt)
	if !ok {
		t.Fatalf("expected comptime block stmt, got %T", fn.Body.Stmts[0])
	}
	if !block.Comptime {
		t.Fatal("expected comptime block marker")
	}
	if len(block.Stmts) != 1 {
		t.Fatalf("expected one stmt in comptime block, got %d", len(block.Stmts))
	}
	exprStmt, ok := block.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected expr stmt in comptime block, got %T", block.Stmts[0])
	}
	if _, ok := exprStmt.Value.(*ast.CallExpr); !ok {
		t.Fatalf("expected plain call expression in comptime block, got %T", exprStmt.Value)
	}
}

func TestParseUnsafePrefixExpression(t *testing.T) {
	src := `
unsafe fn danger() -> i32 { return 1 }

fn main() -> i32 {
    let x = unsafe danger()
    return x
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	if len(mod.Decls) != 2 {
		t.Fatalf("expected 2 decls, got %d", len(mod.Decls))
	}
	fn, ok := mod.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", mod.Decls[1])
	}
	letStmt, ok := fn.Body.Stmts[0].(*ast.LetStmt)
	if !ok {
		t.Fatalf("expected let statement, got %T", fn.Body.Stmts[0])
	}
	prefix, ok := letStmt.Value.(*ast.PrefixExpr)
	if !ok || prefix.Op != "unsafe" {
		t.Fatalf("expected unsafe prefix expression, got %#v", letStmt.Value)
	}
}

func TestParseRejectsLegacyRawAddressSyntax(t *testing.T) {
	src := `
fn main() -> void {
    let mut x: i32 = 1
    let p = @x
    p
}
`

	_, diag := parseTestModule(t, src)
	if !hasDiagnosticMessage(diag, "raw address syntax `@` was removed") {
		t.Fatalf("expected legacy raw address rejection diagnostic, got %v", diag.Diagnostics())
	}
}

func TestParseMutableParameter(t *testing.T) {
	src := `
fn set(mut x: i32, y: i32) -> i32 {
    return x
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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

fn Map<T, U: any>(value: T) -> U {
    return value as U
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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

func TestParseNamedTypeConstraint(t *testing.T) {
	src := `
type numeric union { i32, i64 }

fn Use<T: numeric>(value: T) -> void {}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	if len(mod.Decls) != 2 {
		t.Fatalf("expected 2 declarations, got %d", len(mod.Decls))
	}
	numeric, ok := mod.Decls[0].(*ast.TypeDecl)
	if !ok {
		t.Fatalf("expected first decl to be type decl, got %#v", mod.Decls[0])
	}
	if _, ok := numeric.Type.(*ast.UnionType); !ok {
		t.Fatalf("expected numeric to parse as union type, got %T", numeric.Type)
	}
	fn, ok := mod.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", mod.Decls[1])
	}
	if len(fn.TypeParams) != 1 {
		t.Fatalf("expected one type parameter, got %d", len(fn.TypeParams))
	}
	if _, ok := fn.TypeParams[0].Constraint.(*ast.NamedType); !ok {
		t.Fatalf("expected named type constraint, got %T", fn.TypeParams[0].Constraint)
	}
}

func TestParseInlineConstraintLiterals(t *testing.T) {
	src := `
type W Writer

type Writer interface {
    write(&self, []u8) -> i32
}

fn close_it<T: interface {
    close(&self) -> void
}>(x: T) -> void {}

fn min<T: union {
    i32,
    i64
}>(a: T, b: T) -> T {
    return a
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	if len(mod.Decls) != 4 {
		t.Fatalf("expected 4 declarations, got %d", len(mod.Decls))
	}
	fnInline, ok := mod.Decls[2].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected inline-constraint function, got %T", mod.Decls[2])
	}
	if _, ok := fnInline.TypeParams[0].Constraint.(*ast.InterfaceType); !ok {
		t.Fatalf("expected inline interface constraint, got %T", fnInline.TypeParams[0].Constraint)
	}
	inlineIface := fnInline.TypeParams[0].Constraint.(*ast.InterfaceType)
	if len(inlineIface.Methods) != 1 || inlineIface.Methods[0].Static || inlineIface.Methods[0].Receiver != "&" {
		t.Fatalf("expected inline interface method to preserve explicit shared receiver, got %#v", inlineIface.Methods)
	}
	fnUnion, ok := mod.Decls[3].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected union-constraint function, got %T", mod.Decls[3])
	}
	unionConstraint, ok := fnUnion.TypeParams[0].Constraint.(*ast.UnionType)
	if !ok {
		t.Fatalf("expected inline union constraint, got %T", fnUnion.TypeParams[0].Constraint)
	}
	if len(unionConstraint.Members) != 2 {
		t.Fatalf("expected 2 inline union members, got %#v", unionConstraint.Members)
	}
}

func TestParseTypeDeclRejectsIntersectionBody(t *testing.T) {
	src := `
type A i32
type B i64
type C A & B
`

	_, diag := parseTestModule(t, src)
	if !hasDiagnosticMessage(diag, "expected top-level declaration") {
		t.Fatalf("expected intersection type declaration to be rejected, got %v", diag.Diagnostics())
	}
}

func TestParseTypeParamRejectsIntersectionConstraint(t *testing.T) {
	src := `
type Reader interface {
    Read(&self) -> i32
}

type Printable interface {
    Print(&self) -> void
}

fn Use<T: Reader & Printable>(value: T) -> void {}
`

	_, diag := parseTestModule(t, src)
	if !hasDiagnosticMessage(diag, "expected ',' or '>' after type parameter") {
		t.Fatalf("expected intersection constraint to be rejected, got %v", diag.Diagnostics())
	}
}

func TestParseStructTrailingCommaReportsInfo(t *testing.T) {
	src := `
type Pair struct {
    A: i32,
    B: i32,
}
`

	_, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 1 {
		t.Fatalf("expected one info diagnostic, got %v", got)
	}
	if !hasDiagnosticCode(diag, diagnostics.InfoTrailingComma) {
		t.Fatalf("expected trailing comma info diagnostic, got %v", diag.Diagnostics())
	}
}

func TestParseRepeatedSemicolonsReportInfo(t *testing.T) {
	src := `
fn main() -> void {
    print(1);;;;
}
`

	_, diag := parseTestModule(t, src)
	if !hasDiagnosticCode(diag, diagnostics.InfoUnnecessarySemicolon) {
		t.Fatalf("expected unnecessary semicolon info diagnostic, got %v", diag.Diagnostics())
	}
}

func TestParseReceiverlessInterfaceMethodIsStatic(t *testing.T) {
	src := `
type Writer interface {
    write([]u8) -> i32
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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
	if len(iface.Methods) != 1 || !iface.Methods[0].Static || iface.Methods[0].Receiver != "" {
		t.Fatalf("expected receiverless interface method to be static, got %#v", iface.Methods)
	}
}

func TestParseGenericCallWithAngleTypeArgs(t *testing.T) {
	src := `
fn add<T>(a: T, b: T) -> T {
    return a
}

fn main() -> i32 {
    return add<i32>(1, 2)
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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

func TestParseRejectsEmptyAngleTypeArgs(t *testing.T) {
	src := `
fn add<T>(a: T, b: T) -> T {
    return a
}

fn main() -> i32 {
    return add<>(1, 2)
}
`

	_, diag := parseTestModule(t, src)
	if !hasDiagnosticMessage(diag, "expected at least one type argument") {
		t.Fatalf("expected empty-angle type-argument diagnostic, got %v", diag.Diagnostics())
	}
}

func TestParseStaticMethodCallWithGenericOwnerTypeArgs(t *testing.T) {
	src := `
type Circle<T> struct {
    Rad: T
}

fn Circle<T>::New(v: T) -> Self {
    return .{ .Rad = v }
}

fn main() -> Circle<i32> {
    return Circle<i32>::New(1)
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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
fn ClonePoint(p: Point) -> Point {
    return copy p
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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
const sentinel = 0

/// Returns the current panic payload as a string.
/// Returns an empty string when there is no active panic.
#[builtin]
fn recover() -> string;
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	if len(mod.Decls) != 2 {
		t.Fatalf("expected 2 decls, got %d", len(mod.Decls))
	}
	fn, ok := mod.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", mod.Decls[1])
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
const sentinel = 0

// Point docs.
type Point struct {}

// mutable binding docs
let mut x = 1

/* constant docs */
const Y = 2

// Adds one.
// Returns incremented value.
fn addOne(v: i32) -> i32 {
    return v + 1
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	if len(mod.Decls) != 5 {
		t.Fatalf("expected 5 decls, got %d", len(mod.Decls))
	}

	typ, ok := mod.Decls[1].(*ast.TypeDecl)
	if !ok || typ.Doc == nil || !strings.Contains(typ.Doc.Text, "Point docs.") {
		t.Fatalf("expected type doc from // comment, got %#v", mod.Decls[1])
	}
	letDecl, ok := mod.Decls[2].(*ast.LetDecl)
	if !ok || letDecl.Doc == nil || !strings.Contains(letDecl.Doc.Text, "mutable binding docs") {
		t.Fatalf("expected let doc from // comment, got %#v", mod.Decls[2])
	}
	constDecl, ok := mod.Decls[3].(*ast.ConstDecl)
	if !ok || constDecl.Doc == nil || !strings.Contains(constDecl.Doc.Text, "constant docs") {
		t.Fatalf("expected const doc from /* */ comment, got %#v", mod.Decls[3])
	}
	fn, ok := mod.Decls[4].(*ast.FuncDecl)
	if !ok || fn.Doc == nil {
		t.Fatalf("expected function doc from // comments, got %#v", mod.Decls[4])
	}
	if !strings.Contains(fn.Doc.Text, "Adds one.") || !strings.Contains(fn.Doc.Text, "Returns incremented value.") {
		t.Fatalf("unexpected function doc text: %q", fn.Doc.Text)
	}
}

func TestParseModuleDocComment(t *testing.T) {
	src := `// module docs line 1
// module docs line 2
import "std/os"

fn main() -> void {}
`

	mod, diag := parseTestModule(t, src)
	if diag.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", diag.Diagnostics())
	}
	if mod == nil || mod.Doc == nil {
		t.Fatal("expected module doc comment")
	}
	if !strings.Contains(mod.Doc.Text, "module docs line 1") || !strings.Contains(mod.Doc.Text, "module docs line 2") {
		t.Fatalf("unexpected module doc text: %q", mod.Doc.Text)
	}
}

func TestParseModuleDocCommentWithBlankLineBeforeDeclarations(t *testing.T) {
	src := `// module docs line 1
// module docs line 2

/// function docs
fn main() -> void {}
`

	mod, diag := parseTestModule(t, src)
	if diag.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", diag.Diagnostics())
	}
	if mod == nil || mod.Doc == nil {
		t.Fatal("expected module doc comment")
	}
	if !strings.Contains(mod.Doc.Text, "module docs line 1") || !strings.Contains(mod.Doc.Text, "module docs line 2") {
		t.Fatalf("unexpected module doc text: %q", mod.Doc.Text)
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok || fn.Doc == nil || !strings.Contains(fn.Doc.Text, "function docs") {
		t.Fatalf("expected function doc to remain attached, got %#v", mod.Decls[0])
	}
}

func TestParseDocCommentGapDoesNotAttach(t *testing.T) {
	src := `
// this should not attach due to gap

fn noDoc() -> void {}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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

func TestParseDocCommentAttachesLastContiguousBlockBeforeExternDecl(t *testing.T) {
	src := `
// section header
// more section header
//
// not declaration docs

/// declaration docs line 1
/// declaration docs line 2
#[extern]
fn recover() -> str;
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", mod.Decls[0])
	}
	if fn.Doc == nil {
		t.Fatal("expected declaration doc comment")
	}
	if !strings.Contains(fn.Doc.Text, "declaration docs line 1") || strings.Contains(fn.Doc.Text, "section header") {
		t.Fatalf("unexpected function doc text: %q", fn.Doc.Text)
	}
	if !fn.IsExtern {
		t.Fatal("expected extern function declaration")
	}
}

func TestParseRegularCommentsRemainIgnored(t *testing.T) {
	src := `
fn main() -> void
// comment between signature and body should be ignored
{
    let x = 1
    // local comment should be ignored
    x
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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

func TestParseTrailingCommentsBeforeBlockCloseRemainIgnored(t *testing.T) {
	src := `
fn main() -> void {
    if true {
        print("ok")
        return
    }
    // panic "ignored"
    // still ignored
}
`

	_, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
}

func TestParseBlockDocCommentPreservesLines(t *testing.T) {
	src := `
const sentinel = 0

/**
 * block line 1
 * block line 2
 */
fn f() -> void {}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn, ok := mod.Decls[1].(*ast.FuncDecl)
	if !ok || fn.Doc == nil {
		t.Fatalf("expected function doc from block comment, got %#v", mod.Decls[1])
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
fn main() -> void {
    // local mutable binding doc
    let mut x = 1

    /* local constant doc */
    const y = 2

    x
    y
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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
fn Println(text: string) -> void;
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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
fn Tick() -> void;
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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

func TestParseUnknownAttributeReportsDiagnostic(t *testing.T) {
	src := `
#[unknown_attr]
fn main() -> void {}
`
	_, diag := parseTestModule(t, src)
	if !hasDiagnosticMessage(diag, `unknown attribute "unknown_attr"`) {
		t.Fatalf("expected unknown attribute diagnostic, got %v", diag.Diagnostics())
	}
}

func TestParseAllowUnusedAttributeRejectsArgs(t *testing.T) {
	src := `
#[allow_unused("x")]
fn helper() -> void {}
`
	_, diag := parseTestModule(t, src)
	if !hasDiagnosticMessage(diag, "#[allow_unused] does not accept arguments") {
		t.Fatalf("expected allow_unused arg diagnostic, got %v", diag.Diagnostics())
	}
}

func TestParseExternAttributeOnlyValidOnFunctions(t *testing.T) {
	src := `
#[extern]
type Nope struct {}
`
	_, diag := parseTestModule(t, src)
	if !hasDiagnosticMessage(diag, "#[extern] is only valid on function declarations") {
		t.Fatalf("expected extern-on-type diagnostic, got %v", diag.Diagnostics())
	}
}

func TestParseCastExpression(t *testing.T) {
	src := `
fn CastIt(x: i32) -> i8 {
    return x as i8
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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
fn run() -> i32 {
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
	if got := diag.Diagnostics(); len(got) != 0 {
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

func TestParseWhileWithoutParensBeforeBlock(t *testing.T) {
	src := `
fn run() -> i32 {
    let mut limit: i32 = 2
    let mut count: i32 = 0
    while count <= limit {
        count = count + 1
        break
    }
    return count
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", mod.Decls[0])
	}
	loop, ok := fn.Body.Stmts[2].(*ast.WhileStmt)
	if !ok {
		t.Fatalf("expected while stmt, got %T", fn.Body.Stmts[2])
	}
	if _, ok := loop.Cond.(*ast.BinaryExpr); !ok {
		t.Fatalf("expected binary condition, got %T", loop.Cond)
	}
	if len(loop.Body.Stmts) != 2 {
		t.Fatalf("expected 2 loop statements, got %d", len(loop.Body.Stmts))
	}
}

func TestParseForLoopForms(t *testing.T) {
	src := `
fn run() -> i32 {
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
	if got := diag.Diagnostics(); len(got) != 0 {
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

func TestParseForLoopRangeForms(t *testing.T) {
	src := `
fn run() -> i32 {
    let mut sum: i32 = 0
    for 0..10 |v| {
        sum += v
    }
    for 0..=10:2 |i, v| {
        sum += i
        sum += v
    }
    return sum
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn, ok := mod.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", mod.Decls[0])
	}
	first, ok := fn.Body.Stmts[1].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected first range for stmt, got %T", fn.Body.Stmts[1])
	}
	r0, ok := first.Iterable.(*ast.RangeExpr)
	if !ok || r0.Inclusive || r0.Step != nil {
		t.Fatalf("expected exclusive range without step, got %#v", first.Iterable)
	}
	second, ok := fn.Body.Stmts[2].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected second range for stmt, got %T", fn.Body.Stmts[2])
	}
	r1, ok := second.Iterable.(*ast.RangeExpr)
	if !ok || !r1.Inclusive || r1.Step == nil {
		t.Fatalf("expected inclusive range with step, got %#v", second.Iterable)
	}
}

func TestParseMatchArmRangePattern(t *testing.T) {
	src := `
fn run(x: i32) -> i32 {
    return match x {
        0..=10 => 1
        _ => 0
    }
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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
	matchExpr, ok := ret.Value.(*ast.MatchExpr)
	if !ok {
		t.Fatalf("expected match expr, got %T", ret.Value)
	}
	rangePat, ok := matchExpr.Arms[0].Pattern.(*ast.RangeExpr)
	if !ok || !rangePat.Inclusive {
		t.Fatalf("expected inclusive range pattern, got %#v", matchExpr.Arms[0].Pattern)
	}
}

func TestParserRejectsInvalidForBindingForm(t *testing.T) {
	src := `
fn run() -> void {
    for items {
        return
    }
}
`

	_, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) == 0 {
		t.Fatalf("expected diagnostic, got none")
	}
	if !hasDiagnosticMessage(diag, "expected '|' after for iterable") {
		t.Fatalf("expected invalid for binding diagnostic, got %v", diag.Diagnostics())
	}
}

func TestParseLabelsAndLabeledLoopControl(t *testing.T) {
	src := `
fn run() -> void {
    outer: for items |v| {
        inner: while true {
            break outer
        }
        continue outer
    }
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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
fn run() -> void {
    outer: x =
    return
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 1 {
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
fn run() -> i32 {
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
	if got := diag.Diagnostics(); len(got) != 1 {
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
	if got := diag.Diagnostics(); len(got) == 0 {
		t.Fatal("expected removed destructor syntax diagnostic")
	}
	if !hasDiagnosticMessage(diag, "special destructor syntax has been removed") {
		t.Fatalf("expected removed destructor syntax diagnostic, got %v", diag.Diagnostics())
	}
}

func TestParseElseIfDeferLockUnsafeAndBuiltins(t *testing.T) {
	src := `
fn run(m: Mutex, cond: bool) -> void {
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
	if got := diag.Diagnostics(); len(got) != 0 {
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
unsafe fn Read(ptr: ^i32) -> i32 {
    return *ptr
}

fn run(ptr: ^i32) -> i32 {
    let x: i32
    unsafe {
        x = Read(ptr)
        panic "bad"
    }
    return x
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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
fn run(path: string) -> i32 {
    let file = open(path) catch |err| {
        log(err)
        return 0
    }
    return div(10, 0) catch -1
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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
fn run(x: i32) -> bool {
    return x is i32
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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
fn run(value: Token) -> i32 {
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
	if got := diag.Diagnostics(); len(got) != 0 {
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

func TestParseMatchTypeArmWithBinaryReturnExpr(t *testing.T) {
	src := `
fn run(value: Token) -> i32 {
    match value {
        is i32 => {
            let widened: i32 = value
            return value + widened
        }
        _ => {
            return 0
        }
    }
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn := mod.Decls[0].(*ast.FuncDecl)
	exprStmt := fn.Body.Stmts[0].(*ast.ExprStmt)
	matchExpr := exprStmt.Value.(*ast.MatchExpr)
	if len(matchExpr.Arms) != 2 {
		t.Fatalf("expected 2 match arms, got %d", len(matchExpr.Arms))
	}
	if _, ok := matchExpr.Arms[0].Body.Stmts[1].(*ast.ReturnStmt); !ok {
		t.Fatalf("expected return stmt in first arm, got %#v", matchExpr.Arms[0].Body.Stmts)
	}
}

func TestParseMatchExpression(t *testing.T) {
	src := `
fn run(value: Token) -> i32 {
    let out = match value {
        is i32 => value
        _ => 0
    }
    return out
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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
fn build() -> Point {
    let p: Point = .{ .x = 1, 2 }
    return p
}
`

	_, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(got), got)
	}
	if !hasDiagnosticMessage(diag, "cannot mix named and positional composite elements") {
		t.Fatalf("expected mixed composite literal diagnostic, got %v", diag.Diagnostics())
	}
}

func TestParseTupleLiteralUsesParenthesizedSyntax(t *testing.T) {
	src := `
fn main() -> void {
    let tuple: (i32, i32) = (1, 2)
    let grouped = (1 + 2)
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
		t.Fatalf("unexpected diagnostics: %v", got)
	}
	fn := mod.Decls[0].(*ast.FuncDecl)
	tupleLet := fn.Body.Stmts[0].(*ast.LetStmt)
	lit, ok := tupleLet.Value.(*ast.CompositeLit)
	if !ok {
		t.Fatalf("expected tuple literal composite, got %T", tupleLet.Value)
	}
	if lit.Type != nil {
		t.Fatalf("expected inferred tuple literal type, got %#v", lit.Type)
	}
	if !lit.Tuple {
		t.Fatalf("expected tuple literal marker, got %#v", lit)
	}
	if len(lit.Items) != 2 {
		t.Fatalf("expected 2 tuple elements, got %d", len(lit.Items))
	}
	if lit.Items[0].Name != nil || lit.Items[1].Name != nil {
		t.Fatalf("expected positional tuple elements, got %#v", lit.Items)
	}
	groupedLet := fn.Body.Stmts[1].(*ast.LetStmt)
	if _, ok := groupedLet.Value.(*ast.BinaryExpr); !ok {
		t.Fatalf("expected parenthesized expression grouping, got %T", groupedLet.Value)
	}
}

func TestParserRejectsDuplicateTypeMembers(t *testing.T) {
	src := `
type Point struct {
    x: i32
    x: i32
}

type Shape interface {
    area() -> i32
    area() -> i32
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
	if got := countDiagnosticsExcludingCodes(diag, diagnostics.InfoTrailingComma); got != 5 {
		t.Fatalf("expected 5 diagnostics, got %d: %v", got, diag.Diagnostics())
	}
	for _, substr := range []string{
		`duplicate field "x"`,
		`duplicate interface method "area"`,
		`duplicate enum variant "red"`,
		`duplicate error member "denied"`,
		`duplicate union member "named:[i32]"`,
	} {
		if !hasDiagnosticMessage(diag, substr) {
			t.Fatalf("expected diagnostic containing %q, got %v", substr, diag.Diagnostics())
		}
	}
}

func TestParserRecoversStructBodyMembers(t *testing.T) {
	src := `
type Point struct {
    x: =
    y: i32 = 1
}

fn build() -> i32 {
    return 1
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 1 {
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
fn build() -> Point {
    let p: Point = .{ .x = 1 .y = 2 }
    return p
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(got), got)
	}
	if !hasDiagnosticMessage(diag, "expected ',' or '}' after composite literal element") {
		t.Fatalf("expected missing comma diagnostic, got %v", diag.Diagnostics())
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
fn run() -> void {
    foo("a" "b")
    return
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) < 1 {
		t.Fatalf("expected at least 1 diagnostic, got %d: %v", len(got), got)
	}
	if !hasDiagnosticMessage(diag, "expected ',' or ')' after argument") {
		t.Fatalf("expected missing argument comma diagnostic, got %v", diag.Diagnostics())
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
fn run() -> void {
    print(Point::Counter
    let mut maybe : ?i32 = none
    return
}
`

	_, diag := parseTestModule(t, src)
	d := findDiagnosticWithMessage(diag, "expected ',' or ')' after argument")
	if d == nil {
		t.Fatalf("expected missing call closer diagnostic, got %v", diag.Diagnostics())
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
		t.Fatalf("expected missing call closer diagnostic, got %v", diag.Diagnostics())
	}
	if hasDiagnosticMessage(diag, "expected '}'") {
		t.Fatalf("unexpected cascading expected '}' diagnostic: %v", diag.Diagnostics())
	}
}

func TestParseNewReferenceAndRawTypes(t *testing.T) {
	src := `
type Node struct {
    next: *Node
    view: &Node
    edit: &mut Node
    data: ^u8
    ro: ^const u8
}

fn Node::peek(&self) -> &Node {
    return self.view
}

fn Node::peekRaw(^const self) -> ^const u8 {
    return self.ro
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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
	raw, ok := st.Fields[3].Type.(*ast.RawPtrType)
	if !ok || raw.Const {
		t.Fatalf("expected mutable raw ptr type, got %#v", st.Fields[3].Type)
	}
	rawConst, ok := st.Fields[4].Type.(*ast.RawPtrType)
	if !ok || !rawConst.Const {
		t.Fatalf("expected const raw ptr type, got %#v", st.Fields[4].Type)
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

	rawFn, ok := mod.Decls[2].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected raw func decl, got %T", mod.Decls[2])
	}
	rawRecv, ok := rawFn.Receiver.Type.(*ast.RawPtrType)
	if !ok || !rawRecv.Const {
		t.Fatalf("expected const raw receiver type, got %#v", rawFn.Receiver.Type)
	}
	rawRet, ok := rawFn.Result.(*ast.RawPtrType)
	if !ok || !rawRet.Const {
		t.Fatalf("expected const raw result type, got %#v", rawFn.Result)
	}
}

func TestParseInferredArrayLengthType(t *testing.T) {
	src := `
fn main() {
    let values: [_]i32 = [_]i32{1, 2, 3}
}
`

	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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

fn Point::New() -> Point {
    return .{}
}
`
	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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
    New() -> Self
    Draw(&self)
}

type Point struct {
    X: i32 = 0
}

fn Point::Clone(&self) -> Self {
    return .Point{}
}
`
	mod, diag := parseTestModule(t, src)
	if got := diag.Diagnostics(); len(got) != 0 {
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

fn Point::Len(this) -> i32 {
    return 0
}
`
	_, diag := parseTestModule(t, src)
	found := false
	for _, d := range diag.Diagnostics() {
		if d.Code == diagnostics.WarnNonSelfReceiverName {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected non-self receiver warning, got %v", diag.Diagnostics())
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
	if got := diag.Diagnostics(); len(got) != 0 {
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
