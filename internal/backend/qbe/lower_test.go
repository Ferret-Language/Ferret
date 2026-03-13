package qbe_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compiler/internal/backend"
	"compiler/internal/backend/registry"
	compilerapi "compiler/internal/compiler"
	"compiler/internal/layout"
	midmir "compiler/internal/middleend/mir"
)

func TestLowerScalarFunctionToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
fn add(a i32, b i32) i32 {
    let sum = a + b
    return sum
}
`)
	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"function w $main__add(w %a, w %b)",
		"@entry",
		"%sum =w add %a, %b",
		"ret %sum",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerBranchAndGlobalToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
let GlobalFlag: bool = true

fn main() i32 {
    if GlobalFlag {
        return 1
    }
    return 0
}
`)
	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"data $main__GlobalFlag = { b 1 }",
		"export function w $main()",
		"%_ld1 =w loadub $main__GlobalFlag",
		"jnz %_ld1",
		"ret 1",
		"ret 0",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerMatchToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
fn main(x i32) i32 {
    match x {
        0 => { return 1 }
        _ => { return 2 }
    }
}
`)
	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe: %v", err)
	}
	for _, want := range []string{
		"ceqw %x, 0",
		"ret 1",
		"ret 2",
	} {
		if !strings.Contains(artifact.Text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, artifact.Text)
		}
	}
}

func TestLowerStructFieldAccessToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
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
	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"type :local__main__Point = { w, w }",
		"data $main__GlobalPoint = { w 1, w 2 }",
		"%p =l alloc4 8",
		"blit $main__GlobalPoint, %p, 8",
		"%_t1 =w loadw %p",
		"storew %_t4, %p",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerImportedFunctionCallToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "util", "build.ferr"), `
fn Origin() i32 {
    return 7
}
`)
	mustWrite(t, filepath.Join(root, "main.ferr"), `
import "util/build"

fn main() i32 {
    return build::Origin()
}
`)
	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe: %v", err)
	}
	if !strings.Contains(artifact.Text, "call $util__build__Origin()") {
		t.Fatalf("expected imported call symbol in qbe output:\n%s", artifact.Text)
	}
}

func TestLowerExternFunctionCallToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
#[extern("ferret_math_abs_i32")]
fn AbsI32(value i32) i32;

fn main() i32 {
    return AbsI32(-1)
}
`)
	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe: %v", err)
	}
	if !strings.Contains(artifact.Text, "call $ferret_math_abs_i32(w -1)") {
		t.Fatalf("expected extern link symbol in qbe output:\n%s", artifact.Text)
	}
}

func TestLowerImportedStructTypeToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "math", "vec2.ferr"), `
type Vec2 struct {
    X i32 = 0
    Y i32 = 0
}

fn Origin() Vec2 {
    return .{ .X = 1, .Y = 2 }
}
`)
	mustWrite(t, filepath.Join(root, "main.ferr"), `
import "math/vec2"

fn main() i32 {
    let p = vec2::Origin()
    return p.X
}
`)
	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"%p =l alloc4 8",
		"call $math__vec2__Origin()",
		"%_t1 =w loadw %p",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerNumericToStringCastToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
fn main() void {
    let text = 42 as str
    print(text)
}
`)
	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"call $global__i64_str",
		"call $global__print_str",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerUnionLocalAssignmentToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
type Token union {
    i32,
    i64,
}

fn main() i32 {
    let value: Token = 1
    print(0)
    return 0
}
`)
	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"type :local__main__Token = align 8 { 16 }",
		"%value =l alloc8 16",
		"storew 0, %value",
		"%_unionpayload",
		"storew 1, %_unionpayload",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerUnionExtractCastToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
type Token union {
    i32,
    i64,
}

