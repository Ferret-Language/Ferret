package diagnostics

import (
	"compiler/internal/source"
	"testing"
)

func TestSeverity_String(t *testing.T) {
	tests := []struct {
		severity Severity
		expected string
	}{
		{Error, "error"},
		{Warning, "warning"},
		{Info, "info"},
		{Hint, "hint"},
		{Severity(999), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.severity.String(); got != tt.expected {
			t.Errorf("Severity(%d).String() = %q, want %q", tt.severity, got, tt.expected)
		}
	}
}

func TestNewError(t *testing.T) {
	diag := NewError("test error message")

	if diag == nil {
		t.Fatal("NewError returned nil")
	}

	if diag.Severity != Error {
		t.Errorf("Expected severity Error, got %v", diag.Severity)
	}

	if diag.Message != "test error message" {
		t.Errorf("Expected message 'test error message', got %q", diag.Message)
	}

	if diag.Labels == nil {
		t.Error("Labels should be initialized, not nil")
	}

	if diag.Notes == nil {
		t.Error("Notes should be initialized, not nil")
	}
}

func TestNewWarning(t *testing.T) {
	diag := NewWarning("test warning message")

	if diag == nil {
		t.Fatal("NewWarning returned nil")
	}

	if diag.Severity != Warning {
		t.Errorf("Expected severity Warning, got %v", diag.Severity)
	}

	if diag.Message != "test warning message" {
		t.Errorf("Expected message 'test warning message', got %q", diag.Message)
	}
}

func TestNewInfo(t *testing.T) {
	diag := NewInfo("test info message")

	if diag == nil {
		t.Fatal("NewInfo returned nil")
	}

	if diag.Severity != Info {
		t.Errorf("Expected severity Info, got %v", diag.Severity)
	}

	if diag.Message != "test info message" {
		t.Errorf("Expected message 'test info message', got %q", diag.Message)
	}
}

func TestDiagnostic_WithCode(t *testing.T) {
	diag := NewError("test error").WithCode("test")

	if diag.Code != "test" {
		t.Errorf("Expected code 'test', got %q", diag.Code)
	}
}

func TestDiagnostic_WithPrimaryLabel(t *testing.T) {
	test := "test.fer"
	loc := source.NewLocation(
		&test,
		&source.Position{Line: 1, Column: 5},
		&source.Position{Line: 1, Column: 10},
	)

	diag := NewError("undefined variable").
		WithPrimaryLabel(loc, "not found here")

	if len(diag.Labels) != 1 {
		t.Fatalf("Expected 1 label, got %d", len(diag.Labels))
	}

	label := diag.Labels[0]
	if label.Style != Primary {
		t.Errorf("Expected Primary style, got %v", label.Style)
	}

	if *label.Location.Filename != test {
		t.Errorf("Expected filepath 'test.fer', got %q", *label.Location.Filename)
	}

	if label.Message != "not found here" {
		t.Errorf("Expected message 'not found here', got %q", label.Message)
	}

	if diag.FilePath != "test.fer" {
		t.Errorf("Expected diagnostic filepath to be set to 'test.fer', got %q", diag.FilePath)
	}
}

func TestDiagnostic_WithSecondaryLabel(t *testing.T) {

	test := "test.fer"
	primaryLoc := source.NewLocation(
		&test,
		&source.Position{Line: 1, Column: 5},
		&source.Position{Line: 1, Column: 10},
	)

	secondaryLoc := source.NewLocation(
		&test,
		&source.Position{Line: 5, Column: 1},
		&source.Position{Line: 5, Column: 5},
	)

	diag := NewError("type mismatch").
		WithPrimaryLabel(primaryLoc, "used here").
		WithSecondaryLabel(secondaryLoc, "defined here")

	if len(diag.Labels) != 2 {
		t.Fatalf("Expected 2 labels, got %d", len(diag.Labels))
	}

	if diag.Labels[0].Style != Primary {
		t.Error("First label should be Primary")
	}

	if diag.Labels[1].Style != Secondary {
		t.Error("Second label should be Secondary")
	}

	if diag.Labels[1].Message != "defined here" {
		t.Errorf("Expected secondary message 'defined here', got %q", diag.Labels[1].Message)
	}
}

func TestDiagnostic_SecondaryWithoutPrimary_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when adding secondary label without primary")
		}
	}()

	test := "test.fer"

	loc := source.NewLocation(
		&test,
		&source.Position{Line: 1, Column: 1},
		&source.Position{Line: 1, Column: 5},
	)

	// This should panic
	NewError("test").WithSecondaryLabel(loc, "invalid")
}

func TestDiagnostic_MultiplePrimaryLabels(t *testing.T) {

	test := "test.fer"
	loc1 := source.NewLocation(
		&test,
		&source.Position{Line: 1, Column: 1},
		&source.Position{Line: 1, Column: 5},
	)

	loc2 := source.NewLocation(
		&test,
		&source.Position{Line: 2, Column: 1},
		&source.Position{Line: 2, Column: 5},
	)

	diag := NewError("test").
		WithPrimaryLabel(loc1, "first primary").
		WithPrimaryLabel(loc2, "second primary attempt")

	// Should only have one primary label
	primaryCount := 0
	for _, label := range diag.Labels {
		if label.Style == Primary {
			primaryCount++
		}
	}

	if primaryCount != 1 {
		t.Errorf("Expected exactly 1 primary label, got %d", primaryCount)
	}

	if len(diag.Labels) != 1 {
		t.Errorf("Expected 1 label total (second primary ignored), got %d", len(diag.Labels))
	}
}

