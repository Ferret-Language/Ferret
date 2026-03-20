package backend

import (
	"errors"
	"testing"

	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/ir/mir"
)

func TestClassifyPanicPayloadLiteralString(t *testing.T) {
	term := &mir.PanicTerm{Value: &mir.StringValue{Value: "boom"}}
	payload, err := ClassifyPanicPayload(term, nil)
	if err != nil {
		t.Fatalf("unexpected classify error: %v", err)
	}
	if payload.Kind != PanicPayloadLiteralString {
		t.Fatalf("expected literal payload kind, got %v", payload.Kind)
	}
	if payload.Literal != "boom" {
		t.Fatalf("expected literal %q, got %q", "boom", payload.Literal)
	}
}

func TestClassifyPanicPayloadDynamicString(t *testing.T) {
	value := &mir.NameValue{Path: []string{"msg"}}
	term := &mir.PanicTerm{Value: value}
	payload, err := ClassifyPanicPayload(term, func(_ typeinfo.Type) bool { return true })
	if err != nil {
		t.Fatalf("unexpected classify error: %v", err)
	}
	if payload.Kind != PanicPayloadDynamicString {
		t.Fatalf("expected dynamic payload kind, got %v", payload.Kind)
	}
	if payload.Value != value {
		t.Fatalf("expected payload value pointer to be preserved")
	}
}

func TestClassifyPanicPayloadUnsupported(t *testing.T) {
	term := &mir.PanicTerm{Value: &mir.NumberValue{Value: "1"}}
	_, err := ClassifyPanicPayload(term, func(_ typeinfo.Type) bool { return false })
	if err == nil {
		t.Fatalf("expected unsupported payload error")
	}
}

func TestResolvePanicStringAddressPrefersAddrOf(t *testing.T) {
	value := &mir.NameValue{Path: []string{"msg"}}
	addr, err := ResolvePanicStringAddress(
		value,
		func(v mir.Value) (string, error) {
			if v != value {
				t.Fatalf("addrOf called with unexpected value")
			}
			return "%addr", nil
		},
		func(mir.Value) (string, error) {
			t.Fatalf("valueOf should not be called when addrOf succeeds")
			return "", nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}
	if addr != "%addr" {
		t.Fatalf("expected %q, got %q", "%addr", addr)
	}
}

func TestResolvePanicStringAddressFallsBackToValue(t *testing.T) {
	value := &mir.NameValue{Path: []string{"msg"}}
	addr, err := ResolvePanicStringAddress(
		value,
		func(mir.Value) (string, error) { return "", errors.New("no-address") },
		func(v mir.Value) (string, error) {
			if v != value {
				t.Fatalf("valueOf called with unexpected value")
			}
			return "%val", nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}
	if addr != "%val" {
		t.Fatalf("expected %q, got %q", "%val", addr)
	}
}

func TestResolvePanicStringAddressErrorsWhenUnresolvable(t *testing.T) {
	value := &mir.NameValue{Path: []string{"msg"}}
	_, err := ResolvePanicStringAddress(
		value,
		func(mir.Value) (string, error) { return "", errors.New("addr failed") },
		func(mir.Value) (string, error) { return "", errors.New("value failed") },
	)
	if err == nil {
		t.Fatalf("expected unresolved payload error")
	}
}
