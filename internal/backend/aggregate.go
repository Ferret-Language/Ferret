package backend

import (
	"fmt"

	"compiler/internal/ir/mir"
)

// ResolveAggregateSource resolves a value used as the backing storage/source
// for aggregate operations. Backends provide target-specific lowering
// callbacks for each supported MIR value shape.
func ResolveAggregateSource(
	value mir.Value,
	onLocal func(*mir.LocalValue) (string, error),
	onName func(*mir.NameValue) (string, error),
	onLoadPointer func(mir.Value) (string, error),
	onFieldLoad func(base mir.Value, fieldIndex int) (string, error),
) (string, error) {
	switch v := value.(type) {
	case *mir.LocalValue:
		if onLocal == nil {
			break
		}
		return onLocal(v)
	case *mir.NameValue:
		if onName == nil {
			break
		}
		return onName(v)
	case *mir.LoadValue:
		if onLoadPointer == nil {
			break
		}
		return onLoadPointer(v.Pointer)
	case *mir.FieldLoadValue:
		if onFieldLoad == nil {
			break
		}
		return onFieldLoad(v.Base, v.FieldIndex)
	}
	return "", fmt.Errorf("unsupported aggregate source %T", value)
}
