package pipeline_test

import (
	"os"
	"path/filepath"
	"testing"

	compilerapi "compiler/internal/compiler"
	"compiler/internal/diagnostics"
)

func TestPipelineParsesImportsAndReusesCachedModules(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `import "util";

fn build() i32 {
    return util::value()
}
`)
	mustWrite(t, filepath.Join(root, "util.ferr"), `fn value() i32 {
    return 1
}
`)

	diag := diagnostics.NewBag()
	c := compilerapi.New(root, ".ferr", diag)
	first := c.ParseEntry(filepath.Join(root, "main.ferr"))
	if first.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", first.Diagnostics.Diagnostics())
	}
	if len(first.Modules) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(first.Modules))
	}
	if len(first.Module.Imports) != 1 || first.Module.Imports[0].Path != "util" {
		t.Fatalf("unexpected imports: %#v", first.Module.Imports)
	}

	firstAST := first.Entry.AST
	second := c.ParseEntry(filepath.Join(root, "main.ferr"))
	if second.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics on cached parse: %v", second.Diagnostics.Diagnostics())
	}
	if second.Entry == nil || second.Entry.AST != firstAST {
		t.Fatalf("expected cached AST to be reused")
	}
}

func TestPipelineReportsImportCycle(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.ferr"), `import "b";
fn a() i32 { return 1 }
`)
	mustWrite(t, filepath.Join(root, "b.ferr"), `import "a";
fn b() i32 { return 2 }
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "a.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatalf("expected cycle diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrCyclicImport {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrCyclicImport, result.Diagnostics.Diagnostics())
	}
}

func TestPipelineParsesWholeWorkspace(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `import "util"

fn build() i32 {
    return util::value()
}
`)
	mustWrite(t, filepath.Join(root, "util.ferr"), `fn value() i32 {
    return 1
}
`)
	mustWrite(t, filepath.Join(root, "extra.ferr"), `const BuildMode = "debug"
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseWorkspace()
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", result.Diagnostics.Diagnostics())
	}
	if len(result.Modules) != 3 {
		t.Fatalf("expected 3 modules, got %d", len(result.Modules))
	}
	if result.Modules[0].ImportPath != "extra" || result.Modules[1].ImportPath != "main" || result.Modules[2].ImportPath != "util" {
		t.Fatalf("unexpected module order: %#v", []string{
			result.Modules[0].ImportPath,
			result.Modules[1].ImportPath,
			result.Modules[2].ImportPath,
		})
	}
}

func TestPipelineReportsWorkspaceCycle(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.ferr"), `import "b"
fn a() i32 { return 1 }
`)
	mustWrite(t, filepath.Join(root, "b.ferr"), `import "a"
fn b() i32 { return 2 }
`)
	mustWrite(t, filepath.Join(root, "main.ferr"), `fn main() i32 { return 0 }`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseWorkspace()
	if !result.Diagnostics.HasErrors() {
		t.Fatalf("expected cycle diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrCyclicImport {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrCyclicImport, result.Diagnostics.Diagnostics())
	}
}

func TestPipelineRejectsRelativeImports(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `import "../util/build"

fn main() i32 { return 0 }
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatalf("expected import path diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidImportPath {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrInvalidImportPath, result.Diagnostics.Diagnostics())
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
