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

	if got := fn.Signature(); got != "fn Point::Calc(&self) -> i32" {
		t.Fatalf("unexpected signature: %q", got)
	}
}

func TestFuncDeclSignatureIncludesTypeParams(t *testing.T) {
	fn := &FuncDecl{
		Name: &Ident{Path: []string{"Map"}},
		TypeParams: []TypeParam{
			{Name: &Ident{Path: []string{"T"}}},
			{Name: &Ident{Path: []string{"U"}}, Constraint: &NamedType{Path: []string{"any"}}},
		},
		Params: []Param{
			{Name: &Ident{Path: []string{"value"}}, Type: &NamedType{Path: []string{"T"}}},
		},
		Result: &NamedType{Path: []string{"U"}},
	}

	want := "fn Map<T, U: any>(value: T) -> U"
	if got := fn.Signature(); got != want {
		t.Fatalf("unexpected signature:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFuncDeclSignatureOmitsMissingParamSyntaxType(t *testing.T) {
	fn := &FuncDecl{
		Name: &Ident{Path: []string{"Something"}},
		Params: []Param{
			{Name: &Ident{Path: []string{"v"}}, Type: &NamedType{Path: []string{"i32"}}},
			{Name: &Ident{Path: []string{"i"}}, Default: &Ident{Path: []string{"v"}}},
		},
		Result: &NamedType{Path: []string{"i32"}},
	}

	want := "fn Something(v: i32, i = v) -> i32"
	if got := fn.Signature(); got != want {
		t.Fatalf("unexpected signature:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFuncDeclNameIncludesOwnerType(t *testing.T) {
	fn := &FuncDecl{
		OwnerType: &NamedType{Path: []string{"math", "Point"}},
		Name:      &Ident{Path: []string{"Calc"}},
	}

	if got := FuncDeclName(fn); got != "math::Point::Calc" {
		t.Fatalf("unexpected function name: %q", got)
	}
}

func TestFormatReceiverTextAndParamList(t *testing.T) {
	if got := FormatReceiverText("self", "&mut Point"); got != "&mut self" {
		t.Fatalf("unexpected receiver text: %q", got)
	}
	if got := FormatParamList([]string{"&self", "x: i32"}); got != "(&self, x: i32)" {
		t.Fatalf("unexpected param list: %q", got)
	}
	if got := FormatInterfaceMethodSignatureText("Read", "&", []string{"buf: []u8"}, "usize"); got != "Read(&self, buf: []u8) -> usize" {
		t.Fatalf("unexpected interface method signature: %q", got)
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

func TestTypeDeclTextIncludesTypeParams(t *testing.T) {
	decl := &TypeDecl{
		Name: &Ident{Path: []string{"Box"}},
		TypeParams: []TypeParam{
			{Name: &Ident{Path: []string{"T"}}},
		},
		Type: &StructType{
			Fields: []*FieldDecl{
				{
					Name: &Ident{Path: []string{"Value"}},
					Type: &NamedType{Path: []string{"T"}},
				},
			},
		},
	}

	want := "type Box<T> struct {\n    Value: T\n}"
	if got := decl.Text(); got != want {
		t.Fatalf("unexpected type text:\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestTypeDeclTextIncludesFullUnionBody(t *testing.T) {
	decl := &TypeDecl{
		Name: &Ident{Path: []string{"Number"}},
		Type: &UnionType{
			Members: []TypeExpr{
				&NamedType{Path: []string{"i32"}},
				&NamedType{Path: []string{"i64"}},
			},
		},
	}

	want := "type Number union { i32, i64 }"
	if got := decl.Text(); got != want {
		t.Fatalf("unexpected type text:\nwant: %q\ngot:  %q", want, got)
	}
}
