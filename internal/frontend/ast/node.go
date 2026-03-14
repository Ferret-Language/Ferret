package ast

import "compiler/internal/core/source"

type Node interface {
	Loc() source.Location
}

type Decl interface {
	Node
	declNode()
}

type Stmt interface {
	Node
	stmtNode()
}

type Expr interface {
	Node
	exprNode()
}

type TypeExpr interface {
	Node
	typeNode()
}

type Module struct {
	FilePath string
	Imports  []*ImportDecl
	Decls    []Decl
}
