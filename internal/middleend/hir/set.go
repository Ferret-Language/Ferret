package hir

import "compiler/internal/source"

func SetStmtLocation(stmt Stmt, loc source.Location) {
	switch s := stmt.(type) {
	case *BlockStmt:
		s.Location = loc
	case *LetStmt:
		s.Location = loc
	case *ConstStmt:
		s.Location = loc
	case *ReturnStmt:
		s.Location = loc
	case *ExprStmt:
		s.Location = loc
	case *AssignStmt:
		s.Location = loc
	case *IfStmt:
		s.Location = loc
	case *SwitchStmt:
		s.Location = loc
	case *WhileStmt:
		s.Location = loc
	case *ForStmt:
		s.Location = loc
	case *LoopStmt:
		s.Location = loc
	case *LabelStmt:
		s.Location = loc
	case *BreakStmt:
		s.Location = loc
	case *ContinueStmt:
		s.Location = loc
	case *DeferStmt:
		s.Location = loc
	case *LockStmt:
		s.Location = loc
	case *UnsafeStmt:
		s.Location = loc
	}
}
