package hir_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compiler/internal/core/diagnostics"
	"compiler/internal/core/phase"
	compiler "compiler/internal/driver"
	"compiler/internal/ir/hir"
)

func TestPipelineGeneratesHIR(t *testing.T) {
	root := t.TempDir()
	mustWriteHIR(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X: i32 = 0
}

let mut GlobalPoint: Point = .{ .X = 1 }

fn main() i32 {
    let p = GlobalPoint
    return p.X
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		msgs := make([]string, 0, len(result.Diagnostics.Diagnostics()))
		for _, diag := range result.Diagnostics.Diagnostics() {
			if diag == nil {
				continue
			}
			msgs = append(msgs, diag.Code+": "+diag.Message)
		}
		t.Fatalf("unexpected diagnostics: %v", msgs)
	}
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	if result.Entry.HIR == nil {
		t.Fatal("expected HIR module")
	}
	if result.Entry.LoweredHIR == nil {
		t.Fatal("expected lowered HIR module")
	}
	if result.Entry.CFG == nil {
		t.Fatal("expected CFG module")
	}
	if len(result.Entry.HIR.Types) != 1 {
		t.Fatalf("expected one lowered type decl, got %#v", result.Entry.HIR.Types)
	}
	if len(result.Entry.HIR.Globals) != 1 {
		t.Fatalf("expected one lowered global, got %#v", result.Entry.HIR.Globals)
	}
	if len(result.Entry.HIR.Functions) != 1 {
		t.Fatalf("expected one lowered function, got %#v", result.Entry.HIR.Functions)
	}
	fn := result.Entry.HIR.Functions[0]
	if fn.Name != "main" {
		t.Fatalf("expected main function, got %#v", fn.Name)
	}
	ret, ok := fn.Body.Stmts[1].(*hir.ReturnStmt)
	if !ok {
		t.Fatalf("expected lowered return stmt, got %T", fn.Body.Stmts[1])
	}
	if ret.Value == nil || ret.Value.Type() == nil || ret.Value.Type().String() != "i32" {
		t.Fatalf("expected typed lowered return value, got %#v", ret.Value)
	}
	text := hir.FormatModule(result.Entry.HIR)
	if text == "" || !strings.Contains(text, "type Point struct") {
		t.Fatalf("expected type declaration in hir dump, got %q", text)
	}
	if !strings.Contains(text, "X: i32 = 0") {
		t.Fatalf("expected field default in hir dump, got %q", text)
	}
}

