package common

import "compiler/internal/ir/mir"

// AddrSourceToPlace converts an addr_of source value into an lvalue place when possible.
// It preserves selector/index structure so backends can compute direct field/element addresses.
func AddrSourceToPlace(fn *mir.Function, src mir.Value) (mir.Place, bool) {
	switch v := src.(type) {
	case nil:
		return nil, false
	case *mir.LocalValue:
		return &mir.LocalPlace{LocalID: v.LocalID}, true
	case *mir.NameValue:
		if len(v.Path) == 1 {
			if local := FindLocalByName(fn, v.Path[0]); local != nil {
				return &mir.LocalPlace{LocalID: local.ID}, true
			}
		}
		return nil, false
	case *mir.FieldLoadValue:
		base, ok := AddrSourceToPlace(fn, v.Base)
		if !ok || v.FieldIndex < 0 {
			return nil, false
		}
		return &mir.FieldPlace{Base: base, FieldIndex: v.FieldIndex}, true
	case *mir.FieldValue:
		base, ok := AddrSourceToPlace(fn, v.Base)
		if !ok || v.FieldIndex < 0 {
			return nil, false
		}
		return &mir.FieldPlace{Base: base, FieldIndex: v.FieldIndex}, true
	case *mir.IndexValue:
		base, ok := AddrSourceToPlace(fn, v.Base)
		if !ok {
			return nil, false
		}
		return &mir.IndexPlace{Base: base, Index: v.Index}, true
	case *mir.LoadValue:
		return &mir.DerefPlace{Pointer: v.Pointer}, true
	default:
		return nil, false
	}
}
