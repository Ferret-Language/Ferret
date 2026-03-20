package hir

import (
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/source"
)

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
	tempID      int
	nextLocalID int
}

func (l *lowerer) nextTempLocal() (string, int) {
	l.tempID++
	name := "__match" + itoa(l.tempID)
	id := l.nextLocalID
	l.nextLocalID++
	return name, id
}

func (l *lowerer) lowerFunc(fn *Func) *Func {
	if fn == nil {
		return nil
	}
	out := *fn
	l.nextLocalID = out.LocalCount
	out.Body = l.lowerBlock(fn.Body)
	out.LocalCount = l.nextLocalID
	return &out
}

func (l *lowerer) lowerBlock(block *BlockStmt) *BlockStmt {
	if block == nil {
		return nil
	}
	out := &BlockStmt{Stmts: make([]Stmt, 0, len(block.Stmts))}
	SetStmtLocation(out, block.Loc())
	for _, stmt := range block.Stmts {
		out.Stmts = append(out.Stmts, l.lowerStmtList(stmt)...)
	}
	return out
}

func (l *lowerer) lowerStmt(stmt Stmt) Stmt {
	lowered := l.lowerStmtList(stmt)
	switch len(lowered) {
	case 0:
		return nil
	case 1:
		return lowered[0]
	default:
		out := &BlockStmt{Stmts: lowered}
		SetStmtLocation(out, stmt.Loc())
		return out
	}
}

func (l *lowerer) lowerStmtList(stmt Stmt) []Stmt {
	switch s := stmt.(type) {
	case nil:
		return nil
	case *BlockStmt:
		return []Stmt{l.lowerBlock(s)}
	case *LetStmt:
		prelude, value := l.lowerExpr(s.Value)
		out := &LetStmt{Name: s.Name, LocalID: s.LocalID, Mutable: s.Mutable, Type: s.Type, Value: value}
		SetStmtLocation(out, s.Loc())
		return append(prelude, out)
	case *ConstStmt:
		prelude, value := l.lowerExpr(s.Value)
		out := &ConstStmt{Name: s.Name, LocalID: s.LocalID, Type: s.Type, Value: value}
		SetStmtLocation(out, s.Loc())
		return append(prelude, out)
	case *ReturnStmt:
		prelude, value := l.lowerExpr(s.Value)
		out := &ReturnStmt{Value: value}
		SetStmtLocation(out, s.Loc())
		return append(prelude, out)
	case *ExprStmt:
		prelude, value := l.lowerExpr(s.Value)
		out := &ExprStmt{Value: value}
		SetStmtLocation(out, s.Loc())
		return append(prelude, out)
	case *AssignStmt:
		leftPrelude, left := l.lowerExpr(s.Left)
		rightPrelude, right := l.lowerExpr(s.Right)
		out := &AssignStmt{Left: left, Right: right}
		SetStmtLocation(out, s.Loc())
		stmts := append([]Stmt{}, leftPrelude...)
		stmts = append(stmts, rightPrelude...)
		return append(stmts, out)
	case *IfStmt:
		prelude, cond := l.lowerExpr(s.Cond)
		out := &IfStmt{Cond: cond, Then: l.lowerBlock(s.Then), Else: l.lowerStmt(s.Else)}
		SetStmtLocation(out, s.Loc())
		if out.Else == nil {
			empty := &BlockStmt{}
			SetStmtLocation(empty, s.Loc())
			out.Else = empty
		}
		return append(prelude, out)
	case *MatchStmt:
		return l.lowerMatchStmt(s)
	case *WhileStmt:
		return l.lowerWhileStmt(s)
	case *ForStmt:
		return l.lowerForStmt(s)
	case *LoopStmt:
		var stmts []Stmt
		if s.Init != nil {
			stmts = append(stmts, l.lowerStmtList(s.Init)...)
		}
		prelude, cond := l.lowerExpr(s.Cond)
		stmts = append(stmts, prelude...)
		out := &LoopStmt{Init: nil, Cond: cond, Post: l.lowerStmt(s.Post), Body: l.lowerBlock(s.Body)}
		SetStmtLocation(out, s.Loc())
		stmts = append(stmts, out)
		return stmts
	case *LabelStmt:
		out := &LabelStmt{Name: s.Name, Stmt: l.lowerStmt(s.Stmt)}
		SetStmtLocation(out, s.Loc())
		return []Stmt{out}
	case *BreakStmt:
		return []Stmt{s}
	case *ContinueStmt:
		return []Stmt{s}
	case *DeferStmt:
		out := &DeferStmt{Body: l.lowerStmt(s.Body)}
		SetStmtLocation(out, s.Loc())
		return []Stmt{out}
	case *ReleaseStmt:
		prelude, value := l.lowerExpr(s.Value)
		out := &ReleaseStmt{Value: value}
		SetStmtLocation(out, s.Loc())
		return append(prelude, out)
	case *PanicStmt:
		prelude, value := l.lowerExpr(s.Value)
		out := &PanicStmt{Value: value}
		SetStmtLocation(out, s.Loc())
		return append(prelude, out)
	case *LockStmt:
		prelude, value := l.lowerExpr(s.Value)
		out := &LockStmt{Value: value, Name: s.Name, LocalID: s.LocalID, Body: l.lowerBlock(s.Body)}
		SetStmtLocation(out, s.Loc())
		return append(prelude, out)
	case *UnsafeStmt:
		out := &UnsafeStmt{Body: l.lowerBlock(s.Body)}
		SetStmtLocation(out, s.Loc())
		return []Stmt{out}
	default:
		return []Stmt{stmt}
	}
}

