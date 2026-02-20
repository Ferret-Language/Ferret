package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	//"compiler/internal/stdlib"
)

func TestCompile_InMemorySimpleCode(t *testing.T) {
	// Note: In-memory compilation currently fails in non-WASM builds due to missing
	// global prelude builtins. This test demonstrates the correct API usage.
	// The global prelude is only loaded when building with WASM tags.

	t.Run("UsingFilesFieldSingleFile", func(t *testing.T) {
		opts := &Options{
			Files: map[string]string{
				"main.fer": "let x := 10;",
			},
			Debug:         false,
			LogFormat:     ANSI,
			TypecheckOnly: true,
		}

		result := Compile(opts)

		if !result.Success {
			t.Errorf("Expected successful compilation, got failure: %s", result.Output)
		}
	})

	t.Run("UsingFilesFieldMultiFile", func(t *testing.T) {
		opts := &Options{
			Files: map[string]string{
				"main.fer": "let x := 10;",
			},
			Debug:          false,
			LogFormat:      ANSI,
			TypecheckOnly:  true,
			CodegenBackend: "qbe",
		}

		result := Compile(opts)

		if !result.Success {
			t.Errorf("Expected successful compilation, got failure: %s", result.Output)
		}
	})
}

// TestCompile_InMemoryMultiFileDemo shows how to use Files field for multi-file compilation
func TestCompile_InMemoryMultiFileDemo(t *testing.T) {
	// This demonstrates the correct way to use the Files field for multi-file in-memory compilation
	// This is particularly useful for the web playground where files exist only in memory

	opts := &Options{
		Files: map[string]string{
			"main.fer":  "import \"playground/utils\";\nlet x := utils::PI * 2.0;",
			"utils.fer": "let PI := 3.14159;",
		},
		Debug:          false,
		LogFormat:      ANSI,
		TypecheckOnly:  true,
		CodegenBackend: "qbe",
	}

	result := Compile(opts)

	if !result.Success {
		t.Errorf("Expected successful compilation, got failure")
	}
}

func TestCompile_InMemoryWithSyntaxError(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": "let x := ;", // Missing value
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	if result.Success {
		t.Error("Expected compilation failure for syntax error")
	}
}

func TestCompile_InMemoryMultipleStatements(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `let x := 42;
let y := 100;
let z := x + y;`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	if !result.Success {
		t.Errorf("Expected successful compilation of multiple statements, got failure: %s", result.Output)
	}
}

func TestCompile_HTMLFormat(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": "let x := 42;",
		},
		Debug:         false,
		LogFormat:     HTML,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	if !result.Success {
		t.Errorf("Expected successful compilation, got failure: %s", result.Output)
	}

	// HTML format may or may not produce output for successful compilation
	// (depends on whether there are warnings)
	// Just verify it doesn't crash
}

func TestCompile_HTMLFormatWithError(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": "let x := ;", // Syntax error
		},
		Debug:         false,
		LogFormat:     HTML,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	if result.Success {
		t.Error("Expected compilation failure")
	}

	// Should have HTML error output
	if result.Output == "" {
		t.Error("Expected error output in HTML format")
	}

	// Output should contain some HTML markers (from ANSI conversion)
	if !strings.Contains(result.Output, "<") {
		t.Error("Expected HTML tags in error output")
	}
}

func TestCompile_FileMode_NonExistentFile(t *testing.T) {
	opts := &Options{
		EntryFile:     "/nonexistent/path/to/file.fer",
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	if result.Success {
		t.Error("Expected failure for non-existent file")
	}
}

func TestCompile_DebugMode(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": "let x := 42;",
		},
		Debug:         true, // Enable debug mode
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	if !result.Success {
		t.Errorf("Expected successful compilation in debug mode, got failure: %s", result.Output)
	}
}

func TestCompile_EmptyCode(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": " ", // Whitespace-only (truly empty causes entry point error)
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	// Empty/whitespace-only code should compile successfully (empty module)
	if !result.Success {
		t.Errorf("Expected successful compilation of empty code, got failure: %s", result.Output)
	}
}

func TestCompile_ComplexExpression(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `let a := 10;
let b := 20;
let c := (a + b) * 2 - 5;`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	if !result.Success {
		t.Errorf("Expected successful compilation of complex expressions, got failure: %s", result.Output)
	}
}

