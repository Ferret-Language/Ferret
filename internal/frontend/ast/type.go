package ast

import "compiler/internal/core/source"

type NamedType struct {
	Path     []string
	Location source.Location
}

func (*NamedType) typeNode()              {}
func (t *NamedType) Loc() source.Location { return t.Location }

type PointerType struct {
	IsOwn    bool
	IsRaw    bool
	IsMut    bool
	Inner    TypeExpr
	Location source.Location
}

func (*PointerType) typeNode()              {}
func (t *PointerType) Loc() source.Location { return t.Location }

type RefType struct {
	Mutable  bool
	Inner    TypeExpr
	Location source.Location
}

func (*RefType) typeNode()              {}
func (t *RefType) Loc() source.Location { return t.Location }

type RawPtrType struct {
	Inner    TypeExpr
	Location source.Location
}

func (*RawPtrType) typeNode()              {}
func (t *RawPtrType) Loc() source.Location { return t.Location }

type OptionalType struct {
	Inner    TypeExpr
	Location source.Location
}

func (*OptionalType) typeNode()              {}
func (t *OptionalType) Loc() source.Location { return t.Location }

type ErrorUnionType struct {
	Error    TypeExpr
	Value    TypeExpr
	Location source.Location
}

func (*ErrorUnionType) typeNode()              {}
func (t *ErrorUnionType) Loc() source.Location { return t.Location }

type ArrayType struct {
	Size     Expr
	Inner    TypeExpr
	Location source.Location
}

func (*ArrayType) typeNode()              {}
func (t *ArrayType) Loc() source.Location { return t.Location }

type SliceType struct {
	Inner    TypeExpr
	Location source.Location
}

func (*SliceType) typeNode()              {}
func (t *SliceType) Loc() source.Location { return t.Location }

type TupleType struct {
	Elems    []TypeExpr
	Location source.Location
}

func (*TupleType) typeNode()              {}
func (t *TupleType) Loc() source.Location { return t.Location }

type StructType struct {
	Fields       []*FieldDecl
	StaticFields []*StaticFieldDecl
	Location     source.Location
}

func (*StructType) typeNode()              {}
func (t *StructType) Loc() source.Location { return t.Location }

type InterfaceMethod struct {
	Receiver string
	Name     *Ident
	Params   []Param
	Result   TypeExpr
	Location source.Location
}

type InterfaceType struct {
	Methods  []*InterfaceMethod
	Location source.Location
}

func (*InterfaceType) typeNode()              {}
func (t *InterfaceType) Loc() source.Location { return t.Location }

type EnumVariant struct {
	Name     *Ident
	Location source.Location
}

func (v *EnumVariant) Loc() source.Location { return v.Location }

type EnumType struct {
	Variants []*EnumVariant
	Location source.Location
}

func (*EnumType) typeNode()              {}
func (t *EnumType) Loc() source.Location { return t.Location }

type UnionType struct {
	Members  []TypeExpr
	Location source.Location
}

func (*UnionType) typeNode()              {}
func (t *UnionType) Loc() source.Location { return t.Location }

type ErrorMember struct {
	Name     *Ident
	Location source.Location
}

func (m *ErrorMember) Loc() source.Location { return m.Location }

type ErrorType struct {
	Members  []*ErrorMember
	Location source.Location
}

func (*ErrorType) typeNode()              {}
func (t *ErrorType) Loc() source.Location { return t.Location }
