package hir

import "compiler/internal/semantics/typeinfo"

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
		l := &lowerer{}
		out.Functions = append(out.Functions, l.lowerFunc(fn))
	}
	return out
}

type lowerer struct {
	tempID int
}

func (l *lowerer) nextTemp() string {
	l.tempID++
	return "__match" + itoa(l.tempID)
}

func (l *lowerer) lowerFunc(fn *Func) *Func {
	if fn == nil {
		return nil
	}
	out := *fn
	out.Body = l.lowerBlock(fn.Body)
	return &out
}

func (l *lowerer) lowerBlock(block *BlockStmt) *BlockStmt {
	if block == nil {
		return nil
	}
	out := &BlockStmt{Stmts: make([]Stmt, 0, len(block.Stmts))}
	SetStmtLocation(out, block.Loc())
	for _, stmt := range block.Stmts {
		if lowered := l.lowerStmt(stmt); lowered != nil {
			out.Stmts = append(out.Stmts, lowered)
		}
	}
	return out
}

func (l *lowerer) lowerStmt(stmt Stmt) Stmt {
	switch s := stmt.(type) {
	case nil:
		return nil
	case *BlockStmt:
		return l.lowerBlock(s)
	case *IfStmt:
		out := &IfStmt{Cond: s.Cond, Then: l.lowerBlock(s.Then), Else: l.lowerStmt(s.Else)}
		SetStmtLocation(out, s.Loc())
		if out.Else == nil {
			empty := &BlockStmt{}
			SetStmtLocation(empty, s.Loc())
			out.Else = empty
		}
		return out
	case *MatchStmt:
		if l.hasTypeMatchArm(s) {
			return l.lowerTypedMatch(s)
		}
		out := &MatchStmt{Value: s.Value, Arms: make([]*MatchArm, 0, len(s.Arms))}
		SetStmtLocation(out, s.Loc())
		for _, arm := range s.Arms {
			if arm == nil {
				continue
			}
			out.Arms = append(out.Arms, &MatchArm{
				Pattern:     arm.Pattern,
				TypePattern: arm.TypePattern,
				BindingName: arm.BindingName,
				Wildcard:    arm.Wildcard,
				Body:        l.lowerBlock(arm.Body),
			})
		}
		return out
	case *WhileStmt:
		out := &LoopStmt{Cond: s.Cond, Body: l.lowerBlock(s.Body)}
		SetStmtLocation(out, s.Loc())
		return out
	case *ForStmt:
		out := &ForStmt{Iterable: s.Iterable, IndexName: s.IndexName, ValueName: s.ValueName, Body: l.lowerBlock(s.Body)}
		SetStmtLocation(out, s.Loc())
		return out
	case *LabelStmt:
		out := &LabelStmt{Name: s.Name, Stmt: l.lowerStmt(s.Stmt)}
		SetStmtLocation(out, s.Loc())
		return out
	case *DeferStmt:
		out := &DeferStmt{Body: l.lowerStmt(s.Body)}
		SetStmtLocation(out, s.Loc())
		return out
	case *ReleaseStmt:
		out := &ReleaseStmt{Value: s.Value}
		SetStmtLocation(out, s.Loc())
		return out
	case *PanicStmt:
		out := &PanicStmt{Value: s.Value}
		SetStmtLocation(out, s.Loc())
		return out
	case *LockStmt:
		out := &LockStmt{Value: s.Value, Name: s.Name, Body: l.lowerBlock(s.Body)}
		SetStmtLocation(out, s.Loc())
		return out
	case *UnsafeStmt:
		out := &UnsafeStmt{Body: l.lowerBlock(s.Body)}
		SetStmtLocation(out, s.Loc())
		return out
	default:
		return stmt
	}
}

func (l *lowerer) hasTypeMatchArm(s *MatchStmt) bool {
	if s == nil {
		return false
	}
	for _, arm := range s.Arms {
		if arm != nil && (arm.TypePattern != nil || arm.BindingName != "") {
			return true
		}
	}
	return false
}

