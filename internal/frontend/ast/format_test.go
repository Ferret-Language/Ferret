package ast

import "testing"

func TestFuncDeclSignature(t *testing.T) {
	fn := &FuncDecl{
		OwnerType: &NamedType{Path: []string{"Point"}},
		Name:      &Ident{Path: []string{"Calc"}},
		Receiver: &Receiver{
			Name: &Ident{Path: []string{"self"}},
			Type: &RefType{Inner: &NamedType{Path: []string{"Point"}}},
		},
		Result: &NamedType{Path: []string{"i32"}},
	}

	if got := fn.Signature(); got != "fn Point::Calc(&self) i32" {
		t.Fatalf("unexpected signature: %q", got)
	}
}

func TestTypeDeclTextStructIncludesFields(t *testing.T) {
	decl := &TypeDecl{
		Name: &Ident{Path: []string{"Point"}},
		Type: &StructType{
			Fields: []*FieldDecl{
				{
					Name: &Ident{Path: []string{"X"}},
					Type: &NamedType{Path: []string{"i32"}},
				},
				{
					Name:    &Ident{Path: []string{"Y"}},
					Type:    &NamedType{Path: []string{"i32"}},
					Default: &NumberLit{Value: "0"},
				},
			},
		},
	}

	want := "type Point struct {\n    X: i32\n    Y: i32 = 0\n}"
	if got := decl.Text(); got != want {
		t.Fatalf("unexpected type text:\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}