func TestCompile_FunctionDeclaration(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `fn add(x: i32, y: i32) -> i32 {
	return x + y;
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	if !result.Success {
		t.Errorf("Expected successful compilation of function declaration, got failure: %s", result.Output)
	}
}

func TestCompile_FileMode_WithSyntaxError(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.fer")

	// Invalid syntax - missing value
	content := "let x := ;"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	opts := &Options{
		EntryFile:     testFile,
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	if result.Success {
		t.Error("Expected compilation failure for syntax error in file")
	}
}

func TestCompile_ResultStructure(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": "let x := 42;",
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	// Verify Result has expected fields
	if !result.Success {
		t.Errorf("Expected successful compilation, got failure: %s", result.Output)
	}

	// ANSI format should not populate Output for success
	// Only HTML format and errors populate Output
}

func TestCompile_ANSIFormat(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": "let x := 42;",
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	if !result.Success {
		t.Errorf("Expected successful compilation, got failure: %s", result.Output)
	}

	// ANSI format with success should not populate output field
	// (diagnostics go to stderr)
	if result.Output != "" {
		t.Logf("Note: ANSI format produced output: %q", result.Output)
	}
}

func TestCompile_ImportOrderValidation(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `let x := 42;
import "std/io";`, // Import after declaration - should error
		},
		Debug:         false,
		LogFormat:     HTML,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	if result.Success {
		t.Error("Expected compilation failure for import after declaration")
	}

	if !strings.Contains(result.Output, "import") {
		t.Error("Expected error message about import ordering")
	}
}

func TestCompile_MultipleImports(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `import "std/io";
import "std/math";
let x := 42;`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	if !result.Success {
		t.Errorf("Expected successful compilation of multiple statements, got failure: %s", result.Output)
	}
}

func TestCompile_ConstraintDeclarations(t *testing.T) {
	good := &Options{
		Files: map[string]string{
			"main.fer": `constraint numeric = union {
	i32, i64
};

constraint signed_like = union {
	~i32, ~i64
};

constraint writer = interface {
	Write(buf: []byte) -> i32
};

constraint numeric_writer = numeric & writer;

fn main() {}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	resGood := Compile(good)
	if !resGood.Success {
		t.Fatalf("expected successful compilation for valid constraints, got: %s", resGood.Output)
	}

	bad := &Options{
		Files: map[string]string{
			"main.fer": `constraint invalid = ~numeric;
constraint numeric = union { i32, i64 };

fn main() {}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	resBad := Compile(bad)
	if resBad.Success {
		t.Fatalf("expected compilation failure for invalid constraint '~' usage")
	}
}

func TestCompile_GenericFunctionInferenceAndExplicitTypeArgs(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `constraint numeric = union { i32, i64 };

fn add<T: numeric>(a: T, b: T) -> T {
	return a + b;
}

fn main() -> i32 {
	let inferred := add(1, 2);
	let explicit := add<i64>(1 as i64, 2 as i64);
	return inferred;
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected successful generic call inference/type-arg checking, got: %s", result.Output)
	}
}

func TestCompile_GenericFunctionConstraintViolation(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `constraint numeric = union { i32, i64 };

fn add<T: numeric>(a: T, b: T) -> T {
	return a;
}

fn main() {
	let bad := add("a", "b");
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if result.Success {
		t.Fatalf("expected constraint failure for generic call with str arguments")
	}
}

func TestCompile_GenericNamedTypeInstantiation(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `type Pair<T, U> struct {
	.A: T,
	.B: U
};

fn sumPair(p: Pair<i32, i32>) -> i32 {
	return p.A + p.B;
}

fn main() -> i32 {
	let p := { .A = 10, .B = 20 } as Pair<i32, i32>;
	return sumPair(p);
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected successful generic named type instantiation, got: %s", result.Output)
	}
}

func TestCompile_GenericNamedTypeNominalMismatch(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `type Pair<T, U> struct {
	.A: T,
	.B: U
};

fn main() {
	let a := { .A = 1, .B = 2 } as Pair<i32, i32>;
	let b := { .A = 1 as i64, .B = 2 as i64 } as Pair<i64, i64>;
	a = b;
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if result.Success {
		t.Fatalf("expected type mismatch between different generic named type instantiations")
	}
}

func TestCompile_GenericMethodInferenceAndExplicitTypeArgs(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `type Box struct {
	.Value: i32
};

fn (b: Box) id<T>(x: T) -> T {
	return x;
}

fn main() -> i32 {
	let b := { .Value = 1 } as Box;
	let inferred := b.id(10);
	let explicit := b.id<i64>(20 as i64);
	return inferred + (explicit as i32);
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected successful generic method inference/type-arg checking, got: %s", result.Output)
	}
}

func TestCompile_GenericMethodConstraintViolation(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `constraint numeric = union { i32, i64 };

type Box struct {
	.Value: i32
};

fn (b: Box) add<T: numeric>(x: T, y: T) -> T {
	return x + y;
}

fn main() {
	let b := { .Value = 1 } as Box;
	let bad := b.add("a", "b");
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if result.Success {
		t.Fatalf("expected constraint failure for generic method call with str arguments")
	}
}

func TestCompile_GenericReceiverMethodSpecializationAndTemplateCoexist(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `type Box<T> struct {
	.Value: T
};

fn (b: Box<T>) tag() -> i32 {
	return 1;
}

fn (b: Box<i32>) tag() -> i64 {
	return 2;
}

fn main() -> i32 {
	let bi := { .Value = 7 } as Box<i32>;
	let x: i64 = bi.tag();
	let bs := { .Value = 9 as i64 } as Box<i64>;
	let y: i32 = bs.tag();
	return (x as i32) + y;
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected specialization + template method resolution to type-check, got: %s", result.Output)
	}
}

func TestCompile_GenericReceiverMethodSpecializationDuplicateRejected(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `type Box<T> struct {
	.Value: T
};

fn (b: Box<i32>) tag() -> i32 {
	return 1;
}

fn (b: Box<i32>) tag() -> i32 {
	return 2;
}

fn main() -> i32 { return 0; }`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if result.Success {
		t.Fatalf("expected duplicate receiver specialization to fail")
	}
}

func TestCompile_GenericReceiverMethodRequiresExplicitReceiverArgs(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `type Box<T> struct {
	.Value: T
};

fn (b: Box) get() -> i32 {
	return 0;
}

fn main() -> i32 { return 0; }`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if result.Success {
		t.Fatalf("expected method receiver on generic type without type arguments to fail")
	}
}

func TestCompile_GenericReceiverMethodRejectsMixedPattern(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `type Pair<T, U> struct {
	.A: T,
	.B: U
};

fn (p: Pair<T, i32>) bad() -> i32 {
	return 0;
}

fn main() -> i32 { return 0; }`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if result.Success {
		t.Fatalf("expected mixed generic receiver pattern to fail")
	}
}

func TestCompile_GenericReceiverReferenceTemplateTypechecks(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `type Box<T> struct {
	.Value: T
};

fn (b: &Box<T>) one() -> i32 {
	return 1;
}

fn main() -> i32 {
	let b := { .Value = 5 } as Box<i32>;
	return b.one();
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected generic reference receiver method to type-check, got: %s", result.Output)
	}
}

func TestCompile_GenericFunctionInterfaceConstraintSatisfied(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `constraint writer = interface {
	Write(buf: []byte) -> i32
};

type File struct {
	.Id: i32
};

fn (f: File) Write(buf: []byte) -> i32 {
	return f.Id;
}

fn useWriter<T: writer>(w: T) -> i32 {
	return 0;
}

fn main() -> i32 {
	let f := { .Id = 7 } as File;
	return useWriter(f);
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected interface constraint satisfaction for generic call, got: %s", result.Output)
	}
}

func TestCompile_GenericFunctionConstraintRejectsImplicitNumericConversion(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `constraint numeric = union { i32, i64 };

fn id<T: numeric>(x: T) -> T {
	return x;
}

fn main() -> i32 {
	let v := id(1 as i8);
	return v as i32;
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if result.Success {
		t.Fatalf("expected constraint failure when type is not explicitly in union type-set")
	}
}

func TestCompile_GenericEnumTypeParamsWarn(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `type Tag<T> enum {
	A, B
};

fn main() -> i32 {
	return 0;
}`,
		},
		Debug:         false,
		LogFormat:     HTML,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected successful compilation for generic enum declaration, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "W0006") {
		t.Fatalf("expected warning code W0006 for generic enum type params, got: %s", result.Output)
	}
}

func TestCompile_ResourceImplicitMoveAllowed(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `import "std/fs";

fn main() {
	let f := fs::CreateRW("/tmp/resource_move_implicit_ok.txt") catch err {
		return;
	};
	let g := f;
	g.Close();
}`,
		},
		Debug:         false,
		LogFormat:     HTML,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected compilation success for implicit resource move, got: %s", result.Output)
	}
}

func TestCompile_ResourceMoveAllowed(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `import "std/fs";

fn main() {
	let f := fs::CreateRW("/tmp/resource_move_ok.txt") catch err {
		return;
	};
	let g := f;
	g.Close();
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected compilation success for explicit resource move, got: %s", result.Output)
	}
}

func TestCompile_NetResourceImplicitMoveAllowed(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `import "net/tcp";

fn main() {
	let listener := tcp::ListenTcp("127.0.0.1:0") catch err {
		return;
	};
	let alias := listener;
	alias.Close();
}`,
		},
		Debug:         false,
		LogFormat:     HTML,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected compilation success for implicit tcp resource move, got: %s", result.Output)
	}
}

