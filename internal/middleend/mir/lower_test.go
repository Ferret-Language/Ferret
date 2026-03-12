package mir_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	compilerapi "compiler/internal/compiler"
	"compiler/internal/diagnostics"
	midmir "compiler/internal/middleend/mir"
	"compiler/internal/phase"
)

func TestPipelineGeneratesMIR(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X i32 = 0
    Y i32 = 0
}

let mut GlobalPoint: Point = .{ .X = 1, .Y = 2 }

fn main() i32 {
    let mut p = copy GlobalPoint
    if p.X > 0 {
        p.X = p.X + 1
    }
    return p.X
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected ownership analyzed phase, got %#v", result.Entry)
	}
	if result.Entry.MIR == nil {
		t.Fatal("expected MIR module")
	}
	if len(result.Entry.MIR.Types) != 1 {
		t.Fatalf("expected one mir type decl, got %#v", result.Entry.MIR.Types)
	}
	if len(result.Entry.MIR.Functions) != 1 {
		t.Fatalf("expected one mir function, got %#v", result.Entry.MIR.Functions)
	}
	fn := result.Entry.MIR.Functions[0]
	if fn.EntryID < 0 {
		t.Fatalf("expected valid entry id, got %#v", fn)
	}
	foundStore := false
	foundBranch := false
	foundCompute := false
	for _, block := range fn.Blocks {
		for _, instr := range block.Instructions {
			if _, ok := instr.(*midmir.StoreFieldInstr); ok {
				foundStore = true
			}
			if _, ok := instr.(*midmir.ComputeInstr); ok {
				foundCompute = true
			}
		}
		if _, ok := block.Terminator.(*midmir.BranchTerm); ok {
			foundBranch = true
		}
	}
	if !foundStore {
		t.Fatal("expected store_field instruction in lowered MIR")
	}
	if !foundBranch {
		t.Fatal("expected branch terminator in lowered MIR")
	}
	if !foundCompute {
		t.Fatal("expected compute instruction in normalized MIR")
	}
	for _, block := range fn.Blocks {
		switch term := block.Terminator.(type) {
		case *midmir.BranchTerm:
			if _, ok := term.Cond.(*midmir.LocalValue); !ok {
				t.Fatalf("expected branch condition temp, got %T", term.Cond)
			}
		case *midmir.ReturnTerm:
			if term.Value != nil {
				switch term.Value.(type) {
				case *midmir.LocalValue, *midmir.NameValue, *midmir.NumberValue, *midmir.StringValue, *midmir.NoneValue:
				default:
					t.Fatalf("expected normalized simple return value, got %T", term.Value)
				}
			}
		}
	}
	text := midmir.FormatModule(result.Entry.MIR)
	if !strings.Contains(text, "type Point struct") {
		t.Fatalf("expected type declaration in mir dump, got %q", text)
	}
	if !strings.Contains(text, "X i32 = 0") || !strings.Contains(text, "Y i32 = 0") {
		t.Fatalf("expected field defaults in mir dump, got %q", text)
	}
}

func TestPipelineGeneratesExplicitAddrOfAndLoadInMIR(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X i32 = 0
}

fn probe(p *own Point) void {
    let q = &*p
    q
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil || len(result.Entry.MIR.Functions) != 1 {
		t.Fatalf("expected one MIR function, got %#v", result.Entry)
	}
	text := midmir.FormatModule(result.Entry.MIR)
	if !strings.Contains(text, "load p") {
		t.Fatalf("expected explicit load in MIR dump, got %q", text)
	}
	if !strings.Contains(text, "addr_of") {
		t.Fatalf("expected explicit addr_of in MIR dump, got %q", text)
	}
}

