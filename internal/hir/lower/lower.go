package lower

import (
	"compiler/internal/context_v2"
	"compiler/internal/hir"
	"compiler/internal/hir/consteval"
	"compiler/internal/source"
	"compiler/internal/tokens"
	"compiler/internal/types"
	"fmt"
	"strconv"
)

// Lowerer rewrites source-shaped HIR into a lowered, canonical form.
type Lowerer struct {
	ctx               *context_v2.CompilerContext
	currentReturnType types.SemType
	tempCounter       int
}

// New creates a new HIR lowerer.
func New(ctx *context_v2.CompilerContext) *Lowerer {
	return &Lowerer{ctx: ctx}
}

// LowerModule performs a lowering pass over a HIR module.
// This pass makes optional and result operations explicit for later MIR/codegen stages.
func (l *Lowerer) LowerModule(mod *hir.Module) *hir.Module {
	if mod == nil {
		return nil
	}

	lowered := &hir.Module{
		ImportPath: mod.ImportPath,
		Location:   mod.Location,
	}

	if len(mod.Items) > 0 {
		lowered.Items = make([]hir.Node, 0, len(mod.Items))
		for _, item := range mod.Items {
			lowered.Items = append(lowered.Items, l.lowerNode(item))
		}
	}

	return lowered
}

func (l *Lowerer) lowerNode(node hir.Node) hir.Node {
	if node == nil {
		return nil
	}

	switch n := node.(type) {
	case *hir.FuncDecl:
		return l.lowerFuncDecl(n)
	case *hir.MethodDecl:
		return l.lowerMethodDecl(n)
	case *hir.VarDecl:
		return l.lowerVarDecl(n)
	case *hir.ConstDecl:
		return l.lowerConstDecl(n)
	case *hir.TypeDecl:
		return n
	case *hir.Block:
		return l.lowerBlock(n)
	case *hir.DeclStmt:
		return &hir.DeclStmt{Decl: l.lowerDecl(n.Decl), Location: n.Location}
	case *hir.AssignStmt:
		return l.lowerAssignStmt(n)
	case *hir.ReturnStmt:
		return l.lowerReturnStmt(n)
	case *hir.ExprStmt:
		return &hir.ExprStmt{X: l.lowerExpr(n.X, types.TypeUnknown), Location: n.Location}
	case *hir.IfStmt:
		return l.lowerIfStmt(n)
	case *hir.ForStmt:
		return l.lowerForStmt(n)
	case *hir.WhileStmt:
		return l.lowerWhileStmt(n)
	case *hir.MatchStmt:
		return l.lowerMatchStmt(n)
	case *hir.DeferStmt:
		return l.lowerDeferStmt(n)
	case *hir.ImportStmt, *hir.BreakStmt, *hir.ContinueStmt, *hir.Invalid:
		return n
	default:
		return n
	}
}

func (l *Lowerer) lowerDecl(decl hir.Decl) hir.Decl {
	if decl == nil {
		return nil
	}

	switch d := decl.(type) {
	case *hir.VarDecl:
		return l.lowerVarDecl(d)
	case *hir.ConstDecl:
		return l.lowerConstDecl(d)
	case *hir.TypeDecl:
		return d
	case *hir.FuncDecl:
		return l.lowerFuncDecl(d)
	case *hir.MethodDecl:
		return l.lowerMethodDecl(d)
	default:
		return d
	}
}

func (l *Lowerer) lowerBlock(block *hir.Block) *hir.Block {
	if block == nil {
		return nil
	}

	lowered := &hir.Block{Location: block.Location}
	if len(block.Nodes) > 0 {
		lowered.Nodes = make([]hir.Node, 0, len(block.Nodes))
		for _, node := range block.Nodes {
			lowered.Nodes = append(lowered.Nodes, l.lowerNode(node))
		}
	}
	return lowered
}

func (l *Lowerer) lowerFuncDecl(decl *hir.FuncDecl) *hir.FuncDecl {
	if decl == nil {
		return nil
	}

	prevReturn := l.currentReturnType
	l.currentReturnType = types.TypeVoid
	if decl.Type != nil && decl.Type.Return != nil {
		l.currentReturnType = decl.Type.Return
	}

	body := l.lowerBlock(decl.Body)
	l.currentReturnType = prevReturn

	// Keep function type as-is (with IsVariadic flag) for call sites
	// Parameter symbols were already converted from ...T to []T during type checking
	return &hir.FuncDecl{
		Name:     decl.Name,
		Type:     decl.Type,
		Body:     body,
		Location: decl.Location,
	}
}

func (l *Lowerer) lowerMethodDecl(decl *hir.MethodDecl) *hir.MethodDecl {
	if decl == nil {
		return nil
	}

	prevReturn := l.currentReturnType
	l.currentReturnType = types.TypeVoid
	if decl.Type != nil && decl.Type.Return != nil {
		l.currentReturnType = decl.Type.Return
	}

	body := l.lowerBlock(decl.Body)
	l.currentReturnType = prevReturn

	// Keep method type as-is (with IsVariadic flag) for call sites
	// Parameter symbols were already converted from ...T to []T during type checking
	return &hir.MethodDecl{
		Receiver: decl.Receiver,
		Name:     decl.Name,
		Type:     decl.Type,
		Body:     body,
		Location: decl.Location,
	}
}

func (l *Lowerer) lowerVarDecl(decl *hir.VarDecl) *hir.VarDecl {
	if decl == nil {
		return nil
	}

	lowered := &hir.VarDecl{Location: decl.Location}
	if len(decl.Decls) > 0 {
		lowered.Decls = make([]hir.DeclItem, 0, len(decl.Decls))
		for _, item := range decl.Decls {
			lowered.Decls = append(lowered.Decls, l.lowerDeclItem(item))
		}
	}

	return lowered
}

func (l *Lowerer) lowerConstDecl(decl *hir.ConstDecl) *hir.ConstDecl {
	if decl == nil {
		return nil
	}

	lowered := &hir.ConstDecl{Location: decl.Location}
	if len(decl.Decls) > 0 {
		lowered.Decls = make([]hir.DeclItem, 0, len(decl.Decls))
		for _, item := range decl.Decls {
			lowered.Decls = append(lowered.Decls, l.lowerDeclItem(item))
		}
	}

	return lowered
}

func (l *Lowerer) lowerDeclItem(item hir.DeclItem) hir.DeclItem {
	expected := item.Type
	if expected == nil || expected.Equals(types.TypeUnknown) {
		if item.Name != nil {
			expected = item.Name.Type
		}
	}

	return hir.DeclItem{
		Name:  item.Name,
		Type:  item.Type,
		Value: l.lowerExpr(item.Value, expected),
	}
}

func (l *Lowerer) lowerAssignStmt(stmt *hir.AssignStmt) *hir.AssignStmt {
	if stmt == nil {
		return nil
	}

	lhs := l.lowerExpr(stmt.Lhs, types.TypeUnknown)
	expected := types.TypeUnknown
	if stmt.Op == nil || stmt.Op.Kind == tokens.EQUALS_TOKEN {
		expected = l.exprType(lhs)
		if idx, ok := lhs.(*hir.IndexExpr); ok {
			baseType := types.UnwrapType(l.exprType(idx.X))
			if ref, ok := baseType.(*types.ReferenceType); ok {
				baseType = types.UnwrapType(ref.Inner)
			}
			if mapType, ok := baseType.(*types.MapType); ok && mapType.Value != nil {
				expected = mapType.Value
			}
		}
	}

	return &hir.AssignStmt{
		Lhs:      lhs,
		Rhs:      l.lowerExpr(stmt.Rhs, expected),
		Op:       stmt.Op,
		Location: stmt.Location,
	}
}

func (l *Lowerer) lowerReturnStmt(stmt *hir.ReturnStmt) *hir.ReturnStmt {
	if stmt == nil {
		return nil
	}

	expected := l.currentReturnType
	if expected == nil {
		expected = types.TypeVoid
	}

	if resultType, ok := types.UnwrapType(expected).(*types.ResultType); ok {
		loc := locationFromExpr(stmt.Result)
		if stmt.IsError {
			errValue := l.lowerExpr(stmt.Result, resultType.Err)
			return &hir.ReturnStmt{
				Result:   &hir.ResultErr{Value: errValue, Type: expected, Location: loc},
				IsError:  false,
				Location: stmt.Location,
			}
		}
		okValue := l.lowerExpr(stmt.Result, resultType.Ok)
		return &hir.ReturnStmt{
			Result:   &hir.ResultOk{Value: okValue, Type: expected, Location: loc},
			IsError:  false,
			Location: stmt.Location,
		}
	}

	return &hir.ReturnStmt{
		Result:   l.lowerExpr(stmt.Result, expected),
		IsError:  stmt.IsError,
		Location: stmt.Location,
	}
}

