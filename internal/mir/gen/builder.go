package gen

import (
	"fmt"
	"strconv"
	"strings"

	"compiler/internal/hir"
	"compiler/internal/hir/consteval"
	"compiler/internal/mir"
	"compiler/internal/semantics/symbols"
	"compiler/internal/source"
	"compiler/internal/tokens"
	"compiler/internal/types"
	"compiler/internal/utils/numeric"
)

type functionBuilder struct {
	gen          *Generator
	fn           *mir.Function
	current      *mir.Block
	paramsByName map[string]mir.ValueID
	slots        map[*symbols.Symbol]mir.ValueID
	tempSlots    map[*hir.Ident]mir.ValueID
	ptrElem      map[mir.ValueID]types.SemType
	loopStack    []loopTargets
	retParam     mir.ValueID
	retType      types.SemType
	refOutParam  mir.ValueID
	refOutType   types.SemType
	closureEnv   mir.ValueID
	captures     map[*symbols.Symbol]captureInfo
	boxed        map[*symbols.Symbol]mir.ValueID
	entry        *mir.Block
}

func newFunctionBuilder(gen *Generator, fn *mir.Function) *functionBuilder {
	return &functionBuilder{
		gen:          gen,
		fn:           fn,
		paramsByName: make(map[string]mir.ValueID),
		slots:        make(map[*symbols.Symbol]mir.ValueID),
		tempSlots:    make(map[*hir.Ident]mir.ValueID),
		ptrElem:      make(map[mir.ValueID]types.SemType),
		loopStack:    nil,
		retParam:     mir.InvalidValue,
		refOutParam:  mir.InvalidValue,
		closureEnv:   mir.InvalidValue,
		captures:     nil,
		boxed:        make(map[*symbols.Symbol]mir.ValueID),
	}
}

func (b *functionBuilder) buildFuncBody(body *hir.Block) {
	entry := b.newBlock("entry", b.fn.Location)
	b.entry = entry
	b.setBlock(entry)

	for _, param := range b.fn.Params {
		if param.Name != "" {
			b.paramsByName[param.Name] = param.ID
		}
		if param.Name == "__ret" {
			b.retParam = param.ID
			if ref, ok := types.UnwrapType(param.Type).(*types.ReferenceType); ok {
				b.retType = ref.Inner
			} else {
				b.retType = param.Type
			}
			b.ptrElem[b.retParam] = b.retType
		}
		if param.Name == "__out" {
			b.refOutParam = param.ID
			if ref, ok := types.UnwrapType(param.Type).(*types.ReferenceType); ok {
				b.refOutType = ref.Inner
			} else {
				b.refOutType = param.Type
			}
			b.ptrElem[b.refOutParam] = b.refOutType
		}
	}

	if body != nil {
		b.lowerBlock(body)
	}

	if b.current.Term == nil {
		b.finalizeCurrent()
	}
}

func (b *functionBuilder) setClosureEnv(env mir.ValueID, captures []captureInfo) {
	b.closureEnv = env
	if len(captures) == 0 {
		b.captures = nil
		return
	}
	b.captures = make(map[*symbols.Symbol]captureInfo, len(captures))
	for _, cap := range captures {
		if cap.ident == nil || cap.ident.Symbol == nil {
			continue
		}
		b.captures[cap.ident.Symbol] = cap
	}
}

func (b *functionBuilder) finalizeCurrent() {
	if b.current == nil || b.current.Term != nil {
		return
	}
	if b.fn.Return != nil && b.fn.Return.Equals(types.TypeVoid) {
		b.current.Term = &mir.Return{HasValue: false, Location: b.current.Location}
		return
	}
	b.current.Term = &mir.Unreachable{Location: b.current.Location}
}

func (b *functionBuilder) lowerBlock(block *hir.Block) {
	if block == nil || b.current == nil {
		return
	}

	for _, node := range block.Nodes {
		b.lowerNode(node)
		if b.current.Term != nil {
			return
		}
	}
}

func (b *functionBuilder) lowerNode(node hir.Node) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *hir.DeclStmt:
		b.lowerDecl(n.Decl)
	case *hir.VarDecl:
		b.lowerVarDecl(n)
	case *hir.ConstDecl:
		b.lowerConstDecl(n)
	case *hir.AssignStmt:
		b.lowerAssign(n)
	case *hir.ReturnStmt:
		b.lowerReturn(n)
	case *hir.BreakStmt:
		b.lowerBreak(n)
	case *hir.ContinueStmt:
		b.lowerContinue(n)
	case *hir.ExprStmt:
		b.lowerExpr(n.X)
	case *hir.Block:
		b.lowerBlock(n)
	case *hir.IfStmt:
		b.lowerIf(n)
	case *hir.WhileStmt:
		b.lowerWhile(n)
	case *hir.MatchStmt:
		b.lowerMatch(n)
	default:
		b.reportUnsupported("statement", node.Loc())
	}
}

func (b *functionBuilder) lowerDecl(decl hir.Decl) {
	switch d := decl.(type) {
	case *hir.VarDecl:
		b.lowerVarDecl(d)
	case *hir.ConstDecl:
		b.lowerConstDecl(d)
	default:
		b.reportUnsupported("declaration", decl.Loc())
	}
}

func (b *functionBuilder) lowerVarDecl(decl *hir.VarDecl) {
	if decl == nil {
		return
	}

	for _, item := range decl.Decls {
		b.lowerDeclItem(item)
	}
}

func (b *functionBuilder) lowerConstDecl(decl *hir.ConstDecl) {
	if decl == nil {
		return
	}

	for _, item := range decl.Decls {
		b.lowerDeclItem(item)
	}
}

func (b *functionBuilder) lowerDeclItem(item hir.DeclItem) {
	if item.Name == nil {
		return
	}

	typ := item.Type
	if typ == nil && item.Value != nil {
		if vtyp := b.exprType(item.Value); vtyp != nil {
			typ = vtyp
		}
	}

	addr := b.emitAlloca(typ, item.Name.Location)
	if item.Name.Symbol != nil {
		b.slots[item.Name.Symbol] = addr
	} else {
		b.tempSlots[item.Name] = addr
	}

	if item.Value != nil {
		if lit, ok := item.Value.(*hir.CompositeLit); ok {
			if structType, ok := types.UnwrapType(typ).(*types.StructType); ok {
				b.lowerStructLiteralInto(addr, structType, lit)
				return
			}
			if arrType, ok := types.UnwrapType(typ).(*types.ArrayType); ok && arrType.Length >= 0 {
				b.lowerArrayLiteralInto(addr, arrType, lit)
				return
			}
		}

		val := b.lowerExpr(item.Value)
		if val != mir.InvalidValue {
			val = b.coerceValueForAssign(val, b.exprType(item.Value), typ, item.Name.Location)
			if val != mir.InvalidValue {
				b.emitStore(addr, val, item.Name.Location)
			}
		}
	}
}

func (b *functionBuilder) lowerAssign(stmt *hir.AssignStmt) {
	if stmt == nil {
		return
	}

	if idx, ok := stmt.Lhs.(*hir.IndexExpr); ok {
		b.lowerIndexAssign(idx, stmt.Rhs, stmt.Op, stmt.Location)
		return
	}

	addr := b.lowerLValue(stmt.Lhs)
	if addr == mir.InvalidValue {
		return
	}

	// Get the storage type for the LHS - this is the actual type of the variable,
	// not the narrowed type. Important for union/optional assignments after narrowing.
	lhsStorageType := b.getStorageType(stmt.Lhs)

	if lhsStorageType != nil {
		if ref, ok := types.UnwrapType(lhsStorageType).(*types.ReferenceType); ok {
			refPtr := b.emitLoad(addr, lhsStorageType, stmt.Location)
			if refPtr == mir.InvalidValue {
				return
			}
			if stmt.Op != nil && stmt.Op.Kind != tokens.EQUALS_TOKEN {
				cur := b.emitLoad(refPtr, ref.Inner, stmt.Location)
				rhs := b.lowerExpr(stmt.Rhs)
				if cur == mir.InvalidValue || rhs == mir.InvalidValue {
					return
				}
				rhs = b.coerceValueForAssign(rhs, b.exprType(stmt.Rhs), ref.Inner, stmt.Location)
				if rhs == mir.InvalidValue {
					return
				}
				op := assignTokenToBinary(stmt.Op.Kind)
				if op == "" {
					b.reportUnsupported("assignment operator", stmt.Loc())
					return
				}
				res := b.emitBinary(op, cur, rhs, ref.Inner, stmt.Location)
				b.emitStore(refPtr, res, stmt.Location)
				return
			}

			rhs := b.lowerExpr(stmt.Rhs)
			if rhs == mir.InvalidValue {
				return
			}
			rhs = b.coerceValueForAssign(rhs, b.exprType(stmt.Rhs), ref.Inner, stmt.Location)
			if rhs == mir.InvalidValue {
				return
			}
			b.emitStore(refPtr, rhs, stmt.Location)
			return
		}
	}

	if stmt.Op != nil && stmt.Op.Kind != tokens.EQUALS_TOKEN {
		cur := b.emitLoad(addr, lhsStorageType, stmt.Location)
		rhs := b.lowerExpr(stmt.Rhs)
		if cur == mir.InvalidValue || rhs == mir.InvalidValue {
			return
		}
		rhs = b.coerceValueForAssign(rhs, b.exprType(stmt.Rhs), lhsStorageType, stmt.Location)
		if rhs == mir.InvalidValue {
			return
		}
		op := assignTokenToBinary(stmt.Op.Kind)
		if op == "" {
			b.reportUnsupported("assignment operator", stmt.Loc())
			return
		}
		res := b.emitBinary(op, cur, rhs, lhsStorageType, stmt.Location)
		b.emitStore(addr, res, stmt.Location)
		return
	}

	rhs := b.lowerExpr(stmt.Rhs)
	if rhs == mir.InvalidValue {
		return
	}
	rhs = b.coerceValueForAssign(rhs, b.exprType(stmt.Rhs), lhsStorageType, stmt.Location)
	if rhs == mir.InvalidValue {
		return
	}
	b.emitStore(addr, rhs, stmt.Location)
}

func (b *functionBuilder) lowerReturn(stmt *hir.ReturnStmt) {
	if stmt == nil {
		return
	}

	if stmt.Result == nil {
		b.current.Term = &mir.Return{HasValue: false, Location: stmt.Location}
		return
	}

	val := b.lowerExpr(stmt.Result)
	if val == mir.InvalidValue {
		b.current.Term = &mir.Unreachable{Location: stmt.Location}
		return
	}

	resultType := b.exprType(stmt.Result)
	retType := b.fn.Return
	if b.retParam != mir.InvalidValue {
		retType = b.retType
	}
	val = b.coerceValueForAssign(val, resultType, retType, stmt.Location)
	if val == mir.InvalidValue {
		b.current.Term = &mir.Unreachable{Location: stmt.Location}
		return
	}

	if b.refOutParam != mir.InvalidValue {
		if resultType != nil {
			if _, ok := types.UnwrapType(resultType).(*types.ReferenceType); ok {
				b.current.Term = &mir.Return{HasValue: true, Value: val, Location: stmt.Location}
				return
			}
		}
		b.emitStore(b.refOutParam, val, stmt.Location)
		b.current.Term = &mir.Return{HasValue: true, Value: b.refOutParam, Location: stmt.Location}
		return
	}

	if b.retParam != mir.InvalidValue {
		b.emitStore(b.retParam, val, stmt.Location)
		b.current.Term = &mir.Return{HasValue: false, Location: stmt.Location}
		return
	}

	b.current.Term = &mir.Return{HasValue: true, Value: val, Location: stmt.Location}
}

func (b *functionBuilder) lowerIf(stmt *hir.IfStmt) {
	if stmt == nil {
		return
	}

	cond := b.lowerValueExpr(stmt.Cond, stmt.Location)
	if cond == mir.InvalidValue {
		b.current.Term = &mir.Unreachable{Location: stmt.Location}
		return
	}

	thenBlock := b.newBlock("if.then", stmt.Location)
	mergeBlock := b.newBlock("if.end", stmt.Location)

	var elseBlock *mir.Block
	if stmt.Else != nil {
		elseBlock = b.newBlock("if.else", stmt.Location)
	}

	elseTarget := mergeBlock.ID
	if elseBlock != nil {
		elseTarget = elseBlock.ID
	}

	b.current.Term = &mir.CondBr{
		Cond:     cond,
		Then:     thenBlock.ID,
		Else:     elseTarget,
		Location: stmt.Location,
	}

	b.setBlock(thenBlock)
	if stmt.Body != nil {
		b.lowerBlock(stmt.Body)
	}
	b.branchIfNoTerm(mergeBlock.ID, stmt.Location)

	if elseBlock != nil {
		b.setBlock(elseBlock)
		b.lowerNode(stmt.Else)
		b.branchIfNoTerm(mergeBlock.ID, stmt.Location)
	}

	b.setBlock(mergeBlock)
}

func (b *functionBuilder) lowerBreak(stmt *hir.BreakStmt) {
	if stmt == nil {
		return
	}

	loop := b.currentLoop()
	if loop == nil {
		b.reportUnsupported("break outside loop", &stmt.Location)
		b.current.Term = &mir.Unreachable{Location: stmt.Location}
		return
	}

	b.current.Term = &mir.Br{Target: loop.breakTarget, Location: stmt.Location}
}

func (b *functionBuilder) lowerContinue(stmt *hir.ContinueStmt) {
	if stmt == nil {
		return
	}

	loop := b.currentLoop()
	if loop == nil {
		b.reportUnsupported("continue outside loop", &stmt.Location)
		b.current.Term = &mir.Unreachable{Location: stmt.Location}
		return
	}

	b.current.Term = &mir.Br{Target: loop.continueTarget, Location: stmt.Location}
}

func (b *functionBuilder) lowerWhile(stmt *hir.WhileStmt) {
	if stmt == nil {
		return
	}

	condBlock := b.newBlock("while.cond", stmt.Location)
	bodyBlock := b.newBlock("while.body", stmt.Location)
	exitBlock := b.newBlock("while.end", stmt.Location)

	b.branchIfNoTerm(condBlock.ID, stmt.Location)

	b.setBlock(condBlock)
	cond := b.lowerValueExpr(stmt.Cond, stmt.Location)
	if cond == mir.InvalidValue {
		b.current.Term = &mir.Unreachable{Location: stmt.Location}
	} else {
		b.current.Term = &mir.CondBr{
			Cond:     cond,
			Then:     bodyBlock.ID,
			Else:     exitBlock.ID,
			Location: stmt.Location,
		}
	}

	b.pushLoop(exitBlock.ID, condBlock.ID)
	b.setBlock(bodyBlock)
	if stmt.Body != nil {
		b.lowerBlock(stmt.Body)
	}
	b.branchIfNoTerm(condBlock.ID, stmt.Location)
	b.popLoop()

	b.setBlock(exitBlock)
}

func (b *functionBuilder) lowerMatch(stmt *hir.MatchStmt) {
	if stmt == nil {
		return
	}

	cond := b.lowerValueExpr(stmt.Expr, stmt.Location)
	if cond == mir.InvalidValue {
		b.current.Term = &mir.Unreachable{Location: stmt.Location}
		return
	}

	matchType := b.exprType(stmt.Expr)
	if ref, ok := types.UnwrapType(matchType).(*types.ReferenceType); ok {
		matchType = ref.Inner
	}
	if matchType == nil || matchType.Equals(types.TypeUnknown) {
		matchType = b.exprType(stmt.Expr)
	}

	mergeBlock := b.newBlock("match.end", stmt.Location)
	defaultBlock := mergeBlock

	type caseEntry struct {
		clause *hir.CaseClause
		block  *mir.Block
	}
	entries := make([]caseEntry, 0, len(stmt.Cases))

	useSwitch := isSwitchableMatchType(matchType)
	switchCases := make([]mir.SwitchCase, 0, len(stmt.Cases))
	seenValues := make(map[string]struct{}, len(stmt.Cases))

	type matchCase struct {
		block *mir.Block
		value mir.ValueID
		loc   source.Location
	}

	for idx := range stmt.Cases {
		clause := &stmt.Cases[idx]
		block := b.newBlock("match.case", clause.Location)
		entries = append(entries, caseEntry{clause: clause, block: block})

		if clause.Pattern == nil {
			defaultBlock = block
			continue
		}

		if useSwitch {
			value, ok := b.matchCaseValue(clause.Pattern)
			if !ok {
				useSwitch = false
				continue
			}
			if _, exists := seenValues[value]; exists {
				useSwitch = false
				continue
			}
			seenValues[value] = struct{}{}
			switchCases = append(switchCases, mir.SwitchCase{
				Value:  value,
				Target: block.ID,
			})
		}
	}

	if useSwitch && len(switchCases) > 0 {
		b.current.Term = &mir.Switch{
			Cond:     cond,
			Cases:    switchCases,
			Default:  defaultBlock.ID,
			Location: stmt.Location,
		}
	} else {
		patternCases := make([]matchCase, 0, len(stmt.Cases))
		for _, entry := range entries {
			clause := entry.clause
			if clause.Pattern == nil {
				continue
			}
			value, ok := b.matchCaseConstValue(clause.Pattern, matchType)
			if !ok {
				b.reportUnsupported("match pattern", clause.Pattern.Loc())
				continue
			}
			patternCases = append(patternCases, matchCase{
				block: entry.block,
				value: value,
				loc:   clause.Location,
			})
		}

		if len(patternCases) == 0 {
			b.branchIfNoTerm(defaultBlock.ID, stmt.Location)
		} else {
			current := b.current
			for idx, entry := range patternCases {
				var elseTarget mir.BlockID
				var elseBlock *mir.Block
				if idx < len(patternCases)-1 {
					elseBlock = b.newBlock("match.check", entry.loc)
					elseTarget = elseBlock.ID
				} else {
					elseTarget = defaultBlock.ID
				}

				var cmp mir.ValueID
				if isLargePrimitiveType(matchType) {
					cmp = b.emitLargeCompare(tokens.DOUBLE_EQUAL_TOKEN, cond, entry.value, matchType, entry.loc)
				} else {
					cmp = b.emitBinary(tokens.DOUBLE_EQUAL_TOKEN, cond, entry.value, matchType, entry.loc)
				}

				current.Term = &mir.CondBr{
					Cond:     cmp,
					Then:     entry.block.ID,
					Else:     elseTarget,
					Location: stmt.Location,
				}

				if elseBlock != nil {
					b.setBlock(elseBlock)
					current = elseBlock
				}
			}
		}
	}

	for _, entry := range entries {
		clause := entry.clause
		block := entry.block
		b.setBlock(block)
		if clause.Body != nil {
			b.lowerBlock(clause.Body)
		}
		b.branchIfNoTerm(mergeBlock.ID, clause.Location)
	}

	b.setBlock(mergeBlock)
}

