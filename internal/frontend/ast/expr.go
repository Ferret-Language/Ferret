package ast

import (
	"compiler/internal/core/source"
	"strings"
)

type Ident struct {
	Path     []string
	TypeArgs []TypeExpr
	Location source.Location
}

func (*Ident) exprNode()              {}
func (e *Ident) Loc() source.Location { return e.Location }
func (e *Ident) Text() string {
	if e == nil {
		return ""
	}
	if len(e.TypeArgs) == 0 {
		return strings.Join(e.Path, "::")
	}
	parts := append([]string(nil), e.Path...)
	args := make([]string, 0, len(e.TypeArgs))
	for _, arg := range e.TypeArgs {
		args = append(args, TypeString(arg))
	}
	if len(parts) > 1 {
		parts[len(parts)-2] = parts[len(parts)-2] + "<" + strings.Join(args, ", ") + ">"
		return strings.Join(parts, "::")
	}
	parts[0] = parts[0] + "<" + strings.Join(args, ", ") + ">"
	return strings.Join(parts, "::")
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

type CharLit struct {
	Value    string
	IsByte   bool
	Location source.Location
}

func (*CharLit) exprNode()              {}
func (e *CharLit) Loc() source.Location { return e.Location }

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

type SpreadExpr struct {
	Right    Expr
	Location source.Location
}

func (*SpreadExpr) exprNode()              {}
func (e *SpreadExpr) Loc() source.Location { return e.Location }

type BinaryExpr struct {
	Left     Expr
	Op       string
	Right    Expr
	Location source.Location
}

func (*BinaryExpr) exprNode()              {}
func (e *BinaryExpr) Loc() source.Location { return e.Location }

type RangeExpr struct {
	Start     Expr
	End       Expr
	Step      Expr
	Inclusive bool
	Location  source.Location
}

func (*RangeExpr) exprNode()              {}
func (e *RangeExpr) Loc() source.Location { return e.Location }

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

type IsExpr struct {
	Left     Expr
	Type     TypeExpr
	Location source.Location
}

func (*IsExpr) exprNode()              {}
func (e *IsExpr) Loc() source.Location { return e.Location }

type MatchExpr struct {
	Value    Expr
	Arms     []*MatchArm
	Location source.Location
}

func (*MatchExpr) exprNode()              {}
func (e *MatchExpr) Loc() source.Location { return e.Location }

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
	Type     TypeExpr
	Items    []CompositeItem
	Tuple    bool
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
	case *CharLit:
		return e.Value
	default:
		return ""
	}
}
