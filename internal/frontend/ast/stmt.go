package ast

import "compiler/internal/source"

type BlockStmt struct {
	Stmts    []Stmt
	Location source.Location
}

func (*BlockStmt) stmtNode()              {}
func (s *BlockStmt) Loc() source.Location { return s.Location }

type LetStmt struct {
	Name     string
	IsMut    bool
	Type     TypeExpr
	Value    Expr
	Location source.Location
}

func (*LetStmt) stmtNode()              {}
func (s *LetStmt) Loc() source.Location { return s.Location }

type ConstStmt struct {
	Name     string
	Type     TypeExpr
	Value    Expr
	Location source.Location
}

func (*ConstStmt) stmtNode()              {}
func (s *ConstStmt) Loc() source.Location { return s.Location }

type ReturnStmt struct {
	Value    Expr
	Location source.Location
}

func (*ReturnStmt) stmtNode()              {}
func (s *ReturnStmt) Loc() source.Location { return s.Location }

type ExprStmt struct {
	Value    Expr
	Location source.Location
}

func (*ExprStmt) stmtNode()              {}
func (s *ExprStmt) Loc() source.Location { return s.Location }

type IfStmt struct {
	Cond     Expr
	Then     *BlockStmt
	Else     *BlockStmt
	Location source.Location
}

func (*IfStmt) stmtNode()              {}
func (s *IfStmt) Loc() source.Location { return s.Location }

type SwitchCase struct {
	Expr     Expr
	Body     *BlockStmt
	Location source.Location
}

type SwitchStmt struct {
	Value    Expr
	Cases    []*SwitchCase
	Location source.Location
}

func (*SwitchStmt) stmtNode()              {}
func (s *SwitchStmt) Loc() source.Location { return s.Location }