func (l *lowerer) lowerMatchStmt(s *MatchStmt) []Stmt {
	prelude, value := l.lowerExpr(s.Value)
	out := &MatchStmt{Value: value, Arms: make([]*MatchArm, 0, len(s.Arms))}
	SetStmtLocation(out, s.Loc())
	for _, arm := range s.Arms {
		if arm == nil {
			continue
		}
		out.Arms = append(out.Arms, &MatchArm{
			Pattern:     arm.Pattern,
			TypePattern: arm.TypePattern,
			Wildcard:    arm.Wildcard,
			Body:        l.lowerBlock(arm.Body),
		})
	}
	if l.hasTypeMatchArm(out) {
		return append(prelude, l.lowerTypedMatch(out))
	}
	return append(prelude, out)
}

func (l *lowerer) lowerWhileStmt(s *WhileStmt) []Stmt {
	prelude, cond := l.lowerExpr(s.Cond)
	body := l.lowerBlock(s.Body)
	if len(prelude) == 0 {
		out := &LoopStmt{Cond: cond, Body: body}
		SetStmtLocation(out, s.Loc())
		return []Stmt{out}
	}
	negated := &PrefixExpr{Op: "!", Right: cond}
	negated.ExprType = &typeinfo.BuiltinType{Name: "bool"}
	negated.Location = s.Cond.Loc()
	negated.Source = s.Cond.SourceExpr()
	breakStmt := &BreakStmt{}
	SetStmtLocation(breakStmt, s.Loc())
	breakBlock := &BlockStmt{Stmts: []Stmt{breakStmt}}
	SetStmtLocation(breakBlock, s.Loc())
	empty := &BlockStmt{}
	SetStmtLocation(empty, s.Loc())
	guard := &IfStmt{Cond: negated, Then: breakBlock, Else: empty}
	SetStmtLocation(guard, s.Loc())
	loopBody := &BlockStmt{Stmts: append(append([]Stmt{}, prelude...), guard)}
	SetStmtLocation(loopBody, s.Loc())
	loopBody.Stmts = append(loopBody.Stmts, body.Stmts...)
	out := &LoopStmt{Body: loopBody}
	SetStmtLocation(out, s.Loc())
	return []Stmt{out}
}

