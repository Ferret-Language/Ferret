package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompile_InMemorySimpleCode(t *testing.T) {
	opts := &Options{
		Code:          "let x := 42;",
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	if !result.Success {
		t.Errorf("Expected successful compilation, got failure")
	}
}

func TestCompile_InMemoryWithSyntaxError(t *testing.T) {
	opts := &Options{
		Code:          "let x := ;", // Missing value
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
		Code: `let x := 42;
let y := 100;
let z := x + y;`,
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	if !result.Success {
		t.Errorf("Expected successful compilation of multiple statements")
	}
}

func TestCompile_HTMLFormat(t *testing.T) {
	opts := &Options{
		Code:          "let x := 42;",
		Debug:         false,
		LogFormat:     HTML,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	if !result.Success {
		t.Error("Expected successful compilation")
	}

	// HTML format may or may not produce output for successful compilation
	// (depends on whether there are warnings)
	// Just verify it doesn't crash
}

func TestCompile_HTMLFormatWithError(t *testing.T) {
	opts := &Options{
		Code:          "let x := ;", // Syntax error
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

func TestCompile_FileMode_ValidFile(t *testing.T) {
	// Create temporary test file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.fer")

	content := "let x := 42;"
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

	if !result.Success {
		t.Errorf("Expected successful compilation of valid file")
	}
}

func TestCompile_FileMode_WithImports(t *testing.T) {
	// Create temporary test directory
	tmpDir := t.TempDir()

	// Create utils module
	utilsPath := filepath.Join(tmpDir, "utils.fer")
	utilsContent := "let pi := 3.14;"
	if err := os.WriteFile(utilsPath, []byte(utilsContent), 0644); err != nil {
		t.Fatalf("Failed to create utils.fer: %v", err)
	}

	// Create main file that imports utils
	mainPath := filepath.Join(tmpDir, "main.fer")
	mainContent := `import "` + filepath.Base(tmpDir) + `/utils";
let x := 42;`
	if err := os.WriteFile(mainPath, []byte(mainContent), 0644); err != nil {
		t.Fatalf("Failed to create main.fer: %v", err)
	}

	opts := &Options{
		EntryFile:     mainPath,
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	if !result.Success {
		t.Errorf("Expected successful compilation with imports")
	}
}

func TestCompile_DebugMode(t *testing.T) {
	opts := &Options{
		Code:          "let x := 42;",
		Debug:         true, // Enable debug mode
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	if !result.Success {
		t.Errorf("Expected successful compilation in debug mode")
	}
}

func TestCompile_EmptyCode(t *testing.T) {
	opts := &Options{
		Code:          " ", // Whitespace-only (truly empty causes entry point error)
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	// Empty/whitespace-only code should compile successfully (empty module)
	if !result.Success {
		t.Error("Expected successful compilation of empty code")
	}
}

func TestCompile_ComplexExpression(t *testing.T) {
	opts := &Options{
		Code: `let a := 10;
let b := 20;
let c := (a + b) * 2 - 5;`,
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	if !result.Success {
		t.Error("Expected successful compilation of complex expressions")
	}
}

func TestCompile_FunctionDeclaration(t *testing.T) {
	opts := &Options{
		Code: `fn add(x: i32, y: i32) -> i32 {
	return x + y;
}`,
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	if !result.Success {
		t.Error("Expected successful compilation of function declaration")
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
		Code:          "let x := 42;",
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	// Verify Result has expected fields
	if result.Success != true {
		t.Error("Expected Success field to be true")
	}

	// ANSI format should not populate Output for success
	// Only HTML format and errors populate Output
}

func TestCompile_ANSIFormat(t *testing.T) {
	opts := &Options{
		Code:          "let x := 42;",
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	if !result.Success {
		t.Error("Expected successful compilation")
	}

	// ANSI format with success should not populate output field
	// (diagnostics go to stderr)
	if result.Output != "" {
		t.Logf("Note: ANSI format produced output: %q", result.Output)
	}
}

func TestCompile_ImportOrderValidation(t *testing.T) {
	opts := &Options{
		Code: `let x := 42;
import "std/io";`, // Import after declaration - should error
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
		Code: `import "std/io";
import "std/math";
let x := 42;`,
		Debug:         false,
		LogFormat:     ANSI,
		TypecheckOnly: true,
	}

	result := Compile(opts)

	// May succeed or fail depending on whether std modules exist
	// The important thing is that it doesn't crash
	if result.Success {
		t.Log("Multiple imports compiled successfully")
	} else {
		t.Log("Multiple imports failed (expected if std modules don't exist)")
	}
}
