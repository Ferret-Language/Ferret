package backend

import (
	"testing"

	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/frontend/ast"
)

func TestOptionalUsesNiche(t *testing.T) {
	tests := []struct {
		name string
		typ  typeinfo.Type
		want bool
	}{
		{name: "pointer", typ: &typeinfo.PointerType{}, want: true},
		{name: "ref", typ: &typeinfo.RefType{}, want: true},
		{name: "rawptr", typ: &typeinfo.RawPtrType{}, want: true},
		{name: "bool", typ: &typeinfo.BuiltinType{Name: "bool"}, want: true},
		{name: "char", typ: &typeinfo.BuiltinType{Name: "char"}, want: true},
		{name: "i32", typ: &typeinfo.BuiltinType{Name: "i32"}, want: false},
	}
	for _, tc := range tests {
		if got := OptionalUsesNiche(tc.typ); got != tc.want {
			t.Fatalf("%s: expected %v, got %v", tc.name, tc.want, got)
		}
	}
}

func TestClassifyABIType(t *testing.T) {
	namedInterface := &typeinfo.NamedType{
		Name: "Shape",
		Decl: &ast.TypeDecl{Type: &ast.InterfaceType{}},
	}
	namedStruct := &typeinfo.NamedType{
		Name: "Point",
		Decl: &ast.TypeDecl{Type: &ast.StructType{}},
	}
	tests := []struct {
		name      string
		typ       typeinfo.Type
		hasLayout bool
		want      ABITypeKind
	}{
		{name: "named layout", typ: namedStruct, hasLayout: true, want: ABITypeNamedLayout},
		{name: "named interface fallback", typ: namedInterface, hasLayout: false, want: ABITypeNamedInterface},
		{name: "optional aggregate", typ: &typeinfo.OptionalType{Inner: &typeinfo.BuiltinType{Name: "i32"}}, want: ABITypeOptionalAggregate},
		{name: "slice", typ: &typeinfo.SliceType{Inner: &typeinfo.BuiltinType{Name: "i32"}}, want: ABITypeSliceLike},
		{name: "string", typ: &typeinfo.StringType{}, want: ABITypeSliceLike},
		{name: "scalar", typ: &typeinfo.BuiltinType{Name: "i64"}, want: ABITypeScalar},
	}
	for _, tc := range tests {
		got := ClassifyABIType(tc.typ, func(named *typeinfo.NamedType) bool {
			return tc.hasLayout && named == tc.typ
		})
		if got != tc.want {
			t.Fatalf("%s: expected %v, got %v", tc.name, tc.want, got)
		}
	}
}