func (l *lowerer) lowerForStmt(s *ForStmt) []Stmt {
	prelude, iterable := l.lowerExpr(s.Iterable)
	arrayType, ok := iterable.Type().(*typeinfo.ArrayType)
	if !ok {
		out := &ForStmt{Iterable: iterable, IndexName: s.IndexName, IndexID: s.IndexID, ValueName: s.ValueName, ValueID: s.ValueID, Body: l.lowerBlock(s.Body)}
		SetStmtLocation(out, s.Loc())
		return append(prelude, out)
	}

	iterName, iterID := l.nextTempLocal()
	indexName, indexID := l.nextTempLocal()

	iterDecl := &LetStmt{Name: iterName, LocalID: iterID, Mutable: false, Type: iterable.Type(), Value: iterable}
	SetStmtLocation(iterDecl, s.Iterable.Loc())

	zero := &NumberLit{Value: "0"}
	zero.ExprType = &typeinfo.BuiltinType{Name: "usize"}
	zero.Location = s.Loc()
	zero.Source = s.Iterable.SourceExpr()
	indexDecl := &LetStmt{Name: indexName, LocalID: indexID, Mutable: true, Type: zero.Type(), Value: zero}
	SetStmtLocation(indexDecl, s.Loc())

	indexIdent := makeTempIdent(indexName, indexID, zero.Type(), s.Loc())

	limit := &NumberLit{Value: itoa(int(arrayType.Len))}
	limit.ExprType = zero.Type()
	limit.Location = s.Loc()

	cond := &BinaryExpr{Left: indexIdent, Op: "<", Right: limit}
	cond.ExprType = &typeinfo.BuiltinType{Name: "bool"}
	cond.Location = s.Loc()

	body := &BlockStmt{}
	SetStmtLocation(body, s.Loc())
	if s.IndexName != "" {
		indexValue := makeTempIdent(indexName, indexID, zero.Type(), s.Loc())
		indexBind := &LetStmt{Name: s.IndexName, LocalID: s.IndexID, Mutable: false, Type: zero.Type(), Value: indexValue}
		SetStmtLocation(indexBind, s.Loc())
		body.Stmts = append(body.Stmts, indexBind)
	}
	if s.ValueName != "" {
		valueIndex := makeTempIdent(indexName, indexID, zero.Type(), s.Loc())
		valueExpr := &IndexExpr{Left: makeTempIdent(iterName, iterID, iterable.Type(), s.Iterable.Loc()), Index: valueIndex}
		valueExpr.ExprType = arrayType.Inner
		valueExpr.Location = s.Loc()
		valueBind := &LetStmt{Name: s.ValueName, LocalID: s.ValueID, Mutable: false, Type: arrayType.Inner, Value: valueExpr}
		SetStmtLocation(valueBind, s.Loc())
		body.Stmts = append(body.Stmts, valueBind)
	}
	if loweredBody := l.lowerBlock(s.Body); loweredBody != nil {
		body.Stmts = append(body.Stmts, loweredBody.Stmts...)
	}

	postLeft := makeTempIdent(indexName, indexID, zero.Type(), s.Loc())
	postRight := &BinaryExpr{
		Left:  makeTempIdent(indexName, indexID, zero.Type(), s.Loc()),
		Op:    "+",
		Right: &NumberLit{Value: "1"},
	}
	postRight.ExprType = zero.Type()
	postRight.Location = s.Loc()
	postRight.Right.(*NumberLit).ExprType = zero.Type()
	postRight.Right.(*NumberLit).Location = s.Loc()
	post := &AssignStmt{Left: postLeft, Right: postRight}
	SetStmtLocation(post, s.Loc())

	loop := &LoopStmt{Cond: cond, Post: post, Body: body}
	SetStmtLocation(loop, s.Loc())

	stmts := append([]Stmt{}, prelude...)
	stmts = append(stmts, iterDecl, indexDecl, loop)
	return stmts
}

