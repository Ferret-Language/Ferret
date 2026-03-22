package common

import (
	"testing"

	"compiler/internal/ir/mir"
)

func TestSplitSliceCompositeEmptyTreatsAsPositional(t *testing.T) {
	ptr, length, elems, err := SplitSliceComposite(&mir.CompositeValue{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ptr != nil || length != nil {
		t.Fatalf("expected nil ptr/len for empty positional slice, got ptr=%#v len=%#v", ptr, length)
	}
	if elems == nil {
		t.Fatal("expected empty elems slice, got nil")
	}
	if len(elems) != 0 {
		t.Fatalf("expected zero elems, got %d", len(elems))
	}
}

func TestSplitSliceCompositeNamedFields(t *testing.T) {
	ptrValue := &mir.NumberValue{Value: "1"}
	lenValue := &mir.NumberValue{Value: "2"}
	comp := &mir.CompositeValue{
		Items: []mir.CompositeItem{
			{Name: "ptr", Value: ptrValue},
			{Name: "len", Value: lenValue},
		},
	}

	ptr, length, elems, err := SplitSliceComposite(comp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ptr != ptrValue || length != lenValue {
		t.Fatalf("unexpected ptr/len values: ptr=%#v len=%#v", ptr, length)
	}
	if elems != nil {
		t.Fatalf("expected nil elems for named form, got %#v", elems)
	}
}