func TestCompile_UserCopyableStructImplicitCopyAllowed(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `type Vec2 struct {
	.x: i32,
	.y: i32
};

fn main() {
	let a: Vec2 = { .x = 1, .y = 2 };
	let b := a;
}`,
		},
		Debug:         false,
		LogFormat:     HTML,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected compilation success for implicit copy of copyable struct, got: %s", result.Output)
	}
}

func TestCompile_UserTypeImplicitCopyWithCopyMethodAllowed(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `type Vec2 struct {
	.x: i32,
	.y: i32
};

fn (v: &Vec2) copy() -> Vec2 {
	return { .x = v.x, .y = v.y } as Vec2;
}

fn main() {
	let a: Vec2 = { .x = 1, .y = 2 };
	let b := a;
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected compilation success with valid copy() method, got: %s", result.Output)
	}
}

func TestCompile_InterfaceReceiverModePrefixes(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `type MutWriter interface {
	&mut Write(v: i32) -> i32
};

type AnyWriter interface {
	~Write(v: i32) -> i32
};

type S struct {
	.x: i32
};

fn (s: &S) copy() -> S {
	return { .x = s.x } as S;
}

fn (s: &mut S) Write(v: i32) -> i32 {
	return v;
}

fn main() {
	let s: S = { .x = 0 };
	let a: MutWriter = s;
	let b: AnyWriter = s;
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected compilation success for &mut/~ receiver mode interface methods, got: %s", result.Output)
	}
}

