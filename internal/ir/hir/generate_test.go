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

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
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
	if text == "" || !contains(text, "type Point struct") {
		t.Fatalf("expected type declaration in hir dump, got %q", text)
	}
	if !contains(text, "X: i32 = 0") {
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

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
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

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
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

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
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

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
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

func contains(s, sub string) bool { return strings.Contains(s, sub) }

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
	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
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

	result := compiler.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
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
