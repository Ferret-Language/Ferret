package diagnostics

import (
	"testing"

	"compiler/internal/core/source"
)

func testLoc(file string, line, col int) source.Location {
	start := source.Position{Line: line, Column: col}
	end := source.Position{Line: line, Column: col + 1}
	return source.NewLocation(file, start, end)
}

func TestWithSecondaryLabelRequiresPrimary(t *testing.T) {
	d := NewError("boom")
	loc := testLoc("a.ferr", 1, 1)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when adding secondary label without primary")
		}
	}()

	d.WithSecondaryLabel(&loc, "context")
}

func TestWithCodeReplacementAddsOrderedExtra(t *testing.T) {
	d := NewError("immutable")
	loc := testLoc("main.ferr", 2, 5)
	d.WithCodeReplacement(&loc, "maybe", "mut maybe")

	if len(d.Extras) != 1 {
		t.Fatalf("expected 1 extra entry, got %d", len(d.Extras))
	}
	if d.Extras[0].Kind != ExtraCodeHint {
		t.Fatalf("expected extra kind ExtraCodeHint, got %v", d.Extras[0].Kind)
	}
	if len(d.CodeHints) != 1 {
		t.Fatalf("expected 1 code hint, got %d", len(d.CodeHints))
	}
	hint := d.CodeHints[0]
	if len(hint.Lines) != 2 {
		t.Fatalf("expected 2 hint lines, got %d", len(hint.Lines))
	}
	if hint.Lines[0].Prefix != "-" || hint.Lines[0].Code != "maybe" {
		t.Fatalf("unexpected first replacement line: %#v", hint.Lines[0])
	}
	if hint.Lines[1].Prefix != "+" || hint.Lines[1].Code != "mut maybe" {
		t.Fatalf("unexpected second replacement line: %#v", hint.Lines[1])
	}
}

func TestWithPrimaryLabelSetsFilePath(t *testing.T) {
	d := NewError("x")
	loc := testLoc("sample.ferr", 3, 2)
	d.WithPrimaryLabel(&loc, "here")

	if d.FilePath != "sample.ferr" {
		t.Fatalf("expected filepath sample.ferr, got %q", d.FilePath)
	}
	if len(d.Labels) != 1 || d.Labels[0].Style != Primary {
		t.Fatalf("expected one primary label, got %#v", d.Labels)
	}
}
