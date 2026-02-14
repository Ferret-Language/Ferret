package diagnostics

import (
	"compiler/internal/source"
	"strings"
	"testing"
)

func TestEmitter_CodeReplacementHint(t *testing.T) {
	file := "test.fer"
	bag := NewDiagnosticBag(file)
	bag.AddSourceContent(file, "fn main() {\n    let y := @&x;\n}\n")

	loc := source.NewLocation(
		&file,
		&source.Position{Line: 2, Column: 14},
		&source.Position{Line: 2, Column: 17},
	)

	diag := NewError("cannot combine move with borrow").
		WithCode("T0004").
		WithPrimaryLabel(loc, "move operator '@' cannot be applied to a borrow expression").
		WithCodeReplacement(loc, "@&x", "@x").
		WithHelp("move the owned value directly (e.g., '@x')")

	bag.Add(diag)

	out := StripColors(bag.EmitAllToString())
	if !strings.Contains(out, "= suggestion:") {
		t.Fatalf("expected suggestion header, got:\n%s", out)
	}
	if !strings.Contains(out, "--- a/test.fer") {
		t.Fatalf("expected unified diff header, got:\n%s", out)
	}
	if !strings.Contains(out, "+++ b/test.fer") {
		t.Fatalf("expected unified diff header, got:\n%s", out)
	}
	if !strings.Contains(out, "@@ line 2 @@") {
		t.Fatalf("expected hunk line header, got:\n%s", out)
	}
	if !strings.Contains(out, "| -     let y := @&x;") {
		t.Fatalf("expected removed line, got:\n%s", out)
	}
	if !strings.Contains(out, "| +     let y := @x;") {
		t.Fatalf("expected added line, got:\n%s", out)
	}
	helpIdx := strings.Index(out, "= help:")
	hintIdx := strings.Index(out, "| +     let y := @x;")
	if helpIdx == -1 || hintIdx == -1 || helpIdx > hintIdx {
		t.Fatalf("expected code hint after help, got:\n%s", out)
	}
}

func TestEmitter_LegacyCodeHintUsesPlusGutter(t *testing.T) {
	file := "test.fer"
	bag := NewDiagnosticBag(file)
	bag.AddSourceContent(file, "let x := 1;\n")

	loc := source.NewLocation(
		&file,
		&source.Position{Line: 1, Column: 1},
		&source.Position{Line: 1, Column: 4},
	)

	diag := NewError("missing entry point").
		WithCode("P0001").
		WithCodeHint(loc, "fn main() {\n}")
	bag.Add(diag)

	out := StripColors(bag.EmitAllToString())
	if !strings.Contains(out, "+ | fn main() {") {
		t.Fatalf("expected legacy code hint '+' gutter, got:\n%s", out)
	}
}

func TestEmitter_CodeReplacementHintWithNarrowLocation(t *testing.T) {
	file := "test.fer"
	bag := NewDiagnosticBag(file)
	bag.AddSourceContent(file, "fn main() {\n    let y := @&x;\n}\n")

	// Token-only location (start/end at '@') to simulate narrow spans.
	loc := source.NewLocation(
		&file,
		&source.Position{Line: 2, Column: 14},
		&source.Position{Line: 2, Column: 14},
	)

	diag := NewError("cannot combine move with borrow").
		WithCode("T0004").
		WithPrimaryLabel(loc, "move operator '@' cannot be applied to a borrow expression").
		WithCodeReplacement(loc, "@&x", "@x")

	bag.Add(diag)

	out := StripColors(bag.EmitAllToString())
	if !strings.Contains(out, "| +     let y := @x;") {
		t.Fatalf("expected replacement suggestion with narrow location, got:\n%s", out)
	}
}