func TestPipelineSpecializesGenericTopLevelFunctions(t *testing.T) {
	root := t.TempDir()
	mustWriteHIR(t, filepath.Join(root, "main.ferr"), `
fn add<T>(a: T, b: T) T {
    return a + b
}

fn main() i32 {
    return add(1, 2)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		msgs := make([]string, 0, len(result.Diagnostics.Diagnostics()))
		for _, diag := range result.Diagnostics.Diagnostics() {
			if diag == nil {
				continue
			}
			msgs = append(msgs, diag.Code+": "+diag.Message)
		}
		t.Fatalf("unexpected diagnostics: %v", msgs)
	}
	if result.Entry == nil || result.Entry.LoweredHIR == nil {
		t.Fatal("expected lowered HIR module")
	}

	var mainFn *hir.Func
	var specialized *hir.Func
	for _, fn := range result.Entry.LoweredHIR.Functions {
		if fn == nil {
			continue
		}
		switch {
		case fn.Name == "main":
			mainFn = fn
		case strings.HasPrefix(fn.Name, "add$"):
			specialized = fn
		case fn.Name == "add":
			t.Fatalf("expected generic template function to be removed from lowered HIR, got %#v", fn.Name)
		}
	}
	if mainFn == nil || specialized == nil {
		t.Fatalf("expected main and specialized add functions, got %#v", result.Entry.LoweredHIR.Functions)
	}
	ret, ok := mainFn.Body.Stmts[0].(*hir.ReturnStmt)
	if !ok {
		t.Fatalf("expected lowered return stmt, got %T", mainFn.Body.Stmts[0])
	}
	call, ok := ret.Value.(*hir.CallExpr)
	if !ok {
		t.Fatalf("expected call expr, got %T", ret.Value)
	}
	callee, ok := call.Callee.(*hir.Ident)
	if !ok {
		t.Fatalf("expected specialized callee ident, got %T", call.Callee)
	}
	if len(callee.Path) != 1 || callee.Path[0] != specialized.Name {
		t.Fatalf("expected call rewritten to specialized function %q, got %#v", specialized.Name, callee.Path)
	}
}

func TestPipelineSpecializesGenericMethodsAndTypes(t *testing.T) {
	root := t.TempDir()
	mustWriteHIR(t, filepath.Join(root, "main.ferr"), `
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

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		msgs := make([]string, 0, len(result.Diagnostics.Diagnostics()))
		for _, diag := range result.Diagnostics.Diagnostics() {
			if diag == nil {
				continue
			}
			msgs = append(msgs, diag.Code+": "+diag.Message)
		}
		t.Fatalf("unexpected diagnostics: %v", msgs)
	}
	if result.Entry == nil || result.Entry.LoweredHIR == nil {
		t.Fatal("expected lowered HIR module")
	}

	var specializedType *hir.TypeDecl
	for _, decl := range result.Entry.LoweredHIR.Types {
		if decl == nil {
			continue
		}
		if decl.Name == "Box" {
			t.Fatalf("expected generic type template to be removed from lowered HIR, got %#v", decl.Name)
		}
		if strings.HasPrefix(decl.Name, "Box$") {
			specializedType = decl
		}
	}
	if specializedType == nil || specializedType.Struct == nil || len(specializedType.Struct.Fields) != 1 {
		t.Fatalf("expected specialized Box type decl, got %#v", result.Entry.LoweredHIR.Types)
	}
	if specializedType.Struct.Fields[0] == nil || specializedType.Struct.Fields[0].Type == nil || specializedType.Struct.Fields[0].Type.String() != "i32" {
		t.Fatalf("expected specialized Box field type i32, got %#v", specializedType.Struct.Fields[0])
	}

	var mainFn *hir.Func
	var specializedMethod *hir.Func
	for _, fn := range result.Entry.LoweredHIR.Functions {
		if fn == nil {
			continue
		}
		switch {
		case fn.Name == "main":
			mainFn = fn
		case strings.HasPrefix(fn.Name, "Get$"):
			specializedMethod = fn
		case fn.Name == "Get" && fn.Receiver != nil:
			t.Fatalf("expected generic method template to be removed from lowered HIR, got %#v", fn.Name)
		}
	}
	if mainFn == nil || specializedMethod == nil {
		t.Fatalf("expected main and specialized method functions, got %#v", result.Entry.LoweredHIR.Functions)
	}
	ret, ok := mainFn.Body.Stmts[1].(*hir.ReturnStmt)
	if !ok {
		t.Fatalf("expected lowered return stmt, got %T", mainFn.Body.Stmts[1])
	}
	call, ok := ret.Value.(*hir.CallExpr)
	if !ok {
		t.Fatalf("expected method call expr, got %T", ret.Value)
	}
	callee, ok := call.Callee.(*hir.SelectorExpr)
	if !ok {
		t.Fatalf("expected selector callee for method call, got %T", call.Callee)
	}
	if callee.Name != specializedMethod.Name {
		t.Fatalf("expected selector rewritten to specialized method %q, got %q", specializedMethod.Name, callee.Name)
	}
}

