package backend

import (
	"errors"
	"reflect"
	"testing"

	"compiler/internal/analysis/semantics/typeinfo"
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