func (b *functionBuilder) lowerExpr(expr hir.Expr) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}

	switch e := expr.(type) {
	case *hir.Literal:
		if isLargePrimitiveType(e.Type) {
			return b.emitLargeConst(e.Type, e.Value, e.Location)
		}
		return b.emitConst(e.Type, e.Value, e.Location)
	case *hir.FuncLit:
		return b.lowerFuncLit(e)
	case *hir.Ident:
		return b.loadIdent(e)
	case *hir.OptionalNone:
		id := b.gen.nextValueID()
		b.emitInstr(&mir.OptionalNone{
			Result:   id,
			Type:     e.Type,
			Location: e.Location,
		})
		return id
	case *hir.OptionalSome:
		value := b.lowerExpr(e.Value)
		if value == mir.InvalidValue {
			return mir.InvalidValue
		}
		id := b.gen.nextValueID()
		b.emitInstr(&mir.OptionalSome{
			Result:   id,
			Value:    value,
			Type:     e.Type,
			Location: e.Location,
		})
		return id
	case *hir.OptionalIsSome:
		value := b.lowerExpr(e.Value)
		if value == mir.InvalidValue {
			return mir.InvalidValue
		}
		id := b.gen.nextValueID()
		b.emitInstr(&mir.OptionalIsSome{
			Result:   id,
			Value:    value,
			Location: e.Location,
		})
		return id
	case *hir.OptionalIsNone:
		value := b.lowerExpr(e.Value)
		if value == mir.InvalidValue {
			return mir.InvalidValue
		}
		isSome := b.gen.nextValueID()
		b.emitInstr(&mir.OptionalIsSome{
			Result:   isSome,
			Value:    value,
			Location: e.Location,
		})
		return b.emitUnary(tokens.NOT_TOKEN, isSome, types.TypeBool, e.Location)
	case *hir.OptionalUnwrap:
		value := b.lowerExpr(e.Value)
		if value == mir.InvalidValue {
			return mir.InvalidValue
		}
		defaultVal := mir.InvalidValue
		hasDefault := false
		if e.Default != nil {
			defaultVal = b.lowerExpr(e.Default)
			if defaultVal == mir.InvalidValue {
				return mir.InvalidValue
			}
			hasDefault = true
		}
		id := b.gen.nextValueID()
		b.emitInstr(&mir.OptionalUnwrap{
			Result:     id,
			Value:      value,
			Default:    defaultVal,
			HasDefault: hasDefault,
			Type:       e.Type,
			Location:   e.Location,
		})
		return id
	case *hir.UnionVariantCheck:
		value := b.lowerExpr(e.Value)
		if value == mir.InvalidValue {
			return mir.InvalidValue
		}
		value, _ = b.derefValueIfNeeded(value, b.exprType(e.Value), e.Location)
		id := b.gen.nextValueID()
		b.emitInstr(&mir.UnionVariantCheck{
			Result:       id,
			Value:        value,
			VariantIndex: e.VariantIndex,
			UnionType:    e.UnionType,
			Location:     e.Location,
		})
		return id
	case *hir.UnionExtract:
		value := b.lowerExpr(e.Value)
		if value == mir.InvalidValue {
			return mir.InvalidValue
		}
		value, _ = b.derefValueIfNeeded(value, b.exprType(e.Value), e.Location)
		id := b.gen.nextValueID()
		b.emitInstr(&mir.UnionExtract{
			Result:       id,
			Value:        value,
			VariantIndex: e.VariantIndex,
			UnionType:    e.UnionType,
			Type:         e.Type,
			Location:     e.Location,
		})
		return id
	case *hir.ResultOk:
		value := b.lowerExpr(e.Value)
		if value == mir.InvalidValue {
			return mir.InvalidValue
		}
		id := b.gen.nextValueID()
		b.emitInstr(&mir.ResultOk{
			Result:   id,
			Value:    value,
			Type:     e.Type,
			Location: e.Location,
		})
		return id
	case *hir.ResultErr:
		value := b.lowerExpr(e.Value)
		if value == mir.InvalidValue {
			return mir.InvalidValue
		}
		id := b.gen.nextValueID()
		b.emitInstr(&mir.ResultErr{
			Result:   id,
			Value:    value,
			Type:     e.Type,
			Location: e.Location,
		})
		return id
	case *hir.ResultUnwrap:
		return b.lowerResultUnwrap(e)
	case *hir.BinaryExpr:
		// Special handling for 'is' operator
		if e.Op.Kind == tokens.IS_TOKEN {
			return b.emitTypeCheck(e)
		}

		left := b.lowerExpr(e.X)
		if left == mir.InvalidValue {
			return mir.InvalidValue
		}
		leftType := b.exprType(e.X)
		left, leftType = b.derefValueIfNeeded(left, leftType, e.Location)

		right := b.lowerExpr(e.Y)
		if right == mir.InvalidValue {
			return mir.InvalidValue
		}
		rightType := b.exprType(e.Y)
		right, rightType = b.derefValueIfNeeded(right, rightType, e.Location)

		// String concatenation: str + str, str + number, str + bool
		if e.Op.Kind == tokens.PLUS_TOKEN && b.isStringType(e.X) {
			return b.emitStringConcat(left, right, rightType, e.Location)
		}
		if e.Type != nil && !isCompareOp(e.Op.Kind) {
			target := types.UnwrapType(e.Type)
			if types.IsNumeric(target) {
				if leftType != nil && types.IsNumeric(types.UnwrapType(leftType)) && !leftType.Equals(e.Type) {
					left = b.castValue(left, leftType, e.Type, e.Location)
				}
				if rightType != nil && types.IsNumeric(types.UnwrapType(rightType)) && !rightType.Equals(e.Type) {
					right = b.castValue(right, rightType, e.Type, e.Location)
				}
			}
		}
		if isLargePrimitiveType(leftType) {
			if isCompareOp(e.Op.Kind) {
				return b.emitLargeCompare(e.Op.Kind, left, right, leftType, e.Location)
			}
			return b.emitLargeBinary(e.Op.Kind, left, right, leftType, e.Location)
		}
		return b.emitBinary(e.Op.Kind, left, right, e.Type, e.Location)
	case *hir.UnaryExpr:
		if e.Op.Kind == tokens.BIT_AND_TOKEN || e.Op.Kind == tokens.MUT_REF_TOKEN {
			return b.lowerLValue(e.X)
		}
		operand := b.lowerExpr(e.X)
		if operand == mir.InvalidValue {
			return mir.InvalidValue
		}
		operand, _ = b.derefValueIfNeeded(operand, b.exprType(e.X), e.Location)
		return b.emitUnary(e.Op.Kind, operand, e.Type, e.Location)
	case *hir.PrefixExpr:
		return b.lowerPrefix(e)
	case *hir.PostfixExpr:
		return b.lowerPostfix(e)
	case *hir.ParenExpr:
		return b.lowerExpr(e.X)
	case *hir.CallExpr:
		return b.lowerCall(e)
	case *hir.IndexExpr:
		return b.lowerIndexValue(e)
	case *hir.CastExpr:
		value := b.lowerExpr(e.X)
		if value == mir.InvalidValue {
			return mir.InvalidValue
		}
		return b.castValue(value, b.exprType(e.X), e.Type, e.Location)
	case *hir.CoalescingExpr:
		cond := b.lowerValueExpr(e.Cond, e.Location)
		if cond == mir.InvalidValue {
			return mir.InvalidValue
		}
		def := b.lowerValueExpr(e.Default, e.Location)
		if def == mir.InvalidValue {
			return mir.InvalidValue
		}
		id := b.gen.nextValueID()
		b.emitInstr(&mir.OptionalUnwrap{
			Result:     id,
			Value:      cond,
			Default:    def,
			HasDefault: true,
			Type:       e.Type,
			Location:   e.Location,
		})
		return id
	case *hir.CompositeLit:
		return b.lowerCompositeLit(e)
	case *hir.ArrayLenExpr:
		arrVal := b.lowerExpr(e.X)
		if arrVal == mir.InvalidValue {
			return mir.InvalidValue
		}
		if arrType := b.arrayTypeOf(e.X); arrType != nil && arrType.Length >= 0 {
			return b.emitConst(types.TypeI32, strconv.Itoa(arrType.Length), e.Location)
		}
		return b.emitArrayLen(arrVal, e.Location)
	case *hir.StringLenExpr:
		strVal := b.lowerExpr(e.X)
		if strVal == mir.InvalidValue {
			return mir.InvalidValue
		}
		return b.emitStringLen(strVal, e.Location)
	case *hir.MapIterInitExpr:
		return b.lowerMapIterInit(e)
	case *hir.MapIterNextExpr:
		return b.lowerMapIterNext(e)
	case *hir.SelectorExpr:
		return b.lowerSelector(e)
	case *hir.ScopeResolutionExpr:
		return b.lowerQualifiedValue(e)
	default:
		b.reportUnsupported("expression", expr.Loc())
		return mir.InvalidValue
	}
}

func (b *functionBuilder) lowerValueExpr(expr hir.Expr, loc source.Location) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}
	val := b.lowerExpr(expr)
	if val == mir.InvalidValue {
		return mir.InvalidValue
	}
	val, _ = b.derefValueIfNeeded(val, b.exprType(expr), loc)
	return val
}

func (b *functionBuilder) derefValueIfNeeded(val mir.ValueID, typ types.SemType, loc source.Location) (mir.ValueID, types.SemType) {
	if val == mir.InvalidValue || typ == nil {
		return val, typ
	}
	if ref, ok := types.UnwrapType(typ).(*types.ReferenceType); ok {
		val = b.emitLoad(val, ref.Inner, loc)
		typ = ref.Inner
	}
	return val, typ
}

func (b *functionBuilder) coerceValueForAssign(val mir.ValueID, fromType, toType types.SemType, loc source.Location) mir.ValueID {
	if val == mir.InvalidValue || fromType == nil || toType == nil {
		return val
	}
	if _, ok := types.UnwrapType(toType).(*types.ReferenceType); ok {
		return val
	}
	if ref, ok := types.UnwrapType(fromType).(*types.ReferenceType); ok {
		val = b.emitLoad(val, ref.Inner, loc)
		fromType = ref.Inner
	}
	// Handle union type assignment
	if unionType, ok := types.UnwrapType(toType).(*types.UnionType); ok {
		val = b.boxUnionValue(val, fromType, unionType, loc)
	} else if interfaceTypeOf(toType) != nil {
		val = b.boxInterfaceValue(val, fromType, toType, loc)
	}
	return val
}

func (b *functionBuilder) lowerResultUnwrap(expr *hir.ResultUnwrap) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}

	if expr.Catch == nil {
		value := b.lowerExpr(expr.Value)
		if value == mir.InvalidValue {
			return mir.InvalidValue
		}
		id := b.gen.nextValueID()
		b.emitInstr(&mir.ResultUnwrap{
			Result:     id,
			Value:      value,
			Default:    mir.InvalidValue,
			HasDefault: false,
			Type:       expr.Type,
			Location:   expr.Location,
		})
		return id
	}

	value := b.lowerExpr(expr.Value)
	if value == mir.InvalidValue {
		return mir.InvalidValue
	}

	valueType := b.exprType(expr.Value)
	resultType, ok := types.UnwrapType(valueType).(*types.ResultType)
	if !ok || resultType == nil {
		b.reportUnsupported("result catch type", expr.Loc())
		return mir.InvalidValue
	}

	isOk := b.gen.nextValueID()
	b.emitInstr(&mir.ResultIsOk{
		Result:   isOk,
		Value:    value,
		Location: expr.Location,
	})

	okBlock := b.newBlock("result.ok", expr.Location)
	errBlock := b.newBlock("result.err", expr.Location)
	needMerge := expr.Catch.Fallback != nil
	var mergeBlock *mir.Block
	if needMerge {
		mergeBlock = b.newBlock("result.merge", expr.Location)
	}

	b.current.Term = &mir.CondBr{
		Cond:     isOk,
		Then:     okBlock.ID,
		Else:     errBlock.ID,
		Location: expr.Location,
	}

	b.setBlock(okBlock)
	okType := resultType.Ok
	if okType == nil {
		okType = expr.Type
	}
	okVal := b.gen.nextValueID()
	b.emitInstr(&mir.ResultUnwrap{
		Result:     okVal,
		Value:      value,
		Default:    mir.InvalidValue,
		HasDefault: false,
		Type:       okType,
		Location:   expr.Location,
	})
	if needMerge {
		b.branchIfNoTerm(mergeBlock.ID, expr.Location)
	}

	b.setBlock(errBlock)
	if expr.Catch.ErrIdent != nil {
		errType := resultType.Err
		if errType == nil {
			b.reportUnsupported("result catch error type", expr.Loc())
			return mir.InvalidValue
		}
		errVal := b.gen.nextValueID()
		b.emitInstr(&mir.ResultUnwrap{
			Result:     errVal,
			Value:      value,
			Default:    mir.InvalidValue,
			HasDefault: false,
			Type:       errType,
			Location:   expr.Location,
		})
		b.bindCatchIdent(expr.Catch.ErrIdent, errVal, errType)
	}
	if expr.Catch.Handler != nil {
		b.lowerBlock(expr.Catch.Handler)
	}

	var fallbackVal mir.ValueID
	errReachesMerge := false
	if needMerge && b.current.Term == nil {
		fallbackVal = b.lowerExpr(expr.Catch.Fallback)
		if fallbackVal == mir.InvalidValue {
			b.current.Term = &mir.Unreachable{Location: expr.Location}
		} else {
			b.branchIfNoTerm(mergeBlock.ID, expr.Location)
			errReachesMerge = true
		}
	} else if b.current.Term == nil {
		b.current.Term = &mir.Unreachable{Location: expr.Location}
	}

	if needMerge {
		b.setBlock(mergeBlock)
		incoming := []mir.PhiIncoming{{Pred: okBlock.ID, Value: okVal}}
		if errReachesMerge && fallbackVal != mir.InvalidValue {
			incoming = append(incoming, mir.PhiIncoming{Pred: errBlock.ID, Value: fallbackVal})
		}
		result := b.gen.nextValueID()
		b.emitInstr(&mir.Phi{
			Result:   result,
			Type:     expr.Type,
			Incoming: incoming,
			Location: expr.Location,
		})
		return result
	}

	b.setBlock(okBlock)
	return okVal
}

func (b *functionBuilder) bindCatchIdent(ident *hir.Ident, value mir.ValueID, typ types.SemType) {
	if ident == nil || value == mir.InvalidValue || typ == nil {
		return
	}
	addr := b.emitAlloca(typ, ident.Location)
	if ident.Symbol != nil {
		b.slots[ident.Symbol] = addr
	} else {
		b.tempSlots[ident] = addr
	}
	b.emitStore(addr, value, ident.Location)
}

func (b *functionBuilder) lowerPrefix(expr *hir.PrefixExpr) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}

	if expr.Op.Kind != tokens.PLUS_PLUS_TOKEN && expr.Op.Kind != tokens.MINUS_MINUS_TOKEN {
		operand := b.lowerExpr(expr.X)
		if operand == mir.InvalidValue {
			return mir.InvalidValue
		}
		operand, _ = b.derefValueIfNeeded(operand, b.exprType(expr.X), expr.Location)
		return b.emitUnary(expr.Op.Kind, operand, expr.Type, expr.Location)
	}

	addr := b.lowerLValue(expr.X)
	if addr == mir.InvalidValue {
		return mir.InvalidValue
	}

	typ := b.exprType(expr.X)
	if ref, ok := types.UnwrapType(typ).(*types.ReferenceType); ok {
		refPtr := b.emitLoad(addr, typ, expr.Location)
		if refPtr == mir.InvalidValue {
			return mir.InvalidValue
		}
		cur := b.emitLoad(refPtr, ref.Inner, expr.Location)
		if cur == mir.InvalidValue {
			return mir.InvalidValue
		}
		one := mir.InvalidValue
		if isLargePrimitiveType(ref.Inner) {
			one = b.emitLargeConst(ref.Inner, "1", expr.Location)
		} else {
			one = b.emitConst(ref.Inner, "1", expr.Location)
		}
		op := tokens.PLUS_TOKEN
		if expr.Op.Kind == tokens.MINUS_MINUS_TOKEN {
			op = tokens.MINUS_TOKEN
		}
		next := b.emitBinary(op, cur, one, ref.Inner, expr.Location)
		b.emitStore(refPtr, next, expr.Location)
		return next
	}

	cur := b.emitLoad(addr, typ, expr.Location)
	if cur == mir.InvalidValue {
		return mir.InvalidValue
	}
	one := mir.InvalidValue
	if isLargePrimitiveType(typ) {
		one = b.emitLargeConst(typ, "1", expr.Location)
	} else {
		one = b.emitConst(typ, "1", expr.Location)
	}
	op := tokens.PLUS_TOKEN
	if expr.Op.Kind == tokens.MINUS_MINUS_TOKEN {
		op = tokens.MINUS_TOKEN
	}
	next := b.emitBinary(op, cur, one, typ, expr.Location)
	b.emitStore(addr, next, expr.Location)
	return next
}