fn main(flag bool) i32 {
    let mut value: Token = 1
    if flag {
        value = 2 as i64
    }
    let out = value as i32
    return out
}
`)
	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"%value =l alloc8 16",
		"%_unioncast",
		"%_unionpayload2 =l add %value, 8",
		"storew 1, %value",
		"%_store",
		"storel %_store",
		"loadw %_unionpayload",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerUnionGlobalToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
type Token union {
    i32,
    i64,
}

let Global: Token = 1

fn main() i32 {
    let out = Global as i32
    return out
}
`)
	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"type :local__main__Token = align 8 { 16 }",
		"data $main__Global = { w 0, z 4, w 1, z 4 }",
		"%_unionpayload1 =l add $main__Global, 8",
		"loadw %_unionpayload1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testUnit(result compilerapi.Result) *backend.Unit {
	layouts := make(map[string]*layout.Module)
	modules := make(map[string]*midmir.Module)
	for _, mod := range result.Modules {
		if mod != nil && mod.Layout != nil {
			layouts[mod.Key] = mod.Layout
		}
		if mod != nil && mod.MIR != nil {
			modules[mod.Key] = mod.MIR
		}
	}
	if result.Entry != nil && result.Entry.Layout != nil {
		layouts[result.Entry.Key] = result.Entry.Layout
	}
	if result.Entry != nil && result.Entry.MIR != nil {
		modules[result.Entry.Key] = result.Entry.MIR
	}
	return &backend.Unit{
		Module:  result.Entry.MIR,
		Layout:  result.Entry.Layout,
		Layouts: layouts,
		Modules: modules,
	}
}

func TestLowerMethodCallToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X i32 = 0
    Y i32 = 0
}

fn (p Point) Len2() i32 {
    return p.X * p.X + p.Y * p.Y
}

fn main() i32 {
    let p: Point = .{ .X = 3, .Y = 4 }
    return p.Len2()
}
`)
	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe: %v", err)
	}
	text := artifact.Text
	// Method should be emitted with receiver as first parameter.
	if !strings.Contains(text, "function w $Point__Len2(l %p)") {
		t.Fatalf("expected receiver parameter in Len2 signature, got:\n%s", text)
	}
	// Call site should pass receiver as first argument.
	if !strings.Contains(text, "call $Point__Len2(") {
		t.Fatalf("expected direct method call in main, got:\n%s", text)
	}
}

func TestLowerInterfaceDispatchToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
type Stringer interface {
    String() str
}

type Name struct {
    value i32 = 0
}

fn (n Name) String() str {
    return 1 as str
}

fn main() str {
    let n: Name = .{ .value = 1 }
    let s: Stringer = n
    return s.String()
}
`)
	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("unexpected qbe error: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"data $vtable__local__main__Stringer__main__Name = { l $ifacewrap__local__main__Stringer__main__Name__String }",
		"function :__ferret_slice $ifacewrap__local__main__Stringer__main__Name__String(l %data)",
		"%s =l alloc8 16",
		"%_iface_fn",
		"call %_iface_fn",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerImportedInterfaceDispatchToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "util", "name.ferr"), `
type Name struct {
    value i32 = 0
}

fn Origin() Name {
    return .{ .value = 7 }
}

fn (n Name) String() str {
    return 1 as str
}
`)
	mustWrite(t, filepath.Join(root, "main.ferr"), `
import "util/name"

type Stringer interface {
    String() str
}

fn main() str {
    let n = name::Origin()
    let s: Stringer = n
    return s.String()
}
`)
	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("unexpected qbe error: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"type :local__main__Stringer = { l, l }",
		"data $vtable__local__main__Stringer__util__name__Name = { l $ifacewrap__local__main__Stringer__util__name__Name__String }",
		"call $util__name__Name__String(",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerGlobalInterfaceValueToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
type Stringer interface {
    String() str
}

type Name struct {
    value i32 = 0
}

fn (n Name) String() str {
    return 1 as str
}

let GlobalName: Name = .{ .value = 1 }
let GlobalStringer: Stringer = GlobalName

fn main() str {
    return GlobalStringer.String()
}
`)
	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("unexpected qbe error: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"data $main__GlobalName = { w 1 }",
		"data $main__GlobalStringer = { l $main__GlobalName, l $vtable__local__main__Stringer__main__Name }",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerEnumValuesToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
type Color enum {
    Red,
    Green,
    Blue,
}

let mut RuntimeColor: Color = Color::Red
const DefaultColor = Color::Green

fn main(flag bool) i32 {
    let mut value = Color::Blue
    if flag {
        value = RuntimeColor
    }
    if value == DefaultColor {
        return 1
    }
    return 2
}
`)
	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"data $main__RuntimeColor = { w 0 }",
		"data $main__DefaultColor = { w 1 }",
		"%value_slot =l alloc4 4",
		"storew %_asgn1, %value_slot",
		"storew %_ld2, %value_slot",
		"ceqw %_ld3, 1",
		"ret 1",
		"ret 2",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}
