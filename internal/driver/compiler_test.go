package compiler

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	projectpkg "compiler/internal/core/project"
	"compiler/internal/frontend/ast"
	"compiler/internal/prelude"
)

func setIDEExecutablePaths(t *testing.T, root string) {
	t.Helper()
	execPath := filepath.Join(root, "bundle", "bin", "ferret")
	mustWrite(t, execPath, "")
	mustWrite(t, filepath.Join(root, "bundle", "libs", "global.fer"), ``)
	mustWrite(t, filepath.Join(root, "bundle", "libs", "std", "io.fer"), ``)
	oldProjectExecutablePath := projectpkg.ExecutablePath
	oldPreludeExecutablePath := prelude.ExecutablePath
	projectpkg.ExecutablePath = func() (string, error) { return execPath, nil }
	prelude.ExecutablePath = func() (string, error) { return execPath, nil }
	t.Cleanup(func() {
		projectpkg.ExecutablePath = oldProjectExecutablePath
		prelude.ExecutablePath = oldPreludeExecutablePath
	})
}

func TestParsePathResolvesDependencyAliasFromManifest(t *testing.T) {
	root := t.TempDir()
	depRoot := filepath.Join(root, "deps", "json")

	mustWrite(t, filepath.Join(root, "app", "fer.ret"), `[package]
name = "app"

[dependencies]
json = "../deps/json"
`)
	mustWrite(t, filepath.Join(root, "app", "main.fer"), `import "json/parser"

fn main() -> i32 {
    return parser::Value()
}
`)
	mustWrite(t, filepath.Join(depRoot, "fer.ret"), `[package]
name = "json"
`)
	mustWrite(t, filepath.Join(depRoot, "parser.fer"), `fn Value() -> i32 {
    return 1
}
`)

	result := ParsePath(filepath.Join(root, "app", "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", result.Diagnostics.Diagnostics())
	}
	if len(result.Modules) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(result.Modules))
	}
	if result.Modules[0].Key != "dependency:json/parser" || result.Modules[1].Key != "local:main" {
		t.Fatalf("unexpected modules: %#v", []string{result.Modules[0].Key, result.Modules[1].Key})
	}
}

func TestParseEntrySynthesizesMainForTests(t *testing.T) {
	root := t.TempDir()
	libsRoot := filepath.Join(root, "bundle", "core", "libs")
	mustWrite(t, filepath.Join(libsRoot, "global.fer"), ``)
	mustWrite(t, filepath.Join(root, "main.fer"), `
test "smoke" {
    let ok = true
    ok
}
`)

	cfg := context.Config{
		RootDir:         root,
		Extension:       ".fer",
		StdlibRoot:      filepath.Join(libsRoot, "std"),
		DependencyRoots: map[string]string{},
	}
	result := NewWithConfig(cfg, diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.AST == nil {
		t.Fatalf("expected parsed entry AST, got %#v", result.Entry)
	}

	foundTest := false
	foundMain := false
	for _, decl := range result.Entry.AST.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn == nil {
			continue
		}
		if fn.IsTest && fn.TestName == "smoke" {
			foundTest = true
		}
		if fn.IsSynthetic && fn.Name != nil && fn.Name.Text() == "main" {
			foundMain = true
		}
	}
	if !foundTest || !foundMain {
		t.Fatalf("expected synthesized test harness and test decl, got %#v", result.Entry.AST.Decls)
	}
}

