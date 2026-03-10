package hir

import "compiler/internal/semantics/typeinfo"

type BlockStmt struct {
	baseStmt
	Stmts []Stmt
}

func (*BlockStmt) stmtNode() {}

type LetStmt struct {
	baseStmt
	Name    string
	Mutable bool
	Type    typeinfo.Type
	Value   Expr
}

func (*LetStmt) stmtNode() {}

type ConstStmt struct {
	baseStmt
	Name  string
	Type  typeinfo.Type
	Value Expr
}

func (*ConstStmt) stmtNode() {}

type ReturnStmt struct {
	baseStmt
	Value Expr
}

func (*ReturnStmt) stmtNode() {}

type ExprStmt struct {
	baseStmt
	Value Expr
}

func (*ExprStmt) stmtNode() {}

type AssignStmt struct {
	baseStmt
	Left  Expr
	Right Expr
}

func (*AssignStmt) stmtNode() {}

type IfStmt struct {
	baseStmt
	Cond Expr
	Then *BlockStmt
	Else Stmt
}

func (*IfStmt) stmtNode() {}

type SwitchCase struct {
	Expr Expr
	Body *BlockStmt
}

type SwitchStmt struct {
	baseStmt
	Value Expr
	Cases []*SwitchCase
}

func (*SwitchStmt) stmtNode() {}

type WhileStmt struct {
	baseStmt
	Cond Expr
	Body *BlockStmt
}

func (*WhileStmt) stmtNode() {}

type ForStmt struct {
	baseStmt
	Init Stmt
	Cond Expr
	Post Stmt
	Body *BlockStmt
}

func (*ForStmt) stmtNode() {}

type LoopStmt struct {
	baseStmt
	Init Stmt
	Cond Expr
	Post Stmt
	Body *BlockStmt
}

func (*LoopStmt) stmtNode() {}

type LabelStmt struct {
	baseStmt
	Name string
	Stmt Stmt
}

func (*LabelStmt) stmtNode() {}

type BreakStmt struct {
	baseStmt
	Label string
}

func (*BreakStmt) stmtNode() {}

type ContinueStmt struct {
	baseStmt
	Label string
}

func (*ContinueStmt) stmtNode() {}

type DeferStmt struct {
	baseStmt
	Body Stmt
}

func (*DeferStmt) stmtNode() {}

type LockStmt struct {
	baseStmt
	Value Expr
	Name  string
	Body  *BlockStmt
}

func (*LockStmt) stmtNode() {}

type UnsafeStmt struct {
	baseStmt
	Body *BlockStmt
}

func (*UnsafeStmt) stmtNode() {}
