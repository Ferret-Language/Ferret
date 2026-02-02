package gen

import (
	"fmt"
	"strconv"
	"strings"

	"compiler/internal/hir"
	"compiler/internal/hir/consteval"
	"compiler/internal/mir"
	"compiler/internal/semantics/narrowing"
	"compiler/internal/semantics/symbols"
	"compiler/internal/semantics/typechecker"
	"compiler/internal/source"
	"compiler/internal/tokens"
	"compiler/internal/types"
	"compiler/internal/utils/numeric"
)

type functionBuilder struct {
	gen             *Generator
	fn              *mir.Function
	current         *mir.Block
	paramsByName    map[string]mir.ValueID
	paramTypes      map[string]types.SemType
	slots           map[*symbols.Symbol]mir.ValueID
	tempSlots       map[*hir.Ident]mir.ValueID
	ptrElem         map[mir.ValueID]types.SemType
	loopStack       []loopTargets
	deferStack      []deferScope
	narrowedEntries map[string]*narrowing.NarrowingEntry
	retParam        mir.ValueID
	retType         types.SemType
	refOutParam     mir.ValueID
	refOutType      types.SemType
	refOutHeapParam mir.ValueID
	closureEnv      mir.ValueID
	captures        map[*symbols.Symbol]captureInfo
	boxed           map[*symbols.Symbol]mir.ValueID
	bindings        map[*symbols.Symbol]mir.ValueID
	refHeapSlots    map[*symbols.Symbol]mir.ValueID
	tempRefHeap     map[*hir.Ident]mir.ValueID
	refHeapParams   map[string]mir.ValueID
	entry           *mir.Block
	inDeferCatch    bool
	catchEndLabel   mir.BlockID
}

type deferScope struct {
	defers []*hir.DeferStmt
}

func newFunctionBuilder(gen *Generator, fn *mir.Function) *functionBuilder {
	return &functionBuilder{
		gen:             gen,
		fn:              fn,
		paramsByName:    make(map[string]mir.ValueID),
		paramTypes:      make(map[string]types.SemType),
		slots:           make(map[*symbols.Symbol]mir.ValueID),
		tempSlots:       make(map[*hir.Ident]mir.ValueID),
		ptrElem:         make(map[mir.ValueID]types.SemType),
		loopStack:       nil,
		narrowedEntries: nil,
		retParam:        mir.InvalidValue,
		refOutParam:     mir.InvalidValue,
		refOutHeapParam: mir.InvalidValue,
		closureEnv:      mir.InvalidValue,
		captures:        nil,
		boxed:           make(map[*symbols.Symbol]mir.ValueID),
		bindings:        make(map[*symbols.Symbol]mir.ValueID),
		refHeapSlots:    make(map[*symbols.Symbol]mir.ValueID),
		tempRefHeap:     make(map[*hir.Ident]mir.ValueID),
		refHeapParams:   make(map[string]mir.ValueID),
	}
}