func TestParseEntryTestModeOverridesUserMain(t *testing.T) {
	root := t.TempDir()
	libsRoot := filepath.Join(root, "bundle", "core", "libs")
	mustWrite(t, filepath.Join(libsRoot, "global.fer"), `
#[extern]
fn print(values: ...Any) -> void;
type Any interface {}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    print("app")
}

test "smoke" {
    let ok = true
    ok
}
`)

	cfg := context.Config{
		RootDir:         root,
		Extension:       ".fer",
		StdlibRoot:      filepath.Join(libsRoot, "std"),
		DependencyRoots: map[string]string{},
		TestMode:        true,
	}
	result := NewWithConfig(cfg, diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	userMain := 0
	synthMain := 0
	for _, decl := range result.Entry.AST.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn == nil || fn.Name == nil || fn.Name.Text() != "main" {
			continue
		}
		if fn.IsSynthetic {
			synthMain++
		} else {
			userMain++
		}
	}
	if userMain != 0 || synthMain != 1 {
		t.Fatalf("expected only one synthetic main in test mode, got user=%d synthetic=%d", userMain, synthMain)
	}
}

func TestParseEntryTestModeSelectsSingleTest(t *testing.T) {
	root := t.TempDir()
	libsRoot := filepath.Join(root, "bundle", "core", "libs")
	mustWrite(t, filepath.Join(libsRoot, "global.fer"), ``)
	mustWrite(t, filepath.Join(root, "main.fer"), `
test "smoke" {}

test "other" {}
`)

	cfg := context.Config{
		RootDir:         root,
		Extension:       ".fer",
		StdlibRoot:      filepath.Join(libsRoot, "std"),
		DependencyRoots: map[string]string{},
		TestMode:        true,
		TestName:        "__ferret_test_1",
	}
	result := NewWithConfig(cfg, diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	tests := 0
	for _, decl := range result.Entry.AST.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn == nil || !fn.IsTest {
			continue
		}
		tests++
	}
	if tests != 2 {
		t.Fatalf("expected original test decls to remain visible, got %d", tests)
	}
}

func TestParsePathResolvesStdlibWithoutManifest(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "io.fer"), `#[extern("ferret_io_println")]
fn Println(text: str) -> void;
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/io"

fn main() -> void {
    io::Println("hello")
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
	}
	if len(result.Modules) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(result.Modules))
	}
	foundMain := false
	foundStd := false
	for _, mod := range result.Modules {
		switch mod.Key {
		case "local:main":
			foundMain = true
		case "stdlib:std/io":
			foundStd = true
		}
	}
	if !foundMain || !foundStd {
		t.Fatalf("expected main and std/io modules, got %#v", []string{result.Modules[0].Key, result.Modules[1].Key})
	}
}

func TestParsePathTypechecksExternStdlibSignature(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "io.fer"), `#[extern("ferret_io_println")]
fn Println(text: str) -> void;
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/io"

fn main() -> void {
    io::Println(1)
}
`)
	result := ParsePath(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected stdlib signature type error")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected type mismatch diagnostic, got %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
	}
}

func TestParsePathTypechecksStdIOWrite(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "io.fer"), `
type Error error {
    unknown
}

type Writer interface {
    Write(&mut self, text: str) -> Error!usize
}

type Stream struct {
    kind: i32
}

#[extern("ferret_std_io_write_stream")]
fn write_stream(kind: i32, text: &str) -> usize;

let mut Stdout: Stream = .{ .kind = 1 }

fn WrapCount(value: usize) -> Error!usize {
    return value
}

fn Stream::Write(&mut self, text: str) -> Error!usize {
    return WrapCount(write_stream(self.kind, &text))
}

fn Write(mut dst: Writer, text: str) -> Error!usize {
    return dst.Write(text)
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/io"

fn main() -> void {
    _ = io::Write(io::Stdout, "hello") catch |err| {
        print(err)
        return
    }
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
	}
}