func (b *functionBuilder) lowerPostfix(expr *hir.PostfixExpr) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}

	if expr.Op.Kind != tokens.PLUS_PLUS_TOKEN && expr.Op.Kind != tokens.MINUS_MINUS_TOKEN {
		operand := b.lowerExpr(expr.X)
		if operand == mir.InvalidValue {
			return mir.InvalidValue
		}
		operand, _ = b.derefValueIfNeeded(operand, b.exprType(expr.X), expr.Location)
		return b.emitUnary(expr.Op.Kind, operand, expr.Type, expr.Location)
	}

	addr := b.lowerLValue(expr.X)
	if addr == mir.InvalidValue {
		return mir.InvalidValue
	}

	typ := b.exprType(expr.X)
	if ref, ok := types.UnwrapType(typ).(*types.ReferenceType); ok {
		refPtr := b.emitLoad(addr, typ, expr.Location)
		if refPtr == mir.InvalidValue {
			return mir.InvalidValue
		}
		cur := b.emitLoad(refPtr, ref.Inner, expr.Location)
		if cur == mir.InvalidValue {
			return mir.InvalidValue
		}
		one := mir.InvalidValue
		if isLargePrimitiveType(ref.Inner) {
			one = b.emitLargeConst(ref.Inner, "1", expr.Location)
		} else {
			one = b.emitConst(ref.Inner, "1", expr.Location)
		}
		op := tokens.PLUS_TOKEN
		if expr.Op.Kind == tokens.MINUS_MINUS_TOKEN {
			op = tokens.MINUS_TOKEN
		}
		next := b.emitBinary(op, cur, one, ref.Inner, expr.Location)
		b.emitStore(refPtr, next, expr.Location)
		return cur
	}

	cur := b.emitLoad(addr, typ, expr.Location)
	if cur == mir.InvalidValue {
		return mir.InvalidValue
	}

	one := mir.InvalidValue
	if isLargePrimitiveType(typ) {
		one = b.emitLargeConst(typ, "1", expr.Location)
	} else {
		one = b.emitConst(typ, "1", expr.Location)
	}
	op := tokens.PLUS_TOKEN
	if expr.Op.Kind == tokens.MINUS_MINUS_TOKEN {
		op = tokens.MINUS_TOKEN
	}
	next := b.emitBinary(op, cur, one, typ, expr.Location)
	b.emitStore(addr, next, expr.Location)
	return cur
}

func (b *functionBuilder) lowerCall(expr *hir.CallExpr) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}

	if ident, ok := expr.Fun.(*hir.Ident); ok {
		if name, ok := builtinNameFromIdent(ident); ok {
			switch name {
			case "len":
				return b.lowerBuiltinLenCall(expr)
			case "append":
				return b.lowerBuiltinAppendCall(expr)
			case "panic":
				return b.lowerBuiltinPanicCall(expr)
			}
		}
	}

	fnType, _ := types.UnwrapType(b.exprType(expr.Fun)).(*types.FunctionType)

	if selector, ok := expr.Fun.(*hir.SelectorExpr); ok {
		if iface := interfaceTypeOf(b.exprType(selector.X)); iface != nil && len(iface.Methods) > 0 {
			return b.lowerInterfaceMethodCall(selector, expr, fnType)
		}
		if target, method, ok := b.methodCallTarget(selector); ok {
			recv := b.methodReceiverArg(selector, method)
			if recv == mir.InvalidValue {
				return mir.InvalidValue
			}
			args := b.lowerCallArgs(expr.Args, fnType, expr.Location, false)
			if args == nil {
				return mir.InvalidValue
			}
			return b.emitCall(target, append([]mir.ValueID{recv}, args...), expr)
		}
	}

	if target, ok := b.callTarget(expr.Fun); ok {
		// Print/Println resolve by concrete arg type in QBE.
		skipInterfaceBoxing := b.isStdIoPrintTarget(target)
		args := b.lowerCallArgs(expr.Args, fnType, expr.Location, skipInterfaceBoxing)
		if args == nil {
			return mir.InvalidValue
		}
		return b.emitCall(target, args, expr)
	}

	if _, ok := types.UnwrapType(b.exprType(expr.Fun)).(*types.FunctionType); ok {
		callee := b.lowerExpr(expr.Fun)
		if callee == mir.InvalidValue {
			return mir.InvalidValue
		}
		args := b.lowerCallArgs(expr.Args, fnType, expr.Location, false)
		if args == nil {
			return mir.InvalidValue
		}
		return b.emitCallIndirect(callee, append([]mir.ValueID{callee}, args...), expr)
	}

	b.reportUnsupported("call target", &expr.Location)
	return mir.InvalidValue
}

func (b *functionBuilder) lowerCallArgs(args []hir.Expr, fnType *types.FunctionType, loc source.Location, skipInterfaceBoxing bool) []mir.ValueID {
	out := make([]mir.ValueID, 0, len(args))
	for i, arg := range args {
		val := b.lowerExpr(arg)
		if val == mir.InvalidValue {
			return nil
		}
		argType := b.exprType(arg)
		var paramType types.SemType
		if fnType != nil && i < len(fnType.Params) {
			paramType = fnType.Params[i].Type
		}
		if ref, ok := types.UnwrapType(argType).(*types.ReferenceType); ok {
			paramIsRef := false
			if paramType != nil {
				if _, ok := types.UnwrapType(paramType).(*types.ReferenceType); ok {
					paramIsRef = true
				}
			}
			if paramType == nil || !paramIsRef {
				val = b.emitLoad(val, ref.Inner, loc)
				argType = ref.Inner
			}
		}
		if !skipInterfaceBoxing && paramType != nil && interfaceTypeOf(paramType) != nil {
			val = b.boxInterfaceValue(val, argType, paramType, loc)
			if val == mir.InvalidValue {
				return nil
			}
		}
		out = append(out, val)
	}
	return out
}

func (b *functionBuilder) isStdIoPrintTarget(target string) bool {
	if target == "" {
		return false
	}
	if !strings.Contains(target, "::") {
		if target != "Print" && target != "Println" {
			return false
		}
		return b.gen != nil && b.gen.mod != nil && b.gen.mod.ImportPath == "std/io"
	}

	parts := strings.Split(target, "::")
	if len(parts) < 2 {
		return false
	}
	funcName := parts[len(parts)-1]
	if funcName != "Print" && funcName != "Println" {
		return false
	}
	moduleAlias := strings.Join(parts[:len(parts)-1], "::")
	if moduleAlias == "" {
		return false
	}

	if b.gen == nil || b.gen.mod == nil || b.gen.mod.ImportAliasMap == nil {
		return false
	}
	return b.gen.mod.ImportAliasMap[moduleAlias] == "std/io"
}

func (b *functionBuilder) lowerInterfaceMethodCall(selector *hir.SelectorExpr, call *hir.CallExpr, fnType *types.FunctionType) mir.ValueID {
	if selector == nil || selector.Field == nil || call == nil {
		return mir.InvalidValue
	}
	iface := interfaceTypeOf(b.exprType(selector.X))
	if iface == nil || len(iface.Methods) == 0 {
		b.reportUnsupported("interface call", selector.Loc())
		return mir.InvalidValue
	}

	methodIndex := -1
	for i, method := range iface.Methods {
		if method.Name == selector.Field.Name {
			methodIndex = i
			break
		}
	}
	if methodIndex < 0 {
		b.reportUnsupported("interface method", selector.Loc())
		return mir.InvalidValue
	}

	ifaceVal := b.lowerExpr(selector.X)
	if ifaceVal == mir.InvalidValue {
		return mir.InvalidValue
	}

	ptrType := types.NewReference(types.TypeU8)
	dataSlot := b.emitPtrAdd(ifaceVal, 0, ptrType, call.Location)
	dataPtr := b.emitLoad(dataSlot, ptrType, call.Location)
	vtSlot := b.emitPtrAdd(ifaceVal, b.gen.layout.PointerSize, ptrType, call.Location)
	vtPtr := b.emitLoad(vtSlot, ptrType, call.Location)

	offset := methodIndex * b.gen.layout.PointerSize
	methodSlot := b.emitPtrAdd(vtPtr, offset, ptrType, call.Location)

	args := b.lowerCallArgs(call.Args, fnType, call.Location, false)
	if args == nil {
		return mir.InvalidValue
	}
	allArgs := append([]mir.ValueID{dataPtr}, args...)
	return b.emitCallIndirect(methodSlot, allArgs, call)
}

func (b *functionBuilder) boxInterfaceValue(value mir.ValueID, valueType, ifaceType types.SemType, loc source.Location) mir.ValueID {
	if value == mir.InvalidValue {
		return mir.InvalidValue
	}
	if valueType == nil || ifaceType == nil {
		return value
	}
	if interfaceTypeOf(valueType) != nil {
		return value
	}

	dataPtr := value
	if _, ok := types.UnwrapType(valueType).(*types.ReferenceType); !ok {
		size := 0
		if b.gen != nil && b.gen.layout != nil {
			size = b.gen.layout.SizeOf(valueType)
		}
		if size <= 0 {
			b.reportUnsupported("interface box size", &loc)
			return mir.InvalidValue
		}
		sizeVal := b.emitConst(types.TypeU64, strconv.Itoa(size), loc)
		box := b.gen.nextValueID()
		b.emitInstr(&mir.Call{
			Result:   box,
			Target:   "ferret_alloc",
			Args:     []mir.ValueID{sizeVal},
			Type:     types.NewReference(valueType),
			Location: loc,
		})
		b.ptrElem[box] = valueType
		b.emitStore(box, value, loc)
		dataPtr = box
	}

	ifaceAddr := b.emitAlloca(ifaceType, loc)
	ptrType := types.NewReference(types.TypeU8)

	// Store data pointer at offset 0
	dataSlot := b.emitPtrAdd(ifaceAddr, 0, ptrType, loc)
	b.emitStore(dataSlot, dataPtr, loc)

	if isEmptyInterface(ifaceType) {
		// For empty interface{}, store type ID string pointer at offset PointerSize
		typeID := typeIDString(valueType)
		typeIDGlobal := b.gen.ensureTypeIDGlobal(typeID)
		typeIDPtr := b.emitConst(ptrType, "$"+typeIDGlobal, loc)
		typeIDSlot := b.emitPtrAdd(ifaceAddr, b.gen.layout.PointerSize, ptrType, loc)
		b.emitStore(typeIDSlot, typeIDPtr, loc)
	} else {
		// For interfaces with methods, store vtable pointer at offset PointerSize
		vtableName, ok := b.gen.ensureInterfaceVTable(valueType, ifaceType, loc)
		if !ok || vtableName == "" {
			return mir.InvalidValue
		}
		vtablePtr := b.emitConst(ptrType, "$"+vtableName, loc)
		vtSlot := b.emitPtrAdd(ifaceAddr, b.gen.layout.PointerSize, ptrType, loc)
		b.emitStore(vtSlot, vtablePtr, loc)
	}

	return ifaceAddr
}

// boxUnionValue creates a tagged union value from a variant value.
// Union layout: [4-byte discriminant/tag][variant data]
func (b *functionBuilder) boxUnionValue(value mir.ValueID, valueType types.SemType, unionType *types.UnionType, loc source.Location) mir.ValueID {
	if value == mir.InvalidValue || unionType == nil {
		return mir.InvalidValue
	}

	// Find which variant this value matches
	variantIndex := -1
	for i, variant := range unionType.Variants {
		if valueType.Equals(variant) {
			variantIndex = i
			break
		}
	}

	if variantIndex < 0 {
		// Type checker should have caught this
		b.reportUnsupported("union variant mismatch", &loc)
		return mir.InvalidValue
	}

	// Allocate space for the union (tag + max variant size)
	unionAddr := b.emitAlloca(unionType, loc)

	// Store the discriminant (tag) at offset 0
	tagSlot := b.emitPtrAdd(unionAddr, 0, types.TypeI32, loc)
	tagValue := b.emitConst(types.TypeI32, strconv.Itoa(variantIndex), loc)
	b.emitStore(tagSlot, tagValue, loc)

	// Store the actual value at offset 4 (after the tag)
	dataSlot := b.emitPtrAdd(unionAddr, 4, valueType, loc)
	b.emitStore(dataSlot, value, loc)

	return unionAddr
}

// emitTypeCheck generates code for the 'is' operator to check type at runtime.
// Handles both union types and interface{} types.
// Returns a boolean indicating if the value is of the expected type.
func (b *functionBuilder) emitTypeCheck(expr *hir.BinaryExpr) mir.ValueID {
	if expr == nil || expr.TargetType == nil {
		// Fallback: return false if no target type
		return b.emitConst(types.TypeBool, "0", expr.Location)
	}

	// Lower the value (LHS)
	val := b.lowerExpr(expr.X)
	if val == mir.InvalidValue {
		return mir.InvalidValue
	}

	// For 'is' checks, we need the ORIGINAL type of the variable, not the narrowed type.
	// If the LHS is an identifier with a symbol, check if the symbol has an original type.
	valType := b.exprType(expr.X)

	// Try to get the original (pre-narrowing) type from the symbol
	if ident, ok := expr.X.(*hir.Ident); ok && ident.Symbol != nil {
		if ident.Symbol.OriginalType != nil {
			valType = ident.Symbol.OriginalType
		} else if ident.Symbol.Type != nil {
			// Symbol type might be more accurate than expression type
			valType = ident.Symbol.Type
		}
	}

	val, valType = b.derefValueIfNeeded(val, valType, expr.Location)

	unwrappedType := types.UnwrapType(valType)

	// Check if it's a union type
	if unionTypeStruct, ok := unwrappedType.(*types.UnionType); ok {
		return b.emitUnionTypeCheckImpl(val, unionTypeStruct, expr.TargetType, expr.Location)
	}

	// Check if it's an interface{} type
	if isEmptyInterface(valType) {
		return b.emitInterfaceTypeCheck(val, expr.TargetType, expr.Location)
	}

	// Not a union or interface - should have been caught by type checker
	b.reportUnsupported("'is' check on non-union/non-interface type", expr.Loc())
	return mir.InvalidValue
}

// emitUnionTypeCheckImpl implements union variant checking.
func (b *functionBuilder) emitUnionTypeCheckImpl(unionVal mir.ValueID, unionType *types.UnionType, targetType types.SemType, loc source.Location) mir.ValueID {
	// Find the variant index for the target type
	variantIndex := -1
	for i, variant := range unionType.Variants {
		if targetType.Equals(variant) {
			variantIndex = i
			break
		}
	}

	if variantIndex < 0 {
		// Target type is not a variant - should have been caught by type checker
		b.reportUnsupported("invalid union variant", &loc)
		return mir.InvalidValue
	}

	// Load the discriminant (tag) from the union at offset 0
	tagSlot := b.emitPtrAdd(unionVal, 0, types.TypeI32, loc)
	tagValue := b.emitLoad(tagSlot, types.TypeI32, loc)

	// Compare the tag with the expected variant index
	expectedTag := b.emitConst(types.TypeI32, strconv.Itoa(variantIndex), loc)
	result := b.gen.nextValueID()
	b.emitInstr(&mir.Binary{
		Result:   result,
		Op:       tokens.DOUBLE_EQUAL_TOKEN,
		Left:     tagValue,
		Right:    expectedTag,
		Type:     types.TypeBool,
		Location: loc,
	})

	return result
}

// emitInterfaceTypeCheck implements interface{} type checking.
// Compares the stored type ID string with the expected type ID.
func (b *functionBuilder) emitInterfaceTypeCheck(ifaceVal mir.ValueID, targetType types.SemType, loc source.Location) mir.ValueID {
	// Load the type ID pointer from the interface at offset PointerSize
	ptrType := types.NewReference(types.TypeU8)
	typeIDSlot := b.emitPtrAdd(ifaceVal, b.gen.layout.PointerSize, ptrType, loc)
	storedTypeIDPtr := b.emitLoad(typeIDSlot, ptrType, loc)

	// Get the expected type ID
	expectedTypeID := typeIDString(targetType)
	expectedTypeIDGlobal := b.gen.ensureTypeIDGlobal(expectedTypeID)
	expectedTypeIDPtr := b.emitConst(ptrType, "$"+expectedTypeIDGlobal, loc)

	// Compare the two type ID string pointers using strcmp
	result := b.gen.nextValueID()
	b.emitInstr(&mir.Call{
		Result:   result,
		Target:   "ferret_strcmp",
		Args:     []mir.ValueID{storedTypeIDPtr, expectedTypeIDPtr},
		Type:     types.TypeI32,
		Location: loc,
	})

	// strcmp returns 0 if equal, so we need to compare result == 0
	zero := b.emitConst(types.TypeI32, "0", loc)
	isEqual := b.gen.nextValueID()
	b.emitInstr(&mir.Binary{
		Result:   isEqual,
		Op:       tokens.DOUBLE_EQUAL_TOKEN,
		Left:     result,
		Right:    zero,
		Type:     types.TypeBool,
		Location: loc,
	})

	return isEqual
}

