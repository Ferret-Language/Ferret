package hir

func Lower(input *Module) *Module {
	if input == nil {
		return nil
	}
	out := &Module{
		Key:        input.Key,
		ImportPath: input.ImportPath,
		FilePath:   input.FilePath,
		Source:     input.Source,
		Types:      append([]*TypeDecl(nil), input.Types...),
		Globals:    append([]*Global(nil), input.Globals...),
		Functions:  make([]*Func, 0, len(input.Functions)),
	}
	for _, fn := range input.Functions {
		out.Functions = append(out.Functions, lowerFunc(fn))
	}
	return out
}

func lowerFunc(fn *Func) *Func {
	if fn == nil {
		return nil
	}
	out := *fn
	out.Body = lowerBlock(fn.Body)
	return &out
}

func lowerBlock(block *BlockStmt) *BlockStmt {
	if block == nil {
		return nil
	}
	out := &BlockStmt{Stmts: make([]Stmt, 0, len(block.Stmts))}
	SetStmtLocation(out, block.Loc())
	for _, stmt := range block.Stmts {
		if lowered := lowerStmt(stmt); lowered != nil {
			out.Stmts = append(out.Stmts, lowered)
		}
	}
	return out
}

func lowerStmt(stmt Stmt) Stmt {
	switch s := stmt.(type) {
	case nil:
		return nil
	case *BlockStmt:
		return lowerBlock(s)
	case *IfStmt:
		out := &IfStmt{Cond: s.Cond, Then: lowerBlock(s.Then), Else: lowerStmt(s.Else)}
		SetStmtLocation(out, s.Loc())
		if out.Else == nil {
			empty := &BlockStmt{}
			SetStmtLocation(empty, s.Loc())
			out.Else = empty
		}
		return out
	case *SwitchStmt:
		out := &SwitchStmt{Value: s.Value, Cases: make([]*SwitchCase, 0, len(s.Cases))}
		SetStmtLocation(out, s.Loc())
		for _, kase := range s.Cases {
			if kase == nil {
				continue
			}
			out.Cases = append(out.Cases, &SwitchCase{Expr: kase.Expr, Body: lowerBlock(kase.Body)})
		}
		return out
	case *WhileStmt:
		out := &LoopStmt{Cond: s.Cond, Body: lowerBlock(s.Body)}
		SetStmtLocation(out, s.Loc())
		return out
	case *ForStmt:
		out := &LoopStmt{Init: lowerStmt(s.Init), Cond: s.Cond, Post: lowerStmt(s.Post), Body: lowerBlock(s.Body)}
		SetStmtLocation(out, s.Loc())
		return out
	case *LabelStmt:
		out := &LabelStmt{Name: s.Name, Stmt: lowerStmt(s.Stmt)}
		SetStmtLocation(out, s.Loc())
		return out
	case *DeferStmt:
		out := &DeferStmt{Body: lowerStmt(s.Body)}
		SetStmtLocation(out, s.Loc())
		return out
	case *LockStmt:
		out := &LockStmt{Value: s.Value, Name: s.Name, Body: lowerBlock(s.Body)}
		SetStmtLocation(out, s.Loc())
		return out
	case *UnsafeStmt:
		out := &UnsafeStmt{Body: lowerBlock(s.Body)}
		SetStmtLocation(out, s.Loc())
		return out
	default:
		return stmt
	}
}
