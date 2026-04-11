package backend

import (
	"errors"
	"reflect"
	"testing"

	layout "compiler/internal/analysis/layout/model"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/abi"
	"compiler/internal/frontend/ast"
)

func TestUnwrapNamedEnumAndError(t *testing.T) {
	enumType := &typeinfo.NamedType{
		Name: "Color",
		Decl: &ast.TypeDecl{Type: &ast.EnumType{}},
	}
	errType := &typeinfo.NamedType{
		Name: "FileError",
		Decl: &ast.TypeDecl{Type: &ast.ErrorType{}},
	}
	for _, in := range []typeinfo.Type{enumType, errType} {
		out := UnwrapNamed(in)
		b, ok := out.(*typeinfo.BuiltinType)
		if !ok || b.Name != "i32" {
			t.Fatalf("expected i32 builtin for %T, got %T (%v)", in, out, out)
		}
	}
}

func TestUnwrapNamedLeavesNonEnumErrorUnchanged(t *testing.T) {
	in := &typeinfo.NamedType{
		Name: "Point",
		Decl: &ast.TypeDecl{Type: &ast.StructType{}},
	}
	if got := UnwrapNamed(in); got != in {
		t.Fatalf("expected non enum/error named type to remain unchanged")
	}
}

func TestIsNamedUnionAndInterface(t *testing.T) {
	union := &typeinfo.NamedType{Name: "Token", Decl: &ast.TypeDecl{Type: &ast.UnionType{}}}
	iface := &typeinfo.NamedType{Name: "Shape", Decl: &ast.TypeDecl{Type: &ast.InterfaceType{}}}
	if !IsNamedUnion(union) {
		t.Fatalf("expected union detection")
	}
	if IsNamedInterface(union) {
		t.Fatalf("did not expect interface detection for union")
	}
	if !IsNamedInterface(iface) {
		t.Fatalf("expected interface detection")
	}
	if IsNamedUnion(iface) {
		t.Fatalf("did not expect union detection for interface")
	}
}

func TestIsVoidType(t *testing.T) {
	if !IsVoidType(nil) {
		t.Fatalf("nil type should be treated as void")
	}
	if !IsVoidType(&typeinfo.BuiltinType{Name: "void"}) {
		t.Fatalf("void builtin should be void")
	}
	enumType := &typeinfo.NamedType{
		Name: "Color",
		Decl: &ast.TypeDecl{Type: &ast.EnumType{}},
	}
	if IsVoidType(enumType) {
		t.Fatalf("enum lowered to i32 should not be void")
	}
}

func TestDescribeRuntimeTypeMarksArbitraryWidthIntegers(t *testing.T) {
	desc := DescribeRuntimeType(&typeinfo.BuiltinType{Name: "i128"})
	if desc.Name != "i128" {
		t.Fatalf("expected i128 runtime type name, got %q", desc.Name)
	}
	if desc.ID != RuntimeTypeUnknown {
		t.Fatalf("expected i128 runtime type id to remain unknown, got %d", desc.ID)
	}
	if desc.Flags&RuntimeTypeFlagInteger == 0 || desc.Flags&RuntimeTypeFlagSigned == 0 {
		t.Fatalf("expected i128 runtime flags to mark signed integer, got %#x", desc.Flags)
	}

	desc = DescribeRuntimeType(&typeinfo.BuiltinType{Name: "u1024"})
	if desc.Flags&RuntimeTypeFlagInteger == 0 {
		t.Fatalf("expected u1024 runtime flags to mark integer, got %#x", desc.Flags)
	}
	if desc.Flags&RuntimeTypeFlagSigned != 0 {
		t.Fatalf("expected u1024 runtime flags to stay unsigned, got %#x", desc.Flags)
	}
}

