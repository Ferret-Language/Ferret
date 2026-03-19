package typeinfo

import (
	"testing"

	"compiler/internal/frontend/ast"
)

func TestFormatFuncSignature(t *testing.T) {
	fn := &FuncType{
		IsUnsafe:       true,
		TypeParams:     []*TypeParam{{Name: "T"}, {Name: "U", Constraint: &BuiltinType{Name: "bool"}}},
		Params:         []Type{&BuiltinType{Name: "i32"}, &BuiltinType{Name: "i64"}},
		MutParams:      []bool{false, true},
		ComptimeParams: []bool{true, false},
		Result:         &BuiltinType{Name: "bool"},
	}

	got := FormatFuncSignature("Point::Calc", fn)
	want := "unsafe fn Point::Calc<T, U: bool>(comptime i32, mut i64) bool"
	if got != want {
		t.Fatalf("unexpected signature:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFormatFuncDeclSignature(t *testing.T) {
	fn := &ast.FuncDecl{
		OwnerType: &ast.NamedType{Path: []string{"Point"}},
		Name:      &ast.Ident{Path: []string{"Calc"}},
		Receiver: &ast.Receiver{
			Name: &ast.Ident{Path: []string{"self"}},
			Type: &ast.RefType{Inner: &ast.NamedType{Path: []string{"Point"}}},
		},
		Params: []ast.Param{
			{Name: &ast.Ident{Path: []string{"cx"}}, IsMut: true},
			{Name: &ast.Ident{Path: []string{"flag"}}, IsComptime: true},
		},
	}
	fnType := &FuncType{
		Params:         []Type{&BuiltinType{Name: "i32"}, &BuiltinType{Name: "bool"}},
		MutParams:      []bool{true, false},
		ComptimeParams: []bool{false, true},
		Result:         &BuiltinType{Name: "void"},
	}

	got := FormatFuncDeclSignature(fn, fnType)
	want := "fn Point::Calc(&self, mut cx: i32, comptime flag: bool) void"
	if got != want {
		t.Fatalf("unexpected declaration signature:\nwant: %q\ngot:  %q", want, got)
	}
}
