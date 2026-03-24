package ast

import "compiler/internal/core/source"

type ImportDecl struct {
	Path     Expr
	Alias    *Ident
	Location source.Location
}

func (*ImportDecl) declNode()              {}
func (d *ImportDecl) Loc() source.Location { return d.Location }

type ConstDecl struct {
	Name     *Ident
	Doc      *CommentGroup
	Attrs    []Attribute
	Type     TypeExpr
	Value    Expr
	Location source.Location
}

func (*ConstDecl) declNode()              {}
func (d *ConstDecl) Loc() source.Location { return d.Location }

type LetDecl struct {
	Name     *Ident
	Doc      *CommentGroup
	Attrs    []Attribute
	IsMut    bool
	Type     TypeExpr
	Value    Expr
	Location source.Location
}

func (*LetDecl) declNode()              {}
func (d *LetDecl) Loc() source.Location { return d.Location }

type TypeDecl struct {
	Name       *Ident
	TypeParams []TypeParam
	Doc        *CommentGroup
	Attrs      []Attribute
	Type       TypeExpr
	Location   source.Location
}

func (*TypeDecl) declNode()              {}
func (d *TypeDecl) Loc() source.Location { return d.Location }

type Receiver struct {
	Name     *Ident
	Type     TypeExpr
	Location source.Location
}

func (r *Receiver) Loc() source.Location { return r.Location }

type Param struct {
	Name       *Ident
	IsMut      bool
	IsComptime bool
	IsVariadic bool
	Type       TypeExpr
	Location   source.Location
}

type TypeParam struct {
	Name       *Ident
	Constraint TypeExpr
	Location   source.Location
}

func (p *TypeParam) Loc() source.Location { return p.Location }

type FuncDecl struct {
	Receiver      *Receiver
	OwnerType     *NamedType
	IsStatic      bool
	Name          *Ident
	TypeParams    []TypeParam
	Doc           *CommentGroup
	Attrs         []Attribute
	IsUnsafe      bool
	IsExtern      bool
	ExternName    string
	IsConstructor bool
	IsDestructor  bool
	Params        []Param
	Result        TypeExpr
	Body          *BlockStmt
	Location      source.Location
}

func (*FuncDecl) declNode()              {}
func (d *FuncDecl) Loc() source.Location { return d.Location }

type FieldDecl struct {
	Name     *Ident
	Type     TypeExpr
	Default  Expr
	Location source.Location
}

func (d *FieldDecl) Loc() source.Location { return d.Location }