func TestDescribeRuntimeTypeCapturesNamedVariantMetadata(t *testing.T) {
	enumDesc := DescribeRuntimeType(&typeinfo.NamedType{
		Name: "Color",
		Decl: &ast.TypeDecl{Type: &ast.EnumType{
			Variants: []*ast.EnumVariant{
				{Name: &ast.Ident{Path: []string{"Red"}}},
				{Name: &ast.Ident{Path: []string{"Green"}}},
			},
		}},
	})
	if enumDesc.Flags&RuntimeTypeFlagVariants == 0 {
		t.Fatalf("expected enum descriptor to mark variants, got %#x", enumDesc.Flags)
	}
	if !reflect.DeepEqual(enumDesc.Variants, []string{"Red", "Green"}) {
		t.Fatalf("unexpected enum variants: %#v", enumDesc.Variants)
	}

	errorDesc := DescribeRuntimeType(&typeinfo.NamedType{
		Name: "Io",
		Decl: &ast.TypeDecl{Type: &ast.ErrorType{
			Members: []*ast.ErrorMember{
				{Name: &ast.Ident{Path: []string{"denied"}}},
				{Name: &ast.Ident{Path: []string{"timeout"}}},
			},
		}},
	})
	if errorDesc.Flags&RuntimeTypeFlagVariants == 0 {
		t.Fatalf("expected error descriptor to mark variants, got %#x", errorDesc.Flags)
	}
	if !reflect.DeepEqual(errorDesc.Variants, []string{"denied", "timeout"}) {
		t.Fatalf("unexpected error variants: %#v", errorDesc.Variants)
	}
}

func TestDescribeRuntimeTypeLayoutCapturesCompositePrintShape(t *testing.T) {
	ctx := AggregateLayoutContext{
		BackendName: "test",
		ScalarSizeAlign: func(typ typeinfo.Type) (int64, int64, error) {
			switch t := typ.(type) {
			case *typeinfo.BuiltinType:
				switch t.Name {
				case "i32":
					return 4, 4, nil
				case "bool":
					return 1, 1, nil
				case "usize":
					return 8, 8, nil
				}
			case *typeinfo.StringType:
				return 16, 8, nil
			}
			return 0, 0, errors.New("unsupported type")
		},
	}

	arrayDesc, err := DescribeRuntimeTypeLayout(ctx, &typeinfo.ArrayType{Len: 3, Inner: &typeinfo.BuiltinType{Name: "i32"}})
	if err != nil {
		t.Fatalf("describe array layout: %v", err)
	}
	if arrayDesc.Flags&RuntimeTypeFlagArray == 0 || arrayDesc.Stride != 4 || arrayDesc.Length != 3 {
		t.Fatalf("unexpected array descriptor: %#v", arrayDesc)
	}

	sliceDesc, err := DescribeRuntimeTypeLayout(ctx, &typeinfo.SliceType{Inner: &typeinfo.BuiltinType{Name: "bool"}})
	if err != nil {
		t.Fatalf("describe slice layout: %v", err)
	}
	if sliceDesc.Flags&RuntimeTypeFlagSlice == 0 || sliceDesc.Stride != 1 {
		t.Fatalf("unexpected slice descriptor: %#v", sliceDesc)
	}

	tupleDesc, err := DescribeRuntimeTypeLayout(ctx, &typeinfo.TupleType{Elems: []typeinfo.Type{
		&typeinfo.BuiltinType{Name: "i32"},
		&typeinfo.BuiltinType{Name: "bool"},
		&typeinfo.StringType{},
	}})
	if err != nil {
		t.Fatalf("describe tuple layout: %v", err)
	}
	if tupleDesc.Flags&RuntimeTypeFlagTuple == 0 || len(tupleDesc.Fields) != 3 {
		t.Fatalf("unexpected tuple descriptor: %#v", tupleDesc)
	}
	if tupleDesc.Fields[0].Offset != 0 || tupleDesc.Fields[1].Offset != 4 || tupleDesc.Fields[2].Offset != 8 {
		t.Fatalf("unexpected tuple field offsets: %#v", tupleDesc.Fields)
	}

	optDesc, err := DescribeRuntimeTypeLayout(ctx, &typeinfo.OptionalType{Inner: &typeinfo.BuiltinType{Name: "i32"}})
	if err != nil {
		t.Fatalf("DescribeRuntimeTypeLayout(optional): %v", err)
	}
	if optDesc.Flags&RuntimeTypeFlagOptional == 0 || optDesc.Elem == nil || optDesc.PayloadOffset == 0 {
		t.Fatalf("unexpected optional runtime descriptor: %#v", optDesc)
	}
}

func TestResolveMapTypeFromNamedAlias(t *testing.T) {
	named := &typeinfo.NamedType{
		Name: "mymap",
		Decl: &ast.TypeDecl{
			Type: &ast.MapType{
				Key:   &ast.NamedType{Path: []string{"str"}},
				Value: &ast.NamedType{Path: []string{"str"}},
			},
		},
	}
	mt, ok := ResolveMapType(named)
	if !ok || mt == nil {
		t.Fatalf("expected map type from alias")
	}
	if _, ok := mt.Key.(*typeinfo.StringType); !ok {
		t.Fatalf("expected string key, got %T", mt.Key)
	}
	if _, ok := mt.Value.(*typeinfo.StringType); !ok {
		t.Fatalf("expected string value, got %T", mt.Value)
	}
}

