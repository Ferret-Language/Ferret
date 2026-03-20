package backend

import (
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