func (l *lowerer) lowerExpr(expr Expr) ([]Stmt, Expr) {
	switch e := expr.(type) {
	case nil, *Ident, *BadExpr, *NumberLit, *StringLit, *NoneLit:
		return nil, expr
	case *PrefixExpr:
		prelude, right := l.lowerExpr(e.Right)
		out := *e
		out.Right = right
		return prelude, &out
	case *BinaryExpr:
		leftPrelude, left := l.lowerExpr(e.Left)
		rightPrelude, right := l.lowerExpr(e.Right)
		out := *e
		out.Left = left
		out.Right = right
		prelude := append([]Stmt{}, leftPrelude...)
		prelude = append(prelude, rightPrelude...)
		return prelude, &out
	case *PostfixExpr:
		prelude, left := l.lowerExpr(e.Left)
		out := *e
		out.Left = left
		return prelude, &out
	case *CallExpr:
		prelude, callee := l.lowerExpr(e.Callee)
		out := *e
		out.Callee = callee
		out.Args = append([]Expr(nil), e.Args...)
		for i, arg := range out.Args {
			argPrelude, lowered := l.lowerExpr(arg)
			prelude = append(prelude, argPrelude...)
			out.Args[i] = lowered
		}
		return prelude, &out
	case *ConstructorCallExpr:
		out := *e
		out.Args = append([]Expr(nil), e.Args...)
		var prelude []Stmt
		for i, arg := range out.Args {
			argPrelude, lowered := l.lowerExpr(arg)
			prelude = append(prelude, argPrelude...)
			out.Args[i] = lowered
		}
		return prelude, &out
	case *SelectorExpr:
		prelude, left := l.lowerExpr(e.Left)
		out := *e
		out.Left = left
		return prelude, &out
	case *CastExpr:
		prelude, left := l.lowerExpr(e.Left)
		out := *e
		out.Left = left
		return prelude, &out
	case *IsExpr:
		prelude, left := l.lowerExpr(e.Left)
		out := *e
		out.Left = left
		return prelude, &out
	case *MatchExpr:
		return l.lowerMatchExpr(e)
	case *CatchExpr:
		leftPrelude, left := l.lowerExpr(e.Left)
		fallbackPrelude, fallback := l.lowerExpr(e.Fallback)
		out := *e
		out.Left = left
		out.Fallback = fallback
		out.Handler = l.lowerBlock(e.Handler)
		prelude := append([]Stmt{}, leftPrelude...)
		prelude = append(prelude, fallbackPrelude...)
		return prelude, &out
	case *CompositeLit:
		out := *e
		out.Items = append([]CompositeItem(nil), e.Items...)
		var prelude []Stmt
		for i, item := range out.Items {
			itemPrelude, lowered := l.lowerExpr(item.Value)
			prelude = append(prelude, itemPrelude...)
			item.Value = lowered
			out.Items[i] = item
		}
		return prelude, &out
	case *IndexExpr:
		leftPrelude, left := l.lowerExpr(e.Left)
		indexPrelude, index := l.lowerExpr(e.Index)
		out := *e
		out.Left = left
		out.Index = index
		prelude := append([]Stmt{}, leftPrelude...)
		prelude = append(prelude, indexPrelude...)
		return prelude, &out
	default:
		return nil, expr
	}
}

func (l *lowerer) lowerMatchExpr(expr *MatchExpr) ([]Stmt, Expr) {
	if expr == nil {
		return nil, nil
	}
	valuePrelude, value := l.lowerExpr(expr.Value)
	resultName, resultID := l.nextTempLocal()
	resultDecl := &LetStmt{Name: resultName, LocalID: resultID, Mutable: false, Type: expr.Type(), Value: nil}
	SetStmtLocation(resultDecl, expr.Loc())
	matchStmt := &MatchStmt{Value: value, Arms: make([]*MatchArm, 0, len(expr.Arms))}
	SetStmtLocation(matchStmt, expr.Loc())
	for _, arm := range expr.Arms {
		if arm == nil {
			continue
		}
		matchStmt.Arms = append(matchStmt.Arms, l.lowerMatchExprArm(arm, resultName, resultID, expr.Type()))
	}
	prelude := append([]Stmt{}, valuePrelude...)
	prelude = append(prelude, resultDecl)
	prelude = append(prelude, l.lowerMatchStmt(matchStmt)...)
	result := makeTempIdent(resultName, resultID, expr.Type(), expr.Loc())
	result.Source = expr.SourceExpr()
	return prelude, result
}