func builtinNameFromIdent(ident *hir.Ident) (string, bool) {
	if ident == nil || ident.Symbol == nil || !ident.Symbol.IsBuiltin {
		return "", false
	}
	if ident.Symbol.BuiltinName != "" {
		return ident.Symbol.BuiltinName, true
	}
	return ident.Name, true
}

func (b *functionBuilder) lowerBuiltinLenCall(expr *hir.CallExpr) mir.ValueID {
	if expr == nil || len(expr.Args) != 1 {
		b.reportUnsupported("len argument count", expr.Loc())
		return mir.InvalidValue
	}

	argExpr := expr.Args[0]
	argVal := b.lowerExpr(argExpr)
	if argVal == mir.InvalidValue {
		return mir.InvalidValue
	}
	argVal, _ = b.derefValueIfNeeded(argVal, b.exprType(argExpr), expr.Location)

	if arrType := b.arrayTypeOf(argExpr); arrType != nil {
		if arrType.Length >= 0 {
			return b.emitConst(types.TypeI32, strconv.Itoa(arrType.Length), expr.Location)
		}
		return b.emitArrayLen(argVal, expr.Location)
	}

	if b.mapTypeOf(argExpr) != nil {
		result := mir.InvalidValue
		if expr.Type != nil && !expr.Type.Equals(types.TypeVoid) {
			result = b.gen.nextValueID()
		}
		b.emitInstr(&mir.Call{
			Result:   result,
			Target:   "ferret_map_size",
			Args:     []mir.ValueID{argVal},
			Type:     types.TypeI32,
			Location: expr.Location,
		})
		return result
	}

	if b.isStringType(argExpr) {
		result := b.gen.nextValueID()
		b.emitInstr(&mir.Call{
			Result:   result,
			Target:   "ferret_string_len",
			Args:     []mir.ValueID{argVal},
			Type:     types.TypeI32,
			Location: expr.Location,
		})
		return result
	}

	b.reportUnsupported("len target", expr.Loc())
	return mir.InvalidValue
}

func (b *functionBuilder) lowerBuiltinAppendCall(expr *hir.CallExpr) mir.ValueID {
	if expr == nil || len(expr.Args) != 2 {
		b.reportUnsupported("append argument count", expr.Loc())
		return mir.InvalidValue
	}

	arrExpr := expr.Args[0]
	valExpr := expr.Args[1]
	arrVal := b.lowerExpr(arrExpr)
	if arrVal == mir.InvalidValue {
		return mir.InvalidValue
	}
	arrVal, _ = b.derefValueIfNeeded(arrVal, b.exprType(arrExpr), expr.Location)
	value := b.lowerExpr(valExpr)
	if value == mir.InvalidValue {
		return mir.InvalidValue
	}

	arrType := b.arrayTypeOf(arrExpr)
	if arrType == nil || arrType.Length >= 0 {
		b.reportUnsupported("append target", expr.Loc())
		return mir.InvalidValue
	}

	temp := b.emitAlloca(arrType.Element, expr.Location)
	b.emitStore(temp, value, expr.Location)

	result := mir.InvalidValue
	if expr.Type != nil && !expr.Type.Equals(types.TypeVoid) {
		result = b.gen.nextValueID()
	}
	b.emitInstr(&mir.Call{
		Result:   result,
		Target:   "ferret_array_append",
		Args:     []mir.ValueID{arrVal, temp},
		Type:     types.TypeBool,
		Location: expr.Location,
	})
	return result
}

func (b *functionBuilder) lowerBuiltinPanicCall(expr *hir.CallExpr) mir.ValueID {
	if expr == nil || len(expr.Args) != 1 {
		b.reportUnsupported("panic argument count", expr.Loc())
		return mir.InvalidValue
	}

	msgExpr := expr.Args[0]
	msgVal := b.lowerExpr(msgExpr)
	if msgVal == mir.InvalidValue {
		return mir.InvalidValue
	}

	// Call ferret_global_panic with the message string
	b.emitInstr(&mir.Call{
		Result:   mir.InvalidValue,
		Target:   "ferret_global_panic",
		Args:     []mir.ValueID{msgVal},
		Type:     types.TypeVoid,
		Location: expr.Location,
	})
	return mir.InvalidValue // panic never returns
}

func (b *functionBuilder) emitCall(target string, args []mir.ValueID, expr *hir.CallExpr) mir.ValueID {
	retType := expr.Type
	if ref, ok := types.UnwrapType(retType).(*types.ReferenceType); ok {
		out := b.emitAlloca(ref.Inner, expr.Location)
		callArgs := append([]mir.ValueID{out}, args...)
		result := b.gen.nextValueID()
		b.emitInstr(&mir.Call{
			Result:   result,
			Target:   target,
			Args:     callArgs,
			Type:     retType,
			Location: expr.Location,
		})
		if expr.Catch != nil {
			b.reportUnsupported("call-site catch should be lowered in HIR", &expr.Location)
		}
		return result
	}
	if needsByRefType(retType) {
		out := b.emitAlloca(retType, expr.Location)
		callArgs := append([]mir.ValueID{out}, args...)
		b.emitInstr(&mir.Call{
			Result:   mir.InvalidValue,
			Target:   target,
			Args:     callArgs,
			Type:     types.TypeVoid,
			Location: expr.Location,
		})
		if expr.Catch != nil {
			b.reportUnsupported("call-site catch should be lowered in HIR", &expr.Location)
		}
		return out
	}

	result := mir.InvalidValue
	if retType != nil && !retType.Equals(types.TypeVoid) {
		result = b.gen.nextValueID()
	}

	b.emitInstr(&mir.Call{
		Result:   result,
		Target:   target,
		Args:     args,
		Type:     retType,
		Location: expr.Location,
	})

	if expr.Catch != nil {
		b.reportUnsupported("call-site catch should be lowered in HIR", &expr.Location)
	}

	return result
}

func (b *functionBuilder) emitCallIndirect(callee mir.ValueID, args []mir.ValueID, expr *hir.CallExpr) mir.ValueID {
	retType := expr.Type
	if ref, ok := types.UnwrapType(retType).(*types.ReferenceType); ok {
		out := b.emitAlloca(ref.Inner, expr.Location)
		callArgs := append([]mir.ValueID{out}, args...)
		result := b.gen.nextValueID()
		b.emitInstr(&mir.CallIndirect{
			Result:   result,
			Callee:   callee,
			Args:     callArgs,
			Type:     retType,
			Location: expr.Location,
		})
		if expr.Catch != nil {
			b.reportUnsupported("call-site catch should be lowered in HIR", &expr.Location)
		}
		return result
	}
	if needsByRefType(retType) {
		out := b.emitAlloca(retType, expr.Location)
		callArgs := append([]mir.ValueID{out}, args...)
		b.emitInstr(&mir.CallIndirect{
			Result:   mir.InvalidValue,
			Callee:   callee,
			Args:     callArgs,
			Type:     types.TypeVoid,
			Location: expr.Location,
		})
		if expr.Catch != nil {
			b.reportUnsupported("call-site catch should be lowered in HIR", &expr.Location)
		}
		return out
	}

	result := mir.InvalidValue
	if retType != nil && !retType.Equals(types.TypeVoid) {
		result = b.gen.nextValueID()
	}
	b.emitInstr(&mir.CallIndirect{
		Result:   result,
		Callee:   callee,
		Args:     args,
		Type:     retType,
		Location: expr.Location,
	})

	if expr.Catch != nil {
		b.reportUnsupported("call-site catch should be lowered in HIR", &expr.Location)
	}

	return result
}

func (b *functionBuilder) lowerQualifiedValue(expr *hir.ScopeResolutionExpr) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}

	name, ok := b.qualifiedName(expr)
	if !ok {
		b.reportUnsupported("qualified value", &expr.Location)
		return mir.InvalidValue
	}

	if value, ok := b.lookupQualifiedConst(name); ok {
		if isLargePrimitiveType(expr.Type) {
			return b.emitLargeConst(expr.Type, value, expr.Location)
		}
		return b.emitConst(expr.Type, value, expr.Location)
	}
	if _, ok := types.UnwrapType(expr.Type).(*types.FunctionType); ok {
		return b.makeFuncValue(name, expr.Type, expr.Location)
	}

	b.reportUnsupported(fmt.Sprintf("qualified value %s", name), &expr.Location)
	return mir.InvalidValue
}

func (b *functionBuilder) lowerSelector(expr *hir.SelectorExpr) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}

	addr := b.lowerFieldAddr(expr)
	if addr == mir.InvalidValue {
		return mir.InvalidValue
	}

	return b.emitLoad(addr, expr.Type, expr.Location)
}

func (b *functionBuilder) lowerLValue(expr hir.Expr) mir.ValueID {
	switch e := expr.(type) {
	case *hir.Ident:
		return b.addrForIdent(e)
	case *hir.SelectorExpr:
		return b.lowerFieldAddr(e)
	case *hir.IndexExpr:
		return b.lowerIndexAddr(e)
	default:
		b.reportUnsupported("lvalue", expr.Loc())
		return mir.InvalidValue
	}
}

func (b *functionBuilder) loadIdent(ident *hir.Ident) mir.ValueID {
	if ident == nil {
		return mir.InvalidValue
	}

	// Special handling for builtin constants: true, false, none
	if ident.Name == "true" {
		return b.emitConst(types.TypeBool, "1", ident.Location)
	}
	if ident.Name == "false" {
		return b.emitConst(types.TypeBool, "0", ident.Location)
	}
	if ident.Name == "none" {
		id := b.gen.nextValueID()
		optType := &types.OptionalType{Inner: types.TypeNone}
		b.emitInstr(&mir.OptionalNone{Result: id, Type: optType, Location: ident.Location})
		return id
	}

	if ident.Symbol != nil && ident.Symbol.Kind == symbols.SymbolFunction {
		return b.makeFuncValue(ident.Name, ident.Type, ident.Location)
	}

	// Check for narrowed union access: if ident.Type differs from the storage type (Symbol.Type)
	// and the storage type is a union, we need to extract the variant
	if ident.Symbol != nil && ident.Type != nil && ident.Symbol.Type != nil {
		storageType := types.UnwrapType(ident.Symbol.Type)
		accessType := types.UnwrapType(ident.Type)

		if unionType, ok := storageType.(*types.UnionType); ok {
			// Storage is a union but we're accessing a narrowed variant type
			if !accessType.Equals(storageType) {
				// Find the variant index
				variantIndex := -1
				for i, variant := range unionType.Variants {
					if accessType.Equals(variant) {
						variantIndex = i
						break
					}
				}

				if variantIndex >= 0 {
					// Load the union pointer, then extract the variant
					if addr, ok := b.slots[ident.Symbol]; ok {
						unionVal := b.emitLoad(addr, ident.Symbol.Type, ident.Location)

						// Emit union extract - get data at offset 4 with the narrowed type
						result := b.gen.nextValueID()
						b.emitInstr(&mir.UnionExtract{
							Result:       result,
							Value:        unionVal,
							VariantIndex: variantIndex,
							UnionType:    storageType,
							Type:         accessType,
							Location:     ident.Location,
						})
						return result
					}
				}
			}
		}
	}

	if ident.Symbol != nil && b.captures != nil {
		if _, ok := b.captures[ident.Symbol]; ok {
			if addr := b.addrForIdent(ident); addr != mir.InvalidValue {
				return b.emitLoad(addr, ident.Type, ident.Location)
			}
		}
	}

	if ident.Symbol != nil {
		if addr, ok := b.slots[ident.Symbol]; ok {
			return b.emitLoad(addr, ident.Type, ident.Location)
		}
	}

	if addr, ok := b.tempSlots[ident]; ok {
		return b.emitLoad(addr, ident.Type, ident.Location)
	}

	if ident.Symbol != nil && (ident.Symbol.Kind == symbols.SymbolParameter || ident.Symbol.Kind == symbols.SymbolReceiver) {
		if val, ok := b.paramsByName[ident.Name]; ok {
			return val
		}
	}

	if val, ok := b.paramsByName[ident.Name]; ok {
		return val
	}

	if ident.Name != "" {
		b.reportUnsupported(fmt.Sprintf("identifier %s", ident.Name), &ident.Location)
	} else {
		b.reportUnsupported("identifier", &ident.Location)
	}
	return mir.InvalidValue
}

func (b *functionBuilder) addrForIdent(ident *hir.Ident) mir.ValueID {
	if ident == nil {
		return mir.InvalidValue
	}

	if ident.Symbol != nil {
		if b.captures != nil {
			if cap, ok := b.captures[ident.Symbol]; ok && b.closureEnv != mir.InvalidValue {
				fieldAddr := b.emitPtrAdd(b.closureEnv, cap.offset, cap.typ, ident.Location)
				return b.emitLoad(fieldAddr, cap.typ, ident.Location)
			}
		}
		if addr, ok := b.slots[ident.Symbol]; ok {
			return addr
		}
		if ident.Symbol.Kind == symbols.SymbolParameter || ident.Symbol.Kind == symbols.SymbolReceiver {
			if val, ok := b.paramsByName[ident.Name]; ok {
				addr := b.emitAllocaInEntry(ident.Type, ident.Location)
				b.emitStoreInEntry(addr, val, ident.Location)
				b.slots[ident.Symbol] = addr
				return addr
			}
		}
	}

	if addr, ok := b.tempSlots[ident]; ok {
		return addr
	}

	return mir.InvalidValue
}

func (b *functionBuilder) lowerFieldAddr(expr *hir.SelectorExpr) mir.ValueID {
	if expr == nil || expr.Field == nil {
		return mir.InvalidValue
	}

	baseType := b.exprType(expr.X)
	if baseType == nil {
		b.reportUnsupported("selector base", expr.Loc())
		return mir.InvalidValue
	}

	baseType = types.UnwrapType(baseType)
	basePtr := mir.InvalidValue
	if isAddressableExpr(expr.X) {
		baseAddr := b.lowerLValue(expr.X)
		if baseAddr == mir.InvalidValue {
			return mir.InvalidValue
		}
		if ref, ok := baseType.(*types.ReferenceType); ok {
			basePtr = b.emitLoad(baseAddr, baseType, expr.Location)
			baseType = types.UnwrapType(ref.Inner)
			b.ptrElem[basePtr] = ref.Inner
		} else {
			basePtr = baseAddr
		}
	} else {
		basePtr = b.lowerExpr(expr.X)
		if basePtr == mir.InvalidValue {
			return mir.InvalidValue
		}
		if ref, ok := baseType.(*types.ReferenceType); ok {
			baseType = types.UnwrapType(ref.Inner)
			b.ptrElem[basePtr] = ref.Inner
		}
	}

	structType, ok := baseType.(*types.StructType)
	if !ok {
		b.reportUnsupported("selector struct", expr.Loc())
		return mir.InvalidValue
	}

	layout := b.gen.layout.StructLayout(structType)
	offset, ok := layout.FieldOffset(expr.Field.Name)
	if !ok {
		b.reportUnsupported("selector field", expr.Loc())
		return mir.InvalidValue
	}

	return b.emitPtrAdd(basePtr, offset, expr.Type, expr.Location)
}

func (b *functionBuilder) lowerCompositeLit(expr *hir.CompositeLit) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}
	if expr.Type == nil {
		b.reportUnsupported("composite literal type", expr.Loc())
		return mir.InvalidValue
	}
	switch typ := types.UnwrapType(expr.Type).(type) {
	case *types.StructType:
		out := b.emitAlloca(expr.Type, expr.Location)
		b.lowerStructLiteralInto(out, typ, expr)
		return out
	case *types.ArrayType:
		if typ.Length < 0 {
			return b.lowerDynamicArrayLiteral(typ, expr)
		}
		out := b.emitAlloca(expr.Type, expr.Location)
		b.lowerArrayLiteralInto(out, typ, expr)
		return out
	case *types.MapType:
		return b.lowerMapLiteral(typ, expr)
	default:
		b.reportUnsupported("composite literal", expr.Loc())
		return mir.InvalidValue
	}
}

