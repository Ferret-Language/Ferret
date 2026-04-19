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
	if l.hasSpecialMatchArm(out) {
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
	if rangeExpr, ok := iterable.(*RangeExpr); ok {
		return l.lowerRangeForStmt(s, rangeExpr, prelude)
	}
	elemType := typeinfo.Type(nil)
	limitExpr := Expr(nil)
	limitType := &typeinfo.BuiltinType{Name: "usize"}
	sliceIter := false

	if arrayType, ok := iterable.Type().(*typeinfo.ArrayType); ok {
		elemType = arrayType.Inner
		limit := &NumberLit{Value: itoa(int(arrayType.Len))}
		limit.ExprType = limitType
		limit.Location = s.Loc()
		limitExpr = limit
	} else if sliceType, ok := iterable.Type().(*typeinfo.SliceType); ok {
		elemType = sliceType.Inner
		sliceIter = true
	}

	if elemType == nil {
		out := &ForStmt{Iterable: iterable, IndexName: s.IndexName, IndexID: s.IndexID, ValueName: s.ValueName, ValueID: s.ValueID, Body: l.lowerBlock(s.Body)}
		SetStmtLocation(out, s.Loc())
		return append(prelude, out)
	}

	iterName, iterID := l.nextTempLocal()
	indexName, indexID := l.nextTempLocal()

	iterDecl := &LetStmt{Name: iterName, LocalID: iterID, Mutable: false, Type: iterable.Type(), Value: iterable}
	SetStmtLocation(iterDecl, s.Iterable.Loc())

	if sliceIter {
		callee := &Ident{Path: []string{"global", "len"}, LocalID: -1}
		callee.ExprType = &typeinfo.FuncType{
			Params: []typeinfo.ParamSpec{
				{Type: iterable.Type()},
			},
			Result: limitType,
		}
		callee.Location = s.Loc()
		limit := &CallExpr{
			Callee: callee,
			Args:   []Expr{makeTempIdent(iterName, iterID, iterable.Type(), s.Iterable.Loc())},
		}
		limit.ExprType = limitType
		limit.Location = s.Loc()
		limitExpr = limit
	}

	zero := &NumberLit{Value: "0"}
	zero.ExprType = limitType
	zero.Location = s.Loc()
	zero.Source = s.Iterable.SourceExpr()
	indexDecl := &LetStmt{Name: indexName, LocalID: indexID, Mutable: true, Type: zero.Type(), Value: zero}
	SetStmtLocation(indexDecl, s.Loc())

	indexIdent := makeTempIdent(indexName, indexID, zero.Type(), s.Loc())

	cond := &BinaryExpr{Left: indexIdent, Op: "<", Right: limitExpr}
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
		valueExpr.ExprType = elemType
		valueExpr.Location = s.Loc()
		valueBind := &LetStmt{Name: s.ValueName, LocalID: s.ValueID, Mutable: false, Type: elemType, Value: valueExpr}
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

func (l *lowerer) lowerRangeForStmt(s *ForStmt, r *RangeExpr, prelude []Stmt) []Stmt {
	rangeType, _ := r.Type().(*typeinfo.RangeType)
	elemType := typeinfo.Type(&typeinfo.BuiltinType{Name: typeinfo.DefaultIntTypeName})
	if rangeType != nil && rangeType.Elem != nil {
		elemType = rangeType.Elem
	}
	boolType := &typeinfo.BuiltinType{Name: "bool"}
	usizeType := &typeinfo.BuiltinType{Name: "usize"}

	startName, startID := l.nextTempLocal()
	endName, endID := l.nextTempLocal()
	stepName, stepID := l.nextTempLocal()
	valueName, valueID := l.nextTempLocal()
	indexName, indexID := l.nextTempLocal()

	startDecl := &LetStmt{Name: startName, LocalID: startID, Mutable: false, Type: elemType, Value: r.Start}
	SetStmtLocation(startDecl, r.Start.Loc())

	endDecl := &LetStmt{Name: endName, LocalID: endID, Mutable: false, Type: elemType, Value: r.End}
	SetStmtLocation(endDecl, r.End.Loc())

	stepExpr := r.Step
	if stepExpr == nil {
		one := &NumberLit{Value: "1"}
		one.ExprType = elemType
		one.Location = s.Loc()
		stepExpr = one
	}
	stepDecl := &LetStmt{Name: stepName, LocalID: stepID, Mutable: false, Type: elemType, Value: stepExpr}
	SetStmtLocation(stepDecl, s.Loc())

	valueDecl := &LetStmt{
		Name:    valueName,
		LocalID: valueID,
		Mutable: true,
		Type:    elemType,
		Value:   makeTempIdent(startName, startID, elemType, s.Loc()),
	}
	SetStmtLocation(valueDecl, s.Loc())

	zero := &NumberLit{Value: "0"}
	zero.ExprType = usizeType
	zero.Location = s.Loc()
	indexDecl := &LetStmt{Name: indexName, LocalID: indexID, Mutable: true, Type: usizeType, Value: zero}
	SetStmtLocation(indexDecl, s.Loc())

	stepZero := &NumberLit{Value: "0"}
	stepZero.ExprType = elemType
	stepZero.Location = s.Loc()
	stepPos := &BinaryExpr{
		Left:  makeTempIdent(stepName, stepID, elemType, s.Loc()),
		Op:    ">",
		Right: stepZero,
	}
	stepPos.ExprType = boolType
	stepPos.Location = s.Loc()

	stepZeroNeg := &NumberLit{Value: "0"}
	stepZeroNeg.ExprType = elemType
	stepZeroNeg.Location = s.Loc()
	stepNeg := &BinaryExpr{
		Left:  makeTempIdent(stepName, stepID, elemType, s.Loc()),
		Op:    "<",
		Right: stepZeroNeg,
	}
	stepNeg.ExprType = boolType
	stepNeg.Location = s.Loc()

	posCmpOp := "<"
	negCmpOp := ">"
	if r.Inclusive {
		posCmpOp = "<="
		negCmpOp = ">="
	}
	posCmp := &BinaryExpr{
		Left:  makeTempIdent(valueName, valueID, elemType, s.Loc()),
		Op:    posCmpOp,
		Right: makeTempIdent(endName, endID, elemType, s.Loc()),
	}
	posCmp.ExprType = boolType
	posCmp.Location = s.Loc()
	negCmp := &BinaryExpr{
		Left:  makeTempIdent(valueName, valueID, elemType, s.Loc()),
		Op:    negCmpOp,
		Right: makeTempIdent(endName, endID, elemType, s.Loc()),
	}
	negCmp.ExprType = boolType
	negCmp.Location = s.Loc()

	posCond := &BinaryExpr{Left: stepPos, Op: "&&", Right: posCmp}
	posCond.ExprType = boolType
	posCond.Location = s.Loc()
	negCond := &BinaryExpr{Left: stepNeg, Op: "&&", Right: negCmp}
	negCond.ExprType = boolType
	negCond.Location = s.Loc()
	cond := &BinaryExpr{Left: posCond, Op: "||", Right: negCond}
	cond.ExprType = boolType
	cond.Location = s.Loc()

	body := &BlockStmt{}
	SetStmtLocation(body, s.Loc())
	if s.IndexName != "" {
		indexBind := &LetStmt{
			Name:    s.IndexName,
			LocalID: s.IndexID,
			Mutable: false,
			Type:    usizeType,
			Value:   makeTempIdent(indexName, indexID, usizeType, s.Loc()),
		}
		SetStmtLocation(indexBind, s.Loc())
		body.Stmts = append(body.Stmts, indexBind)
	}
	if s.ValueName != "" {
		valueBind := &LetStmt{
			Name:    s.ValueName,
			LocalID: s.ValueID,
			Mutable: false,
			Type:    elemType,
			Value:   makeTempIdent(valueName, valueID, elemType, s.Loc()),
		}
		SetStmtLocation(valueBind, s.Loc())
		body.Stmts = append(body.Stmts, valueBind)
	}
	if loweredBody := l.lowerBlock(s.Body); loweredBody != nil {
		body.Stmts = append(body.Stmts, loweredBody.Stmts...)
	}

	nextValue := &BinaryExpr{
		Left:  makeTempIdent(valueName, valueID, elemType, s.Loc()),
		Op:    "+",
		Right: makeTempIdent(stepName, stepID, elemType, s.Loc()),
	}
	nextValue.ExprType = elemType
	nextValue.Location = s.Loc()
	valuePost := &AssignStmt{
		Left:  makeTempIdent(valueName, valueID, elemType, s.Loc()),
		Right: nextValue,
	}
	SetStmtLocation(valuePost, s.Loc())

	indexInc := &BinaryExpr{
		Left:  makeTempIdent(indexName, indexID, usizeType, s.Loc()),
		Op:    "+",
		Right: &NumberLit{Value: "1"},
	}
	indexInc.ExprType = usizeType
	indexInc.Location = s.Loc()
	indexInc.Right.(*NumberLit).ExprType = usizeType
	indexInc.Right.(*NumberLit).Location = s.Loc()
	indexPost := &AssignStmt{
		Left:  makeTempIdent(indexName, indexID, usizeType, s.Loc()),
		Right: indexInc,
	}
	SetStmtLocation(indexPost, s.Loc())

	post := &BlockStmt{Stmts: []Stmt{valuePost, indexPost}}
	SetStmtLocation(post, s.Loc())

	loop := &LoopStmt{Cond: cond, Post: post, Body: body}
	SetStmtLocation(loop, s.Loc())

	stmts := append([]Stmt{}, prelude...)
	stmts = append(stmts, startDecl, endDecl, stepDecl, valueDecl, indexDecl, loop)
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
	case *RangeExpr:
		startPrelude, start := l.lowerExpr(e.Start)
		endPrelude, end := l.lowerExpr(e.End)
		stepPrelude, step := l.lowerExpr(e.Step)
		out := *e
		out.Start = start
		out.End = end
		out.Step = step
		prelude := append([]Stmt{}, startPrelude...)
		prelude = append(prelude, endPrelude...)
		prelude = append(prelude, stepPrelude...)
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
	case *ClosureLit:
		out := *e
		out.Captures = append([]Expr(nil), e.Captures...)
		var prelude []Stmt
		for i, capture := range out.Captures {
			capturePrelude, lowered := l.lowerExpr(capture)
			prelude = append(prelude, capturePrelude...)
			out.Captures[i] = lowered
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
		return l.lowerCatchExpr(e)
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

func (l *lowerer) lowerCatchExpr(expr *CatchExpr) ([]Stmt, Expr) {
	if expr == nil {
		return nil, nil
	}
	leftPrelude, left := l.lowerExpr(expr.Left)
	errUnion, _ := left.Type().(*typeinfo.ErrorUnionType)
	if errUnion == nil {
		out := *expr
		out.Left = left
		out.Fallback = nil
		out.Handler = l.lowerBlock(expr.Handler)
		return leftPrelude, &out
	}

	unionName, unionID := l.nextTempLocal()
	unionDecl := &LetStmt{Name: unionName, LocalID: unionID, Mutable: false, Type: left.Type(), Value: left}
	SetStmtLocation(unionDecl, left.Loc())
	unionValue := makeTempIdent(unionName, unionID, left.Type(), left.Loc())
	unionValue.Source = left.SourceExpr()

	resultName, resultID := l.nextTempLocal()
	resultDecl := &LetStmt{Name: resultName, LocalID: resultID, Mutable: false, Type: expr.Type(), Value: nil}
	SetStmtLocation(resultDecl, expr.Loc())
	resultValue := makeTempIdent(resultName, resultID, expr.Type(), expr.Loc())
	resultValue.Source = expr.SourceExpr()

	cond := &IsExpr{Left: unionValue, Target: errUnion.Value, StaticKnown: false}
	cond.ExprType = &typeinfo.BuiltinType{Name: "bool"}
	cond.Location = expr.Loc()
	cond.Source = expr.SourceExpr()

	successCast := &CastExpr{Left: unionValue}
	successCast.ExprType = errUnion.Value
	successCast.Location = expr.Loc()
	successCast.Source = expr.SourceExpr()
	successAssign := &AssignStmt{Left: resultValue, Right: successCast}
	SetStmtLocation(successAssign, expr.Loc())
	thenBlock := &BlockStmt{Stmts: []Stmt{successAssign}}
	SetStmtLocation(thenBlock, expr.Loc())

	var elseStmt Stmt
	if expr.Handler != nil {
		handler := l.lowerBlock(expr.Handler)
		if expr.PayloadName != "" && expr.PayloadID >= 0 {
			payloadCast := &CastExpr{Left: unionValue}
			payloadCast.ExprType = errUnion.Error
			payloadCast.Location = expr.Loc()
			payloadCast.Source = expr.SourceExpr()
			payloadBind := &LetStmt{
				Name:    expr.PayloadName,
				LocalID: expr.PayloadID,
				Mutable: false,
				Type:    errUnion.Error,
				Value:   payloadCast,
			}
			SetStmtLocation(payloadBind, expr.Loc())
			handler.Stmts = append([]Stmt{payloadBind}, handler.Stmts...)
		}
		handler = l.lowerBlockResult(handler, resultName, resultID, expr.Type())
		elseStmt = handler
	} else {
		fallbackPrelude, fallback := l.lowerExpr(expr.Fallback)
		fallbackAssign := &AssignStmt{Left: resultValue, Right: fallback}
		SetStmtLocation(fallbackAssign, expr.Loc())
		elseBlock := &BlockStmt{Stmts: make([]Stmt, 0, len(fallbackPrelude)+1)}
		SetStmtLocation(elseBlock, expr.Loc())
		elseBlock.Stmts = append(elseBlock.Stmts, fallbackPrelude...)
		elseBlock.Stmts = append(elseBlock.Stmts, fallbackAssign)
		elseStmt = elseBlock
	}

	ifStmt := &IfStmt{Cond: cond, Then: thenBlock, Else: elseStmt}
	SetStmtLocation(ifStmt, expr.Loc())

	prelude := append([]Stmt{}, leftPrelude...)
	prelude = append(prelude, unionDecl, resultDecl, ifStmt)
	return prelude, resultValue
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
	out.Body = l.lowerBlockResult(arm.Body, resultName, resultID, resultType)
	return out
}

func (l *lowerer) lowerBlockResult(body *BlockStmt, resultName string, resultID int, resultType typeinfo.Type) *BlockStmt {
	if body == nil {
		return nil
	}
	out := &BlockStmt{Stmts: make([]Stmt, 0, len(body.Stmts))}
	SetStmtLocation(out, body.Loc())
	if len(body.Stmts) == 0 {
		return out
	}
	out.Stmts = append(out.Stmts, body.Stmts[:len(body.Stmts)-1]...)
	last := body.Stmts[len(body.Stmts)-1]
	if hirStmtDefinitelyExits(last) {
		out.Stmts = append(out.Stmts, last)
		return out
	}
	if exprStmt, ok := last.(*ExprStmt); ok {
		assign := &AssignStmt{Left: makeTempIdent(resultName, resultID, resultType, exprStmt.Loc()), Right: exprStmt.Value}
		SetStmtLocation(assign, exprStmt.Loc())
		out.Stmts = append(out.Stmts, assign)
		return out
	}
	out.Stmts = append(out.Stmts, last)
	return out
}

func makeTempIdent(name string, localID int, typ typeinfo.Type, loc source.Location) *Ident {
	ident := &Ident{Path: []string{name}, LocalID: localID}
	ident.ExprType = typ
	ident.Location = loc
	return ident
}

func (l *lowerer) hasSpecialMatchArm(s *MatchStmt) bool {
	if s == nil {
		return false
	}
	for _, arm := range s.Arms {
		if arm == nil {
			continue
		}
		if arm.TypePattern != nil {
			return true
		}
		if _, ok := arm.Pattern.(*RangeExpr); ok {
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
		} else if rangePat, ok := arm.Pattern.(*RangeExpr); ok {
			cond = l.rangeMatchCond(matchValue, rangePat, body.Loc())
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

func (l *lowerer) rangeMatchCond(value Expr, pattern *RangeExpr, loc source.Location) Expr {
	if pattern == nil {
		return nil
	}
	boolType := &typeinfo.BuiltinType{Name: "bool"}
	elemType := value.Type()
	if elemType == nil {
		elemType = pattern.Start.Type()
	}
	if elemType == nil {
		elemType = &typeinfo.BuiltinType{Name: typeinfo.DefaultIntTypeName}
	}

	posEndOp := "<"
	negEndOp := ">"
	if pattern.Inclusive {
		posEndOp = "<="
		negEndOp = ">="
	}
	posStart := &BinaryExpr{Left: value, Op: ">=", Right: pattern.Start}
	posStart.ExprType = boolType
	posStart.Location = loc
	posEnd := &BinaryExpr{Left: value, Op: posEndOp, Right: pattern.End}
	posEnd.ExprType = boolType
	posEnd.Location = loc
	posBounds := &BinaryExpr{Left: posStart, Op: "&&", Right: posEnd}
	posBounds.ExprType = boolType
	posBounds.Location = loc

	if pattern.Step == nil {
		return posBounds
	}

	negStart := &BinaryExpr{Left: value, Op: "<=", Right: pattern.Start}
	negStart.ExprType = boolType
	negStart.Location = loc
	negEnd := &BinaryExpr{Left: value, Op: negEndOp, Right: pattern.End}
	negEnd.ExprType = boolType
	negEnd.Location = loc
	negBounds := &BinaryExpr{Left: negStart, Op: "&&", Right: negEnd}
	negBounds.ExprType = boolType
	negBounds.Location = loc

	stepZero := &NumberLit{Value: "0"}
	stepZero.ExprType = elemType
	stepZero.Location = loc
	stepPos := &BinaryExpr{Left: pattern.Step, Op: ">", Right: stepZero}
	stepPos.ExprType = boolType
	stepPos.Location = loc
	stepZeroNeg := &NumberLit{Value: "0"}
	stepZeroNeg.ExprType = elemType
	stepZeroNeg.Location = loc
	stepNeg := &BinaryExpr{Left: pattern.Step, Op: "<", Right: stepZeroNeg}
	stepNeg.ExprType = boolType
	stepNeg.Location = loc

	posCond := &BinaryExpr{Left: stepPos, Op: "&&", Right: posBounds}
	posCond.ExprType = boolType
	posCond.Location = loc
	negCond := &BinaryExpr{Left: stepNeg, Op: "&&", Right: negBounds}
	negCond.ExprType = boolType
	negCond.Location = loc
	signCond := &BinaryExpr{Left: posCond, Op: "||", Right: negCond}
	signCond.ExprType = boolType
	signCond.Location = loc

	delta := &BinaryExpr{Left: value, Op: "-", Right: pattern.Start}
	delta.ExprType = elemType
	delta.Location = loc
	mod := &BinaryExpr{Left: delta, Op: "%", Right: pattern.Step}
	mod.ExprType = elemType
	mod.Location = loc
	zero := &NumberLit{Value: "0"}
	zero.ExprType = elemType
	zero.Location = loc
	aligned := &BinaryExpr{Left: mod, Op: "==", Right: zero}
	aligned.ExprType = boolType
	aligned.Location = loc

	cond := &BinaryExpr{Left: signCond, Op: "&&", Right: aligned}
	cond.ExprType = boolType
	cond.Location = loc
	return cond
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
	case *RangeExpr:
		out := *e
		out.Start = l.rewriteExprIdents(e.Start, fromName, fromID, toName, toID)
		out.End = l.rewriteExprIdents(e.End, fromName, fromID, toName, toID)
		out.Step = l.rewriteExprIdents(e.Step, fromName, fromID, toName, toID)
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
	case *ClosureLit:
		out := *e
		out.Captures = append([]Expr(nil), e.Captures...)
		for i, capture := range out.Captures {
			out.Captures[i] = l.rewriteExprIdents(capture, fromName, fromID, toName, toID)
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
