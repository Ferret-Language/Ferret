package layoutanalysis_test

import (
	"os"
	"path/filepath"
	"testing"

	"compiler/internal/core/phase"
	compiler "compiler/internal/driver"
)

func TestLayoutComputesStructOffsets(t *testing.T) {
	root := t.TempDir()
	mustWriteLayout(t, filepath.Join(root, "main.ferr"), `type Point struct {
    X: i8
    Y: i64
    Z: i16
}

fn main() i32 {
    return 0
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.ferr"))
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

	result := compiler.ParsePath(filepath.Join(root, "main.ferr"))
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

func TestLayoutComputesTaggedUnionLayout(t *testing.T) {
	root := t.TempDir()
	mustWriteLayout(t, filepath.Join(root, "main.ferr"), `type Token union {
    i32,
    i64,
    bool,
}

fn main() i32 {
    return 0
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	token, ok := result.Entry.Layout.Lookup("Token")
	if !ok || token == nil {
		t.Fatalf("expected Token layout, got %#v", result.Entry.Layout)
	}
	if token.Size != 16 || token.Align != 8 {
		t.Fatalf("expected Token size=16 align=8, got size=%d align=%d", token.Size, token.Align)
	}
	if token.Struct != nil {
		t.Fatalf("expected named union to expose dedicated union layout, got struct %#v", token.Struct)
	}
	if token.Union == nil {
		t.Fatalf("expected union layout, got %#v", token)
	}
	if token.Union.TagOffset != 0 || token.Union.PayloadOffset != 8 {
		t.Fatalf("expected tag@0 payload@8, got %#v", token.Union)
	}
	if len(token.Union.Members) != 3 {
		t.Fatalf("expected 3 union members, got %#v", token.Union.Members)
	}
}

func TestLayoutUsesNicheForOptionalPointer(t *testing.T) {
	root := t.TempDir()
	mustWriteLayout(t, filepath.Join(root, "main.ferr"), `type Holder struct {
    Value: ?^i32
}

fn main() i32 {
    return 0
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	holder, ok := result.Entry.Layout.Lookup("Holder")
	if !ok || holder == nil || holder.Struct == nil {
		t.Fatalf("expected Holder layout, got %#v", result.Entry.Layout)
	}
	if holder.Size != 16 || holder.Align != 8 {
		t.Fatalf("expected Holder size=16 align=8, got size=%d align=%d", holder.Size, holder.Align)
	}
	if got := holder.Struct.Fields[0].Size; got != 16 {
		t.Fatalf("expected optional raw pointer field size=16, got %d", got)
	}
}

func TestLayoutComputesRawPointerFieldSizes(t *testing.T) {
	root := t.TempDir()
	mustWriteLayout(t, filepath.Join(root, "main.ferr"), `type Holder struct {
    Raw: ^void
    Raw2: ^i32
}

fn main() i32 {
    return 0
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	holder, ok := result.Entry.Layout.Lookup("Holder")
	if !ok || holder == nil || holder.Struct == nil {
		t.Fatalf("expected Holder layout, got %#v", result.Entry.Layout)
	}
	if holder.Size != 16 || holder.Align != 8 {
		t.Fatalf("expected Holder size=16 align=8, got size=%d align=%d", holder.Size, holder.Align)
	}
	if got := holder.Struct.Fields[0].Size; got != 8 {
		t.Fatalf("expected raw pointer field size=8, got %d", got)
	}
	if got := holder.Struct.Fields[1].Size; got != 8 {
		t.Fatalf("expected second raw pointer field size=8, got %d", got)
	}
}

func TestLayoutUsesNicheForOptionalBool(t *testing.T) {
	root := t.TempDir()
	mustWriteLayout(t, filepath.Join(root, "main.ferr"), `type Flags struct {
    Value: ?bool
}

fn main() i32 {
    return 0
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	flags, ok := result.Entry.Layout.Lookup("Flags")
	if !ok || flags == nil || flags.Struct == nil {
		t.Fatalf("expected Flags layout, got %#v", result.Entry.Layout)
	}
	if flags.Size != 1 || flags.Align != 1 {
		t.Fatalf("expected Flags size=1 align=1, got size=%d align=%d", flags.Size, flags.Align)
	}
	if got := flags.Struct.Fields[0].Size; got != 1 {
		t.Fatalf("expected optional bool field size=1, got %d", got)
	}
}

func TestLayoutUsesNicheForOptionalEnum(t *testing.T) {
	root := t.TempDir()
	mustWriteLayout(t, filepath.Join(root, "main.ferr"), `type Color enum {
    red,
    blue,
}

type Holder struct {
    Value: ?Color
}

fn main() i32 {
    return 0
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	holder, ok := result.Entry.Layout.Lookup("Holder")
	if !ok || holder == nil || holder.Struct == nil {
		t.Fatalf("expected Holder layout, got %#v", result.Entry.Layout)
	}
	if holder.Size != 4 || holder.Align != 4 {
		t.Fatalf("expected Holder size=4 align=4, got size=%d align=%d", holder.Size, holder.Align)
	}
	if got := holder.Struct.Fields[0].Size; got != 4 {
		t.Fatalf("expected optional enum field size=4, got %d", got)
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
