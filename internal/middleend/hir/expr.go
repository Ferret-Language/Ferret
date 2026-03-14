package hir

import "compiler/internal/semantics/typeinfo"

type Ident struct {
	baseExpr
	Path    []string
	LocalID int // -1 when this ident is not a local (globals, builtins, etc).
}

func (*Ident) exprNode() {}

type BadExpr struct{ baseExpr }

func (*BadExpr) exprNode() {}

type NumberLit struct {
	baseExpr
	Value string
}

func (*NumberLit) exprNode() {}

type StringLit struct {
	baseExpr
	Value string
}

func (*StringLit) exprNode() {}

type NoneLit struct{ baseExpr }

func (*NoneLit) exprNode() {}

type PrefixExpr struct {
	baseExpr
	Op    string
	Right Expr
}

func (*PrefixExpr) exprNode() {}

type BinaryExpr struct {
	baseExpr
	Left  Expr
	Op    string
	Right Expr
}

func (*BinaryExpr) exprNode() {}

type PostfixExpr struct {
	baseExpr
	Left Expr
	Op   string
}

func (*PostfixExpr) exprNode() {}

type CallExpr struct {
	baseExpr
	Callee Expr
	Args   []Expr
}

func (*CallExpr) exprNode() {}

type ConstructorCallExpr struct {
	baseExpr
	Path []string
	Args []Expr
}

func (*ConstructorCallExpr) exprNode() {}

type SelectorExpr struct {
	baseExpr
	Left Expr
	Name string
}

func (*SelectorExpr) exprNode() {}

type CastExpr struct {
	baseExpr
	Left Expr
}

func (*CastExpr) exprNode() {}

type IsExpr struct {
	baseExpr
	Left        Expr
	Target      typeinfo.Type
	StaticKnown bool
	StaticValue bool
}

func (*IsExpr) exprNode() {}

type MatchExpr struct {
	baseExpr
	Value Expr
	Arms  []*MatchArm
}

func (*MatchExpr) exprNode() {}

type CatchExpr struct {
	baseExpr
	Left        Expr
	Fallback    Expr
	PayloadName string
	PayloadID   int
	Handler     *BlockStmt
}

func (*CatchExpr) exprNode() {}

type CompositeItem struct {
	Name  string
	Value Expr
}

type CompositeLit struct {
	baseExpr
	Items           []CompositeItem
	ConstructorPath []string
}

func (*CompositeLit) exprNode() {}

// IndexExpr represents arr[index].
type IndexExpr struct {
	baseExpr
	Left  Expr
	Index Expr
}

func (*IndexExpr) exprNode() {}
