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
	"compiler/internal/ir/mir"
)

func TestTypecheckerChecksWorkspaceExampleSubset(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
import "math/vec2"
import "util/build"

fn main() -> i32 {
    let p = build::Origin()
    if p == .{ .X = 0, .Y = 0 } {
        return vec2::Len2(p)
    }
    return 1
}
`)
	mustWriteType(t, filepath.Join(root, "math", "vec2.fer"), `
type Vec2 struct {
    X: i32 = 0
    Y: i32 = 0
}

fn Len2(v: Vec2) -> i32 {
    return v.X * v.X + v.Y * v.Y
}
`)
	mustWriteType(t, filepath.Join(root, "util", "build.fer"), `
import "math/vec2"

fn Origin() -> vec2::Vec2 {
    return .{}
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn bad() -> i32 {
    return
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn Add(x: i32, y: i32) -> i32 {
    return x + y
}

fn main() -> i32 {
    return Add("x", 1)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i64 {
    let a = 1
    let b = 1.5
    let c: i64 = 1
    return c
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Box<T> struct {
    Value: T
}

fn Identity<T>(value: T) -> T {
    return value
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Box<T> struct {
    Value: T
}

fn main() -> i32 {
    let b: Box<i32> = .{ .Value = 7 }
    return b.Value
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Box<T> struct {
    Value: T
}

fn Box<T>::Get(&self) -> T {
    return self.Value
}

fn main() -> i32 {
    let b: Box<i32> = .{ .Value = 7 }
    return b.Get()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Circle<T> struct {
    Rad: T
}

fn Circle<T>::New(v: T) -> Self {
    return .{ .Rad = v }
}

fn main() -> Circle<i32> {
    return Circle<i32>::New(1)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
 type Point<T> struct {
     Value: T = 0
 }

 fn Point::Calc(&self) -> i32 {
     return self.Value
 }
 `)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
#[extern]
fn Identity<T>(value: T) -> T;

fn main() -> i32 {
    return Identity(1)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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

func TestTypecheckerInfersParamTypeFromDefaultValue(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn add(base = 2, extra = base + 1) -> i32 {
    return base + extra
}

fn main() -> i32 {
    return add()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	addFn := findTypeFunc(t, result.Entry.AST, "add")
	addSym := result.Entry.Bindings.FunctionSymbols[addFn]
	fnType, ok := result.Entry.Types.Symbols[addSym.ID].(*typeinfo.FuncType)
	if !ok {
		t.Fatalf("expected function type, got %T", result.Entry.Types.Symbols[addSym.ID])
	}
	if len(fnType.Params) != 2 {
		t.Fatalf("expected 2 params, got %#v", fnType.Params)
	}
	if !typeinfo.IsBuiltinNamed(fnType.Params[0].Type, "i32") || !typeinfo.IsBuiltinNamed(fnType.Params[1].Type, "i32") {
		t.Fatalf("expected inferred i32 params, got %#v", fnType.Params)
	}
}

func TestTypecheckerRejectsGenericConstraintMismatch(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Reader interface {
    Read(&self) -> i32
}

#[extern]
fn Use<T: Reader>(value: T) -> void;

fn main() -> void {
    Use(1)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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

func TestTypecheckerReportsConstraintDeclarationMismatch(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type numeric union { i32, i64 }

fn Id<T: numeric>(v: T) -> T {
    return v
}

fn main() -> void {
    Id("x")
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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

func TestTypecheckerAllowsNamedConstraintTypeAsConcreteType(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type numeric union { i32, i64 }

fn main() -> void {
    let x: numeric = 1
    x
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerSupportsNamedAndInlineConstraints(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Writer interface {
    write(&self, []u8) -> i32
}

type W Writer

type FileWriter struct {}

fn FileWriter::write(&self, data: []u8) -> i32 {
    data
    return 0
}

fn write_all<T: W>(x: T) -> void {
    x
}

fn close_it<T: interface {
    close(&self) -> void
}>(x: T) -> void {
    x.close()
}

type Door struct {}

fn Door::close(&self) -> void {}

fn main() -> void {
    let w = .FileWriter{}
    let d = .Door{}
    write_all(w)
    close_it(d)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerDoesNotConflictNestedCallExpectedTypeInference(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type numeric union {
    i32,
    f32,
    u32,
}

fn add_numbers<T: numeric>(a: T, b: T) -> T {
    return a + b
}

fn main() -> void {
    print(add_numbers(1, 2))
    print(add_numbers(1.4, 2.7))
    print(add_numbers(1, 2))
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsMethodsFromTypeParamInterfaceConstraint(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Reader interface {
    Read(&self) -> i32
}

fn ReadValue<T: Reader>(value: T) -> i32 {
    return value.Read()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Reader interface {
    Read(&self) -> i32
}

type Box<T: Reader> struct {
    Value: T
}

fn Box<T>::New(value: T) -> Self {
    return .{ .Value = value }
}

fn main() -> void {
    Box<i32>::New(1)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32
}

fn Add<T>(a: T, b: T) -> T {
    return a + b
}

fn main() -> void {
    let p: Point = .{ .X = 1 }
    Add(p, p)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn Add<T>(a: T, b: T) -> T {
    return a + b
}

fn main() -> i32 {
    return Add<bool>(1, 2)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Circle<T> struct {
    Rad: T
}

fn Circle<T>::New(v: T) -> Self {
    return .{ .Rad = v }
}

fn main() -> void {
    let c = Circle::New(1)
    c
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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

func TestTypecheckerRejectsNonCanonicalRecursiveGenericSelfUse(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Node<T> struct {
    Next: ?*Node<Node<T>>
    Value: T
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected recursive generic self-use diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag != nil && diag.Code == diagnostics.ErrInvalidType && strings.Contains(diag.Message, "must preserve declaration type parameters") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected canonical recursive generic diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsNonCanonicalGenericMethodOwner(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point<T> struct {
    X: T
}

fn Point<i32>::Incr(&mut self, dx: i32) -> void {
    self.X += dx
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected canonical generic owner diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag != nil && diag.Code == diagnostics.ErrInvalidType && strings.Contains(diag.Message, "attached methods for generic type") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected canonical generic owner diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerInfersGenericCompositeLiteralTypeArgs(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point<T> struct {
    Value: T
}

fn main() -> void {
    let p = .Point{ .Value = 2 }
    p
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i64 {
    let a: i32 = 1
    let b: i64 = a
    return b
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsUnionMemberAssignment(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Token union {
    i32,
    bool,
}

fn main() -> i32 {
    let a: Token = 1
    let b: Token = true
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsInvalidUnionMemberAssignment(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Token union {
    i32,
    bool,
}

fn main() -> i32 {
    let bad: Token = "text"
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type MaybeInt union {
    ?i32,
    i32,
}

fn main() -> i32 {
    let a: MaybeInt = 1
    let b: MaybeInt = 1 as i32
    let c: MaybeInt = 1 as ?i32
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsCompositeLiteralCastWithTargetType(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 1
    Y: i32 = 2
}

fn main() -> i32 {
    let p = .{} as Point
    return p.X
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsValueToSatisfyInterfaceValueMethod(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn Point::Show(self) {
}

type Shape interface {
    Show(self)
}

fn main() -> i32 {
    let p: Point = .{}
    let s: Shape = p
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsAmbiguousUnionMemberAssignment(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Both union {
    i32,
    i32,
}

fn main() -> i32 {
    let bad: Both = 1
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type MaybeInt union {
    ?i32,
    i32,
}

fn main() -> i32 {
    let picked: MaybeInt = 1 as MaybeInt::i32
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsUnionExtractCastToExactMember(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Token union {
    i32,
    i64,
}

fn main() -> i32 {
    let value: Token = 1
    let out = value as i32
    return out
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsBinaryOpsOnUnionValues(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Token union {
    i32,
    i64,
}

fn main() -> i32 {
    let left: Token = 1
    let right: Token = 2 as i64
    if left == right {
        return 1
    }
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    let a = 42 as str
    let b = 1.5 as str
    print(a)
    print(b)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsMutationThroughStringSliceView(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main(s: str) -> void {
    let mut bytes: []u8 = s as []u8
    bytes[0] = 1
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected readonly string slice mutation diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrConstantReassignment && strings.Contains(diag.Message, "cannot assign through immutable access path") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected readonly string slice mutation diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsStringIndexing(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main(s: str) -> char {
    return s[0]
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	ret, ok := mainFn.Body.Stmts[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", mainFn.Body.Stmts[0])
	}
	idx, ok := ret.Value.(*ast.IndexExpr)
	if !ok {
		t.Fatalf("expected index expr, got %T", ret.Value)
	}
	if !typeinfo.IsBuiltinNamed(result.Entry.Types.Nodes[idx], "char") {
		t.Fatalf("expected str index type char, got %#v", result.Entry.Types.Nodes[idx])
	}
}

func TestTypecheckerTypesCharAndByteLiterals(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    let c = 'é'
    let b = b'h'
    print(c)
    print(b)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	charLet, ok := mainFn.Body.Stmts[0].(*ast.LetStmt)
	if !ok {
		t.Fatalf("expected first stmt let, got %T", mainFn.Body.Stmts[0])
	}
	byteLet, ok := mainFn.Body.Stmts[1].(*ast.LetStmt)
	if !ok {
		t.Fatalf("expected second stmt let, got %T", mainFn.Body.Stmts[1])
	}
	if !typeinfo.IsBuiltinNamed(result.Entry.Types.Nodes[charLet.Value], "char") {
		t.Fatalf("expected char literal type char, got %#v", result.Entry.Types.Nodes[charLet.Value])
	}
	if !typeinfo.IsBuiltinNamed(result.Entry.Types.Nodes[byteLet.Value], "u8") {
		t.Fatalf("expected byte literal type u8, got %#v", result.Entry.Types.Nodes[byteLet.Value])
	}
}

func TestTypecheckerRejectsStringIndexAssignment(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main(mut s: str) -> void {
    s[0] = 1
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected readonly string index assignment diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrConstantReassignment && strings.Contains(diag.Message, "cannot assign through immutable access path") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected readonly string index assignment diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerInfersStringLiteralAsByteArrayWithoutContext(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> usize {
    let bytes = "hi"
    return len(bytes)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	letBytes, ok := mainFn.Body.Stmts[0].(*ast.LetStmt)
	if !ok {
		t.Fatalf("expected let stmt, got %T", mainFn.Body.Stmts[0])
	}
	arr, ok := result.Entry.Types.Nodes[letBytes.Value].(*typeinfo.ArrayType)
	if !ok {
		t.Fatalf("expected inferred string literal type [N]u8, got %#v", result.Entry.Types.Nodes[letBytes.Value])
	}
	if arr.Len != 2 || !typeinfo.IsBuiltinNamed(arr.Inner, "u8") {
		t.Fatalf("expected inferred string literal type [2]u8, got %#v", arr)
	}
}

func TestTypecheckerContextualizesStringLiteralAsStr(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    let text: str = "hi"
    print("ok")
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	letText, ok := mainFn.Body.Stmts[0].(*ast.LetStmt)
	if !ok {
		t.Fatalf("expected let stmt, got %T", mainFn.Body.Stmts[0])
	}
	if _, ok := result.Entry.Types.Nodes[letText.Value].(*typeinfo.StringType); !ok {
		t.Fatalf("expected contextual string literal type str for let binding, got %#v", result.Entry.Types.Nodes[letText.Value])
	}
	printStmt, ok := mainFn.Body.Stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected expr stmt, got %T", mainFn.Body.Stmts[1])
	}
	printCall, ok := printStmt.Value.(*ast.CallExpr)
	if !ok || len(printCall.Args) != 1 {
		t.Fatalf("expected print call, got %T %#v", printStmt.Value, printStmt.Value)
	}
	if _, ok := result.Entry.Types.Nodes[printCall.Args[0]].(*typeinfo.StringType); !ok {
		t.Fatalf("expected contextual string literal type str for print arg, got %#v", result.Entry.Types.Nodes[printCall.Args[0]])
	}
}

func TestTypecheckerAllowsRawPartsCastToStr(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    let bytes: []u8 = []u8{104, 105}
    unsafe {
        let ptr = bytes as ^const u8
        let text = (ptr, 2 as usize) as str
        print(text)
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	unsafeStmt, ok := mainFn.Body.Stmts[1].(*ast.UnsafeStmt)
	if !ok {
		t.Fatalf("expected unsafe stmt, got %#v", mainFn.Body.Stmts[1])
	}
	letText := unsafeStmt.Body.Stmts[1].(*ast.LetStmt)
	if _, ok := result.Entry.Types.Nodes[letText.Value].(*typeinfo.StringType); !ok {
		t.Fatalf("expected raw-parts cast to typecheck as str, got %#v", result.Entry.Types.Nodes[letText.Value])
	}
}

func TestTypecheckerAllowsConcreteTypeAssignmentToInterface(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Stringer interface {
    String(self) -> str
}

type Name struct {
    Value: i32 = 0
}

fn Name::String(self) -> str {
    return 1 as str
}

fn main() -> str {
    let n: Name = .{ .Value = 1 }
    let s: Stringer = n
    return s.String()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerMatchesInterfaceSelfReturnAndTypedCompositeLiteral(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Shape interface {
    New() -> Self
    Draw(&self)
}

type Point struct {
    value: i32 = 0
}

fn Point::New() -> Self {
    return .Point{}
}

fn Point::Draw(&self) {
}

fn main() -> void {
    let p: Point = .Point{}
    let s: Shape = p
    _ = s
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsStaticIsChecks(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Stringer interface {
    String(self) -> str
}

type Name struct {
    Value: i32 = 0
}

fn Name::String(self) -> str {
    return 1 as str
}

fn main() -> i32 {
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

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsRuntimeUnionTypeTest(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Token union {
    i32,
    i64,
}

	fn main() -> bool {
	    let value: Token = 1
	    return value is i32
	}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerNarrowsUnionTypeInIfBranch(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Token union {
    i32,
    i64,
}

fn main() -> i32 {
    let value: Token = 1
    if value is i32 {
        let narrowed: i32 = value
        return narrowed
    }
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerNarrowsUnionTypeInWhileBody(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Token union {
    i32,
    i64,
}

fn main() -> i32 {
    let value: Token = 1
    while value is i32 {
        let narrowed: i32 = value
        return narrowed
    }
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerNarrowsUnionTypeInElseBranch(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Token union {
    i32,
    i64,
}

fn main() -> i64 {
    let value: Token = 2 as i64
    if value is i32 {
        return 0 as i64
    } else {
        let narrowed: i64 = value
        return narrowed
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerNarrowsUnionTypeInNegatedIfBranch(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Token union {
    i32,
    i64,
}

fn main() -> i64 {
    let value: Token = 2 as i64
    if !(value is i32) {
        let narrowed: i64 = value
        return narrowed
    }
    return 0 as i64
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerNarrowsUnionTypeInMatchTypeArm(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Token union {
    i32,
    i64,
}

fn main() -> i32 {
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

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAcceptsMatchExpression(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Token union {
    i32,
    i64,
}

fn main() -> i32 {
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

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsRuntimeInterfaceToConcreteTypeTest(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Stringer interface {
    String() -> str
}

type Name struct {}

fn Name::String() -> str {
    return "name"
}

fn main(s: Stringer) -> bool {
    return s is Name
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerNarrowsInterfaceTypeInIfBranch(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Stringer interface {
    String() -> str
}

type Name struct {
    value: i32 = 0
}

fn Name::String() -> str {
    return "name"
}

fn main(s: Stringer) -> i32 {
    if s is Name {
        let narrowed: Name = s
        return narrowed.value
    }
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerNarrowsOptionalBindingInIfBranch(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main(value: ?i32) -> i32 {
    if value != none {
        let narrowed: i32 = value
        return narrowed
    }
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerNarrowsOptionalBindingInElseBranch(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main(value: ?i32) -> i32 {
    if value == none {
        return 0
    } else {
        let narrowed: i32 = value
        return narrowed
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerNarrowsOptionalSelectorChainInIfBranch(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Leaf struct {
    value: i32
}

type D struct {
    e: ?Leaf
}

type C struct {
    d: D
}

type B struct {
    c: C
}

type A struct {
    b: B
}

fn main(a: A) -> i32 {
    if a.b.c.d.e != none {
        return a.b.c.d.e.value
    }
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerNarrowsOptionalIndexPathInIfBranch(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Item struct {
    value: i32
}

fn main(values: [1]?Item) -> i32 {
    if values[0] != none {
        return values[0].value
    }
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerNarrowsUnionSelectorPathInIfBranch(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Token union {
    i32,
    i64,
}

type Holder struct {
    value: Token
}

fn main(h: Holder) -> i32 {
    if h.value is i32 {
        let narrowed: i32 = h.value
        return narrowed
    }
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsExplicitInterfaceDowncast(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Stringer interface {
    String(self) -> str
}

type Name struct {
    value: i32 = 0
}

fn Name::String(self) -> str {
    return "name"
}

fn main(s: Stringer) -> i32 {
    let narrowed: Name = s as Name
    return narrowed.value
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsConcreteTypeThatMissesInterfaceMethod(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Stringer interface {
    String() -> str
}

type Name struct {
    value: i32 = 0
}

fn main() -> i32 {
    let n: Name = .{ .value = 1 }
    let s: Stringer = n
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected interface assignment diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, `missing method String`) {
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Stringer interface {
    String() -> str
}

type Name struct {
    value: i32 = 0
}

fn Name::String(self) -> i32 {
    return self.value
}

fn main() -> i32 {
    let n: Name = .{ .value = 1 }
    let s: Stringer = n
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Reader interface {
    read(&mut self, buf []u8) -> i32
}

type File struct {
    value: i32 = 0
}

fn File::read(&self, buf: []u8) -> i32 {
    return 0
}

fn main() -> i32 {
    let f: File = .{}
    let r: Reader = f
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
import "util/name"

fn name::Name::String(self) -> str {
    return "x"
}
`)
	mustWriteType(t, filepath.Join(root, "util", "name.fer"), `
type Name struct {
    value: i32 = 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn Point::Point(*self, x: i32) {
    self.X = x
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsMethodNamedLikeTypeOnRefReceiver(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn Point::Point(&mut self) {
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsRemovedDestructorSyntax(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn Point::~Point(*self, x: i32) -> i32 {
    return x
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn Point::Point(self) -> i32 {
    return self.X
}

fn main() -> i32 {
    let p: Point = .Point{}
    return p.Point()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsStaticMethodNamedLikeType(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn Point::Point() -> Point {
    return .{}
}

fn main() -> i32 {
    let p = Point::Point()
    return p.X
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsFieldMutationThroughImmutableBinding(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn main() -> void {
    let p: Point = .{}
    p.X = 1
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "pkg.fer"), `
type Box struct {
    hidden: i32 = 1
    Visible: i32 = 2
}
`)
	mustWriteType(t, filepath.Join(root, "main.fer"), `
import "pkg"

fn main() -> i32 {
    let b: pkg::Box = .{}
    return b.hidden
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Box struct {
    hidden: i32 = 1
}

fn main() -> i32 {
    let b: Box = .{}
    return b.hidden
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsPrivateFieldAccessInsideOwnerMethods(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Box struct {
    hidden: i32 = 1
}

fn Box::Read(&self) -> i32 {
    return self.hidden
}

fn Box::Set(&mut self, value: i32) -> void {
    self.hidden = value
}

fn main() -> i32 {
    let mut b: Box = .{}
    b.Set(3)
    return b.Read()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsPrivateFieldInCompositeSameModule(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Box struct {
    hidden: i32 = 1
    Visible: i32 = 2
}

fn main() -> i32 {
    let b: Box = .{ .hidden = 3, .Visible = 4 }
    return b.Visible
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsFieldMutationThroughMutableBinding(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn main() -> void {
    let mut p: Point = .{}
    p.X = 1
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsMutationThroughMutableReference(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn bump(p: &mut Point) -> void {
    (*p).X = 1
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsByValueMutableParameterWithImmutableArgument(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn mutate(mut x: i32) -> i32 {
    x = x + 1
    return x
}

fn main() -> i32 {
    let x: i32 = 1
    return mutate(x)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsMutationThroughImmutableReference(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn bump(p: &Point) -> void {
    (*p).X = 1
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn main() -> void {
    let p: Point = .{}
    let m = &mut p
    m
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn read(p: &Point) -> i32 {
    return p.X
}

fn main() -> i32 {
    let p: Point = .{}
    return read(p)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn bump(p: &mut Point) -> i32 {
    return p.X
}

fn main() -> i32 {
    let p: Point = .{}
    return bump(p)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> bool {
    if true {
        return false
    }
    return true
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	ifStmt := mainFn.Body.Stmts[0].(*ast.IfStmt)
	if !typeinfo.IsBuiltinNamed(result.Entry.Types.Nodes[ifStmt.Cond], "bool") {
		t.Fatalf("expected bool type for true constant, got %#v", result.Entry.Types.Nodes[ifStmt.Cond])
	}
}

func TestTypecheckerRejectsUndefinedSymbol(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let mut x: i32 = undefined
    x = 1
    return x
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected undefined symbol diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUndefinedSymbol && strings.Contains(diag.Message, `undefined symbol "undefined"`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected undefined symbol diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsNumericNarrowingAndLiteralOverflow(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let a: i64 = 1
    let b: i32 = a
    let c: i8 = 1000
    return b
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let huge = 10235543634636243636263462346
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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

func TestTypecheckerRejectsInvalidBinaryLiteralWithoutSplittingToken(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let bad = 0b4234
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected invalid numeric literal diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidNumber && diag.Message == "invalid binary literal 0b4234" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected invalid binary literal diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}

	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	letBad, ok := mainFn.Body.Stmts[0].(*ast.LetStmt)
	if !ok {
		t.Fatalf("expected let stmt, got %T", mainFn.Body.Stmts[0])
	}
	if !typeinfo.IsBuiltinNamed(result.Entry.Types.Nodes[letBad.Value], typeinfo.DefaultIntTypeName) {
		t.Fatalf("expected invalid literal to keep default int type, got %#v", result.Entry.Types.Nodes[letBad.Value])
	}
}

func TestTypecheckerAllowsCatchFallbackValue(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Io error { denied }

fn main(x: Io!i32) -> i32 {
    return x catch -1
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerTreatsRecoverAsBuiltinFunction(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> str {
    return recover()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> string {
    return recover("x")
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "ferret_libs_dev", "std", "math.fer"), `
fn ClampToZero(value: i32) -> i32 {
    return value
}
`)
	mustWriteType(t, filepath.Join(root, "main.fer"), `
import "std/math"

fn main() -> i32 {
    return math::ClampToZeros(-34)
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Io error { denied }

fn log(x: Io) -> void {}

fn main(x: Io!i32) -> i32 {
    let file = x catch |err| {
        log(err)
    }
    return file
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let num1: i32 = 1
    let num2: i8 = num1
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let num1: i32 = 1
    let num2: i8 = num1 as i8
    return num2 as i32
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerResolvesMethodCalls(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
    Y: i32 = 0
}

fn Point::Len2(self) -> i32 {
    return self.X * self.X + self.Y * self.Y
}

fn Point::Len(*self) -> i32 {
    return self.X * self.X + self.Y * self.Y
}

fn main() -> i32 {
    let p: Point = .{ .X = 3, .Y = 4 }
    let q: *Point
    return p.Len2() + q.Len()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsPointerMethodCallOnValue(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn Point::Len(*self) -> i32 {
    return self.X
}

fn main() -> i32 {
    let p: Point = .{}
    return p.Len()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn main() -> i32 {
    let p: Point = .{ .X = 1 }
    return p.Missing()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn main() -> i32 {
    let p: Point = .{ .X = 1 }
    let q = p
    return p.X
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsCopyAsNotYetImplemented(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
    Y: i32 = 0
}

fn main() -> i32 {
    let p: Point = .{ .X = 1, .Y = 2 }
    let q = copy p
    return p.X + q.Y
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Conn struct {}

fn bad(c: *Conn) -> void {
    let d = copy c
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn bad(p: ^i32) -> void {
    let d = copy p
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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

func TestTypecheckerAllowsCTFEConstInitializerFromLocalValue(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let x = 1
    const y = x
    return y
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("expected CTFE-able const initializer to pass, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsCTFEConstInitializerFromMutatedLocalValue(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let mut x = 1
    x = 2
    const y = x
    return y
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected mutable local const initializer diagnostic")
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag != nil && diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, "constant initializer must be compile-time evaluable") {
			return
		}
	}
	t.Fatalf("expected const initializer diagnostic, got %#v", result.Diagnostics.Diagnostics())
}

func TestTypecheckerRejectsNonCTFEConstInitializer(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
#[extern("clock")]
fn clock() -> i32;

fn main() -> i32 {
    const y = clock()
    return y
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected const initializer diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, "compile-time evaluable") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected const-evaluable diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsCTFEConstInitializerFromRuntimeMethodReceiver(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Token struct {}

#[extern("make_token")]
fn make_token() -> Token;

fn Token::Always(&self) -> i32 {
    return 1
}

fn main() -> i32 {
    const y = make_token().Always()
    return y
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected const initializer diagnostic")
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag != nil && diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, "constant initializer must be compile-time evaluable") {
			return
		}
	}
	t.Fatalf("expected const initializer diagnostic, got %#v", result.Diagnostics.Diagnostics())
}

func TestTypecheckerAllowsCTFEConstInitializerFromLocalTupleCall(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn pairSum(pair: (i32, i32)) -> i32 {
    return pair[0] + pair[1]
}

fn main() -> i32 {
    let ints: (i32, i32) = (3, 4)
    const ress = pairSum(ints)
    let arr: [ress]i32
    return arr[0]
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("expected on-demand CTFE const initializer to pass, got %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	letArr := mainFn.Body.Stmts[2].(*ast.LetStmt)
	arrType := result.Entry.Types.Symbols[result.Entry.Bindings.Nodes[letArr.Name].Symbol.ID]
	arr, ok := arrType.(*typeinfo.ArrayType)
	if !ok || arr.Len != 7 || !typeinfo.IsBuiltinNamed(arr.Inner, "i32") {
		t.Fatalf("expected arr type [7]i32, got %T %#v", arrType, arrType)
	}
}

func TestTypecheckerAllowsShortCircuitConstInitializer(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> bool {
    const a = true || (1 / 0 == 0)
    const b = false && (1 / 0 == 0)
    return a && !b
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("expected short-circuit const initializers to pass, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsExplicitTypeArgsInConstCall(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn id<T>(x: T) -> T {
    return x
}

fn main() -> i32 {
    const n = id<i32>(3)
    let arr: [n]i32
    return arr[0]
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("expected explicit generic type args in const call to pass, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsImportedLenCallInConstInitializer(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
import "util"

fn main() -> i32 {
    const n = util::Size()
    let arr: [n]i32
    return arr[0]
}
`)
	mustWriteType(t, filepath.Join(root, "util.fer"), `
fn Size() -> usize {
    return len("abcd")
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("expected imported len const initializer to pass, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsCTFEConstInitializerFromLocalWhileLoopCall(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn sumTo(limit: i32) -> i32 {
    let mut i = 0
    let mut sum = 0
    while i < limit {
        sum = sum + i
        i = i + 1
    }
    return sum
}

fn main() -> i32 {
    let limit = 5
    const total = sumTo(limit)
    let arr: [total]i32
    return arr[0]
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("expected while-loop const initializer to pass, got %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	letArr := mainFn.Body.Stmts[2].(*ast.LetStmt)
	arrType := result.Entry.Types.Symbols[result.Entry.Bindings.Nodes[letArr.Name].Symbol.ID]
	arr, ok := arrType.(*typeinfo.ArrayType)
	if !ok || arr.Len != 10 || !typeinfo.IsBuiltinNamed(arr.Inner, "i32") {
		t.Fatalf("expected arr type [10]i32, got %T %#v", arrType, arrType)
	}
}

func TestTypecheckerComptimePrefixExpressionFoldsInMIR(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let x = 1
    let y = comptime x + 2
    return y
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("expected comptime prefix to fold, got %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR for comptime fold, got %#v", result.Entry)
	}
	text := mir.FormatModule(result.Entry.MIR)
	if !strings.Contains(text, "y = 3") || !strings.Contains(text, "return 3") {
		t.Fatalf("expected folded comptime value in MIR, got %q", text)
	}
}

func TestTypecheckerCachesSemanticConstExprResults(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn pair() -> (i32, i32) {
    return (1, 2)
}

fn main() -> i32 {
    let value = comptime pair()[1]
    return value
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("expected semantic const cache test to pass, got %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	letValue := mainFn.Body.Stmts[0].(*ast.LetStmt)
	prefix, ok := letValue.Value.(*ast.PrefixExpr)
	if !ok {
		t.Fatalf("expected comptime prefix expr, got %T", letValue.Value)
	}
	value, ok := result.Entry.Types.LookupConstValue(prefix)
	if !ok {
		t.Fatal("expected semantic const value cached for comptime expression")
	}
	if got, ok := value.NonNegativeInt64(); !ok || got != 2 {
		t.Fatalf("expected comptime cache value 2, got %#v", value)
	}
	indexExpr, ok := prefix.Right.(*ast.IndexExpr)
	if !ok {
		t.Fatalf("expected index expr inside comptime prefix, got %T", prefix.Right)
	}
	indexValue, ok := result.Entry.Types.LookupConstValue(indexExpr.Index)
	if !ok {
		t.Fatal("expected semantic const value cached for tuple index expression")
	}
	if got, ok := indexValue.NonNegativeInt64(); !ok || got != 1 {
		t.Fatalf("expected tuple index cache value 1, got %#v", indexValue)
	}
}

func TestTypecheckerCachesEnumVariantConstInitializers(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Color enum {
    Red,
    Green,
    Blue,
}

const DefaultColor = Color::Green

fn main() -> Color {
    return DefaultColor
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("expected enum const cache test to pass, got %#v", result.Diagnostics.Diagnostics())
	}
	var decl *ast.ConstDecl
	for _, node := range result.Entry.AST.Decls {
		if constDecl, ok := node.(*ast.ConstDecl); ok && constDecl.Name != nil && constDecl.Name.Text() == "DefaultColor" {
			decl = constDecl
			break
		}
	}
	if decl == nil {
		t.Fatal("expected DefaultColor const declaration")
	}
	value, ok := result.Entry.Types.LookupConstValue(decl)
	if !ok {
		t.Fatal("expected semantic const value cached for enum const initializer")
	}
	if got, ok := value.NonNegativeInt64(); !ok || got != 1 {
		t.Fatalf("expected enum variant cache value 1, got %#v", value)
	}
}

func TestTypecheckerComptimeEvaluatesFunctionCallWithLoop(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn getVal() -> i32 {
    let mut i = 0
    let mut sum = 0
    while i < 5 {
        sum = sum + i
        i = i + 1
    }
    return sum
}

fn main() -> i32 {
    let val = comptime getVal()
    return val
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("expected comptime function call to evaluate, got %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR for comptime fold, got %#v", result.Entry)
	}
	text := mir.FormatModule(result.Entry.MIR)
	if !strings.Contains(text, "val = 10") || !strings.Contains(text, "return 10") {
		t.Fatalf("expected folded comptime value 10 in MIR, got %q", text)
	}
	if strings.Contains(text, "= getVal()") {
		t.Fatalf("expected no runtime getVal call residue after comptime fold, got %q", text)
	}
}

func TestTypecheckerRejectsComptimeOnExternCall(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
#[extern("clock")]
fn clock() -> i64;

fn main() -> i64 {
    let now = comptime clock()
    return now
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected comptime extern-call diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, "`comptime` expression must be compile-time evaluable: call to clock cannot run at compile time") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected comptime extern-call diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerComptimePanicReportsCompileError(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn assertNonZero(x: i32) -> i32 {
    if x == 0 {
        panic "x must not be zero"
    }
    return x
}

fn main() -> i32 {
    let v = comptime assertNonZero(0)
    return v
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected compile-time panic diagnostic")
	}
	foundPanic := false
	foundGeneric := false
	foundPrimaryCallSite := false
	foundSecondaryPanicSite := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidOperation && strings.Contains(diag.Message, "compile-time panic: x must not be zero") {
			foundPanic = true
			if len(diag.Labels) > 0 && strings.Contains(diag.Labels[0].Message, "this comptime evaluation failed") {
				foundPrimaryCallSite = true
			}
			if len(diag.Labels) > 1 && strings.Contains(diag.Labels[1].Message, "panic triggered during compile-time evaluation") {
				foundSecondaryPanicSite = true
			}
		}
		if diag.Code == diagnostics.ErrTypeMismatch && diag.Message == "`comptime` expression must be compile-time evaluable" {
			foundGeneric = true
		}
	}
	if !foundPanic {
		t.Fatalf("expected compile-time panic diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
	if foundGeneric {
		t.Fatalf("expected no generic comptime evaluable diagnostic when panic is reported, got %#v", result.Diagnostics.Diagnostics())
	}
	if !foundPrimaryCallSite {
		t.Fatalf("expected compile-time panic diagnostic to point at comptime call site, got %#v", result.Diagnostics.Diagnostics())
	}
	if !foundSecondaryPanicSite {
		t.Fatalf("expected compile-time panic diagnostic to retain inner panic site as secondary context, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerComptimeAssertPatternWorks(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn assert(cond: bool, msg: str) -> void {
    if !cond {
        panic msg
    }
}

fn main() -> void {
    comptime {
        assert(1 + 1 == 2, "math broke")
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		if result.Entry != nil && result.Entry.MIR != nil {
			t.Log(mir.FormatModule(result.Entry.MIR))
		}
		for _, d := range result.Diagnostics.Diagnostics() {
			t.Logf("diag: %s %q", d.Code, d.Message)
		}
		t.Fatalf("expected comptime assert pattern to typecheck, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerComptimeAssertWrapperWorks(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn assert(cond: bool, msg: str) -> void {
    if !cond {
        panic msg
    }
}

fn static_assert(cond: bool, msg: str) -> void {
    comptime assert(cond, msg)
}

fn main() -> void {
    comptime static_assert(1 + 1 == 2, "math broke")
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		if result.Entry != nil && result.Entry.MIR != nil {
			t.Log(mir.FormatModule(result.Entry.MIR))
		}
		for _, d := range result.Diagnostics.Diagnostics() {
			t.Logf("diag: %s %q", d.Code, d.Message)
		}
		t.Fatalf("expected comptime assert wrapper pattern to typecheck, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerComptimePanicKeepsOriginalCallSiteWithFollowingComptimeExpr(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn assert(cond: bool, msg: str) -> void {
    if !cond {
        panic msg
    }
}

fn static_assert(cond: bool, msg: str) -> void {
    comptime assert(cond, msg)
}

fn main() -> void {
    comptime static_assert(1 == 2, "error")
    comptime assert(1 == 1, "ok")
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected compile-time panic diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code != diagnostics.ErrInvalidOperation || !strings.Contains(diag.Message, "compile-time panic: error") {
			continue
		}
		if len(diag.Labels) == 0 || diag.Labels[0].Location == nil || diag.Labels[0].Location.Start == nil {
			t.Fatalf("expected primary label on compile-time panic diagnostic, got %#v", diag)
		}
		if got := diag.Labels[0].Location.Start.Line; got != 13 {
			t.Fatalf("expected primary label to stay on failing comptime call line 13, got line %d: %#v", got, diag)
		}
		found = true
	}
	if !found {
		t.Fatalf("expected compile-time panic diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerComptimeRequireAllowsPrintSideEffects(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn require(name: str, cond: bool) -> void {
    if !cond {
        print("FAIL:")
        print(name)
        panic "ctfe check failed"
    }
    print("ok:")
    print(name)
}

fn tuplePick() -> i32 {
    let p: (i32, i32) = (1, 2)
    return p[0] + p[1]
}

fn main() -> void {
    let v = comptime tuplePick()
    comptime require("comptime tuple index", v == 3)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("expected comptime require with print side effects to typecheck, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerCompileErrorRequiresComptimeContext(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    compile_error("boom")
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected compile_error context diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidOperation && strings.Contains(diag.Message, "compile_error can only be used in comptime context") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected compile_error context diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerComptimeCompileErrorReportsMessage(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn assert(cond: bool, msg: str) -> void {
    if !cond {
        comptime {
            compile_error(msg)
        }
    }
}

fn main() -> void {
    comptime {
        assert(false, "boom")
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected compile_error diagnostic")
	}
	foundMessage := false
	foundGeneric := false
	foundDeferred := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidOperation && strings.Contains(diag.Message, "compile-time error: boom") {
			foundMessage = true
		}
		if diag.Code == diagnostics.ErrTypeMismatch && diag.Message == "`comptime` expression must be compile-time evaluable" {
			foundGeneric = true
		}
		if diag.Code == diagnostics.ErrInvalidOperation && strings.Contains(diag.Message, "compile_error message must be compile-time evaluable") {
			foundDeferred = true
		}
	}
	if !foundMessage {
		t.Fatalf("expected compile_error message diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
	if foundGeneric {
		t.Fatalf("expected no generic comptime evaluable diagnostic when compile_error is reported, got %#v", result.Diagnostics.Diagnostics())
	}
	if foundDeferred {
		t.Fatalf("expected no deferred-evaluation compile_error message diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerSoftComptimeBlockSkipsRuntimeDependentExpr(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
#[extern("clock")]
fn clock() -> i64;

fn requirePositive(v: i64) -> void {
    if v < 0 {
        panic "negative"
    }
}

fn main() -> void {
    let now = clock()
    comptime {
        requirePositive(now)
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("expected runtime-dependent soft comptime block to skip, got %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR for soft comptime skip, got %#v", result.Entry)
	}
	text := mir.FormatModule(result.Entry.MIR)
	mainIdx := strings.Index(text, "fn main() -> void {")
	if mainIdx < 0 {
		t.Fatalf("expected main function in MIR, got %q", text)
	}
	mainText := text[mainIdx:]
	if strings.Contains(mainText, "comptime ") || strings.Contains(mainText, "requirePositive(") {
		t.Fatalf("expected skipped soft comptime block to leave no runtime residue in main, got %q", mainText)
	}
}

func TestTypecheckerHardComptimeInsideSoftBlockStillErrors(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
#[extern("clock")]
fn clock() -> i64;

fn hardCheck() -> void {
    let _ = comptime clock()
}

fn main() -> void {
    comptime {
        hardCheck()
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected nested hard comptime failure inside soft block")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, "`comptime` expression must be compile-time evaluable: call to clock cannot run at compile time") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected nested hard comptime diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerComptimeEvaluatesArrayIndexAndForLoop(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn edgeSum() -> i32 {
    let arr: [3]i32 = [3]i32{1, 2, 3}
    return arr[0] + arr[2]
}

fn sumWithFor() -> i32 {
    let arr: [4]i32 = [4]i32{1, 2, 3, 4}
    let mut sum = 0
    for arr |value| {
        sum = sum + value
    }
    return sum
}

fn main() -> i32 {
    let a = comptime edgeSum()
    let b = comptime sumWithFor()
    return a + b
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("expected comptime array/index + for evaluation, got %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR for comptime fold, got %#v", result.Entry)
	}
	text := mir.FormatModule(result.Entry.MIR)
	if !strings.Contains(text, "a = 4") || !strings.Contains(text, "b = 10") {
		t.Fatalf("expected folded comptime values in MIR, got %q", text)
	}
	if strings.Contains(text, "= edgeSum()") || strings.Contains(text, "= sumWithFor()") {
		t.Fatalf("expected no runtime call residues after comptime fold, got %q", text)
	}
}

func TestTypecheckerComptimeEvaluatesTupleAggregateAndIndex(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn tuplePick() -> i32 {
    let p: (i32, i32) = (1, 2)
    return p[0] + p[1]
}

fn main() -> i32 {
    let v = comptime tuplePick()
    return v
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("expected comptime tuple aggregate/index evaluation, got %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR for comptime fold, got %#v", result.Entry)
	}
	text := mir.FormatModule(result.Entry.MIR)
	if !strings.Contains(text, "v = 3") || !strings.Contains(text, "return 3") {
		t.Fatalf("expected folded comptime tuple value in MIR, got %q", text)
	}
	if strings.Contains(text, "= tuplePick()") {
		t.Fatalf("expected no runtime tuplePick call residue after comptime fold, got %q", text)
	}
}

func TestTypecheckerSupportsMixedTupleElementTypes(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let p: (i32, bool, str) = (7, true, "ok")
    if !p[1] {
        return 0
    }
    if p[2] != "ok" {
        return 0
    }
    return p[0]
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("expected mixed tuple elements to typecheck, got %#v", result.Diagnostics.Diagnostics())
	}

	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	letP := mainFn.Body.Stmts[0].(*ast.LetStmt)
	tupleType, ok := result.Entry.Types.Nodes[letP.Type].(*typeinfo.TupleType)
	if !ok {
		t.Fatalf("expected tuple type annotation, got %T", result.Entry.Types.Nodes[letP.Type])
	}
	if len(tupleType.Elems) != 3 {
		t.Fatalf("expected 3 tuple elements, got %#v", tupleType.Elems)
	}
	if !typeinfo.IsBuiltinNamed(tupleType.Elems[0], "i32") {
		t.Fatalf("expected first tuple element i32, got %#v", tupleType.Elems[0])
	}
	if !typeinfo.IsBuiltinNamed(tupleType.Elems[1], "bool") {
		t.Fatalf("expected second tuple element bool, got %#v", tupleType.Elems[1])
	}
	if _, ok := tupleType.Elems[2].(*typeinfo.StringType); !ok {
		t.Fatalf("expected third tuple element str, got %#v", tupleType.Elems[2])
	}
}

func TestTypecheckerComptimeEvaluatesMixedTupleAggregateAndIndex(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn tupleCheck() -> bool {
    let p: (i32, bool, str) = (7, true, "ok")
    return p[0] == 7 && p[1] && p[2] == "ok"
}

fn main() -> bool {
    let v = comptime tupleCheck()
    return v
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("expected comptime mixed tuple aggregate/index evaluation, got %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR for comptime fold, got %#v", result.Entry)
	}
	text := mir.FormatModule(result.Entry.MIR)
	if !strings.Contains(text, "v = true") || !strings.Contains(text, "return true") {
		t.Fatalf("expected folded comptime mixed tuple value in MIR, got %q", text)
	}
	if strings.Contains(text, "= tupleCheck()") {
		t.Fatalf("expected no runtime tupleCheck call residue after comptime fold, got %q", text)
	}
}

func TestTypecheckerRejectsTupleIndexWithRuntimeValue(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main(i: usize) -> i32 {
    let p: (i32, bool) = (7, true)
    return p[i]
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected tuple runtime-index diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidOperation && strings.Contains(diag.Message, "tuple index must be a non-negative compile-time integer") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected tuple runtime-index diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsTupleIndexOutOfBounds(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let p: (i32, bool) = (7, true)
    return p[2]
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected tuple index out of bounds diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidOperation && strings.Contains(diag.Message, "tuple index out of bounds") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected tuple index out of bounds diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsTooManyTupleLiteralElements(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let p: (i32, bool) = (7, true, 9)
    if p[1] {
        return p[0]
    }
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected extra tuple element diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrExtraField && strings.Contains(diag.Message, "too many elements in tuple literal") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected extra tuple element diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsMissingTupleLiteralElements(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let p: (i32, bool, str) = (7, true)
    if p[1] {
        return p[0]
    }
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected missing tuple element diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrMissingField && strings.Contains(diag.Message, "missing required tuple element(s) in composite literal") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected missing tuple element diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerComptimeEvaluatesMethodCallOnStruct(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Counter struct {
    Value: i32
}

fn Counter::Inc(&mut self, by: i32) -> i32 {
    self.Value = self.Value + by
    return self.Value
}

fn build() -> i32 {
    let mut c: Counter = .{ .Value = 1 }
    let n = c.Inc(2)
    return c.Value + n
}

fn main() -> i32 {
    let v = comptime build()
    return v
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("expected comptime method call evaluation, got %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR for comptime fold, got %#v", result.Entry)
	}
	text := mir.FormatModule(result.Entry.MIR)
	if !strings.Contains(text, "v = 6") || !strings.Contains(text, "return 6") {
		t.Fatalf("expected folded comptime value 6 in MIR, got %q", text)
	}
}

func TestTypecheckerComptimeEvaluatesCrossModuleCall(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "counter.fer"), `
type Counter struct {
    Value: i32
}

fn Counter::Inc(&mut self, by: i32) -> i32 {
    self.Value = self.Value + by
    return self.Value
}

fn MakeAndInc() -> i32 {
    let mut c: Counter = .{ .Value = 3 }
    return c.Inc(4)
}
`)
	mustWriteType(t, filepath.Join(root, "main.fer"), `
import "counter"

fn main() -> i32 {
    let v = comptime counter::MakeAndInc()
    return v
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("expected cross-module comptime evaluation, got %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR for comptime fold, got %#v", result.Entry)
	}
	text := mir.FormatModule(result.Entry.MIR)
	if !strings.Contains(text, "v = 7") || !strings.Contains(text, "return 7") {
		t.Fatalf("expected folded comptime value 7 in MIR, got %q", text)
	}
}

func TestTypecheckerRejectsInvalidExplicitCast(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let x = "hi" as i32
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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

func TestTypecheckerRejectsRawToOwningPointerCast(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    unsafe {
        let rp = 0 as ^void
        let own = rp as *i32
        own
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected invalid raw-to-owning cast diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidCast && strings.Contains(diag.Message, "cannot cast ^void to *i32") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected raw-to-owning cast diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerSuggestsOwnershipBoundaryAPIsForRawOwnerCasts(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    unsafe {
        let rp = 0 as ^i32
        let own = rp as *i32
        own
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected invalid raw-to-owning cast diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code != diagnostics.ErrInvalidCast {
			continue
		}
		for _, label := range diag.Labels {
			if strings.Contains(label.Message, "std/mem::Adopt") && strings.Contains(label.Message, "std/mem::Expose") {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatalf("expected ownership-boundary API guidance in cast diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsOwningPointerToRawCast(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn use_ptr(p: *i32) -> void {
    unsafe {
        let raw = p as ^i32
        raw
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected invalid owning-to-raw cast diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidCast && strings.Contains(diag.Message, "cannot cast *i32 to ^i32") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected owning-to-raw cast diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAcceptsAdoptExposeCalls(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "mem.fer"), `
#[extern]
fn Expose<T>(owner: *T) -> ^T;

#[extern]
fn ExposeRef<T>(owner: &*T) -> ^T;

#[extern]
fn Adopt<T>(raw: ^T) -> *T;
`)
	mustWriteType(t, filepath.Join(root, "main.fer"), `
import "mem"

fn main() -> void {
    unsafe {
        let raw = 0 as ^i32
        let own = mem::Adopt(raw)
        let back1 = mem::Expose(own)
        let own2 = mem::Adopt(back1)
        let back2 = mem::ExposeRef(&own2)
        back2
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAcceptsRawSliceCasts(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    let mut arr: [4]u8 = .{1, 2, 3, 4}
    unsafe {
        let raw = 0 as ^u8
        let readonly = (raw, 4 as usize) as []u8
        let mut writable: []u8 = (raw, 4 as usize) as []u8
        let ptr1 = readonly as ^const u8
        let ptr2 = arr as ^const u8
        let ptr3 = arr as ^u8
        let ptr4 = writable as ^u8
        ptr1
        ptr2
        ptr3
        ptr4
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsMutationThroughMutableRawPartsSliceBinding(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    unsafe {
        let raw = 0 as ^u8
        let mut bytes: []u8 = (raw, 4 as usize) as []u8
        bytes[0] = 1
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	unsafeStmt, ok := mainFn.Body.Stmts[0].(*ast.UnsafeStmt)
	if !ok {
		t.Fatalf("expected unsafe stmt, got %#v", mainFn.Body.Stmts[0])
	}
	block := unsafeStmt.Body
	letBytes := block.Stmts[1].(*ast.LetStmt)
	bytesRes := result.Entry.Bindings.Nodes[letBytes.Name]
	bytesType := result.Entry.Types.Symbols[bytesRes.Symbol.ID]
	sl, ok := bytesType.(*typeinfo.SliceType)
	if !ok || !sl.Mutable || !typeinfo.IsBuiltinNamed(sl.Inner, "u8") {
		t.Fatalf("expected mutable-capable []u8 binding, got %T %#v", bytesType, bytesType)
	}
}

func TestTypecheckerBindsLocalSymbolTypes(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type MyErr error {
    Oops
}

fn run(items: [3]i32, input: MyErr!i32) -> i32 {
    let r = input
    let x = 1
    return r catch |e| { return x }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn readPoint() -> i32 {
    let p: Point = .{}
    let r = &p
    let x = *r
    return x.X
}

fn writePoint() -> void {
    let mut p: Point = .{}
    let m = &mut p
    m
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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

func TestTypecheckerUsesRawPointerTypesForRawCoercion(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn main() -> void {
    let mut p: Point = .{}
    unsafe {
        let r: ^const Point = &p
        let m: ^Point = &mut p
        let x = *m
        r
        x
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	if !ok || !rok || rNamed.Name != "Point" || !rType.Const {
		t.Fatalf("expected const RawPtrType for &p coercion, got %#v", result.Entry.Types.Nodes[letR.Value])
	}
	mType, ok := result.Entry.Types.Nodes[letM.Value].(*typeinfo.RawPtrType)
	mNamed, mok := mType.Inner.(*typeinfo.NamedType)
	if !ok || !mok || mNamed.Name != "Point" || mType.Const {
		t.Fatalf("expected mutable RawPtrType for &mut p coercion, got %#v", result.Entry.Types.Nodes[letM.Value])
	}
	if _, ok := result.Entry.Types.Nodes[letX.Value].(*typeinfo.NamedType); !ok {
		t.Fatalf("expected dereference of raw pointer to produce named value type, got %#v", result.Entry.Types.Nodes[letX.Value])
	}
}

func TestTypecheckerRejectsRawCoercionOutsideUnsafe(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn main() -> void {
    let p: Point = .{}
    let r: ^const Point = &p
    r
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected raw-coercion unsafe diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidOperation && strings.Contains(diag.Message, "raw address operator requires unsafe block") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected raw-coercion unsafe diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerCoercesRefToConstRawWhenExpected(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn takeConstRaw(rp: ^const Point) -> void {
    unsafe {
        rp.X
    }
}

fn main() -> void {
    let p: Point = .{}
    unsafe {
        takeConstRaw(&p)
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerCoercesMutRefToRawWhenExpected(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn readRaw(rp: ^Point) -> i32 {
    unsafe {
        return rp.X
    }
}

fn main() -> void {
    let mut p: Point = .{}
    unsafe {
        readRaw(&mut p)
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsImmutableRefToMutableRaw(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn readRaw(rp: ^Point) -> i32 {
    unsafe {
        return rp.X
    }
}

fn main() -> void {
    let p: Point = .{}
    unsafe {
        readRaw(&p)
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected type mismatch for immutable ref to mutable raw pointer")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, "expected ^Point, got ^const Point") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected immutable-to-mutable raw mismatch diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsRefToRawOutsideUnsafeWhenExpected(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn takeConstRaw(rp: ^const Point) -> void {
    rp
}

fn main() -> void {
    let p: Point = .{}
    takeConstRaw(&p)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected unsafe diagnostic for implicit raw pointer creation")
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

func TestTypecheckerRequiresUnsafeForUnsafeFunctionCall(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
unsafe fn dangerous() -> void {}

fn main() -> void {
    dangerous()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected unsafe call diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidOperation && strings.Contains(diag.Message, "unsafe function call requires unsafe block") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unsafe call diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsUnsafePrefixExpressionForUnsafeCall(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
unsafe fn dangerous() -> i32 {
    return 1
}

fn main() -> i32 {
    return unsafe dangerous()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRequiresUnsafeForUnsafeMethodCall(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {}

unsafe fn Point::Run(&self) -> void {}

fn main() -> void {
    let p: Point = .{}
    p.Run()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected unsafe method call diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidOperation && strings.Contains(diag.Message, "unsafe function call requires unsafe block") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unsafe method call diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsRawFieldAccessOutsideUnsafe(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn read(rp: ^Point) -> i32 {
    return rp.X
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected raw-field unsafe diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidOperation && strings.Contains(diag.Message, "raw pointer field access requires unsafe block") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected raw-field unsafe diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsAssignThroughConstRawPointer(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn write(mut rp: ^const Point) -> void {
    unsafe {
        rp.X = 1
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected immutable-access assignment diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrConstantReassignment && strings.Contains(diag.Message, "cannot assign through immutable access path") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected immutable-access assignment diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsAssignThroughMutableRawPointerInUnsafe(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn write(mut rp: ^Point) -> void {
    unsafe {
        rp.X = 1
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerTypesArrayIndexing(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main(items: [3]i32) -> i32 {
    let v = items[1]
    return v
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let items: [_]i32 = [_]i32{1, 2, 3}
    let v = items[1]
    return v
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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

func TestTypecheckerResolvesArrayLengthFromConstExpr(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
const BASE = 2
const EXTRA = 1

fn main(items: [BASE + EXTRA]i32) -> i32 {
    return items[2]
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	paramRes := result.Entry.Bindings.Nodes[mainFn.Params[0].Name]
	paramType := result.Entry.Types.Symbols[paramRes.Symbol.ID]
	arr, ok := paramType.(*typeinfo.ArrayType)
	if !ok || arr.Len != 3 || !typeinfo.IsBuiltinNamed(arr.Inner, "i32") {
		t.Fatalf("expected items type [3]i32, got %T %#v", paramType, paramType)
	}
}

func TestTypecheckerResolvesArrayLengthFromCTFEFunctionCall(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn count() -> i32 {
    let mut i = 0
    let mut sum = 0
    while i < 4 {
        sum = sum + 1
        i = i + 1
    }
    return sum
}

fn main(items: [count()]i32) -> i32 {
    return items[3]
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	paramRes := result.Entry.Bindings.Nodes[mainFn.Params[0].Name]
	paramType := result.Entry.Types.Symbols[paramRes.Symbol.ID]
	arr, ok := paramType.(*typeinfo.ArrayType)
	if !ok || arr.Len != 4 || !typeinfo.IsBuiltinNamed(arr.Inner, "i32") {
		t.Fatalf("expected items type [4]i32, got %T %#v", paramType, paramType)
	}
}

func TestTypecheckerResolvesArrayLengthFromEarlierComptimeLet(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn count() -> i32 {
    let mut i = 0
    let mut sum = 0
    while i < 5 {
        sum = sum + 1
        i = i + 1
    }
    return sum
}

fn main() -> i32 {
    let size = comptime count()
    let items: [size]i32 = [size]i32{1, 2, 3, 4, 5}
    return items[4]
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	letItems := mainFn.Body.Stmts[1].(*ast.LetStmt)
	itemsRes := result.Entry.Bindings.Nodes[letItems.Name]
	itemsType := result.Entry.Types.Symbols[itemsRes.Symbol.ID]
	arr, ok := itemsType.(*typeinfo.ArrayType)
	if !ok || arr.Len != 5 || !typeinfo.IsBuiltinNamed(arr.Inner, "i32") {
		t.Fatalf("expected items type [5]i32, got %T %#v", itemsType, itemsType)
	}
}

func TestTypecheckerResolvesArrayLengthFromImportedConst(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "sizes.fer"), `
const COUNT = 3
`)
	mustWriteType(t, filepath.Join(root, "main.fer"), `
import "sizes"

fn main(items: [sizes::COUNT]i32) -> i32 {
    return items[2]
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	paramRes := result.Entry.Bindings.Nodes[mainFn.Params[0].Name]
	paramType := result.Entry.Types.Symbols[paramRes.Symbol.ID]
	arr, ok := paramType.(*typeinfo.ArrayType)
	if !ok || arr.Len != 3 || !typeinfo.IsBuiltinNamed(arr.Inner, "i32") {
		t.Fatalf("expected items type [3]i32, got %T %#v", paramType, paramType)
	}
}

func TestTypecheckerRejectsRuntimeArrayLength(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main(n: i32) -> i32 {
    let items: [n]i32
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected compile-time array length diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag == nil || diag.Code != diagnostics.ErrTypeMismatch {
			continue
		}
		if strings.Contains(diag.Message, "array length must be a non-negative compile-time integer") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected compile-time array length diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsNegativeArrayLength(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let items: [-1]i32
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected negative array length diagnostic")
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag != nil && len(diag.Labels) > 0 && diag.Labels[0].Message == "array length must be non-negative" {
			return
		}
	}
	t.Fatalf("expected non-negative array length diagnostic, got %#v", result.Diagnostics.Diagnostics())
}

func TestTypecheckerTypesSliceLiterals(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let items: []i32 = []i32{1, 2, 3}
    return items[0]
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	letItems := mainFn.Body.Stmts[0].(*ast.LetStmt)
	itemsRes := result.Entry.Bindings.Nodes[letItems.Name]
	itemsType := result.Entry.Types.Symbols[itemsRes.Symbol.ID]
	sl, ok := itemsType.(*typeinfo.SliceType)
	if !ok || !typeinfo.IsBuiltinNamed(sl.Inner, "i32") {
		t.Fatalf("expected items type []i32, got %T %#v", itemsType, itemsType)
	}
}

func TestTypecheckerTypesEmptySliceLiteral(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> usize {
    let items: []i32 = []i32{}
    return len(items)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	letItems := mainFn.Body.Stmts[0].(*ast.LetStmt)
	itemsRes := result.Entry.Bindings.Nodes[letItems.Name]
	itemsType := result.Entry.Types.Symbols[itemsRes.Symbol.ID]
	sl, ok := itemsType.(*typeinfo.SliceType)
	if !ok || !typeinfo.IsBuiltinNamed(sl.Inner, "i32") {
		t.Fatalf("expected items type []i32, got %T %#v", itemsType, itemsType)
	}
}

func TestTypecheckerAllowsForOverSlice(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn sum(items: []i32) -> i32 {
    let mut total = 0
    for items |v| {
        total += v
    }
    return total
}

fn main() -> i32 {
    let items: []i32 = []i32{1, 2, 3}
    return sum(items)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsForOverIntegerRange(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn sum() -> i32 {
    let mut total = 0
    for 0..=10:2 |i, v| {
        total += i as i32
        total += v
    }
    return total
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsForOverIntegerRangeWithBindingsAndConsts(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn sum() -> i32 {
    const start: i32 = 2
    const end: i32 = 10
    let step: i32 = 2
    let mut total = 0
    for start..end:step |v| {
        total += v
    }
    return total
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsFloatRange(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn bad() -> i32 {
    for 0.0..1.0 |v| {
        return v as i32
    }
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected float range diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if strings.Contains(diag.Message, "range endpoints must be integers") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected integer-range diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsMatchRangePattern(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn classify(x: i32) -> i32 {
    return match x {
        0..=10 => 1
        _ => 0
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsVariadicCalls(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn sum(nums: ...i32) -> i32 {
    let mut total = 0
    for nums |v| {
        total += v
    }
    return total
}

fn main() -> i32 {
    return sum(1, 2, 3, 4)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsOmittedDefaultArgs(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn add(base: i32, extra: i32 = 2) -> i32 {
    return base + extra
}

fn main() -> i32 {
    return add(5)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsImportedDefaultArgs(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "util", "math.fer"), `
const DefaultStep: i32 = 3

fn Add(base: i32, step: i32 = DefaultStep) -> i32 {
    return base + step
}
`)
	mustWriteType(t, filepath.Join(root, "main.fer"), `
import "util/math"

fn main() -> i32 {
    return math::Add(4)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsRuntimeCallDefaultParamValue(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn bump(x: i32) -> i32 {
    return x + 1
}

fn add(base: i32, extra: i32 = bump(2)) -> i32 {
    return base + extra
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsDefaultParamReferencingEarlierParams(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn add(base: i32, extra: i32 = base, bias: i32 = extra + 1) -> i32 {
    return base + extra + bias
}

fn main() -> i32 {
    return add(2)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsSpreadIntoVariadicCall(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn sum(nums: ...i32) -> i32 {
    let mut total = 0
    for nums |v| {
        total += v
    }
    return total
}

fn main() -> i32 {
    let items: []i32 = []i32{1, 2, 3}
    return sum(items...)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsSpreadOnNonVariadicCall(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn sum(items: []i32) -> i32 {
    return items[0]
}

fn main() -> i32 {
    let items: []i32 = []i32{1, 2, 3}
    return sum(items...)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected spread-on-non-variadic diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidOperation && strings.Contains(diag.Message, "spread argument requires a variadic parameter") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected spread-on-non-variadic diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerTypesMutableSliceBindingFromLiteral(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let mut items: []i32 = []i32{1, 2, 3}
    items[0] = 9
    return items[0]
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	letItems := mainFn.Body.Stmts[0].(*ast.LetStmt)
	itemsRes := result.Entry.Bindings.Nodes[letItems.Name]
	itemsType := result.Entry.Types.Symbols[itemsRes.Symbol.ID]
	sl, ok := itemsType.(*typeinfo.SliceType)
	if !ok || !sl.Mutable || !typeinfo.IsBuiltinNamed(sl.Inner, "i32") {
		t.Fatalf("expected mutable-capable []i32 binding, got %T %#v", itemsType, itemsType)
	}
}

func TestTypecheckerAllowsMutableSliceBindingToReadonlyParam(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn sum(items: []i32) -> i32 {
    return items[0]
}

fn main() -> i32 {
    let mut items: []i32 = []i32{1, 2, 3}
    return sum(items)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsReadonlySliceBindingToMutableParam(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn fill(mut items: []i32) -> void {
    items[0] = 1
}

fn main() -> void {
    let items: []i32 = []i32{1, 2, 3}
    fill(items)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected mutable slice mismatch diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, "slice value is not writable in this context") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected mutable slice mismatch diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsMutationThroughReadonlySlice(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    let items: []i32 = []i32{1, 2, 3}
    items[0] = 9
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected readonly slice mutation diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrConstantReassignment && strings.Contains(diag.Message, "cannot assign through immutable access path") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected readonly slice mutation diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsArrayIndexOutOfBounds(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let items: [3]i32 = [3]i32{1, 2, 3}
    return items[3]
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected array index out of bounds diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidOperation && strings.Contains(diag.Message, "array index out of bounds") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected array index out of bounds diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsReadonlySliceViewFromArray(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn head(items: []i32) -> i32 {
    return items[0]
}

fn main() -> i32 {
    let items: [3]i32 = [3]i32{1, 2, 3}
    return head(items)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsMutableSliceViewFromMutableArray(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn bump(mut items: []i32) -> i32 {
    items[0] = 9
    return items[0]
}

fn main() -> i32 {
    let mut items: [3]i32 = [3]i32{1, 2, 3}
    return bump(items)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsMutableSliceViewFromImmutableArray(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn bump(mut items: []i32) -> i32 {
    items[0] = 9
    return items[0]
}

fn main() -> i32 {
    let items: [3]i32 = [3]i32{1, 2, 3}
    return bump(items)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected immutable array to mutable slice diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, "type mismatch: expected []i32, got [3]i32") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected immutable array to mutable slice diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerTypesLenForArrayAndSlice(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn lenArray() -> usize {
    let items: [_]i32 = [_]i32{1, 2, 3}
    return len(items)
}

fn lenSlice(items: []i32) -> usize {
    return len(items)
}

fn lenString(s: str) -> usize {
    return len(s)
}

fn lenArrayRef(items: &[3]i32) -> usize {
    return len(items)
}

fn lenSliceRef(items: &[]i32) -> usize {
    return len(items)
}

fn lenStringRef(s: &str) -> usize {
    return len(s)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsLenOnNonArraySlice(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> usize {
    let x: i32 = 1
    return len(x)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected len type diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidType && strings.Contains(diag.Message, "len expects an array, slice, or str argument") {
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    let mut x = 10
    let y = &mut x
    print(y)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsDiscardAssignment(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let a = 10
    _ = a
    return 0
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerResolvesExplicitReferenceReceivers(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn Point::Read(&self) -> i32 {
    return self.X
}

fn Point::Bump(&mut self) -> i32 {
    return self.X + 1
}

fn main() -> i32 {
    let mut p: Point = .{ .X = 1 }
    let a = (&p).Read()
    let b = (&mut p).Bump()
    return a + b
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerResolvesAttachedReferenceReceiversFromValueCalls(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
}

fn Point::Read(&self) -> i32 {
    return self.X
}

fn Point::Bump(&mut self) -> i32 {
    self.X++
    return self.X
}

fn main() -> i32 {
    let mut p: Point = .{ .X = 1 }
    return p.Read() + p.Bump()
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsOwningPointerToReferenceContainingType(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.fer"), `
type Inner struct {
    Ref: &i32
}

type Outer struct {
    Child: *Inner
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
	mustWriteType(t, filepath.Join(root, "main.fer"), `
let GlobalRef: &i32 = 0
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
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