func TestCompile_InterfaceValueReceiverRequirementRejected(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `type ValueWriter interface {
	Write(v: i32) -> i32
};

type S struct {
	.x: i32
};

fn (s: &S) copy() -> S {
	return { .x = s.x } as S;
}

fn (s: &mut S) Write(v: i32) -> i32 {
	return v;
}

fn main() {
	let s: S = { .x = 0 };
	let w: ValueWriter = s;
}`,
		},
		Debug:         false,
		LogFormat:     HTML,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if result.Success {
		t.Fatalf("expected compilation failure for value receiver interface requirement")
	}
	if !strings.Contains(result.Output, "receiver mismatch") && !strings.Contains(result.Output, "missing methods") {
		t.Fatalf("expected receiver mismatch diagnostic, got: %s", result.Output)
	}
}

func TestCompile_HttpServeResourceImplicitMoveAllowed(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `import "net/http";
import "net/tcp";

fn main() {
	let app := http::Server();
	let listener := tcp::ListenTcp("127.0.0.1:0") catch err {
		return;
	};
	app.Serve(listener) catch err {
		return;
	};
}`,
		},
		Debug:         false,
		LogFormat:     HTML,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected compilation success when passing listener with implicit move, got: %s", result.Output)
	}
}

func TestCompile_HttpServeResourceMoveAllowed(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `import "net/http";
import "net/tcp";

fn main() {
	let app := http::Server();
	let listener := tcp::ListenTcp("127.0.0.1:0") catch err {
		return;
	};
	app.Serve(listener) catch err {
		return;
	};
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected compilation success when moving listener, got: %s", result.Output)
	}
}

func TestCompile_DefaultParam_TrailingAllowed(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `fn add(a: i32, b: i32 = 2) -> i32 {
	return a + b;
}

fn main() {
	let x := add(10);
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected compilation success for trailing default parameter, got: %s", result.Output)
	}
}