func (l *Lowerer) lowerIfStmt(stmt *hir.IfStmt) *hir.IfStmt {
	if stmt == nil {
		return nil
	}

	return &hir.IfStmt{
		Cond:     l.lowerExpr(stmt.Cond, types.TypeBool),
		Body:     l.lowerBlock(stmt.Body),
		Else:     l.lowerNode(stmt.Else),
		Location: stmt.Location,
	}
}

func (l *Lowerer) lowerForStmt(stmt *hir.ForStmt) hir.Node {
	if stmt == nil {
		return nil
	}

	if rangeExpr := unwrapRangeExpr(stmt.Range); rangeExpr != nil {
		return l.lowerRangeFor(stmt, rangeExpr)
	}

	rangeType := types.UnwrapType(dereferenceType(l.exprType(stmt.Range)))
	if arrayType, ok := rangeType.(*types.ArrayType); ok {
		return l.lowerArrayFor(stmt, arrayType)
	}
	if mapType, ok := rangeType.(*types.MapType); ok {
		return l.lowerMapFor(stmt, mapType)
	}
	// Check if iterating over a string
	if prim, ok := rangeType.(*types.PrimitiveType); ok && prim.GetName() == types.TYPE_STRING {
		return l.lowerStringFor(stmt)
	}

	return &hir.ForStmt{
		Iterator: l.lowerNode(stmt.Iterator),
		Range:    l.lowerExpr(stmt.Range, types.TypeUnknown),
		Body:     l.lowerBlock(stmt.Body),
		Location: stmt.Location,
	}
}

func (l *Lowerer) lowerWhileStmt(stmt *hir.WhileStmt) *hir.WhileStmt {
	if stmt == nil {
		return nil
	}

	return &hir.WhileStmt{
		Cond:     l.lowerExpr(stmt.Cond, types.TypeBool),
		Body:     l.lowerBlock(stmt.Body),
		Location: stmt.Location,
	}
}

func (l *Lowerer) lowerRangeFor(stmt *hir.ForStmt, rng *hir.RangeExpr) hir.Node {
	loc := stmt.Location
	elemType := l.rangeElementType(stmt.Range, rng)
	if elemType == nil {
		elemType = types.TypeUnknown
	}

	startExpr := l.lowerExpr(rng.Start, elemType)
	endExpr := l.lowerExpr(rng.End, elemType)
	var incrExpr hir.Expr
	if rng.Incr != nil {
		incrExpr = l.lowerExpr(rng.Incr, elemType)
	} else {
		incrExpr = defaultIncrementLiteral(elemType, loc)
	}

	startIdent := l.newTempIdent("__range_start_", elemType, loc)
	endIdent := l.newTempIdent("__range_end_", elemType, loc)
	incrIdent := l.newTempIdent("__range_incr_", elemType, loc)

	prelude := []hir.Node{
		newVarDecl(startIdent, elemType, startExpr, loc),
		newVarDecl(endIdent, elemType, endExpr, loc),
		newVarDecl(incrIdent, elemType, incrExpr, loc),
	}
	if rangeStepFloatMismatch(l.exprType(rng.Start), l.exprType(rng.End), l.exprType(rng.Incr)) {
		prelude = append(prelude, l.panicStmt("invalid range", loc))
	}
	prelude = append(prelude, l.rangeInvalidCheck(startIdent, endIdent, incrIdent, elemType, loc))

	items, iterLoc, isDecl := iteratorItems(stmt.Iterator)
	if len(items) == 0 {
		return &hir.ForStmt{
			Iterator: l.lowerNode(stmt.Iterator),
			Range:    l.lowerExpr(stmt.Range, types.TypeUnknown),
			Body:     l.lowerBlock(stmt.Body),
			Location: stmt.Location,
		}
	}

	firstIdent := l.newTempIdent("__for_first_", types.TypeBool, loc)
	prelude = append(prelude, newVarDecl(firstIdent, types.TypeBool, boolLiteral(true, loc), loc))

	sign, signKnown := incrementSign(rng.Incr)
	condToken := tokens.LESS_TOKEN
	negCondToken := tokens.GREATER_TOKEN
	if rng.Inclusive {
		condToken = tokens.LESS_EQUAL_TOKEN
		negCondToken = tokens.GREATER_EQUAL_TOKEN
	}

	loweredBody := l.lowerBlock(stmt.Body)
	if len(items) == 1 {
		item := items[0]
		valueIdent := item.Name
		valueType := declItemType(item)
		if valueType == nil || valueType.Equals(types.TypeUnknown) {
			valueType = elemType
		}

		valueNeedsDecl := isDecl
		if isDiscardIdent(valueIdent) {
			valueIdent = l.newTempIdent("__range_val_", valueType, loc)
			valueNeedsDecl = true
		}

		if valueNeedsDecl {
			prelude = append(prelude, newVarDecl(valueIdent, valueType, startIdent, iterLoc))
		} else {
			prelude = append(prelude, newAssign(valueIdent, startIdent, tokens.EQUALS_TOKEN, iterLoc))
		}

		incrAssign := newAssign(valueIdent, incrIdent, tokens.PLUS_EQUALS_TOKEN, loc)
		condExpr := rangeCondExpr(valueIdent, endIdent, incrIdent, elemType, condToken, negCondToken, sign, signKnown, loc)
		loopNodes := l.buildLoopBody(firstIdent, incrAssign, condExpr, nil, loweredBody, loc)
		return l.wrapLoop(prelude, loopNodes, loc)
	}

	indexItem := items[0]
	valueItem := items[1]

	indexType := declItemType(indexItem)
	if indexType == nil || indexType.Equals(types.TypeUnknown) {
		indexType = elemType
	}

	indexIdent := indexItem.Name
	indexNeedsDecl := isDecl
	if isDiscardIdent(indexIdent) {
		indexIdent = l.newTempIdent("__range_idx_", indexType, loc)
		indexNeedsDecl = true
	}

	zero := zeroLiteral(indexType, loc)
	if indexNeedsDecl {
		prelude = append(prelude, newVarDecl(indexIdent, indexType, zero, iterLoc))
	} else {
		prelude = append(prelude, newAssign(indexIdent, zero, tokens.EQUALS_TOKEN, iterLoc))
	}

	valueIdent := valueItem.Name
	if isDiscardIdent(valueIdent) {
		valueIdent = nil
	} else if isDecl {
		valueType := declItemType(valueItem)
		if valueType == nil || valueType.Equals(types.TypeUnknown) {
			valueType = elemType
		}
		prelude = append(prelude, newVarDecl(valueIdent, valueType, nil, iterLoc))
	}

	limitIdent := l.newTempIdent("__range_limit_", elemType, loc)
	limitExpr := newBinary(endIdent, startIdent, tokens.MINUS_TOKEN, elemType, loc)
	prelude = append(prelude, newVarDecl(limitIdent, elemType, limitExpr, loc))

	incrAssign := newAssign(indexIdent, incrIdent, tokens.PLUS_EQUALS_TOKEN, loc)
	condExpr := rangeCondExpr(indexIdent, limitIdent, incrIdent, elemType, condToken, negCondToken, sign, signKnown, loc)
	var valueAssign hir.Node
	if valueIdent != nil {
		valueExpr := newBinary(startIdent, indexIdent, tokens.PLUS_TOKEN, elemType, loc)
		valueAssign = newAssign(valueIdent, valueExpr, tokens.EQUALS_TOKEN, loc)
	}

	loopNodes := l.buildLoopBody(firstIdent, incrAssign, condExpr, valueAssign, loweredBody, loc)
	return l.wrapLoop(prelude, loopNodes, loc)
}

