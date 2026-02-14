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

func TestCompile_ResourceImplicitCopyRejected(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `import "std/fs";

fn main() {
	let f := fs::CreateRW("/tmp/resource_copy_reject.txt") catch err {
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
	if result.Success {
		t.Fatalf("expected compilation failure for implicit resource copy")
	}
	if !strings.Contains(result.Output, "resource values are non-copyable") {
		t.Fatalf("expected resource copy diagnostic, got: %s", result.Output)
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
	let g := @f;
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

func TestCompile_NetResourceImplicitCopyRejected(t *testing.T) {
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
	if result.Success {
		t.Fatalf("expected compilation failure for implicit tcp resource copy")
	}
	if !strings.Contains(result.Output, "resource values are non-copyable") {
		t.Fatalf("expected resource copy diagnostic, got: %s", result.Output)
	}
}

func TestCompile_HttpServeResourceMoveRequired(t *testing.T) {
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
	if result.Success {
		t.Fatalf("expected compilation failure when passing listener without move")
	}
	if !strings.Contains(result.Output, "resource values are non-copyable") {
		t.Fatalf("expected resource copy diagnostic, got: %s", result.Output)
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
	app.Serve(@listener) catch err {
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

func TestCompile_MoveQualifiedParam_RequiresMove(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `fn consume(v: @i32) {
	let x := v;
}

fn main() {
	let n := 10;
	consume(n);
}`,
		},
		Debug:         false,
		LogFormat:     HTML,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if result.Success {
		t.Fatalf("expected compilation failure for move-qualified parameter without @")
	}
	if !strings.Contains(result.Output, "must be moved") {
		t.Fatalf("expected move-qualified argument diagnostic, got: %s", result.Output)
	}
}

func TestCompile_MoveQualifiedParam_WithMoveAllowed(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `fn consume(v: @i32) {
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
	if !result.Success {
		t.Fatalf("expected compilation success for explicit move argument, got: %s", result.Output)
	}
}

func TestCompile_MoveQualifiedReceiver_ConsumesValue(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `type Box struct {
	.v: i32
};

fn (b: @Box) Consume() {
}

fn main() {
	let a := { .v = 1 } as Box;
	a.Consume();
}`,
		},
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if !result.Success {
		t.Fatalf("expected compilation success for move-qualified receiver call, got: %s", result.Output)
	}
}

func TestCompile_MoveQualifiedReceiver_ReferenceRejected(t *testing.T) {
	opts := &Options{
		Files: map[string]string{
			"main.fer": `type Box struct {
	.v: i32
};

fn (b: @&mut Box) Consume() {
}
`,
		},
		Debug:         false,
		LogFormat:     HTML,
		TypecheckOnly: true,
	}

	result := Compile(opts)
	if result.Success {
		t.Fatalf("expected compilation failure for move-qualified reference receiver")
	}
	if !strings.Contains(result.Output, "move-qualified receiver cannot be a reference") {
		t.Fatalf("expected move-qualified receiver reference diagnostic, got: %s", result.Output)
	}
}

func TestCompile_MoveBorrowMix_Rejected(t *testing.T) {
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
		t.Fatalf("expected compilation failure for move+borrow mix")
	}
	if !strings.Contains(result.Output, "cannot combine move with borrow") {
		t.Fatalf("expected move+borrow diagnostic, got: %s", result.Output)
	}
}
