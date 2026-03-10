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

type AssignStmt struct {
	Left     Expr
	Right    Expr
	Location source.Location
}

func (*AssignStmt) stmtNode()              {}
func (s *AssignStmt) Loc() source.Location { return s.Location }

type IfStmt struct {
	Cond     Expr
	Then     *BlockStmt
	Else     Stmt
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

type WhileStmt struct {
	Cond     Expr
	Body     *BlockStmt
	Location source.Location
}

func (*WhileStmt) stmtNode()              {}
func (s *WhileStmt) Loc() source.Location { return s.Location }

type ForStmt struct {
	Init     Stmt
	Cond     Expr
	Post     Stmt
	Body     *BlockStmt
	Location source.Location
}

func (*ForStmt) stmtNode()              {}
func (s *ForStmt) Loc() source.Location { return s.Location }

type LabelStmt struct {
	Name     string
	Stmt     Stmt
	Location source.Location
}

func (*LabelStmt) stmtNode()              {}
func (s *LabelStmt) Loc() source.Location { return s.Location }

type BreakStmt struct {
	Label    string
	Location source.Location
}

func (*BreakStmt) stmtNode()              {}
func (s *BreakStmt) Loc() source.Location { return s.Location }

type ContinueStmt struct {
	Label    string
	Location source.Location
}

func (*ContinueStmt) stmtNode()              {}
func (s *ContinueStmt) Loc() source.Location { return s.Location }

type DeferStmt struct {
	Body     Stmt
	Location source.Location
}

func (*DeferStmt) stmtNode()              {}
func (s *DeferStmt) Loc() source.Location { return s.Location }

type LockStmt struct {
	Value    Expr
	Name     string
	Body     *BlockStmt
	Location source.Location
}

func (*LockStmt) stmtNode()              {}
func (s *LockStmt) Loc() source.Location { return s.Location }

type UnsafeStmt struct {
	Body     *BlockStmt
	Location source.Location
}

func (*UnsafeStmt) stmtNode()              {}
func (s *UnsafeStmt) Loc() source.Location { return s.Location }
