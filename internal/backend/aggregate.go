package backend

import (
	"fmt"

	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/ir/mir"
)

// ResolveAggregateSource resolves a value used as the backing storage/source
// for aggregate operations. Backends provide target-specific lowering
// callbacks for each supported MIR value shape.
func ResolveAggregateSource(
	value mir.Value,
	onLocal func(*mir.LocalValue) ([]string, string, error),
	onName func(*mir.NameValue) ([]string, string, error),
	onLoadPointer func(mir.Value) ([]string, string, error),
	onFieldLoad func(base mir.Value, fieldIndex int) ([]string, string, error),
	onIndexLoad func(base mir.Value, index mir.Value) ([]string, string, error),
) ([]string, string, error) {
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
	case *mir.IndexValue:
		if onIndexLoad == nil {
			break
		}
		return onIndexLoad(v.Base, v.Index)
	}
	return nil, "", fmt.Errorf("unsupported aggregate source %T", value)
}

func ResolveTaggedUnionComposite(target typeinfo.Type, value mir.Value) (mir.Value, mir.Value, bool) {
	comp, ok := value.(*mir.CompositeValue)
	if !ok || comp == nil || len(comp.Items) < 2 {
		return nil, nil, false
	}
	if !typeinfo.Equal(target, comp.Type()) && !typeinfo.Equal(UnwrapNamed(target), UnwrapNamed(comp.Type())) {
		return nil, nil, false
	}
	return comp.Items[0].Value, comp.Items[1].Value, true
}
