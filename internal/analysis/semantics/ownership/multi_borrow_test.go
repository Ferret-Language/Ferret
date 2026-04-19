package ownership

import (
	"testing"

	"compiler/internal/analysis/semantics/typeinfo"
	corectx "compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	"compiler/internal/ir/mir"
)

func TestBindBorrowValueRejectsConflictingPlannedBorrows(t *testing.T) {
	ctx := corectx.New(".", ".fer", diagnostics.NewDiagnosticBag(""))
	a := &ownershipAnalyzer{
		ctx:      ctx,
		reported: make(map[string]struct{}),
		currentFn: &mir.Function{
			Locals: []*mir.Local{
				{ID: 0, Name: "x", Type: &typeinfo.BuiltinType{Name: "i32"}, Mutable: true},
				{ID: 1, Name: "tmp", Type: typeinfo.UnknownType{}},
			},
		},
	}
	scope := newValueScope()
	scope.Declare(0, valueInfo{typ: &typeinfo.BuiltinType{Name: "i32"}, mutable: true})
	slot := scope.Declare(1, valueInfo{typ: typeinfo.UnknownType{}})
	value := &mir.CompositeValue{
		Items: []mir.CompositeItem{
			{
				Value: &mir.AddrOfValue{
					Source: &mir.LocalValue{LocalID: 0},
				},
			},
			{
				Value: &mir.AddrOfValue{
					Source:  &mir.LocalValue{LocalID: 0},
					Mutable: true,
				},
			},
		},
	}

	a.bindBorrowValue(scope, slot, value)

	if len(slot.borrows) != 0 {
		t.Fatalf("expected conflicting planned borrows to be rejected, got %#v", slot.borrows)
	}
	for _, diag := range ctx.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrBorrowConflict {
			return
		}
	}
	t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrBorrowConflict, ctx.Diagnostics.Diagnostics())
}