func TestPipelineSpecializesGenericStaticOwnerMethodCallPath(t *testing.T) {
	root := t.TempDir()
	mustWriteHIR(t, filepath.Join(root, "main.ferr"), `
type Circle<T> struct {
    Rad: T
}

fn Circle<T>::New(v: T) Self {
    return .{ .Rad = v }
}

fn main() void {
    let _ = Circle<i32>::New(1)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		msgs := make([]string, 0, len(result.Diagnostics.Diagnostics()))
		for _, diag := range result.Diagnostics.Diagnostics() {
			if diag == nil {
				continue
			}
			msgs = append(msgs, diag.Code+": "+diag.Message)
		}
		t.Fatalf("unexpected diagnostics: %v", msgs)
	}
	if result.Entry == nil || result.Entry.LoweredHIR == nil {
		t.Fatal("expected lowered HIR module")
	}

	var mainFn *hir.Func
	var specialized *hir.Func
	for _, fn := range result.Entry.LoweredHIR.Functions {
		if fn == nil {
			continue
		}
		switch {
		case fn.Name == "main":
			mainFn = fn
		case strings.HasPrefix(fn.Name, "New$"):
			specialized = fn
		}
	}
	if mainFn == nil || specialized == nil {
		t.Fatalf("expected main and specialized New function, got %#v", result.Entry.LoweredHIR.Functions)
	}

	letStmt, ok := mainFn.Body.Stmts[0].(*hir.LetStmt)
	if !ok {
		t.Fatalf("expected let stmt, got %T", mainFn.Body.Stmts[0])
	}
	call, ok := letStmt.Value.(*hir.CallExpr)
	if !ok {
		t.Fatalf("expected call expr, got %T", letStmt.Value)
	}
	callee, ok := call.Callee.(*hir.Ident)
	if !ok {
		t.Fatalf("expected ident callee, got %T", call.Callee)
	}
	if len(callee.Path) != 2 || callee.Path[0] != "Circle" || callee.Path[1] != specialized.Name {
		t.Fatalf("expected specialized static owner path Circle::%s, got %#v", specialized.Name, callee.Path)
	}
}

func TestPipelineSpecializesInferredGenericStaticOwnerMethodCallPath(t *testing.T) {
	root := t.TempDir()
	mustWriteHIR(t, filepath.Join(root, "main.ferr"), `
type Circle<T> struct {
    Rad: T
}

fn Circle<T>::New(v: T) Self {
    return .{ .Rad = v }
}

fn main() void {
    let _ = Circle::New(1)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		msgs := make([]string, 0, len(result.Diagnostics.Diagnostics()))
		for _, diag := range result.Diagnostics.Diagnostics() {
			if diag == nil {
				continue
			}
			msgs = append(msgs, diag.Code+": "+diag.Message)
		}
		t.Fatalf("unexpected diagnostics: %v", msgs)
	}
	if result.Entry == nil || result.Entry.LoweredHIR == nil {
		t.Fatal("expected lowered HIR module")
	}

	var mainFn *hir.Func
	var specialized *hir.Func
	for _, fn := range result.Entry.LoweredHIR.Functions {
		if fn == nil {
			continue
		}
		switch {
		case fn.Name == "main":
			mainFn = fn
		case strings.HasPrefix(fn.Name, "New$"):
			specialized = fn
		}
	}
	if mainFn == nil || specialized == nil {
		t.Fatalf("expected main and specialized New function, got %#v", result.Entry.LoweredHIR.Functions)
	}

	letStmt, ok := mainFn.Body.Stmts[0].(*hir.LetStmt)
	if !ok {
		t.Fatalf("expected let stmt, got %T", mainFn.Body.Stmts[0])
	}
	call, ok := letStmt.Value.(*hir.CallExpr)
	if !ok {
		t.Fatalf("expected call expr, got %T", letStmt.Value)
	}
	callee, ok := call.Callee.(*hir.Ident)
	if !ok {
		t.Fatalf("expected ident callee, got %T", call.Callee)
	}
	if len(callee.Path) != 2 || callee.Path[0] != "Circle" || callee.Path[1] != specialized.Name {
		t.Fatalf("expected specialized inferred static owner path Circle::%s, got %#v", specialized.Name, callee.Path)
	}
}

func TestPipelineSpecializesGenericOwnerMethodMutation(t *testing.T) {
	root := t.TempDir()
	mustWriteHIR(t, filepath.Join(root, "main.ferr"), `
 type Point<T> struct {
     Value: T
 }

 fn Point<T>::Incr(&mut self, cx: i32) {
     self.Value += cx
 }

 fn main() void {
     let mut p: Point<i32> = .{ .Value = 0 }
     p.Incr(1)
 }
 `)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		msgs := make([]string, 0, len(result.Diagnostics.Diagnostics()))
		for _, diag := range result.Diagnostics.Diagnostics() {
			if diag == nil {
				continue
			}
			msgs = append(msgs, diag.Code+": "+diag.Message)
		}
		t.Fatalf("unexpected diagnostics: %v", msgs)
	}
	if result.Entry == nil || result.Entry.LoweredHIR == nil {
		t.Fatal("expected lowered HIR module")
	}
	text := hir.FormatModule(result.Entry.LoweredHIR)
	if !strings.Contains(text, "type Point$T_i32 struct") {
		t.Fatalf("expected specialized concrete Point type, got %q", text)
	}
	if strings.Contains(text, "type Point$T_T struct") {
		t.Fatalf("expected no unresolved generic Point specialization, got %q", text)
	}
	if !strings.Contains(text, "fn Point$T_i32::Incr$T_i32") {
		t.Fatalf("expected concrete specialized mutating method, got %q", text)
	}
}

