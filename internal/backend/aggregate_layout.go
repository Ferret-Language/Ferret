package backend

import (
	"fmt"

	layout "compiler/internal/analysis/layout/model"
	"compiler/internal/analysis/semantics/typeinfo"
)

type AggregateLayoutContext struct {
	BackendName      string
	ScalarSizeAlign  func(typeinfo.Type) (int64, int64, error)
	OptionalSizeFunc func(typeinfo.Type) (int64, int64, error)
	LookupNamed      func(*typeinfo.NamedType) (*layout.TypeLayout, error)
}

type TupleElementLayout struct {
	Index  int
	Type   typeinfo.Type
	Offset int64
	Size   int64
	Align  int64
}

func AlignUpInt64(size, align int64) int64 {
	if align <= 1 {
		return size
	}
	return (size + align - 1) &^ (align - 1)
}

func LookupNamedLayout(
	layouts map[string]*layout.Module,
	currentLayout *layout.Module,
	currentModuleKey string,
	named *typeinfo.NamedType,
	backendName string,
) (*layout.TypeLayout, error) {
	if named == nil {
		return nil, fmt.Errorf("nil named type")
	}
	if layouts != nil {
		if lm, ok := layouts[named.ModuleKey]; ok && lm != nil {
			if info, ok := lm.Lookup(named.Name); ok {
				return info, nil
			}
		}
	}
	if currentLayout != nil && named.ModuleKey == currentModuleKey {
		if info, ok := currentLayout.Lookup(named.Name); ok {
			return info, nil
		}
	}
	return nil, fmt.Errorf("layout for named type %s is not available in %s backend", named.String(), backendName)
}

func LookupStructLayout(
	lookupNamed func(*typeinfo.NamedType) (*layout.TypeLayout, error),
	typ typeinfo.Type,
) (*layout.StructLayout, error) {
	switch t := typ.(type) {
	case *typeinfo.BuiltinType:
		return nil, fmt.Errorf("builtin %s is not a struct layout", t.Name)
	case *typeinfo.NamedType:
		info, err := lookupNamed(t)
		if err != nil {
			return nil, err
		}
		if info == nil || info.Struct == nil {
			return nil, fmt.Errorf("type %s is not a struct layout", t.String())
		}
		return info.Struct, nil
	case *typeinfo.PointerType:
		return LookupStructLayout(lookupNamed, t.Inner)
	case *typeinfo.RefType:
		return LookupStructLayout(lookupNamed, t.Inner)
	case *typeinfo.RawPtrType:
		return LookupStructLayout(lookupNamed, t.Inner)
	default:
		return nil, fmt.Errorf("unsupported struct base type %s", typeinfo.FormatType(typ))
	}
}

func aggregateElementSizeAlign(ctx AggregateLayoutContext, typ typeinfo.Type) (int64, int64, error) {
	if ctx.ScalarSizeAlign != nil {
		if size, align, err := ctx.ScalarSizeAlign(typ); err == nil {
			return size, align, nil
		}
	}
	return AggregateSizeAlign(ctx, typ)
}

func TupleLayout(ctx AggregateLayoutContext, tuple *typeinfo.TupleType) ([]TupleElementLayout, int64, int64, error) {
	if tuple == nil {
		return nil, 0, 0, fmt.Errorf("nil tuple type")
	}
	entries := make([]TupleElementLayout, 0, len(tuple.Elems))
	size := int64(0)
	align := int64(1)
	for i, elem := range tuple.Elems {
		elemSize, elemAlign, err := aggregateElementSizeAlign(ctx, elem)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("unsupported tuple element %d type %s", i, typeinfo.FormatType(elem))
		}
		if elemAlign <= 0 {
			elemAlign = 1
		}
		size = AlignUpInt64(size, elemAlign)
		entries = append(entries, TupleElementLayout{
			Index:  i,
			Type:   elem,
			Offset: size,
			Size:   elemSize,
			Align:  elemAlign,
		})
		size += elemSize
		if elemAlign > align {
			align = elemAlign
		}
	}
	size = AlignUpInt64(size, align)
	return entries, size, align, nil
}

func AggregateSizeAlign(ctx AggregateLayoutContext, typ typeinfo.Type) (int64, int64, error) {
	switch t := typ.(type) {
	case *typeinfo.BuiltinType:
		return 0, 0, fmt.Errorf("unsupported aggregate builtin %s", t.Name)
	case *typeinfo.ArrayType:
		if t.Len < 0 {
			return 0, 0, fmt.Errorf("array with unknown length")
		}
		elemSize, elemAlign, err := aggregateElementSizeAlign(ctx, t.Inner)
		if err != nil {
			return 0, 0, fmt.Errorf("unsupported array element type %s", t.Inner)
		}
		stride := AlignUpInt64(elemSize, elemAlign)
		return stride * t.Len, elemAlign, nil
	case *typeinfo.TupleType:
		_, size, align, err := TupleLayout(ctx, t)
		if err != nil {
			return 0, 0, err
		}
		return size, align, nil
	case *typeinfo.StringType:
		return 16, 8, nil
	case *typeinfo.SliceType:
		return 16, 8, nil
	case *typeinfo.OptionalType:
		if OptionalUsesNiche(t.Inner) {
			return 0, 0, fmt.Errorf("optional %s uses niche layout", t.Inner)
		}
		if ctx.OptionalSizeFunc == nil {
			return 0, 0, fmt.Errorf("aggregate size context missing optional size resolver")
		}
		return ctx.OptionalSizeFunc(typ)
	case *typeinfo.NamedType:
		if IsNamedInterface(t) {
			return 16, 8, nil
		}
		if ctx.LookupNamed == nil {
			return 0, 0, fmt.Errorf("aggregate size context missing named layout resolver")
		}
		info, err := ctx.LookupNamed(t)
		if err != nil {
			return 0, 0, err
		}
		if info == nil || !info.Known {
			return 0, 0, fmt.Errorf("unknown aggregate layout for %s", t.String())
		}
		return info.Size, info.Align, nil
	default:
		return 0, 0, fmt.Errorf("unsupported aggregate type %s", typeinfo.FormatType(typ))
	}
}