func (l *Lowerer) lowerArrayFor(stmt *hir.ForStmt, arrayType *types.ArrayType) hir.Node {
	loc := stmt.Location
	arrayExpr := l.lowerExpr(stmt.Range, types.TypeUnknown)

	arraySemType := l.exprType(stmt.Range)
	if arraySemType == nil {
		arraySemType = types.TypeUnknown
	}

	prelude := []hir.Node{}
	rangeValue := arrayExpr
	if !isSimpleValue(arrayExpr) {
		arrayIdent := l.newTempIdent("__for_arr_", arraySemType, loc)
		prelude = append(prelude, newVarDecl(arrayIdent, arraySemType, arrayExpr, loc))
		rangeValue = arrayIdent
	}

	lenIdent := l.newTempIdent("__for_len_", types.TypeI32, loc)
	lenExpr := &hir.ArrayLenExpr{
		X:        rangeValue,
		Type:     types.TypeI32,
		Location: loc,
	}
	prelude = append(prelude, newVarDecl(lenIdent, types.TypeI32, lenExpr, loc))

	items, iterLoc, isDecl := iteratorItems(stmt.Iterator)
	if len(items) == 0 {
		return &hir.ForStmt{
			Iterator: l.lowerNode(stmt.Iterator),
			Range:    l.lowerExpr(stmt.Range, types.TypeUnknown),
			Body:     l.lowerBlock(stmt.Body),
			Location: stmt.Location,
		}
	}

	elemType := arrayType.Element
	if elemType == nil {
		elemType = types.TypeUnknown
	}

	var indexIdent *hir.Ident
	var valueIdent *hir.Ident

	if len(items) == 1 {
		valueItem := items[0]
		if !isDiscardIdent(valueItem.Name) {
			valueIdent = valueItem.Name
			if isDecl {
				valueType := declItemType(valueItem)
				if valueType == nil || valueType.Equals(types.TypeUnknown) {
					valueType = elemType
				}
				prelude = append(prelude, newVarDecl(valueIdent, valueType, nil, iterLoc))
			}
		}

		indexIdent = l.newTempIdent("__array_idx_", types.TypeI32, loc)
		prelude = append(prelude, newVarDecl(indexIdent, types.TypeI32, intLiteral(0, types.TypeI32, loc), iterLoc))
	} else {
		indexItem := items[0]
		valueItem := items[1]

		indexIdent = indexItem.Name
		indexNeedsDecl := isDecl
		if isDiscardIdent(indexIdent) {
			indexIdent = l.newTempIdent("__array_idx_", types.TypeI32, loc)
			indexNeedsDecl = true
		}
		zero := intLiteral(0, types.TypeI32, loc)
		if indexNeedsDecl {
			prelude = append(prelude, newVarDecl(indexIdent, types.TypeI32, zero, iterLoc))
		} else {
			prelude = append(prelude, newAssign(indexIdent, zero, tokens.EQUALS_TOKEN, iterLoc))
		}

		if !isDiscardIdent(valueItem.Name) {
			valueIdent = valueItem.Name
			if isDecl {
				valueType := declItemType(valueItem)
				if valueType == nil || valueType.Equals(types.TypeUnknown) {
					valueType = elemType
				}
				prelude = append(prelude, newVarDecl(valueIdent, valueType, nil, iterLoc))
			}
		}
	}

	firstIdent := l.newTempIdent("__for_first_", types.TypeBool, loc)
	prelude = append(prelude, newVarDecl(firstIdent, types.TypeBool, boolLiteral(true, loc), loc))

	incrAssign := newAssign(indexIdent, intLiteral(1, types.TypeI32, loc), tokens.PLUS_EQUALS_TOKEN, loc)
	condExpr := newBinary(indexIdent, lenIdent, tokens.LESS_TOKEN, types.TypeBool, loc)

	var valueAssign hir.Node
	if valueIdent != nil {
		indexExpr := &hir.IndexExpr{
			X:        rangeValue,
			Index:    indexIdent,
			Type:     elemType,
			Location: loc,
		}
		valueAssign = newAssign(valueIdent, indexExpr, tokens.EQUALS_TOKEN, loc)
	}

	loopNodes := l.buildLoopBody(firstIdent, incrAssign, condExpr, valueAssign, l.lowerBlock(stmt.Body), loc)
	return l.wrapLoop(prelude, loopNodes, loc)
}

func (l *Lowerer) lowerMapFor(stmt *hir.ForStmt, mapType *types.MapType) hir.Node {
	loc := stmt.Location
	mapExpr := l.lowerExpr(stmt.Range, types.TypeUnknown)

	mapSemType := l.exprType(stmt.Range)
	if mapSemType == nil {
		mapSemType = types.TypeUnknown
	}

	prelude := []hir.Node{}
	rangeValue := mapExpr
	if !isSimpleValue(mapExpr) {
		mapIdent := l.newTempIdent("__for_map_", mapSemType, loc)
		prelude = append(prelude, newVarDecl(mapIdent, mapSemType, mapExpr, loc))
		rangeValue = mapIdent
	}

	iterIdent := l.newTempIdent("__map_iter_", types.NewReference(types.TypeVoid), loc)
	iterInit := &hir.MapIterInitExpr{
		Map:      rangeValue,
		Type:     types.TypeUnknown,
		Location: loc,
	}
	prelude = append(prelude, newVarDecl(iterIdent, iterIdent.Type, iterInit, loc))

	keyType := types.TypeUnknown
	valType := types.TypeUnknown
	if mapType != nil {
		if mapType.Key != nil {
			keyType = mapType.Key
		}
		if mapType.Value != nil {
			valType = mapType.Value
		}
	}

	keyTemp := l.newTempIdent("__map_key_", keyType, loc)
	valTemp := l.newTempIdent("__map_val_", valType, loc)
	prelude = append(prelude, newVarDecl(keyTemp, keyType, nil, loc))
	prelude = append(prelude, newVarDecl(valTemp, valType, nil, loc))

	items, iterLoc, isDecl := iteratorItems(stmt.Iterator)
	if len(items) == 0 {
		return &hir.ForStmt{
			Iterator: l.lowerNode(stmt.Iterator),
			Range:    l.lowerExpr(stmt.Range, types.TypeUnknown),
			Body:     l.lowerBlock(stmt.Body),
			Location: stmt.Location,
		}
	}

	var keyIdent *hir.Ident
	var valueIdent *hir.Ident

	keyItem := items[0]
	if !isDiscardIdent(keyItem.Name) {
		keyIdent = keyItem.Name
		if isDecl {
			keyDeclType := declItemType(keyItem)
			if keyDeclType == nil || keyDeclType.Equals(types.TypeUnknown) {
				keyDeclType = keyType
			}
			prelude = append(prelude, newVarDecl(keyIdent, keyDeclType, nil, iterLoc))
		}
	}

	if len(items) > 1 {
		valueItem := items[1]
		if !isDiscardIdent(valueItem.Name) {
			valueIdent = valueItem.Name
			if isDecl {
				valDeclType := declItemType(valueItem)
				if valDeclType == nil || valDeclType.Equals(types.TypeUnknown) {
					valDeclType = valType
				}
				prelude = append(prelude, newVarDecl(valueIdent, valDeclType, nil, iterLoc))
			}
		}
	}

	cond := &hir.MapIterNextExpr{
		Map:      rangeValue,
		Iter:     iterIdent,
		Key:      keyTemp,
		Value:    valTemp,
		Type:     types.TypeBool,
		Location: loc,
	}

	notCond := &hir.UnaryExpr{
		Op:       token(tokens.NOT_TOKEN),
		X:        cond,
		Type:     types.TypeBool,
		Location: loc,
	}

	loopNodes := []hir.Node{
		&hir.IfStmt{
			Cond: notCond,
			Body: &hir.Block{
				Nodes:    []hir.Node{&hir.BreakStmt{Location: loc}},
				Location: loc,
			},
			Location: loc,
		},
	}

	if keyIdent != nil {
		loopNodes = append(loopNodes, newAssign(keyIdent, keyTemp, tokens.EQUALS_TOKEN, iterLoc))
	}
	if valueIdent != nil {
		loopNodes = append(loopNodes, newAssign(valueIdent, valTemp, tokens.EQUALS_TOKEN, iterLoc))
	}

	body := l.lowerBlock(stmt.Body)
	if body != nil && len(body.Nodes) > 0 {
		loopNodes = append(loopNodes, body.Nodes...)
	}

	whileStmt := &hir.WhileStmt{
		Cond:     boolLiteral(true, loc),
		Body:     &hir.Block{Nodes: loopNodes, Location: loc},
		Location: loc,
	}

	prelude = append(prelude, whileStmt)
	return &hir.Block{Nodes: prelude, Location: loc}
}

