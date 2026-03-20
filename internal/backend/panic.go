package backend

import (
	"fmt"

	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/ir/mir"
)

type PanicPayloadKind uint8

const (
	PanicPayloadInvalid PanicPayloadKind = iota
	PanicPayloadLiteralString
	PanicPayloadDynamicString
)

type PanicPayload struct {
	Kind    PanicPayloadKind
	Literal string
	Value   mir.Value
}

// ClassifyPanicPayload determines whether a panic payload is a compile-time
// string literal or a runtime string value, leaving backend syntax emission
// to target-specific code.
func ClassifyPanicPayload(term *mir.PanicTerm, isStringType func(typeinfo.Type) bool) (PanicPayload, error) {
	if term == nil || term.Value == nil {
		return PanicPayload{}, fmt.Errorf("panic: unsupported payload type %T", nil)
	}
	switch v := term.Value.(type) {
	case *mir.StringValue:
		return PanicPayload{Kind: PanicPayloadLiteralString, Literal: v.Value, Value: v}, nil
	default:
		if isStringType != nil && isStringType(term.Value.Type()) {
			return PanicPayload{Kind: PanicPayloadDynamicString, Value: term.Value}, nil
		}
		return PanicPayload{}, fmt.Errorf("panic: unsupported payload type %T", term.Value)
	}
}

// ResolvePanicStringAddress resolves an address/operand for runtime string
// panic payloads. It first tries addrOf and falls back to valueOf.
func ResolvePanicStringAddress(
	value mir.Value,
	addrOf func(mir.Value) (string, error),
	valueOf func(mir.Value) (string, error),
) (string, error) {
	if value == nil {
		return "", fmt.Errorf("panic: unsupported string payload (%T)", value)
	}
	var addrErr error
	if addrOf != nil {
		if addr, err := addrOf(value); err == nil {
			return addr, nil
		} else {
			addrErr = err
		}
	}
	if valueOf != nil {
		if lv, err := valueOf(value); err == nil {
			return lv, nil
		}
	}
	if addrErr != nil {
		return "", fmt.Errorf("panic: unsupported string payload (%T): %w", value, addrErr)
	}
	return "", fmt.Errorf("panic: unsupported string payload (%T)", value)
}
