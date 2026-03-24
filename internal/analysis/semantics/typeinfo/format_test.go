package typeinfo

import (
	"strings"
	"testing"

	"compiler/internal/frontend/ast"
)

func TestFormatFuncSignature(t *testing.T) {
	fn := &FuncType{
		IsUnsafe:   true,
		TypeParams: []*TypeParam{{Name: "T"}, {Name: "U", Constraint: &BuiltinType{Name: "bool"}}},
		Params: []ParamSpec{
			{Type: &BuiltinType{Name: "i32"}, Flags: FlagComptime},
			{Type: &BuiltinType{Name: "i64"}, Flags: FlagMutable},
		},
		Result: &BuiltinType{Name: "bool"},
	}

	got := FormatFuncSignature("Point::Calc", fn)
	want := "unsafe fn Point::Calc<T, U: bool>(comptime i32, mut i64) -> bool"
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
		Params: []ParamSpec{
			{Type: &BuiltinType{Name: "i32"}, Flags: FlagMutable},
			{Type: &BuiltinType{Name: "bool"}, Flags: FlagComptime},
		},
		Result: &BuiltinType{Name: "void"},
	}

	got := FormatFuncDeclSignature(fn, fnType)
	want := "fn Point::Calc(&self, mut cx: i32, comptime flag: bool) -> void"
	if got != want {
		t.Fatalf("unexpected declaration signature:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFuncTypeStringUsesCanonicalFormatter(t *testing.T) {
	fn := &FuncType{
		IsUnsafe:   true,
		TypeParams: []*TypeParam{{Name: "T"}},
		Params: []ParamSpec{
			{Type: &BuiltinType{Name: "i32"}, Flags: FlagComptime},
			{Type: &BuiltinType{Name: "i64"}, Flags: FlagMutable},
		},
		Result: &BuiltinType{Name: "bool"},
	}

	got := fn.String()
	want := "unsafe fn<T>(comptime i32, mut i64) -> bool"
	if got != want {
		t.Fatalf("unexpected canonical func type string:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFormatMethodSignatureIncludesSelf(t *testing.T) {
	receiver := &RefType{Mutable: true, Inner: &NamedType{Name: "Point"}}
	fn := &FuncType{
		Params: []ParamSpec{{Type: &BuiltinType{Name: "i32"}}},
		Result: &BuiltinType{Name: "void"},
	}

	got := FormatMethodSignature("Point::Calc", receiver, fn)
	want := "fn Point::Calc(&mut self, i32) -> void"
	if got != want {
		t.Fatalf("unexpected method signature:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestFormatBindingDecl(t *testing.T) {
	if got := FormatBindingDecl("let", "a", &BuiltinType{Name: "i32"}, FlagMutable); got != "let mut a: i32" {
		t.Fatalf("unexpected let binding declaration: %q", got)
	}
	if got := FormatBindingDecl("const", "b", &BuiltinType{Name: "i32"}, 0); got != "const b: i32" {
		t.Fatalf("unexpected const binding declaration: %q", got)
	}
	if got := FormatBindingDecl("parameter", "p", &BuiltinType{Name: "i32"}, FlagMutable|FlagComptime); got != "parameter mut comptime p: i32" {
		t.Fatalf("unexpected parameter binding declaration: %q", got)
	}
	if got := FormatBindingDecl("parameter", "s", &TypeParam{Name: "T", Constraint: &NamedType{Name: "Shape"}}, 0); got != "parameter s: T: Shape" {
		t.Fatalf("unexpected constrained parameter binding declaration: %q", got)
	}
}

func TestFormatReceiverBindingDecl(t *testing.T) {
	if got := FormatReceiverBindingDecl("self", ReceiverRefMut); got != "receiver self: &mut Self" {
		t.Fatalf("unexpected receiver binding declaration: %q", got)
	}
}

func TestFormatReceiverTextPreservesRawConst(t *testing.T) {
	if got := ast.FormatReceiverText("self", "^const Point"); got != "^const self" {
		t.Fatalf("unexpected raw const receiver text: %q", got)
	}
}

func TestFormatNamedTypeHoverMarkdown(t *testing.T) {
	got := FormatNamedTypeHoverMarkdown(NamedTypeHoverBlock{
		DeclText: "type Point struct {\n    Value: i32 = 7\n}",
		InstanceMethods: []string{
			"fn Point::Calc(&self) -> i32",
		},
		StaticMethods: []string{
			"fn Point::New(v: i32) -> Point",
		},
	})
	if !strings.Contains(got, "```ferret\ntype Point struct {\n    Value: i32 = 7\n}\n```") {
		t.Fatalf("expected declaration code block, got %q", got)
	}
	if !strings.Contains(got, "Instance methods:\n```ferret\nfn Point::Calc(&self) -> i32\n```") {
		t.Fatalf("expected instance method section, got %q", got)
	}
	if !strings.Contains(got, "Static methods:\n```ferret\nfn Point::New(v: i32) -> Point\n```") {
		t.Fatalf("expected static method section, got %q", got)
	}
}

func TestFormatNamedTypeHoverMarkdownIncludesTruncationNote(t *testing.T) {
	got := FormatNamedTypeHoverMarkdown(NamedTypeHoverBlock{
		DeclText:        "type Point struct {}",
		TruncationNote:  "_Hover truncated: omitted 3 additional method signature(s)._",
		InstanceMethods: []string{"fn Point::Calc(&self) -> i32"},
	})
	if !strings.Contains(got, "_Hover truncated: omitted 3 additional method signature(s)._") {
		t.Fatalf("expected truncation note in hover markdown, got %q", got)
	}
}
