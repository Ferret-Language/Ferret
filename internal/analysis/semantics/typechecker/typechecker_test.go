package typechecker_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/phase"
	compiler "compiler/internal/driver"
	"compiler/internal/frontend/ast"
)

func TestTypecheckerChecksWorkspaceExampleSubset(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
import "math/vec2"
import "util/build"

fn main() i32 {
    let p = build::Origin()
    if p == .{ .X = 0, .Y = 0 } {
        return vec2::Len2(p)
    }
    return 1
}
`)
	mustWriteType(t, filepath.Join(root, "math", "vec2.ferr"), `
type Vec2 struct {
    X: i32 = 0
    Y: i32 = 0
}

fn Len2(v: Vec2) i32 {
    return v.X * v.X + v.Y * v.Y
}
`)
	mustWriteType(t, filepath.Join(root, "util", "build.ferr"), `
import "math/vec2"

fn Origin() vec2::Vec2 {
    return .{}
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected CFG-analyzed entry, got %#v", result.Entry)
	}
	if result.Entry.Types == nil {
		t.Fatal("expected module type info")
	}

	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	ifStmt := mainFn.Body.Stmts[1].(*ast.IfStmt)
	condType := result.Entry.Types.Nodes[ifStmt.Cond]
	if !typeinfo.IsBuiltinNamed(condType, "bool") {
		t.Fatalf("expected bool condition type, got %v", condType)
	}
}