func TestResolveMapTypeFromGenericAlias(t *testing.T) {
	named := &typeinfo.NamedType{
		Name:     "Table",
		TypeArgs: []typeinfo.Type{&typeinfo.StringType{}, &typeinfo.BuiltinType{Name: "i64"}},
		Decl: &ast.TypeDecl{
			TypeParams: []ast.TypeParam{
				{Name: &ast.Ident{Path: []string{"K"}}},
				{Name: &ast.Ident{Path: []string{"V"}}},
			},
			Type: &ast.MapType{
				Key:   &ast.NamedType{Path: []string{"K"}},
				Value: &ast.NamedType{Path: []string{"V"}},
			},
		},
	}
	mt, ok := ResolveMapType(named)
	if !ok || mt == nil {
		t.Fatalf("expected map type from generic alias")
	}
	if _, ok := mt.Key.(*typeinfo.StringType); !ok {
		t.Fatalf("expected bound string key, got %T", mt.Key)
	}
	if !typeinfo.IsBuiltinNamed(mt.Value, "i64") {
		t.Fatalf("expected bound i64 value, got %v", mt.Value)
	}
}

func TestDescribeRuntimeTypeLayoutCapturesNamedStructFields(t *testing.T) {
	point := &typeinfo.NamedType{
		Name: "Point",
		Decl: &ast.TypeDecl{Type: &ast.StructType{}},
	}
	ctx := AggregateLayoutContext{
		LookupNamed: func(named *typeinfo.NamedType) (*layout.TypeLayout, error) {
			if named == nil || named.Name != "Point" {
				return nil, errors.New("unexpected named type")
			}
			return &layout.TypeLayout{
				Known: true,
				Struct: &layout.StructLayout{
					Fields: []*layout.FieldLayout{
						{
							Name:          "x",
							Type:          &typeinfo.BuiltinType{Name: "i32"},
							SemanticIndex: 0,
							PhysicalIndex: 0,
							Offset:        0,
							Size:          4,
							Align:         4,
						},
						{
							Name:          "y",
							Type:          &typeinfo.BuiltinType{Name: "i32"},
							SemanticIndex: 1,
							PhysicalIndex: 1,
							Offset:        4,
							Size:          4,
							Align:         4,
						},
					},
					PhysicalOrder: []int{0, 1},
					Size:          8,
					Align:         4,
				},
			}, nil
		},
	}

	desc, err := DescribeRuntimeTypeLayout(ctx, point)
	if err != nil {
		t.Fatalf("DescribeRuntimeTypeLayout(named struct): %v", err)
	}
	if desc.Flags&RuntimeTypeFlagStruct == 0 {
		t.Fatalf("expected struct runtime flag, got %#x", desc.Flags)
	}
	if len(desc.Fields) != 2 || desc.Fields[0].Offset != 0 || desc.Fields[1].Offset != 4 {
		t.Fatalf("unexpected struct field descriptor list: %#v", desc.Fields)
	}
}

func TestLookupStructLayoutAnonymousStructSupportsMapField(t *testing.T) {
	st := &typeinfo.StructType{
		OrderedFields: []*typeinfo.StructField{
			{
				Name: "items",
				Type: &typeinfo.MapType{
					Key:   &typeinfo.StringType{},
					Value: &typeinfo.BuiltinType{Name: "i32"},
				},
			},
		},
	}

	out, err := LookupStructLayout(func(*typeinfo.NamedType) (*layout.TypeLayout, error) {
		return nil, errors.New("unexpected named lookup")
	}, st)
	if err != nil {
		t.Fatalf("LookupStructLayout(anonymous struct): %v", err)
	}
	ptrSize := abi.PointerBytes()
	if out == nil || len(out.Fields) != 1 {
		t.Fatalf("unexpected anonymous struct layout: %#v", out)
	}
	if out.Fields[0].Offset != 0 || out.Fields[0].Size != ptrSize || out.Fields[0].Align != ptrSize {
		t.Fatalf("unexpected map field layout: %#v", out.Fields[0])
	}
	if out.Size != ptrSize || out.Align != ptrSize {
		t.Fatalf("unexpected struct size/align: size=%d align=%d", out.Size, out.Align)
	}
}