func (b *functionBuilder) lowerMapLiteral(mapType *types.MapType, lit *hir.CompositeLit) mir.ValueID {
	if mapType == nil || lit == nil {
		return mir.InvalidValue
	}
	if mapType.Key == nil || mapType.Value == nil {
		b.reportUnsupported("map literal type", lit.Loc())
		return mir.InvalidValue
	}

	keySize := b.gen.layout.SizeOf(mapType.Key)
	valSize := b.gen.layout.SizeOf(mapType.Value)
	if keySize <= 0 || valSize <= 0 {
		b.reportUnsupported("map literal element size", lit.Loc())
		return mir.InvalidValue
	}

	sizeType := types.TypeI64
	if b.gen.layout.PointerSize <= 4 {
		sizeType = types.TypeI32
	}

	keySizeVal := b.emitConst(sizeType, strconv.Itoa(keySize), lit.Location)
	valSizeVal := b.emitConst(sizeType, strconv.Itoa(valSize), lit.Location)

	fns := b.mapRuntimeFns(mapType.Key)
	if len(lit.Elts) == 0 {
		result := b.gen.nextValueID()
		b.emitInstr(&mir.Call{
			Result:   result,
			Target:   fns.newFn,
			Args:     []mir.ValueID{keySizeVal, valSizeVal},
			Type:     lit.Type,
			Location: lit.Location,
		})
		return result
	}

	keyArrType := types.NewArray(mapType.Key, len(lit.Elts))
	valArrType := types.NewArray(mapType.Value, len(lit.Elts))
	keysAddr := b.emitAlloca(keyArrType, lit.Location)
	valsAddr := b.emitAlloca(valArrType, lit.Location)

	for i, elt := range lit.Elts {
		kv, ok := elt.(*hir.KeyValueExpr)
		if !ok {
			b.reportUnsupported("map literal element", elt.Loc())
			return mir.InvalidValue
		}
		if kv.Key == nil || kv.Value == nil {
			b.reportUnsupported("map literal key/value", elt.Loc())
			return mir.InvalidValue
		}
		keyVal := b.lowerExpr(kv.Key)
		if keyVal == mir.InvalidValue {
			return mir.InvalidValue
		}
		valueVal := b.lowerExpr(kv.Value)
		if valueVal == mir.InvalidValue {
			return mir.InvalidValue
		}

		keyVal = b.castValue(keyVal, b.exprType(kv.Key), mapType.Key, kv.Location)
		valueVal = b.castValue(valueVal, b.exprType(kv.Value), mapType.Value, kv.Location)

		keyOffset := i * keySize
		valOffset := i * valSize
		keySlot := b.emitPtrAdd(keysAddr, keyOffset, mapType.Key, kv.Location)
		valSlot := b.emitPtrAdd(valsAddr, valOffset, mapType.Value, kv.Location)
		b.emitStore(keySlot, keyVal, kv.Location)
		b.emitStore(valSlot, valueVal, kv.Location)
	}

	countVal := b.emitConst(sizeType, strconv.Itoa(len(lit.Elts)), lit.Location)
	result := b.gen.nextValueID()
	b.emitInstr(&mir.Call{
		Result:   result,
		Target:   fns.fromPairsFn,
		Args:     []mir.ValueID{keySizeVal, valSizeVal, keysAddr, valsAddr, countVal},
		Type:     lit.Type,
		Location: lit.Location,
	})
	return result
}

func (b *functionBuilder) lowerDynamicArrayLiteral(arrType *types.ArrayType, lit *hir.CompositeLit) mir.ValueID {
	if arrType == nil || lit == nil {
		return mir.InvalidValue
	}

	elemSize := b.gen.layout.SizeOf(arrType.Element)
	if elemSize <= 0 {
		b.reportUnsupported("array element size", lit.Loc())
		return mir.InvalidValue
	}

	sizeType := types.TypeI64
	if b.gen.layout.PointerSize <= 4 {
		sizeType = types.TypeI32
	}

	sizeVal := b.emitConst(sizeType, strconv.Itoa(elemSize), lit.Location)
	capVal := b.emitConst(types.TypeI32, strconv.Itoa(len(lit.Elts)), lit.Location)

	arr := b.gen.nextValueID()
	b.emitInstr(&mir.Call{
		Result:   arr,
		Target:   "ferret_array_new",
		Args:     []mir.ValueID{sizeVal, capVal},
		Type:     lit.Type,
		Location: lit.Location,
	})

	for _, elt := range lit.Elts {
		if _, ok := elt.(*hir.KeyValueExpr); ok {
			b.reportUnsupported("array key/value literal", elt.Loc())
			return mir.InvalidValue
		}
		value := b.lowerExpr(elt)
		if value == mir.InvalidValue {
			return mir.InvalidValue
		}
		// Box into union if element type is union
		eltType := b.exprType(elt)
		value = b.coerceValueForAssign(value, eltType, arrType.Element, lit.Location)

		// ferret_array_append expects a pointer to the element
		// For unions, coerceValueForAssign already returns a pointer
		// For primitives, we need to allocate storage and store the value
		valuePtr := value
		if _, isUnion := types.UnwrapType(arrType.Element).(*types.UnionType); !isUnion {
			// Allocate storage for the element
			temp := b.emitAlloca(arrType.Element, lit.Location)
			b.emitStore(temp, value, lit.Location)
			valuePtr = temp
		}

		b.emitInstr(&mir.Call{
			Result:   mir.InvalidValue,
			Target:   "ferret_array_append",
			Args:     []mir.ValueID{arr, valuePtr},
			Type:     types.TypeBool,
			Location: lit.Location,
		})
	}

	return arr
}

func (b *functionBuilder) lowerStructLiteralInto(addr mir.ValueID, structType *types.StructType, lit *hir.CompositeLit) {
	if structType == nil || lit == nil {
		return
	}

	layout := b.gen.layout.StructLayout(structType)
	for _, elt := range lit.Elts {
		kv, ok := elt.(*hir.KeyValueExpr)
		if !ok || kv == nil {
			b.reportUnsupported("struct literal element", elt.Loc())
			return
		}
		keyIdent, ok := kv.Key.(*hir.Ident)
		if !ok || keyIdent == nil {
			b.reportUnsupported("struct literal key", kv.Loc())
			return
		}
		fieldType, ok := structFieldType(structType, keyIdent.Name)
		if !ok {
			b.reportUnsupported("struct literal field", kv.Loc())
			return
		}
		offset, ok := layout.FieldOffset(keyIdent.Name)
		if !ok {
			b.reportUnsupported("struct literal field offset", kv.Loc())
			return
		}
		value := b.lowerExpr(kv.Value)
		if value == mir.InvalidValue {
			return
		}
		fieldAddr := b.emitPtrAdd(addr, offset, fieldType, lit.Location)
		b.emitStore(fieldAddr, value, lit.Location)
	}
}

func structFieldType(structType *types.StructType, name string) (types.SemType, bool) {
	if structType == nil {
		return nil, false
	}
	for _, field := range structType.Fields {
		if field.Name == name {
			return field.Type, true
		}
	}
	return nil, false
}

func (b *functionBuilder) lowerArrayLiteralInto(addr mir.ValueID, arrType *types.ArrayType, lit *hir.CompositeLit) {
	if arrType == nil || lit == nil {
		return
	}

	elemSize := b.gen.layout.SizeOf(arrType.Element)
	if elemSize <= 0 {
		b.reportUnsupported("array element size", lit.Loc())
		return
	}

	for i, elt := range lit.Elts {
		if i >= arrType.Length {
			break
		}
		if _, ok := elt.(*hir.KeyValueExpr); ok {
			b.reportUnsupported("array key/value literal", elt.Loc())
			return
		}
		value := b.lowerExpr(elt)
		if value == mir.InvalidValue {
			return
		}
		// Box into union if element type is union
		eltType := b.exprType(elt)
		value = b.coerceValueForAssign(value, eltType, arrType.Element, lit.Location)
		offset := i * elemSize
		elemAddr := b.emitPtrAdd(addr, offset, arrType.Element, lit.Location)
		b.emitStore(elemAddr, value, lit.Location)
	}
}

func (b *functionBuilder) lowerIndexValue(expr *hir.IndexExpr) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}

	if b.isStringType(expr.X) {
		return b.lowerStringIndexValue(expr)
	}

	if mapType := b.mapTypeOf(expr.X); mapType != nil {
		return b.lowerMapIndexValue(expr, mapType)
	}

	// Check if it's an array literal first (before arrayTypeOf, which might fail for untyped literals)
	if lit, ok := expr.X.(*hir.CompositeLit); ok {
		// Try to get array type from the literal's type
		var arrType *types.ArrayType
		if lit.Type != nil {
			baseType := types.UnwrapType(lit.Type)
			if ref, ok := baseType.(*types.ReferenceType); ok {
				baseType = types.UnwrapType(ref.Inner)
			}
			if arr, ok := baseType.(*types.ArrayType); ok {
				arrType = arr
			}
		}
		// If type not set, infer from elements (dynamic array)
		if arrType == nil && len(lit.Elts) > 0 {
			// Infer as dynamic array
			elemType := b.exprType(lit.Elts[0])
			if elemType != nil {
				arrType = types.NewArray(elemType, -1)
			}
		}

		if arrType != nil {
			// Evaluate index as constant if possible (works for both literals and constant variables)
			var index int
			var indexOk bool
			if arrType.Length >= 0 {
				// Fixed array - use constArrayIndex
				index, indexOk = b.constArrayIndex(expr, arrType)
			} else {
				// Dynamic array literal - evaluate index directly (supports constant variables too)
				if b.gen != nil && b.gen.ctx != nil && b.gen.mod != nil {
					val := consteval.EvaluateHIRExpr(b.gen.ctx, b.gen.mod, expr.Index)
					if val != nil {
						if idx, ok := val.AsInt64(); ok {
							// Handle negative indices
							if idx < 0 {
								idx = int64(len(lit.Elts)) + idx
							}
							if idx >= 0 && idx < int64(len(lit.Elts)) {
								index = int(idx)
								indexOk = true
							}
						}
					}
				}
			}

			if indexOk && index >= 0 && index < len(lit.Elts) {
				// Constant index within bounds - just extract the element directly
				elt := lit.Elts[index]
				return b.lowerExpr(elt)
			}

			// Non-constant index or out of bounds - need to materialize array
			// Get the index value
			indexVal := b.lowerExpr(expr.Index)
			if indexVal == mir.InvalidValue {
				return mir.InvalidValue
			}

			// Non-constant index or out of bounds - need to materialize array
			// (bounds checking will catch out-of-bounds at compile time in HIR analysis)
			arrVal := b.lowerCompositeLit(lit)
			if arrVal == mir.InvalidValue {
				return mir.InvalidValue
			}

			indexVal = b.castValue(indexVal, b.exprType(expr.Index), types.TypeI32, expr.Location)

			// For fixed arrays, we still need runtime bounds checking
			if arrType.Length >= 0 {
				lenVal := b.emitConst(types.TypeI32, strconv.Itoa(arrType.Length), expr.Location)
				indexVal = b.emitBoundsCheckedIndex(indexVal, lenVal, types.TypeI32, expr.Location)
				if indexVal == mir.InvalidValue {
					return mir.InvalidValue
				}
			} else {
				// Dynamic array: bounds check using runtime length
				lenVal := b.emitArrayLen(arrVal, expr.Location)
				indexVal = b.emitBoundsCheckedIndex(indexVal, lenVal, types.TypeI32, expr.Location)
				if indexVal == mir.InvalidValue {
					return mir.InvalidValue
				}
			}

			// Use ArrayGet to get the element
			result := b.gen.nextValueID()
			b.emitInstr(&mir.ArrayGet{
				Result:   result,
				Array:    arrVal,
				Index:    indexVal,
				Type:     expr.Type,
				Location: expr.Location,
			})
			return result
		}
	}

	// Not a literal or not an array - try normal array handling
	arrType := b.arrayTypeOf(expr.X)
	if arrType != nil {
		// Not a literal - handle normally
		if arrType.Length < 0 {
			return b.lowerDynamicIndexValue(expr, arrType)
		}
	}

	addr := b.lowerIndexAddr(expr)
	if addr == mir.InvalidValue {
		return mir.InvalidValue
	}

	return b.emitLoad(addr, expr.Type, expr.Location)
}

func (b *functionBuilder) lowerIndexAddr(expr *hir.IndexExpr) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}

	arrType := b.arrayTypeOf(expr.X)
	if arrType == nil {
		b.reportUnsupported("index base", expr.Loc())
		return mir.InvalidValue
	}

	if arrType.Length < 0 {
		b.reportUnsupported("dynamic array address", expr.Loc())
		return mir.InvalidValue
	}

	baseType := b.exprType(expr.X)
	if baseType == nil {
		b.reportUnsupported("index base", expr.Loc())
		return mir.InvalidValue
	}

	baseType = types.UnwrapType(baseType)
	basePtr := mir.InvalidValue
	if isAddressableExpr(expr.X) {
		baseAddr := b.lowerLValue(expr.X)
		if baseAddr == mir.InvalidValue {
			return mir.InvalidValue
		}
		if ref, ok := baseType.(*types.ReferenceType); ok {
			basePtr = b.emitLoad(baseAddr, baseType, expr.Location)
			baseType = types.UnwrapType(ref.Inner)
			b.ptrElem[basePtr] = ref.Inner
		} else {
			basePtr = baseAddr
		}
	} else {
		if _, ok := baseType.(*types.ReferenceType); !ok && !needsByRefType(baseType) {
			b.reportUnsupported("index base", expr.Loc())
			return mir.InvalidValue
		}
		basePtr = b.lowerExpr(expr.X)
		if basePtr == mir.InvalidValue {
			return mir.InvalidValue
		}
		if ref, ok := baseType.(*types.ReferenceType); ok {
			baseType = types.UnwrapType(ref.Inner)
			b.ptrElem[basePtr] = ref.Inner
		}
	}

	elemSize := b.gen.layout.SizeOf(arrType.Element)
	if elemSize <= 0 {
		b.reportUnsupported("array element size", expr.Loc())
		return mir.InvalidValue
	}

	index, ok := b.constArrayIndex(expr, arrType)
	if !ok {
		if b.gen == nil || b.gen.ctx == nil || !b.gen.ctx.Diagnostics.HasErrors() {
			b.reportUnsupported("array index", expr.Loc())
		}
		return mir.InvalidValue
	}

	offset := index * elemSize
	return b.emitPtrAdd(basePtr, offset, arrType.Element, expr.Location)
}

func (b *functionBuilder) lowerIndexAssign(expr *hir.IndexExpr, rhs hir.Expr, op *tokens.Token, loc source.Location) {
	if expr == nil {
		return
	}

	if b.isStringType(expr.X) {
		b.reportUnsupported("string index assignment", &loc)
		return
	}

	if mapType := b.mapTypeOf(expr.X); mapType != nil {
		b.lowerMapIndexAssign(expr, rhs, op, loc, mapType)
		return
	}

	arrType := b.arrayTypeOf(expr.X)
	if arrType != nil {
		// Use ArraySet MIR instruction for both fixed and dynamic arrays
		// This ensures consistent bounds checking and panicking behavior
		arrVal := b.lowerExpr(expr.X)
		if arrVal == mir.InvalidValue {
			return
		}
		if arrType.Length < 0 {
			arrVal, _ = b.derefValueIfNeeded(arrVal, b.exprType(expr.X), loc)
		}

		indexVal := b.lowerExpr(expr.Index)
		if indexVal == mir.InvalidValue {
			return
		}

		indexVal = b.castValue(indexVal, b.exprType(expr.Index), types.TypeI32, loc)

		// For fixed arrays, we still need runtime bounds checking
		if arrType.Length >= 0 {
			lenVal := b.emitConst(types.TypeI32, strconv.Itoa(arrType.Length), loc)
			indexVal = b.emitBoundsCheckedIndex(indexVal, lenVal, types.TypeI32, loc)
			if indexVal == mir.InvalidValue {
				return
			}
		} else {
			// Dynamic array: bounds check using runtime length
			lenVal := b.emitArrayLen(arrVal, loc)
			indexVal = b.emitBoundsCheckedIndex(indexVal, lenVal, types.TypeI32, loc)
			if indexVal == mir.InvalidValue {
				return
			}
		}

		value := b.lowerExpr(rhs)
		if value == mir.InvalidValue {
			return
		}
		value = b.coerceValueForAssign(value, b.exprType(rhs), arrType.Element, loc)
		if value == mir.InvalidValue {
			return
		}

		// Handle compound assignment operators (e.g., +=, -=)
		if op != nil && op.Kind != tokens.EQUALS_TOKEN {
			cur := b.gen.nextValueID()
			b.emitInstr(&mir.ArrayGet{
				Result:   cur,
				Array:    arrVal,
				Index:    indexVal,
				Type:     arrType.Element,
				Location: loc,
			})
			opKind := assignTokenToBinary(op.Kind)
			if opKind == "" {
				b.reportUnsupported("assignment operator", &loc)
				return
			}
			value = b.emitBinary(opKind, cur, value, arrType.Element, loc)
		}

		// Emit ArraySet instruction
		b.emitInstr(&mir.ArraySet{
			Array:    arrVal,
			Index:    indexVal,
			Value:    value,
			Location: loc,
		})
		return
	}

	// Non-array indexing - use old code path for compatibility
	addr := b.lowerIndexAddr(expr)
	if addr == mir.InvalidValue {
		return
	}

	if ref, ok := types.UnwrapType(expr.Type).(*types.ReferenceType); ok {
		refPtr := b.emitLoad(addr, expr.Type, loc)
		if refPtr == mir.InvalidValue {
			return
		}
		if op != nil && op.Kind != tokens.EQUALS_TOKEN {
			cur := b.emitLoad(refPtr, ref.Inner, loc)
			rhsVal := b.lowerExpr(rhs)
			if cur == mir.InvalidValue || rhsVal == mir.InvalidValue {
				return
			}
			rhsVal = b.coerceValueForAssign(rhsVal, b.exprType(rhs), ref.Inner, loc)
			if rhsVal == mir.InvalidValue {
				return
			}
			opKind := assignTokenToBinary(op.Kind)
			if opKind == "" {
				b.reportUnsupported("assignment operator", &loc)
				return
			}
			res := b.emitBinary(opKind, cur, rhsVal, ref.Inner, loc)
			b.emitStore(refPtr, res, loc)
			return
		}

		rhsVal := b.lowerExpr(rhs)
		if rhsVal == mir.InvalidValue {
			return
		}
		rhsVal = b.coerceValueForAssign(rhsVal, b.exprType(rhs), ref.Inner, loc)
		if rhsVal == mir.InvalidValue {
			return
		}
		b.emitStore(refPtr, rhsVal, loc)
		return
	}

	if op != nil && op.Kind != tokens.EQUALS_TOKEN {
		cur := b.emitLoad(addr, expr.Type, loc)
		rhsVal := b.lowerExpr(rhs)
		if cur == mir.InvalidValue || rhsVal == mir.InvalidValue {
			return
		}
		rhsVal = b.coerceValueForAssign(rhsVal, b.exprType(rhs), expr.Type, loc)
		if rhsVal == mir.InvalidValue {
			return
		}
		opKind := assignTokenToBinary(op.Kind)
		if opKind == "" {
			b.reportUnsupported("assignment operator", &loc)
			return
		}
		res := b.emitBinary(opKind, cur, rhsVal, expr.Type, loc)
		b.emitStore(addr, res, loc)
		return
	}

	rhsVal := b.lowerExpr(rhs)
	if rhsVal == mir.InvalidValue {
		return
	}
	rhsVal = b.coerceValueForAssign(rhsVal, b.exprType(rhs), expr.Type, loc)
	if rhsVal == mir.InvalidValue {
		return
	}
	b.emitStore(addr, rhsVal, loc)
}

