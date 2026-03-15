package hir

import (
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/source"
	"compiler/internal/frontend/ast"
)

type Node interface {
	Loc() source.Location
	node()
}

type Stmt interface {
	Node
	stmtNode()
}

type Expr interface {
	Node
	exprNode()
	Type() typeinfo.Type
	SourceExpr() ast.Expr
}

type baseExpr struct {
	ExprType typeinfo.Type
	Location source.Location
	Source   ast.Expr
}

func (e *baseExpr) Type() typeinfo.Type  { return e.ExprType }
func (e *baseExpr) Loc() source.Location { return e.Location }
func (e *baseExpr) SourceExpr() ast.Expr { return e.Source }
func (e *baseExpr) node()                {}

type baseStmt struct {
	Location source.Location
}

func (s *baseStmt) Loc() source.Location { return s.Location }
func (s *baseStmt) node()                {}
