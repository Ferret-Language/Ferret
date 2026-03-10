package ast

import "compiler/internal/source"

type Ident struct {
	Path     []string
	Location source.Location
}

func (*Ident) exprNode()              {}
func (e *Ident) Loc() source.Location { return e.Location }

type BadExpr struct {
	Location source.Location
}

func (*BadExpr) exprNode()              {}
func (e *BadExpr) Loc() source.Location { return e.Location }

type NumberLit struct {
	Value    string
	Location source.Location
}

func (*NumberLit) exprNode()              {}
func (e *NumberLit) Loc() source.Location { return e.Location }

type StringLit struct {
	Value    string
	Location source.Location
}

func (*StringLit) exprNode()              {}
func (e *StringLit) Loc() source.Location { return e.Location }

type NoneLit struct {
	Location source.Location
}

func (*NoneLit) exprNode()              {}
func (e *NoneLit) Loc() source.Location { return e.Location }

type PrefixExpr struct {
	Op       string
	Right    Expr
	Location source.Location
}

func (*PrefixExpr) exprNode()              {}
func (e *PrefixExpr) Loc() source.Location { return e.Location }

type UnsafeExpr struct {
	Value    Expr
	Location source.Location
}

func (*UnsafeExpr) exprNode()              {}
func (e *UnsafeExpr) Loc() source.Location { return e.Location }

type BinaryExpr struct {
	Left     Expr
	Op       string
	Right    Expr
	Location source.Location
}

func (*BinaryExpr) exprNode()              {}
func (e *BinaryExpr) Loc() source.Location { return e.Location }

type PostfixExpr struct {
	Left     Expr
	Op       string
	Location source.Location
}

func (*PostfixExpr) exprNode()              {}
func (e *PostfixExpr) Loc() source.Location { return e.Location }

type CallExpr struct {
	Callee   Expr
	TypeArgs []TypeExpr
	Args     []Expr
	Location source.Location
}

func (*CallExpr) exprNode()              {}
func (e *CallExpr) Loc() source.Location { return e.Location }

type SelectorExpr struct {
	Left     Expr
	Name     string
	Location source.Location
}

func (*SelectorExpr) exprNode()              {}
func (e *SelectorExpr) Loc() source.Location { return e.Location }

type CastExpr struct {
	Left     Expr
	Type     TypeExpr
	Location source.Location
}

func (*CastExpr) exprNode()              {}
func (e *CastExpr) Loc() source.Location { return e.Location }

type CompositeItem struct {
	Name  string
	Value Expr
}

type CompositeLit struct {
	Items    []CompositeItem
	Location source.Location
}

func (*CompositeLit) exprNode()              {}
func (e *CompositeLit) Loc() source.Location { return e.Location }