func (b *functionBuilder) lowerStringIndexValue(expr *hir.IndexExpr) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}
	base := b.lowerExpr(expr.X)
	if base == mir.InvalidValue {
		return mir.InvalidValue
	}
	base, _ = b.derefValueIfNeeded(base, b.exprType(expr.X), expr.Location)
	indexVal := b.lowerExpr(expr.Index)
	if indexVal == mir.InvalidValue {
		return mir.InvalidValue
	}

	indexVal = b.castValue(indexVal, b.exprType(expr.Index), types.TypeI32, expr.Location)
	lenVal := b.gen.nextValueID()
	b.emitInstr(&mir.Call{
		Result:   lenVal,
		Target:   "ferret_string_len",
		Args:     []mir.ValueID{base},
		Type:     types.TypeI32,
		Location: expr.Location,
	})
	indexVal = b.emitBoundsCheckedIndex(indexVal, lenVal, types.TypeI32, expr.Location)
	if indexVal == mir.InvalidValue {
		return mir.InvalidValue
	}

	elemPtr := b.emitPtrOffset(base, indexVal, types.TypeByte, expr.Location)
	return b.emitLoad(elemPtr, types.TypeByte, expr.Location)
}

func (b *functionBuilder) lowerDynamicIndexValue(expr *hir.IndexExpr, arrType *types.ArrayType) mir.ValueID {
	arrVal := b.lowerExpr(expr.X)
	if arrVal == mir.InvalidValue {
		return mir.InvalidValue
	}
	arrVal, _ = b.derefValueIfNeeded(arrVal, b.exprType(expr.X), expr.Location)

	indexVal := b.lowerExpr(expr.Index)
	if indexVal == mir.InvalidValue {
		return mir.InvalidValue
	}

	indexVal = b.castValue(indexVal, b.exprType(expr.Index), types.TypeI32, expr.Location)
	lenVal := b.emitArrayLen(arrVal, expr.Location)
	indexVal = b.emitBoundsCheckedIndex(indexVal, lenVal, types.TypeI32, expr.Location)
	if indexVal == mir.InvalidValue {
		return mir.InvalidValue
	}
	return b.emitDynamicArrayGet(arrVal, indexVal, arrType.Element, expr.Location)
}

func (b *functionBuilder) constArrayIndex(expr *hir.IndexExpr, arrType *types.ArrayType) (int, bool) {
	if expr == nil || arrType == nil || arrType.Length < 0 {
		return 0, false
	}
	if b.gen == nil || b.gen.ctx == nil || b.gen.mod == nil {
		return 0, false
	}

	val := consteval.EvaluateHIRExpr(b.gen.ctx, b.gen.mod, expr.Index)
	if val == nil {
		return 0, false
	}
	index, ok := val.AsInt64()
	if !ok {
		return 0, false
	}

	if index < 0 {
		index = int64(arrType.Length) + index
	}
	if index < 0 || index >= int64(arrType.Length) {
		return 0, false
	}
	return int(index), true
}

func (b *functionBuilder) lowerMapIndexValue(expr *hir.IndexExpr, mapType *types.MapType) mir.ValueID {
	if expr == nil || mapType == nil {
		return mir.InvalidValue
	}

	mapVal := b.lowerExpr(expr.X)
	if mapVal == mir.InvalidValue {
		return mir.InvalidValue
	}
	mapVal, _ = b.derefValueIfNeeded(mapVal, b.exprType(expr.X), expr.Location)

	keyVal := b.lowerExpr(expr.Index)
	if keyVal == mir.InvalidValue {
		return mir.InvalidValue
	}

	if mapType.Key != nil {
		keyVal = b.castValue(keyVal, b.exprType(expr.Index), mapType.Key, expr.Location)
	}

	result := b.gen.nextValueID()
	b.emitInstr(&mir.MapGet{
		Result:   result,
		Map:      mapVal,
		Key:      keyVal,
		Type:     expr.Type,
		Location: expr.Location,
	})
	return result
}

func (b *functionBuilder) lowerMapIndexAssign(expr *hir.IndexExpr, rhs hir.Expr, op *tokens.Token, loc source.Location, mapType *types.MapType) {
	if expr == nil || mapType == nil {
		return
	}

	mapVal := b.lowerExpr(expr.X)
	if mapVal == mir.InvalidValue {
		return
	}
	mapVal, _ = b.derefValueIfNeeded(mapVal, b.exprType(expr.X), loc)

	keyVal := b.lowerExpr(expr.Index)
	if keyVal == mir.InvalidValue {
		return
	}

	valueVal := b.lowerExpr(rhs)
	if valueVal == mir.InvalidValue {
		return
	}

	if mapType.Key != nil {
		keyVal = b.castValue(keyVal, b.exprType(expr.Index), mapType.Key, expr.Location)
	}
	if mapType.Value != nil {
		valueVal = b.castValue(valueVal, b.exprType(rhs), mapType.Value, loc)
	}

	if op != nil && op.Kind != tokens.EQUALS_TOKEN {
		opKind := assignTokenToBinary(op.Kind)
		if opKind == "" {
			b.reportUnsupported("assignment operator", &loc)
			return
		}

		optType := types.NewOptional(mapType.Value)
		curOpt := b.gen.nextValueID()
		b.emitInstr(&mir.MapGet{
			Result:   curOpt,
			Map:      mapVal,
			Key:      keyVal,
			Type:     optType,
			Location: expr.Location,
		})
		curVal := b.gen.nextValueID()
		b.emitInstr(&mir.OptionalUnwrap{
			Result:     curVal,
			Value:      curOpt,
			HasDefault: false,
			Type:       mapType.Value,
			Location:   loc,
		})

		valueVal = b.emitBinary(opKind, curVal, valueVal, mapType.Value, loc)
	}

	b.emitInstr(&mir.MapSet{
		Map:      mapVal,
		Key:      keyVal,
		Value:    valueVal,
		Location: loc,
	})
}

func (b *functionBuilder) arrayTypeOf(expr hir.Expr) *types.ArrayType {
	if expr == nil {
		return nil
	}

	baseType := b.exprType(expr)
	if baseType == nil {
		return nil
	}
	baseType = types.UnwrapType(baseType)
	if ref, ok := baseType.(*types.ReferenceType); ok {
		baseType = types.UnwrapType(ref.Inner)
	}

	arrType, _ := baseType.(*types.ArrayType)
	return arrType
}

func (b *functionBuilder) isStringType(expr hir.Expr) bool {
	if expr == nil {
		return false
	}
	baseType := b.exprType(expr)
	if baseType == nil {
		return false
	}
	baseType = types.UnwrapType(baseType)
	if ref, ok := baseType.(*types.ReferenceType); ok {
		baseType = types.UnwrapType(ref.Inner)
	}
	if prim, ok := baseType.(*types.PrimitiveType); ok {
		return prim.GetName() == types.TYPE_STRING
	}
	return false
}

func (b *functionBuilder) mapTypeOf(expr hir.Expr) *types.MapType {
	if expr == nil {
		return nil
	}

	baseType := b.exprType(expr)
	if baseType == nil {
		return nil
	}
	baseType = types.UnwrapType(baseType)
	if ref, ok := baseType.(*types.ReferenceType); ok {
		baseType = types.UnwrapType(ref.Inner)
	}

	mapType, _ := baseType.(*types.MapType)
	return mapType
}

type mapRuntimeFns struct {
	newFn       string
	fromPairsFn string
}

func (b *functionBuilder) mapRuntimeFns(keyType types.SemType) mapRuntimeFns {
	keyType = types.UnwrapType(keyType)
	switch kt := keyType.(type) {
	case *types.PrimitiveType:
		switch kt.GetName() {
		case types.TYPE_I32:
			return mapRuntimeFns{
				newFn:       "ferret_map_new_i32",
				fromPairsFn: "ferret_map_from_pairs_i32",
			}
		case types.TYPE_I64:
			return mapRuntimeFns{
				newFn:       "ferret_map_new_i64",
				fromPairsFn: "ferret_map_from_pairs_i64",
			}
		case types.TYPE_STRING:
			return mapRuntimeFns{
				newFn:       "ferret_map_new_str",
				fromPairsFn: "ferret_map_from_pairs_str",
			}
		}
	case *types.NamedType:
		return b.mapRuntimeFns(kt.Underlying)
	}
	return mapRuntimeFns{
		newFn:       "ferret_map_new_bytes",
		fromPairsFn: "ferret_map_from_pairs_bytes",
	}
}

func (b *functionBuilder) mapIterStructType() *types.StructType {
	sizeType := types.TypeI64
	if b.gen != nil && b.gen.layout != nil && b.gen.layout.PointerSize <= 4 {
		sizeType = types.TypeI32
	}
	return types.NewStruct("", []types.StructField{
		{Name: "bucket_index", Type: sizeType},
		{Name: "entry", Type: types.NewReference(types.TypeVoid)},
	})
}

func (b *functionBuilder) lowerMapIterInit(expr *hir.MapIterInitExpr) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}
	mapVal := b.lowerExpr(expr.Map)
	if mapVal == mir.InvalidValue {
		return mir.InvalidValue
	}

	iterType := b.mapIterStructType()
	iterAddr := b.emitAlloca(iterType, expr.Location)
	b.emitInstr(&mir.Call{
		Result:   mir.InvalidValue,
		Target:   "ferret_map_iter_begin",
		Args:     []mir.ValueID{mapVal, iterAddr},
		Type:     types.TypeBool,
		Location: expr.Location,
	})
	return iterAddr
}

func (b *functionBuilder) lowerMapIterNext(expr *hir.MapIterNextExpr) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}
	mapVal := b.lowerExpr(expr.Map)
	if mapVal == mir.InvalidValue {
		return mir.InvalidValue
	}
	iterVal := b.lowerExpr(expr.Iter)
	if iterVal == mir.InvalidValue {
		return mir.InvalidValue
	}

	keyType := types.TypeUnknown
	valType := types.TypeUnknown
	if mapType := b.mapTypeOf(expr.Map); mapType != nil {
		if mapType.Key != nil {
			keyType = mapType.Key
		}
		if mapType.Value != nil {
			valType = mapType.Value
		}
	}
	if expr.Key != nil && expr.Key.Type != nil && !expr.Key.Type.Equals(types.TypeUnknown) {
		keyType = expr.Key.Type
	}
	if expr.Value != nil && expr.Value.Type != nil && !expr.Value.Type.Equals(types.TypeUnknown) {
		valType = expr.Value.Type
	}

	keyPtrSlot := b.emitAlloca(types.NewReference(keyType), expr.Location)
	valPtrSlot := b.emitAlloca(types.NewReference(valType), expr.Location)

	result := b.gen.nextValueID()
	b.emitInstr(&mir.Call{
		Result:   result,
		Target:   "ferret_map_iter_next",
		Args:     []mir.ValueID{mapVal, iterVal, keyPtrSlot, valPtrSlot},
		Type:     types.TypeBool,
		Location: expr.Location,
	})

	if b.current == nil || b.current.Term != nil {
		return result
	}

	updateBlock := b.newBlock("mapiter.update", expr.Location)
	mergeBlock := b.newBlock("mapiter.merge", expr.Location)
	b.current.Term = &mir.CondBr{
		Cond:     result,
		Then:     updateBlock.ID,
		Else:     mergeBlock.ID,
		Location: expr.Location,
	}

	b.setBlock(updateBlock)
	if expr.Key != nil {
		keyPtr := b.emitLoad(keyPtrSlot, types.NewReference(keyType), expr.Location)
		keyVal := b.emitLoad(keyPtr, keyType, expr.Location)
		if keyAddr := b.addrForIdent(expr.Key); keyAddr != mir.InvalidValue {
			b.emitStore(keyAddr, keyVal, expr.Location)
		}
	}
	if expr.Value != nil {
		valPtr := b.emitLoad(valPtrSlot, types.NewReference(valType), expr.Location)
		valVal := b.emitLoad(valPtr, valType, expr.Location)
		if valAddr := b.addrForIdent(expr.Value); valAddr != mir.InvalidValue {
			b.emitStore(valAddr, valVal, expr.Location)
		}
	}
	updateBlock.Term = &mir.Br{Target: mergeBlock.ID, Location: expr.Location}

	b.setBlock(mergeBlock)
	return result
}

func (b *functionBuilder) callTarget(expr hir.Expr) (string, bool) {
	if expr == nil {
		return "", false
	}

	switch e := expr.(type) {
	case *hir.Ident:
		if e.Symbol != nil && e.Symbol.Kind == symbols.SymbolFunction {
			return e.Name, true
		}
		return "", false
	case *hir.ScopeResolutionExpr:
		return b.qualifiedName(e)
	default:
		return "", false
	}
}

func (b *functionBuilder) lowerFuncLit(lit *hir.FuncLit) mir.ValueID {
	if lit == nil {
		return mir.InvalidValue
	}
	info := b.gen.closureForFuncLit(lit)
	if info == nil {
		return mir.InvalidValue
	}
	return b.makeClosureValue(info.name, lit.Type, info.envType, info.captures, lit.Location)
}

func (b *functionBuilder) makeFuncValue(name string, fnType types.SemType, loc source.Location) mir.ValueID {
	envType := &types.StructType{Fields: []types.StructField{{Name: "__fn", Type: types.TypeU64}}}
	inner, ok := types.UnwrapType(fnType).(*types.FunctionType)
	if !ok || inner == nil {
		b.reportUnsupported("function value type", &loc)
		return mir.InvalidValue
	}
	wrapper := b.gen.funcValueWrapper(name, inner, envType, loc)
	if wrapper == "" {
		return mir.InvalidValue
	}
	return b.makeClosureValue(wrapper, fnType, envType, nil, loc)
}

func (b *functionBuilder) makeClosureValue(name string, fnType types.SemType, envType *types.StructType, captures []captureInfo, loc source.Location) mir.ValueID {
	if envType == nil {
		b.reportUnsupported("closure env", &loc)
		return mir.InvalidValue
	}
	size := b.gen.layout.SizeOf(envType)
	if size <= 0 {
		b.reportUnsupported("closure env size", &loc)
		return mir.InvalidValue
	}
	sizeVal := b.emitConst(types.TypeU64, strconv.Itoa(size), loc)
	resultType := fnType
	if resultType == nil {
		resultType = types.TypeU64
	}
	env := b.gen.nextValueID()
	b.emitInstr(&mir.Call{
		Result:   env,
		Target:   "ferret_alloc",
		Args:     []mir.ValueID{sizeVal},
		Type:     resultType,
		Location: loc,
	})

	layout := b.gen.layout.StructLayout(envType)
	fnOffset, ok := layout.FieldOffset("__fn")
	if !ok {
		fnOffset = 0
	}
	fnConstType := fnType
	if fnConstType == nil {
		fnConstType = types.TypeU64
	}
	fnVal := b.emitConst(fnConstType, name, loc)
	fnAddr := b.emitPtrAdd(env, fnOffset, types.TypeU64, loc)
	b.emitStore(fnAddr, fnVal, loc)

	for _, cap := range captures {
		if cap.ident == nil {
			continue
		}
		fieldAddr := b.emitPtrAdd(env, cap.offset, cap.typ, loc)
		addr := b.boxCapturedIdent(cap.ident)
		if addr == mir.InvalidValue {
			return mir.InvalidValue
		}
		b.emitStore(fieldAddr, addr, loc)
	}

	return env
}

func (b *functionBuilder) boxCapturedIdent(ident *hir.Ident) mir.ValueID {
	if ident == nil || ident.Symbol == nil {
		return mir.InvalidValue
	}

	if addr, ok := b.boxed[ident.Symbol]; ok {
		return addr
	}

	typ := ident.Type
	if typ == nil {
		b.reportUnsupported("capture box type", &ident.Location)
		return mir.InvalidValue
	}

	size := b.gen.layout.SizeOf(typ)
	if size <= 0 {
		b.reportUnsupported("capture box size", &ident.Location)
		return mir.InvalidValue
	}

	sizeVal := b.emitConst(types.TypeU64, strconv.Itoa(size), ident.Location)
	box := b.gen.nextValueID()
	b.emitInstr(&mir.Call{
		Result:   box,
		Target:   "ferret_alloc",
		Args:     []mir.ValueID{sizeVal},
		Type:     types.NewReference(typ),
		Location: ident.Location,
	})
	b.ptrElem[box] = typ

	var val mir.ValueID
	if addr, ok := b.slots[ident.Symbol]; ok {
		val = b.emitLoad(addr, typ, ident.Location)
	} else if ident.Symbol.Kind == symbols.SymbolParameter || ident.Symbol.Kind == symbols.SymbolReceiver {
		if param, ok := b.paramsByName[ident.Name]; ok {
			val = param
		}
	}
	if val != mir.InvalidValue {
		b.emitStore(box, val, ident.Location)
	}

	b.boxed[ident.Symbol] = box
	b.slots[ident.Symbol] = box
	return box
}