func (l *lowerer) lowerMatchExprArm(arm *MatchArm, resultName string, resultID int, resultType typeinfo.Type) *MatchArm {
	if arm == nil {
		return nil
	}
	out := &MatchArm{
		Pattern:     arm.Pattern,
		TypePattern: arm.TypePattern,
		Wildcard:    arm.Wildcard,
	}
	if arm.Body == nil {
		return out
	}
	out.Body = &BlockStmt{Stmts: make([]Stmt, 0, len(arm.Body.Stmts))}
	SetStmtLocation(out.Body, arm.Body.Loc())
	if len(arm.Body.Stmts) == 0 {
		return out
	}
	for _, stmt := range arm.Body.Stmts[:len(arm.Body.Stmts)-1] {
		out.Body.Stmts = append(out.Body.Stmts, stmt)
	}
	last := arm.Body.Stmts[len(arm.Body.Stmts)-1]
	if hirStmtDefinitelyExits(last) {
		out.Body.Stmts = append(out.Body.Stmts, last)
		return out
	}
	if exprStmt, ok := last.(*ExprStmt); ok {
		assign := &AssignStmt{Left: makeTempIdent(resultName, resultID, resultType, exprStmt.Loc()), Right: exprStmt.Value}
		SetStmtLocation(assign, exprStmt.Loc())
		out.Body.Stmts = append(out.Body.Stmts, assign)
		return out
	}
	out.Body.Stmts = append(out.Body.Stmts, last)
	return out
}

func makeTempIdent(name string, localID int, typ typeinfo.Type, loc source.Location) *Ident {
	ident := &Ident{Path: []string{name}, LocalID: localID}
	ident.ExprType = typ
	ident.Location = loc
	return ident
}

func (l *lowerer) hasTypeMatchArm(s *MatchStmt) bool {
	if s == nil {
		return false
	}
	for _, arm := range s.Arms {
		if arm != nil && arm.TypePattern != nil {
			return true
		}
	}
	return false
}

