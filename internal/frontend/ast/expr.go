package ast

import (
	"compiler/internal/source"
	"strings"
)

type Ident struct {
	Path     []string
	Location source.Location
}

func (*Ident) exprNode()              {}
func (e *Ident) Loc() source.Location { return e.Location }
func (e *Ident) Text() string {
	if e == nil {
		return ""
	}
	return strings.Join(e.Path, "::")
}
func (e *Ident) Last() string {
	if e == nil || len(e.Path) == 0 {
		return ""
	}
	return e.Path[len(e.Path)-1]
}

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
	Name     *Ident
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

type CatchExpr struct {
	Left     Expr
	Fallback Expr
	Payload  *Ident
	Handler  *BlockStmt
	Location source.Location
}

func (*CatchExpr) exprNode()              {}
func (e *CatchExpr) Loc() source.Location { return e.Location }

type CompositeItem struct {
	Name  *Ident
	Value Expr
}

type CompositeLit struct {
	Items    []CompositeItem
	Location source.Location
}

func (*CompositeLit) exprNode()              {}
func (e *CompositeLit) Loc() source.Location { return e.Location }

// IndexExpr represents arr[index].
type IndexExpr struct {
	Left     Expr
	Index    Expr
	Location source.Location
}

func (*IndexExpr) exprNode()              {}
func (e *IndexExpr) Loc() source.Location { return e.Location }

func ExprText(expr Expr) string {
	switch e := expr.(type) {
	case nil:
		return ""
	case *Ident:
		return e.Text()
	case *StringLit:
		return e.Value
	default:
		return ""
	}
}