func (l *Lowerer) lowerStringFor(stmt *hir.ForStmt) hir.Node {
	loc := stmt.Location
	stringExpr := l.lowerExpr(stmt.Range, types.TypeUnknown)

	stringSemType := l.exprType(stmt.Range)
	if stringSemType == nil {
		stringSemType = types.TypeString
	}

	prelude := []hir.Node{}
	rangeValue := stringExpr
	if !isSimpleValue(stringExpr) {
		stringIdent := l.newTempIdent("__for_str_", stringSemType, loc)
		prelude = append(prelude, newVarDecl(stringIdent, stringSemType, stringExpr, loc))
		rangeValue = stringIdent
	}

	// Get string length
	lenIdent := l.newTempIdent("__for_len_", types.TypeI32, loc)
	lenExpr := &hir.StringLenExpr{
		X:        rangeValue,
		Type:     types.TypeI32,
		Location: loc,
	}
	prelude = append(prelude, newVarDecl(lenIdent, types.TypeI32, lenExpr, loc))

	items, iterLoc, isDecl := iteratorItems(stmt.Iterator)
	if len(items) == 0 {
		return &hir.ForStmt{
			Iterator: l.lowerNode(stmt.Iterator),
			Range:    l.lowerExpr(stmt.Range, types.TypeUnknown),
			Body:     l.lowerBlock(stmt.Body),
			Location: stmt.Location,
		}
	}

	elemType := types.TypeByte // Strings iterate over bytes

	var indexIdent *hir.Ident
	var valueIdent *hir.Ident

	if len(items) == 1 {
		// Single iterator: value only
		valueItem := items[0]
		if !isDiscardIdent(valueItem.Name) {
			valueIdent = valueItem.Name
			if isDecl {
				valueType := declItemType(valueItem)
				if valueType == nil || valueType.Equals(types.TypeUnknown) {
					valueType = elemType
				}
				prelude = append(prelude, newVarDecl(valueIdent, valueType, nil, iterLoc))
			}
		}

		indexIdent = l.newTempIdent("__str_idx_", types.TypeI32, loc)
		prelude = append(prelude, newVarDecl(indexIdent, types.TypeI32, intLiteral(0, types.TypeI32, loc), iterLoc))
	} else {
		// Dual iterator: index and value
		indexItem := items[0]
		valueItem := items[1]

		indexIdent = indexItem.Name
		indexNeedsDecl := isDecl
		if isDiscardIdent(indexIdent) {
			indexIdent = l.newTempIdent("__str_idx_", types.TypeI32, loc)
			indexNeedsDecl = true
		}
		zero := intLiteral(0, types.TypeI32, loc)
		if indexNeedsDecl {
			prelude = append(prelude, newVarDecl(indexIdent, types.TypeI32, zero, iterLoc))
		} else {
			prelude = append(prelude, newAssign(indexIdent, zero, tokens.EQUALS_TOKEN, iterLoc))
		}

		if !isDiscardIdent(valueItem.Name) {
			valueIdent = valueItem.Name
			if isDecl {
				valueType := declItemType(valueItem)
				if valueType == nil || valueType.Equals(types.TypeUnknown) {
					valueType = elemType
				}
				prelude = append(prelude, newVarDecl(valueIdent, valueType, nil, iterLoc))
			}
		}
	}

	firstIdent := l.newTempIdent("__for_first_", types.TypeBool, loc)
	prelude = append(prelude, newVarDecl(firstIdent, types.TypeBool, boolLiteral(true, loc), loc))

	incrAssign := newAssign(indexIdent, intLiteral(1, types.TypeI32, loc), tokens.PLUS_EQUALS_TOKEN, loc)
	condExpr := newBinary(indexIdent, lenIdent, tokens.LESS_TOKEN, types.TypeBool, loc)

	var valueAssign hir.Node
	if valueIdent != nil {
		// String indexing returns a byte
		indexExpr := &hir.IndexExpr{
			X:        rangeValue,
			Index:    indexIdent,
			Type:     elemType,
			Location: loc,
		}
		valueAssign = newAssign(valueIdent, indexExpr, tokens.EQUALS_TOKEN, loc)
	}

	loweredBody := l.lowerBlock(stmt.Body)
	loopNodes := l.buildLoopBody(firstIdent, incrAssign, condExpr, valueAssign, loweredBody, loc)
	return l.wrapLoop(prelude, loopNodes, loc)
}

func (l *Lowerer) lowerMatchStmt(stmt *hir.MatchStmt) *hir.MatchStmt {
	if stmt == nil {
		return nil
	}

	lowered := &hir.MatchStmt{
		Expr:     l.lowerExpr(stmt.Expr, types.TypeUnknown),
		Location: stmt.Location,
	}

	if len(stmt.Cases) > 0 {
		lowered.Cases = make([]hir.CaseClause, 0, len(stmt.Cases))
		for _, clause := range stmt.Cases {
			lowered.Cases = append(lowered.Cases, hir.CaseClause{
				Pattern:  l.lowerExpr(clause.Pattern, types.TypeUnknown),
				Body:     l.lowerBlock(clause.Body),
				Location: clause.Location,
			})
		}
	}

	return lowered
}

func (l *Lowerer) lowerDeferStmt(stmt *hir.DeferStmt) *hir.DeferStmt {
	if stmt == nil {
		return nil
	}

	call, ok := l.lowerExpr(stmt.Call, types.TypeUnknown).(*hir.CallExpr)
	if !ok {
		// Should not happen if parser validated correctly
		return nil
	}

	return &hir.DeferStmt{
		Call:     call,
		Catch:    l.lowerCatchClause(stmt.Catch, types.TypeUnknown),
		Location: stmt.Location,
	}
}