func (b *functionBuilder) methodCallTarget(expr *hir.SelectorExpr) (string, *symbols.MethodInfo, bool) {
	if expr == nil || expr.Field == nil || expr.Field.Name == "" {
		return "", nil, false
	}

	baseType := b.exprType(expr.X)
	if baseType == nil {
		return "", nil, false
	}

	baseType = dereferenceType(baseType)
	named, ok := baseType.(*types.NamedType)
	if !ok || named.Name == "" {
		return "", nil, false
	}

	if structType, ok := types.UnwrapType(named).(*types.StructType); ok {
		for _, field := range structType.Fields {
			if field.Name == expr.Field.Name {
				return "", nil, false
			}
		}
	}

	typeSym, alias, ok := b.lookupTypeSymbol(named.Name)
	if !ok || typeSym == nil || typeSym.Methods == nil {
		return "", nil, false
	}

	method, ok := typeSym.Methods[expr.Field.Name]
	if !ok || method == nil {
		return "", nil, false
	}

	target := named.Name + "_" + expr.Field.Name
	if alias != "" {
		target = alias + "::" + target
	}
	return target, method, true
}

func (b *functionBuilder) methodReceiverArg(expr *hir.SelectorExpr, method *symbols.MethodInfo) mir.ValueID {
	if expr == nil || method == nil {
		return mir.InvalidValue
	}

	recvType := method.Receiver
	if recvType == nil {
		return b.lowerExpr(expr.X)
	}

	recvInner := recvType
	receiverIsRef := false
	if ref, ok := recvType.(*types.ReferenceType); ok {
		receiverIsRef = true
		recvInner = ref.Inner
	}

	if receiverIsRef {
		return b.methodReceiverRef(expr, recvInner)
	}

	if needsByRefType(recvInner) {
		return b.methodReceiverCopy(expr, recvInner)
	}

	return b.methodReceiverValue(expr)
}

func (b *functionBuilder) methodReceiverRef(expr *hir.SelectorExpr, recvInner types.SemType) mir.ValueID {
	exprType := b.exprType(expr.X)
	if exprType != nil {
		if _, ok := types.UnwrapType(exprType).(*types.ReferenceType); ok || needsByRefType(exprType) {
			return b.lowerExpr(expr.X)
		}
	}

	if isAddressableExpr(expr.X) {
		return b.lowerLValue(expr.X)
	}

	val := b.lowerExpr(expr.X)
	if val == mir.InvalidValue {
		return mir.InvalidValue
	}
	tmp := b.emitAlloca(recvInner, expr.Location)
	b.emitStore(tmp, val, expr.Location)
	return tmp
}

func (b *functionBuilder) methodReceiverCopy(expr *hir.SelectorExpr, recvInner types.SemType) mir.ValueID {
	val := b.lowerExpr(expr.X)
	if val == mir.InvalidValue {
		return mir.InvalidValue
	}
	tmp := b.emitAlloca(recvInner, expr.Location)
	b.emitStore(tmp, val, expr.Location)
	return tmp
}

func (b *functionBuilder) methodReceiverValue(expr *hir.SelectorExpr) mir.ValueID {
	exprType := b.exprType(expr.X)
	val := b.lowerExpr(expr.X)
	if val == mir.InvalidValue {
		return mir.InvalidValue
	}
	if exprType != nil {
		if ref, ok := types.UnwrapType(exprType).(*types.ReferenceType); ok {
			return b.emitLoad(val, ref.Inner, expr.Location)
		}
	}
	return val
}

func (b *functionBuilder) lookupTypeSymbol(name string) (*symbols.Symbol, string, bool) {
	if b.gen == nil || b.gen.mod == nil || b.gen.mod.ModuleScope == nil {
		return nil, "", false
	}

	if sym, ok := b.gen.mod.ModuleScope.GetSymbol(name); ok && sym.Kind == symbols.SymbolType {
		return sym, "", true
	}

	if b.gen.ctx == nil || b.gen.mod.ImportAliasMap == nil {
		return nil, "", false
	}

	for alias, importPath := range b.gen.mod.ImportAliasMap {
		if imported, ok := b.gen.ctx.GetModule(importPath); ok && imported.ModuleScope != nil {
			if sym, ok := imported.ModuleScope.GetSymbol(name); ok && sym.Kind == symbols.SymbolType {
				return sym, alias, true
			}
		}
	}

	return nil, "", false
}

func dereferenceType(typ types.SemType) types.SemType {
	if ref, ok := typ.(*types.ReferenceType); ok {
		return ref.Inner
	}
	return typ
}

func (b *functionBuilder) qualifiedName(expr hir.Expr) (string, bool) {
	switch e := expr.(type) {
	case *hir.Ident:
		return e.Name, e.Name != ""
	case *hir.ScopeResolutionExpr:
		left, ok := b.qualifiedName(e.X)
		if !ok || e.Selector == nil || e.Selector.Name == "" {
			return "", false
		}
		return left + "::" + e.Selector.Name, true
	default:
		return "", false
	}
}

func (b *functionBuilder) lookupQualifiedConst(name string) (string, bool) {
	if b.gen == nil || b.gen.mod == nil {
		return "", false
	}

	mod := b.gen.mod
	if mod.ImportAliasMap != nil {
		parts := strings.Split(name, "::")
		if len(parts) > 1 {
			if importPath, ok := mod.ImportAliasMap[parts[0]]; ok {
				if b.gen.ctx == nil {
					return "", false
				}
				imported, ok := b.gen.ctx.GetModule(importPath)
				if !ok || imported == nil || imported.ModuleScope == nil {
					return "", false
				}
				symName := strings.Join(parts[1:], "::")
				if sym, ok := imported.ModuleScope.GetSymbol(symName); ok && sym.ConstValue != nil && sym.ConstValue.IsConstant() {
					return sym.ConstValue.String(), true
				}
				return "", false
			}
		}
	}

	if mod.ModuleScope == nil {
		return "", false
	}
	if sym, ok := mod.ModuleScope.GetSymbol(name); ok && sym.ConstValue != nil && sym.ConstValue.IsConstant() {
		return sym.ConstValue.String(), true
	}
	return "", false
}

func (b *functionBuilder) matchCaseValue(pattern hir.Expr) (string, bool) {
	if pattern == nil {
		return "", false
	}

	switch p := pattern.(type) {
	case *hir.Literal:
		return p.Value, true
	case *hir.Ident:
		if p.Symbol != nil && p.Symbol.ConstValue != nil && p.Symbol.ConstValue.IsConstant() {
			return p.Symbol.ConstValue.String(), true
		}
	case *hir.ScopeResolutionExpr:
		if name, ok := b.qualifiedName(p); ok {
			if value, ok := b.lookupQualifiedConst(name); ok {
				return value, true
			}
		}
	}

	if b.gen != nil && b.gen.ctx != nil && b.gen.mod != nil {
		if value := consteval.EvaluateHIRExpr(b.gen.ctx, b.gen.mod, pattern); value != nil && value.IsConstant() {
			return value.String(), true
		}
	}

	return "", false
}

func (b *functionBuilder) matchCaseConstValue(pattern hir.Expr, matchType types.SemType) (mir.ValueID, bool) {
	if pattern == nil {
		return mir.InvalidValue, false
	}

	value, ok := b.matchCaseValue(pattern)
	if !ok {
		return mir.InvalidValue, false
	}

	if matchType == nil || matchType.Equals(types.TypeUnknown) {
		matchType = b.exprType(pattern)
	}
	if matchType == nil || matchType.Equals(types.TypeUnknown) {
		return mir.InvalidValue, false
	}

	loc := pattern.Loc()
	if loc == nil {
		loc = &source.Location{}
	}
	if isLargePrimitiveType(matchType) {
		return b.emitLargeConst(matchType, value, *loc), true
	}
	return b.emitConst(matchType, value, *loc), true
}

func (b *functionBuilder) exprType(expr hir.Expr) types.SemType {
	switch e := expr.(type) {
	case *hir.Literal:
		return e.Type
	case *hir.Ident:
		return e.Type
	case *hir.BinaryExpr:
		return e.Type
	case *hir.UnaryExpr:
		return e.Type
	case *hir.PrefixExpr:
		return e.Type
	case *hir.PostfixExpr:
		return e.Type
	case *hir.CallExpr:
		return e.Type
	case *hir.IndexExpr:
		return e.Type
	case *hir.SelectorExpr:
		return e.Type
	case *hir.ScopeResolutionExpr:
		return e.Type
	case *hir.ParenExpr:
		return e.Type
	default:
		return nil
	}
}

// getStorageType returns the actual storage type for an expression.
// For identifiers, this returns the Symbol's type (which is the storage type),
// not the narrowed expression type. This is important for assignments to
// narrowed union/optional variables.
func (b *functionBuilder) getStorageType(expr hir.Expr) types.SemType {
	if ident, ok := expr.(*hir.Ident); ok && ident.Symbol != nil {
		// Use Symbol.Type which is the actual storage type
		return ident.Symbol.Type
	}
	// For other expressions, fall back to expression type
	return b.exprType(expr)
}

func isAddressableExpr(expr hir.Expr) bool {
	switch expr.(type) {
	case *hir.Ident, *hir.SelectorExpr, *hir.IndexExpr:
		return true
	default:
		return false
	}
}

func (b *functionBuilder) emitConst(typ types.SemType, value string, loc source.Location) mir.ValueID {
	id := b.gen.nextValueID()
	b.emitInstr(&mir.Const{
		Result:   id,
		Type:     typ,
		Value:    value,
		Location: loc,
	})
	return id
}

func (b *functionBuilder) emitMemcpy(dst, src mir.ValueID, typ types.SemType, loc source.Location) {
	if typ == nil {
		b.reportUnsupported("memcpy size", &loc)
		return
	}
	size := 0
	if b.gen != nil && b.gen.layout != nil {
		size = b.gen.layout.SizeOf(typ)
	}
	if size <= 0 {
		b.reportUnsupported("memcpy size", &loc)
		return
	}
	sizeStr := strconv.FormatInt(int64(size), 10)
	sizeVal := b.emitConst(types.TypeU64, sizeStr, loc)
	b.emitInstr(&mir.Call{
		Result:   mir.InvalidValue,
		Target:   "ferret_memcpy",
		Args:     []mir.ValueID{dst, src, sizeVal},
		Type:     types.TypeVoid,
		Location: loc,
	})
}

func (b *functionBuilder) emitLargeConst(typ types.SemType, value string, loc source.Location) mir.ValueID {
	typeName, ok := largePrimitiveName(typ)
	if !ok {
		b.reportUnsupported("large const", &loc)
		return mir.InvalidValue
	}
	if isLargeIntName(typeName) {
		clean := strings.TrimSpace(value)
		sign := ""
		if strings.HasPrefix(clean, "-") {
			sign = "-"
			clean = strings.TrimPrefix(clean, "-")
		}
		if bigInt, err := numeric.StringToBigInt(clean); err == nil {
			if sign == "-" {
				bigInt.Neg(bigInt)
			}
			value = bigInt.String()
		} else {
			value = strings.ReplaceAll(value, "_", "")
		}
	} else {
		value = strings.ReplaceAll(value, "_", "")
	}
	out := b.emitAlloca(typ, loc)
	lit := b.emitConst(types.TypeString, value, loc)
	b.emitInstr(&mir.Call{
		Result:   mir.InvalidValue,
		Target:   "ferret_" + typeName + "_from_string_ptr",
		Args:     []mir.ValueID{lit, out},
		Type:     types.TypeVoid,
		Location: loc,
	})
	return out
}

func (b *functionBuilder) emitLargeBinary(op tokens.TOKEN, left, right mir.ValueID, typ types.SemType, loc source.Location) mir.ValueID {
	typeName, ok := largePrimitiveName(typ)
	if !ok {
		b.reportUnsupported("large binary", &loc)
		return mir.InvalidValue
	}
	fn, ok := largeBinaryFunc(op, typeName)
	if !ok {
		b.reportUnsupported(fmt.Sprintf("binary op %s", op), &loc)
		return mir.InvalidValue
	}
	out := b.emitAlloca(typ, loc)
	b.emitInstr(&mir.Call{
		Result:   mir.InvalidValue,
		Target:   fn,
		Args:     []mir.ValueID{left, right, out},
		Type:     types.TypeVoid,
		Location: loc,
	})
	return out
}

func (b *functionBuilder) emitLargeCompare(op tokens.TOKEN, left, right mir.ValueID, typ types.SemType, loc source.Location) mir.ValueID {
	typeName, ok := largePrimitiveName(typ)
	if !ok {
		b.reportUnsupported("large compare", &loc)
		return mir.InvalidValue
	}

	switch op {
	case tokens.DOUBLE_EQUAL_TOKEN:
		return b.emitLargeCompareCall(typeName, "eq", left, right, loc)
	case tokens.NOT_EQUAL_TOKEN:
		eq := b.emitLargeCompareCall(typeName, "eq", left, right, loc)
		return b.emitUnary(tokens.NOT_TOKEN, eq, types.TypeBool, loc)
	case tokens.LESS_TOKEN:
		return b.emitLargeCompareCall(typeName, "lt", left, right, loc)
	case tokens.GREATER_TOKEN:
		return b.emitLargeCompareCall(typeName, "gt", left, right, loc)
	case tokens.LESS_EQUAL_TOKEN:
		lt := b.emitLargeCompareCall(typeName, "lt", left, right, loc)
		eq := b.emitLargeCompareCall(typeName, "eq", left, right, loc)
		return b.emitBinary(tokens.OR_TOKEN, lt, eq, types.TypeBool, loc)
	case tokens.GREATER_EQUAL_TOKEN:
		gt := b.emitLargeCompareCall(typeName, "gt", left, right, loc)
		eq := b.emitLargeCompareCall(typeName, "eq", left, right, loc)
		return b.emitBinary(tokens.OR_TOKEN, gt, eq, types.TypeBool, loc)
	default:
		b.reportUnsupported(fmt.Sprintf("compare op %s", op), &loc)
		return mir.InvalidValue
	}
}

func (b *functionBuilder) emitLargeCompareCall(typeName, op string, left, right mir.ValueID, loc source.Location) mir.ValueID {
	id := b.gen.nextValueID()
	b.emitInstr(&mir.Call{
		Result:   id,
		Target:   "ferret_" + typeName + "_" + op + "_ptr",
		Args:     []mir.ValueID{left, right},
		Type:     types.TypeBool,
		Location: loc,
	})
	return id
}

func (b *functionBuilder) emitLargeUnary(op tokens.TOKEN, value mir.ValueID, typ types.SemType, loc source.Location) mir.ValueID {
	switch op {
	case tokens.PLUS_TOKEN:
		return value
	case tokens.MINUS_TOKEN:
		zero := b.emitLargeConst(typ, "0", loc)
		return b.emitLargeBinary(tokens.MINUS_TOKEN, zero, value, typ, loc)
	default:
		b.reportUnsupported(fmt.Sprintf("unary op %s", op), &loc)
		return mir.InvalidValue
	}
}

func (b *functionBuilder) emitLargeCast(value mir.ValueID, from, to types.SemType, loc source.Location) mir.ValueID {
	fromName, fromLarge := largePrimitiveName(from)
	toName, toLarge := largePrimitiveName(to)

	if fromLarge && toLarge {
		if fromName == toName {
			return value
		}
		out := b.emitAlloca(to, loc)
		str := b.emitLargeToString(fromName, value, loc)
		b.emitInstr(&mir.Call{
			Result:   mir.InvalidValue,
			Target:   "ferret_" + toName + "_from_string_ptr",
			Args:     []mir.ValueID{str, out},
			Type:     types.TypeVoid,
			Location: loc,
		})
		return out
	}

	if toLarge && !fromLarge {
		fn, argType, ok := largeFromSmallFunc(toName, from)
		if !ok {
			b.reportUnsupported("cast to large type", &loc)
			return mir.InvalidValue
		}
		if argType != nil && !argType.Equals(from) {
			value = b.castValue(value, from, argType, loc)
		}
		out := b.emitAlloca(to, loc)
		b.emitInstr(&mir.Call{
			Result:   mir.InvalidValue,
			Target:   fn,
			Args:     []mir.ValueID{value, out},
			Type:     types.TypeVoid,
			Location: loc,
		})
		return out
	}

	if fromLarge && !toLarge {
		fn, retType, ok := largeToSmallFunc(fromName, to)
		if !ok {
			b.reportUnsupported("cast from large type", &loc)
			return mir.InvalidValue
		}
		id := b.gen.nextValueID()
		b.emitInstr(&mir.Call{
			Result:   id,
			Target:   fn,
			Args:     []mir.ValueID{value},
			Type:     retType,
			Location: loc,
		})
		if retType == nil || to == nil || retType.Equals(to) {
			return id
		}
		return b.emitCast(id, to, loc)
	}

	return b.emitCast(value, to, loc)
}

func (b *functionBuilder) emitLargeToString(typeName string, value mir.ValueID, loc source.Location) mir.ValueID {
	id := b.gen.nextValueID()
	b.emitInstr(&mir.Call{
		Result:   id,
		Target:   "ferret_" + typeName + "_to_string_ptr",
		Args:     []mir.ValueID{value},
		Type:     types.TypeString,
		Location: loc,
	})
	return id
}