func (b *functionBuilder) buildFuncBody(body *hir.Block) {
	entry := b.newBlock("entry", b.fn.Location)
	b.entry = entry
	b.setBlock(entry)

	// Push function-level defer scope
	b.pushDeferScope()

	b.collectRefHeapParams()
	for _, param := range b.fn.Params {
		if param.Name == outHeapParamName {
			b.refOutHeapParam = param.ID
			b.ptrElem[b.refOutHeapParam] = types.TypeU64
			continue
		}
		if isRefHeapParamName(param.Name) {
			continue
		}

		if param.Name != "" {
			b.paramsByName[param.Name] = param.ID
			b.paramTypes[param.Name] = param.Type
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

func (b *functionBuilder) collectRefHeapParams() {
	if b == nil || b.fn == nil || b.refHeapParams == nil {
		return
	}

	receiverName := ""
	if b.fn.Receiver != nil {
		receiverName = b.fn.Receiver.Name
	}

	userParamNames := make([]string, 0, len(b.fn.Params))
	for _, param := range b.fn.Params {
		if param.IsEnv || param.Name == "" || param.Name == "__ret" || param.Name == "__out" || param.Name == outHeapParamName {
			continue
		}
		if isRefHeapParamName(param.Name) {
			continue
		}
		if receiverName != "" && param.Name == receiverName {
			continue
		}
		userParamNames = append(userParamNames, param.Name)
	}

	for _, param := range b.fn.Params {
		if !isRefHeapParamName(param.Name) {
			continue
		}
		indexStr := strings.TrimPrefix(param.Name, refHeapParamPrefix)
		index, err := strconv.Atoi(indexStr)
		if err != nil {
			continue
		}
		if index == -1 {
			if receiverName != "" {
				b.refHeapParams[receiverName] = param.ID
			}
			continue
		}
		if index >= 0 && index < len(userParamNames) {
			b.refHeapParams[userParamNames[index]] = param.ID
		}
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

	// Emit deferred calls before implicit return
	b.emitDeferredCalls()

	if b.fn.Return != nil && b.fn.Return.Equals(types.TypeVoid) {
		b.current.Term = &mir.Return{HasValue: false, Location: b.current.Location}
		return
	}
	b.current.Term = &mir.Unreachable{Location: b.current.Location}
}

func (b *functionBuilder) withNarrowing(block *hir.Block, fn func()) {
	if fn == nil {
		return
	}
	if b == nil || b.gen == nil || b.gen.mod == nil || block == nil {
		fn()
		return
	}

	info := narrowing.GetNarrowingInfo(b.gen.mod)
	if info == nil {
		fn()
		return
	}

	scopeKey := block.NarrowingKey
	if scopeKey == "" && block.Location.Start != nil {
		filePath := b.gen.mod.FilePath
		if block.Location.Filename != nil && *block.Location.Filename != "" {
			filePath = *block.Location.Filename
		}
		scopeKey = narrowing.ScopeKeyFromLocation(filePath, block.Location.Start.Line, block.Location.Start.Column)
	}
	if scopeKey == "" {
		fn()
		return
	}

	narrowed := narrowing.NarrowedEntriesForScope(info, scopeKey)
	if len(narrowed) == 0 {
		fn()
		return
	}

	prevNarrowed := b.narrowedEntries
	nextNarrowed := make(map[string]*narrowing.NarrowingEntry, len(prevNarrowed)+len(narrowed))
	for name, entry := range prevNarrowed {
		nextNarrowed[name] = entry
	}
	for name, entry := range narrowed {
		nextNarrowed[name] = entry
	}
	b.narrowedEntries = nextNarrowed
	defer func() {
		b.narrowedEntries = prevNarrowed
	}()

	fn()
}

func (b *functionBuilder) lowerBlock(block *hir.Block) {
	if block == nil || b.current == nil {
		return
	}

	b.withNarrowing(block, func() {
		for _, node := range block.Nodes {
			b.lowerNode(node)
			if b.current.Term != nil {
				return
			}
		}
	})
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
	case *hir.DeferStmt:
		b.lowerDefer(n)
	case *hir.ExprStmt:
		b.lowerExpr(n.X)
	case *hir.Block:
		// Standalone block - push/pop defer scope
		b.pushDeferScope()
		b.lowerBlock(n)
		b.popDeferScope()
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

	var refInner types.SemType
	isRefDecl := false
	if ref, ok := types.UnwrapType(typ).(*types.ReferenceType); ok {
		isRefDecl = true
		refInner = ref.Inner
	}

	isGlobal := b.isModuleGlobalIdent(item.Name)

	if !isGlobal && item.Value != nil {
		if heapExpr, ok := item.Value.(*hir.UnaryExpr); ok && heapExpr.Op.Kind == tokens.HASH_TOKEN {
			val := b.lowerExpr(heapExpr.X)
			if val != mir.InvalidValue {
				val = b.coerceValueForAssign(val, b.exprType(heapExpr.X), typ, item.Name.Location)
			}
			heapAddr := b.emitHeapAlloc(val, typ, item.Name.Location)
			if heapAddr != mir.InvalidValue {
				if item.Name.Symbol != nil {
					b.slots[item.Name.Symbol] = heapAddr
					b.boxed[item.Name.Symbol] = heapAddr
					if _, ok := b.bindings[item.Name.Symbol]; !ok {
						bind := b.emitAlloca(types.NewReference(typ), item.Name.Location)
						b.emitStore(bind, heapAddr, item.Name.Location)
						b.bindings[item.Name.Symbol] = bind
					}
				} else {
					b.tempSlots[item.Name] = heapAddr
				}
			}
			return
		}
	}

	if !isGlobal && item.Value != nil {
		if moveExpr, ok := item.Value.(*hir.UnaryExpr); ok && moveExpr.Op.Kind == tokens.AT_TOKEN {
			if ident, ok := moveExpr.X.(*hir.Ident); ok && ident.Symbol != nil {
				if b.isHeapLValue(ident) {
					addr := b.addrForIdent(ident)
					if addr != mir.InvalidValue {
						if item.Name.Symbol != nil {
							b.slots[item.Name.Symbol] = addr
							b.boxed[item.Name.Symbol] = addr
							if _, ok := b.bindings[item.Name.Symbol]; !ok {
								bind := b.emitAlloca(types.NewReference(typ), item.Name.Location)
								b.emitStore(bind, addr, item.Name.Location)
								b.bindings[item.Name.Symbol] = bind
							}
						} else {
							b.tempSlots[item.Name] = addr
						}
						b.resetHeapBinding(ident, moveExpr.Location)
						return
					}
				}
			}
		}
	}

	if !isGlobal && item.Value != nil {
		if call, ok := item.Value.(*hir.CallExpr); ok {
			if heapRet, ok := b.isHeapReturnCall(call); ok {
				heapAddr := b.lowerHeapReturnCall(call, heapRet, item.Name.Location)
				if heapAddr != mir.InvalidValue {
					if item.Name.Symbol != nil {
						b.slots[item.Name.Symbol] = heapAddr
						b.boxed[item.Name.Symbol] = heapAddr
						if _, ok := b.bindings[item.Name.Symbol]; !ok {
							bind := b.emitAlloca(types.NewReference(typ), item.Name.Location)
							b.emitStore(bind, heapAddr, item.Name.Location)
							b.bindings[item.Name.Symbol] = bind
						}
					} else {
						b.tempSlots[item.Name] = heapAddr
					}
				}
				return
			}
		}
	}

	var addr mir.ValueID
	if isGlobal {
		if item.Name.Symbol == nil {
			return
		}
		if globalAddr, _, ok := b.moduleGlobalStorageAddr(item.Name.Symbol, item.Name.Location); ok {
			addr = globalAddr
		} else {
			return
		}
		if item.Name.Symbol.IsHeap {
			if item.Value != nil {
				if heapExpr, ok := item.Value.(*hir.UnaryExpr); ok && heapExpr.Op.Kind == tokens.HASH_TOKEN {
					val := b.lowerExpr(heapExpr.X)
					if val != mir.InvalidValue {
						val = b.coerceValueForAssign(val, b.exprType(heapExpr.X), typ, item.Name.Location)
					}
					heapAddr := b.emitHeapAlloc(val, typ, item.Name.Location)
					if heapAddr != mir.InvalidValue {
						b.emitStore(addr, heapAddr, item.Name.Location)
					}
					return
				}
				if call, ok := item.Value.(*hir.CallExpr); ok {
					if heapRet, ok := b.isHeapReturnCall(call); ok {
						heapAddr := b.lowerHeapReturnCall(call, heapRet, item.Name.Location)
						if heapAddr != mir.InvalidValue {
							b.emitStore(addr, heapAddr, item.Name.Location)
						}
						return
					}
				}
				if moveExpr, ok := item.Value.(*hir.UnaryExpr); ok && moveExpr.Op.Kind == tokens.AT_TOKEN {
					if ident, ok := moveExpr.X.(*hir.Ident); ok && ident.Symbol != nil && b.isHeapLValue(ident) {
						heapAddr := b.addrForIdent(ident)
						if heapAddr != mir.InvalidValue {
							b.emitStore(addr, heapAddr, item.Name.Location)
							b.resetHeapBinding(ident, moveExpr.Location)
						}
						return
					}
				}
			}
			return
		}
	} else {
		addr = b.emitAlloca(typ, item.Name.Location)
		if item.Name.Symbol != nil {
			b.slots[item.Name.Symbol] = addr
			b.bindings[item.Name.Symbol] = addr
		} else {
			b.tempSlots[item.Name] = addr
		}
	}

	if item.Value != nil {
		if helper, ok := assignHelperForType(typ); ok && !isMoveExpr(item.Value) {
			zero := b.nullPointerValue(typ, item.Name.Location)
			b.emitStore(addr, zero, item.Name.Location)
			val := b.lowerExpr(item.Value)
			if val != mir.InvalidValue {
				val = b.coerceValueForAssign(val, b.exprType(item.Value), typ, item.Name.Location)
				if val != mir.InvalidValue {
					b.emitInstr(&mir.Call{
						Result:   mir.InvalidValue,
						Target:   helper,
						Args:     []mir.ValueID{addr, val},
						Type:     types.TypeVoid,
						Location: item.Name.Location,
					})
				}
			}
			return
		}
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
			if arrType, ok := types.UnwrapType(typ).(*types.ArrayType); ok && arrType.Length < 0 {
				val := b.lowerExpr(item.Value)
				if val != mir.InvalidValue {
					val = b.coerceValueForAssign(val, b.exprType(item.Value), typ, item.Name.Location)
					if val != mir.InvalidValue {
						b.emitInstr(&mir.Store{
							Addr:     addr,
							Value:    val,
							Location: item.Name.Location,
						})
					}
				}
				return
			}
			if _, ok := types.UnwrapType(typ).(*types.MapType); ok {
				val := b.lowerExpr(item.Value)
				if val != mir.InvalidValue {
					val = b.coerceValueForAssign(val, b.exprType(item.Value), typ, item.Name.Location)
					if val != mir.InvalidValue {
						b.emitInstr(&mir.Store{
							Addr:     addr,
							Value:    val,
							Location: item.Name.Location,
						})
					}
				}
				return
			}
		}
		if _, ok := item.Value.(*hir.RangeExpr); ok {
			if arrType, ok := types.UnwrapType(typ).(*types.ArrayType); ok && arrType.Length < 0 {
				val := b.lowerExpr(item.Value)
				if val != mir.InvalidValue {
					val = b.coerceValueForAssign(val, b.exprType(item.Value), typ, item.Name.Location)
					if val != mir.InvalidValue {
						b.emitInstr(&mir.Store{
							Addr:     addr,
							Value:    val,
							Location: item.Name.Location,
						})
					}
				}
				return
			}
		}

		val := b.lowerExpr(item.Value)
		if val != mir.InvalidValue {
			heapVal := mir.InvalidValue
			if isRefDecl {
				heapVal = b.computeBorrowHeap(item.Value, val, refInner, item.Name.Location)
			}
			val = b.coerceValueForAssign(val, b.exprType(item.Value), typ, item.Name.Location)
			if val != mir.InvalidValue {
				if isMoveExpr(item.Value) {
					b.emitStoreMove(addr, val, item.Name.Location)
				} else {
					b.emitStore(addr, val, item.Name.Location)
				}
				if isRefDecl {
					if heapVal == mir.InvalidValue {
						heapVal = b.zeroU64(item.Name.Location)
					}
					b.storeRefHeapForIdent(item.Name, heapVal, item.Name.Location)
				}
			}
		}
	}
}

func (b *functionBuilder) lowerAssign(stmt *hir.AssignStmt) {
	if stmt == nil {
		return
	}

	rhsIsMove := stmt.Rhs != nil && isMoveExpr(stmt.Rhs)
	rebindRef := false
	var refInner types.SemType
	var lhsIdent *hir.Ident
	if ident, ok := stmt.Lhs.(*hir.Ident); ok {
		lhsIdent = ident
	}
	lhsIsGlobal := lhsIdent != nil && b.isModuleGlobalIdent(lhsIdent)

	if (stmt.Op == nil || stmt.Op.Kind == tokens.EQUALS_TOKEN) && rhsIsMove {
		if moveExpr, ok := stmt.Rhs.(*hir.UnaryExpr); ok && moveExpr.Op.Kind == tokens.AT_TOKEN {
			if srcIdent, ok := moveExpr.X.(*hir.Ident); ok && b.isHeapLValue(srcIdent) {
				if dstIdent, ok := stmt.Lhs.(*hir.Ident); ok && dstIdent.Symbol != nil {
					heapAddr := b.addrForIdent(srcIdent)
					if heapAddr != mir.InvalidValue {
						if lhsIsGlobal && dstIdent.Symbol.IsHeap {
							if storageAddr, _, ok := b.moduleGlobalStorageAddr(dstIdent.Symbol, stmt.Location); ok {
								b.emitStore(storageAddr, heapAddr, stmt.Location)
								b.resetHeapBinding(srcIdent, stmt.Location)
								return
							}
						}
						if !lhsIsGlobal {
							b.slots[dstIdent.Symbol] = heapAddr
							b.boxed[dstIdent.Symbol] = heapAddr
							if bind, ok := b.bindings[dstIdent.Symbol]; ok {
								if elem, ok := b.ptrElem[bind]; ok {
									if _, ok := types.UnwrapType(elem).(*types.ReferenceType); ok {
										b.emitStore(bind, heapAddr, stmt.Location)
									}
								}
							} else {
								lhsType := b.exprType(stmt.Lhs)
								if lhsType == nil {
									lhsType = types.TypeUnknown
								}
								bind := b.emitAlloca(types.NewReference(lhsType), stmt.Location)
								b.emitStore(bind, heapAddr, stmt.Location)
								b.bindings[dstIdent.Symbol] = bind
							}
							b.resetHeapBinding(srcIdent, stmt.Location)
							return
						}
					}
				}
			}
		}
	}

	if stmt.Op == nil || stmt.Op.Kind == tokens.EQUALS_TOKEN {
		if call, ok := stmt.Rhs.(*hir.CallExpr); ok {
			if heapRet, ok := b.isHeapReturnCall(call); ok {
				if dstIdent, ok := stmt.Lhs.(*hir.Ident); ok && dstIdent.Symbol != nil {
					heapAddr := b.lowerHeapReturnCall(call, heapRet, stmt.Location)
					if heapAddr != mir.InvalidValue {
						if lhsIsGlobal && dstIdent.Symbol.IsHeap {
							if storageAddr, _, ok := b.moduleGlobalStorageAddr(dstIdent.Symbol, stmt.Location); ok {
								b.emitStore(storageAddr, heapAddr, stmt.Location)
								return
							}
						}
						if !lhsIsGlobal {
							b.slots[dstIdent.Symbol] = heapAddr
							b.boxed[dstIdent.Symbol] = heapAddr
							if bind, ok := b.bindings[dstIdent.Symbol]; ok {
								if elem, ok := b.ptrElem[bind]; ok {
									if _, ok := types.UnwrapType(elem).(*types.ReferenceType); ok {
										b.emitStore(bind, heapAddr, stmt.Location)
									}
								}
							} else {
								lhsType := b.exprType(stmt.Lhs)
								if lhsType == nil {
									lhsType = types.TypeUnknown
								}
								bind := b.emitAlloca(types.NewReference(lhsType), stmt.Location)
								b.emitStore(bind, heapAddr, stmt.Location)
								b.bindings[dstIdent.Symbol] = bind
							}
							return
						}
					}
				}
			}
		}
	}

	if idx, ok := stmt.Lhs.(*hir.IndexExpr); ok {
		b.lowerIndexAssign(idx, stmt.Rhs, stmt.Op, stmt.Location)
		return
	}

	if stmt.Op == nil || stmt.Op.Kind == tokens.EQUALS_TOKEN {
		if heapExpr, ok := stmt.Rhs.(*hir.UnaryExpr); ok && heapExpr.Op.Kind == tokens.HASH_TOKEN {
			if ident, ok := stmt.Lhs.(*hir.Ident); ok {
				val := b.lowerExpr(heapExpr.X)
				if val != mir.InvalidValue {
					val = b.coerceValueForAssign(val, b.exprType(heapExpr.X), b.exprType(stmt.Lhs), stmt.Location)
				}
				heapAddr := b.emitHeapAlloc(val, b.exprType(stmt.Lhs), stmt.Location)
				if heapAddr != mir.InvalidValue {
					if lhsIsGlobal && ident.Symbol != nil && ident.Symbol.IsHeap {
						if storageAddr, _, ok := b.moduleGlobalStorageAddr(ident.Symbol, stmt.Location); ok {
							b.emitStore(storageAddr, heapAddr, stmt.Location)
							return
						}
					}
					if !lhsIsGlobal {
						if ident.Symbol != nil {
							b.slots[ident.Symbol] = heapAddr
							b.boxed[ident.Symbol] = heapAddr
						} else {
							b.tempSlots[ident] = heapAddr
						}
						return
					}
				}
			}
		}
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
			// Check if RHS is a borrow expression (&x or &mut x)
			// If so, we're rebinding the reference variable to point to a new location
			// Otherwise, we're assigning through the reference to modify the pointed-to value
			isBorrowRhs := false
			if unary, ok := stmt.Rhs.(*hir.UnaryExpr); ok {
				if unary.Op.Kind == tokens.BIT_AND_TOKEN || unary.Op.Kind == tokens.MUT_TOKEN {
					isBorrowRhs = true
				}
			}

			if !isBorrowRhs {
				// Assigning through reference: *v = value
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

				if helper, ok := assignHelperForType(ref.Inner); ok && !rhsIsMove {
					rhs := b.lowerExpr(stmt.Rhs)
					if rhs == mir.InvalidValue {
						return
					}
					rhs = b.coerceValueForAssign(rhs, b.exprType(stmt.Rhs), ref.Inner, stmt.Location)
					if rhs == mir.InvalidValue {
						return
					}
					b.emitInstr(&mir.Call{
						Result:   mir.InvalidValue,
						Target:   helper,
						Args:     []mir.ValueID{refPtr, rhs},
						Type:     types.TypeVoid,
						Location: stmt.Location,
					})
					return
				}

				if b.isDynamicArrayLiteralExpr(stmt.Rhs, ref.Inner) {
					rhs := b.lowerExpr(stmt.Rhs)
					if rhs == mir.InvalidValue {
						return
					}
					rhs = b.coerceValueForAssign(rhs, b.exprType(stmt.Rhs), ref.Inner, stmt.Location)
					if rhs == mir.InvalidValue {
						return
					}
					b.emitInstr(&mir.Store{
						Addr:     refPtr,
						Value:    rhs,
						Location: stmt.Location,
					})
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
				if rhsIsMove {
					b.emitStoreMove(refPtr, rhs, stmt.Location)
				} else {
					b.emitStore(refPtr, rhs, stmt.Location)
				}
				return
			}
			rebindRef = true
			refInner = ref.Inner
			if ident, ok := unwrapParenExpr(stmt.Lhs).(*hir.Ident); ok {
				lhsIdent = ident
			}
			// Fall through to handle reference rebinding (v = &mut arr[i])
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

	if helper, ok := assignHelperForType(lhsStorageType); ok && !rhsIsMove {
		rhs := b.lowerExpr(stmt.Rhs)
		if rhs == mir.InvalidValue {
			return
		}
		rhs = b.coerceValueForAssign(rhs, b.exprType(stmt.Rhs), lhsStorageType, stmt.Location)
		if rhs == mir.InvalidValue {
			return
		}
		b.emitInstr(&mir.Call{
			Result:   mir.InvalidValue,
			Target:   helper,
			Args:     []mir.ValueID{addr, rhs},
			Type:     types.TypeVoid,
			Location: stmt.Location,
		})
		return
	}

	if b.isDynamicArrayLiteralExpr(stmt.Rhs, lhsStorageType) || b.isMapLiteralExpr(stmt.Rhs, lhsStorageType) {
		rhs := b.lowerExpr(stmt.Rhs)
		if rhs == mir.InvalidValue {
			return
		}
		rhs = b.coerceValueForAssign(rhs, b.exprType(stmt.Rhs), lhsStorageType, stmt.Location)
		if rhs == mir.InvalidValue {
			return
		}
		b.emitInstr(&mir.Store{
			Addr:     addr,
			Value:    rhs,
			Location: stmt.Location,
		})
		return
	}

	rhs := b.lowerExpr(stmt.Rhs)
	if rhs == mir.InvalidValue {
		return
	}
	heapVal := mir.InvalidValue
	if rebindRef && lhsIdent != nil {
		heapVal = b.computeBorrowHeap(stmt.Rhs, rhs, refInner, stmt.Location)
	}
	rhs = b.coerceValueForAssign(rhs, b.exprType(stmt.Rhs), lhsStorageType, stmt.Location)
	if rhs == mir.InvalidValue {
		return
	}
	if rhsIsMove {
		b.emitStoreMove(addr, rhs, stmt.Location)
	} else {
		b.emitStore(addr, rhs, stmt.Location)
	}
	if rebindRef && lhsIdent != nil {
		if heapVal == mir.InvalidValue {
			heapVal = b.zeroU64(stmt.Location)
		}
		b.storeRefHeapForIdent(lhsIdent, heapVal, stmt.Location)
	}
}

func (b *functionBuilder) lowerReturn(stmt *hir.ReturnStmt) {
	if stmt == nil {
		return
	}

	if b.inDeferCatch {
		// Void returns in defer catch are diagnostic-only and should not return from the function
		if stmt.Result == nil {
			// Code after return is unreachable
			b.current.Term = &mir.Br{Target: b.catchEndLabel, Location: stmt.Location}
			return
		}
		// If somehow there's a result, treat as error (should be caught by typechecker)
		b.current.Term = &mir.Unreachable{Location: stmt.Location}
		return
	}

	// Emit deferred calls in LIFO order before returning
	b.emitDeferredCalls()

	if stmt.Result == nil {
		b.current.Term = &mir.Return{HasValue: false, Location: stmt.Location}
		return
	}

	if heapRet, ok := b.heapReturnType(b.fn.Name); ok {
		heapPtr := b.lowerHeapReturnValue(stmt.Result, heapRet, stmt.Location)
		if heapPtr == mir.InvalidValue {
			b.current.Term = &mir.Unreachable{Location: stmt.Location}
			return
		}
		b.current.Term = &mir.Return{HasValue: true, Value: heapPtr, Location: stmt.Location}
		return
	}

	resultType := b.exprType(stmt.Result)
	retType := b.fn.Return
	if b.retParam != mir.InvalidValue {
		retType = b.retType
	}
	if b.refOutParam != mir.InvalidValue {
		if borrow, ok := stmt.Result.(*hir.UnaryExpr); ok {
			if borrow.Op.Kind == tokens.BIT_AND_TOKEN || borrow.Op.Kind == tokens.MUT_TOKEN {
				val := b.lowerExpr(borrow.X)
				if val == mir.InvalidValue {
					b.current.Term = &mir.Unreachable{Location: stmt.Location}
					return
				}
				val = b.coerceValueForAssign(val, b.exprType(borrow.X), b.refOutType, stmt.Location)
				if val == mir.InvalidValue {
					b.current.Term = &mir.Unreachable{Location: stmt.Location}
					return
				}
				b.emitStore(b.refOutParam, val, stmt.Location)
				if b.refOutHeapParam != mir.InvalidValue {
					heapVal := b.heapFromValue(val, b.refOutType, stmt.Location)
					if heapVal == mir.InvalidValue {
						heapVal = b.zeroU64(stmt.Location)
					}
					b.emitStore(b.refOutHeapParam, heapVal, stmt.Location)
				}
				b.current.Term = &mir.Return{HasValue: true, Value: b.refOutParam, Location: stmt.Location}
				return
			}
		}
	}

	val := b.lowerExpr(stmt.Result)
	if val == mir.InvalidValue {
		b.current.Term = &mir.Unreachable{Location: stmt.Location}
		return
	}
	val = b.coerceValueForAssign(val, resultType, retType, stmt.Location)
	if val == mir.InvalidValue {
		b.current.Term = &mir.Unreachable{Location: stmt.Location}
		return
	}

	resultIsMove := isMoveExpr(stmt.Result)
	if !resultIsMove {
		if _, ok := dynamicArrayValueType(retType); ok {
			if !b.isDynamicArrayLiteralExpr(stmt.Result, retType) {
				val = b.emitArrayClone(val, retType, stmt.Location)
			}
		} else if _, ok := mapValueType(retType); ok {
			if !b.isMapLiteralExpr(stmt.Result, retType) {
				val = b.emitMapClone(val, retType, stmt.Location)
			}
		}
	}

	if b.refOutParam != mir.InvalidValue {
		if resultType != nil {
			if refType, ok := types.UnwrapType(resultType).(*types.ReferenceType); ok {
				if b.refOutHeapParam != mir.InvalidValue {
					heapVal := b.computeBorrowHeap(stmt.Result, val, refType.Inner, stmt.Location)
					if heapVal == mir.InvalidValue {
						heapVal = b.zeroU64(stmt.Location)
					}
					b.emitStore(b.refOutHeapParam, heapVal, stmt.Location)
				}
				b.current.Term = &mir.Return{HasValue: true, Value: val, Location: stmt.Location}
				return
			}
		}
		// Return-by-ref: copy the value into the out param.
		// This prevents returning pointers to stack-allocated interface boxes.
		b.emitMemcpy(b.refOutParam, val, retType, stmt.Location)
		if b.refOutHeapParam != mir.InvalidValue {
			heapVal := b.heapFromValue(val, retType, stmt.Location)
			if heapVal == mir.InvalidValue {
				heapVal = b.zeroU64(stmt.Location)
			}
			b.emitStore(b.refOutHeapParam, heapVal, stmt.Location)
		}
		b.current.Term = &mir.Return{HasValue: true, Value: b.refOutParam, Location: stmt.Location}
		return
	}

	if b.retParam != mir.InvalidValue {
		// Return-by-value via out param: always copy the payload.
		if !resultIsMove && typeNeedsDeepCopy(retType) {
			b.emitDeepCopy(b.retParam, val, retType, stmt.Location)
		} else {
			b.emitMemcpy(b.retParam, val, retType, stmt.Location)
		}
		b.current.Term = &mir.Return{HasValue: false, Location: stmt.Location}
		return
	}

	b.current.Term = &mir.Return{HasValue: true, Value: val, Location: stmt.Location}
}

func (b *functionBuilder) heapReturnType(name string) (types.SemType, bool) {
	if b == nil || b.gen == nil {
		return nil, false
	}
	return b.gen.heapReturnType(name)
}

func (b *functionBuilder) lowerHeapReturnValue(expr hir.Expr, inner types.SemType, loc source.Location) mir.ValueID {
	expr = unwrapParenExpr(expr)
	if expr == nil {
		return mir.InvalidValue
	}

	if unary, ok := expr.(*hir.UnaryExpr); ok && unary.Op.Kind == tokens.AT_TOKEN {
		if ident, ok := unary.X.(*hir.Ident); ok && b.isHeapLValue(ident) {
			return b.addrForIdent(ident)
		}
	}

	val := b.lowerExpr(expr)
	if val == mir.InvalidValue {
		return mir.InvalidValue
	}
	val = b.coerceValueForAssign(val, b.exprType(expr), inner, loc)
	if val == mir.InvalidValue {
		return mir.InvalidValue
	}
	return b.emitHeapAlloc(val, inner, loc)
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

func (b *functionBuilder) lowerDefer(stmt *hir.DeferStmt) {
	if stmt == nil {
		return
	}

	// Add to current defer scope
	if len(b.deferStack) > 0 {
		currentScope := &b.deferStack[len(b.deferStack)-1]
		currentScope.defers = append(currentScope.defers, stmt)
	}
}

// pushDeferScope creates a new defer scope for a block/loop iteration
func (b *functionBuilder) pushDeferScope() {
	b.deferStack = append(b.deferStack, deferScope{defers: nil})
}

// popDeferScope emits deferred calls from the current scope in LIFO order and pops the scope
func (b *functionBuilder) popDeferScope() {
	if len(b.deferStack) == 0 {
		return
	}

	// Get current scope
	currentScope := b.deferStack[len(b.deferStack)-1]

	// Emit deferred calls in reverse order (LIFO)
	for i := len(currentScope.defers) - 1; i >= 0; i-- {
		deferStmt := currentScope.defers[i]
		b.emitDeferredCall(deferStmt)
	}

	// Pop the scope
	b.deferStack = b.deferStack[:len(b.deferStack)-1]
}

// emitDeferredCalls emits all deferred calls from all scopes in LIFO order
func (b *functionBuilder) emitDeferredCalls() {
	// Emit from innermost to outermost scope
	for i := len(b.deferStack) - 1; i >= 0; i-- {
		scope := b.deferStack[i]
		// Emit calls in reverse order within each scope
		for j := len(scope.defers) - 1; j >= 0; j-- {
			deferStmt := scope.defers[j]
			b.emitDeferredCall(deferStmt)
		}
	}
}

// emitDeferredCall emits a single deferred call, handling catch blocks if present
func (b *functionBuilder) emitDeferredCall(deferStmt *hir.DeferStmt) {
	if deferStmt == nil || deferStmt.Call == nil {
		return
	}

	// If there's no catch clause, just emit the call
	if deferStmt.Catch == nil {
		b.lowerExpr(deferStmt.Call)
		return
	}

	// Handle deferred call with catch block
	// Check if the call returns a result type
	callType := b.exprType(deferStmt.Call)
	resultType, isResult := types.UnwrapType(callType).(*types.ResultType)
	if !isResult || resultType == nil {
		// Not a result type, just emit the call
		b.lowerExpr(deferStmt.Call)
		return
	}

	// Execute the call
	callValue := b.lowerExpr(deferStmt.Call)
	if callValue == mir.InvalidValue {
		return
	}

	// Check if result is ok or error
	isOk := b.gen.nextValueID()
	b.emitInstr(&mir.ResultIsOk{
		Result:   isOk,
		Value:    callValue,
		Location: deferStmt.Location,
	})

	okBlock := b.newBlock("defer.ok", deferStmt.Location)
	errBlock := b.newBlock("defer.err", deferStmt.Location)
	mergeBlock := b.newBlock("defer.merge", deferStmt.Location)

	b.current.Term = &mir.CondBr{
		Cond:     isOk,
		Then:     okBlock.ID,
		Else:     errBlock.ID,
		Location: deferStmt.Location,
	}

	// Ok branch - do nothing, just continue
	b.setBlock(okBlock)
	b.branchIfNoTerm(mergeBlock.ID, deferStmt.Location)

	// Error branch - execute catch handler
	b.setBlock(errBlock)
	if deferStmt.Catch.ErrIdent != nil {
		errType := resultType.Err
		if errType != nil {
			errVal := b.gen.nextValueID()
			b.emitInstr(&mir.ResultUnwrap{
				Result:     errVal,
				Value:      callValue,
				Default:    mir.InvalidValue,
				HasDefault: false,
				Type:       errType,
				Location:   deferStmt.Location,
			})
			b.bindCatchIdent(deferStmt.Catch.ErrIdent, errVal, errType)
		}
	}
	if deferStmt.Catch.Handler != nil {
		b.inDeferCatch = true
		b.catchEndLabel = mergeBlock.ID
		b.lowerBlock(deferStmt.Catch.Handler)
		b.inDeferCatch = false
	}
	b.branchIfNoTerm(mergeBlock.ID, deferStmt.Location)

	// Continue after defer
	b.setBlock(mergeBlock)
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
		// Push defer scope for loop iteration
		b.pushDeferScope()
		b.lowerBlock(stmt.Body)
		// Pop defer scope at end of iteration
		b.popDeferScope()
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
		// For non-switch cases, generate if-else chain
		// We need to handle different pattern types differently
		current := b.current

		for idx, entry := range entries {
			clause := entry.clause
			if clause.Pattern == nil {
				// Default case - handle at the end
				continue
			}

			var elseTarget mir.BlockID
			var elseBlock *mir.Block
			if idx < len(entries)-1 {
				elseBlock = b.newBlock("match.check", entry.clause.Location)
				elseTarget = elseBlock.ID
			} else {
				elseTarget = defaultBlock.ID
			}

			var cmp mir.ValueID

			// Handle different pattern types
			switch pat := clause.Pattern.(type) {
			case *hir.TypeCheckPattern:
				// Type check pattern: is Type
				if unionType, ok := types.UnwrapType(matchType).(*types.UnionType); ok {
					// Find variant index
					variantIndex := -1
					for i, variant := range unionType.Variants {
						if types.UnwrapType(variant).Equals(types.UnwrapType(pat.Type)) {
							variantIndex = i
							break
						}
					}
					if variantIndex >= 0 {
						// cond is a pointer to the union, load discriminant from offset 0
						var unionPtr mir.ValueID
						// Check if cond is already a pointer type
						if _, isPtr := b.ptrElem[cond]; isPtr {
							unionPtr = cond
						} else {
							// cond is a value, we need its address (shouldn't happen for unions)
							// Unions should always be passed by reference
							unionPtr = cond
						}

						tagSlot := b.emitPtrAdd(unionPtr, 0, types.TypeI32, clause.Location)
						tagValue := b.emitLoad(tagSlot, types.TypeI32, clause.Location)

						// Compare with expected variant index
						expectedTag := b.emitConst(types.TypeI32, strconv.Itoa(variantIndex), clause.Location)
						cmp = b.emitBinary(tokens.DOUBLE_EQUAL_TOKEN, tagValue, expectedTag, types.TypeI32, clause.Location)
					} else {
						// Type not found in union - always false
						cmp = b.emitConst(types.TypeBool, "0", clause.Location)
					}
				} else {
					// Not a union type - cannot do type check
					b.reportUnsupported("type check pattern on non-union type", clause.Pattern.Loc())
					cmp = b.emitConst(types.TypeBool, "0", clause.Location)
				}

			case *hir.RangeCheckPattern:
				// Range check pattern: in Range
				if rangeExpr, ok := pat.Range.(*hir.RangeExpr); ok {
					// Generate: cond >= start && cond < end (or <= for inclusive)
					startVal := b.lowerExpr(rangeExpr.Start)
					endVal := b.lowerExpr(rangeExpr.End)

					// Check lower bound: cond >= start
					lowerCmp := b.emitBinary(tokens.GREATER_EQUAL_TOKEN, cond, startVal, matchType, clause.Location)

					// Check upper bound: cond < end or cond <= end
					var upperCmp mir.ValueID
					if rangeExpr.Inclusive {
						upperCmp = b.emitBinary(tokens.LESS_EQUAL_TOKEN, cond, endVal, matchType, clause.Location)
					} else {
						upperCmp = b.emitBinary(tokens.LESS_TOKEN, cond, endVal, matchType, clause.Location)
					}

					// Combine: lower && upper
					cmp = b.emitBinary(tokens.AND_TOKEN, lowerCmp, upperCmp, types.TypeBool, clause.Location)
				} else {
					b.reportUnsupported("invalid range in range check pattern", clause.Pattern.Loc())
					cmp = b.emitConst(types.TypeBool, "0", clause.Location)
				}

			default:
				// Regular value match pattern
				value, ok := b.matchCaseConstValue(clause.Pattern, matchType)
				if !ok {
					b.reportUnsupported("match pattern", clause.Pattern.Loc())
					// Skip this case
					if elseBlock != nil {
						current = elseBlock
					}
					continue
				}

				if isLargePrimitiveType(matchType) {
					cmp = b.emitLargeCompare(tokens.DOUBLE_EQUAL_TOKEN, cond, value, matchType, clause.Location)
				} else {
					cmp = b.emitBinary(tokens.DOUBLE_EQUAL_TOKEN, cond, value, matchType, clause.Location)
				}
			}

			current.Term = &mir.CondBr{
				Cond:     cmp,
				Then:     entry.block.ID,
				Else:     elseTarget,
				Location: clause.Location,
			}

			if elseBlock != nil {
				b.setBlock(elseBlock)
				current = elseBlock
			}
		}

		// Ensure the last check block branches to default if no match
		if current != nil && current.Term == nil {
			current.Term = &mir.Br{
				Target:   defaultBlock.ID,
				Location: stmt.Location,
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

		// Special handling for 'in' operator: x in a..b => x >= a && x < b
		if e.Op.Kind == tokens.IN_TOKEN {
			return b.emitRangeCheck(e)
		}

		// Short-circuit evaluation for && and ||
		if e.Op.Kind == tokens.AND_TOKEN || e.Op.Kind == tokens.OR_TOKEN {
			return b.emitShortCircuitBinary(e)
		}

		left := b.lowerExpr(e.X)
		if left == mir.InvalidValue {
			return mir.InvalidValue
		}
		leftType := b.exprType(e.X)

		right := b.lowerExpr(e.Y)
		if right == mir.InvalidValue {
			return mir.InvalidValue
		}
		rightType := b.exprType(e.Y)

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
		if isCompareOp(e.Op.Kind) {
			leftBase := types.UnwrapType(leftType)
			rightBase := types.UnwrapType(rightType)
			leftIsFloat := types.IsFloat(leftBase) || types.IsUntypedFloat(leftType)
			rightIsFloat := types.IsFloat(rightBase) || types.IsUntypedFloat(rightType)
			if (leftIsFloat || rightIsFloat) && !isLargePrimitiveType(leftType) && !isLargePrimitiveType(rightType) {
				target := compareFloatType(leftType, rightType)
				if target != nil && !target.Equals(types.TypeUnknown) {
					if leftType != nil && types.IsNumeric(types.UnwrapType(leftType)) && !leftType.Equals(target) {
						left = b.castValue(left, leftType, target, e.Location)
						leftType = target
					}
					if rightType != nil && types.IsNumeric(types.UnwrapType(rightType)) && !rightType.Equals(target) {
						right = b.castValue(right, rightType, target, e.Location)
						rightType = target
					}
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
		if e.Op.Kind == tokens.BIT_AND_TOKEN || e.Op.Kind == tokens.MUT_TOKEN {
			refVal := b.lowerLValue(e.X)
			if refVal == mir.InvalidValue {
				return mir.InvalidValue
			}
			return refVal
		}
		if e.Op.Kind == tokens.AT_TOKEN {
			operand := b.lowerExpr(e.X)
			if operand == mir.InvalidValue {
				return mir.InvalidValue
			}
			if ident, ok := e.X.(*hir.Ident); ok && b.isHeapLValue(ident) {
				b.resetHeapBinding(ident, e.Location)
			}
			return operand
		}
		if e.Op.Kind == tokens.HASH_TOKEN {
			return b.lowerExpr(e.X)
		}
		operand := b.lowerExpr(e.X)
		if operand == mir.InvalidValue {
			return mir.InvalidValue
		}
		return b.emitUnary(e.Op.Kind, operand, e.Type, e.Location)
	case *hir.DerefExpr:
		// Dereference: load the value from the reference
		refVal := b.lowerExpr(e.X)
		if refVal == mir.InvalidValue {
			return mir.InvalidValue
		}
		return b.emitLoad(refVal, e.Type, e.Location)
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
	case *hir.RangeExpr:
		return b.lowerRangeExpr(e)
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
	return val
}

func (b *functionBuilder) useAddrValue(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	if _, ok := types.UnwrapType(typ).(*types.ReferenceType); ok {
		return false
	}
	return needsByRefType(typ)
}

func dynamicArrayValueType(typ types.SemType) (*types.ArrayType, bool) {
	if typ == nil {
		return nil, false
	}
	typ = types.UnwrapType(typ)
	if _, ok := typ.(*types.ReferenceType); ok {
		return nil, false
	}
	if arr, ok := typ.(*types.ArrayType); ok && arr.Length < 0 {
		return arr, true
	}
	return nil, false
}

func mapValueType(typ types.SemType) (*types.MapType, bool) {
	if typ == nil {
		return nil, false
	}
	typ = types.UnwrapType(typ)
	if _, ok := typ.(*types.ReferenceType); ok {
		return nil, false
	}
	if mapType, ok := typ.(*types.MapType); ok {
		return mapType, true
	}
	return nil, false
}

func isStringSemType(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	if prim, ok := types.UnwrapType(typ).(*types.PrimitiveType); ok {
		return prim.GetName() == types.TYPE_STRING
	}
	return false
}

func assignHelperForType(typ types.SemType) (string, bool) {
	if isStringSemType(typ) {
		return "ferret_string_assign", true
	}
	if _, ok := dynamicArrayValueType(typ); ok {
		return "ferret_array_assign", true
	}
	if _, ok := mapValueType(typ); ok {
		return "ferret_map_assign", true
	}
	return "", false
}

func (b *functionBuilder) nullPointerValue(typ types.SemType, loc source.Location) mir.ValueID {
	zero := b.emitConst(types.TypeU64, "0", loc)
	return b.emitCast(zero, typ, loc)
}

func (b *functionBuilder) isDynamicArrayLiteralExpr(expr hir.Expr, expected types.SemType) bool {
	if expr == nil {
		return false
	}
	switch expr.(type) {
	case *hir.CompositeLit, *hir.RangeExpr:
	default:
		return false
	}
	if _, ok := dynamicArrayValueType(expected); ok {
		return true
	}
	if _, ok := dynamicArrayValueType(b.exprType(expr)); ok {
		return true
	}
	return false
}

func (b *functionBuilder) isMapLiteralExpr(expr hir.Expr, expected types.SemType) bool {
	if expr == nil {
		return false
	}
	if _, ok := expr.(*hir.CompositeLit); !ok {
		return false
	}
	if expected != nil {
		if _, ok := types.UnwrapType(expected).(*types.MapType); ok {
			return true
		}
	}
	return b.mapTypeOf(expr) != nil
}

func (b *functionBuilder) isSliceType(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	if arr, ok := types.UnwrapType(typ).(*types.ArrayType); ok {
		return arr.Length < 0 // Negative length = slice
	}
	return false
}

// isImplicitlyCompatible checks if source type can be implicitly converted to target type
// Used for MIR generation to determine if a cast is needed
func (b *functionBuilder) isImplicitlyCompatible(source, target types.SemType) bool {
	if source == nil || target == nil {
		return false
	}

	if b != nil && b.gen != nil && b.gen.ctx != nil && b.gen.mod != nil {
		return typechecker.IsImplicitlyCompatibleTypes(b.gen.ctx, b.gen.mod, source, target)
	}

	// Exact match
	if source.Equals(target) {
		return true
	}

	// Check if target is a named type and source matches its underlying type
	// This allows: i32 -> Int1 where type Int1 i32
	if targetNamed, ok := target.(*types.NamedType); ok {
		if _, sourceIsNamed := source.(*types.NamedType); !sourceIsNamed {
			// Source is base type, target is named type wrapping it
			if source.Equals(types.UnwrapType(targetNamed)) {
				return true
			}
		}
	}

	// Check reference compatibility: &i32 -> &Int1 if i32 -> Int1
	if srcRef, ok := source.(*types.ReferenceType); ok {
		if tgtRef, ok := target.(*types.ReferenceType); ok {
			if srcRef.Mutable == tgtRef.Mutable {
				return b.isImplicitlyCompatible(srcRef.Inner, tgtRef.Inner)
			}
		}
	}

	return false
}

func (b *functionBuilder) coerceValueForAssign(val mir.ValueID, fromType, toType types.SemType, loc source.Location) mir.ValueID {
	if val == mir.InvalidValue || fromType == nil || toType == nil {
		return val
	}
	if ref, ok := types.UnwrapType(toType).(*types.ReferenceType); ok {
		if isEmptyInterface(ref.Inner) {
			if _, ok := types.UnwrapType(fromType).(*types.ReferenceType); ok {
				return val
			}
			val = b.boxInterfaceValue(val, fromType, ref.Inner, loc)
		}
		return val
	}
	if unionType, ok := types.UnwrapType(toType).(*types.UnionType); ok {
		if fromUnion, ok := types.UnwrapType(fromType).(*types.UnionType); ok {
			if fromUnion.Equals(unionType) {
				return val
			}
		}
		return b.boxUnionValue(val, fromType, unionType, loc)
	} else if interfaceTypeOf(toType) != nil {
		val = b.boxInterfaceValue(val, fromType, toType, loc)
		return val
	}
	if ref, ok := types.UnwrapType(fromType).(*types.ReferenceType); ok {
		val = b.emitLoad(val, ref.Inner, loc)
		fromType = ref.Inner
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
		return b.emitUnary(expr.Op.Kind, operand, expr.Type, expr.Location)
	}

	addr := b.lowerLValue(expr.X)
	if addr == mir.InvalidValue {
		return mir.InvalidValue
	}

	typ := b.exprType(expr.X)
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
		return b.emitUnary(expr.Op.Kind, operand, expr.Type, expr.Location)
	}

	addr := b.lowerLValue(expr.X)
	if addr == mir.InvalidValue {
		return mir.InvalidValue
	}

	typ := b.exprType(expr.X)
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

	isSelfAddrCall := false
	isAddrCall := false
	if ident, ok := expr.Fun.(*hir.Ident); ok {
		if ident.Name == "self_addr" && ident.Symbol != nil && ident.Symbol.IsNative {
			isSelfAddrCall = true
		}
		if ident.Name == "addr" && ident.Symbol != nil && ident.Symbol.IsNative {
			isAddrCall = true
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
			recvArgs := []mir.ValueID{recv}
			if method.Receiver != nil {
				if refType, ok := types.UnwrapType(method.Receiver).(*types.ReferenceType); ok {
					recvHeap := b.computeBorrowHeap(selector.X, recv, refType.Inner, expr.Location)
					if recvHeap == mir.InvalidValue {
						recvHeap = b.emitConst(types.TypeU64, "0", expr.Location)
					}
					recvArgs = append(recvArgs, recvHeap)
				}
			}
			args := b.lowerCallArgs(expr.Args, fnType, expr.Location, false)
			if args == nil {
				return mir.InvalidValue
			}
			if heapRet, ok := b.heapReturnType(target); ok {
				ptr := b.emitHeapReturnCall(target, append(recvArgs, args...), heapRet, expr.Location)
				if ptr == mir.InvalidValue {
					return mir.InvalidValue
				}
				return b.emitLoad(ptr, heapRet, expr.Location)
			}
			return b.emitCall(target, append(recvArgs, args...), expr)
		}
	}

	if target, ok := b.callTarget(expr.Fun); ok {
		// Print/Println resolve by concrete arg type in QBE.
		skipInterfaceBoxing := b.isStdIoPrintTarget(target)
		args := b.lowerCallArgs(expr.Args, fnType, expr.Location, skipInterfaceBoxing)
		if args == nil {
			return mir.InvalidValue
		}
		if isAddrCall && len(expr.Args) == 1 && len(args) >= 1 {
			argType := b.exprType(expr.Args[0])
			if _, ok := types.UnwrapType(argType).(*types.ReferenceType); !ok {
				if addr := b.bindingAddrArg(expr.Args[0]); addr != mir.InvalidValue {
					args[0] = addr
				}
			}
		}
		if isSelfAddrCall && len(expr.Args) == 1 && len(args) >= 1 {
			if addr := b.bindingAddrArg(expr.Args[0]); addr != mir.InvalidValue {
				args[0] = addr
			}
		}
		if heapRet, ok := b.heapReturnType(target); ok {
			ptr := b.emitHeapReturnCall(target, args, heapRet, expr.Location)
			if ptr == mir.InvalidValue {
				return mir.InvalidValue
			}
			return b.emitLoad(ptr, heapRet, expr.Location)
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

func (b *functionBuilder) bindingAddrArg(expr hir.Expr) mir.ValueID {
	expr = unwrapParenExpr(expr)
	switch e := expr.(type) {
	case *hir.Ident:
		return b.bindingAddr(e)
	case *hir.UnaryExpr:
		if e.Op.Kind != tokens.BIT_AND_TOKEN && e.Op.Kind != tokens.MUT_TOKEN {
			return mir.InvalidValue
		}
		if ident, ok := unwrapParenExpr(e.X).(*hir.Ident); ok {
			return b.bindingAddr(ident)
		}
	}
	return mir.InvalidValue
}

func (b *functionBuilder) lowerCallArgs(args []hir.Expr, fnType *types.FunctionType, loc source.Location, skipInterfaceBoxing bool) []mir.ValueID {
	out := make([]mir.ValueID, 0, len(args))
	for i, arg := range args {
		boxedUnion := false
		argIsMove := isMoveExpr(arg)
		val := b.lowerExpr(arg)
		if val == mir.InvalidValue {
			return nil
		}
		argType := b.exprType(arg)
		var paramType types.SemType
		if fnType != nil && i < len(fnType.Params) {
			paramType = fnType.Params[i].Type
			// For variadic parameters, convert ...T to []T for comparison
			// (HIR lowering already converted args to array literals)
			if fnType.Params[i].IsVariadic {
				paramType = types.NewArray(paramType, -1) // []T
			}
		}
		// Keep array cloning logic
		if paramType != nil && !argIsMove {
			if _, ok := dynamicArrayValueType(paramType); ok {
				if _, ok := dynamicArrayValueType(argType); ok {
					if !b.isDynamicArrayLiteralExpr(arg, paramType) {
						val = b.emitArrayClone(val, paramType, loc)
					}
				}
			} else if _, ok := mapValueType(paramType); ok {
				if _, ok := mapValueType(argType); ok {
					if !b.isMapLiteralExpr(arg, paramType) {
						val = b.emitMapClone(val, paramType, loc)
					}
				}
			}
		}
		if !skipInterfaceBoxing && paramType != nil && interfaceTypeOf(paramType) != nil {
			if !isRefToEmptyInterface(paramType) {
				val = b.boxInterfaceValue(val, argType, paramType, loc)
				if val == mir.InvalidValue {
					return nil
				}
			}
		}
		if !skipInterfaceBoxing && paramType != nil {
			if _, ok := types.UnwrapType(paramType).(*types.UnionType); ok {
				val = b.coerceValueForAssign(val, argType, paramType, loc)
				if val == mir.InvalidValue {
					return nil
				}
				boxedUnion = true
			}
		}
		if paramType != nil {
			paramBase := types.UnwrapType(paramType)
			if _, ok := paramBase.(*types.ReferenceType); !ok && needsByRefType(paramType) && !boxedUnion {
				tmp := b.emitAlloca(paramType, loc)
				if argIsMove {
					b.emitStoreMove(tmp, val, loc)
				} else {
					b.emitStore(tmp, val, loc)
				}
				val = tmp
			}
		}
		out = append(out, val)
		if paramType != nil {
			if refType, ok := types.UnwrapType(paramType).(*types.ReferenceType); ok {
				heapArg := b.computeBorrowHeap(arg, val, refType.Inner, loc)
				if heapArg == mir.InvalidValue {
					heapArg = b.emitConst(types.TypeU64, "0", loc)
				}
				out = append(out, heapArg)
			}
		}
	}
	return out
}

func (b *functionBuilder) isHeapReturnCall(call *hir.CallExpr) (types.SemType, bool) {
	if call == nil {
		return nil, false
	}
	if selector, ok := call.Fun.(*hir.SelectorExpr); ok {
		if target, _, ok := b.methodCallTarget(selector); ok {
			return b.heapReturnType(target)
		}
	}
	if target, ok := b.callTarget(call.Fun); ok {
		return b.heapReturnType(target)
	}
	return nil, false
}

func (b *functionBuilder) lowerHeapReturnCall(call *hir.CallExpr, inner types.SemType, loc source.Location) mir.ValueID {
	if call == nil || inner == nil {
		return mir.InvalidValue
	}

	fnType, _ := types.UnwrapType(b.exprType(call.Fun)).(*types.FunctionType)

	if selector, ok := call.Fun.(*hir.SelectorExpr); ok {
		if iface := interfaceTypeOf(b.exprType(selector.X)); iface != nil && len(iface.Methods) > 0 {
			return mir.InvalidValue
		}
		if target, method, ok := b.methodCallTarget(selector); ok {
			recv := b.methodReceiverArg(selector, method)
			if recv == mir.InvalidValue {
				return mir.InvalidValue
			}
			recvArgs := []mir.ValueID{recv}
			if method.Receiver != nil {
				if refType, ok := types.UnwrapType(method.Receiver).(*types.ReferenceType); ok {
					recvHeap := b.computeBorrowHeap(selector.X, recv, refType.Inner, call.Location)
					if recvHeap == mir.InvalidValue {
						recvHeap = b.emitConst(types.TypeU64, "0", call.Location)
					}
					recvArgs = append(recvArgs, recvHeap)
				}
			}
			args := b.lowerCallArgs(call.Args, fnType, call.Location, false)
			if args == nil {
				return mir.InvalidValue
			}
			ptr := b.emitHeapReturnCall(target, append(recvArgs, args...), inner, loc)
			if ptr != mir.InvalidValue {
				b.ptrElem[ptr] = inner
			}
			return ptr
		}
	}

	if target, ok := b.callTarget(call.Fun); ok {
		skipInterfaceBoxing := b.isStdIoPrintTarget(target)
		args := b.lowerCallArgs(call.Args, fnType, call.Location, skipInterfaceBoxing)
		if args == nil {
			return mir.InvalidValue
		}
		ptr := b.emitHeapReturnCall(target, args, inner, loc)
		if ptr != mir.InvalidValue {
			b.ptrElem[ptr] = inner
		}
		return ptr
	}

	return mir.InvalidValue
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

func isRefToEmptyInterface(typ types.SemType) bool {
	if ref, ok := types.UnwrapType(typ).(*types.ReferenceType); ok {
		return isEmptyInterface(ref.Inner)
	}
	return false
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
	selfHeap := b.heapFromValue(ifaceVal, b.exprType(selector.X), call.Location)
	vtSlot := b.emitPtrAdd(ifaceVal, b.gen.layout.PointerSize, ptrType, call.Location)
	vtPtr := b.emitLoad(vtSlot, ptrType, call.Location)

	offset := methodIndex * b.gen.layout.PointerSize
	methodSlot := b.emitPtrAdd(vtPtr, offset, ptrType, call.Location)

	args := b.lowerCallArgs(call.Args, fnType, call.Location, false)
	if args == nil {
		return mir.InvalidValue
	}
	allArgs := append([]mir.ValueID{dataPtr, selfHeap}, args...)
	return b.emitCallIndirect(methodSlot, allArgs, call)
}

func (b *functionBuilder) boxInterfaceValue(value mir.ValueID, valueType, ifaceType types.SemType, loc source.Location) mir.ValueID {
	if value == mir.InvalidValue {
		return mir.InvalidValue
	}
	if valueType == nil || ifaceType == nil {
		return value
	}
	if ref, ok := types.UnwrapType(ifaceType).(*types.ReferenceType); ok {
		ifaceType = ref.Inner
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
		// For zero-size types (empty structs), allocate a minimum of 1 byte
		// This ensures we have a valid pointer to store in the interface
		if size <= 0 {
			size = 1
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
		// For empty interface{}, store type descriptor pointer at offset PointerSize
		typeDesc := b.getOrCreateTypeDescriptor(valueType)
		typeDescPtr := b.emitCast(typeDesc, ptrType, loc)
		typeDescSlot := b.emitPtrAdd(ifaceAddr, b.gen.layout.PointerSize, ptrType, loc)
		b.emitStore(typeDescSlot, typeDescPtr, loc)
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

	heapPtr := mir.InvalidValue
	if ref, ok := types.UnwrapType(valueType).(*types.ReferenceType); ok {
		heapPtr = b.computeBorrowHeap(nil, value, ref.Inner, loc)
	} else {
		heapPtr = b.emitCast(dataPtr, types.TypeU64, loc)
	}
	if heapPtr == mir.InvalidValue {
		heapPtr = b.zeroU64(loc)
	}

	return ifaceAddr
}

func (b *functionBuilder) emitInterfaceDataPtr(ifaceAddr mir.ValueID, loc source.Location) mir.ValueID {
	ptrType := types.NewReference(types.TypeU8)
	dataSlot := b.emitPtrAdd(ifaceAddr, 0, ptrType, loc)
	return b.emitLoad(dataSlot, ptrType, loc)
}

func (b *functionBuilder) emitTypeIDPtr(typ types.SemType, loc source.Location) mir.ValueID {
	if b == nil {
		return mir.InvalidValue
	}
	ptrType := types.NewReference(types.TypeU8)
	if b.gen == nil {
		return b.emitConst(ptrType, "0", loc)
	}
	if typ == nil || typ.Equals(types.TypeUnknown) {
		return b.emitConst(ptrType, "0", loc)
	}
	typeDesc := b.getOrCreateTypeDescriptor(typ)
	if typeDesc == mir.InvalidValue {
		return b.emitConst(ptrType, "0", loc)
	}
	return b.emitCast(typeDesc, ptrType, loc)
}

// boxUnionValue creates a tagged union value from a variant value.
// Union layout: [4-byte discriminant/tag][variant data]
func (b *functionBuilder) boxUnionValue(value mir.ValueID, valueType types.SemType, unionType *types.UnionType, loc source.Location) mir.ValueID {
	if value == mir.InvalidValue || unionType == nil {
		return mir.InvalidValue
	}

	// Find which variant this value matches
	// First try exact match, then try compatibility (for implicit upcasts)
	variantIndex := -1
	var targetVariant types.SemType

	// Try exact match first (for performance)
	for i, variant := range unionType.Variants {
		if valueType.Equals(variant) {
			variantIndex = i
			targetVariant = variant
			break
		}
	}

	// If no exact match, check for implicit compatibility
	// This handles cases like: i32 -> Int1 (named type upcast), &i32 -> &Int1, etc.
	if variantIndex < 0 {
		for i, variant := range unionType.Variants {
			if b.isImplicitlyCompatible(valueType, variant) {
				variantIndex = i
				targetVariant = variant
				break
			}
		}
	}

	if variantIndex < 0 {
		// Type checker should have caught this, but provide helpful debug info
		if b.gen != nil && b.gen.ctx != nil {
			valueTypeStr := valueType.String()
			variantStrs := make([]string, len(unionType.Variants))
			for i, v := range unionType.Variants {
				variantStrs[i] = v.String()
			}
			b.gen.ctx.ReportError(fmt.Sprintf("MIR: union variant mismatch: type '%s' not in union variants [%s]",
				valueTypeStr, strings.Join(variantStrs, ", ")), &loc)
		}
		b.reportUnsupported("union variant mismatch", &loc)
		return mir.InvalidValue
	}

	// If value type doesn't exactly match target variant, insert implicit cast
	if !valueType.Equals(targetVariant) {
		if interfaceTypeOf(targetVariant) != nil {
			value = b.boxInterfaceValue(value, valueType, targetVariant, loc)
		} else {
			value = b.emitCast(value, targetVariant, loc)
		}
		valueType = targetVariant
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

// emitShortCircuitBinary implements short-circuit evaluation for && and || operators.
// For &&: if left is false, return false without evaluating right.
// For ||: if left is true, return true without evaluating right.
func (b *functionBuilder) emitShortCircuitBinary(e *hir.BinaryExpr) mir.ValueID {
	// Allocate a stack slot to store the result
	resultSlot := b.emitAlloca(types.TypeBool, e.Location)

	// Evaluate the left operand
	left := b.lowerExpr(e.X)
	if left == mir.InvalidValue {
		return mir.InvalidValue
	}

	// Create blocks for the control flow
	rightBlock := b.newBlock("and_or.right", e.Location)
	mergeBlock := b.newBlock("and_or.merge", e.Location)

	if e.Op.Kind == tokens.AND_TOKEN {
		// For &&: if left is false, skip to merge with false result
		// Store false as the default result
		falseVal := b.emitConst(types.TypeBool, "0", e.Location)
		b.emitStore(resultSlot, falseVal, e.Location)

		// Branch: if left is true, evaluate right; otherwise go to merge
		b.current.Term = &mir.CondBr{
			Cond:     left,
			Then:     rightBlock.ID,
			Else:     mergeBlock.ID,
			Location: e.Location,
		}

		// Right block: evaluate right operand
		b.setBlock(rightBlock)
		right := b.lowerExpr(e.Y)
		if right == mir.InvalidValue {
			return mir.InvalidValue
		}

		// Store right result and branch to merge
		b.emitStore(resultSlot, right, e.Location)
		b.branchIfNoTerm(mergeBlock.ID, e.Location)
	} else {
		// For ||: if left is true, skip to merge with true result
		// Store true as the default result
		trueVal := b.emitConst(types.TypeBool, "1", e.Location)
		b.emitStore(resultSlot, trueVal, e.Location)

		// Branch: if left is false, evaluate right; otherwise go to merge
		b.current.Term = &mir.CondBr{
			Cond:     left,
			Then:     mergeBlock.ID,
			Else:     rightBlock.ID,
			Location: e.Location,
		}

		// Right block: evaluate right operand
		b.setBlock(rightBlock)
		right := b.lowerExpr(e.Y)
		if right == mir.InvalidValue {
			return mir.InvalidValue
		}

		// Store right result and branch to merge
		b.emitStore(resultSlot, right, e.Location)
		b.branchIfNoTerm(mergeBlock.ID, e.Location)
	}

	// Merge block: load and return the result
	b.setBlock(mergeBlock)
	return b.emitLoad(resultSlot, types.TypeBool, e.Location)
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
	// Load the type descriptor pointer from the interface at offset PointerSize
	ptrType := types.NewReference(types.TypeU8)
	typeSlot := b.emitPtrAdd(ifaceVal, b.gen.layout.PointerSize, ptrType, loc)
	storedTypePtr := b.emitLoad(typeSlot, ptrType, loc)

	expectedTypeDesc := b.getOrCreateTypeDescriptor(targetType)
	expectedTypePtr := b.emitCast(expectedTypeDesc, ptrType, loc)

	isEqual := b.gen.nextValueID()
	b.emitInstr(&mir.Binary{
		Result:   isEqual,
		Op:       tokens.DOUBLE_EQUAL_TOKEN,
		Left:     storedTypePtr,
		Right:    expectedTypePtr,
		Type:     types.TypeBool,
		Location: loc,
	})

	return isEqual
}

// emitRangeCheck generates code for the 'in' operator: x in a..b => x >= a && x < b
// Returns a boolean indicating if the value is within the range.
func (b *functionBuilder) emitRangeCheck(expr *hir.BinaryExpr) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}

	// The RHS must be a RangeExpr
	rangeExpr, ok := expr.Y.(*hir.RangeExpr)
	if !ok {
		b.reportUnsupported("'in' operator without range expression", expr.Loc())
		return mir.InvalidValue
	}

	// Lower the value being checked (LHS)
	val := b.lowerExpr(expr.X)
	if val == mir.InvalidValue {
		return mir.InvalidValue
	}
	valType := b.exprType(expr.X)

	// Lower the range bounds
	startVal := b.lowerExpr(rangeExpr.Start)
	if startVal == mir.InvalidValue {
		return mir.InvalidValue
	}

	endVal := b.lowerExpr(rangeExpr.End)
	if endVal == mir.InvalidValue {
		return mir.InvalidValue
	}

	// Generate: val >= start
	lowerCheck := b.emitBinary(tokens.GREATER_EQUAL_TOKEN, val, startVal, types.TypeBool, expr.Location)
	if lowerCheck == mir.InvalidValue {
		return mir.InvalidValue
	}

	// Generate: val < end (or val <= end for inclusive)
	upperOp := tokens.LESS_TOKEN
	if rangeExpr.Inclusive {
		upperOp = tokens.LESS_EQUAL_TOKEN
	}
	upperCheck := b.emitBinary(upperOp, val, endVal, types.TypeBool, expr.Location)
	if upperCheck == mir.InvalidValue {
		return mir.InvalidValue
	}

	// Combine: lowerCheck && upperCheck
	boundsCheck := lowerCheck
	if lowerCheck != upperCheck {
		id := b.gen.nextValueID()
		b.emitInstr(&mir.Binary{
			Result:   id,
			Op:       tokens.AND_TOKEN,
			Left:     lowerCheck,
			Right:    upperCheck,
			Type:     types.TypeBool,
			Location: expr.Location,
		})
		boundsCheck = id
	}

	// If there's a step, we need to check alignment: (val - start) % step == 0
	// Note: Float ranges are rejected at type-checking phase, so we only handle integers here.
	if rangeExpr.Incr != nil {
		stepVal := b.lowerExpr(rangeExpr.Incr)
		if stepVal == mir.InvalidValue {
			return mir.InvalidValue
		}

		// Calculate: val - start
		diffID := b.gen.nextValueID()
		b.emitInstr(&mir.Binary{
			Result:   diffID,
			Op:       tokens.MINUS_TOKEN,
			Left:     val,
			Right:    startVal,
			Type:     valType,
			Location: expr.Location,
		})

		// Calculate: (val - start) % step
		modID := b.gen.nextValueID()
		b.emitInstr(&mir.Binary{
			Result:   modID,
			Op:       tokens.MOD_TOKEN,
			Left:     diffID,
			Right:    stepVal,
			Type:     valType,
			Location: expr.Location,
		})

		// Create zero constant for comparison
		zeroID := b.emitConst(valType, "0", expr.Location)
		if zeroID == mir.InvalidValue {
			return mir.InvalidValue
		}

		// Check: mod == 0
		alignCheck := b.emitBinary(tokens.DOUBLE_EQUAL_TOKEN, modID, zeroID, types.TypeBool, expr.Location)
		if alignCheck == mir.InvalidValue {
			return mir.InvalidValue
		}

		// Final result: boundsCheck && alignCheck
		finalID := b.gen.nextValueID()
		b.emitInstr(&mir.Binary{
			Result:   finalID,
			Op:       tokens.AND_TOKEN,
			Left:     boundsCheck,
			Right:    alignCheck,
			Type:     types.TypeBool,
			Location: expr.Location,
		})
		return finalID
	}

	return boundsCheck
}

func isMoveExpr(expr hir.Expr) bool {
	for {
		if p, ok := expr.(*hir.ParenExpr); ok {
			expr = p.X
			continue
		}
		break
	}
	unary, ok := expr.(*hir.UnaryExpr)
	if !ok {
		return false
	}
	return unary.Op.Kind == tokens.AT_TOKEN
}

func compareFloatType(leftType, rightType types.SemType) types.SemType {
	if leftType == nil || rightType == nil {
		return types.TypeUnknown
	}
	left := types.ResolveUntypedType(leftType, rightType)
	right := types.ResolveUntypedType(rightType, leftType)
	if types.IsUntyped(left) {
		left = types.ResolveUntypedType(left, types.TypeUnknown)
	}
	if types.IsUntyped(right) {
		right = types.ResolveUntypedType(right, types.TypeUnknown)
	}
	leftBase := types.UnwrapType(left)
	rightBase := types.UnwrapType(right)
	if types.IsFloat(leftBase) || types.IsFloat(rightBase) {
		if leftBase.Equals(types.TypeF64) || rightBase.Equals(types.TypeF64) {
			return types.TypeF64
		}
		return types.TypeF32
	}
	return types.TypeUnknown
}

func (b *functionBuilder) isHeapLValue(expr hir.Expr) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *hir.Ident:
		if e.Symbol == nil {
			return false
		}
		if e.Symbol.IsHeap {
			return true
		}
		if b.boxed != nil {
			if _, ok := b.boxed[e.Symbol]; ok {
				return true
			}
		}
		return false
	case *hir.SelectorExpr:
		return b.isHeapLValue(e.X)
	case *hir.IndexExpr:
		return b.isHeapLValue(e.X)
	case *hir.ParenExpr:
		return b.isHeapLValue(e.X)
	default:
		return false
	}
}

func (b *functionBuilder) resetHeapBinding(ident *hir.Ident, loc source.Location) {
	if ident == nil || ident.Symbol == nil {
		return
	}
	typ := b.getStorageType(ident)
	if typ == nil {
		return
	}
	heapAddr := b.emitHeapAlloc(mir.InvalidValue, typ, loc)
	if heapAddr == mir.InvalidValue {
		return
	}
	b.slots[ident.Symbol] = heapAddr
	b.boxed[ident.Symbol] = heapAddr

	bind, ok := b.bindings[ident.Symbol]
	if ok {
		if elem, ok := b.ptrElem[bind]; ok {
			if _, ok := types.UnwrapType(elem).(*types.ReferenceType); ok {
				b.emitStore(bind, heapAddr, loc)
				return
			}
		}
	}
	bind = b.emitAlloca(types.NewReference(typ), loc)
	b.emitStore(bind, heapAddr, loc)
	b.bindings[ident.Symbol] = bind
}

func (b *functionBuilder) bindingAddr(ident *hir.Ident) mir.ValueID {
	if ident == nil || ident.Symbol == nil {
		return mir.InvalidValue
	}
	if bind, ok := b.bindings[ident.Symbol]; ok {
		return bind
	}
	return b.addrForIdent(ident)
}

func (b *functionBuilder) zeroU64(loc source.Location) mir.ValueID {
	return b.emitConst(types.TypeU64, "0", loc)
}

func (b *functionBuilder) refHeapSlotForIdent(ident *hir.Ident) (mir.ValueID, bool) {
	if ident == nil {
		return mir.InvalidValue, false
	}
	if ident.Symbol != nil {
		if slot, ok := b.refHeapSlots[ident.Symbol]; ok {
			return slot, true
		}
	}
	if slot, ok := b.tempRefHeap[ident]; ok {
		return slot, true
	}
	return mir.InvalidValue, false
}

func (b *functionBuilder) ensureRefHeapSlot(ident *hir.Ident, loc source.Location) mir.ValueID {
	if slot, ok := b.refHeapSlotForIdent(ident); ok {
		return slot
	}
	if ident == nil {
		return mir.InvalidValue
	}
	slot := b.emitAllocaInEntry(types.TypeU64, loc)
	if ident.Symbol != nil {
		b.refHeapSlots[ident.Symbol] = slot
	} else {
		b.tempRefHeap[ident] = slot
	}
	return slot
}

func (b *functionBuilder) loadRefHeapForIdent(ident *hir.Ident, loc source.Location) mir.ValueID {
	if ident == nil {
		return mir.InvalidValue
	}
	if slot, ok := b.refHeapSlotForIdent(ident); ok {
		return b.emitLoad(slot, types.TypeU64, loc)
	}
	if b.refHeapParams != nil {
		if heapParam, ok := b.refHeapParams[ident.Name]; ok {
			return heapParam
		}
	}
	return mir.InvalidValue
}

func (b *functionBuilder) storeRefHeapForIdent(ident *hir.Ident, heapVal mir.ValueID, loc source.Location) {
	if ident == nil || heapVal == mir.InvalidValue {
		return
	}
	slot := b.ensureRefHeapSlot(ident, loc)
	if slot == mir.InvalidValue {
		return
	}
	b.emitStore(slot, heapVal, loc)
}

func (b *functionBuilder) computeBorrowHeap(expr hir.Expr, val mir.ValueID, inner types.SemType, loc source.Location) mir.ValueID {
	if val == mir.InvalidValue {
		return mir.InvalidValue
	}
	if inner == nil {
		return b.zeroU64(loc)
	}
	expr = unwrapParenExpr(expr)

	isBorrow := false
	if unary, ok := expr.(*hir.UnaryExpr); ok && (unary.Op.Kind == tokens.BIT_AND_TOKEN || unary.Op.Kind == tokens.MUT_TOKEN) {
		isBorrow = true
	} else if ident, ok := expr.(*hir.Ident); ok {
		if _, ok := types.UnwrapType(ident.Type).(*types.ReferenceType); ok {
			if heapVal := b.loadRefHeapForIdent(ident, loc); heapVal != mir.InvalidValue {
				return heapVal
			}
		}
	}

	if expr != nil {
		if refType, ok := types.UnwrapType(b.exprType(expr)).(*types.ReferenceType); ok {
			exprInner := types.UnwrapType(refType.Inner)
			if interfaceTypeOf(inner) != nil && interfaceTypeOf(exprInner) == nil {
				inner = exprInner
			}
		}
	}

	inner = types.UnwrapType(inner)
	if ref, ok := inner.(*types.ReferenceType); ok {
		inner = types.UnwrapType(ref.Inner)
	}
	if prim, ok := inner.(*types.PrimitiveType); ok && prim.GetName() == types.TYPE_STRING {
		loaded := b.emitLoad(val, types.TypeString, loc)
		return b.emitCast(loaded, types.TypeU64, loc)
	}
	if _, ok := dynamicArrayValueType(inner); ok {
		loaded := b.emitLoad(val, inner, loc)
		return b.emitCast(loaded, types.TypeU64, loc)
	}
	if _, ok := mapValueType(inner); ok {
		loaded := b.emitLoad(val, inner, loc)
		return b.emitCast(loaded, types.TypeU64, loc)
	}
	lval := expr
	if isBorrow {
		lval = expr.(*hir.UnaryExpr).X
	}
	if b.isHeapLValue(lval) {
		return b.emitCast(val, types.TypeU64, loc)
	}
	if interfaceTypeOf(inner) != nil {
		ifaceVal := b.emitLoad(val, inner, loc)
		// Get heap address from the interface data pointer
		dataPtr := b.emitInterfaceDataPtr(ifaceVal, loc)
		return b.emitCast(dataPtr, types.TypeU64, loc)
	}
	return b.zeroU64(loc)
}

func (b *functionBuilder) heapFromValue(val mir.ValueID, typ types.SemType, loc source.Location) mir.ValueID {
	if val == mir.InvalidValue || typ == nil {
		return mir.InvalidValue
	}
	base := types.UnwrapType(typ)
	if prim, ok := base.(*types.PrimitiveType); ok && prim.GetName() == types.TYPE_STRING {
		return b.emitCast(val, types.TypeU64, loc)
	}
	if _, ok := dynamicArrayValueType(base); ok {
		return b.emitCast(val, types.TypeU64, loc)
	}
	if _, ok := mapValueType(base); ok {
		return b.emitCast(val, types.TypeU64, loc)
	}
	if interfaceTypeOf(base) != nil {
		// For interfaces, the heap address is the data pointer stored in the interface struct.
		dataPtr := b.emitInterfaceDataPtr(val, loc)
		return b.emitCast(dataPtr, types.TypeU64, loc)
	}
	return b.zeroU64(loc)
}

func (b *functionBuilder) emitCall(target string, args []mir.ValueID, expr *hir.CallExpr) mir.ValueID {
	retType := expr.Type
	if ref, ok := types.UnwrapType(retType).(*types.ReferenceType); ok {
		out := b.emitAlloca(ref.Inner, expr.Location)
		outHeap := b.emitAlloca(types.TypeU64, expr.Location)
		callArgs := append([]mir.ValueID{out, outHeap}, args...)
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

func (b *functionBuilder) emitHeapReturnCall(target string, args []mir.ValueID, inner types.SemType, loc source.Location) mir.ValueID {
	if target == "" || inner == nil {
		return mir.InvalidValue
	}
	retType := types.NewReference(inner)
	result := b.gen.nextValueID()
	b.emitInstr(&mir.Call{
		Result:   result,
		Target:   target,
		Args:     args,
		Type:     retType,
		Location: loc,
	})
	b.ptrElem[result] = inner
	return result
}

func (b *functionBuilder) emitCallIndirect(callee mir.ValueID, args []mir.ValueID, expr *hir.CallExpr) mir.ValueID {
	retType := expr.Type
	if ref, ok := types.UnwrapType(retType).(*types.ReferenceType); ok {
		out := b.emitAlloca(ref.Inner, expr.Location)
		outHeap := b.emitAlloca(types.TypeU64, expr.Location)
		callArgs := append([]mir.ValueID{out, outHeap}, args...)
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
	if globalName, sym, ok := b.lookupQualifiedGlobal(name); ok {
		storageType := globalStorageType(sym)
		addr := b.emitGlobalAddrByName(globalName, storageType, expr.Location)
		if addr == mir.InvalidValue {
			return mir.InvalidValue
		}
		if sym != nil && sym.IsHeap {
			heapPtr := b.emitLoad(addr, storageType, expr.Location)
			b.ptrElem[heapPtr] = sym.Type
			addr = heapPtr
		}
		if b.useAddrValue(expr.Type) {
			return addr
		}
		return b.emitLoad(addr, expr.Type, expr.Location)
	}

	b.reportUnsupported(fmt.Sprintf("qualified value %s", name), &expr.Location)
	return mir.InvalidValue
}

func (b *functionBuilder) lowerSelector(expr *hir.SelectorExpr) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}

	if entry := b.narrowedOptionalEntry(expr); entry != nil && entry.OriginalType != nil {
		addr := b.lowerFieldAddr(expr)
		if addr == mir.InvalidValue {
			return mir.InvalidValue
		}
		optVal := b.emitLoad(addr, entry.OriginalType, expr.Location)
		result := b.gen.nextValueID()
		b.emitInstr(&mir.OptionalUnwrap{
			Result:     result,
			Value:      optVal,
			Default:    mir.InvalidValue,
			HasDefault: false,
			Type:       entry.NarrowedType,
			Location:   expr.Location,
		})
		return result
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
	case *hir.ScopeResolutionExpr:
		if addr := b.addrForQualified(e); addr != mir.InvalidValue {
			return addr
		}
		b.reportUnsupported("lvalue", expr.Loc())
		return mir.InvalidValue
	case *hir.DerefExpr:
		// For dereference, the operand IS the address (it's a reference/pointer)
		return b.lowerExpr(e.X)
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

	wrap := func(val mir.ValueID) mir.ValueID {
		return val
	}

	// Check for narrowed union access: if the access type differs from the storage type
	// and the storage type is a union, we need to extract the variant.
	if ident.Symbol != nil && ident.Symbol.Type != nil {
		storageType := types.UnwrapType(ident.Symbol.Type)
		accessType := types.UnwrapType(b.exprType(ident))

		if unionType, ok := storageType.(*types.UnionType); ok {
			// Storage is a union but we're accessing a narrowed variant type
			if accessType != nil && !accessType.Equals(storageType) {
				// Find the variant index
				variantIndex := -1
				for i, variant := range unionType.Variants {
					if accessType.Equals(variant) {
						variantIndex = i
						break
					}
				}

				if variantIndex >= 0 {
					// Load the union value, then extract the variant.
					unionVal := mir.InvalidValue
					if addr, ok := b.slots[ident.Symbol]; ok {
						unionVal = b.emitLoad(addr, ident.Symbol.Type, ident.Location)
					} else if val, ok := b.paramsByName[ident.Name]; ok {
						unionVal = val
					} else if addr, ok := b.tempSlots[ident]; ok {
						unionVal = b.emitLoad(addr, ident.Symbol.Type, ident.Location)
					}

					if unionVal != mir.InvalidValue {
						result := b.gen.nextValueID()
						b.emitInstr(&mir.UnionExtract{
							Result:       result,
							Value:        unionVal,
							VariantIndex: variantIndex,
							UnionType:    storageType,
							Type:         accessType,
							Location:     ident.Location,
						})
						return wrap(result)
					}
				}
			}
		}
	}

	// Check for narrowed optional access: if the access type is the optional inner type,
	// unwrap the optional value before use.
	if ident.Symbol != nil && ident.Symbol.Type != nil {
		storageType := types.UnwrapType(ident.Symbol.Type)
		accessType := b.exprType(ident)
		if optType, ok := storageType.(*types.OptionalType); ok {
			if optType.Inner != nil && accessType != nil {
				accessMatches := accessType.Equals(optType.Inner)
				if !accessMatches {
					accessMatches = types.UnwrapType(accessType).Equals(types.UnwrapType(optType.Inner))
				}
				if accessMatches {
					optVal := mir.InvalidValue
					if addr := b.addrForIdent(ident); addr != mir.InvalidValue {
						optVal = b.emitLoad(addr, ident.Symbol.Type, ident.Location)
					} else if val, ok := b.paramsByName[ident.Name]; ok {
						optVal = val
					}
					if optVal != mir.InvalidValue {
						result := b.gen.nextValueID()
						b.emitInstr(&mir.OptionalUnwrap{
							Result:     result,
							Value:      optVal,
							Default:    mir.InvalidValue,
							HasDefault: false,
							Type:       accessType,
							Location:   ident.Location,
						})
						return wrap(result)
					}
				}
			}
		}
	}

	if ident.Symbol != nil && b.captures != nil {
		if _, ok := b.captures[ident.Symbol]; ok {
			if addr := b.addrForIdent(ident); addr != mir.InvalidValue {
				if b.useAddrValue(ident.Type) {
					return wrap(addr)
				}
				return wrap(b.emitLoad(addr, ident.Type, ident.Location))
			}
		}
	}

	if ident.Symbol != nil {
		if addr, ok := b.slots[ident.Symbol]; ok {
			if b.useAddrValue(ident.Type) {
				return wrap(addr)
			}
			return wrap(b.emitLoad(addr, ident.Type, ident.Location))
		}
	}

	if addr, ok := b.tempSlots[ident]; ok {
		if b.useAddrValue(ident.Type) {
			return wrap(addr)
		}
		return wrap(b.emitLoad(addr, ident.Type, ident.Location))
	}

	if ident.Symbol != nil && (ident.Symbol.Kind == symbols.SymbolParameter || ident.Symbol.Kind == symbols.SymbolReceiver) {
		if val, ok := b.paramsByName[ident.Name]; ok {
			return wrap(val)
		}
	}

	if val, ok := b.paramsByName[ident.Name]; ok {
		return wrap(val)
	}

	if ident.Symbol != nil {
		if addr, storageType, ok := b.moduleGlobalStorageAddr(ident.Symbol, ident.Location); ok {
			if ident.Symbol.IsHeap {
				heapPtr := b.emitLoad(addr, storageType, ident.Location)
				b.ptrElem[heapPtr] = ident.Symbol.Type
				if b.useAddrValue(ident.Type) {
					return wrap(heapPtr)
				}
				return wrap(b.emitLoad(heapPtr, ident.Type, ident.Location))
			}
			if b.useAddrValue(ident.Type) {
				return wrap(addr)
			}
			return wrap(b.emitLoad(addr, ident.Type, ident.Location))
		}
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
				// Use MIR parameter type for correct allocation size
				// This is important when HIR has value type but MIR converted to reference
				paramType := ident.Type
				if mirType, ok := b.paramTypes[ident.Name]; ok {
					paramType = mirType
				}
				// Always allocate and store parameter value
				// This works for both QBE (optimization possible later) and WASM (required)
				addr := b.emitAllocaInEntry(paramType, ident.Location)
				b.emitStoreInEntry(addr, val, ident.Location)
				b.slots[ident.Symbol] = addr
				if _, ok := b.bindings[ident.Symbol]; !ok {
					b.bindings[ident.Symbol] = addr
				}
				if _, ok := types.UnwrapType(paramType).(*types.ReferenceType); ok {
					// Heap value for parameters is now handled by the ABI and call sites.
				}
				return addr
			}
		}
	}

	if addr, ok := b.tempSlots[ident]; ok {
		return addr
	}

	if ident.Symbol != nil {
		if addr, storageType, ok := b.moduleGlobalStorageAddr(ident.Symbol, ident.Location); ok {
			if ident.Symbol.IsHeap {
				heapPtr := b.emitLoad(addr, storageType, ident.Location)
				b.ptrElem[heapPtr] = ident.Symbol.Type
				return heapPtr
			}
			return addr
		}
	}

	return mir.InvalidValue
}

func (b *functionBuilder) isModuleGlobalIdent(ident *hir.Ident) bool {
	if ident == nil {
		return false
	}
	return b.isModuleGlobalSymbol(ident.Symbol)
}

func (b *functionBuilder) isModuleGlobalSymbol(sym *symbols.Symbol) bool {
	if sym == nil || b.gen == nil || b.gen.mod == nil || b.gen.mod.ModuleScope == nil {
		return false
	}
	return sym.DeclaredScope == b.gen.mod.ModuleScope
}

func (b *functionBuilder) moduleGlobalStorageAddr(sym *symbols.Symbol, loc source.Location) (mir.ValueID, types.SemType, bool) {
	if sym == nil {
		return mir.InvalidValue, nil, false
	}
	if !b.isModuleGlobalSymbol(sym) {
		return mir.InvalidValue, nil, false
	}
	name := globalSymbolName(b.gen.mod.ImportPath, sym.Name)
	if name == "" {
		return mir.InvalidValue, nil, false
	}
	storageType := globalStorageType(sym)
	addr := b.emitGlobalAddrByName(name, storageType, loc)
	if addr == mir.InvalidValue {
		return mir.InvalidValue, nil, false
	}
	return addr, storageType, true
}

func (b *functionBuilder) emitGlobalAddrByName(name string, storageType types.SemType, loc source.Location) mir.ValueID {
	if name == "" {
		return mir.InvalidValue
	}
	if storageType == nil {
		storageType = types.TypeUnknown
	}
	addrType := types.NewReference(storageType)
	id := b.emitConst(addrType, "$"+name, loc)
	b.ptrElem[id] = storageType
	return id
}

func (b *functionBuilder) addrForQualified(expr *hir.ScopeResolutionExpr) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}
	name, ok := b.qualifiedName(expr)
	if !ok {
		return mir.InvalidValue
	}
	globalName, sym, ok := b.lookupQualifiedGlobal(name)
	if !ok {
		return mir.InvalidValue
	}
	storageType := globalStorageType(sym)
	addr := b.emitGlobalAddrByName(globalName, storageType, expr.Location)
	if addr == mir.InvalidValue {
		return mir.InvalidValue
	}
	if sym != nil && sym.IsHeap {
		heapPtr := b.emitLoad(addr, storageType, expr.Location)
		b.ptrElem[heapPtr] = sym.Type
		return heapPtr
	}
	return addr
}

func (b *functionBuilder) lowerFieldAddr(expr *hir.SelectorExpr) mir.ValueID {
	if expr == nil || expr.Field == nil {
		return mir.InvalidValue
	}

	basePtr := mir.InvalidValue
	baseType := b.exprType(expr.X)
	if baseType == nil {
		b.reportUnsupported("selector base", expr.Loc())
		return mir.InvalidValue
	}

	if entry := b.narrowedOptionalEntry(expr.X); entry != nil {
		basePtr = b.optionalPayloadPtr(expr.X, entry)
		if basePtr == mir.InvalidValue {
			return mir.InvalidValue
		}
		baseType = types.UnwrapType(entry.NarrowedType)
		if ref, ok := baseType.(*types.ReferenceType); ok {
			basePtr = b.emitLoad(basePtr, baseType, expr.Location)
			baseType = types.UnwrapType(ref.Inner)
			b.ptrElem[basePtr] = ref.Inner
		}
	} else {
		baseType = types.UnwrapType(baseType)
		addressable := isAddressableExpr(expr.X)
		if addressable {
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

	fieldType := expr.Type
	for _, field := range structType.Fields {
		if field.Name == expr.Field.Name {
			fieldType = field.Type
			break
		}
	}

	return b.emitPtrAdd(basePtr, offset, fieldType, expr.Location)
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

func (b *functionBuilder) lowerRangeExpr(expr *hir.RangeExpr) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}

	arrType, ok := types.UnwrapType(expr.Type).(*types.ArrayType)
	if !ok || arrType == nil {
		b.reportUnsupported("range expression type", expr.Loc())
		return mir.InvalidValue
	}

	elemType := arrType.Element
	if elemType == nil {
		elemType = types.TypeUnknown
	}

	loc := expr.Location
	startVal := b.lowerRangeValue(expr.Start, elemType, loc)
	if startVal == mir.InvalidValue {
		return mir.InvalidValue
	}
	endVal := b.lowerRangeValue(expr.End, elemType, loc)
	if endVal == mir.InvalidValue {
		return mir.InvalidValue
	}

	var incrVal mir.ValueID
	if expr.Incr != nil {
		incrVal = b.lowerRangeValue(expr.Incr, elemType, loc)
	} else {
		incrVal = b.rangeConst(elemType, "1", loc)
	}
	if incrVal == mir.InvalidValue {
		return mir.InvalidValue
	}

	currentAddr := b.emitAlloca(elemType, loc)
	b.emitStore(currentAddr, startVal, loc)

	startType := b.exprType(expr.Start)
	endType := b.exprType(expr.End)
	incrType := b.exprType(expr.Incr)
	mismatch := b.rangeStepFloatMismatch(startType, endType, incrType)
	b.emitRangeValidation(startVal, endVal, incrVal, elemType, loc, mismatch)

	if arrType.Length >= 0 {
		arrAddr := b.emitAlloca(arrType, loc)
		idxAddr := b.emitAlloca(types.TypeI32, loc)
		b.emitStore(idxAddr, b.emitConst(types.TypeI32, "0", loc), loc)

		condBlock := b.newBlock("range.cond", loc)
		bodyBlock := b.newBlock("range.body", loc)
		exitBlock := b.newBlock("range.end", loc)

		lenVal := b.emitConst(types.TypeI32, strconv.Itoa(arrType.Length), loc)
		b.branchIfNoTerm(condBlock.ID, loc)

		b.setBlock(condBlock)
		idxVal := b.emitLoad(idxAddr, types.TypeI32, loc)
		cond := b.emitBinary(tokens.LESS_TOKEN, idxVal, lenVal, types.TypeBool, loc)
		b.current.Term = &mir.CondBr{
			Cond:     cond,
			Then:     bodyBlock.ID,
			Else:     exitBlock.ID,
			Location: loc,
		}

		b.setBlock(bodyBlock)
		currentVal := b.rangeCurrentValue(currentAddr, elemType, loc)
		b.emitInstr(&mir.ArraySet{
			Array:    arrAddr,
			Index:    idxVal,
			Value:    currentVal,
			Location: loc,
		})
		nextVal := b.emitBinary(tokens.PLUS_TOKEN, currentVal, incrVal, elemType, loc)
		b.emitStore(currentAddr, nextVal, loc)

		nextIdx := b.emitBinary(tokens.PLUS_TOKEN, idxVal, b.emitConst(types.TypeI32, "1", loc), types.TypeI32, loc)
		b.emitStore(idxAddr, nextIdx, loc)
		b.branchIfNoTerm(condBlock.ID, loc)

		b.setBlock(exitBlock)
		return arrAddr
	}

	elemSize := b.gen.layout.SizeOf(elemType)
	if elemSize <= 0 {
		b.reportUnsupported("range expression element size", expr.Loc())
		return mir.InvalidValue
	}
	sizeType := types.TypeI64
	if b.gen.layout.PointerSize <= 4 {
		sizeType = types.TypeI32
	}
	sizeVal := b.emitConst(sizeType, strconv.Itoa(elemSize), loc)
	capVal := b.emitConst(types.TypeI32, "0", loc)
	typeIDPtr := b.emitTypeIDPtr(elemType, loc)

	arrVal := b.gen.nextValueID()
	b.emitInstr(&mir.Call{
		Result:   arrVal,
		Target:   "ferret_array_new",
		Args:     []mir.ValueID{sizeVal, capVal, typeIDPtr},
		Type:     expr.Type,
		Location: loc,
	})

	valueTemp := b.emitAlloca(elemType, loc)
	condBlock := b.newBlock("range.cond", loc)
	bodyBlock := b.newBlock("range.body", loc)
	exitBlock := b.newBlock("range.end", loc)

	condToken := tokens.LESS_TOKEN
	negCondToken := tokens.GREATER_TOKEN
	if expr.Inclusive {
		condToken = tokens.LESS_EQUAL_TOKEN
		negCondToken = tokens.GREATER_EQUAL_TOKEN
	}

	b.branchIfNoTerm(condBlock.ID, loc)

	b.setBlock(condBlock)
	currentVal := b.rangeCurrentValue(currentAddr, elemType, loc)
	zeroVal := b.rangeConst(elemType, "0", loc)
	posCheck := b.rangeCompare(tokens.GREATER_TOKEN, incrVal, zeroVal, elemType, loc)
	negCheck := b.rangeCompare(tokens.LESS_TOKEN, incrVal, zeroVal, elemType, loc)
	posCond := b.rangeCompare(condToken, currentVal, endVal, elemType, loc)
	negCond := b.rangeCompare(negCondToken, currentVal, endVal, elemType, loc)
	posAnd := b.emitBinary(tokens.AND_TOKEN, posCheck, posCond, types.TypeBool, loc)
	negAnd := b.emitBinary(tokens.AND_TOKEN, negCheck, negCond, types.TypeBool, loc)
	cond := b.emitBinary(tokens.OR_TOKEN, posAnd, negAnd, types.TypeBool, loc)
	b.current.Term = &mir.CondBr{
		Cond:     cond,
		Then:     bodyBlock.ID,
		Else:     exitBlock.ID,
		Location: loc,
	}

	b.setBlock(bodyBlock)
	currentVal = b.rangeCurrentValue(currentAddr, elemType, loc)
	b.emitStore(valueTemp, currentVal, loc)
	b.emitInstr(&mir.Call{
		Result:   mir.InvalidValue,
		Target:   "ferret_array_append",
		Args:     []mir.ValueID{arrVal, valueTemp},
		Type:     types.TypeBool,
		Location: loc,
	})
	nextVal := b.emitBinary(tokens.PLUS_TOKEN, currentVal, incrVal, elemType, loc)
	b.emitStore(currentAddr, nextVal, loc)
	b.branchIfNoTerm(condBlock.ID, loc)

	b.setBlock(exitBlock)
	return arrVal
}

func (b *functionBuilder) lowerRangeValue(expr hir.Expr, elemType types.SemType, loc source.Location) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}
	val := b.lowerExpr(expr)
	if val == mir.InvalidValue {
		return mir.InvalidValue
	}
	valType := b.exprType(expr)
	return b.castValue(val, valType, elemType, loc)
}

func (b *functionBuilder) rangeConst(elemType types.SemType, value string, loc source.Location) mir.ValueID {
	if isLargePrimitiveType(elemType) {
		return b.emitLargeConst(elemType, value, loc)
	}
	return b.emitConst(elemType, value, loc)
}

func (b *functionBuilder) rangeCompare(op tokens.TOKEN, left, right mir.ValueID, elemType types.SemType, loc source.Location) mir.ValueID {
	if isLargePrimitiveType(elemType) {
		return b.emitLargeCompare(op, left, right, elemType, loc)
	}
	return b.emitBinary(op, left, right, types.TypeBool, loc)
}

func (b *functionBuilder) rangeCurrentValue(addr mir.ValueID, elemType types.SemType, loc source.Location) mir.ValueID {
	if isLargePrimitiveType(elemType) {
		return addr
	}
	return b.emitLoad(addr, elemType, loc)
}

func (b *functionBuilder) emitRangeValidation(startVal, endVal, incrVal mir.ValueID, elemType types.SemType, loc source.Location, mismatch bool) {
	if b.current == nil || b.current.Term != nil {
		return
	}
	var invalid mir.ValueID
	if mismatch {
		invalid = b.emitConst(types.TypeBool, "1", loc)
	} else {
		if elemType == nil || elemType.Equals(types.TypeUnknown) {
			return
		}

		zero := b.rangeConst(elemType, "0", loc)
		if zero == mir.InvalidValue {
			return
		}

		isZero := b.rangeCompare(tokens.DOUBLE_EQUAL_TOKEN, incrVal, zero, elemType, loc)
		posCheck := b.rangeCompare(tokens.GREATER_TOKEN, incrVal, zero, elemType, loc)
		negCheck := b.rangeCompare(tokens.LESS_TOKEN, incrVal, zero, elemType, loc)
		startGtEnd := b.rangeCompare(tokens.GREATER_TOKEN, startVal, endVal, elemType, loc)
		startLtEnd := b.rangeCompare(tokens.LESS_TOKEN, startVal, endVal, elemType, loc)

		posInvalid := b.emitBinary(tokens.AND_TOKEN, posCheck, startGtEnd, types.TypeBool, loc)
		negInvalid := b.emitBinary(tokens.AND_TOKEN, negCheck, startLtEnd, types.TypeBool, loc)
		dirInvalid := b.emitBinary(tokens.OR_TOKEN, posInvalid, negInvalid, types.TypeBool, loc)
		invalid = b.emitBinary(tokens.OR_TOKEN, isZero, dirInvalid, types.TypeBool, loc)
	}
	if invalid == mir.InvalidValue {
		return
	}

	panicBlock := b.newBlock("range.invalid", loc)
	okBlock := b.newBlock("range.ok", loc)
	b.current.Term = &mir.CondBr{
		Cond:     invalid,
		Then:     panicBlock.ID,
		Else:     okBlock.ID,
		Location: loc,
	}

	b.setBlock(panicBlock)
	msg := b.emitConst(types.TypeString, "invalid range", loc)
	b.emitInstr(&mir.Call{
		Result:   mir.InvalidValue,
		Target:   "ferret_global_panic",
		Args:     []mir.ValueID{msg},
		Type:     types.TypeVoid,
		Location: loc,
	})
	panicBlock.Term = &mir.Unreachable{Location: loc}

	b.setBlock(okBlock)
}

func (b *functionBuilder) rangeStepFloatMismatch(startType, endType, incrType types.SemType) bool {
	if incrType == nil {
		return false
	}
	if !b.isFloatLikeType(incrType) {
		return false
	}
	return b.isIntLikeType(startType) || b.isIntLikeType(endType)
}

func (b *functionBuilder) isFloatLikeType(t types.SemType) bool {
	if t == nil || t.Equals(types.TypeUnknown) {
		return false
	}
	t = types.UnwrapType(t)
	return types.IsFloat(t) || types.IsUntypedFloat(t)
}

func (b *functionBuilder) isIntLikeType(t types.SemType) bool {
	if t == nil || t.Equals(types.TypeUnknown) {
		return false
	}
	t = types.UnwrapType(t)
	return types.IsInteger(t) || types.IsUntypedInt(t)
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
	keyTypeIDPtr := b.emitTypeIDPtr(mapType.Key, lit.Location)
	valueTypeIDPtr := b.emitTypeIDPtr(mapType.Value, lit.Location)

	fns := b.mapRuntimeFns(mapType.Key)

	var args []mir.ValueID
	if fns.needsTypeInfo {
		// Generate type descriptor for universal hashing
		typeDesc := b.getOrCreateTypeDescriptor(mapType.Key)
		args = []mir.ValueID{keySizeVal, valSizeVal, typeDesc, keyTypeIDPtr, valueTypeIDPtr}
	} else {
		args = []mir.ValueID{keySizeVal, valSizeVal, keyTypeIDPtr, valueTypeIDPtr}
	}

	if len(lit.Elts) == 0 {
		result := b.gen.nextValueID()
		b.emitInstr(&mir.Call{
			Result:   result,
			Target:   fns.newFn,
			Args:     args,
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

		// For keys: use direct Store for slices/maps to avoid cloning the underlying data
		// We want to copy the slice/map struct itself, not clone its contents
		if b.isDynamicArrayLiteralExpr(kv.Key, mapType.Key) || b.isMapLiteralExpr(kv.Key, mapType.Key) || b.isSliceType(mapType.Key) {
			b.emitInstr(&mir.Store{
				Addr:     keySlot,
				Value:    keyVal,
				Location: kv.Location,
			})
		} else {
			b.emitStore(keySlot, keyVal, kv.Location)
		}

		// For values: use direct Store for array/map literals to avoid cloning
		if b.isDynamicArrayLiteralExpr(kv.Value, mapType.Value) || b.isMapLiteralExpr(kv.Value, mapType.Value) {
			b.emitInstr(&mir.Store{
				Addr:     valSlot,
				Value:    valueVal,
				Location: kv.Location,
			})
		} else {
			b.emitStore(valSlot, valueVal, kv.Location)
		}
	}

	countVal := b.emitConst(sizeType, strconv.Itoa(len(lit.Elts)), lit.Location)
	result := b.gen.nextValueID()

	var pairsArgs []mir.ValueID
	if fns.needsTypeInfo {
		// Generate type descriptor for universal hashing
		typeDesc := b.getOrCreateTypeDescriptor(mapType.Key)
		pairsArgs = []mir.ValueID{keySizeVal, valSizeVal, keysAddr, valsAddr, countVal, typeDesc, keyTypeIDPtr, valueTypeIDPtr}
	} else {
		pairsArgs = []mir.ValueID{keySizeVal, valSizeVal, keysAddr, valsAddr, countVal, keyTypeIDPtr, valueTypeIDPtr}
	}

	b.emitInstr(&mir.Call{
		Result:   result,
		Target:   fns.fromPairsFn,
		Args:     pairsArgs,
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
	typeIDPtr := b.emitTypeIDPtr(arrType.Element, lit.Location)

	arr := b.gen.nextValueID()
	b.emitInstr(&mir.Call{
		Result:   arr,
		Target:   "ferret_array_new",
		Args:     []mir.ValueID{sizeVal, capVal, typeIDPtr},
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
			if b.isDynamicArrayLiteralExpr(elt, arrType.Element) {
				b.emitInstr(&mir.Store{
					Addr:     temp,
					Value:    value,
					Location: lit.Location,
				})
			} else {
				b.emitStore(temp, value, lit.Location)
			}
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
		if b.isDynamicArrayLiteralExpr(kv.Value, fieldType) || b.isMapLiteralExpr(kv.Value, fieldType) {
			b.emitInstr(&mir.Store{
				Addr:     fieldAddr,
				Value:    value,
				Location: lit.Location,
			})
		} else {
			b.emitStore(fieldAddr, value, lit.Location)
		}
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
		if b.isDynamicArrayLiteralExpr(elt, arrType.Element) || b.isMapLiteralExpr(elt, arrType.Element) {
			b.emitInstr(&mir.Store{
				Addr:     elemAddr,
				Value:    value,
				Location: lit.Location,
			})
		} else {
			b.emitStore(elemAddr, value, lit.Location)
		}
	}
}

func (b *functionBuilder) lowerIndexValue(expr *hir.IndexExpr) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}

	entry := b.narrowedOptionalEntry(expr)
	rawType := expr.Type
	if entry != nil && entry.OriginalType != nil {
		rawType = entry.OriginalType
	}
	unwrapValue := func(val mir.ValueID) mir.ValueID {
		if entry == nil || entry.NarrowedType == nil {
			return val
		}
		result := b.gen.nextValueID()
		b.emitInstr(&mir.OptionalUnwrap{
			Result:     result,
			Value:      val,
			Default:    mir.InvalidValue,
			HasDefault: false,
			Type:       entry.NarrowedType,
			Location:   expr.Location,
		})
		return result
	}

	if b.isStringType(expr.X) {
		return b.lowerStringIndexValue(expr)
	}

	if mapType := b.mapTypeOf(expr.X); mapType != nil {
		val := b.lowerMapIndexValue(expr, mapType)
		if entry != nil && val != mir.InvalidValue {
			return unwrapValue(val)
		}
		return val
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
				Type:     rawType,
				Location: expr.Location,
			})
			return unwrapValue(result)
		}
	}

	// Not a literal or not an array - try normal array handling
	arrType, _ := b.indexBaseType(expr.X).(*types.ArrayType)
	if arrType != nil {
		// Not a literal - handle normally
		if arrType.Length < 0 {
			val := b.lowerDynamicIndexValue(expr, arrType)
			if entry != nil && val != mir.InvalidValue {
				return unwrapValue(val)
			}
			return val
		}
		if _, ok := b.constArrayIndex(expr, arrType); ok {
			addr := b.lowerIndexAddr(expr)
			if addr == mir.InvalidValue {
				return mir.InvalidValue
			}
			return b.emitLoad(addr, expr.Type, expr.Location)
		}

		arrVal := b.lowerExpr(expr.X)
		if arrVal == mir.InvalidValue {
			return mir.InvalidValue
		}

		indexVal := b.lowerExpr(expr.Index)
		if indexVal == mir.InvalidValue {
			return mir.InvalidValue
		}
		indexVal = b.castValue(indexVal, b.exprType(expr.Index), types.TypeI32, expr.Location)
		lenVal := b.emitConst(types.TypeI32, strconv.Itoa(arrType.Length), expr.Location)
		indexVal = b.emitBoundsCheckedIndex(indexVal, lenVal, types.TypeI32, expr.Location)
		if indexVal == mir.InvalidValue {
			return mir.InvalidValue
		}

		result := b.gen.nextValueID()
		b.emitInstr(&mir.ArrayGet{
			Result:   result,
			Array:    arrVal,
			Index:    indexVal,
			Type:     rawType,
			Location: expr.Location,
		})
		return unwrapValue(result)
	}

	addr := b.lowerIndexAddr(expr)
	if addr == mir.InvalidValue {
		return mir.InvalidValue
	}

	val := b.emitLoad(addr, rawType, expr.Location)
	return unwrapValue(val)
}

func (b *functionBuilder) lowerIndexAddr(expr *hir.IndexExpr) mir.ValueID {
	if expr == nil {
		return mir.InvalidValue
	}

	if mapType, ok := b.indexBaseType(expr.X).(*types.MapType); ok {
		mapVal := b.lowerExpr(expr.X)
		if mapVal == mir.InvalidValue {
			return mir.InvalidValue
		}

		keyVal := b.lowerExpr(expr.Index)
		if keyVal == mir.InvalidValue {
			return mir.InvalidValue
		}
		if mapType.Key != nil {
			keyVal = b.castValue(keyVal, b.exprType(expr.Index), mapType.Key, expr.Location)
		}

		result := b.gen.nextValueID()
		refType := types.NewReference(mapType.Value)
		b.emitInstr(&mir.MapGet{
			Result:   result,
			Map:      mapVal,
			Key:      keyVal,
			Type:     refType,
			Location: expr.Location,
		})
		b.ptrElem[result] = mapType.Value
		return result
	}

	arrType, _ := b.indexBaseType(expr.X).(*types.ArrayType)
	if arrType == nil {
		b.reportUnsupported("index base", expr.Loc())
		return mir.InvalidValue
	}

	if arrType.Length < 0 {
		arrVal := b.lowerExpr(expr.X)
		if arrVal == mir.InvalidValue {
			return mir.InvalidValue
		}

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

		ptrType := types.NewReference(arrType.Element)
		ptrVal := b.gen.nextValueID()
		b.emitInstr(&mir.Call{
			Result:   ptrVal,
			Target:   "ferret_array_get",
			Args:     []mir.ValueID{arrVal, indexVal},
			Type:     ptrType,
			Location: expr.Location,
		})
		b.ptrElem[ptrVal] = arrType.Element
		return ptrVal
	}

	baseType := b.exprType(expr.X)
	if baseType == nil {
		b.reportUnsupported("index base", expr.Loc())
		return mir.InvalidValue
	}

	basePtr := mir.InvalidValue
	if entry := b.narrowedOptionalEntry(expr.X); entry != nil {
		basePtr = b.optionalPayloadPtr(expr.X, entry)
		if basePtr == mir.InvalidValue {
			return mir.InvalidValue
		}
		baseType = types.UnwrapType(entry.NarrowedType)
		if ref, ok := baseType.(*types.ReferenceType); ok {
			basePtr = b.emitLoad(basePtr, baseType, expr.Location)
			baseType = types.UnwrapType(ref.Inner)
			b.ptrElem[basePtr] = ref.Inner
		}
	} else {
		baseType = types.UnwrapType(baseType)
		addressable := isAddressableExpr(expr.X)
		if addressable {
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

	if mapType, ok := b.indexBaseType(expr.X).(*types.MapType); ok {
		b.lowerMapIndexAssign(expr, rhs, op, loc, mapType)
		return
	}

	arrType, _ := b.indexBaseType(expr.X).(*types.ArrayType)
	if arrType != nil {
		// Use ArraySet MIR instruction for both fixed and dynamic arrays
		// This ensures consistent bounds checking and panicking behavior
		arrVal := b.lowerExpr(expr.X)
		if arrVal == mir.InvalidValue {
			return
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

		rhsIsMove := rhs != nil && isMoveExpr(rhs)
		if _, ok := dynamicArrayValueType(arrType.Element); ok {
			if !rhsIsMove && !b.isDynamicArrayLiteralExpr(rhs, arrType.Element) {
				value = b.emitArrayClone(value, arrType.Element, loc)
			}
		} else if _, ok := mapValueType(arrType.Element); ok {
			if !rhsIsMove && !b.isMapLiteralExpr(rhs, arrType.Element) {
				value = b.emitMapClone(value, arrType.Element, loc)
			}
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

	if mapType.Value != nil {
		rhsIsMove := rhs != nil && isMoveExpr(rhs)
		if _, ok := dynamicArrayValueType(mapType.Value); ok {
			if !rhsIsMove && !b.isDynamicArrayLiteralExpr(rhs, mapType.Value) {
				valueVal = b.emitArrayClone(valueVal, mapType.Value, loc)
			}
		} else if _, ok := mapValueType(mapType.Value); ok {
			if !rhsIsMove && !b.isMapLiteralExpr(rhs, mapType.Value) {
				valueVal = b.emitMapClone(valueVal, mapType.Value, loc)
			}
		}
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

	mapType, _ := baseType.(*types.MapType)
	return mapType
}

func (b *functionBuilder) indexBaseType(expr hir.Expr) types.SemType {
	if expr == nil {
		return nil
	}

	baseType := b.exprType(expr)
	if baseType == nil {
		return nil
	}
	baseType = types.UnwrapType(baseType)
	return baseType
}

type mapRuntimeFns struct {
	newFn         string
	fromPairsFn   string
	needsTypeInfo bool
}

// needsUniversalHashing returns true if the key type requires content-based hashing
func (b *functionBuilder) needsUniversalHashing(keyType types.SemType) bool {
	keyType = types.UnwrapType(keyType)
	switch kt := keyType.(type) {
	case *types.PrimitiveType:
		// Primitives have specialized hash functions
		switch kt.GetName() {
		case types.TYPE_I32, types.TYPE_I64, types.TYPE_F32, types.TYPE_F64, types.TYPE_STRING:
			return false
		}
		// Other primitives (bool, byte, etc.) could use universal or bytes
		return false
	case *types.NamedType:
		return b.needsUniversalHashing(kt.Underlying)
	case *types.MapType, *types.ArrayType, *types.StructType, *types.InterfaceType:
		// Complex types need universal hashing for content comparison
		return true
	}
	return false
}

func (b *functionBuilder) mapRuntimeFns(keyType types.SemType) mapRuntimeFns {
	keyType = types.UnwrapType(keyType)
	switch kt := keyType.(type) {
	case *types.PrimitiveType:
		// String needs special handling (pointer dereference)
		if kt.GetName() == types.TYPE_STRING {
			return mapRuntimeFns{
				newFn:       "ferret_map_new_str",
				fromPairsFn: "ferret_map_from_pairs_str",
			}
		}
		// All numeric types (integers and floats) use generic numeric functions
		if types.IsNumericTypeName(kt.GetName()) {
			return mapRuntimeFns{
				newFn:       "ferret_map_new_numeric",
				fromPairsFn: "ferret_map_from_pairs_numeric",
			}
		}
	case *types.NamedType:
		return b.mapRuntimeFns(kt.Underlying)
	}
	// For complex types, use universal hashing if needed
	if b.needsUniversalHashing(keyType) {
		return mapRuntimeFns{
			newFn:         "ferret_map_new_universal",
			fromPairsFn:   "ferret_map_from_pairs_universal",
			needsTypeInfo: true,
		}
	}
	// Default to bytes for simple composite types
	return mapRuntimeFns{
		newFn:       "ferret_map_new_bytes",
		fromPairsFn: "ferret_map_from_pairs_bytes",
	}
}

// getOrCreateTypeDescriptor returns a ValueID for the type descriptor global address
// The actual type descriptor structure will be created during C codegen based on the registered type
func (b *functionBuilder) getOrCreateTypeDescriptor(typ types.SemType) mir.ValueID {
	if b.gen == nil {
		return mir.InvalidValue
	}

	// Generate unique key for this type
	key := b.typeDescriptorKey(typ)

	// Check if we already have a global name for this type
	globalName, exists := b.gen.typeDescriptors[key]
	if !exists {
		// Register new type descriptor
		b.gen.typeDescSeq++
		globalName = fmt.Sprintf("$typedesc%d", b.gen.typeDescSeq)
		b.gen.typeDescriptors[key] = globalName
		// Store the type for QBE codegen to emit the actual structure
		b.gen.typeDescTypes[globalName] = typ
	}

	// Emit a constant that references the global type descriptor
	// QBE will see this as the address of the global
	return b.emitConst(types.NewReference(types.TypeVoid), globalName, source.Location{})
}

// typeDescriptorKey generates a unique key for a type
func (b *functionBuilder) typeDescriptorKey(typ types.SemType) string {
	return b.typeDescriptorKeyWithSeen(typ, make(map[types.SemType]bool))
}

func (b *functionBuilder) typeDescriptorKeyWithSeen(typ types.SemType, seen map[types.SemType]bool) string {
	typ = types.UnwrapType(typ)
	switch t := typ.(type) {
	case *types.PrimitiveType:
		return "prim_" + string(t.GetName())
	case *types.MapType:
		return "map_" + b.typeDescriptorKeyWithSeen(t.Key, seen) + "_" + b.typeDescriptorKeyWithSeen(t.Value, seen)
	case *types.ArrayType:
		if t.Length < 0 {
			return fmt.Sprintf("slice_%s", b.typeDescriptorKeyWithSeen(t.Element, seen))
		}
		return fmt.Sprintf("array_%d_%s", t.Length, b.typeDescriptorKeyWithSeen(t.Element, seen))
	case *types.StructType:
		if seen[typ] {
			return fmt.Sprintf("struct_rec_%p", t)
		}
		seen[typ] = true
		var sb strings.Builder
		sb.WriteString("struct{")
		for i, field := range t.Fields {
			if i > 0 {
				sb.WriteString(";")
			}
			sb.WriteString(field.Name)
			sb.WriteString(":")
			sb.WriteString(b.typeDescriptorKeyWithSeen(field.Type, seen))
		}
		sb.WriteString("}")
		return sb.String()
	case *types.InterfaceType:
		if len(t.Methods) == 0 {
			return "interface_empty"
		}
		if t.ID != "" {
			return "interface_" + t.ID
		}
		return fmt.Sprintf("interface_%p", t)
	case *types.ReferenceType:
		return "ref_" + b.typeDescriptorKeyWithSeen(t.Inner, seen)
	case *types.NamedType:
		return "named_" + t.Name + "_" + b.typeDescriptorKeyWithSeen(t.Underlying, seen)
	default:
		return fmt.Sprintf("unknown_%T", typ)
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

	var val mir.ValueID
	if addr, ok := b.slots[ident.Symbol]; ok {
		val = b.emitLoad(addr, typ, ident.Location)
	} else if ident.Symbol.Kind == symbols.SymbolParameter || ident.Symbol.Kind == symbols.SymbolReceiver {
		if param, ok := b.paramsByName[ident.Name]; ok {
			val = param
		}
	}
	box := b.emitHeapAlloc(val, typ, ident.Location)
	if box == mir.InvalidValue {
		return mir.InvalidValue
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
			recv := b.lowerExpr(expr.X)
			return recv
		}
	}

	if isAddressableExpr(expr.X) {
		recv := b.lowerLValue(expr.X)
		return recv
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
					return constValueLiteral(sym.ConstValue)
				}
				return "", false
			}
		}
	}

	if mod.ModuleScope == nil {
		return "", false
	}
	if sym, ok := mod.ModuleScope.GetSymbol(name); ok && sym.ConstValue != nil && sym.ConstValue.IsConstant() {
		return constValueLiteral(sym.ConstValue)
	}
	return "", false
}

func (b *functionBuilder) lookupQualifiedGlobal(name string) (string, *symbols.Symbol, bool) {
	if b.gen == nil || b.gen.mod == nil || b.gen.ctx == nil {
		return "", nil, false
	}
	parts := strings.Split(name, "::")
	if len(parts) < 2 {
		return "", nil, false
	}
	moduleAlias := parts[0]
	if moduleAlias == "" {
		return "", nil, false
	}
	importPath := ""
	if b.gen.mod.ImportAliasMap != nil {
		importPath = b.gen.mod.ImportAliasMap[moduleAlias]
	}
	if importPath == "" {
		return "", nil, false
	}
	imported, ok := b.gen.ctx.GetModule(importPath)
	if !ok || imported == nil || imported.ModuleScope == nil {
		return "", nil, false
	}
	symName := strings.Join(parts[1:], "::")
	sym, ok := imported.ModuleScope.GetSymbol(symName)
	if !ok || sym == nil {
		return "", nil, false
	}
	if sym.Kind != symbols.SymbolVariable && sym.Kind != symbols.SymbolConstant {
		return "", nil, false
	}
	return globalSymbolName(importPath, sym.Name), sym, true
}

func constValueLiteral(value symbols.ConstValue) (string, bool) {
	if value == nil || !value.IsConstant() {
		return "", false
	}
	if cv, ok := value.(*consteval.ConstValue); ok {
		if str, ok := cv.AsString(); ok {
			return str, true
		}
	}
	return value.String(), true
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
			return constValueLiteral(p.Symbol.ConstValue)
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
			if str, ok := value.AsString(); ok {
				return str, true
			}
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

func exprKeyHIR(expr hir.Expr) (string, bool) {
	if expr == nil {
		return "", false
	}

	switch e := expr.(type) {
	case *hir.Ident:
		return e.Name, true
	case *hir.Literal:
		return e.Value, true
	case *hir.SelectorExpr:
		base, ok := exprKeyHIR(e.X)
		if !ok || e.Field == nil {
			return "", false
		}
		return base + "." + e.Field.Name, true
	case *hir.IndexExpr:
		base, ok := exprKeyHIR(e.X)
		if !ok {
			return "", false
		}
		indexKey, ok := exprKeyHIR(e.Index)
		if !ok {
			return "", false
		}
		return base + "[" + indexKey + "]", true
	case *hir.ParenExpr:
		return exprKeyHIR(e.X)
	case *hir.DerefExpr:
		base, ok := exprKeyHIR(e.X)
		if !ok {
			return "", false
		}
		return "*" + base, true
	default:
		return "", false
	}
}

func (b *functionBuilder) narrowedEntry(expr hir.Expr) *narrowing.NarrowingEntry {
	if b == nil || b.narrowedEntries == nil {
		return nil
	}
	key, ok := exprKeyHIR(expr)
	if !ok {
		return nil
	}
	return b.narrowedEntries[key]
}

func (b *functionBuilder) narrowedOptionalEntry(expr hir.Expr) *narrowing.NarrowingEntry {
	entry := b.narrowedEntry(expr)
	if entry == nil || entry.Kind != narrowing.NarrowingOptional {
		return nil
	}
	if entry.NarrowedType == nil || entry.NarrowedType.Equals(types.TypeNone) {
		return nil
	}
	return entry
}

func (b *functionBuilder) optionalPayloadPtr(expr hir.Expr, entry *narrowing.NarrowingEntry) mir.ValueID {
	if entry == nil || entry.OriginalType == nil {
		return mir.InvalidValue
	}
	addr := b.lowerLValue(expr)
	if addr == mir.InvalidValue {
		return mir.InvalidValue
	}
	loc := source.Location{}
	if expr != nil && expr.Loc() != nil {
		loc = *expr.Loc()
	}
	return b.emitLoad(addr, entry.OriginalType, loc)
}

func (b *functionBuilder) exprType(expr hir.Expr) types.SemType {
	if entry := b.narrowedEntry(expr); entry != nil && entry.NarrowedType != nil {
		return entry.NarrowedType
	}
	switch e := expr.(type) {
	case *hir.Literal:
		return e.Type
	case *hir.FuncLit:
		return e.Type
	case *hir.Ident:
		// Check if this is a parameter with an overridden MIR type
		// This ensures field access through reference parameters works correctly
		if mirType, ok := b.paramTypes[e.Name]; ok {
			return mirType
		}
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
	case *hir.CastExpr:
		if e.Type != nil {
			return e.Type
		}
		if e.TargetType != nil {
			return e.TargetType
		}
		return b.exprType(e.X)
	case *hir.CoalescingExpr:
		return e.Type
	case *hir.ForkExpr:
		return e.Type
	case *hir.RangeExpr:
		return e.Type
	case *hir.ArrayLenExpr:
		return e.Type
	case *hir.StringLenExpr:
		return e.Type
	case *hir.MapIterInitExpr:
		return e.Type
	case *hir.MapIterNextExpr:
		return e.Type
	case *hir.ParenExpr:
		if e.Type != nil && !e.Type.Equals(types.TypeUnknown) {
			return e.Type
		}
		return b.exprType(e.X)
	case *hir.CompositeLit:
		return e.Type
	case *hir.OptionalNone:
		return e.Type
	case *hir.OptionalSome:
		return e.Type
	case *hir.OptionalIsSome:
		return e.Type
	case *hir.OptionalIsNone:
		return e.Type
	case *hir.OptionalUnwrap:
		return e.Type
	case *hir.UnionVariantCheck:
		return types.TypeBool
	case *hir.UnionExtract:
		return e.Type
	case *hir.ResultOk:
		return e.Type
	case *hir.ResultErr:
		return e.Type
	case *hir.ResultUnwrap:
		return e.Type
	case *hir.DerefExpr:
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
	case *hir.Ident, *hir.SelectorExpr, *hir.IndexExpr, *hir.ScopeResolutionExpr:
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
	// For zero-size types (empty structs), memcpy is a no-op
	if size <= 0 {
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

func typeNeedsDeepCopy(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	typ = types.UnwrapType(typ)
	switch t := typ.(type) {
	case *types.ReferenceType:
		return false
	case *types.MapType:
		return true
	case *types.ArrayType:
		if t.Length < 0 {
			return true
		}
		return typeNeedsDeepCopy(t.Element)
	case *types.StructType:
		for _, field := range t.Fields {
			if typeNeedsDeepCopy(field.Type) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (b *functionBuilder) emitDeepCopy(dst, src mir.ValueID, typ types.SemType, loc source.Location) {
	if typ == nil {
		b.reportUnsupported("deep copy type", &loc)
		return
	}
	if b.gen == nil || b.gen.layout == nil {
		b.emitMemcpy(dst, src, typ, loc)
		return
	}
	typ = types.UnwrapType(typ)
	switch t := typ.(type) {
	case *types.StructType:
		layout := b.gen.layout.StructLayout(t)
		for _, field := range layout.Fields {
			fieldDst := b.emitPtrAdd(dst, field.Offset, field.Type, loc)
			fieldSrc := b.emitPtrAdd(src, field.Offset, field.Type, loc)
			val := fieldSrc
			if !needsByRefType(field.Type) {
				val = b.emitLoad(fieldSrc, field.Type, loc)
			}
			b.emitStore(fieldDst, val, loc)
		}
	case *types.ArrayType:
		if t.Length < 0 {
			val := b.emitArrayClone(src, typ, loc)
			b.emitInstr(&mir.Store{
				Addr:     dst,
				Value:    val,
				Location: loc,
			})
			return
		}
		elemSize := b.gen.layout.SizeOf(t.Element)
		if elemSize <= 0 {
			b.emitMemcpy(dst, src, typ, loc)
			return
		}
		for i := 0; i < t.Length; i++ {
			offset := i * elemSize
			elemDst := b.emitPtrAdd(dst, offset, t.Element, loc)
			elemSrc := b.emitPtrAdd(src, offset, t.Element, loc)
			val := elemSrc
			if !needsByRefType(t.Element) {
				val = b.emitLoad(elemSrc, t.Element, loc)
			}
			b.emitStore(elemDst, val, loc)
		}
	default:
		b.emitMemcpy(dst, src, typ, loc)
	}
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
	case tokens.BIT_NOT_TOKEN:
		typeName, ok := largePrimitiveName(typ)
		if !ok {
			b.reportUnsupported("large unary", &loc)
			return mir.InvalidValue
		}
		out := b.emitAlloca(typ, loc)
		b.emitInstr(&mir.Call{
			Result:   mir.InvalidValue,
			Target:   "ferret_" + typeName + "_not_ptr",
			Args:     []mir.ValueID{value, out},
			Type:     types.TypeVoid,
			Location: loc,
		})
		return out
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

func (b *functionBuilder) emitArrayClone(value mir.ValueID, arrType types.SemType, loc source.Location) mir.ValueID {
	if value == mir.InvalidValue {
		return value
	}
	id := b.gen.nextValueID()
	b.emitInstr(&mir.Call{
		Result:   id,
		Target:   "ferret_array_clone",
		Args:     []mir.ValueID{value},
		Type:     arrType,
		Location: loc,
	})
	return id
}

func (b *functionBuilder) emitMapClone(value mir.ValueID, mapType types.SemType, loc source.Location) mir.ValueID {
	if value == mir.InvalidValue {
		return value
	}
	id := b.gen.nextValueID()
	b.emitInstr(&mir.Call{
		Result:   id,
		Target:   "ferret_map_clone",
		Args:     []mir.ValueID{value},
		Type:     mapType,
		Location: loc,
	})
	return id
}

func (b *functionBuilder) emitHeapAlloc(value mir.ValueID, typ types.SemType, loc source.Location) mir.ValueID {
	if typ == nil {
		b.reportUnsupported("heap alloc type", &loc)
		return mir.InvalidValue
	}
	size := b.gen.layout.SizeOf(typ)
	if size <= 0 {
		b.reportUnsupported("heap alloc size", &loc)
		return mir.InvalidValue
	}
	sizeVal := b.emitConst(types.TypeU64, strconv.Itoa(size), loc)
	box := b.gen.nextValueID()
	b.emitInstr(&mir.Call{
		Result:   box,
		Target:   "ferret_alloc",
		Args:     []mir.ValueID{sizeVal},
		Type:     types.NewReference(typ),
		Location: loc,
	})
	b.ptrElem[box] = typ
	if value != mir.InvalidValue {
		b.emitStore(box, value, loc)
	}
	return box
}

func (b *functionBuilder) emitStore(addr, value mir.ValueID, loc source.Location) {
	if elem, ok := b.ptrElem[addr]; ok {
		if _, ok := dynamicArrayValueType(elem); ok {
			value = b.emitArrayClone(value, elem, loc)
		} else if _, ok := mapValueType(elem); ok {
			value = b.emitMapClone(value, elem, loc)
		}
		if needsByRefType(elem) {
			if typeNeedsDeepCopy(elem) {
				b.emitDeepCopy(addr, value, elem, loc)
			} else {
				b.emitMemcpy(addr, value, elem, loc)
			}
			return
		}
	}
	b.emitInstr(&mir.Store{
		Addr:     addr,
		Value:    value,
		Location: loc,
	})
}

func (b *functionBuilder) emitStoreMove(addr, value mir.ValueID, loc source.Location) {
	if elem, ok := b.ptrElem[addr]; ok {
		if needsByRefType(elem) {
			b.emitMemcpy(addr, value, elem, loc)
			return
		}
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
	if rightType == nil {
		b.reportUnsupported("string concat rhs type", &loc)
		rightType = types.TypeString
	}
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

	// Handle string <-> array conversions with runtime functions
	fromUnwrapped := types.UnwrapType(from)
	toUnwrapped := types.UnwrapType(to)

	// str -> []char
	if fromPrim, ok := fromUnwrapped.(*types.PrimitiveType); ok && fromPrim.GetName() == types.TYPE_STRING {
		if toArr, ok := toUnwrapped.(*types.ArrayType); ok && toArr.Length < 0 {
			if elemPrim, ok := toArr.Element.(*types.PrimitiveType); ok && elemPrim.GetName() == types.TYPE_CHAR {
				// Call ferret_string_to_char_array
				typeIDPtr := b.emitTypeIDPtr(toArr.Element, loc)
				result := b.gen.nextValueID()
				b.emitInstr(&mir.Call{
					Result:   result,
					Target:   "ferret_string_to_char_array",
					Args:     []mir.ValueID{value, typeIDPtr},
					Type:     to,
					Location: loc,
				})
				return result
			}
		}
	}

	// str -> []byte
	if fromPrim, ok := fromUnwrapped.(*types.PrimitiveType); ok && fromPrim.GetName() == types.TYPE_STRING {
		if toArr, ok := toUnwrapped.(*types.ArrayType); ok && toArr.Length < 0 {
			if elemPrim, ok := toArr.Element.(*types.PrimitiveType); ok && (elemPrim.GetName() == types.TYPE_BYTE || elemPrim.GetName() == types.TYPE_U8) {
				// Call ferret_string_to_byte_array
				typeIDPtr := b.emitTypeIDPtr(toArr.Element, loc)
				result := b.gen.nextValueID()
				b.emitInstr(&mir.Call{
					Result:   result,
					Target:   "ferret_string_to_byte_array",
					Args:     []mir.ValueID{value, typeIDPtr},
					Type:     to,
					Location: loc,
				})
				return result
			}
		}
	}

	// []char -> str
	if fromArr, ok := fromUnwrapped.(*types.ArrayType); ok && fromArr.Length < 0 {
		if elemPrim, ok := fromArr.Element.(*types.PrimitiveType); ok && elemPrim.GetName() == types.TYPE_CHAR {
			if toPrim, ok := toUnwrapped.(*types.PrimitiveType); ok && toPrim.GetName() == types.TYPE_STRING {
				// Call ferret_char_array_to_string
				result := b.gen.nextValueID()
				b.emitInstr(&mir.Call{
					Result:   result,
					Target:   "ferret_char_array_to_string",
					Args:     []mir.ValueID{value},
					Type:     to,
					Location: loc,
				})
				return result
			}
		}
	}

	// []byte -> str
	if fromArr, ok := fromUnwrapped.(*types.ArrayType); ok && fromArr.Length < 0 {
		if elemPrim, ok := fromArr.Element.(*types.PrimitiveType); ok && (elemPrim.GetName() == types.TYPE_BYTE || elemPrim.GetName() == types.TYPE_U8) {
			if toPrim, ok := toUnwrapped.(*types.PrimitiveType); ok && toPrim.GetName() == types.TYPE_STRING {
				// Call ferret_byte_array_to_string
				result := b.gen.nextValueID()
				b.emitInstr(&mir.Call{
					Result:   result,
					Target:   "ferret_byte_array_to_string",
					Args:     []mir.ValueID{value},
					Type:     to,
					Location: loc,
				})
				return result
			}
		}
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
