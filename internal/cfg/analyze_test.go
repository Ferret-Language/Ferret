package cfg_test

import (
	"os"
	"path/filepath"
	"testing"

	compilerapi "compiler/internal/compiler"
	"compiler/internal/diagnostics"
	"compiler/internal/phase"
)

func TestCFGReportsMissingReturn(t *testing.T) {
	root := t.TempDir()
	mustWriteCFG(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    if true {
        return 1
    }
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase != phase.PhaseCFGAnalyzed {
		t.Fatalf("expected CFG analyzed phase, got %#v", result.Entry)
	}
	if result.Entry.CFG == nil {
		t.Fatal("expected CFG")
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrMissingReturn {
			if len(diag.Labels) < 2 {
				t.Fatalf("expected branch-specific secondary label, got %#v", diag.Labels)
			}
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrMissingReturn, result.Diagnostics.Diagnostics())
}

func TestCFGReportsUnreachableCode(t *testing.T) {
	root := t.TempDir()
	mustWriteCFG(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    return 1
    let x = 2
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase != phase.PhaseCFGAnalyzed {
		t.Fatalf("expected CFG analyzed phase, got %#v", result.Entry)
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.WarnUnreachableCode {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s warning, got %#v", diagnostics.WarnUnreachableCode, result.Diagnostics.Diagnostics())
	}
}

func TestCFGAllowsSwitchFallbackReturn(t *testing.T) {
	root := t.TempDir()
	mustWriteCFG(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    switch 1 {
    case 0 {
        return 10
    }
    case 1 {
        return 20
    }
    }
    return 30
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase != phase.PhaseCFGAnalyzed {
		t.Fatalf("expected CFG analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrMissingReturn || diag.Code == diagnostics.WarnUnreachableCode {
			t.Fatalf("unexpected CFG diagnostic %#v", diag)
		}
	}
}

func TestCFGReportsMissingReturnInSwitchFallback(t *testing.T) {
	root := t.TempDir()
	mustWriteCFG(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    switch 1 {
    case 0 {
        return 10
    }
    case 1 {
        return 20
    }
    }
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Entry == nil || result.Entry.Phase != phase.PhaseCFGAnalyzed {
		t.Fatalf("expected CFG analyzed phase, got %#v", result.Entry)
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrMissingReturn {
			if len(diag.Labels) < 2 {
				t.Fatalf("expected switch fallback secondary label, got %#v", diag.Labels)
			}
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrMissingReturn, result.Diagnostics.Diagnostics())
}

func mustWriteCFG(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