func (l *Lowerer) lowerExpr(expr hir.Expr, expected types.SemType) hir.Expr {
	if expr == nil {
		return nil
	}

	var lowered hir.Expr
	switch e := expr.(type) {
	case *hir.Ident:
		lowered = e
	case *hir.Literal:
		if e.Type != nil && types.IsUntyped(e.Type) {
			resolved := types.ResolveUntypedType(e.Type, expected)
			if resolved != nil && !resolved.Equals(e.Type) {
				lowered = &hir.Literal{
					Kind:     e.Kind,
					Value:    e.Value,
					Type:     resolved,
					Location: e.Location,
				}
				break
			}
		}
		lowered = e
	case *hir.OptionalNone:
		lowered = e
	case *hir.OptionalSome:
		innerExpected := l.optionalInnerType(e.Type)
		lowered = &hir.OptionalSome{
			Value:    l.lowerExpr(e.Value, innerExpected),
			Type:     e.Type,
			Location: e.Location,
		}
	case *hir.OptionalIsSome:
		lowered = &hir.OptionalIsSome{
			Value:    l.lowerExpr(e.Value, types.TypeUnknown),
			Type:     e.Type,
			Location: e.Location,
		}
	case *hir.OptionalIsNone:
		lowered = &hir.OptionalIsNone{
			Value:    l.lowerExpr(e.Value, types.TypeUnknown),
			Type:     e.Type,
			Location: e.Location,
		}
	case *hir.OptionalUnwrap:
		lowered = &hir.OptionalUnwrap{
			Value:    l.lowerExpr(e.Value, types.TypeUnknown),
			Default:  l.lowerExpr(e.Default, e.Type),
			Type:     e.Type,
			Location: e.Location,
		}
	case *hir.ResultOk:
		innerExpected := types.TypeUnknown
		if resultType, ok := types.UnwrapType(e.Type).(*types.ResultType); ok {
			innerExpected = resultType.Ok
		}
		lowered = &hir.ResultOk{
			Value:    l.lowerExpr(e.Value, innerExpected),
			Type:     e.Type,
			Location: e.Location,
		}
	case *hir.ResultErr:
		innerExpected := types.TypeUnknown
		if resultType, ok := types.UnwrapType(e.Type).(*types.ResultType); ok {
			innerExpected = resultType.Err
		}
		lowered = &hir.ResultErr{
			Value:    l.lowerExpr(e.Value, innerExpected),
			Type:     e.Type,
			Location: e.Location,
		}
	case *hir.ResultUnwrap:
		lowered = &hir.ResultUnwrap{
			Value:    l.lowerExpr(e.Value, types.TypeUnknown),
			Catch:    l.lowerCatchClause(e.Catch, e.Type),
			Type:     e.Type,
			Location: e.Location,
		}
	case *hir.BinaryExpr:
		lowered = l.lowerBinaryExpr(e)
	case *hir.UnaryExpr:
		lowered = &hir.UnaryExpr{Op: e.Op, X: l.lowerExpr(e.X, types.TypeUnknown), Type: e.Type, Location: e.Location}
	case *hir.DerefExpr:
		lowered = &hir.DerefExpr{X: l.lowerExpr(e.X, types.TypeUnknown), Type: e.Type, Location: e.Location}
	case *hir.PrefixExpr:
		lowered = &hir.PrefixExpr{Op: e.Op, X: l.lowerExpr(e.X, types.TypeUnknown), Type: e.Type, Location: e.Location}
	case *hir.PostfixExpr:
		lowered = &hir.PostfixExpr{Op: e.Op, X: l.lowerExpr(e.X, types.TypeUnknown), Type: e.Type, Location: e.Location}
	case *hir.CallExpr:
		lowered = l.lowerCallExpr(e)
	case *hir.SelectorExpr:
		lowered = &hir.SelectorExpr{X: l.lowerExpr(e.X, types.TypeUnknown), Field: e.Field, Type: e.Type, Location: e.Location}
	case *hir.ScopeResolutionExpr:
		lowered = &hir.ScopeResolutionExpr{X: l.lowerExpr(e.X, types.TypeUnknown), Selector: e.Selector, Type: e.Type, Location: e.Location}
	case *hir.RangeExpr:
		lowered = &hir.RangeExpr{
			Start:     l.lowerExpr(e.Start, types.TypeUnknown),
			End:       l.lowerExpr(e.End, types.TypeUnknown),
			Incr:      l.lowerExpr(e.Incr, types.TypeUnknown),
			Inclusive: e.Inclusive,
			Type:      e.Type,
			Location:  e.Location,
		}
	case *hir.ArrayLenExpr:
		lowered = &hir.ArrayLenExpr{
			X:        l.lowerExpr(e.X, types.TypeUnknown),
			Type:     e.Type,
			Location: e.Location,
		}
	case *hir.StringLenExpr:
		lowered = &hir.StringLenExpr{
			X:        l.lowerExpr(e.X, types.TypeUnknown),
			Type:     e.Type,
			Location: e.Location,
		}
	case *hir.MapIterInitExpr:
		lowered = &hir.MapIterInitExpr{
			Map:      l.lowerExpr(e.Map, types.TypeUnknown),
			Type:     e.Type,
			Location: e.Location,
		}
	case *hir.MapIterNextExpr:
		lowered = &hir.MapIterNextExpr{
			Map:      l.lowerExpr(e.Map, types.TypeUnknown),
			Iter:     l.lowerExpr(e.Iter, types.TypeUnknown),
			Key:      e.Key,
			Value:    e.Value,
			Type:     e.Type,
			Location: e.Location,
		}
	case *hir.IndexExpr:
		lowered = &hir.IndexExpr{X: l.lowerExpr(e.X, types.TypeUnknown), Index: l.lowerExpr(e.Index, types.TypeUnknown), Type: e.Type, Location: e.Location}
	case *hir.CastExpr:
		lowered = &hir.CastExpr{X: l.lowerExpr(e.X, types.TypeUnknown), TargetType: e.TargetType, Type: e.Type, Location: e.Location}
	case *hir.CoalescingExpr:
		lowered = l.lowerCoalescingExpr(e)
	case *hir.ForkExpr:
		lowered = &hir.ForkExpr{Call: l.lowerExpr(e.Call, types.TypeUnknown), Type: e.Type, Location: e.Location}
	case *hir.ParenExpr:
		lowered = &hir.ParenExpr{X: l.lowerExpr(e.X, expected), Type: e.Type, Location: e.Location}
	case *hir.CompositeLit:
		lowered = l.lowerCompositeLit(e)
	case *hir.KeyValueExpr:
		lowered = &hir.KeyValueExpr{
			Key:      l.lowerExpr(e.Key, types.TypeUnknown),
			Value:    l.lowerExpr(e.Value, types.TypeUnknown),
			Location: e.Location,
		}
	case *hir.FuncLit:
		lowered = l.lowerFuncLit(e)
	case *hir.Invalid:
		lowered = e
	default:
		lowered = e
	}

	return l.wrapOptional(lowered, expected)
}

func (l *Lowerer) lowerBinaryExpr(expr *hir.BinaryExpr) hir.Expr {
	left := l.lowerExpr(expr.X, types.TypeUnknown)
	right := l.lowerExpr(expr.Y, types.TypeUnknown)

	if expr.Op.Kind == tokens.DOUBLE_EQUAL_TOKEN || expr.Op.Kind == tokens.NOT_EQUAL_TOKEN {
		leftNone := isNoneExpr(left)
		rightNone := isNoneExpr(right)
		if leftNone != rightNone {
			optionalExpr := left
			if leftNone {
				optionalExpr = right
			}
			if isOptionalType(l.exprType(optionalExpr)) {
				if expr.Op.Kind == tokens.DOUBLE_EQUAL_TOKEN {
					return &hir.OptionalIsNone{
						Value:    optionalExpr,
						Type:     expr.Type,
						Location: expr.Location,
					}
				}
				return &hir.OptionalIsSome{
					Value:    optionalExpr,
					Type:     expr.Type,
					Location: expr.Location,
				}
			}
		}
	}

	return &hir.BinaryExpr{X: left, Op: expr.Op, Y: right, Type: expr.Type, TargetType: expr.TargetType, Location: expr.Location}
}

func (l *Lowerer) lowerCoalescingExpr(expr *hir.CoalescingExpr) hir.Expr {
	cond := l.lowerExpr(expr.Cond, types.TypeUnknown)
	def := l.lowerExpr(expr.Default, expr.Type)

	if isOptionalType(l.exprType(expr.Cond)) {
		return &hir.OptionalUnwrap{
			Value:    cond,
			Default:  def,
			Type:     expr.Type,
			Location: expr.Location,
		}
	}

	return &hir.CoalescingExpr{
		Cond:     cond,
		Default:  def,
		Type:     expr.Type,
		Location: expr.Location,
	}
}

func (l *Lowerer) lowerCallExpr(expr *hir.CallExpr) hir.Expr {
	if expr == nil {
		return nil
	}

	funType := l.getFunctionType(expr.Fun)

	// Check if this is a variadic call - bundle args into array literal
	var args []hir.Expr
	if funType != nil && len(funType.Params) > 0 {
		lastParamIdx := len(funType.Params) - 1
		lastParam := funType.Params[lastParamIdx]

		if lastParam.IsVariadic {
			// Process regular parameters
			args = make([]hir.Expr, 0, len(funType.Params))
			for i := 0; i < lastParamIdx && i < len(expr.Args); i++ {
				expected := funType.Params[i].Type
				args = append(args, l.lowerExpr(expr.Args[i], expected))
			}

			// Bundle variadic args into array literal
			variadicArgs := expr.Args[lastParamIdx:]
			elemType := lastParam.Type

			// Create array literal elements
			litElts := make([]hir.Expr, len(variadicArgs))
			for i, arg := range variadicArgs {
				litElts[i] = l.lowerExpr(arg, elemType)
			}

			// Create []T array literal
			arrayType := types.NewArray(elemType, -1)
			sliceLit := &hir.CompositeLit{
				Type:     arrayType,
				Elts:     litElts,
				Location: expr.Location,
			}
			args = append(args, sliceLit)
		} else {
			// Non-variadic
			args = make([]hir.Expr, 0, len(expr.Args))
			for i, arg := range expr.Args {
				expected := l.expectedArgType(funType, i)
				args = append(args, l.lowerExpr(arg, expected))
			}
		}
	} else {
		// No function type info
		args = make([]hir.Expr, 0, len(expr.Args))
		for i, arg := range expr.Args {
			expected := l.expectedArgType(funType, i)
			args = append(args, l.lowerExpr(arg, expected))
		}
	}

	callType := expr.Type
	if funType != nil && funType.Return != nil {
		callType = funType.Return
	}
	call := &hir.CallExpr{
		Fun:      l.lowerExpr(expr.Fun, types.TypeUnknown),
		Args:     args,
		Catch:    nil,
		Type:     callType,
		Location: expr.Location,
	}

	if expr.Catch == nil {
		return call
	}

	fallbackExpected := expr.Type
	if funType != nil {
		if resultType, ok := types.UnwrapType(funType.Return).(*types.ResultType); ok {
			fallbackExpected = resultType.Ok
		}
	}

	return &hir.ResultUnwrap{
		Value:    call,
		Catch:    l.lowerCatchClause(expr.Catch, fallbackExpected),
		Type:     expr.Type,
		Location: expr.Location,
	}
}