func TestPipelineLowersPanicToMIRTerminator(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.ferr"), `
fn fail() void {
    panic "bad"
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil || len(result.Entry.MIR.Functions) != 1 {
		t.Fatalf("expected MIR functions, got %#v", result.Entry)
	}
	var fn *midmir.Function
	for _, candidate := range result.Entry.MIR.Functions {
		if candidate.Name == "fail" {
			fn = candidate
			break
		}
	}
	if fn == nil {
		t.Fatalf("expected MIR function fail, got %#v", result.Entry.MIR.Functions)
	}
	if len(fn.Blocks) == 0 {
		t.Fatalf("expected MIR blocks, got %#v", fn)
	}
	text := midmir.FormatModule(result.Entry.MIR)
	if !strings.Contains(text, "panic ") || !strings.Contains(text, "\"bad\"") {
		t.Fatalf("expected lowered panic sequence in MIR dump, got %q", text)
	}
}

func TestPipelineLowersDeferredPanicCleanupToMIR(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.ferr"), `
fn close() void {}

fn fail() void {
    defer close()
    panic "bad"
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	var fn *midmir.Function
	for _, candidate := range result.Entry.MIR.Functions {
		if candidate.Name == "fail" {
			fn = candidate
			break
		}
	}
	if fn == nil {
		t.Fatalf("expected MIR function fail, got %#v", result.Entry.MIR.Functions)
	}
	text := midmir.FormatModule(result.Entry.MIR)
	if !strings.Contains(text, "defer close()") || !strings.Contains(text, "panic ") || !strings.Contains(text, "close()") {
		t.Fatalf("expected deferred panic cleanup sequence in MIR dump, got %q", text)
	}
}

func TestPipelineLowersDeferredReturnCleanupToMIR(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.ferr"), `
fn close() void {}

fn run() i32 {
    defer close()
    return 1
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	var fn *midmir.Function
	for _, candidate := range result.Entry.MIR.Functions {
		if candidate.Name == "run" {
			fn = candidate
			break
		}
	}
	if fn == nil {
		t.Fatalf("expected MIR function run, got %#v", result.Entry.MIR.Functions)
	}
	found := false
	for _, block := range fn.Blocks {
		term, ok := block.Terminator.(*midmir.ReturnTerm)
		if !ok {
			continue
		}
		found = true
		if term.CleanupID < 0 {
			t.Fatalf("expected return cleanup id, got %#v", term)
		}
		break
	}
	if !found {
		t.Fatalf("expected return terminator in MIR, got %#v", fn.Blocks)
	}
	text := midmir.FormatModule(result.Entry.MIR)
	if !strings.Contains(text, "return 1 unwind") {
		t.Fatalf("expected return unwind in MIR dump, got %q", text)
	}
}

func TestPipelineLowersStaticFieldConstructorAndDestructorToMIR(t *testing.T) {
	root := t.TempDir()
	mustWriteIR(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X i32 = 0
    Y i32 = 0
    static Origin Point = .{}
}

fn (p *mut Point) Point() {
	p.Y = p.Y + 1
}

fn (p *own Point) ~Point() void {
    p.X = 0
}

fn main() i32 {
	let p: Point = .{ .X = 3, .Y = 4 }
    let q = Point::Origin
    return p.X + q.X
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatalf("expected MIR module, got %#v", result.Entry)
	}

	foundStaticGlobal := false
	for _, global := range result.Entry.MIR.Globals {
		if global != nil && global.Name == "Point__Origin" {
			foundStaticGlobal = true
			break
		}
	}
	if !foundStaticGlobal {
		t.Fatalf("expected synthesized static global Point__Origin, got %#v", result.Entry.MIR.Globals)
	}

	var mainFn *midmir.Function
	for _, fn := range result.Entry.MIR.Functions {
		if fn != nil && fn.Name == "main" {
			mainFn = fn
			break
		}
	}
	if mainFn == nil {
		t.Fatalf("expected MIR function main, got %#v", result.Entry.MIR.Functions)
	}

	foundImplicitConstructor := false
	foundDestructorDefer := false
	for _, block := range mainFn.Blocks {
		for _, instr := range block.Instructions {
			switch ins := instr.(type) {
			case *midmir.AssignInstr:
				if comp, ok := ins.Value.(*midmir.CompositeValue); ok && hasConstructorPath(comp, "Point") {
					foundImplicitConstructor = true
				}
			case *midmir.ComputeInstr:
				if comp, ok := ins.Value.(*midmir.CompositeValue); ok && hasConstructorPath(comp, "Point") {
					foundImplicitConstructor = true
				}
			case *midmir.EvalInstr:
				if comp, ok := ins.Value.(*midmir.CompositeValue); ok && hasConstructorPath(comp, "Point") {
					foundImplicitConstructor = true
				}
			case *midmir.DeferInstr:
				if deferContainsCallNamed(ins, "~Point") {
					foundDestructorDefer = true
				}
			}
		}
	}
	if !foundImplicitConstructor {
		t.Fatal("expected implicit constructor metadata on lowered composite literal")
	}
	if !foundDestructorDefer {
		t.Fatal("expected destructor defer in lowered MIR")
	}
}

func hasConstructorPath(comp *midmir.CompositeValue, name string) bool {
	if comp == nil || len(comp.ConstructorPath) == 0 {
		return false
	}
	return comp.ConstructorPath[len(comp.ConstructorPath)-1] == name
}

func hasCallNamed(call *midmir.CallValue, name string) bool {
	if call == nil {
		return false
	}
	callee, ok := call.Callee.(*midmir.NameValue)
	if !ok || len(callee.Path) == 0 {
		return false
	}
	return callee.Path[len(callee.Path)-1] == name
}

func deferContainsCallNamed(instr *midmir.DeferInstr, name string) bool {
	if instr == nil {
		return false
	}
	for _, child := range instr.Body {
		switch body := child.(type) {
		case *midmir.EvalInstr:
			if call, ok := body.Value.(*midmir.CallValue); ok && hasCallNamed(call, name) {
				return true
			}
		case *midmir.ComputeInstr:
			if call, ok := body.Value.(*midmir.CallValue); ok && hasCallNamed(call, name) {
				return true
			}
		case *midmir.DeferInstr:
			if deferContainsCallNamed(body, name) {
				return true
			}
		}
	}
	return false
}

func mustWriteIR(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
