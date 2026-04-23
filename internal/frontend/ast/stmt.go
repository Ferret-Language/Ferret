package ast

import "compiler/internal/core/source"

type BlockStmt struct {
	Stmts    []Stmt
	Comptime bool
	Location source.Location
}

func (*BlockStmt) stmtNode()              {}
func (s *BlockStmt) Loc() source.Location { return s.Location }

type LetStmt struct {
	Name     *Ident
	Doc      *CommentGroup
	IsMut    bool
	IsAtomic bool
	Type     TypeExpr
	Value    Expr
	Location source.Location
}

func (*LetStmt) stmtNode()              {}
func (s *LetStmt) Loc() source.Location { return s.Location }

type ConstStmt struct {
	Name     *Ident
	Doc      *CommentGroup
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

type MatchArm struct {
	Pattern     Expr
	TypePattern TypeExpr
	Wildcard    bool
	Body        *BlockStmt
	Location    source.Location
}

type MatchStmt struct {
	Value    Expr
	Arms     []*MatchArm
	Location source.Location
}

func (*MatchStmt) stmtNode()              {}
func (s *MatchStmt) Loc() source.Location { return s.Location }

type WhileStmt struct {
	Cond     Expr
	Body     *BlockStmt
	Location source.Location
}

func (*WhileStmt) stmtNode()              {}
func (s *WhileStmt) Loc() source.Location { return s.Location }

type ForStmt struct {
	Iterable Expr
	Index    *Ident
	Value    *Ident
	Body     *BlockStmt
	Location source.Location
}

func (*ForStmt) stmtNode()              {}
func (s *ForStmt) Loc() source.Location { return s.Location }

type LabelStmt struct {
	Name     *Ident
	Stmt     Stmt
	Location source.Location
}

func (*LabelStmt) stmtNode()              {}
func (s *LabelStmt) Loc() source.Location { return s.Location }

type BreakStmt struct {
	Label    *Ident
	Location source.Location
}

func (*BreakStmt) stmtNode()              {}
func (s *BreakStmt) Loc() source.Location { return s.Location }

type ContinueStmt struct {
	Label    *Ident
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

type ReleaseStmt struct {
	Value    Expr
	Location source.Location
}

func (*ReleaseStmt) stmtNode()              {}
func (s *ReleaseStmt) Loc() source.Location { return s.Location }

type PanicStmt struct {
	Value    Expr
	Location source.Location
}

func (*PanicStmt) stmtNode()              {}
func (s *PanicStmt) Loc() source.Location { return s.Location }

type LockStmt struct {
	Value    Expr
	Name     *Ident
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
