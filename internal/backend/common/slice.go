package common

import (
	"fmt"

	"compiler/internal/ir/mir"
)

// SplitSliceComposite classifies a slice-like composite value into one of:
// 1) named form: { ptr = ..., len = ... }
// 2) element form: { e0, e1, ... }
func SplitSliceComposite(comp *mir.CompositeValue) (ptr mir.Value, length mir.Value, elems []mir.Value, err error) {
	if comp == nil {
		return nil, nil, nil, fmt.Errorf("slice aggregate value must be composite")
	}
	if len(comp.Items) == 0 {
		return nil, nil, []mir.Value{}, nil
	}

	hasNamed := false
	hasUnnamed := false
	for _, item := range comp.Items {
		if item.Name == "" {
			hasUnnamed = true
			continue
		}
		hasNamed = true
	}
	if hasNamed && hasUnnamed {
		return nil, nil, nil, fmt.Errorf("slice composite cannot mix named and positional elements")
	}
	if hasUnnamed {
		out := make([]mir.Value, 0, len(comp.Items))
		for _, item := range comp.Items {
			out = append(out, item.Value)
		}
		return nil, nil, out, nil
	}

	for _, item := range comp.Items {
		switch item.Name {
		case "ptr":
			if ptr != nil {
				return nil, nil, nil, fmt.Errorf("slice composite has duplicate ptr field")
			}
			ptr = item.Value
		case "len":
			if length != nil {
				return nil, nil, nil, fmt.Errorf("slice composite has duplicate len field")
			}
			length = item.Value
		default:
			return nil, nil, nil, fmt.Errorf("slice composite has unsupported named field %q", item.Name)
		}
	}
	if ptr == nil || length == nil {
		return nil, nil, nil, fmt.Errorf("slice composite requires ptr and len fields")
	}
	return ptr, length, nil, nil
}
