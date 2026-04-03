package backend

import (
	"errors"
	"testing"

	layout "compiler/internal/analysis/layout/model"
	"compiler/internal/analysis/semantics/typeinfo"
)

func TestLookupStructLayoutSupportsSliceLikeTypes(t *testing.T) {
	lookupNamed := func(*typeinfo.NamedType) (*layout.TypeLayout, error) {
		t.Fatal("lookupNamed should not be used for builtin slice-like layouts")
		return nil, nil
	}

	sliceLayout, err := LookupStructLayout(lookupNamed, &typeinfo.SliceType{Inner: &typeinfo.BuiltinType{Name: "u8"}})
	if err != nil {
		t.Fatalf("lookup slice layout: %v", err)
	}
	if got := len(sliceLayout.Fields); got != 2 {
		t.Fatalf("expected 2 slice fields, got %d", got)
	}
	if sliceLayout.Fields[0].Name != "ptr" || sliceLayout.Fields[1].Name != "len" {
		t.Fatalf("unexpected slice fields: %#v", sliceLayout.Fields)
	}

	ptrType, ok := sliceLayout.Fields[0].Type.(*typeinfo.RawPtrType)
	if !ok || !ptrType.Const || !typeinfo.Equal(ptrType.Inner, &typeinfo.BuiltinType{Name: "u8"}) {
		t.Fatalf("expected readonly slice ptr field type ^const u8, got %#v", sliceLayout.Fields[0].Type)
	}

	strLayout, err := LookupStructLayout(lookupNamed, &typeinfo.StringType{})
	if err != nil {
		t.Fatalf("lookup string layout: %v", err)
	}
	strPtrType, ok := strLayout.Fields[0].Type.(*typeinfo.RawPtrType)
	if !ok || !strPtrType.Const || !typeinfo.Equal(strPtrType.Inner, &typeinfo.BuiltinType{Name: "u8"}) {
		t.Fatalf("expected str ptr field type ^const u8, got %#v", strLayout.Fields[0].Type)
	}
}

func TestAggregateSizeAlignSupportsErrorUnions(t *testing.T) {
	ctx := AggregateLayoutContext{
		BackendName: "test",
		ScalarSizeAlign: func(typ typeinfo.Type) (int64, int64, error) {
			switch t := typ.(type) {
			case *typeinfo.BuiltinType:
				switch t.Name {
				case "i32":
					return 4, 4, nil
				case "usize":
					return 8, 8, nil
				}
			}
			return 0, 0, errors.New("unsupported type")
		},
	}

	size, align, err := AggregateSizeAlign(ctx, &typeinfo.ErrorUnionType{
		Error: &typeinfo.BuiltinType{Name: "i32"},
		Value: &typeinfo.BuiltinType{Name: "usize"},
	})
	if err != nil {
		t.Fatalf("aggregate size for error union: %v", err)
	}
	if size != 16 || align != 8 {
		t.Fatalf("expected size=16 align=8, got size=%d align=%d", size, align)
	}
}