func (b *functionBuilder) emitBinary(op tokens.TOKEN, left, right mir.ValueID, typ types.SemType, loc source.Location) mir.ValueID {
	if isLargePrimitiveType(typ) {
		return b.emitLargeBinary(op, left, right, typ, loc)
	}
	id := b.gen.nextValueID()
	b.emitInstr(&mir.Binary{
		Result:   id,
		Op:       op,
		Left:     left,
		Right:    right,
		Type:     typ,
		Location: loc,
	})
	return id
}

func (b *functionBuilder) emitUnary(op tokens.TOKEN, value mir.ValueID, typ types.SemType, loc source.Location) mir.ValueID {
	if isLargePrimitiveType(typ) {
		return b.emitLargeUnary(op, value, typ, loc)
	}
	id := b.gen.nextValueID()
	b.emitInstr(&mir.Unary{
		Result:   id,
		Op:       op,
		X:        value,
		Type:     typ,
		Location: loc,
	})
	return id
}

func (b *functionBuilder) emitCast(value mir.ValueID, typ types.SemType, loc source.Location) mir.ValueID {
	id := b.gen.nextValueID()
	b.emitInstr(&mir.Cast{
		Result:   id,
		X:        value,
		Type:     typ,
		Location: loc,
	})
	return id
}

func (b *functionBuilder) emitAlloca(typ types.SemType, loc source.Location) mir.ValueID {
	id := b.gen.nextValueID()
	b.emitInstr(&mir.Alloca{
		Result:   id,
		Type:     typ,
		Location: loc,
	})
	b.ptrElem[id] = typ
	return id
}

func (b *functionBuilder) emitAllocaInEntry(typ types.SemType, loc source.Location) mir.ValueID {
	if b.entry == nil {
		return b.emitAlloca(typ, loc)
	}
	id := b.gen.nextValueID()
	b.entry.Instrs = append(b.entry.Instrs, &mir.Alloca{
		Result:   id,
		Type:     typ,
		Location: loc,
	})
	b.ptrElem[id] = typ
	return id
}

func (b *functionBuilder) emitStoreInEntry(addr, value mir.ValueID, loc source.Location) {
	if b.entry == nil {
		b.emitStore(addr, value, loc)
		return
	}
	b.entry.Instrs = append(b.entry.Instrs, &mir.Store{
		Addr:     addr,
		Value:    value,
		Location: loc,
	})
}

func (b *functionBuilder) emitLoad(addr mir.ValueID, typ types.SemType, loc source.Location) mir.ValueID {
	if needsByRefType(typ) {
		return addr
	}
	id := b.gen.nextValueID()
	b.emitInstr(&mir.Load{
		Result:   id,
		Addr:     addr,
		Type:     typ,
		Location: loc,
	})
	if ref, ok := types.UnwrapType(typ).(*types.ReferenceType); ok {
		b.ptrElem[id] = ref.Inner
	}
	return id
}

func (b *functionBuilder) emitStore(addr, value mir.ValueID, loc source.Location) {
	if elem, ok := b.ptrElem[addr]; ok && needsByRefType(elem) {
		b.emitMemcpy(addr, value, elem, loc)
		return
	}
	b.emitInstr(&mir.Store{
		Addr:     addr,
		Value:    value,
		Location: loc,
	})
}

func (b *functionBuilder) emitPtrAdd(base mir.ValueID, offset int, elem types.SemType, loc source.Location) mir.ValueID {
	id := b.gen.nextValueID()
	b.emitInstr(&mir.PtrAdd{
		Result:   id,
		Base:     base,
		Offset:   offset,
		Elem:     elem,
		Location: loc,
	})
	b.ptrElem[id] = elem
	return id
}

func (b *functionBuilder) emitPtrOffset(base mir.ValueID, offset mir.ValueID, elem types.SemType, loc source.Location) mir.ValueID {
	id := b.gen.nextValueID()
	b.emitInstr(&mir.PtrOffset{
		Result:   id,
		Base:     base,
		Offset:   offset,
		Elem:     elem,
		Location: loc,
	})
	b.ptrElem[id] = elem
	return id
}

func (b *functionBuilder) emitStringConcat(left, right mir.ValueID, rightType types.SemType, loc source.Location) mir.ValueID {
	result := b.gen.nextValueID()
	rightBase := types.UnwrapType(rightType)

	// Determine which concat function to use based on RHS type
	var target string
	args := []mir.ValueID{left, right}

	// Get primitive type name if available
	var rightTypeName types.TYPE_NAME
	if pt, ok := rightBase.(*types.PrimitiveType); ok {
		rightTypeName = pt.GetName()
	}

	// Handle large primitive types (i128, u128, i256, u256, f128, f256)
	// by converting to string first, then concatenating
	if typeName, ok := largePrimitiveName(rightBase); ok {
		rightStr := b.emitLargeToString(typeName, right, loc)
		args = []mir.ValueID{left, rightStr}
		target = "ferret_io_ConcatStrings"
		b.emitInstr(&mir.Call{
			Result:   result,
			Target:   target,
			Args:     args,
			Type:     types.TypeString,
			Location: loc,
		})
		return result
	}

	switch {
	case rightBase.Equals(types.TypeString):
		target = "ferret_io_ConcatStrings"
	case rightBase.Equals(types.TypeBool):
		target = "ferret_string_concat_bool"
	case rightBase.Equals(types.TypeByte):
		target = "ferret_string_concat_byte"
	case types.IsFloat(rightBase):
		// Cast to f64 for the concat function
		if !rightBase.Equals(types.TypeF64) {
			right = b.castValue(right, rightType, types.TypeF64, loc)
		}
		target = "ferret_string_concat_f64"
	case types.IsUnsigned(rightTypeName):
		// Cast to u64 for the concat function
		if !rightBase.Equals(types.TypeU64) {
			right = b.castValue(right, rightType, types.TypeU64, loc)
		}
		target = "ferret_string_concat_u64"
		args = []mir.ValueID{left, right}
	case types.IsSigned(rightTypeName) || types.IsUntyped(rightBase):
		// Cast to i64 for the concat function
		if !rightBase.Equals(types.TypeI64) {
			right = b.castValue(right, rightType, types.TypeI64, loc)
		}
		target = "ferret_string_concat_i64"
		args = []mir.ValueID{left, right}
	default:
		// Fallback to string concat (shouldn't happen if typechecker is correct)
		target = "ferret_io_ConcatStrings"
	}

	b.emitInstr(&mir.Call{
		Result:   result,
		Target:   target,
		Args:     args,
		Type:     types.TypeString,
		Location: loc,
	})
	return result
}

func (b *functionBuilder) emitArrayLen(arrVal mir.ValueID, loc source.Location) mir.ValueID {
	id := b.gen.nextValueID()
	b.emitInstr(&mir.Call{
		Result:   id,
		Target:   "ferret_array_len",
		Args:     []mir.ValueID{arrVal},
		Type:     types.TypeI32,
		Location: loc,
	})
	return id
}

func (b *functionBuilder) emitStringLen(strVal mir.ValueID, loc source.Location) mir.ValueID {
	id := b.gen.nextValueID()
	b.emitInstr(&mir.Call{
		Result:   id,
		Target:   "ferret_string_len",
		Args:     []mir.ValueID{strVal},
		Type:     types.TypeI32,
		Location: loc,
	})
	return id
}

func (b *functionBuilder) emitDynamicArrayGet(arrVal, indexVal mir.ValueID, elemType types.SemType, loc source.Location) mir.ValueID {
	ptrType := types.NewReference(elemType)
	ptrVal := b.gen.nextValueID()
	b.emitInstr(&mir.Call{
		Result:   ptrVal,
		Target:   "ferret_array_get",
		Args:     []mir.ValueID{arrVal, indexVal},
		Type:     ptrType,
		Location: loc,
	})
	return b.emitLoad(ptrVal, elemType, loc)
}

func (b *functionBuilder) emitBoundsCheckedIndex(indexVal, lenVal mir.ValueID, indexType types.SemType, loc source.Location) mir.ValueID {
	if indexVal == mir.InvalidValue || lenVal == mir.InvalidValue {
		return mir.InvalidValue
	}
	if b.current == nil || b.current.Term != nil {
		return indexVal
	}

	zero := b.emitConst(indexType, "0", loc)
	condNeg := b.emitBinary(tokens.LESS_TOKEN, indexVal, zero, indexType, loc)

	negBlock := b.newBlock("index.neg", loc)
	posBlock := b.newBlock("index.pos", loc)
	mergeBlock := b.newBlock("index.merge", loc)
	okBlock := b.newBlock("index.ok", loc)
	oobBlock := b.newBlock("index.oob", loc)

	b.current.Term = &mir.CondBr{
		Cond:     condNeg,
		Then:     negBlock.ID,
		Else:     posBlock.ID,
		Location: loc,
	}

	b.setBlock(negBlock)
	idxNeg := b.emitBinary(tokens.PLUS_TOKEN, lenVal, indexVal, indexType, loc)
	negBlock.Term = &mir.Br{Target: mergeBlock.ID, Location: loc}

	b.setBlock(posBlock)
	idxPos := indexVal
	posBlock.Term = &mir.Br{Target: mergeBlock.ID, Location: loc}

	b.setBlock(mergeBlock)
	idxAdj := b.gen.nextValueID()
	b.emitInstr(&mir.Phi{
		Result: idxAdj,
		Type:   indexType,
		Incoming: []mir.PhiIncoming{
			{Pred: negBlock.ID, Value: idxNeg},
			{Pred: posBlock.ID, Value: idxPos},
		},
		Location: loc,
	})

	condLow := b.emitBinary(tokens.LESS_TOKEN, idxAdj, zero, indexType, loc)
	condHigh := b.emitBinary(tokens.GREATER_EQUAL_TOKEN, idxAdj, lenVal, indexType, loc)
	condOOB := b.emitBinary(tokens.OR_TOKEN, condLow, condHigh, types.TypeBool, loc)
	mergeBlock.Term = &mir.CondBr{
		Cond:     condOOB,
		Then:     oobBlock.ID,
		Else:     okBlock.ID,
		Location: loc,
	}

	b.setBlock(oobBlock)
	msg := b.emitConst(types.TypeString, "index out of bounds", loc)
	b.emitInstr(&mir.Call{
		Result:   mir.InvalidValue,
		Target:   "ferret_global_panic",
		Args:     []mir.ValueID{msg},
		Type:     types.TypeVoid,
		Location: loc,
	})
	oobBlock.Term = &mir.Unreachable{Location: loc}

	b.setBlock(okBlock)
	return idxAdj
}

func (b *functionBuilder) emitInstr(instr mir.Instr) {
	if b.current == nil || b.current.Term != nil || instr == nil {
		return
	}
	b.current.Instrs = append(b.current.Instrs, instr)
}

func (b *functionBuilder) newBlock(name string, loc source.Location) *mir.Block {
	block := &mir.Block{
		ID:       b.gen.nextBlockID(),
		Name:     name,
		Location: loc,
	}
	b.fn.Blocks = append(b.fn.Blocks, block)
	return block
}

func (b *functionBuilder) setBlock(block *mir.Block) {
	b.current = block
}

func (b *functionBuilder) branchIfNoTerm(target mir.BlockID, loc source.Location) {
	if b.current == nil || b.current.Term != nil {
		return
	}
	b.current.Term = &mir.Br{Target: target, Location: loc}
}

func (b *functionBuilder) pushLoop(breakTarget, continueTarget mir.BlockID) {
	b.loopStack = append(b.loopStack, loopTargets{
		breakTarget:    breakTarget,
		continueTarget: continueTarget,
	})
}

func (b *functionBuilder) popLoop() {
	if len(b.loopStack) == 0 {
		return
	}
	b.loopStack = b.loopStack[:len(b.loopStack)-1]
}

func (b *functionBuilder) currentLoop() *loopTargets {
	if len(b.loopStack) == 0 {
		return nil
	}
	return &b.loopStack[len(b.loopStack)-1]
}

func (b *functionBuilder) reportUnsupported(kind string, loc *source.Location) {
	if b.gen.ctx == nil {
		return
	}
	message := fmt.Sprintf("MIR lowering unsupported: %s", kind)
	b.gen.ctx.ReportError(message, loc)
}

func (b *functionBuilder) castValue(value mir.ValueID, from, to types.SemType, loc source.Location) mir.ValueID {
	if from == nil || to == nil {
		return value
	}
	if ref, ok := types.UnwrapType(from).(*types.ReferenceType); ok {
		if _, ok := types.UnwrapType(to).(*types.ReferenceType); !ok {
			value = b.emitLoad(value, ref.Inner, loc)
			from = ref.Inner
		}
	}
	if from.Equals(to) {
		return value
	}
	if interfaceTypeOf(to) != nil {
		return b.boxInterfaceValue(value, from, to, loc)
	}
	if isLargePrimitiveType(from) || isLargePrimitiveType(to) {
		return b.emitLargeCast(value, from, to, loc)
	}
	return b.emitCast(value, to, loc)
}

type loopTargets struct {
	breakTarget    mir.BlockID
	continueTarget mir.BlockID
}

func assignTokenToBinary(token tokens.TOKEN) tokens.TOKEN {
	switch token {
	case tokens.PLUS_EQUALS_TOKEN:
		return tokens.PLUS_TOKEN
	case tokens.MINUS_EQUALS_TOKEN:
		return tokens.MINUS_TOKEN
	case tokens.MUL_EQUALS_TOKEN:
		return tokens.MUL_TOKEN
	case tokens.DIV_EQUALS_TOKEN:
		return tokens.DIV_TOKEN
	case tokens.MOD_EQUALS_TOKEN:
		return tokens.MOD_TOKEN
	case tokens.POW_EQUALS_TOKEN:
		return tokens.EXP_TOKEN
	case tokens.EXP_EQUALS_TOKEN:
		return tokens.BIT_XOR_TOKEN
	default:
		return ""
	}
}

func isCompareOp(op tokens.TOKEN) bool {
	switch op {
	case tokens.LESS_TOKEN, tokens.LESS_EQUAL_TOKEN, tokens.GREATER_TOKEN, tokens.GREATER_EQUAL_TOKEN,
		tokens.DOUBLE_EQUAL_TOKEN, tokens.NOT_EQUAL_TOKEN:
		return true
	default:
		return false
	}
}

func largeBinaryFunc(op tokens.TOKEN, typeName string) (string, bool) {
	switch op {
	case tokens.PLUS_TOKEN:
		return "ferret_" + typeName + "_add_ptr", true
	case tokens.MINUS_TOKEN:
		return "ferret_" + typeName + "_sub_ptr", true
	case tokens.MUL_TOKEN:
		return "ferret_" + typeName + "_mul_ptr", true
	case tokens.DIV_TOKEN:
		return "ferret_" + typeName + "_div_ptr", true
	case tokens.EXP_TOKEN:
		return "ferret_" + typeName + "_pow_ptr", true
	case tokens.MOD_TOKEN:
		if isLargeIntName(typeName) {
			return "ferret_" + typeName + "_mod_ptr", true
		}
	case tokens.BIT_AND_TOKEN:
		if isLargeIntName(typeName) {
			return "ferret_" + typeName + "_and_ptr", true
		}
	case tokens.BIT_OR_TOKEN:
		if isLargeIntName(typeName) {
			return "ferret_" + typeName + "_or_ptr", true
		}
	case tokens.BIT_XOR_TOKEN:
		if isLargeIntName(typeName) {
			return "ferret_" + typeName + "_xor_ptr", true
		}
	}
	return "", false
}

func largeFromSmallFunc(toName string, from types.SemType) (string, types.SemType, bool) {
	if from == nil {
		return "", nil, false
	}
	from = types.UnwrapType(from)
	if prim, ok := from.(*types.PrimitiveType); ok {
		if !types.IsNumericTypeName(prim.GetName()) {
			return "", nil, false
		}
		switch toName {
		case string(types.TYPE_I128), string(types.TYPE_I256):
			return "ferret_" + toName + "_from_i64_ptr", types.TypeI64, true
		case string(types.TYPE_U128), string(types.TYPE_U256):
			return "ferret_" + toName + "_from_u64_ptr", types.TypeU64, true
		case string(types.TYPE_F128), string(types.TYPE_F256):
			return "ferret_" + toName + "_from_f64_ptr", types.TypeF64, true
		}
	}
	return "", nil, false
}

func largeToSmallFunc(fromName string, to types.SemType) (string, types.SemType, bool) {
	if to == nil {
		return "", nil, false
	}
	to = types.UnwrapType(to)
	prim, ok := to.(*types.PrimitiveType)
	if !ok {
		return "", nil, false
	}
	if isLargeFloatName(fromName) {
		if types.IsFloatTypeName(prim.GetName()) || types.IsIntegerTypeName(prim.GetName()) {
			return "ferret_" + fromName + "_to_f64_ptr", types.TypeF64, true
		}
	}
	if isLargeIntName(fromName) {
		suffix := "i64"
		retType := types.TypeI64
		if fromName == string(types.TYPE_U128) || fromName == string(types.TYPE_U256) {
			suffix = "u64"
			retType = types.TypeU64
		}
		if types.IsIntegerTypeName(prim.GetName()) || types.IsFloatTypeName(prim.GetName()) {
			return "ferret_" + fromName + "_to_" + suffix + "_ptr", retType, true
		}
	}
	return "", nil, false
}