func (l *lowerer) lowerTypedMatch(s *MatchStmt) Stmt {
	matchName := l.nextTemp()
	matchValue := &Ident{Path: []string{matchName}}
	matchValue.ExprType = s.Value.Type()
	matchValue.Location = s.Value.Loc()
	matchValue.Source = s.Value.SourceExpr()

	out := &BlockStmt{Stmts: make([]Stmt, 0, 2)}
	SetStmtLocation(out, s.Loc())

	init := &LetStmt{
		Name:    matchName,
		Mutable: false,
		Type:    s.Value.Type(),
		Value:   s.Value,
	}
	SetStmtLocation(init, s.Value.Loc())
	out.Stmts = append(out.Stmts, init)

	elseStmt := Stmt(nil)
	empty := &BlockStmt{}
	SetStmtLocation(empty, s.Loc())
	elseStmt = empty

	for i := len(s.Arms) - 1; i >= 0; i-- {
		arm := s.Arms[i]
		if arm == nil {
			continue
		}
		body := l.lowerBlock(arm.Body)
		if arm.Wildcard {
			elseStmt = body
			continue
		}
		var cond Expr
		if arm.TypePattern != nil {
			bindName := arm.BindingName
			if bindName == "" {
				bindName = l.nextTemp()
			}
			if valueIdent, ok := s.Value.(*Ident); ok && len(valueIdent.Path) == 1 {
				body = l.rewriteMatchArmBody(body, valueIdent.Path[0], bindName)
			}
			cast := &CastExpr{Left: matchValue}
			cast.ExprType = arm.TypePattern
			cast.Location = body.Loc()
			binding := &LetStmt{
				Name:    bindName,
				Mutable: false,
				Type:    arm.TypePattern,
				Value:   cast,
			}
			SetStmtLocation(binding, body.Loc())
			body.Stmts = append([]Stmt{binding}, body.Stmts...)

			cond = &IsExpr{
				Left:        matchValue,
				Target:      arm.TypePattern,
				StaticKnown: false,
			}
			cond.(*IsExpr).ExprType = &typeinfo.BuiltinType{Name: "bool"}
			cond.(*IsExpr).Location = body.Loc()
		} else {
			cond = &BinaryExpr{Left: matchValue, Op: "==", Right: arm.Pattern}
			cond.(*BinaryExpr).ExprType = &typeinfo.BuiltinType{Name: "bool"}
			cond.(*BinaryExpr).Location = body.Loc()
		}
		ifStmt := &IfStmt{Cond: cond, Then: body, Else: elseStmt}
		SetStmtLocation(ifStmt, body.Loc())
		elseStmt = ifStmt
	}

	if elseStmt != nil {
		out.Stmts = append(out.Stmts, elseStmt)
	}
	return out
}

func (l *lowerer) rewriteMatchArmBody(body *BlockStmt, fromName, toName string) *BlockStmt {
	if body == nil || fromName == "" || toName == "" || fromName == toName {
		return body
	}
	out := &BlockStmt{Stmts: make([]Stmt, 0, len(body.Stmts))}
	SetStmtLocation(out, body.Loc())
	for _, stmt := range body.Stmts {
		out.Stmts = append(out.Stmts, l.rewriteStmtIdents(stmt, fromName, toName))
	}
	return out
}