func TestPipelineSpecializesOnlyRequiredGenericOwnerMethods(t *testing.T) {
	root := t.TempDir()
	mustWriteHIR(t, filepath.Join(root, "main.ferr"), `
type Shape interface {
    Draw(&self)
}

type Point<T> struct {
    Value: T
}

fn Point<T>::New(v: T) Self {
    return .{ .Value = v }
}

fn Point<T>::Draw(&self) {
}

fn Point<T>::Incr(&mut self, cx: T) {
    self.Value += cx
}

fn drawShape(s: Shape) {
    s.Draw()
}

fn main() void {
    let p: Point<i32> = .{ .Value = 1 }
    drawShape(p)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		msgs := make([]string, 0, len(result.Diagnostics.Diagnostics()))
		for _, diag := range result.Diagnostics.Diagnostics() {
			if diag == nil {
				continue
			}
			msgs = append(msgs, diag.Code+": "+diag.Message)
		}
		t.Fatalf("unexpected diagnostics: %v", msgs)
	}
	if result.Entry == nil || result.Entry.LoweredHIR == nil {
		t.Fatal("expected lowered HIR module")
	}
	text := hir.FormatModule(result.Entry.LoweredHIR)
	if !strings.Contains(text, "fn Point$T_i32::Draw$T_i32") {
		t.Fatalf("expected required Draw specialization, got %q", text)
	}
	if strings.Contains(text, "fn Point$T_i32::New$T_i32") {
		t.Fatalf("did not expect unused New specialization, got %q", text)
	}
	if strings.Contains(text, "fn Point$T_i32::Incr$T_i32") {
		t.Fatalf("did not expect unused Incr specialization, got %q", text)
	}
}

func TestPipelineKeepsDistinctSpecializationsForNamedAndTupleTypeArgs(t *testing.T) {
	root := t.TempDir()
	mustWriteHIR(t, filepath.Join(root, "main.ferr"), `
type t_i32 struct {}

type Wrap<T> struct {}

fn main() void {
    let a: Wrap<t_i32> = .{}
    let b: Wrap<(i32)> = .{}
    a
    b
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		msgs := make([]string, 0, len(result.Diagnostics.Diagnostics()))
		for _, diag := range result.Diagnostics.Diagnostics() {
			if diag == nil {
				continue
			}
			msgs = append(msgs, diag.Code+": "+diag.Message)
		}
		t.Fatalf("unexpected diagnostics: %v", msgs)
	}
	if result.Entry == nil || result.Entry.LoweredHIR == nil {
		t.Fatal("expected lowered HIR module")
	}
	names := make(map[string]struct{})
	for _, decl := range result.Entry.LoweredHIR.Types {
		if decl == nil || !strings.HasPrefix(decl.Name, "Wrap$") {
			continue
		}
		names[decl.Name] = struct{}{}
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 distinct Wrap specializations, got %d: %#v", len(names), names)
	}
}

func TestPipelineKeepsDistinctStaticOwnerMethodSpecializationsForDifferentOwnerArgs(t *testing.T) {
	root := t.TempDir()
	mustWriteHIR(t, filepath.Join(root, "main.ferr"), `
type Box<T> struct {}

fn Box<T>::Id(v: T) T {
    return v
}

fn main() i32 {
    let a = Box<i32>::Id(1)
    let b = Box<i64>::Id(2)
    return a + (b as i32)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		msgs := make([]string, 0, len(result.Diagnostics.Diagnostics()))
		for _, diag := range result.Diagnostics.Diagnostics() {
			if diag == nil {
				continue
			}
			msgs = append(msgs, diag.Code+": "+diag.Message)
		}
		t.Fatalf("unexpected diagnostics: %v", msgs)
	}
	if result.Entry == nil || result.Entry.LoweredHIR == nil {
		t.Fatal("expected lowered HIR module")
	}
	methodNames := make(map[string]struct{})
	for _, fn := range result.Entry.LoweredHIR.Functions {
		if fn == nil {
			continue
		}
		if !strings.HasPrefix(fn.Name, "Id$") {
			continue
		}
		methodNames[fn.Name] = struct{}{}
	}
	if len(methodNames) != 2 {
		t.Fatalf("expected 2 distinct Id specializations, got %d: %#v", len(methodNames), methodNames)
	}
}

func TestPipelineKeepsDistinctFunctionSpecializationsForCrossModuleSameTypeName(t *testing.T) {
	root := t.TempDir()
	mustWriteHIR(t, filepath.Join(root, "lib", "a.ferr"), `
type Data struct {}

fn Make() Data {
    return .{}
}
`)
	mustWriteHIR(t, filepath.Join(root, "lib", "b.ferr"), `
type Data struct {}

fn Make() Data {
    return .{}
}
`)
	mustWriteHIR(t, filepath.Join(root, "main.ferr"), `
import "lib/a"
import "lib/b"

fn use<T>(value: T) {
    value
}

fn main() {
    use(a::Make())
    use(b::Make())
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		msgs := make([]string, 0, len(result.Diagnostics.Diagnostics()))
		for _, diag := range result.Diagnostics.Diagnostics() {
			if diag == nil {
				continue
			}
			msgs = append(msgs, diag.Code+": "+diag.Message)
		}
		t.Fatalf("unexpected diagnostics: %v", msgs)
	}
	if result.Entry == nil || result.Entry.LoweredHIR == nil {
		t.Fatal("expected lowered HIR module")
	}
	names := make(map[string]struct{})
	for _, fn := range result.Entry.LoweredHIR.Functions {
		if fn == nil || !strings.HasPrefix(fn.Name, "use$") {
			continue
		}
		names[fn.Name] = struct{}{}
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 distinct use specializations, got %d: %#v", len(names), names)
	}
}

func TestPipelineKeepsDistinctTypeSpecializationsForCrossModuleSameTypeName(t *testing.T) {
	root := t.TempDir()
	mustWriteHIR(t, filepath.Join(root, "lib", "a.ferr"), `
type Data struct {}

fn Make() Data {
    return .{}
}
`)
	mustWriteHIR(t, filepath.Join(root, "lib", "b.ferr"), `
type Data struct {}

fn Make() Data {
    return .{}
}
`)
	mustWriteHIR(t, filepath.Join(root, "main.ferr"), `
import "lib/a"
import "lib/b"

type Wrap<T> struct {
    Value: T
}

fn main() {
    let wa: Wrap<a::Data> = .{ .Value = a::Make() }
    let wb: Wrap<b::Data> = .{ .Value = b::Make() }
    wa
    wb
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		msgs := make([]string, 0, len(result.Diagnostics.Diagnostics()))
		for _, diag := range result.Diagnostics.Diagnostics() {
			if diag == nil {
				continue
			}
			msgs = append(msgs, diag.Code+": "+diag.Message)
		}
		t.Fatalf("unexpected diagnostics: %v", msgs)
	}
	if result.Entry == nil || result.Entry.LoweredHIR == nil {
		t.Fatal("expected lowered HIR module")
	}
	names := make(map[string]struct{})
	for _, decl := range result.Entry.LoweredHIR.Types {
		if decl == nil || !strings.HasPrefix(decl.Name, "Wrap$") {
			continue
		}
		names[decl.Name] = struct{}{}
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 distinct Wrap specializations, got %d: %#v", len(names), names)
	}
}

func TestPipelineCrossModuleSpecializesImportedGenericFunction(t *testing.T) {
	root := t.TempDir()
	mustWriteHIR(t, filepath.Join(root, "util", "math.ferr"), `
fn Pick<T>(value: T) T {
    return value
}

fn UsePickI32() i32 {
    return Pick(1)
}

fn UsePickI64() i64 {
    return Pick(2 as i64)
}
`)
	mustWriteHIR(t, filepath.Join(root, "main.ferr"), `
import "util/math"

fn main() i32 {
    return math::UsePickI32() + (math::UsePickI64() as i32)
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		msgs := make([]string, 0, len(result.Diagnostics.Diagnostics()))
		for _, diag := range result.Diagnostics.Diagnostics() {
			if diag == nil {
				continue
			}
			msgs = append(msgs, diag.Code+": "+diag.Message)
		}
		t.Fatalf("unexpected diagnostics: %v", msgs)
	}
	specialized := make(map[string]struct{})
	for _, mod := range append(result.Modules, result.Entry) {
		if mod == nil || mod.LoweredHIR == nil {
			continue
		}
		for _, fn := range mod.LoweredHIR.Functions {
			if fn == nil || !strings.HasPrefix(fn.Name, "Pick$") {
				continue
			}
			specialized[fn.Name] = struct{}{}
		}
	}
	if len(specialized) != 2 {
		t.Fatalf("expected 2 imported Pick specializations, got %d: %#v", len(specialized), specialized)
	}
}

func TestPipelineCrossModuleSpecializesImportedGenericTypeMethod(t *testing.T) {
	root := t.TempDir()
	mustWriteHIR(t, filepath.Join(root, "util", "box.ferr"), `
type Box<T> struct {
    Value: T
}

fn Box<T>::Get(&self) T {
    return self.Value
}

fn UseBoxGet() i32 {
    let b: Box<i32> = .{ .Value = 7 }
    return b.Get()
}
`)
	mustWriteHIR(t, filepath.Join(root, "main.ferr"), `
import "util/box"

fn main() i32 {
    return box::UseBoxGet()
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		msgs := make([]string, 0, len(result.Diagnostics.Diagnostics()))
		for _, diag := range result.Diagnostics.Diagnostics() {
			if diag == nil {
				continue
			}
			msgs = append(msgs, diag.Code+": "+diag.Message)
		}
		t.Fatalf("unexpected diagnostics: %v", msgs)
	}
	typeNames := make(map[string]struct{})
	methodNames := make(map[string]struct{})
	for _, mod := range append(result.Modules, result.Entry) {
		if mod == nil || mod.LoweredHIR == nil {
			continue
		}
		for _, decl := range mod.LoweredHIR.Types {
			if decl == nil || !strings.HasPrefix(decl.Name, "Box$") {
				continue
			}
			typeNames[decl.Name] = struct{}{}
		}
		for _, fn := range mod.LoweredHIR.Functions {
			if fn == nil || !strings.HasPrefix(fn.Name, "Get$") {
				continue
			}
			methodNames[fn.Name] = struct{}{}
		}
	}
	if len(typeNames) == 0 {
		t.Fatalf("expected imported Box specialization, got %#v", typeNames)
	}
	if len(methodNames) == 0 {
		t.Fatalf("expected imported Get specialization, got %#v", methodNames)
	}
}

func mustWriteHIR(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHIRBorrowPrefixOpAndSpacing(t *testing.T) {
	root := t.TempDir()
	src := "fn main() {\n    let mut p = 1\n    let m = &mut p\n    m\n}\n"
	mustWriteHIR(t, filepath.Join(root, "main.ferr"), src)
	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.HIR == nil || len(result.Entry.HIR.Functions) == 0 {
		t.Fatalf("expected HIR functions, got %#v", result.Entry)
	}
	mainFn := result.Entry.HIR.Functions[0]
	letStmt, ok := mainFn.Body.Stmts[1].(*hir.LetStmt)
	if !ok {
		t.Fatalf("expected second stmt let, got %T", mainFn.Body.Stmts[1])
	}
	prefix, ok := letStmt.Value.(*hir.PrefixExpr)
	if !ok {
		t.Fatalf("expected prefix expr, got %T", letStmt.Value)
	}
	if prefix.Op != "&mut" {
		t.Fatalf("expected op &mut, got %q", prefix.Op)
	}
	text := hir.FormatModule(result.Entry.HIR)
	if !strings.Contains(text, "&mut p") {
		t.Fatalf("expected spacing &mut p in HIR dump, got %q", text)
	}
	if result.Entry.LoweredHIR != nil {
		loweredText := hir.FormatModule(result.Entry.LoweredHIR)
		if !strings.Contains(loweredText, "&mut p") {
			t.Fatalf("expected spacing &mut p in lowered HIR dump, got %q", loweredText)
		}
	}
}

func TestLoweredHIRGenericMutBorrowSpacing(t *testing.T) {
	root := t.TempDir()
	mustWriteHIR(t, filepath.Join(root, "main.ferr"), `
type Point<T> struct {
    Value: T
}

fn Point<T>::Incr(&mut self, cx: T) {
    self.Value += cx
}

fn main() {
    let mut p: Point<i32> = .{ .Value = 1 }
    p.Incr(1)
    let m = &mut p
    m
}
`)

	result := compiler.New(root, ".ferr", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		msgs := make([]string, 0, len(result.Diagnostics.Diagnostics()))
		for _, diag := range result.Diagnostics.Diagnostics() {
			if diag == nil {
				continue
			}
			msgs = append(msgs, diag.Code+": "+diag.Message)
		}
		t.Fatalf("unexpected diagnostics: %v", msgs)
	}
	if result.Entry == nil || result.Entry.LoweredHIR == nil {
		t.Fatal("expected lowered HIR module")
	}
	text := hir.FormatModule(result.Entry.LoweredHIR)
	if !strings.Contains(text, "&mut p") {
		t.Fatalf("expected spacing &mut p in generic lowered HIR dump, got %q", text)
	}
}