func (l *Lowerer) lowerCatchClause(clause *hir.CatchClause, fallbackExpected types.SemType) *hir.CatchClause {
	if clause == nil {
		return nil
	}

	return &hir.CatchClause{
		ErrIdent: clause.ErrIdent,
		Handler:  l.lowerBlock(clause.Handler),
		Fallback: l.lowerExpr(clause.Fallback, fallbackExpected),
		Location: clause.Location,
	}
}

func (l *Lowerer) lowerCompositeLit(lit *hir.CompositeLit) *hir.CompositeLit {
	if lit == nil {
		return nil
	}

	lowered := &hir.CompositeLit{Type: lit.Type, Location: lit.Location}
	if len(lit.Elts) == 0 {
		return lowered
	}

	unwrapped := types.UnwrapType(lit.Type)
	switch t := unwrapped.(type) {
	case *types.StructType:
		fieldTypes := make(map[string]types.SemType, len(t.Fields))
		for _, field := range t.Fields {
			fieldTypes[field.Name] = field.Type
		}

		lowered.Elts = make([]hir.Expr, 0, len(lit.Elts))
		for _, elt := range lit.Elts {
			if kv, ok := elt.(*hir.KeyValueExpr); ok {
				fieldType := fieldTypes[fieldNameFromKey(kv.Key)]
				lowered.Elts = append(lowered.Elts, l.lowerKeyValueExpr(kv, types.TypeUnknown, fieldType))
				continue
			}
			lowered.Elts = append(lowered.Elts, l.lowerExpr(elt, types.TypeUnknown))
		}
	case *types.MapType:
		lowered.Elts = make([]hir.Expr, 0, len(lit.Elts))
		for _, elt := range lit.Elts {
			if kv, ok := elt.(*hir.KeyValueExpr); ok {
				lowered.Elts = append(lowered.Elts, l.lowerKeyValueExpr(kv, t.Key, t.Value))
				continue
			}
			lowered.Elts = append(lowered.Elts, l.lowerExpr(elt, types.TypeUnknown))
		}
	case *types.ArrayType:
		lowered.Elts = make([]hir.Expr, 0, len(lit.Elts))
		for _, elt := range lit.Elts {
			lowered.Elts = append(lowered.Elts, l.lowerExpr(elt, t.Element))
		}
	default:
		lowered.Elts = make([]hir.Expr, 0, len(lit.Elts))
		for _, elt := range lit.Elts {
			lowered.Elts = append(lowered.Elts, l.lowerExpr(elt, types.TypeUnknown))
		}
	}

	return lowered
}

func (l *Lowerer) lowerKeyValueExpr(kv *hir.KeyValueExpr, keyExpected, valueExpected types.SemType) *hir.KeyValueExpr {
	if kv == nil {
		return nil
	}

	return &hir.KeyValueExpr{
		Key:      l.lowerExpr(kv.Key, keyExpected),
		Value:    l.lowerExpr(kv.Value, valueExpected),
		Location: kv.Location,
	}
}

func (l *Lowerer) lowerFuncLit(lit *hir.FuncLit) *hir.FuncLit {
	if lit == nil {
		return nil
	}

	prevReturn := l.currentReturnType
	l.currentReturnType = types.TypeVoid
	if fnType, ok := types.UnwrapType(lit.Type).(*types.FunctionType); ok {
		l.currentReturnType = fnType.Return
	}

	body := l.lowerBlock(lit.Body)
	l.currentReturnType = prevReturn

	return &hir.FuncLit{
		ID:       lit.ID,
		Type:     lit.Type,
		Captures: lit.Captures,
		Body:     body,
		Location: lit.Location,
	}
}

func (l *Lowerer) wrapOptional(expr hir.Expr, expected types.SemType) hir.Expr {
	if expr == nil {
		return nil
	}

	expectedOpt := l.optionalType(expected)
	if expectedOpt == nil {
		return expr
	}

	if isNoneExpr(expr) {
		return &hir.OptionalNone{Type: expected, Location: locationFromExpr(expr)}
	}

	if l.isOptionalProducer(expr) {
		return expr
	}

	actualType := l.exprType(expr)
	if actualType == nil || actualType.Equals(types.TypeUnknown) {
		return expr
	}

	if expectedOpt.Inner == nil {
		return expr
	}

	if isOptionalType(actualType) || actualType.Equals(expectedOpt.Inner) {
		value := l.forceExprType(expr, expectedOpt.Inner)
		return &hir.OptionalSome{Value: value, Type: expected, Location: locationFromExpr(expr)}
	}

	return expr
}

func (l *Lowerer) isOptionalProducer(expr hir.Expr) bool {
	if expr == nil {
		return false
	}

	switch e := expr.(type) {
	case *hir.OptionalSome, *hir.OptionalNone:
		return true
	case *hir.Ident:
		if e.Symbol != nil && e.Symbol.Type != nil {
			return isOptionalType(e.Symbol.Type)
		}
		return false
	case *hir.CallExpr:
		if fn := l.getFunctionType(e.Fun); fn != nil {
			return isOptionalType(fn.Return)
		}
		return false
	case *hir.ResultUnwrap:
		return isOptionalType(l.exprType(e))
	case *hir.IndexExpr:
		baseType := types.UnwrapType(l.exprType(e.X))
		_, ok := baseType.(*types.MapType)
		return ok
	case *hir.SelectorExpr:
		return isOptionalType(l.selectorFieldType(e))
	case *hir.ScopeResolutionExpr:
		return isOptionalType(l.exprType(e))
	case *hir.CastExpr:
		return isOptionalType(e.TargetType)
	case *hir.ParenExpr:
		return l.isOptionalProducer(e.X)
	default:
		return false
	}
}

func (l *Lowerer) forceExprType(expr hir.Expr, target types.SemType) hir.Expr {
	if expr == nil || target == nil {
		return expr
	}

	switch e := expr.(type) {
	case *hir.Literal:
		return &hir.Literal{Kind: e.Kind, Value: e.Value, Type: target, Location: e.Location}
	case *hir.BinaryExpr:
		return &hir.BinaryExpr{X: e.X, Op: e.Op, Y: e.Y, Type: target, TargetType: e.TargetType, Location: e.Location}
	case *hir.UnaryExpr:
		return &hir.UnaryExpr{Op: e.Op, X: e.X, Type: target, Location: e.Location}
	case *hir.PrefixExpr:
		return &hir.PrefixExpr{Op: e.Op, X: e.X, Type: target, Location: e.Location}
	case *hir.PostfixExpr:
		return &hir.PostfixExpr{Op: e.Op, X: e.X, Type: target, Location: e.Location}
	case *hir.CastExpr:
		return &hir.CastExpr{X: e.X, TargetType: e.TargetType, Type: target, Location: e.Location}
	case *hir.CompositeLit:
		return &hir.CompositeLit{Type: target, Elts: e.Elts, Location: e.Location}
	case *hir.ParenExpr:
		return &hir.ParenExpr{X: e.X, Type: target, Location: e.Location}
	case *hir.CoalescingExpr:
		return &hir.CoalescingExpr{Cond: e.Cond, Default: e.Default, Type: target, Location: e.Location}
	default:
		return expr
	}
}

func (l *Lowerer) selectorFieldType(expr *hir.SelectorExpr) types.SemType {
	if expr == nil || expr.Field == nil {
		return types.TypeUnknown
	}

	baseType := l.exprType(expr.X)
	baseType = dereferenceType(baseType)
	unwrapped := types.UnwrapType(baseType)

	if structType, ok := unwrapped.(*types.StructType); ok {
		for _, field := range structType.Fields {
			if field.Name == expr.Field.Name {
				return field.Type
			}
		}
	}

	return types.TypeUnknown
}

func (l *Lowerer) optionalType(t types.SemType) *types.OptionalType {
	if t == nil {
		return nil
	}

	if opt, ok := t.(*types.OptionalType); ok {
		return opt
	}

	if opt, ok := types.UnwrapType(t).(*types.OptionalType); ok {
		return opt
	}

	return nil
}

func (l *Lowerer) optionalInnerType(t types.SemType) types.SemType {
	opt := l.optionalType(t)
	if opt == nil {
		return types.TypeUnknown
	}
	return opt.Inner
}

