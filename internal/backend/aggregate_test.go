package backend

import (
	"testing"

	"compiler/internal/ir/mir"
)

func TestResolveAggregateSourceLocal(t *testing.T) {
	value := &mir.LocalValue{LocalID: 1}
	lines, got, err := ResolveAggregateSource(
		value,
		func(v *mir.LocalValue) ([]string, string, error) {
			if v != value {
				t.Fatalf("unexpected local value")
			}
			return nil, "%local", nil
		},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("expected no prep lines, got %#v", lines)
	}
	if got != "%local" {
		t.Fatalf("expected %%local, got %q", got)
	}
}

func TestResolveAggregateSourceName(t *testing.T) {
	value := &mir.NameValue{Path: []string{"Global"}}
	lines, got, err := ResolveAggregateSource(
		value,
		nil,
		func(v *mir.NameValue) ([]string, string, error) {
			if v != value {
				t.Fatalf("unexpected name value")
			}
			return nil, "@global", nil
		},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("expected no prep lines, got %#v", lines)
	}
	if got != "@global" {
		t.Fatalf("expected @global, got %q", got)
	}
}

func TestResolveAggregateSourceLoadPointer(t *testing.T) {
	ptr := &mir.LocalValue{LocalID: 2}
	value := &mir.LoadValue{Pointer: ptr}
	lines, got, err := ResolveAggregateSource(
		value,
		nil,
		nil,
		func(v mir.Value) ([]string, string, error) {
			if v != ptr {
				t.Fatalf("unexpected load pointer")
			}
			return nil, "%ptr", nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("expected no prep lines, got %#v", lines)
	}
	if got != "%ptr" {
		t.Fatalf("expected %%ptr, got %q", got)
	}
}

func TestResolveAggregateSourceFieldLoad(t *testing.T) {
	base := &mir.LocalValue{LocalID: 3}
	value := &mir.FieldLoadValue{Base: base, FieldIndex: 7}
	lines, got, err := ResolveAggregateSource(
		value,
		nil,
		nil,
		nil,
		func(v mir.Value, index int) ([]string, string, error) {
			if v != base || index != 7 {
				t.Fatalf("unexpected field load inputs")
			}
			return []string{"%tmp = add"}, "%field_addr", nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 1 || lines[0] != "%tmp = add" {
		t.Fatalf("unexpected prep lines: %#v", lines)
	}
	if got != "%field_addr" {
		t.Fatalf("expected %%field_addr, got %q", got)
	}
}

func TestResolveAggregateSourceUnsupported(t *testing.T) {
	value := &mir.NumberValue{Value: "1"}
	if _, _, err := ResolveAggregateSource(value, nil, nil, nil, nil); err == nil {
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