func TestDiagnostic_WithNote(t *testing.T) {
	diag := NewError("test error").
		WithNote("This is a note").
		WithNote("This is another note")

	if len(diag.Notes) != 2 {
		t.Fatalf("Expected 2 notes, got %d", len(diag.Notes))
	}

	if diag.Notes[0].Message != "This is a note" {
		t.Errorf("Expected first note 'This is a note', got %q", diag.Notes[0].Message)
	}

	if diag.Notes[1].Message != "This is another note" {
		t.Errorf("Expected second note 'This is another note', got %q", diag.Notes[1].Message)
	}
}

func TestDiagnostic_WithHelp(t *testing.T) {
	diag := NewError("syntax error").
		WithHelp("Try adding a semicolon at the end of the statement")

	if diag.Help != "Try adding a semicolon at the end of the statement" {
		t.Errorf("Expected help message to be set, got %q", diag.Help)
	}
}

func TestDiagnostic_BuilderPattern(t *testing.T) {
	// Test the full builder pattern with chaining
	test := "test.fer"
	loc := source.NewLocation(
		&test,
		&source.Position{Line: 10, Column: 5},
		&source.Position{Line: 10, Column: 15},
	)

	diag := NewError("undefined variable 'myVar'").
		WithCode("test").
		WithPrimaryLabel(loc, "not found").
		WithNote("Did you forget to declare this variable?").
		WithHelp("Declare the variable before using it: let myVar := value;")

	// Verify all fields are set correctly
	if diag.Severity != Error {
		t.Error("Severity should be Error")
	}

	if diag.Message != "undefined variable 'myVar'" {
		t.Errorf("Unexpected message: %q", diag.Message)
	}

	if diag.Code != "test" {
		t.Errorf("Expected code 'test', got %q", diag.Code)
	}

	if len(diag.Labels) != 1 {
		t.Errorf("Expected 1 label, got %d", len(diag.Labels))
	}

	if len(diag.Notes) != 1 {
		t.Errorf("Expected 1 note, got %d", len(diag.Notes))
	}

	if diag.Help == "" {
		t.Error("Help message should be set")
	}
}

func TestDiagnostic_MultipleFiles(t *testing.T) {
	file1 := "file1.fer"
	loc1 := source.NewLocation(
		&file1,
		&source.Position{Line: 1, Column: 1},
		&source.Position{Line: 1, Column: 5},
	)

	file2 := "file2.fer"
	loc2 := source.NewLocation(
		&file2,
		&source.Position{Line: 10, Column: 1},
		&source.Position{Line: 10, Column: 5},
	)

	diag := NewError("conflicting definitions").
		WithPrimaryLabel(loc1, "first definition").
		WithSecondaryLabel(loc2, "conflicting definition here")

	if len(diag.Labels) != 2 {
		t.Fatalf("Expected 2 labels, got %d", len(diag.Labels))
	}

	if *diag.Labels[0].Location.Filename != file1 {
		t.Errorf("Expected first label from file1.fer, got %q", *diag.Labels[0].Location.Filename)
	}

	if *diag.Labels[1].Location.Filename != file2 {
		t.Errorf("Expected second label from file2.fer, got %q", *diag.Labels[1].Location.Filename)
	}
}

func TestLabelStyle(t *testing.T) {
	// Test that label styles are distinct constants
	if Primary == Secondary {
		t.Error("Primary and Secondary should have different values")
	}
}

func TestDiagnostic_EmptyLabelsAndNotes(t *testing.T) {
	diag := NewError("simple error")

	if len(diag.Labels) != 0 {
		t.Errorf("New diagnostic should have empty labels, got %d", len(diag.Labels))
	}

	if len(diag.Notes) != 0 {
		t.Errorf("New diagnostic should have empty notes, got %d", len(diag.Notes))
	}

	if diag.Code != "" {
		t.Errorf("New diagnostic should have empty code, got %q", diag.Code)
	}

	if diag.Help != "" {
		t.Errorf("New diagnostic should have empty help, got %q", diag.Help)
	}
}

func TestDiagnostic_WithCodeHintLines(t *testing.T) {
	file := "test.fer"
	loc := source.NewLocation(
		&file,
		&source.Position{Line: 2, Column: 1},
		&source.Position{Line: 2, Column: 6},
	)

	diag := NewError("test").
		WithCodeHintLines(loc, []CodeHintLine{
			{Prefix: "-", Code: "old_call(data);"},
			{Prefix: "+", Code: "old_call(@data);"},
		})

	if diag.CodeHint == nil {
		t.Fatal("expected code hint to be set")
	}
	if len(diag.CodeHint.Lines) != 2 {
		t.Fatalf("expected 2 code hint lines, got %d", len(diag.CodeHint.Lines))
	}
	if diag.CodeHint.Lines[0].Prefix != "-" || diag.CodeHint.Lines[1].Prefix != "+" {
		t.Fatalf("unexpected prefixes: %#v", diag.CodeHint.Lines)
	}
}

func TestDiagnostic_WithCodeReplacement(t *testing.T) {
	file := "test.fer"
	loc := source.NewLocation(
		&file,
		&source.Position{Line: 4, Column: 1},
		&source.Position{Line: 4, Column: 8},
	)

	diag := NewError("replace this").
		WithCodeReplacement(loc, "@&x", "@x")

	if diag.CodeHint == nil {
		t.Fatal("expected code hint to be set")
	}
	if len(diag.CodeHint.Lines) != 2 {
		t.Fatalf("expected 2 code hint lines, got %d", len(diag.CodeHint.Lines))
	}
	if diag.CodeHint.Lines[0].Prefix != "-" {
		t.Fatalf("expected first replacement line to be '-', got %q", diag.CodeHint.Lines[0].Prefix)
	}
	if diag.CodeHint.Lines[1].Prefix != "+" {
		t.Fatalf("expected second replacement line to be '+', got %q", diag.CodeHint.Lines[1].Prefix)
	}
}
