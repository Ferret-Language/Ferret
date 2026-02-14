package analysis_test

import (
	"compiler/internal/diagnostics"
	"strings"
	"testing"
)

func hasError(diags []*diagnostics.Diagnostic) bool {
	for _, diag := range diags {
		if diag.Severity == diagnostics.Error {
			return true
		}
	}
	return false
}

func hasMessage(diags []*diagnostics.Diagnostic, substr string) bool {
	for _, diag := range diags {
		if strings.Contains(diag.Message, substr) {
			return true
		}
	}
	return false
}

func TestBorrowCheckerSharedReborrowFromMut(t *testing.T) {
	src := `fn test() {
		let a := 1;
		let m := &mut a;
		let r := &*m;
		let x := *r;
		*m = 2;
		let y := x;
	}`

	ctx := analyzeHIR(t, src)
	diags := ctx.Diagnostics.Diagnostics()
	if hasError(diags) {
		t.Fatalf("expected no borrow errors, got %d diagnostics", len(diags))
	}
}

func TestBorrowCheckerMutReborrowChain(t *testing.T) {
	src := `fn test() {
		let a := 1;
		let m := &mut a;
		{
			let m2 := &mut *m;
			*m2 = 1;
		}
		*m = 2;
	}`

	ctx := analyzeHIR(t, src)
	diags := ctx.Diagnostics.Diagnostics()
	if hasError(diags) {
		t.Fatalf("expected no borrow errors, got %d diagnostics", len(diags))
	}
}

func TestBorrowCheckerMutableBorrowThroughSharedRef(t *testing.T) {
	src := `fn test() {
		let a := 1;
		let r := &a;
		let m := &mut *r;
	}`

	ctx := analyzeHIR(t, src)
	diags := ctx.Diagnostics.Diagnostics()
	if !hasMessage(diags, "cannot take mutable reference through immutable reference") {
		t.Fatalf("expected mutable-through-shared borrow error, got %d diagnostics", len(diags))
	}
}

func TestBorrowCheckerTwoMutReborrows(t *testing.T) {
	src := `fn test() {
		let a := 1;
		let m := &mut a;
		let m1 := m;
		let m2 := m;
	}`

	ctx := analyzeHIR(t, src)
	diags := ctx.Diagnostics.Diagnostics()
	if !hasMessage(diags, "cannot borrow") {
		t.Fatalf("expected borrow conflict error, got %d diagnostics", len(diags))
	}
}

func TestBorrowCheckerReturnHeapContainingLocalReferenceRejected(t *testing.T) {
	src := `type Cell struct {
	.Value: &i32,
};

fn make_cell() -> #Cell {
	let a := 10;
	let c: #Cell = #{ .Value = &a } as Cell;
	return c;
}`

	ctx := analyzeHIR(t, src)
	diags := ctx.Diagnostics.Diagnostics()
	if !hasMessage(diags, "cannot return value containing reference to local") {
		t.Fatalf("expected escaping local reference error, got %d diagnostics", len(diags))
	}
}
