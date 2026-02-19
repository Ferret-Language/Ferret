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

func TestBorrowCheckerImplicitMoveUseAfterMove(t *testing.T) {
	src := `fn test() {
		let a := [1, 2, 3];
		let b := a;
		let c := a;
	}`

	ctx := analyzeHIR(t, src)
	diags := ctx.Diagnostics.Diagnostics()
	if !hasMessage(diags, "use of moved value") {
		t.Fatalf("expected use-after-move diagnostic, got %d diagnostics", len(diags))
	}
}

func TestBorrowCheckerImplicitPrimitiveCopyNoMove(t *testing.T) {
	src := `fn test() {
		let a := 1;
		let b := a;
		let c := a;
		let d := b + c;
	}`

	ctx := analyzeHIR(t, src)
	diags := ctx.Diagnostics.Diagnostics()
	if hasError(diags) {
		t.Fatalf("expected no borrow errors for primitive copies, got %d diagnostics", len(diags))
	}
}

func TestBorrowCheckerImplicitMoveFromSelectorUseAfterMove(t *testing.T) {
	src := `type S struct {
	.X: []i32,
};

fn test() {
	let s: S = { .X = [1, 2, 3] };
	let a := s.X;
	let b := s.X;
}`

	ctx := analyzeHIR(t, src)
	diags := ctx.Diagnostics.Diagnostics()
	if !hasMessage(diags, "use of moved value") {
		t.Fatalf("expected use-after-move diagnostic for selector move, got %d diagnostics", len(diags))
	}
}

func TestBorrowCheckerPipeArgumentImplicitMoveUseAfterMove(t *testing.T) {
	src := `type S struct {
	.X: []i32,
};

fn consume(v: []i32) {}

fn test() {
	let s: S = { .X = [1, 2, 3] };
	s.X |> consume(_);
	let a := s.X;
}`

	ctx := analyzeHIR(t, src)
	diags := ctx.Diagnostics.Diagnostics()
	if !hasMessage(diags, "use of moved value") {
		t.Fatalf("expected use-after-move diagnostic after pipe argument move, got %d diagnostics", len(diags))
	}
}

func TestBorrowCheckerValueParamImplicitlyConsumesBinding(t *testing.T) {
	src := `fn consume(v: #i32) {
}

fn test() {
	let n := #7;
	consume(n);
	let again := n;
}`

	ctx := analyzeHIR(t, src)
	diags := ctx.Diagnostics.Diagnostics()
	if !hasMessage(diags, "use of moved value") {
		t.Fatalf("expected use-after-move diagnostic for value parameter call, got %d diagnostics", len(diags))
	}
}
