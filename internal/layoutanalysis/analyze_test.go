package layoutanalysis_test

import (
	"os"
	"path/filepath"
	"testing"

	compilerapi "compiler/internal/compiler"
	"compiler/internal/phase"
)

func TestLayoutComputesStructOffsets(t *testing.T) {
	root := t.TempDir()
	mustWriteLayout(t, filepath.Join(root, "main.ferr"), `type Point struct {
    X i8
    Y i64
    Z i16
}

fn main() i32 {
    return 0
}
`)

	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.Phase < phase.PhaseLayoutComputed || result.Entry.Layout == nil {
		t.Fatalf("expected layout-computed entry, got %#v", result.Entry)
	}
	point, ok := result.Entry.Layout.Lookup("Point")
	if !ok || point == nil || point.Struct == nil {
		t.Fatalf("expected Point layout, got %#v", result.Entry.Layout)
	}
	if point.Size != 24 || point.Align != 8 {
		t.Fatalf("expected Point size=24 align=8, got size=%d align=%d", point.Size, point.Align)
	}
	fields := point.Struct.Fields
	if len(fields) != 3 {
		t.Fatalf("expected 3 fields, got %#v", fields)
	}
	if fields[0].Name != "X" || fields[0].Offset != 0 {
		t.Fatalf("expected X at offset 0, got %#v", fields[0])
	}
	if fields[1].Name != "Y" || fields[1].Offset != 8 {
		t.Fatalf("expected Y at offset 8, got %#v", fields[1])
	}
	if fields[2].Name != "Z" || fields[2].Offset != 16 {
		t.Fatalf("expected Z at offset 16, got %#v", fields[2])
	}
}

func TestLayoutPreservesArrayLengthFromTyping(t *testing.T) {
	root := t.TempDir()
	mustWriteLayout(t, filepath.Join(root, "main.ferr"), `type Numbers [3]i16

fn main() i32 {
    return 0
}
`)

	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.Layout == nil {
		t.Fatalf("expected layout result, got %#v", result.Entry)
	}
	numbers, ok := result.Entry.Layout.Lookup("Numbers")
	if !ok || numbers == nil {
		t.Fatalf("expected Numbers layout, got %#v", result.Entry.Layout)
	}
	if numbers.Size != 6 || numbers.Align != 2 {
		t.Fatalf("expected Numbers size=6 align=2, got size=%d align=%d", numbers.Size, numbers.Align)
	}
}

func mustWriteLayout(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
