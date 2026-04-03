package backend

import (
	"testing"

	"compiler/internal/ir/mir"
)

func TestResolveAggregateSourceLocal(t *testing.T) {
	value := &mir.LocalValue{LocalID: 1}
	got, err := ResolveAggregateSource(
		value,
		func(v *mir.LocalValue) (string, error) {
			if v != value {
				t.Fatalf("unexpected local value")
			}
			return "%local", nil
		},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "%local" {
		t.Fatalf("expected %%local, got %q", got)
	}
}

func TestResolveAggregateSourceName(t *testing.T) {
	value := &mir.NameValue{Path: []string{"Global"}}
	got, err := ResolveAggregateSource(
		value,
		nil,
		func(v *mir.NameValue) (string, error) {
			if v != value {
				t.Fatalf("unexpected name value")
			}
			return "@global", nil
		},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "@global" {
		t.Fatalf("expected @global, got %q", got)
	}
}

func TestResolveAggregateSourceLoadPointer(t *testing.T) {
	ptr := &mir.LocalValue{LocalID: 2}
	value := &mir.LoadValue{Pointer: ptr}
	got, err := ResolveAggregateSource(
		value,
		nil,
		nil,
		func(v mir.Value) (string, error) {
			if v != ptr {
				t.Fatalf("unexpected load pointer")
			}
			return "%ptr", nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "%ptr" {
		t.Fatalf("expected %%ptr, got %q", got)
	}
}

func TestResolveAggregateSourceFieldLoad(t *testing.T) {
	base := &mir.LocalValue{LocalID: 3}
	value := &mir.FieldLoadValue{Base: base, FieldIndex: 7}
	got, err := ResolveAggregateSource(
		value,
		nil,
		nil,
		nil,
		func(v mir.Value, index int) (string, error) {
			if v != base || index != 7 {
				t.Fatalf("unexpected field load inputs")
			}
			return "%field_addr", nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "%field_addr" {
		t.Fatalf("expected %%field_addr, got %q", got)
	}
}

func TestResolveAggregateSourceUnsupported(t *testing.T) {
	value := &mir.NumberValue{Value: "1"}
	if _, err := ResolveAggregateSource(value, nil, nil, nil, nil); err == nil {
		t.Fatalf("expected unsupported aggregate source error")
	}
}

func TestResolveTaggedUnionComposite(t *testing.T) {
	tag := &mir.NumberValue{Value: "1"}
	payload := &mir.NumberValue{Value: "41"}
	value := &mir.CompositeValue{
		Items: []mir.CompositeItem{
			{Value: tag},
			{Value: payload},
		},
	}

	gotTag, gotPayload, ok := ResolveTaggedUnionComposite(nil, value)
	if !ok {
		t.Fatalf("expected tagged union composite resolution")
	}
	if gotTag != tag || gotPayload != payload {
		t.Fatalf("unexpected resolved values: %#v %#v", gotTag, gotPayload)
	}
}