func (l *lowerer) rewriteStmtIdents(stmt Stmt, fromName, toName string) Stmt {
	switch s := stmt.(type) {
	case nil:
		return nil
	case *BlockStmt:
		return l.rewriteMatchArmBody(s, fromName, toName)
	case *LetStmt:
		if s.Name == fromName {
			return s
		}
		out := *s
		out.Value = l.rewriteExprIdents(s.Value, fromName, toName)
		return &out
	case *ConstStmt:
		if s.Name == fromName {
			return s
		}
		out := *s
		out.Value = l.rewriteExprIdents(s.Value, fromName, toName)
		return &out
	case *ReturnStmt:
		out := *s
		out.Value = l.rewriteExprIdents(s.Value, fromName, toName)
		return &out
	case *ExprStmt:
		out := *s
		out.Value = l.rewriteExprIdents(s.Value, fromName, toName)
		return &out
	case *AssignStmt:
		out := *s
		out.Left = l.rewriteExprIdents(s.Left, fromName, toName)
		out.Right = l.rewriteExprIdents(s.Right, fromName, toName)
		return &out
	case *IfStmt:
		out := *s
		out.Cond = l.rewriteExprIdents(s.Cond, fromName, toName)
		out.Then = l.rewriteMatchArmBody(s.Then, fromName, toName)
		out.Else = l.rewriteStmtIdents(s.Else, fromName, toName)
		return &out
	case *MatchStmt:
		return s
	case *WhileStmt:
		out := *s
		out.Cond = l.rewriteExprIdents(s.Cond, fromName, toName)
		out.Body = l.rewriteMatchArmBody(s.Body, fromName, toName)
		return &out
	case *ForStmt:
		if s.IndexName == fromName || s.ValueName == fromName {
			return s
		}
		out := *s
		out.Iterable = l.rewriteExprIdents(s.Iterable, fromName, toName)
		out.Body = l.rewriteMatchArmBody(s.Body, fromName, toName)
		return &out
	case *LoopStmt:
		out := *s
		out.Init = l.rewriteStmtIdents(s.Init, fromName, toName)
		out.Cond = l.rewriteExprIdents(s.Cond, fromName, toName)
		out.Post = l.rewriteStmtIdents(s.Post, fromName, toName)
		out.Body = l.rewriteMatchArmBody(s.Body, fromName, toName)
		return &out
	case *LabelStmt:
		out := *s
		out.Stmt = l.rewriteStmtIdents(s.Stmt, fromName, toName)
		return &out
	case *DeferStmt:
		out := *s
		out.Body = l.rewriteStmtIdents(s.Body, fromName, toName)
		return &out
	case *ReleaseStmt:
		out := *s
		out.Value = l.rewriteExprIdents(s.Value, fromName, toName)
		return &out
	case *PanicStmt:
		out := *s
		out.Value = l.rewriteExprIdents(s.Value, fromName, toName)
		return &out
	case *LockStmt:
		if s.Name == fromName {
			return s
		}
		out := *s
		out.Value = l.rewriteExprIdents(s.Value, fromName, toName)
		out.Body = l.rewriteMatchArmBody(s.Body, fromName, toName)
		return &out
	case *UnsafeStmt:
		out := *s
		out.Body = l.rewriteMatchArmBody(s.Body, fromName, toName)
		return &out
	default:
		return stmt
	}
}

func (l *lowerer) rewriteExprIdents(expr Expr, fromName, toName string) Expr {
	switch e := expr.(type) {
	case nil:
		return nil
	case *Ident:
		if len(e.Path) == 1 && e.Path[0] == fromName {
			out := *e
			out.Path = []string{toName}
			return &out
		}
		return e
	case *PrefixExpr:
		out := *e
		out.Right = l.rewriteExprIdents(e.Right, fromName, toName)
		return &out
	case *BinaryExpr:
		out := *e
		out.Left = l.rewriteExprIdents(e.Left, fromName, toName)
		out.Right = l.rewriteExprIdents(e.Right, fromName, toName)
		return &out
	case *PostfixExpr:
		out := *e
		out.Left = l.rewriteExprIdents(e.Left, fromName, toName)
		return &out
	case *CallExpr:
		out := *e
		out.Callee = l.rewriteExprIdents(e.Callee, fromName, toName)
		out.Args = append([]Expr(nil), e.Args...)
		for i, arg := range out.Args {
			out.Args[i] = l.rewriteExprIdents(arg, fromName, toName)
		}
		return &out
	case *ConstructorCallExpr:
		out := *e
		out.Args = append([]Expr(nil), e.Args...)
		for i, arg := range out.Args {
			out.Args[i] = l.rewriteExprIdents(arg, fromName, toName)
		}
		return &out
	case *SelectorExpr:
		out := *e
		out.Left = l.rewriteExprIdents(e.Left, fromName, toName)
		return &out
	case *CastExpr:
		out := *e
		out.Left = l.rewriteExprIdents(e.Left, fromName, toName)
		return &out
	case *IsExpr:
		out := *e
		out.Left = l.rewriteExprIdents(e.Left, fromName, toName)
		return &out
	case *CatchExpr:
		out := *e
		out.Left = l.rewriteExprIdents(e.Left, fromName, toName)
		out.Fallback = l.rewriteExprIdents(e.Fallback, fromName, toName)
		out.Handler = l.rewriteMatchArmBody(e.Handler, fromName, toName)
		return &out
	case *CompositeLit:
		out := *e
		out.Items = append([]CompositeItem(nil), e.Items...)
		for i, item := range out.Items {
			item.Value = l.rewriteExprIdents(item.Value, fromName, toName)
			out.Items[i] = item
		}
		return &out
	case *IndexExpr:
		out := *e
		out.Left = l.rewriteExprIdents(e.Left, fromName, toName)
		out.Index = l.rewriteExprIdents(e.Index, fromName, toName)
		return &out
	default:
		return expr
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + (v % 10))
		v /= 10
	}
	return string(buf[i:])
}