func TestParsePathTypechecksStdFSWrite(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "io.fer"), `
type Error error {
    unknown
}

type Writer interface {
    Write(&mut self, text: str) -> Error!usize
}

fn WrapCount(value: usize) -> Error!usize {
    return value
}

fn Write(mut dst: Writer, text: str) -> Error!usize {
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
import "std/io"

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

fn File::Write(&mut self, text: str) -> io::Error!usize {
    unsafe {
        return io::WrapCount(write_raw(mem::ExposeRef(&self.inner) as ^void, &text))
    }
}

fn File::Close(self) -> void {
    unsafe {
        close_raw(mem::Expose(self.inner) as ^void)
    }
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/io"
import "std/fs"

fn main() -> void {
    let mut file = fs::Open("out.txt")
    _ = io::Write(file, "hello") catch |err| {
        print(err)
        return
    }
    file.Close()
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
	}
}

func TestParsePathTypechecksStdStringWriteMethod(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "string.fer"), `
type String struct {
    len: usize = 0
}

fn New() -> String {
    return .{}
}

fn String::Write(&mut self, text: str) -> usize {
    let bytes = text as []u8
    let count = len(&bytes)
    self.len = self.len + count
    return count
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/string"

fn main() -> void {
    let mut buf = string::New()
    _ = buf.Write("hello")
    _ = buf.Write(" world")
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
	}
}

