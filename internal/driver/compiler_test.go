package compiler

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	"compiler/internal/frontend/ast"
)

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
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/io"

fn main() -> void {
    io::Write(io::Stdout, "hello")
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
	}
	if len(result.Modules) != 3 {
		t.Fatalf("expected 3 modules, got %d", len(result.Modules))
	}
	foundMain := false
	foundStd := false
	foundMem := false
	for _, mod := range result.Modules {
		switch mod.Key {
		case "local:main":
			foundMain = true
		case "stdlib:std/io":
			foundStd = true
		case "stdlib:std/mem":
			foundMem = true
		}
	}
	if !foundMain || !foundStd || !foundMem {
		t.Fatalf("expected main and std/io modules, got %#v", []string{result.Modules[0].Key, result.Modules[1].Key})
	}
}

func TestParsePathTypechecksStdIOWrite(t *testing.T) {
	root := t.TempDir()
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
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/io"
import "std/net/tcp"

fn main() -> void {
    let mut conn = tcp::Dial("127.0.0.1", 8080) catch |err| {
        print(err)
        return
    }
    _ = conn.SetReadTimeoutMs(100) catch |err| {
        print(err)
        return
    }
    _ = conn.SetWriteTimeoutMs(100) catch |err| {
        print(err)
        return
    }
    _ = conn.SetNoDelay(true) catch |err| {
        print(err)
        return
    }
    _ = conn.SetKeepAlive(true) catch |err| {
        print(err)
        return
    }
    _ = conn.LocalAddr() catch |err| {
        print(err)
        return
    }
    _ = conn.PeerAddr() catch |err| {
        print(err)
        return
    }
    _ = io::Write(conn, "ping") catch |err| {
        print(err)
        return
    }
    _ = conn.ShutdownWrite() catch |err| {
        print(err)
        return
    }
    _ = io::Read(conn, 4) catch |err| {
        print(err)
        return
    }
    _ = conn.ShutdownRead() catch |err| {
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
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/io"
import "std/net/tcp"

fn main() -> void {
    let mut listener = tcp::Listen("127.0.0.1", 8080) catch |err| {
        print(err)
        return
    }
    _ = listener.LocalAddr() catch |err| {
        print(err)
        listener.Close()
        return
    }
    _ = listener.SetAcceptTimeoutMs(100) catch |err| {
        print(err)
        listener.Close()
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

func TestParsePathTypechecksStdNetHTTP(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
import "std/net/http"

fn main() -> void {
    let resp = http::Get("127.0.0.1", 8080, "/") catch |err| {
        print(err)
        return
    }
    print(resp.StatusCode())
    print(resp.Body())
    resp.Release()
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
	}
}

func TestParsePathTypechecksStdNetHTTPServer(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
import "std/io"
import "std/net/http"
import "std/net/tcp"

fn main() -> void {
    let mut listener = tcp::Listen("127.0.0.1", 8080) catch |err| {
        print(err)
        return
    }
    defer listener.Close()

    let mut ctx = http::ServeOnce(&mut listener) catch |err| {
        print(err)
        return
    }
    defer ctx.Release()

    _ = http::WriteTextResponse(&mut ctx.Conn, 200, "hello-server") catch |err| {
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

func TestParsePathTypechecksStdNetHTTPServerRoutes(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
import "std/io"
import "std/net/http"

fn hello(req: &http::Request, res: &mut http::ResponseWriter) -> io::Error!usize {
    req
    return res.Send(200, "hello-route")
}

fn main() -> void {
    let mut server = http::NewServer()
    server.Listen(8080)
    server.Get("/hello", hello)
    _ = server.StartOnce() catch |err| {
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

func TestParsePathForIDEReportsUnusedLocalDiagnostics(t *testing.T) {
	root := t.TempDir()
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

func TestParsePathWithOverlayForIDEAcceptsNonSourceExtension(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "main.fer")
	overlayPath := filepath.Join(root, "overlay.tmp")
	mustWrite(t, sourcePath, `fn main() -> void {
}
`)
	mustWrite(t, overlayPath, `fn main() -> void {
    let overlay_value = 1
}
`)

	rejected := ParsePathForIDE(overlayPath)
	if !rejected.Diagnostics.HasErrors() {
		t.Fatalf("expected normal IDE parse to reject non-source extension")
	}

	result := ParsePathWithOverlay(sourcePath, overlayPath, true)
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected overlay diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.FilePath != filepath.Clean(sourcePath) {
		t.Fatalf("expected source entry %q, got %#v", filepath.Clean(sourcePath), result.Entry)
	}
}

func TestParsePathWithOverlayReportsOverlayReadPath(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "main.fer")
	overlayPath := filepath.Join(root, "missing-overlay.tmp")
	mustWrite(t, sourcePath, `fn main() -> void {
}
`)

	result := ParsePathWithOverlay(sourcePath, overlayPath, true)
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected missing overlay diagnostic")
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag != nil && strings.Contains(diag.Message, "cannot read overlay file "+overlayPath) {
			return
		}
	}
	t.Fatalf("expected overlay path diagnostic, got %#v", result.Diagnostics.Diagnostics())
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

func TestParsePathParsesFunctionTypeParameterWithoutReturnAsVoid(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn noop(a: i32, b: i32) {}

fn takefn(fun: fn(i32, i32)) -> void {
    fun(1, 2)
}

fn main() -> void {
    takefn(noop)
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
	}
}

func TestParsePathTypechecksFunctionValueParameterCall(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn inc(x: i32) -> i32 {
    return x + 1
}

fn apply(f: fn(i32) -> i32, x: i32) -> i32 {
    return f(x)
}

fn main() -> i32 {
    return apply(inc, 2)
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
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