func (l *Lowerer) exprType(expr hir.Expr) types.SemType {
	if expr == nil {
		return types.TypeUnknown
	}

	switch e := expr.(type) {
	case *hir.Ident:
		return safeType(e.Type)
	case *hir.Literal:
		return safeType(e.Type)
	case *hir.OptionalNone:
		return safeType(e.Type)
	case *hir.OptionalSome:
		return safeType(e.Type)
	case *hir.OptionalIsSome:
		return safeType(e.Type)
	case *hir.OptionalIsNone:
		return safeType(e.Type)
	case *hir.OptionalUnwrap:
		return safeType(e.Type)
	case *hir.ResultOk:
		return safeType(e.Type)
	case *hir.ResultErr:
		return safeType(e.Type)
	case *hir.ResultUnwrap:
		return safeType(e.Type)
	case *hir.BinaryExpr:
		return safeType(e.Type)
	case *hir.UnaryExpr:
		return safeType(e.Type)
	case *hir.DerefExpr:
		return safeType(e.Type)
	case *hir.PrefixExpr:
		return safeType(e.Type)
	case *hir.PostfixExpr:
		return safeType(e.Type)
	case *hir.CallExpr:
		return safeType(e.Type)
	case *hir.SelectorExpr:
		return safeType(e.Type)
	case *hir.RangeExpr:
		return safeType(e.Type)
	case *hir.ArrayLenExpr:
		return safeType(e.Type)
	case *hir.StringLenExpr:
		return safeType(e.Type)
	case *hir.MapIterInitExpr:
		return safeType(e.Type)
	case *hir.MapIterNextExpr:
		return safeType(e.Type)
	case *hir.IndexExpr:
		return safeType(e.Type)
	case *hir.CastExpr:
		return safeType(e.Type)
	case *hir.CoalescingExpr:
		return safeType(e.Type)
	case *hir.ForkExpr:
		return safeType(e.Type)
	case *hir.ScopeResolutionExpr:
		return safeType(e.Type)
	case *hir.ParenExpr:
		return safeType(e.Type)
	case *hir.CompositeLit:
		return safeType(e.Type)
	case *hir.FuncLit:
		return safeType(e.Type)
	case *hir.KeyValueExpr:
		return types.TypeUnknown
	default:
		return types.TypeUnknown
	}
}

func (l *Lowerer) getFunctionType(expr hir.Expr) *types.FunctionType {
	if expr == nil {
		return nil
	}

	t := l.exprType(expr)
	if t == nil {
		return nil
	}

	if fn, ok := types.UnwrapType(t).(*types.FunctionType); ok {
		return fn
	}

	return nil
}

func (l *Lowerer) expectedArgType(fn *types.FunctionType, index int) types.SemType {
	if fn == nil || len(fn.Params) == 0 || index < 0 {
		return types.TypeUnknown
	}

	last := len(fn.Params) - 1
	isVariadic := fn.Params[last].IsVariadic

	if !isVariadic {
		if index >= len(fn.Params) {
			return types.TypeUnknown
		}
		return fn.Params[index].Type
	}

	if index < last {
		return fn.Params[index].Type
	}
	return fn.Params[last].Type
}

func iteratorItems(iterator hir.Node) ([]hir.DeclItem, source.Location, bool) {
	switch it := iterator.(type) {
	case *hir.VarDecl:
		return it.Decls, it.Location, true
	case *hir.Ident:
		return []hir.DeclItem{{Name: it, Type: it.Type}}, it.Location, false
	default:
		return nil, source.Location{}, false
	}
}

func declItemType(item hir.DeclItem) types.SemType {
	if item.Type != nil && !item.Type.Equals(types.TypeUnknown) {
		return item.Type
	}
	if item.Name != nil && item.Name.Type != nil {
		return item.Name.Type
	}
	return types.TypeUnknown
}

func isDiscardIdent(ident *hir.Ident) bool {
	if ident == nil {
		return true
	}
	return ident.Name == "_"
}

func (l *Lowerer) rangeElementType(rangeExpr hir.Expr, rng *hir.RangeExpr) types.SemType {
	rangeType := types.UnwrapType(dereferenceType(l.exprType(rangeExpr)))
	if arrayType, ok := rangeType.(*types.ArrayType); ok && arrayType.Element != nil {
		return arrayType.Element
	}
	if rng != nil {
		if startType := l.exprType(rng.Start); startType != nil && !startType.Equals(types.TypeUnknown) {
			startType = types.ResolveUntypedType(startType, types.TypeUnknown)
			if startType != nil && !startType.Equals(types.TypeUnknown) {
				return startType
			}
		}
		if endType := l.exprType(rng.End); endType != nil && !endType.Equals(types.TypeUnknown) {
			endType = types.ResolveUntypedType(endType, types.TypeUnknown)
			if endType != nil && !endType.Equals(types.TypeUnknown) {
				return endType
			}
		}
	}
	return types.TypeUnknown
}

func (l *Lowerer) newTempIdent(prefix string, typ types.SemType, loc source.Location) *hir.Ident {
	l.tempCounter++
	if typ == nil {
		typ = types.TypeUnknown
	}
	return &hir.Ident{
		Name:     fmt.Sprintf("%s%d", prefix, l.tempCounter),
		Type:     typ,
		Location: loc,
	}
}

func newVarDecl(ident *hir.Ident, typ types.SemType, value hir.Expr, loc source.Location) *hir.VarDecl {
	if typ == nil {
		typ = types.TypeUnknown
	}
	return &hir.VarDecl{
		Decls:    []hir.DeclItem{{Name: ident, Type: typ, Value: value}},
		Location: loc,
	}
}

func newAssign(lhs hir.Expr, rhs hir.Expr, op tokens.TOKEN, loc source.Location) *hir.AssignStmt {
	return &hir.AssignStmt{
		Lhs:      lhs,
		Rhs:      rhs,
		Op:       tokenPtr(op),
		Location: loc,
	}
}

func newBinary(lhs hir.Expr, rhs hir.Expr, op tokens.TOKEN, typ types.SemType, loc source.Location) *hir.BinaryExpr {
	return &hir.BinaryExpr{
		X:        lhs,
		Op:       token(op),
		Y:        rhs,
		Type:     typ,
		Location: loc,
	}
}

func token(kind tokens.TOKEN) tokens.Token {
	return tokens.Token{Kind: kind, Value: string(kind)}
}

func tokenPtr(kind tokens.TOKEN) *tokens.Token {
	tok := token(kind)
	return &tok
}

func boolLiteral(value bool, loc source.Location) *hir.Literal {
	val := "false"
	if value {
		val = "true"
	}
	return &hir.Literal{
		Kind:     hir.LiteralBool,
		Value:    val,
		Type:     types.TypeBool,
		Location: loc,
	}
}

func intLiteral(value int, typ types.SemType, loc source.Location) *hir.Literal {
	if typ == nil {
		typ = types.TypeUnknown
	}
	return &hir.Literal{
		Kind:     hir.LiteralInt,
		Value:    fmt.Sprintf("%d", value),
		Type:     typ,
		Location: loc,
	}
}

func floatLiteral(value string, typ types.SemType, loc source.Location) *hir.Literal {
	if typ == nil {
		typ = types.TypeUnknown
	}
	return &hir.Literal{
		Kind:     hir.LiteralFloat,
		Value:    value,
		Type:     typ,
		Location: loc,
	}
}

func defaultIncrementLiteral(typ types.SemType, loc source.Location) *hir.Literal {
	unwrapped := types.UnwrapType(typ)
	if types.IsFloat(unwrapped) {
		return floatLiteral("1.0", typ, loc)
	}
	return intLiteral(1, typ, loc)
}

func zeroLiteral(typ types.SemType, loc source.Location) *hir.Literal {
	unwrapped := types.UnwrapType(typ)
	if types.IsFloat(unwrapped) {
		return floatLiteral("0.0", typ, loc)
	}
	return intLiteral(0, typ, loc)
}

func (l *Lowerer) builtinIdent(name string, loc source.Location) *hir.Ident {
	if l.ctx != nil && l.ctx.Universe != nil {
		if sym, ok := l.ctx.Universe.GetSymbol(name); ok && sym != nil {
			return &hir.Ident{
				Name:     name,
				Symbol:   sym,
				Type:     sym.Type,
				Location: loc,
			}
		}
	}
	return &hir.Ident{
		Name:     name,
		Type:     types.TypeUnknown,
		Location: loc,
	}
}

func (l *Lowerer) panicStmt(msg string, loc source.Location) hir.Node {
	panicIdent := l.builtinIdent("panic", loc)
	msgLit := &hir.Literal{
		Kind:     hir.LiteralString,
		Value:    strconv.Quote(msg),
		Type:     types.TypeString,
		Location: loc,
	}
	call := &hir.CallExpr{
		Fun:      panicIdent,
		Args:     []hir.Expr{msgLit},
		Type:     types.TypeVoid,
		Location: loc,
	}
	return &hir.ExprStmt{X: call, Location: loc}
}

