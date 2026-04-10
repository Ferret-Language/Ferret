package qbe_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	layout "compiler/internal/analysis/layout/model"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/backend"
	"compiler/internal/backend/registry"
	"compiler/internal/core/context"
	compiler "compiler/internal/driver"
	"compiler/internal/ir/mir"
	"compiler/internal/testutil"
)

func TestLowerScalarFunctionToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn add(a: i32, b: i32) -> i32 {
    let sum = a + b
    return sum
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	mustWrite(t, filepath.Join(root, "main.fer"), `
let GlobalFlag: bool = true

fn main() -> i32 {
    if GlobalFlag {
        return 1
    }
    return 0
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main(x: i32) -> i32 {
    match x {
        0 => { return 1 }
        _ => { return 2 }
    }
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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

func TestLowerOptionalMatchNoneToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let value: ?i32 = none
    let out: i32 = match value {
        is i32 => value
        _ => -1
    }
    return out
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"%value =l alloc4 8",
		"storew 0, %value",
		"%_t5 =w copy 0",
		"jnz %_t5, @bb1, @bb2",
		"%__match1 =w copy -1",
		"ret %out",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerStructFieldAccessToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
    Y: i32 = 0
}

let mut GlobalPoint: Point = .{ .X = 1, .Y = 2 }

fn main() -> i32 {
    let mut p = GlobalPoint
    if p.X > 0 {
        p.X = p.X + 1
    }
    return p.X
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	mustWrite(t, filepath.Join(root, "util", "build.fer"), `
fn Origin() -> i32 {
    return 7
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `
import "util/build"

fn main() -> i32 {
    return build::Origin()
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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

func TestLowerAggregateLoadAssignmentToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Conn struct {}

fn run(mut c: *Conn) -> void {
    let r = &*c
    r
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	if _, err := lowerer.LowerModule(testUnit(result)); err != nil {
		t.Fatalf("lower qbe aggregate load: %v", err)
	}
}

func TestLowerBuiltinLenArrayToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> usize {
    let items: [_]i32 = [_]i32{1, 2, 3}
    return len(items)
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	if !strings.Contains(text, "ret 3") {
		t.Fatalf("expected array len constant lowering in qbe output:\n%s", text)
	}
}

func TestLowerBuiltinLenSliceToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main(items: []i32) -> usize {
    return len(items)
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	if !strings.Contains(text, "call $ferret_global_slice_len(l %items)") {
		t.Fatalf("expected slice len runtime call in qbe output:\n%s", text)
	}
}

func TestLowerBuiltinLenStringToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main(s: str) -> usize {
    return len(s)
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	if !strings.Contains(text, "call $ferret_global_slice_len(l %s)") {
		t.Fatalf("expected string len runtime call in qbe output:\n%s", text)
	}
}

func TestLowerBuiltinLenStringRefToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main(s: &str) -> usize {
    return len(s)
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	if !strings.Contains(text, "call $ferret_global_slice_len(") {
		t.Fatalf("expected string ref len runtime call in qbe output:\n%s", text)
	}
}

func TestLowerSliceLiteralToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let items: []i32 = []i32{1, 2, 3}
    return items[1]
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"slice_lit_buf",
		"storel 3",
		"storew 1",
		"storew 2",
		"storew 3",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerArrayToSliceCallToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn head(items: []i32) -> i32 {
    return items[0]
}

fn main() -> i32 {
    let items: [3]i32 = [3]i32{1, 2, 3}
    return head(items)
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"function w $main__head(l %items)",
		"call $main__head(",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerStringSliceCastsToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main(s: str) -> str {
    let bytes = s as []u8
    let text = bytes as str
    return text
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"call $ferret_global_str_bytes(",
		"call $ferret_global_bytes_str(",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerStringCharSliceCastsToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main(s: str) -> str {
    let chars = s as []char
    let text = chars as str
    return text
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"call $ferret_global_str_chars(",
		"call $ferret_global_chars_str(",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerReceiverFieldReadToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Point struct {
    Value: i32 = 0
}

fn Point::Incr(&mut self) -> void {
    self.Value = self.Value + 1
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	if _, err := lowerer.LowerModule(testUnit(result)); err != nil {
		t.Fatalf("lower qbe receiver field read: %v", err)
	}
}

func TestLowerExternFunctionCallToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
#[extern("ferret_math_abs_i32")]
fn AbsI32(value: i32) -> i32;

fn main() -> i32 {
    return AbsI32(-1)
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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

func TestLowerDefaultExternFunctionCallToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "io.fer"), `
#[extern]
fn Println(text: str) -> void;
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `
import "std/io"

fn main() -> void {
    io::Println("hello")
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	if !strings.Contains(artifact.Text, "call $ferret_std_io_Println(") {
		t.Fatalf("expected default-mangled extern link symbol in qbe output:\n%s", artifact.Text)
	}
}

func TestLowerStdIOWriteToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "io.fer"), `
type Error error {
    unknown
}

type Writer interface {
    Write(&mut self, text: str) -> usize
}

type Stream struct {
    kind: i32
}

#[extern("ferret_std_io_write_stream")]
fn write_stream(kind: i32, text: &str) -> usize;

let mut Stdout: Stream = .{ .kind = 1 }

fn Stream::Write(&mut self, text: str) -> usize {
    return write_stream(self.kind, &text)
}

fn Write(mut dst: Writer, text: str) -> usize {
    return dst.Write(text)
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `
import "std/io"

fn main() -> void {
    _ = io::Write(io::Stdout, "hello")
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	if !strings.Contains(artifact.Text, "call $std__io__Write(") {
		t.Fatalf("expected std/io write helper call in qbe output:\n%s", artifact.Text)
	}
}

func TestLowerStdFSWriteToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "io.fer"), `
type Error error {
    unknown
}

type Writer interface {
    Write(&mut self, text: str) -> usize
}

fn Write(mut dst: Writer, text: str) -> usize {
    return dst.Write(text)
}
`)
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "mem.fer"), `
#[extern]
fn Expose<T>(owner: *T) -> ^T;

#[extern]
fn ExposeRef<T>(owner: &*T) -> ^T;

#[extern]
fn Adopt<T>(raw: ^T) -> *T;
`)
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "fs.fer"), `
import "std/mem"

type fileInner struct {
    handle: ^void
}

type File struct {
    inner: *fileInner
}

#[extern("ferret_std_fs_open")]
fn open_raw(path: &str) -> ^void;

#[extern("ferret_std_fs_write")]
fn write_raw(handle: ^void, text: &str) -> usize;

#[extern("ferret_std_fs_close")]
fn close_raw(handle: ^void) -> void;

fn Open(path: str) -> File {
    unsafe {
        return .{
            .inner = mem::Adopt(open_raw(&path) as ^fileInner)
        }
    }
}

fn File::Write(&mut self, text: str) -> usize {
    unsafe {
        return write_raw(mem::ExposeRef(&self.inner) as ^void, &text)
    }
}

fn File::Close(self) -> void {
    unsafe {
        close_raw(mem::Expose(self.inner) as ^void)
    }
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `
import "std/io"
import "std/fs"

fn main() -> void {
    let mut file = fs::Open("out.txt")
    _ = io::Write(file, "hello")
    file.Close()
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"call $std__io__Write(",
		"call $std__fs__File__Close(",
		"call $std__fs__Open(",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerStdIOBufferToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "mem.fer"), `
#[extern]
fn Expose<T>(owner: *T) -> ^T;

#[extern]
fn ExposeRef<T>(owner: &*T) -> ^T;

#[extern]
fn Adopt<T>(raw: ^T) -> *T;
`)
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "io.fer"), `
import "std/mem"

type Writer interface {
    Write(&mut self, text: str) -> usize
}

type Reader interface {
    Read(&mut self, size: usize) -> []u8
}

type bufferInner struct {
    data: ^u8
    len: usize = 0
    cap: usize = 0
    read_pos: usize = 0
}

type Buffer struct {
    inner: *bufferInner
}

#[extern("ferret_std_io_buffer_new")]
fn new_buffer_raw() -> ^bufferInner;

#[extern("ferret_std_io_buffer_write")]
fn write_buffer_raw(handle: ^void, text: &str) -> usize;

#[extern("ferret_std_io_buffer_read")]
fn read_buffer_raw(handle: ^void, size: usize) -> []u8;

#[extern("ferret_std_io_buffer_view")]
fn view_buffer_raw(handle: ^void) -> str;

#[extern("ferret_std_io_buffer_close")]
fn close_buffer_raw(handle: ^void) -> void;

fn NewBuffer() -> Buffer {
    unsafe {
        return .{
            .inner = mem::Adopt(new_buffer_raw())
        }
    }
}

fn Buffer::Write(&mut self, text: str) -> usize {
    unsafe {
        return write_buffer_raw(mem::ExposeRef(&self.inner) as ^void, &text)
    }
}

fn Buffer::Read(&mut self, size: usize) -> []u8 {
    unsafe {
        return read_buffer_raw(mem::ExposeRef(&self.inner) as ^void, size)
    }
}

fn Buffer::AsStr(&self) -> str {
    unsafe {
        return view_buffer_raw(mem::ExposeRef(&self.inner) as ^void)
    }
}

fn Buffer::Release(self) -> void {
    unsafe {
        close_buffer_raw(mem::Expose(self.inner) as ^void)
    }
}

fn Write(mut dst: Writer, text: str) -> usize {
    return dst.Write(text)
}

fn Read(mut src: Reader, size: usize) -> []u8 {
    return src.Read(size)
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `
import "std/io"

fn main() -> void {
    let mut buf = io::NewBuffer()
    _ = io::Write(buf, "hello")
    _ = io::Read(buf, 2)
    _ = buf.AsStr()
    buf.Release()
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"call $std__io__Write(",
		"call $std__io__Read(",
		"call $std__io__NewBuffer(",
		"call $std__io__Buffer__Release(",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerImportedStructTypeToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "math", "vec2.fer"), `
type Vec2 struct {
    X: i32 = 0
    Y: i32 = 0
}

fn Origin() -> Vec2 {
    return .{ .X = 1, .Y = 2 }
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `
import "math/vec2"

fn main() -> i32 {
    let p = vec2::Origin()
    return p.X
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"call $math__vec2__Origin(l %p)",
		"%_t1 =w loadw %p",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerStdNetTCPToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "mem.fer"), `
#[extern]
fn Expose<T>(owner: *T) -> ^T;

#[extern]
fn ExposeRef<T>(owner: &*T) -> ^T;

#[extern]
fn Adopt<T>(raw: ^T) -> *T;
`)
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "io.fer"), `
type Error error {
    unknown
}

type Writer interface {
    Write(&mut self, text: str) -> usize
}

type Reader interface {
    Read(&mut self, size: usize) -> []u8
}

fn LastError() -> Error {
    return Error::unknown
}

fn Write(mut dst: Writer, text: str) -> usize {
    return dst.Write(text)
}

fn Read(mut src: Reader, size: usize) -> []u8 {
    return src.Read(size)
}
`)
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "net", "tcp.fer"), `
import "std/io"
import "std/mem"

type connInner struct {
    handle: ^void
}

type Conn struct {
    inner: *connInner
}

#[extern("ferret_std_net_tcp_dial")]
fn dial_raw(host: &str, port: u16) -> ^connInner;

#[extern("ferret_std_net_tcp_write")]
fn write_raw(handle: ^void, text: &str) -> usize;

#[extern("ferret_std_net_tcp_read")]
fn read_raw(handle: ^void, size: usize) -> []u8;

#[extern("ferret_std_net_tcp_close")]
fn close_raw(handle: ^void) -> void;

fn Dial(host: str, port: u16) -> io::Error!Conn {
    let raw = dial_raw(&host, port)
    let mut failed = false
    unsafe {
        failed = raw == 0 as ^connInner
    }
    if failed {
        return io::LastError()
    }
    unsafe {
        return .{
            .inner = mem::Adopt(raw)
        }
    }
}

fn Conn::Write(&mut self, text: str) -> usize {
    unsafe {
        return write_raw(mem::ExposeRef(&self.inner) as ^void, &text)
    }
}

fn Conn::Read(&mut self, size: usize) -> []u8 {
    unsafe {
        return read_raw(mem::ExposeRef(&self.inner) as ^void, size)
    }
}

fn Conn::Close(self) -> void {
    unsafe {
        close_raw(mem::Expose(self.inner) as ^void)
    }
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `
import "std/io"
import "std/net/tcp"

fn main() -> void {
    let mut conn = tcp::Dial("127.0.0.1", 8080) catch |err| {
        print(err)
        return
    }
    _ = io::Write(conn, "ping")
    _ = io::Read(conn, 4)
    conn.Close()
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"call $std__io__Write(",
		"call $std__io__Read(",
		"call $std__net__tcp__Dial(",
		"call $std__net__tcp__Conn__Close(",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerStdNetTCPListenerToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "mem.fer"), `
#[extern]
fn Expose<T>(owner: *T) -> ^T;

#[extern]
fn ExposeRef<T>(owner: &*T) -> ^T;

#[extern]
fn Adopt<T>(raw: ^T) -> *T;
`)
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "io.fer"), `
type Error error {
    unknown
}

type Writer interface {
    Write(&mut self, text: str) -> usize
}

type Reader interface {
    Read(&mut self, size: usize) -> []u8
}

fn LastError() -> Error {
    return Error::unknown
}

fn Write(mut dst: Writer, text: str) -> usize {
    return dst.Write(text)
}

fn Read(mut src: Reader, size: usize) -> []u8 {
    return src.Read(size)
}
`)
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "net", "tcp.fer"), `
import "std/io"
import "std/mem"

type connInner struct {
    handle: ^void
}

type Conn struct {
    inner: *connInner
}

type listenerInner struct {
    handle: ^void
}

type Listener struct {
    inner: *listenerInner
}

#[extern("ferret_std_net_tcp_listen")]
fn listen_raw(host: &str, port: u16) -> ^listenerInner;

#[extern("ferret_std_net_tcp_accept")]
fn accept_raw(handle: ^listenerInner) -> ^connInner;

#[extern("ferret_std_net_tcp_write")]
fn write_raw(handle: ^void, text: &str) -> usize;

#[extern("ferret_std_net_tcp_read")]
fn read_raw(handle: ^void, size: usize) -> []u8;

#[extern("ferret_std_net_tcp_close")]
fn close_raw(handle: ^void) -> void;

#[extern("ferret_std_net_tcp_close_listener")]
fn close_listener_raw(handle: ^void) -> void;

fn Listen(host: str, port: u16) -> io::Error!Listener {
    let raw = listen_raw(&host, port)
    let mut failed = false
    unsafe {
        failed = raw == 0 as ^listenerInner
    }
    if failed {
        return io::LastError()
    }
    unsafe {
        return .{
            .inner = mem::Adopt(raw)
        }
    }
}

fn Listener::Accept(&mut self) -> io::Error!Conn {
    let raw = accept_raw(mem::ExposeRef(&self.inner))
    let mut failed = false
    unsafe {
        failed = raw == 0 as ^connInner
    }
    if failed {
        return io::LastError()
    }
    unsafe {
        return .{
            .inner = mem::Adopt(raw)
        }
    }
}

fn Listener::Close(self) -> void {
    unsafe {
        close_listener_raw(mem::Expose(self.inner) as ^void)
    }
}

fn Conn::Write(&mut self, text: str) -> usize {
    unsafe {
        return write_raw(mem::ExposeRef(&self.inner) as ^void, &text)
    }
}

fn Conn::Read(&mut self, size: usize) -> []u8 {
    unsafe {
        return read_raw(mem::ExposeRef(&self.inner) as ^void, size)
    }
}

fn Conn::Close(self) -> void {
    unsafe {
        close_raw(mem::Expose(self.inner) as ^void)
    }
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `
import "std/io"
import "std/net/tcp"

fn main() -> void {
    let mut listener = tcp::Listen("127.0.0.1", 8080) catch |err| {
        print(err)
        return
    }
    let mut conn = listener.Accept() catch |err| {
        print(err)
        listener.Close()
        return
    }
    _ = io::Read(conn, 4)
    _ = io::Write(conn, "pong")
    conn.Close()
    listener.Close()
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"call $std__net__tcp__Listen(",
		"call $std__net__tcp__Listener__Accept(",
		"call $std__net__tcp__Listener__Close(",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerNumericToStringCastToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    let text = 42 as str
    print(text)
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"call $ferret_global_i64_str",
		"call $ferret_global_print(",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerVariadicPrintToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    print("ok", 42, true)
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe variadic print: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"storew 42, %_iface_data",
		"storeb 1, %_iface_data",
		"storel 3, %_len_addr",
		"call $ferret_global_print(l %_t4)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerPreludePrintlnWrapperToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "global.fer"), `
type Any interface {}

#[extern]
fn print(values: ...Any) -> void;

fn println(values: ...Any) {
    print(values...)
    print("\n")
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    println("hello")
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("unexpected qbe error: %v", err)
	}
	unit := testUnit(result)
	var combined strings.Builder
	seen := make(map[string]struct{})
	modList := append([]*context.Module{}, result.Modules...)
	if result.CompilerState != nil && result.CompilerState.Prelude != nil {
		modList = append(modList, result.CompilerState.Prelude)
	}
	modList = append(modList, result.Entry)
	for _, mod := range modList {
		if mod == nil || mod.MIR == nil || mod.Layout == nil {
			continue
		}
		if _, ok := seen[mod.Key]; ok {
			continue
		}
		seen[mod.Key] = struct{}{}
		artifact, err := lowerer.LowerModule(&backend.Unit{
			Module:  mod.MIR,
			Layout:  mod.Layout,
			Layouts: unit.Layouts,
			Modules: unit.Modules,
		})
		if err != nil {
			t.Fatalf("lower qbe println wrapper %s: %v", mod.ImportPath, err)
		}
		combined.WriteString(artifact.Text)
		combined.WriteByte('\n')
	}
	text := combined.String()
	for _, want := range []string{
		"function $global__println(",
		"call $global__println(",
		"call $ferret_global_print(",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerStructuredPrintTypeInfoToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    let arr: [3]i32 = [3]i32{1, 2, 3}
    let items: []i32 = []i32{4, 5}
    let pair: (i32, bool, str) = (7, true, "ok")
    print(arr, items, pair)
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe structured print: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"data $typeinfo__array_3__i32__meta = { l $typeinfo__i32, l 3, l 4 }",
		"data $typeinfo__slice__i32__meta = { l $typeinfo__i32, l 4 }",
		"data $typeinfo__tuple__i32__bool__str__fields = { l 0, l $typeinfo__i32, l 4, l $typeinfo__bool, l 8, l $typeinfo__str }",
		"data $typeinfo__tuple__i32__bool__str__meta = { l 3, l $typeinfo__tuple__i32__bool__str__fields }",
		"call $ferret_global_print(",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerDirectTupleLiteralPrintToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    print((1, 2, 3))
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe direct tuple print: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"data $typeinfo__tuple__i32__i32__i32__fields = { l 0, l $typeinfo__i32, l 4, l $typeinfo__i32, l 8, l $typeinfo__i32 }",
		"data $typeinfo__tuple__i32__i32__i32__meta = { l 3, l $typeinfo__tuple__i32__i32__i32__fields }",
		"call $ferret_global_print(",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerStructWithStringFieldToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Person struct {
    id: i32
    name: str
}

fn main() -> void {
    let p: Person = .{ .id = 7, .name = "ok" }
    print(p)
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("unexpected qbe error: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe struct string field: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"type :local__main__Person = { w, b 4, b 16 }",
		"blit %_t1, %_addr2, 16",
		"call $ferret_global_print(",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerTaggedOptionalToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    let value: ?i32 = 10
    if value != none {
        print(value)
    }
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("unexpected qbe error: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe tagged optional: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"loaduw %value",
		"cnew",
		"add %value, 4",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
	for _, bad := range []string{
		"add 0, 4",
		"loaduw 0",
		"copy %value",
	} {
		if strings.Contains(text, bad) {
			t.Fatalf("unexpected %q in qbe output:\n%s", bad, text)
		}
	}
}

func TestLowerErrorUnionSuccessCastUsesPayloadOffsetInQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Error error { a }

type RawResult struct {
    ok: bool
    err: Error
    handle: ^void
}

type Conn struct {
    handle: ^void
}

fn raw() -> RawResult {
    unsafe {
        return .{ .ok = true, .err = Error::a, .handle = 0 as ^void }
    }
}

fn wrap() -> Error!Conn {
    let result = raw()
    if result.ok {
        return .{ .handle = result.handle }
    }
    return result.err
}

fn main() -> void {
    let result = wrap() catch |err| {
        print(err)
        return
    }
    println(result.handle)
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	if !strings.Contains(text, "add %__match1, 8") {
		t.Fatalf("expected payload-offset cast in qbe output:\n%s", text)
	}
	if strings.Contains(text, "blit %__match1, %__match2, 8") {
		t.Fatalf("unexpected direct union-header blit in qbe output:\n%s", text)
	}
}

func TestLowerUnionLocalAssignmentToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Token union {
    i32,
    i64,
}

fn main() -> i32 {
    let value: Token = 1
    print(0)
    return 0
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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

func TestLowerRawAddressLocalToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let a = 10
    unsafe {
        let p: ^const i32 = &a
        print(p)
    }
    return 0
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"%a_slot =l alloc4 4",
		"storew %_asgn1, %a_slot",
		"%p =l copy %a_slot",
		"call $ferret_global_print(",
		"ret 0",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerRawPointerIndexWriteToQBE(t *testing.T) {
	root := t.TempDir()
	testutil.WriteStdMemFixture(t, root)
	mustWrite(t, filepath.Join(root, "main.fer"), `
import "std/mem"

fn main() -> u8 {
    let alloc = mem::System()
    unsafe {
        let mut ptr = mem::AllocAs<u8>(alloc, 4)
        ptr[0] = 7
        let view = (ptr as ^const u8, 4 as usize) as []u8
        let out = view[0]
        mem::FreeAs(alloc, ptr)
        return out
    }
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"loadl %ptr_slot",
		"storeb 7,",
		"ret %out",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerUnionExtractCastToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Token union {
    i32,
    i64,
}

fn main(flag: bool) -> i32 {
    let mut value: Token = 1
    if flag {
        value = 2 as i64
    }
    let out = value as i32
    return out
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"loadw %_unionpayload",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
	if !regexp.MustCompile(`storel %[A-Za-z0-9_]+, %_unionpayload[0-9]+`).MatchString(text) {
		t.Fatalf("expected normalized payload store in qbe output:\n%s", text)
	}
}

func TestLowerUnionGlobalToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Token union {
    i32,
    i64,
}

let Global: Token = 1

fn main() -> i32 {
    let out = Global as i32
    return out
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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

func TestLowerIntegerToRawPointerCastToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> ^void {
    unsafe {
        return 0 as ^void
    }
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"extsw 0",
		"ret %_t0",
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

func testUnit(result compiler.Result) *backend.Unit {
	layouts := make(map[string]*layout.Module)
	modules := make(map[string]*mir.Module)
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
	if result.CompilerState != nil && result.CompilerState.Prelude != nil {
		mod := result.CompilerState.Prelude
		if mod.Layout != nil {
			layouts[mod.Key] = mod.Layout
		}
		if mod.MIR != nil {
			modules[mod.Key] = mod.MIR
		}
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
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Point struct {
    X: i32 = 0
    Y: i32 = 0
}

fn Point::Len2(self) -> i32 {
    return self.X * self.X + self.Y * self.Y
}

fn main() -> i32 {
    let p: Point = .{ .X = 3, .Y = 4 }
    return p.Len2()
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	if !strings.Contains(text, "function w $main__Point__Len2(l %") {
		t.Fatalf("expected receiver parameter in Len2 signature, got:\n%s", text)
	}
	// Call site should pass receiver as first argument.
	if !strings.Contains(text, "call $main__Point__Len2(") {
		t.Fatalf("expected direct method call in main, got:\n%s", text)
	}
}

func TestLowerInterfaceDispatchToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Stringer interface {
    String(self) -> str
}

type Name struct {
    value: i32 = 0
}

fn Name::String(self) -> str {
    return 1 as str
}

fn main() -> str {
    let n: Name = .{ .value = 1 }
    let s: Stringer = n
    return s.String()
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"data $vtable__local__main__Stringer__Name = { l $typeinfo__main__Name, l $ifacewrap__local__main__Stringer__Name__String }",
		"function $ifacewrap__local__main__Stringer__Name__String(l %ret, l %data)",
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
	mustWrite(t, filepath.Join(root, "util", "name.fer"), `
type Name struct {
    value: i32 = 0
}

fn Origin() -> Name {
    return .{ .value = 7 }
}

fn Name::String(self) -> str {
    return 1 as str
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `
import "util/name"

type Stringer interface {
    String(self) -> str
}

fn main() -> str {
    let n = name::Origin()
    let s: Stringer = n
    return s.String()
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"data $vtable__local__main__Stringer__Name = { l $typeinfo__util__name__Name, l $ifacewrap__local__main__Stringer__Name__String }",
		"call $util__name__Name__String(",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerQBEDedupesImportedInterfaceHelpersAcrossModules(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "global.fer"), `
type Any interface {}
fn print(values: ...Any) -> void;
`)
	mustWrite(t, filepath.Join(root, "util", "testing.fer"), `
fn Expect(cond: bool, message: str) -> void {
    if !cond {
        print(message)
    }
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `
import "util/testing"

fn main() -> void {
    print("hello")
    testing::Expect(true, "ok")
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	layouts := make(map[string]*layout.Module)
	modules := make(map[string]*mir.Module)
	for _, mod := range result.Modules {
		if mod == nil {
			continue
		}
		if mod.Layout != nil {
			layouts[mod.Key] = mod.Layout
		}
		if mod.MIR != nil {
			modules[mod.Key] = mod.MIR
		}
	}
	if result.Entry != nil {
		if result.Entry.Layout != nil {
			layouts[result.Entry.Key] = result.Entry.Layout
		}
		if result.Entry.MIR != nil {
			modules[result.Entry.Key] = result.Entry.MIR
		}
	}
	seen := make(map[string]struct{})
	var combined strings.Builder
	for _, mod := range result.Modules {
		if mod == nil || mod.MIR == nil || mod.Layout == nil {
			continue
		}
		if _, ok := seen[mod.Key]; ok {
			continue
		}
		seen[mod.Key] = struct{}{}
		artifact, err := lowerer.LowerModule(&backend.Unit{
			Module:  mod.MIR,
			Layout:  mod.Layout,
			Layouts: layouts,
			Modules: modules,
		})
		if err != nil {
			t.Fatalf("lower qbe %s: %v", mod.ImportPath, err)
		}
		combined.WriteString(artifact.Text)
		combined.WriteByte('\n')
	}
	if result.Entry != nil && result.Entry.MIR != nil && result.Entry.Layout != nil {
		if _, ok := seen[result.Entry.Key]; !ok {
			artifact, err := lowerer.LowerModule(&backend.Unit{
				Module:  result.Entry.MIR,
				Layout:  result.Entry.Layout,
				Layouts: layouts,
				Modules: modules,
			})
			if err != nil {
				t.Fatalf("lower qbe %s: %v", result.Entry.ImportPath, err)
			}
			combined.WriteString(artifact.Text)
			combined.WriteByte('\n')
		}
	}
	const sym = "data $typeinfo__str ="
	if got := strings.Count(combined.String(), sym); got != 1 {
		t.Fatalf("expected %q once, got %d\n%s", sym, got, combined.String())
	}
}

func TestLowerGlobalInterfaceValueToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Stringer interface {
    String(self) -> str
}

type Name struct {
    value: i32 = 0
}

fn Name::String(self) -> str {
    return 1 as str
}

let GlobalName: Name = .{ .value = 1 }
let GlobalStringer: Stringer = GlobalName

fn main() -> str {
    return GlobalStringer.String()
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"data $main__GlobalStringer = { l $main__GlobalName, l $vtable__local__main__Stringer__Name }",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerRuntimeInterfaceTypeTestToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
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
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	if !strings.Contains(text, "data $typeinfo__main__Name = {") {
		t.Fatalf("expected runtime type info in qbe output:\n%s", text)
	}
	for _, pattern := range []*regexp.Regexp{
		regexp.MustCompile(`%_iface_vt_addr[0-9]+ =l add %[A-Za-z0-9_]+, 8`),
		regexp.MustCompile(`%_iface_vt[0-9]+ =l loadl %_iface_vt_addr[0-9]+`),
		regexp.MustCompile(`%_iface_typeinfo[0-9]+ =l loadl %_iface_vt[0-9]+`),
		regexp.MustCompile(`%_istype[0-9]+ =w ceql %_iface_typeinfo[0-9]+, \$typeinfo__main__Name`),
	} {
		if !pattern.MatchString(text) {
			t.Fatalf("expected %q in qbe output:\n%s", pattern.String(), text)
		}
	}
}

func TestLowerNarrowedInterfaceValueToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
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
    if s is Name {
        let narrowed: Name = s
        return narrowed.value
    }
    return 0
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	for _, pattern := range []*regexp.Regexp{
		regexp.MustCompile(`%_iface_data[0-9]+ =l loadl %s`),
		regexp.MustCompile(`blit %_iface_data[0-9]+, %narrowed, 4`),
	} {
		if !pattern.MatchString(text) {
			t.Fatalf("expected %q in qbe output:\n%s", pattern.String(), text)
		}
	}
}

func TestLowerExplicitInterfaceDowncastToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
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
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	for _, pattern := range []*regexp.Regexp{
		regexp.MustCompile(`%_iface_cast[0-9]+ =l call \$ferret__interface_downcast\(l %[A-Za-z0-9_]+, l \$typeinfo__main__Name\)`),
		regexp.MustCompile(`blit %_iface_cast[0-9]+, %narrowed, 4`),
	} {
		if !pattern.MatchString(text) {
			t.Fatalf("expected %q in qbe output:\n%s", pattern.String(), text)
		}
	}
}

func TestLowerEnumValuesToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Color enum {
    Red,
    Green,
    Blue,
}

let mut RuntimeColor: Color = Color::Red
const DefaultColor = Color::Green

fn main(flag: bool) -> i32 {
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
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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

func TestLowerSliceIndexToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main(items: []i32) -> i32 {
    let n = items[1]
    return n
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"loadl %items",
		"call $ferret__bounds_check(l 1, l %_slice_len",
		"=l add %_slice_data",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerStringIndexToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main(s: str) -> char {
    return s[1]
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"call $ferret_global_str_index(l %s, l 1)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerMutableStringReassignmentToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    let mut greeting: str = "hello"
    greeting = "hé🙂"
    print(greeting)
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe mutable string reassignment: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"%greeting =l alloc8 16",
		"storel $__ferret_str",
		"storel 7,",
		"storel %greeting, %_t2",
		"storel 1, %_len_addr",
		"call $ferret_global_print(l %_t3)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerPrefixedIntegerLiteralToDecimalQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    return 0x10 + 0b10 + 0o7
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("lowerer: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower qbe prefixed integer literals: %v", err)
	}
	text := artifact.Text
	if !strings.Contains(text, "ret 25") {
		t.Fatalf("expected folded decimal return in qbe output:\n%s", text)
	}
}

func TestLowerMutableSliceElementWriteToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn bump(mut items: []i32) -> i32 {
    items[1] = 9
    return items[1]
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"call $ferret__bounds_check(l 1, l %_slice_len",
		"=l add %_slice_data",
		"storew 9,",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerLocalArrayLiteralAndIndexToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let arr: [3]i32 = [3]i32{1, 2, 3}
    let n = arr[1]
    return n
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"call $ferret__bounds_check(l 1, l 3)",
		"=l add %arr, 4",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerLocalArrayElementWriteToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let mut arr: [3]i32 = [3]i32{1, 2, 3}
    arr[1] = 9
    return arr[1]
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"call $ferret__bounds_check(l 1, l 3)",
		"=l add %arr, 4",
		"storew 9,",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerLocalTupleLiteralAndIndexToQBE(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let pair: (i32, bool, i32) = (1, true, 7)
    if !pair[1] {
        return 0
    }
    return pair[2]
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"storew 1, %pair",
		"storeb 1,",
		"=l add %pair, 8",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in qbe output:\n%s", want, text)
		}
	}
}

func TestLowerUnsupportedFunctionResultTypeReturnsError(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    return 1
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatal("expected MIR entry module")
	}
	var mainFn *mir.Function
	for _, fn := range result.Entry.MIR.Functions {
		if fn != nil && fn.Name == "main" {
			mainFn = fn
			break
		}
	}
	if mainFn == nil {
		t.Fatal("expected main function in MIR")
	}
	mainFn.Result = &typeinfo.TypeParam{Name: "T"}

	lowerer, err := registry.New(backend.TargetQBE)
	if err != nil {
		t.Fatalf("unexpected qbe error: %v", err)
	}
	_, err = lowerer.LowerModule(testUnit(result))
	if err == nil {
		t.Fatal("expected unsupported type lowering error")
	}
	if !strings.Contains(err.Error(), "unsupported qbe base type T") {
		t.Fatalf("expected qbe unsupported type error, got %v", err)
	}
}
