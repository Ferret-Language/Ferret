package diagnostics

import (
	"compiler/colors"
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
	if helpIdx == -1 || hintIdx == -1 || hintIdx > helpIdx {
		t.Fatalf("expected output order to follow builder calls (hint before help), got:\n%s", out)
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

func TestEmitter_MultipleCodeHints(t *testing.T) {
	file := "test.fer"
	bag := NewDiagnosticBag(file)
	bag.AddSourceContent(file, "fn main() {\n    let y := @&x;\n}\n")

	loc := source.NewLocation(
		&file,
		&source.Position{Line: 2, Column: 14},
		&source.Position{Line: 2, Column: 17},
	)

	diag := NewError("implicit copy requires a valid `copy()` method").
		WithCode("T0004").
		WithPrimaryLabel(loc, "cannot implicitly copy value of type `Vec2` in variable initialization").
		WithCodeReplacement(loc, "@&x", "@x").
		WithCodeHint(loc, "fn (v: &Vec2) copy() -> Vec2", CodeHintLabel{
			Line:    1,
			Column:  1,
			Length:  2,
			Message: "Add this method",
			Style:   Secondary,
		})
	bag.Add(diag)

	out := StripColors(bag.EmitAllToString())
	if !strings.Contains(out, "| +     let y := @x;") {
		t.Fatalf("expected replacement hint to render, got:\n%s", out)
	}
	if !strings.Contains(out, "fn (v: &Vec2) copy() -> Vec2") {
		t.Fatalf("expected method signature hint to render, got:\n%s", out)
	}
}

func TestEmitter_TextOrderPreserved(t *testing.T) {
	file := "test.fer"
	bag := NewDiagnosticBag(file)
	bag.AddSourceContent(file, "let x := 1;\n")

	loc := source.NewLocation(
		&file,
		&source.Position{Line: 1, Column: 1},
		&source.Position{Line: 1, Column: 4},
	)

	diag := NewError("ordered text output").
		WithPrimaryLabel(loc, "here").
		WithHelp("h1").
		WithHelp("h2").
		WithNote("n1").
		WithText("custom", "c1", colors.BLUE).
		WithHelp("h3").
		WithCodeHint(loc, "fn main() {}")
	bag.Add(diag)

	out := StripColors(bag.EmitAllToString())

	idxH1 := strings.Index(out, "= help: h1")
	idxH2 := strings.Index(out, "= help: h2")
	idxN1 := strings.Index(out, "= note: n1")
	idxC1 := strings.Index(out, "= custom: c1")
	idxH3 := strings.Index(out, "= help: h3")
	idxSuggestion := strings.Index(out, "= suggestion:")

	if idxH1 == -1 || idxH2 == -1 || idxN1 == -1 || idxC1 == -1 || idxH3 == -1 || idxSuggestion == -1 {
		t.Fatalf("expected all text entries and suggestion header, got:\n%s", out)
	}
	if !(idxH1 < idxH2 && idxH2 < idxN1 && idxN1 < idxC1 && idxC1 < idxH3 && idxH3 < idxSuggestion) {
		t.Fatalf("expected text order h1,h2,n1,c1,h3 before suggestion header, got:\n%s", out)
	}
}

func TestEmitter_InterleavedTextAndCodeHints_Order(t *testing.T) {
	file := "test.fer"
	bag := NewDiagnosticBag(file)
	bag.AddSourceContent(file, "fn main() {\n    let y := @&x;\n}\n")

	loc := source.NewLocation(
		&file,
		&source.Position{Line: 2, Column: 14},
		&source.Position{Line: 2, Column: 17},
	)

	diag := NewError("implicit copy requires a valid `copy()` method").
		WithCode("T0004").
		WithPrimaryLabel(loc, "cannot implicitly copy value of type `Vec2` in variable initialization").
		WithHelp("h1").
		WithNote("n1").
		WithCodeReplacement(loc, "@&x", "@x").
		WithHelp("h2").
		WithCodeHint(loc, "fn (v: &Vec2) copy() -> Vec2")
	bag.Add(diag)

	out := StripColors(bag.EmitAllToString())

	idxH1 := strings.Index(out, "= help: h1")
	idxN1 := strings.Index(out, "= note: n1")
	idxReplace := strings.Index(out, "| +     let y := @x;")
	idxH2 := strings.Index(out, "= help: h2")
	idxMethod := strings.Index(out, "fn (v: &Vec2) copy() -> Vec2")
	suggestionCount := strings.Count(out, "= suggestion:")

	if idxH1 == -1 || idxN1 == -1 || idxReplace == -1 || idxH2 == -1 || idxMethod == -1 || suggestionCount != 1 {
		t.Fatalf("expected all interleaved sections, got:\n%s", out)
	}
	if !(idxH1 < idxN1 && idxN1 < idxReplace && idxReplace < idxH2 && idxH2 < idxMethod) {
		t.Fatalf("expected order h1 -> n1 -> replacement -> h2 -> method hint, got:\n%s", out)
	}
}

func TestDiffHighlightSpans_UsesByteOffsets(t *testing.T) {
	oldStart, oldLen, newStart, newLen := diffHighlightSpans(4, "café", "cafe")

	if oldStart != 7 || oldLen != 2 {
		t.Fatalf("unexpected old span: start=%d len=%d", oldStart, oldLen)
	}
	if newStart != 7 || newLen != 1 {
		t.Fatalf("unexpected new span: start=%d len=%d", newStart, newLen)
	}
}