func (l *lowerer) lowerTypedMatch(s *MatchStmt) Stmt {
	matchName, matchID := l.nextTempLocal()
	matchValue := &Ident{Path: []string{matchName}, LocalID: matchID}
	matchValue.ExprType = s.Value.Type()
	matchValue.Location = s.Value.Loc()
	matchValue.Source = s.Value.SourceExpr()

	out := &BlockStmt{Stmts: make([]Stmt, 0, 2)}
	SetStmtLocation(out, s.Loc())

	init := &LetStmt{
		Name:    matchName,
		LocalID: matchID,
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
			bindName, bindID := l.nextTempLocal()
			if valueIdent, ok := s.Value.(*Ident); ok {
				body = l.rewriteMatchArmBody(body, valueIdent.Path[0], valueIdent.LocalID, bindName, bindID)
			}
			cast := &CastExpr{Left: matchValue}
			cast.ExprType = arm.TypePattern
			cast.Location = body.Loc()
			binding := &LetStmt{
				Name:    bindName,
				LocalID: bindID,
				Mutable: false,
				Type:    arm.TypePattern,
				Value:   cast,
			}
			SetStmtLocation(binding, body.Loc())
			body.Stmts = append([]Stmt{binding}, body.Stmts...)
			cond = &IsExpr{Left: matchValue, Target: arm.TypePattern, StaticKnown: false}
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

func (l *lowerer) rewriteMatchArmBody(body *BlockStmt, fromName string, fromID int, toName string, toID int) *BlockStmt {
	if body == nil || toName == "" || (fromName == "" && fromID < 0) {
		return body
	}
	out := &BlockStmt{Stmts: make([]Stmt, 0, len(body.Stmts))}
	SetStmtLocation(out, body.Loc())
	for _, stmt := range body.Stmts {
		out.Stmts = append(out.Stmts, l.rewriteStmtIdents(stmt, fromName, fromID, toName, toID))
	}
	return out
}

func (l *lowerer) rewriteStmtIdents(stmt Stmt, fromName string, fromID int, toName string, toID int) Stmt {
	switch s := stmt.(type) {
	case nil:
		return nil
	case *BlockStmt:
		return l.rewriteMatchArmBody(s, fromName, fromID, toName, toID)
	case *LetStmt:
		if (fromName != "" && s.Name == fromName) || (fromID >= 0 && s.LocalID == fromID) {
			return s
		}
		out := *s
		out.Value = l.rewriteExprIdents(s.Value, fromName, fromID, toName, toID)
		return &out
	case *ConstStmt:
		if (fromName != "" && s.Name == fromName) || (fromID >= 0 && s.LocalID == fromID) {
			return s
		}
		out := *s
		out.Value = l.rewriteExprIdents(s.Value, fromName, fromID, toName, toID)
		return &out
	case *ReturnStmt:
		out := *s
		out.Value = l.rewriteExprIdents(s.Value, fromName, fromID, toName, toID)
		return &out
	case *ExprStmt:
		out := *s
		out.Value = l.rewriteExprIdents(s.Value, fromName, fromID, toName, toID)
		return &out
	case *AssignStmt:
		out := *s
		out.Left = l.rewriteExprIdents(s.Left, fromName, fromID, toName, toID)
		out.Right = l.rewriteExprIdents(s.Right, fromName, fromID, toName, toID)
		return &out
	case *IfStmt:
		out := *s
		out.Cond = l.rewriteExprIdents(s.Cond, fromName, fromID, toName, toID)
		out.Then = l.rewriteMatchArmBody(s.Then, fromName, fromID, toName, toID)
		out.Else = l.rewriteStmtIdents(s.Else, fromName, fromID, toName, toID)
		return &out
	case *MatchStmt:
		return s
	case *WhileStmt:
		out := *s
		out.Cond = l.rewriteExprIdents(s.Cond, fromName, fromID, toName, toID)
		out.Body = l.rewriteMatchArmBody(s.Body, fromName, fromID, toName, toID)
		return &out
	case *ForStmt:
		if (fromName != "" && (s.IndexName == fromName || s.ValueName == fromName)) || (fromID >= 0 && (s.IndexID == fromID || s.ValueID == fromID)) {
			return s
		}
		out := *s
		out.Iterable = l.rewriteExprIdents(s.Iterable, fromName, fromID, toName, toID)
		out.Body = l.rewriteMatchArmBody(s.Body, fromName, fromID, toName, toID)
		return &out
	case *LoopStmt:
		out := *s
		out.Init = l.rewriteStmtIdents(s.Init, fromName, fromID, toName, toID)
		out.Cond = l.rewriteExprIdents(s.Cond, fromName, fromID, toName, toID)
		out.Post = l.rewriteStmtIdents(s.Post, fromName, fromID, toName, toID)
		out.Body = l.rewriteMatchArmBody(s.Body, fromName, fromID, toName, toID)
		return &out
	case *LabelStmt:
		out := *s
		out.Stmt = l.rewriteStmtIdents(s.Stmt, fromName, fromID, toName, toID)
		return &out
	case *DeferStmt:
		out := *s
		out.Body = l.rewriteStmtIdents(s.Body, fromName, fromID, toName, toID)
		return &out
	case *ReleaseStmt:
		out := *s
		out.Value = l.rewriteExprIdents(s.Value, fromName, fromID, toName, toID)
		return &out
	case *PanicStmt:
		out := *s
		out.Value = l.rewriteExprIdents(s.Value, fromName, fromID, toName, toID)
		return &out
	case *LockStmt:
		if (fromName != "" && s.Name == fromName) || (fromID >= 0 && s.LocalID == fromID) {
			return s
		}
		out := *s
		out.Value = l.rewriteExprIdents(s.Value, fromName, fromID, toName, toID)
		out.Body = l.rewriteMatchArmBody(s.Body, fromName, fromID, toName, toID)
		return &out
	case *UnsafeStmt:
		out := *s
		out.Body = l.rewriteMatchArmBody(s.Body, fromName, fromID, toName, toID)
		return &out
	default:
		return stmt
	}
}

func (l *lowerer) rewriteExprIdents(expr Expr, fromName string, fromID int, toName string, toID int) Expr {
	switch e := expr.(type) {
	case nil:
		return nil
	case *Ident:
		if (fromName != "" && len(e.Path) == 1 && e.Path[0] == fromName) || (fromID >= 0 && e.LocalID == fromID) {
			out := *e
			out.Path = []string{toName}
			out.LocalID = toID
			return &out
		}
		return e
	case *PrefixExpr:
		out := *e
		out.Right = l.rewriteExprIdents(e.Right, fromName, fromID, toName, toID)
		return &out
	case *BinaryExpr:
		out := *e
		out.Left = l.rewriteExprIdents(e.Left, fromName, fromID, toName, toID)
		out.Right = l.rewriteExprIdents(e.Right, fromName, fromID, toName, toID)
		return &out
	case *PostfixExpr:
		out := *e
		out.Left = l.rewriteExprIdents(e.Left, fromName, fromID, toName, toID)
		return &out
	case *CallExpr:
		out := *e
		out.Callee = l.rewriteExprIdents(e.Callee, fromName, fromID, toName, toID)
		out.Args = append([]Expr(nil), e.Args...)
		for i, arg := range out.Args {
			out.Args[i] = l.rewriteExprIdents(arg, fromName, fromID, toName, toID)
		}
		return &out
	case *ConstructorCallExpr:
		out := *e
		out.Args = append([]Expr(nil), e.Args...)
		for i, arg := range out.Args {
			out.Args[i] = l.rewriteExprIdents(arg, fromName, fromID, toName, toID)
		}
		return &out
	case *SelectorExpr:
		out := *e
		out.Left = l.rewriteExprIdents(e.Left, fromName, fromID, toName, toID)
		return &out
	case *CastExpr:
		out := *e
		out.Left = l.rewriteExprIdents(e.Left, fromName, fromID, toName, toID)
		return &out
	case *IsExpr:
		out := *e
		out.Left = l.rewriteExprIdents(e.Left, fromName, fromID, toName, toID)
		return &out
	case *CatchExpr:
		out := *e
		out.Left = l.rewriteExprIdents(e.Left, fromName, fromID, toName, toID)
		out.Fallback = l.rewriteExprIdents(e.Fallback, fromName, fromID, toName, toID)
		out.Handler = l.rewriteMatchArmBody(e.Handler, fromName, fromID, toName, toID)
		return &out
	case *CompositeLit:
		out := *e
		out.Items = append([]CompositeItem(nil), e.Items...)
		for i, item := range out.Items {
			item.Value = l.rewriteExprIdents(item.Value, fromName, fromID, toName, toID)
			out.Items[i] = item
		}
		return &out
	case *IndexExpr:
		out := *e
		out.Left = l.rewriteExprIdents(e.Left, fromName, fromID, toName, toID)
		out.Index = l.rewriteExprIdents(e.Index, fromName, fromID, toName, toID)
		return &out
	default:
		return expr
	}
}

func hirStmtDefinitelyExits(stmt Stmt) bool {
	switch s := stmt.(type) {
	case nil:
		return false
	case *BlockStmt:
		if len(s.Stmts) == 0 {
			return false
		}
		return hirStmtDefinitelyExits(s.Stmts[len(s.Stmts)-1])
	case *ReturnStmt, *PanicStmt, *BreakStmt, *ContinueStmt:
		return true
	case *IfStmt:
		return hirStmtDefinitelyExits(s.Then) && hirStmtDefinitelyExits(s.Else)
	case *MatchStmt:
		if len(s.Arms) == 0 {
			return false
		}
		hasWildcard := false
		for _, arm := range s.Arms {
			if arm == nil || !hirStmtDefinitelyExits(arm.Body) {
				return false
			}
			if arm.Wildcard {
				hasWildcard = true
			}
		}
		return hasWildcard
	default:
		return false
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