func TestParsePathTypechecksStdIOBuffer(t *testing.T) {
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

type Error error {
    unknown
}

type Writer interface {
    Write(&mut self, text: str) -> Error!usize
}

type Reader interface {
    Read(&mut self, size: usize) -> Error![]u8
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

fn WrapCount(value: usize) -> Error!usize {
    return value
}

fn WrapBytes(value: []u8) -> Error![]u8 {
    return value
}

fn NewBuffer() -> Buffer {
    unsafe {
        return .{
            .inner = mem::Adopt(new_buffer_raw())
        }
    }
}

fn Buffer::Write(&mut self, text: str) -> Error!usize {
    unsafe {
        return WrapCount(write_buffer_raw(mem::ExposeRef(&self.inner) as ^void, &text))
    }
}

fn Buffer::Read(&mut self, size: usize) -> Error![]u8 {
    unsafe {
        return WrapBytes(read_buffer_raw(mem::ExposeRef(&self.inner) as ^void, size))
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

fn Write(mut dst: Writer, text: str) -> Error!usize {
    return dst.Write(text)
}

fn Read(mut src: Reader, size: usize) -> Error![]u8 {
    return src.Read(size)
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/io"

fn main() -> void {
    let mut buf = io::NewBuffer()
    _ = io::Write(buf, "hello") catch |err| {
        print(err)
        return
    }
    let head = io::Read(buf, 2) catch |err| {
        print(err)
        return
    }
    _ = head
    _ = buf.AsStr()
    buf.Release()
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
	}
}

func TestParsePathTypechecksStdNetTCP(t *testing.T) {
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
    Write(&mut self, text: str) -> Error!usize
}

type Reader interface {
    Read(&mut self, size: usize) -> Error![]u8
}

fn LastError() -> Error {
    return Error::unknown
}

fn WrapCount(value: usize) -> Error!usize {
    return value
}

fn WrapBytes(value: []u8) -> Error![]u8 {
    return value
}

fn Write(mut dst: Writer, text: str) -> Error!usize {
    return dst.Write(text)
}

fn Read(mut src: Reader, size: usize) -> Error![]u8 {
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

fn Conn::Write(&mut self, text: str) -> io::Error!usize {
    unsafe {
        return io::WrapCount(write_raw(mem::ExposeRef(&self.inner) as ^void, &text))
    }
}

fn Conn::Read(&mut self, size: usize) -> io::Error![]u8 {
    unsafe {
        return io::WrapBytes(read_raw(mem::ExposeRef(&self.inner) as ^void, size))
    }
}

fn Conn::Close(self) -> void {
    unsafe {
        close_raw(mem::Expose(self.inner) as ^void)
    }
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/io"
import "std/net/tcp"

fn main() -> void {
    let mut conn = tcp::Dial("127.0.0.1", 8080) catch |err| {
        print(err)
        return
    }
    _ = io::Write(conn, "ping") catch |err| {
        print(err)
        return
    }
    _ = io::Read(conn, 4) catch |err| {
        print(err)
        return
    }
    conn.Close()
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
	}
}

func TestParsePathTypechecksStdNetTCPListener(t *testing.T) {
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
    Write(&mut self, text: str) -> Error!usize
}

type Reader interface {
    Read(&mut self, size: usize) -> Error![]u8
}

fn LastError() -> Error {
    return Error::unknown
}

fn WrapCount(value: usize) -> Error!usize {
    return value
}

fn WrapBytes(value: []u8) -> Error![]u8 {
    return value
}

fn Write(mut dst: Writer, text: str) -> Error!usize {
    return dst.Write(text)
}

fn Read(mut src: Reader, size: usize) -> Error![]u8 {
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

fn Conn::Write(&mut self, text: str) -> io::Error!usize {
    unsafe {
        return io::WrapCount(write_raw(mem::ExposeRef(&self.inner) as ^void, &text))
    }
}

fn Conn::Read(&mut self, size: usize) -> io::Error![]u8 {
    unsafe {
        return io::WrapBytes(read_raw(mem::ExposeRef(&self.inner) as ^void, size))
    }
}

fn Conn::Close(self) -> void {
    unsafe {
        close_raw(mem::Expose(self.inner) as ^void)
    }
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/io"
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
    _ = io::Read(conn, 4) catch |err| {
        print(err)
        conn.Close()
        listener.Close()
        return
    }
    _ = io::Write(conn, "pong") catch |err| {
        print(err)
        conn.Close()
        listener.Close()
        return
    }
    conn.Close()
    listener.Close()
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
	}
}

func TestParsePathForIDEReportsUnusedLocalDiagnostics(t *testing.T) {
	root := t.TempDir()
	setIDEExecutablePaths(t, root)
	mustWrite(t, filepath.Join(root, "main.fer"), `fn main() -> i32 {
    let dead = 1
    return 0
}
`)

	result := ParsePathForIDE(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		got := make([]string, 0, len(result.Diagnostics.Diagnostics()))
		for _, diag := range result.Diagnostics.Diagnostics() {
			if diag == nil {
				continue
			}
			got = append(got, diag.Code+": "+diag.Message)
		}
		t.Fatalf("unexpected diagnostics: %v", got)
	}

	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.WarnUnusedLocal {
			found = true
			break
		}
	}
	if !found {
		got := make([]string, 0, len(result.Diagnostics.Diagnostics()))
		for _, diag := range result.Diagnostics.Diagnostics() {
			if diag == nil {
				continue
			}
			got = append(got, diag.Code+": "+diag.Message)
		}
		t.Fatalf("expected %s diagnostic, got %v", diagnostics.WarnUnusedLocal, got)
	}
}

func TestParsePathRejectsNonCanonicalRecursiveGenericSelfUseBeforeLowering(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Node<T> struct {
    Next: ?*Node<Node<T>>
    Value: T
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected recursive generic self-use diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag != nil && diag.Code == diagnostics.ErrInvalidType && strings.Contains(diag.Message, "must preserve declaration type parameters") {
			found = true
		}
		if diag != nil && strings.Contains(diag.Message, "does not converge") {
			t.Fatalf("expected no specialization diagnostic, got %#v", result.Diagnostics.Diagnostics())
		}
	}
	if !found {
		t.Fatalf("expected canonical generic self-use diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry != nil && result.Entry.LoweredHIR != nil {
		t.Fatalf("expected no lowered HIR on semantic error, got %#v", result.Entry.LoweredHIR)
	}
}

func TestParsePathForIDERejectsRuntimeConstInitializer(t *testing.T) {
	root := t.TempDir()
	setIDEExecutablePaths(t, root)
	mustWrite(t, filepath.Join(root, "main.fer"), `#[extern("clock")]
fn clock() -> i32;

fn main() -> i32 {
    const y = clock()
    return y
}
`)

	result := ParsePathForIDE(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected const initializer diagnostic in IDE mode")
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag != nil && diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, "constant initializer must be compile-time evaluable") {
			return
		}
	}
	t.Fatalf("expected const initializer diagnostic, got %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
}

func TestParsePathForIDEAllowsPotentialCTFEConstCall(t *testing.T) {
	root := t.TempDir()
	setIDEExecutablePaths(t, root)
	mustWrite(t, filepath.Join(root, "main.fer"), `fn add(a: i32, b: i32) -> i32 {
    return a + b
}

fn main() -> i32 {
    const y = add(1, 2)
    return y
}
`)

	result := ParsePathForIDE(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("expected potential CTFE const call to remain valid in IDE mode, got %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
	}
}

func TestParsePathResolvesStdlibOSWithoutManifest(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "global.fer"), `
type Any interface {}
#[extern("ferret_io_print")]
fn print(values: ...Any) -> void;
`)
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "os.fer"), `#[extern("ferret_os_cpu_count")]
fn CPUCount() -> usize;

#[extern("ferret_os_platform")]
fn Platform() -> str;

#[extern("ferret_os_debug")]
fn Debug() -> bool;
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/os"

fn main() -> void {
    if os::CPUCount() > 0 {
        print(os::Platform())
    }
    if os::Debug() {
        print("debug")
    }
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
	}
	if len(result.Modules) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(result.Modules))
	}
	foundMain := false
	foundStd := false
	for _, mod := range result.Modules {
		switch mod.Key {
		case "local:main":
			foundMain = true
		case "stdlib:std/os":
			foundStd = true
		}
	}
	if !foundMain || !foundStd {
		t.Fatalf("expected main and std/os modules, got %#v", []string{result.Modules[0].Key, result.Modules[1].Key})
	}
}

func TestParsePathTypechecksStdlibOSSignature(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "global.fer"), `
type Any interface {}
#[extern("ferret_io_print")]
fn print(values: ...Any) -> void;
`)
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "os.fer"), `#[extern("ferret_os_cpu_count")]
fn CPUCount() -> usize;
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/os"

fn main() -> void {
    os::CPUCount(1)
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected stdlib signature type error")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrWrongArgumentCount {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected wrong arg count diagnostic, got %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
	}
}

func TestParsePathResolvesStdlibMemWithoutManifest(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "global.fer"), `
#[extern("malloc")]
fn malloc(size: usize) -> ^void;
#[extern("free")]
fn free(ptr: ^void) -> void;
`)
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "mem.fer"), `
type Allocator interface {
    Alloc(&self, size: usize) -> ^void
    Free(&self, ptr: ^void) -> void
}

type CAllocator struct {}

fn CAllocator::Alloc(&self, size: usize) -> ^void {
    return malloc(size)
}

fn CAllocator::Free(&self, ptr: ^void) -> void {
    free(ptr)
}

fn System() -> CAllocator {
    return .{}
}

fn Alloc(a: Allocator, size: usize) -> ^void {
    return a.Alloc(size)
}

fn Free(a: Allocator, ptr: ^void) -> void {
    a.Free(ptr)
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/mem"

fn main() -> void {
    let a = mem::System()
    let p = mem::Alloc(a, 16)
    mem::Free(a, p)
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
	}
	if len(result.Modules) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(result.Modules))
	}
	foundMain := false
	foundStd := false
	for _, mod := range result.Modules {
		switch mod.Key {
		case "local:main":
			foundMain = true
		case "stdlib:std/mem":
			foundStd = true
		}
	}
	if !foundMain || !foundStd {
		t.Fatalf("expected main and std/mem modules, got %#v", []string{result.Modules[0].Key, result.Modules[1].Key})
	}
}

func TestParsePathTypechecksStdlibMemSignature(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "global.fer"), `
#[extern("malloc")]
fn malloc(size: usize) -> ^void;
#[extern("free")]
fn free(ptr: ^void) -> void;
`)
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "mem.fer"), `
type Allocator interface {
    Alloc(&self, size: usize) -> ^void
}

type CAllocator struct {}

fn CAllocator::Alloc(&self, size: usize) -> ^void {
    return malloc(size)
}

fn System() -> CAllocator {
    return .{}
}

fn Alloc(a: Allocator, size: usize) -> ^void {
    return a.Alloc(size)
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/mem"

fn main() -> void {
    let a = mem::System()
    mem::Alloc(a, "bad")
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected stdlib mem signature type error")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected type mismatch diagnostic, got %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
	}
}

func TestIfAttributeFiltersTopLevelDeclarations(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
#[if(target_os, "linux")]
fn LinuxOnly() -> i32 { return 1 }

#[if(target_os, "windows")]
fn WindowsOnly() -> i32 { return 2 }

fn main() -> i32 {
    return LinuxOnly()
}
`)

	cfg := context.Config{
		RootDir:         root,
		Extension:       ".fer",
		DependencyRoots: map[string]string{},
		TargetOS:        "linux",
		TargetArch:      runtime.GOARCH,
	}
	result := NewWithConfig(cfg, diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if len(result.Entry.AST.Decls) != 2 {
		t.Fatalf("expected 2 active declarations after filtering, got %d", len(result.Entry.AST.Decls))
	}
}

func TestIfAttributeSupportsNegatedTargetSelection(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
#[if(target_os, "linux")]
const PlatformTag = 1

#[ifnot(target_os, "linux")]
const PlatformTag = 2

fn main() -> i32 {
    return PlatformTag
}
`)

	cfg := context.Config{
		RootDir:         root,
		Extension:       ".fer",
		DependencyRoots: map[string]string{},
		TargetOS:        "linux",
		TargetArch:      runtime.GOARCH,
	}
	result := NewWithConfig(cfg, diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if len(result.Entry.AST.Decls) != 2 {
		t.Fatalf("expected 2 active declarations after negated filtering, got %d", len(result.Entry.AST.Decls))
	}
}

func TestIfAttributeSupportsBackendSelection(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
#[if(target_backend, "llvm")]
const BackendTag = 1

#[ifnot(target_backend, "llvm")]
const BackendTag = 2

fn main() -> i32 {
    return BackendTag
}
`)

	cfg := context.Config{
		RootDir:         root,
		Extension:       ".fer",
		DependencyRoots: map[string]string{},
		TargetBackend:   "llvm",
	}
	result := NewWithConfig(cfg, diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if len(result.Entry.AST.Decls) != 2 {
		t.Fatalf("expected 2 active declarations after backend filtering, got %d", len(result.Entry.AST.Decls))
	}
}

func TestIfAttributeInvalidFormReportsDiagnostic(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
#[if(target_os, "linux", extra)]
fn main() -> i32 { return 0 }
`)

	cfg := context.Config{
		RootDir:         root,
		Extension:       ".fer",
		DependencyRoots: map[string]string{},
	}
	result := NewWithConfig(cfg, diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected invalid #[if(...)] diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidOperation && diag.Message == "invalid #[if(...)] attribute" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected invalid #[if(...)] diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestArbitraryWidthIntegersRequireLLVMBackend(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> i128 {
    let value: i128 = 1
    return value
}
`)

	cfg := context.Config{
		RootDir:         root,
		Extension:       ".fer",
		DependencyRoots: map[string]string{},
		TargetBackend:   "qbe",
	}
	result := NewWithConfig(cfg, diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected arbitrary-width integer backend diagnostic")
	}

	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, "only supported on the llvm backend") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected llvm-only arbitrary-width integer diagnostic, got %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func diagnosticSummaries(diags []*diagnostics.Diagnostic) []string {
	out := make([]string, 0, len(diags))
	for _, diag := range diags {
		if diag == nil {
			continue
		}
		out = append(out, diag.Code+": "+diag.Message)
	}
	return out
}