func isSimpleValue(expr hir.Expr) bool {
	switch e := expr.(type) {
	case *hir.Ident, *hir.SelectorExpr, *hir.ScopeResolutionExpr:
		return true
	case *hir.ParenExpr:
		return isSimpleValue(e.X)
	default:
		return false
	}
}

func (l *Lowerer) buildLoopBody(firstIdent *hir.Ident, incrAssign *hir.AssignStmt, cond hir.Expr, preBody hir.Node, body *hir.Block, loc source.Location) []hir.Node {
	firstIf := &hir.IfStmt{
		Cond: firstIdent,
		Body: &hir.Block{
			Nodes:    []hir.Node{newAssign(firstIdent, boolLiteral(false, loc), tokens.EQUALS_TOKEN, loc)},
			Location: loc,
		},
		Else: &hir.Block{
			Nodes:    []hir.Node{incrAssign},
			Location: loc,
		},
		Location: loc,
	}

	notCond := &hir.UnaryExpr{
		Op:       token(tokens.NOT_TOKEN),
		X:        cond,
		Type:     types.TypeBool,
		Location: loc,
	}
	breakIf := &hir.IfStmt{
		Cond: notCond,
		Body: &hir.Block{
			Nodes:    []hir.Node{&hir.BreakStmt{Location: loc}},
			Location: loc,
		},
		Location: loc,
	}

	nodes := []hir.Node{firstIf, breakIf}
	if preBody != nil {
		nodes = append(nodes, preBody)
	}
	if body != nil && len(body.Nodes) > 0 {
		nodes = append(nodes, body.Nodes...)
	}
	return nodes
}

func (l *Lowerer) wrapLoop(prelude []hir.Node, loopNodes []hir.Node, loc source.Location) hir.Node {
	whileStmt := &hir.WhileStmt{
		Cond:     boolLiteral(true, loc),
		Body:     &hir.Block{Nodes: loopNodes, Location: loc},
		Location: loc,
	}
	nodes := append(prelude, whileStmt)
	return &hir.Block{Nodes: nodes, Location: loc}
}

func unwrapRangeExpr(expr hir.Expr) *hir.RangeExpr {
	switch e := expr.(type) {
	case *hir.RangeExpr:
		return e
	case *hir.ParenExpr:
		return unwrapRangeExpr(e.X)
	case *hir.CastExpr:
		return unwrapRangeExpr(e.X)
	default:
		return nil
	}
}

func incrementSign(expr hir.Expr) (int, bool) {
	if expr == nil {
		return 1, true
	}
	val := consteval.EvaluateHIRExpr(nil, nil, expr)
	if val == nil || !val.IsConstant() {
		return 0, false
	}
	if intVal, ok := val.AsInt(); ok {
		if intVal.Sign() == 0 {
			return 0, false
		}
		return intVal.Sign(), true
	}
	if floatVal, ok := val.AsFloat(); ok {
		if floatVal.Sign() == 0 {
			return 0, false
		}
		return floatVal.Sign(), true
	}
	return 0, false
}

func rangeCondExpr(value hir.Expr, end hir.Expr, incr *hir.Ident, elemType types.SemType, posCond tokens.TOKEN, negCond tokens.TOKEN, sign int, signKnown bool, loc source.Location) hir.Expr {
	if signKnown {
		if sign < 0 {
			return newBinary(value, end, negCond, types.TypeBool, loc)
		}
		return newBinary(value, end, posCond, types.TypeBool, loc)
	}

	incrType := elemType
	if incr != nil && incr.Type != nil && !incr.Type.Equals(types.TypeUnknown) {
		incrType = incr.Type
	}
	zero := zeroLiteral(incrType, loc)

	posCheck := newBinary(incr, zero, tokens.GREATER_TOKEN, types.TypeBool, loc)
	negCheck := newBinary(incr, zero, tokens.LESS_TOKEN, types.TypeBool, loc)
	posCondExpr := newBinary(value, end, posCond, types.TypeBool, loc)
	negCondExpr := newBinary(value, end, negCond, types.TypeBool, loc)

	posAnd := newBinary(posCheck, posCondExpr, tokens.AND_TOKEN, types.TypeBool, loc)
	negAnd := newBinary(negCheck, negCondExpr, tokens.AND_TOKEN, types.TypeBool, loc)

	return newBinary(posAnd, negAnd, tokens.OR_TOKEN, types.TypeBool, loc)
}

func rangeStepFloatMismatch(startType, endType, incrType types.SemType) bool {
	if incrType == nil || incrType.Equals(types.TypeUnknown) {
		return false
	}
	if !isFloatLikeType(incrType) {
		return false
	}
	return isIntLikeType(startType) || isIntLikeType(endType)
}

func isFloatLikeType(t types.SemType) bool {
	if t == nil || t.Equals(types.TypeUnknown) {
		return false
	}
	t = types.UnwrapType(t)
	return types.IsFloat(t) || types.IsUntypedFloat(t)
}

func isIntLikeType(t types.SemType) bool {
	if t == nil || t.Equals(types.TypeUnknown) {
		return false
	}
	t = types.UnwrapType(t)
	return types.IsInteger(t) || types.IsUntypedInt(t)
}

func rangeInvalidCond(start, end, incr hir.Expr, elemType types.SemType, loc source.Location) hir.Expr {
	zero := zeroLiteral(elemType, loc)

	isZero := newBinary(incr, zero, tokens.DOUBLE_EQUAL_TOKEN, types.TypeBool, loc)
	posCheck := newBinary(incr, zero, tokens.GREATER_TOKEN, types.TypeBool, loc)
	negCheck := newBinary(incr, zero, tokens.LESS_TOKEN, types.TypeBool, loc)

	startGtEnd := newBinary(start, end, tokens.GREATER_TOKEN, types.TypeBool, loc)
	startLtEnd := newBinary(start, end, tokens.LESS_TOKEN, types.TypeBool, loc)

	posInvalid := newBinary(posCheck, startGtEnd, tokens.AND_TOKEN, types.TypeBool, loc)
	negInvalid := newBinary(negCheck, startLtEnd, tokens.AND_TOKEN, types.TypeBool, loc)
	dirInvalid := newBinary(posInvalid, negInvalid, tokens.OR_TOKEN, types.TypeBool, loc)

	return newBinary(isZero, dirInvalid, tokens.OR_TOKEN, types.TypeBool, loc)
}

func (l *Lowerer) rangeInvalidCheck(start, end, incr *hir.Ident, elemType types.SemType, loc source.Location) hir.Node {
	cond := rangeInvalidCond(start, end, incr, elemType, loc)
	return &hir.IfStmt{
		Cond: cond,
		Body: &hir.Block{
			Nodes:    []hir.Node{l.panicStmt("invalid range", loc)},
			Location: loc,
		},
		Location: loc,
	}
}

func isNoneExpr(expr hir.Expr) bool {
	if expr == nil {
		return false
	}

	switch e := expr.(type) {
	case *hir.OptionalNone:
		return true
	case *hir.Literal:
		return e.Kind == hir.LiteralNone
	case *hir.Ident:
		if e.Name == "none" {
			return true
		}
		if e.Symbol != nil && e.Symbol.Type != nil {
			return e.Symbol.Type.Equals(types.TypeNone)
		}
	}

	return false
}

func isOptionalType(t types.SemType) bool {
	if t == nil {
		return false
	}
	if _, ok := t.(*types.OptionalType); ok {
		return true
	}
	if _, ok := types.UnwrapType(t).(*types.OptionalType); ok {
		return true
	}
	return false
}

func dereferenceType(t types.SemType) types.SemType {
	if ref, ok := t.(*types.ReferenceType); ok {
		return ref.Inner
	}
	return t
}

func fieldNameFromKey(expr hir.Expr) string {
	switch e := expr.(type) {
	case *hir.Ident:
		return e.Name
	case *hir.SelectorExpr:
		if e.Field != nil {
			return e.Field.Name
		}
	}
	return ""
}

func locationFromExpr(expr hir.Expr) source.Location {
	if expr == nil || expr.Loc() == nil {
		return source.Location{}
	}
	return *expr.Loc()
}

func safeType(t types.SemType) types.SemType {
	if t == nil {
		return types.TypeUnknown
	}
	return t
}