func TestCompile_DefaultParam_NonTrailingRejected(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `fn bad(a: i32 = 1, b: i32) -> i32 {
	return a + b;
}

fn main() {
	let x := bad(1, 2);
}`,
		},
		Debug:         false,
		LogFormat:     HTML,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if result.Success {
		t.Fatalf("expected compilation failure for non-trailing default parameter")
	}
	if !strings.Contains(result.Output, "default values must be trailing") {
		t.Fatalf("expected default trailing diagnostic, got: %s", result.Output)
	}
}

func TestCompile_DefaultParam_MethodReceiverReferenceAllowed(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `type S struct {
	.V: i32
};

fn (s: &S) M(x: i32 = s.V) -> i32 {
	return x;
}

fn main() {
	let s := 999; // caller symbol with same name should not leak into default resolution
	let obj: S = { .V = 10 };
	let x := obj.M();
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected compilation success for receiver-referencing default parameter, got: %s", result.Output)
	}
}

func TestCompile_HeapAssignmentTypeMismatchRejected(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `fn main() {
	let x: #i32 = #1;
	x = #true;
}`,
		},
		Debug:         false,
		LogFormat:     HTML,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if result.Success {
		t.Fatalf("expected compilation failure for incompatible heap assignment")
	}
	if !strings.Contains(result.Output, "#bool") || !strings.Contains(result.Output, "#i32") {
		t.Fatalf("expected heap assignment type mismatch diagnostic, got: %s", result.Output)
	}
}

func TestCompile_HeapAssignmentImplicitMoveAllowed(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `fn main() {
	let a: #i32 = #1;
	let b: #i32 = #2;
	a = b;
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected compilation success for implicit heap ownership move, got: %s", result.Output)
	}
}

func TestCompile_ValueParamCopyableAllowed(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `fn consume(v: i32) {
	let x := v;
}

fn main() {
	let n := 10;
	consume(n);
	let again := n;
}`,
		},
		Debug:         false,
		LogFormat:     HTML,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected compilation success for copyable value parameter, got: %s", result.Output)
	}
}

func TestCompile_ValueParamNonCopyableAllowed(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `fn consume(v: #i32) {
}

fn main() {
	let n := #42;
	consume(n);
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected compilation success for non-copyable value parameter, got: %s", result.Output)
	}
}

func TestCompile_ExplicitMoveSyntaxRejected(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `fn consume(v: i32) {
	let x := v;
}

fn main() {
	let n := 10;
	consume(@n);
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if result.Success {
		t.Fatalf("expected compilation failure for explicit '@' syntax")
	}
}

func TestCompile_ValueReceiverNonCopyableAllowed(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `type Box struct {
	.v: #i32
};

fn (b: Box) Consume() {
}

fn main() {
	let a := { .v = #1 } as Box;
	a.Consume();
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected compilation success for non-copyable value receiver, got: %s", result.Output)
	}
}

func TestCompile_MoveQualifiedReceiverSyntaxRejected(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `type Box struct {
	.v: i32
};

fn (b: @Box) Consume() {
}
`,
		},
		Debug:         false,
		LogFormat:     HTML,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if result.Success {
		t.Fatalf("expected compilation failure for move-qualified receiver syntax")
	}
}

func TestCompile_ExplicitMoveBorrowSyntaxRejected(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `fn main() {
	let x := 10;
	let y := @&x;
}`,
		},
		Debug:         false,
		LogFormat:     HTML,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if result.Success {
		t.Fatalf("expected compilation failure for explicit '@' syntax")
	}
}

func TestCompile_NestedReferencesAllowed(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `fn main() {
	let x := 10;
	let r1 := &x;
	let r2 := &r1;
	let y := *r2;
	let z := *y;
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected compilation success for nested references, got: %s", result.Output)
	}
}

func TestCompile_NestedReferenceTypeAnnotationAllowed(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `fn read(r: & &i32) -> i32 {
	let p := *r;
	return *p;
}

fn main() {
	let x := 10;
	let r1 := &x;
	let r2: & &i32 = &r1;
	let y := read(r2);
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected compilation success for nested reference type annotation, got: %s", result.Output)
	}
}

func TestCompile_StructLiteralFieldKeyDoesNotConsumeMatchingBinding(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `type Wrap struct {
	.conn: []i32,
};

fn main() {
	let conn := [1, 2, 3];
	let w: Wrap = { .conn = conn };
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected struct literal field key to not consume matching binding, got: %s", result.Output)
	}
}