func TestTypecheckerReportsInvalidReturn(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn bad() i32 {
    return
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected invalid return diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidReturn {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrInvalidReturn, result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerReportsArgumentTypeMismatch(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn Add(x: i32, y: i32) i32 {
    return x + y
}

fn main() i32 {
    return Add("x", 1)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected type mismatch diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrTypeMismatch, result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerContextualizesNumericLiteralsAndDefaults(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i64 {
    let a = 1
    let b = 1.5
    let c: i64 = 1
    return c
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	letA := mainFn.Body.Stmts[0].(*ast.LetStmt)
	letB := mainFn.Body.Stmts[1].(*ast.LetStmt)
	letC := mainFn.Body.Stmts[2].(*ast.LetStmt)
	if !typeinfo.IsBuiltinNamed(result.Entry.Types.Nodes[letA.Value], typeinfo.DefaultIntTypeName) {
		t.Fatalf("expected default int literal type %s, got %#v", typeinfo.DefaultIntTypeName, result.Entry.Types.Nodes[letA.Value])
	}
	if !typeinfo.IsBuiltinNamed(result.Entry.Types.Nodes[letB.Value], typeinfo.DefaultFloatTypeName) {
		t.Fatalf("expected default float literal type %s, got %#v", typeinfo.DefaultFloatTypeName, result.Entry.Types.Nodes[letB.Value])
	}
	if !typeinfo.IsBuiltinNamed(result.Entry.Types.Nodes[letC.Value], "i64") {
		t.Fatalf("expected contextualized literal type i64, got %#v", result.Entry.Types.Nodes[letC.Value])
	}
}

func TestTypecheckerPreservesDeclarationTypeParams(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Box<T> struct {
    Value: T
}

fn Identity<T>(value: T) T {
    return value
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	box := result.Entry.AST.Decls[0].(*ast.TypeDecl)
	boxType, ok := result.Entry.Types.Nodes[box.Type].(*typeinfo.StructType)
	if !ok {
		t.Fatalf("expected struct type, got %T", result.Entry.Types.Nodes[box.Type])
	}
	field := boxType.Fields["Value"]
	if field == nil {
		t.Fatalf("expected Value field in %#v", boxType.Fields)
	}
	if _, ok := field.Type.(*typeinfo.TypeParam); !ok {
		t.Fatalf("expected Value field type param, got %T", field.Type)
	}

	fn := findTypeFunc(t, result.Entry.AST, "Identity")
	fnType, ok := result.Entry.Types.Symbols[result.Entry.Bindings.FunctionSymbols[fn].ID].(*typeinfo.FuncType)
	if !ok {
		t.Fatalf("expected function type, got %T", result.Entry.Types.Symbols[result.Entry.Bindings.FunctionSymbols[fn].ID])
	}
	if len(fnType.TypeParams) != 1 || fnType.TypeParams[0].Name != "T" {
		t.Fatalf("expected Identity<T>, got %#v", fnType.TypeParams)
	}
	if _, ok := fnType.Params[0].Type.(*typeinfo.TypeParam); !ok {
		t.Fatalf("expected param type param, got %T", fnType.Params[0].Type)
	}
	if _, ok := fnType.Result.(*typeinfo.TypeParam); !ok {
		t.Fatalf("expected result type param, got %T", fnType.Result)
	}
}

func TestTypecheckerHandlesGenericTypeArguments(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Box<T> struct {
    Value: T
}

fn main() i32 {
    let b: Box<i32> = .{ .Value = 7 }
    return b.Value
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	letB := mainFn.Body.Stmts[0].(*ast.LetStmt)
	boxType, ok := result.Entry.Types.Nodes[letB.Type].(*typeinfo.NamedType)
	if !ok {
		t.Fatalf("expected named type for Box<i32>, got %T", result.Entry.Types.Nodes[letB.Type])
	}
	if boxType.Name != "Box" || len(boxType.TypeArgs) != 1 || !typeinfo.IsBuiltinNamed(boxType.TypeArgs[0], "i32") {
		t.Fatalf("expected Box<i32>, got %#v", boxType)
	}
	ret := mainFn.Body.Stmts[1].(*ast.ReturnStmt)
	if !typeinfo.IsBuiltinNamed(result.Entry.Types.Nodes[ret.Value], "i32") {
		t.Fatalf("expected selector type i32, got %#v", result.Entry.Types.Nodes[ret.Value])
	}
}

func TestTypecheckerHandlesGenericOwnerMethod(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Box<T> struct {
    Value: T
}

fn Box<T>::Get(&self) T {
    return self.Value
}

fn main() i32 {
    let b: Box<i32> = .{ .Value = 7 }
    return b.Get()
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	ret := mainFn.Body.Stmts[1].(*ast.ReturnStmt)
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected call expr, got %T", ret.Value)
	}
	if !typeinfo.IsBuiltinNamed(result.Entry.Types.Nodes[call], "i32") {
		t.Fatalf("expected instantiated method result i32, got %#v", result.Entry.Types.Nodes[call])
	}
}

func TestTypecheckerHandlesStaticMethodCallWithGenericOwnerTypeArgs(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Circle<T> struct {
    Rad: T
}

fn Circle<T>::New(v: T) Self {
    return .{ .Rad = v }
}

fn main() Circle<i32> {
    return Circle<i32>::New(1)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	ret := mainFn.Body.Stmts[0].(*ast.ReturnStmt)
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected call expr, got %T", ret.Value)
	}
	retType := result.Entry.Types.Nodes[call]
	named, ok := retType.(*typeinfo.NamedType)
	if !ok || named.Name != "Circle" || len(named.TypeArgs) != 1 || !typeinfo.IsBuiltinNamed(named.TypeArgs[0], "i32") {
		t.Fatalf("expected call result Circle<i32>, got %#v", retType)
	}

	calleeType, ok := result.Entry.Types.Nodes[call.Callee].(*typeinfo.FuncType)
	if !ok {
		t.Fatalf("expected instantiated callee type, got %T", result.Entry.Types.Nodes[call.Callee])
	}
	if len(calleeType.TypeParams) != 0 {
		t.Fatalf("expected no remaining callee type params, got %#v", calleeType.TypeParams)
	}
	if len(calleeType.Params) != 1 || !typeinfo.IsBuiltinNamed(calleeType.Params[0].Type, "i32") {
		t.Fatalf("expected New(i32), got %#v", calleeType.Params)
	}
}

func TestTypecheckerReportsGenericOwnerMissingTypeArgsOnce(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
 type Point<T> struct {
     Value: T = 0
 }

 fn Point::Calc(&self) i32 {
     return self.Value
 }
 `)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected diagnostics")
	}
	count := 0
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, "missing type arguments for generic type \"Point\"") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected one missing-type-args diagnostic for generic owner method, got %d: %#v", count, result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerInfersGenericExternCallResult(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
#[extern]
fn Identity<T>(value: T) T;

fn main() i32 {
    return Identity(1)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	ret := mainFn.Body.Stmts[0].(*ast.ReturnStmt)
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected call expr, got %T", ret.Value)
	}
	if !typeinfo.IsBuiltinNamed(result.Entry.Types.Nodes[call], "i32") {
		t.Fatalf("expected inferred call result i32, got %#v", result.Entry.Types.Nodes[call])
	}
	calleeType, ok := result.Entry.Types.Nodes[call.Callee].(*typeinfo.FuncType)
	if !ok {
		t.Fatalf("expected instantiated callee type, got %T", result.Entry.Types.Nodes[call.Callee])
	}
	if len(calleeType.TypeParams) != 0 {
		t.Fatalf("expected instantiated callee to have no remaining type params, got %#v", calleeType.TypeParams)
	}
	if !typeinfo.IsBuiltinNamed(calleeType.Params[0].Type, "i32") || !typeinfo.IsBuiltinNamed(calleeType.Result, "i32") {
		t.Fatalf("expected instantiated Identity(i32) -> i32, got %#v", calleeType)
	}
}

func TestTypecheckerRejectsGenericConstraintMismatch(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Reader interface {
    Read(&self) i32
}

#[extern]
fn Use<T: Reader>(value: T) void;

fn main() void {
    Use(1)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected generic constraint diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, "does not implement") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected interface constraint mismatch diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerSupportsConstraintDeclarationIntersection(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
constraint reader = interface {
    Read(&self) i32
};

constraint printable = interface {
    Print(&self) void
};

constraint reader_printable = reader & printable;

fn Use<T: reader_printable>(value: T) T {
    return value
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerReportsConstraintDeclarationMismatch(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
constraint numeric = union { i32, i64 };

fn Id<T: numeric>(v: T) T {
    return v
}

fn main() void {
    Id("x")
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected constraint mismatch diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, "expected numeric") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected constraint mismatch diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsConstraintAsConcreteType(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
constraint numeric = union { i32, i64 };

fn main() void {
    let x: numeric = 1
    x
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected invalid concrete-constraint type diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidType && strings.Contains(diag.Message, "constraint \"numeric\" cannot be used as a concrete type") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected concrete-constraint usage diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsMethodsFromTypeParamInterfaceConstraint(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Reader interface {
    Read(&self) i32
}

fn ReadValue<T: Reader>(value: T) i32 {
    return value.Read()
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	fn := findTypeFunc(t, result.Entry.AST, "ReadValue")
	ret := fn.Body.Stmts[0].(*ast.ReturnStmt)
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected method call, got %T", ret.Value)
	}
	if !typeinfo.IsBuiltinNamed(result.Entry.Types.Nodes[call], "i32") {
		t.Fatalf("expected constrained method call result i32, got %#v", result.Entry.Types.Nodes[call])
	}
}

func TestTypecheckerRejectsExplicitOwnerTypeArgsConstraintMismatch(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Reader interface {
    Read(&self) i32
}

type Box<T: Reader> struct {
    Value: T
}

fn Box<T>::New(value: T) Self {
    return .{ .Value = value }
}

fn main() void {
    Box<i32>::New(1)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected owner type-argument constraint diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, "does not implement") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected interface constraint mismatch diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerReportsUnsupportedGenericOperationAtCallSite(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32
}

fn Add<T>(a: T, b: T) T {
    return a + b
}

fn main() void {
    let p: Point = .{ .X = 1 }
    Add(p, p)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected generic call-site diagnostic")
	}

	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	callStmt, ok := mainFn.Body.Stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected expr stmt call, got %T", mainFn.Body.Stmts[1])
	}
	call, ok := callStmt.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected call expr, got %T", callStmt.Value)
	}

	foundCallSite := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code != diagnostics.ErrInvalidOperation || len(diag.Labels) == 0 || diag.Labels[0].Location == nil || diag.Labels[0].Location.Start == nil {
			continue
		}
		if strings.Contains(diag.Message, "instantiated generic call requires valid operation") &&
			diag.Labels[0].Location.Start.Line == call.Location.Start.Line {
			foundCallSite = true
		}
		if strings.Contains(diag.Message, "invalid binary operation T") {
			t.Fatalf("expected no generic-body binary-op diagnostic, got %#v", diag)
		}
	}
	if !foundCallSite {
		t.Fatalf("expected call-site generic instantiation diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerReportsExplicitGenericTypeArgConflictClearly(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn Add<T>(a: T, b: T) T {
    return a + b
}

fn main() i32 {
    return Add<bool>(1, 2)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected explicit generic type argument conflict diagnostic")
	}

	foundMismatch := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch {
			foundMismatch = true
		}
		if diag.Code == diagnostics.ErrInvalidOperation && strings.Contains(diag.Message, "instantiated generic call requires valid operation") {
			t.Fatalf("expected no secondary generic operation diagnostic when call args already mismatch, got %#v", diag)
		}
	}
	if !foundMismatch {
		t.Fatalf("expected standard type mismatch diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerInfersOwnerTypeArgsForStaticGenericMethodCall(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Circle<T> struct {
    Rad: T
}

fn Circle<T>::New(v: T) Self {
    return .{ .Rad = v }
}

fn main() void {
    let c = Circle::New(1)
    c
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	letC, ok := mainFn.Body.Stmts[0].(*ast.LetStmt)
	if !ok {
		t.Fatalf("expected let stmt, got %T", mainFn.Body.Stmts[0])
	}
	named, ok := result.Entry.Types.Nodes[letC.Value].(*typeinfo.NamedType)
	if !ok || named == nil {
		t.Fatalf("expected named type for Circle::New call, got %#v", result.Entry.Types.Nodes[letC.Value])
	}
	if named.Name != "Circle" || len(named.TypeArgs) != 1 || !typeinfo.IsBuiltinNamed(named.TypeArgs[0], "i32") {
		t.Fatalf("expected inferred Circle<i32>, got %#v", named)
	}
}

func TestTypecheckerInfersGenericCompositeLiteralTypeArgs(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point<T> struct {
    Value: T
}

fn main() void {
    let p = .Point{ .Value = 2 }
    p
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	letP, ok := mainFn.Body.Stmts[0].(*ast.LetStmt)
	if !ok {
		t.Fatalf("expected let stmt, got %T", mainFn.Body.Stmts[0])
	}
	named, ok := result.Entry.Types.Nodes[letP.Value].(*typeinfo.NamedType)
	if !ok || named == nil {
		t.Fatalf("expected named type for composite literal, got %#v", result.Entry.Types.Nodes[letP.Value])
	}
	if named.Name != "Point" || len(named.TypeArgs) != 1 || !typeinfo.IsBuiltinNamed(named.TypeArgs[0], "i32") {
		t.Fatalf("expected inferred Point<i32>, got %#v", named)
	}
}

func TestTypecheckerAllowsImplicitNumericWidening(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i64 {
    let a: i32 = 1
    let b: i64 = a
    return b
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsUnionMemberAssignment(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Token union {
    i32,
    bool,
}

fn main() i32 {
    let a: Token = 1
    let b: Token = true
    return 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsInvalidUnionMemberAssignment(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Token union {
    i32,
    bool,
}

fn main() i32 {
    let bad: Token = "text"
    return 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected union member assignment diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, "not a valid member") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected invalid union member diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerPrefersExactUnionMemberMatch(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type MaybeInt union {
    ?i32,
    i32,
}

fn main() i32 {
    let a: MaybeInt = 1
    let b: MaybeInt = 1 as i32
    let c: MaybeInt = 1 as ?i32
    return 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsCompositeLiteralCastWithTargetType(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 1
    Y: i32 = 2
}

fn main() i32 {
    let p = .{} as Point
    return p.X
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsValueToSatisfyInterfaceValueMethod(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn Point::Show(self) {
}

type Shape interface {
    Show(self)
}

fn main() i32 {
    let p: Point = .{}
    let s: Shape = p
    return 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsAmbiguousUnionMemberAssignment(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Both union {
    i32,
    i32,
}

fn main() i32 {
    let bad: Both = 1
    return 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected ambiguous union member diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidOperation && strings.Contains(diag.Message, "matches multiple members") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected ambiguous union member diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsExplicitUnionMemberCast(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type MaybeInt union {
    ?i32,
    i32,
}

fn main() i32 {
    let picked: MaybeInt = 1 as MaybeInt::i32
    return 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsUnionExtractCastToExactMember(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Token union {
    i32,
    i64,
}

fn main() i32 {
    let value: Token = 1
    let out = value as i32
    return out
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsBinaryOpsOnUnionValues(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Token union {
    i32,
    i64,
}

fn main() i32 {
    let left: Token = 1
    let right: Token = 2 as i64
    if left == right {
        return 1
    }
    return 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected union binary-operation diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidOperation && strings.Contains(diag.Message, "union values do not support direct binary operations") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected union binary-operation diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsNumericToStringCast(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() void {
    let a = 42 as str
    let b = 1.5 as str
    print(a)
    print(b)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsConcreteTypeAssignmentToInterface(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Stringer interface {
    String(self) str
}

type Name struct {
    Value: i32 = 0
}

fn Name::String(self) str {
    return 1 as str
}

fn main() str {
    let n: Name = .{ .Value = 1 }
    let s: Stringer = n
    return s.String()
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerMatchesInterfaceSelfReturnAndTypedCompositeLiteral(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Shape interface {
    New() Self
    Draw(&self)
}

type Point struct {
    value: i32 = 0
}

fn Point::New() Self {
    return .Point{}
}

fn Point::Draw(&self) {
}

fn main() void {
    let p: Point = .Point{}
    let s: Shape = p
    _ = s
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsStaticIsChecks(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Stringer interface {
    String(self) str
}

type Name struct {
    Value: i32 = 0
}

fn Name::String(self) str {
    return 1 as str
}

fn main() i32 {
    let n: Name = .{ .Value = 1 }
    if n is Stringer {
        return 1
    }
    if n is i32 {
        return 2
    }
    return 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsStaticStructField(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
    static: Counter i32 = 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected static-struct-field diagnostic")
	}
	if !strings.Contains(result.Diagnostics.Diagnostics()[0].Message, "static struct fields are not supported") {
		t.Fatalf("expected static-field rejection diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsRuntimeUnionTypeTest(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Token union {
    i32,
    i64,
}

	fn main() bool {
	    let value: Token = 1
	    return value is i32
	}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerNarrowsUnionTypeInIfBranch(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Token union {
    i32,
    i64,
}

fn main() i32 {
    let value: Token = 1
    if value is i32 {
        let narrowed: i32 = value
        return narrowed
    }
    return 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerNarrowsUnionTypeInWhileBody(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Token union {
    i32,
    i64,
}

fn main() i32 {
    let value: Token = 1
    while value is i32 {
        let narrowed: i32 = value
        return narrowed
    }
    return 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerNarrowsUnionTypeInElseBranch(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Token union {
    i32,
    i64,
}

fn main() i64 {
    let value: Token = 2 as i64
    if value is i32 {
        return 0 as i64
    } else {
        let narrowed: i64 = value
        return narrowed
    }
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerNarrowsUnionTypeInNegatedIfBranch(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Token union {
    i32,
    i64,
}

fn main() i64 {
    let value: Token = 2 as i64
    if !(value is i32) {
        let narrowed: i64 = value
        return narrowed
    }
    return 0 as i64
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerNarrowsUnionTypeInMatchTypeArm(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Token union {
    i32,
    i64,
}

fn main() i32 {
    let value: Token = 1
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
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAcceptsMatchExpression(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Token union {
    i32,
    i64,
}

fn main() i32 {
    let value: Token = 1
    let out: i32 = match value {
        is i32 => {
            value + value
        }
        _ => {
            0
        }
    }
    return out
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsRuntimeInterfaceToConcreteTypeTest(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Stringer interface {
    String() str
}

fn main(s: Stringer) bool {
    return s is str
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected runtime interface type-test diagnostic")
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidOperation && strings.Contains(diag.Message, "runtime interface type tests are not implemented yet") {
			return
		}
	}
	t.Fatalf("expected runtime interface type-test diagnostic, got %#v", result.Diagnostics.Diagnostics())
}

func TestTypecheckerRejectsConcreteTypeThatMissesInterfaceMethod(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Stringer interface {
    String() str
}

type Name struct {
    value: i32 = 0
}

fn main() i32 {
    let n: Name = .{ .value = 1 }
    let s: Stringer = n
    return 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected interface assignment diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, `missing method String() str`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected detailed missing-interface-method diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsConcreteTypeWithIncompatibleInterfaceMethod(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Stringer interface {
    String() str
}

type Name struct {
    value: i32 = 0
}

fn Name::String(self) i32 {
    return self.value
}

fn main() i32 {
    let n: Name = .{ .value = 1 }
    let s: Stringer = n
    return 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected interface signature diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, `type Name does not implement Stringer`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected detailed incompatible-interface-method diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsConcreteTypeWithWrongInterfaceReceiverModifier(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Reader interface {
    read(&mut self, buf []u8) i32
}

type File struct {
    value: i32 = 0
}

fn File::read(&self, buf: []u8) i32 {
    return 0
}

fn main() i32 {
    let f: File = .{}
    let r: Reader = f
    return 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected interface receiver mismatch diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, `type File does not implement Reader`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected interface receiver mismatch diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsCrossModuleMethodDeclaration(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
import "util/name"

fn name::Name::String(self) str {
    return "x"
}
`)
	mustWriteType(t, filepath.Join(root, "util", "name.ferr"), `
type Name struct {
    value: i32 = 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected cross-module method declaration diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidOperation && strings.Contains(diag.Message, "cross-module method declarations are not allowed") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected cross-module method declaration diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsMethodNamedLikeType(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn Point::Point(*self, x: i32) {
    self.X = x
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsMethodNamedLikeTypeOnRefReceiver(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn Point::Point(&mut self) {
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsRemovedDestructorSyntax(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn Point::~Point(*self, x: i32) i32 {
    return x
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected removed destructor syntax diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if strings.Contains(diag.Message, "special destructor syntax has been removed") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected removed destructor syntax diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsDirectMethodCallNamedLikeType(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn Point::Point(self) i32 {
    return self.X
}

fn main() i32 {
    let p: Point = .Point{}
    return p.Point()
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsStaticMethodNamedLikeType(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn Point::Point() Point {
    return .{}
}

fn main() i32 {
    let p = Point::Point()
    return p.X
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsFieldMutationThroughImmutableBinding(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn main() void {
    let p: Point = .{}
    p.X = 1
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected immutable assignment diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrConstantReassignment && strings.Contains(diag.Message, "cannot assign through immutable access path") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected immutable assignment diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsPrivateImportedStructFieldAccess(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "pkg.ferr"), `
type Box struct {
    hidden: i32 = 1
    Visible: i32 = 2
}
`)
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
import "pkg"

fn main() i32 {
    let b: pkg::Box = .{}
    return b.hidden
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected private field access diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrSymbolNotExported && strings.Contains(diag.Message, `"hidden" is not exported`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected private field export diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsPrivateSameModuleStructFieldAccessOutsideMethods(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Box struct {
    hidden: i32 = 1
}

fn main() i32 {
    let b: Box = .{}
    return b.hidden
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsPrivateFieldAccessInsideOwnerMethods(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Box struct {
    hidden: i32 = 1
}

fn Box::Read(&self) i32 {
    return self.hidden
}

fn Box::Set(&mut self, value: i32) void {
    self.hidden = value
}

fn main() i32 {
    let mut b: Box = .{}
    b.Set(3)
    return b.Read()
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsPrivateFieldInCompositeSameModule(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Box struct {
    hidden: i32 = 1
    Visible: i32 = 2
}

fn main() i32 {
    let b: Box = .{ .hidden = 3, .Visible = 4 }
    return b.Visible
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsFieldMutationThroughMutableBinding(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn main() void {
    let mut p: Point = .{}
    p.X = 1
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsMutationThroughMutableReference(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn bump(p: &mut Point) void {
    (*p).X = 1
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsImmutableArgumentForMutableParameter(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn mutate(mut x: i32) i32 {
    return x
}

fn main() i32 {
    let x: i32 = 1
    return mutate(x)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected mutable parameter argument diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, "mutable parameter requires mutable argument binding") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected mutable parameter argument diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsMutationThroughImmutableReference(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn bump(p: &Point) void {
    (*p).X = 1
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected immutable reference assignment diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrConstantReassignment && strings.Contains(diag.Message, "cannot assign through immutable access path") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected immutable reference assignment diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsMutableReferenceFromImmutableBinding(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn main() void {
    let p: Point = .{}
    let m = &mut p
    m
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected mutable reference creation diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidOperation && strings.Contains(diag.Message, "cannot create mutable reference from immutable value") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected mutable reference creation diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsPlainValueForBorrowParameter(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn read(p: &Point) i32 {
    return p.X
}

fn main() i32 {
    let p: Point = .{}
    return read(p)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected borrow parameter diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, "borrow parameter requires explicit reference argument") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected borrow parameter diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsPlainImmutableValueForMutableBorrowParameter(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn bump(p: &mut Point) i32 {
    return p.X
}

fn main() i32 {
    let p: Point = .{}
    return bump(p)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected mutable borrow parameter diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, "mutable borrow parameter requires explicit `&mut` argument") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected mutable borrow parameter diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerUsesBuiltInBoolConstants(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() bool {
    if true {
        return false
    }
    return true
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	ifStmt := mainFn.Body.Stmts[0].(*ast.IfStmt)
	if !typeinfo.IsBuiltinNamed(result.Entry.Types.Nodes[ifStmt.Cond], "bool") {
		t.Fatalf("expected bool type for true constant, got %#v", result.Entry.Types.Nodes[ifStmt.Cond])
	}
}

func TestTypecheckerAllowsUndefinedWithContext(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let mut x: i32 = undefined
    x = 1
    return x
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsNumericNarrowingAndLiteralOverflow(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let a: i64 = 1
    let b: i32 = a
    let c: i8 = 1000
    return b
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected numeric narrowing diagnostics")
	}
	mismatchCount := 0
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch {
			mismatchCount++
		}
	}
	if mismatchCount < 2 {
		t.Fatalf("expected at least two %s diagnostics, got %#v", diagnostics.ErrTypeMismatch, result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsDefaultNumericLiteralOverflow(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let huge = 10235543634636243636263462346
    return 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected numeric default overflow diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && diag.Message == "numeric literal 10235543634636243636263462346 does not fit in default numeric type i32" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected default overflow diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsCatchFallbackValue(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Io error { denied }

fn main(x: Io!i32) i32 {
    return x catch -1
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerTreatsRecoverAsBuiltinFunction(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() str {
    return recover()
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	ret := mainFn.Body.Stmts[0].(*ast.ReturnStmt)
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected recover call, got %T", ret.Value)
	}
	if _, ok := result.Entry.Types.Nodes[call].(*typeinfo.StringType); !ok {
		t.Fatalf("expected recover() to typecheck as str (StringType), got %#v", result.Entry.Types.Nodes[call])
	}
}

func TestTypecheckerRejectsRecoverArguments(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() string {
    return recover("x")
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected wrong argument count diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrWrongArgumentCount {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrWrongArgumentCount, result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerDoesNotCascadeNotCallableAfterMissingImportedSymbol(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "ferret_libs_dev", "std", "math.ferr"), `
fn ClampToZero(value: i32) i32 {
    return value
}
`)
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
import "std/math"

fn main() i32 {
    return math::ClampToZeros(-34)
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected missing imported symbol diagnostic")
	}
	notCallable := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrNotCallable {
			notCallable = true
			break
		}
	}
	if notCallable {
		t.Fatalf("did not expect %s cascade, got %#v", diagnostics.ErrNotCallable, result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRequiresCatchHandlerToExit(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Io error { denied }

fn log(x: Io) void {}

fn main(x: Io!i32) i32 {
    let file = x catch |err| {
        log(err)
    }
    return file
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected catch early-exit diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Message == "catch handler block must exit early" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected catch early-exit diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerReportsNumericNarrowingMessage(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let num1: i32 = 1
    let num2: i8 = num1
    return 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected numeric narrowing diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && diag.Message == "cannot implicitly narrow i32 to i8" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected narrowing diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsExplicitNumericCast(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let num1: i32 = 1
    let num2: i8 = num1 as i8
    return num2 as i32
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerResolvesMethodCalls(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
    Y: i32 = 0
}

fn Point::Len2(self) i32 {
    return self.X * self.X + self.Y * self.Y
}

fn Point::Len(*self) i32 {
    return self.X * self.X + self.Y * self.Y
}

fn main() i32 {
    let p: Point = .{ .X = 3, .Y = 4 }
    let q: *Point
    return p.Len2() + q.Len()
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsPointerMethodCallOnValue(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn Point::Len(*self) i32 {
    return self.X
}

fn main() i32 {
    let p: Point = .{}
    return p.Len()
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected pointer receiver method diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrMethodNotFound {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrMethodNotFound, result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerReportsMissingMethod(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn main() i32 {
    let p: Point = .{ .X = 1 }
    return p.Missing()
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected missing method diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrMethodNotFound {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrMethodNotFound, result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsPlainStructCopyFromLetBinding(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn main() i32 {
    let p: Point = .{ .X = 1 }
    let q = p
    return p.X
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsCopyAsNotYetImplemented(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
    Y: i32 = 0
}

fn main() i32 {
    let p: Point = .{ .X = 1, .Y = 2 }
    let q = copy p
    return p.X + q.Y
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected invalid copy diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidCopy && strings.Contains(diag.Message, "`copy` is not yet implemented") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected not-yet-implemented copy diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsCopyOfOwningPointer(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Conn struct {}

fn bad(c: *Conn) void {
    let d = copy c
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected invalid copy diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidCopy && strings.Contains(diag.Message, "`copy` is not yet implemented") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrInvalidCopy, result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsCopyOfRawPointer(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn bad(p: ^i32) void {
    let d = copy p
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected invalid copy diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidCopy && strings.Contains(diag.Message, "`copy` is not yet implemented") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected copy not-yet-implemented diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsNonConstConstInitializer(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let x = 1
    const y = x
    return y
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected const initializer diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && diag.Message == "constant initializer must be compile-time evaluable" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected const-evaluable diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsNonConstComptimeArgument(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn id(comptime T: i32, x: i32) i32 {
    return x
}

fn main() i32 {
    let x = 1
    return id(x, 2)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected comptime argument diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && diag.Message == "argument to comptime parameter must be compile-time evaluable" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected comptime-argument diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsInvalidExplicitCast(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let x = "hi" as i32
    return 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected invalid cast diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidCast {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrInvalidCast, result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerBindsLocalSymbolTypes(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type MyErr error {
    Oops
}

fn run(items: [3]i32) i32 {
    let r: MyErr!i32 = undefined
    let x = 1
    return r catch |e| { return x }
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.Types == nil || result.Entry.Bindings == nil {
		t.Fatalf("expected entry types+bindings, got %#v", result.Entry)
	}

	runFn := findTypeFunc(t, result.Entry.AST, "run")

	// Param type binding.
	paramIdent := runFn.Params[0].Name
	paramRes := result.Entry.Bindings.Nodes[paramIdent]
	if paramRes == nil || paramRes.Symbol == nil {
		t.Fatalf("expected param resolution for items, got %#v", paramRes)
	}
	paramType := result.Entry.Types.Symbols[paramRes.Symbol.ID]
	arr, ok := paramType.(*typeinfo.ArrayType)
	if !ok || arr.Len != 3 || !typeinfo.IsBuiltinNamed(arr.Inner, "i32") {
		t.Fatalf("expected items type [3]i32, got %T %#v", paramType, paramType)
	}

	// let r binding.
	letR := runFn.Body.Stmts[0].(*ast.LetStmt)
	rRes := result.Entry.Bindings.Nodes[letR.Name]
	if rRes == nil || rRes.Symbol == nil {
		t.Fatalf("expected let resolution for r, got %#v", rRes)
	}
	rType := result.Entry.Types.Symbols[rRes.Symbol.ID]
	errUnion, ok := rType.(*typeinfo.ErrorUnionType)
	if !ok || !typeinfo.IsBuiltinNamed(errUnion.Value, "i32") {
		t.Fatalf("expected r type MyErr!i32, got %T %#v", rType, rType)
	}
	errNamed, ok := errUnion.Error.(*typeinfo.NamedType)
	if !ok || errNamed.Name != "MyErr" {
		t.Fatalf("expected r error type MyErr, got %T %#v", errUnion.Error, errUnion.Error)
	}

	// let x binding.
	letX := runFn.Body.Stmts[1].(*ast.LetStmt)
	xRes := result.Entry.Bindings.Nodes[letX.Name]
	if xRes == nil || xRes.Symbol == nil {
		t.Fatalf("expected let resolution for x, got %#v", xRes)
	}
	xType := result.Entry.Types.Symbols[xRes.Symbol.ID]
	if !typeinfo.IsBuiltinNamed(xType, "i32") {
		t.Fatalf("expected x type i32, got %T %#v", xType, xType)
	}

	// catch payload binding.
	ret := runFn.Body.Stmts[2].(*ast.ReturnStmt)
	catchExpr := ret.Value.(*ast.CatchExpr)
	payloadRes := result.Entry.Bindings.Nodes[catchExpr.Payload]
	if payloadRes == nil || payloadRes.Symbol == nil {
		t.Fatalf("expected catch payload resolution for e, got %#v", payloadRes)
	}
	payloadType := result.Entry.Types.Symbols[payloadRes.Symbol.ID]
	payloadNamed, ok := payloadType.(*typeinfo.NamedType)
	if !ok || payloadNamed.Name != "MyErr" {
		t.Fatalf("expected catch payload type MyErr, got %T %#v", payloadType, payloadType)
	}
}

func findTypeFunc(t *testing.T, mod *ast.Module, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range mod.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Text() == name {
			return fn
		}
	}
	t.Fatalf("function %s not found", name)
	return nil
}

func mustWriteType(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestTypecheckerUsesReferenceTypesForAddressOf(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn readPoint() i32 {
    let p: Point = .{}
    let r = &p
    let x = *r
    return x.X
}

fn writePoint() void {
    let mut p: Point = .{}
    let m = &mut p
    m
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	readFn := findTypeFunc(t, result.Entry.AST, "readPoint")
	writeFn := findTypeFunc(t, result.Entry.AST, "writePoint")
	letR := readFn.Body.Stmts[1].(*ast.LetStmt)
	letX := readFn.Body.Stmts[2].(*ast.LetStmt)
	letM := writeFn.Body.Stmts[1].(*ast.LetStmt)
	rType, ok := result.Entry.Types.Nodes[letR.Value].(*typeinfo.RefType)
	if !ok || rType.Mutable {
		t.Fatalf("expected immutable RefType for &p, got %#v", result.Entry.Types.Nodes[letR.Value])
	}
	mType, ok := result.Entry.Types.Nodes[letM.Value].(*typeinfo.RefType)
	if !ok || !mType.Mutable {
		t.Fatalf("expected mutable RefType for &mut p, got %#v", result.Entry.Types.Nodes[letM.Value])
	}
	if _, ok := result.Entry.Types.Nodes[letX.Value].(*typeinfo.NamedType); !ok {
		t.Fatalf("expected dereference of ref to produce named value type, got %#v", result.Entry.Types.Nodes[letX.Value])
	}
}

func TestTypecheckerUsesRawPointerTypesForRawAddress(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn main() void {
    let mut p: Point = .{}
    unsafe {
        let r = @p
        let m = @mut p
        let x = *m
        r
        x
    }
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	unsafeStmt := mainFn.Body.Stmts[1].(*ast.UnsafeStmt)
	letR := unsafeStmt.Body.Stmts[0].(*ast.LetStmt)
	letM := unsafeStmt.Body.Stmts[1].(*ast.LetStmt)
	letX := unsafeStmt.Body.Stmts[2].(*ast.LetStmt)
	rType, ok := result.Entry.Types.Nodes[letR.Value].(*typeinfo.RawPtrType)
	rNamed, rok := rType.Inner.(*typeinfo.NamedType)
	if !ok || !rok || rNamed.Name != "Point" {
		t.Fatalf("expected RawPtrType for @p, got %#v", result.Entry.Types.Nodes[letR.Value])
	}
	mType, ok := result.Entry.Types.Nodes[letM.Value].(*typeinfo.RawPtrType)
	mNamed, mok := mType.Inner.(*typeinfo.NamedType)
	if !ok || !mok || mNamed.Name != "Point" {
		t.Fatalf("expected RawPtrType for @mut p, got %#v", result.Entry.Types.Nodes[letM.Value])
	}
	if _, ok := result.Entry.Types.Nodes[letX.Value].(*typeinfo.NamedType); !ok {
		t.Fatalf("expected dereference of raw pointer to produce named value type, got %#v", result.Entry.Types.Nodes[letX.Value])
	}
}

func TestTypecheckerRejectsRawAddressOutsideUnsafe(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn main() void {
    let p: Point = .{}
    let r = @p
    r
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected raw-address unsafe diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidOperation && strings.Contains(diag.Message, "raw address operator requires unsafe block") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected raw-address unsafe diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerTypesArrayIndexing(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main(items: [3]i32) i32 {
    let v = items[1]
    return v
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	letV := mainFn.Body.Stmts[0].(*ast.LetStmt)
	if !typeinfo.IsBuiltinNamed(result.Entry.Types.Nodes[letV.Value], "i32") {
		t.Fatalf("expected items[1] to typecheck as i32, got %#v", result.Entry.Types.Nodes[letV.Value])
	}
}

func TestTypecheckerInfersArrayLengthFromUnderscoreType(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let items: [_]i32 = [_]i32{1, 2, 3}
    let v = items[1]
    return v
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	letItems := mainFn.Body.Stmts[0].(*ast.LetStmt)
	itemsRes := result.Entry.Bindings.Nodes[letItems.Name]
	itemsType := result.Entry.Types.Symbols[itemsRes.Symbol.ID]
	arr, ok := itemsType.(*typeinfo.ArrayType)
	if !ok || arr.Len != 3 || !typeinfo.IsBuiltinNamed(arr.Inner, "i32") {
		t.Fatalf("expected items type [3]i32, got %T %#v", itemsType, itemsType)
	}
	letV := mainFn.Body.Stmts[1].(*ast.LetStmt)
	if !typeinfo.IsBuiltinNamed(result.Entry.Types.Nodes[letV.Value], "i32") {
		t.Fatalf("expected items[1] to typecheck as i32, got %#v", result.Entry.Types.Nodes[letV.Value])
	}
}

func TestTypecheckerRejectsSliceLiteralsAsNotImplemented(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let items: []i32 = []i32{1, 2, 3}
    return items[0]
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected slice literal not implemented diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidType && strings.Contains(diag.Message, "slice literals are not yet implemented") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected slice literal not implemented diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerTypesLenForArrayAndSlice(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn lenArray() usize {
    let items: [_]i32 = [_]i32{1, 2, 3}
    return len(items)
}

fn lenSlice(items: []i32) usize {
    return len(items)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsLenOnNonArraySlice(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() usize {
    let x: i32 = 1
    return len(x)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected len type diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidType && strings.Contains(diag.Message, "len expects an array or slice argument") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected len type diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsPrintingReferenceViaAny(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() void {
    let mut x = 10
    let y = &mut x
    print(y)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsDiscardAssignment(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let a = 10
    _ = a
    return 0
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerResolvesExplicitReferenceReceivers(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn Point::Read(&self) i32 {
    return self.X
}

fn Point::Bump(&mut self) i32 {
    return self.X + 1
}

fn main() i32 {
    let mut p: Point = .{ .X = 1 }
    let a = (&p).Read()
    let b = (&mut p).Bump()
    return a + b
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerResolvesAttachedReferenceReceiversFromValueCalls(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

fn Point::Read(&self) i32 {
    return self.X
}

fn Point::Bump(&mut self) i32 {
    self.X++
    return self.X
}

fn main() i32 {
    let mut p: Point = .{ .X = 1 }
    return p.Read() + p.Bump()
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsOwningPointerToReferenceContainingType(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Inner struct {
    Ref: &i32
}

type Outer struct {
    Child: *Inner
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected heap reference containment diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidType && strings.Contains(diag.Message, "owning heap types cannot contain references") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected heap reference containment diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsModuleLevelReferenceBinding(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
let GlobalRef: &i32 = undefined
`)

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected module-level reference binding diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidOperation && strings.Contains(diag.Message, "module-level bindings cannot have reference type") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected module-level reference binding diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}
