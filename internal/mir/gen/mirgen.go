package gen

import (
	"fmt"

	"compiler/internal/context_v2"
	"compiler/internal/hir"
	"compiler/internal/mir"
	"compiler/internal/semantics/symbols"
	"compiler/internal/source"
	"compiler/internal/types"
)

type captureInfo struct {
	ident  *hir.Ident
	offset int
	typ    types.SemType
}

type closureInfo struct {
	name     string
	envType  *types.StructType
	captures []captureInfo
}

// Generator lowers lowered HIR into MIR.
type Generator struct {
	ctx             *context_v2.CompilerContext
	mod             *context_v2.Module
	layout          *mir.DataLayout
	nextValue       mir.ValueID
	nextBlock       mir.BlockID
	funcLits        map[string]*closureInfo
	extraFns        []*mir.Function
	funcWraps       map[string]string
	wrapID          int
	vtableSeq       int
	vtables         map[string]*mir.VTable
	typeIDGlobals   map[string]string // Maps type ID string to global name
	typeIDSeq       int               // Counter for generating unique type ID global names
	heapReturns     map[string]types.SemType
	typeDescriptors map[string]string        // Maps type key to type descriptor global name
	typeDescTypes   map[string]types.SemType // Maps global name to type for codegen
	typeDescSeq     int                      // Counter for type descriptor names
}

// New creates a new MIR generator for a module.
func New(ctx *context_v2.CompilerContext, mod *context_v2.Module) *Generator {
	pointerSize := 8
	if ctx != nil && ctx.Config != nil && ctx.Config.PointerSize > 0 {
		pointerSize = ctx.Config.PointerSize
	}
	return &Generator{
		ctx:             ctx,
		mod:             mod,
		layout:          mir.NewDataLayout(pointerSize),
		funcLits:        make(map[string]*closureInfo),
		funcWraps:       make(map[string]string),
		vtables:         make(map[string]*mir.VTable),
		typeIDGlobals:   make(map[string]string),
		heapReturns:     make(map[string]types.SemType),
		typeDescriptors: make(map[string]string),
		typeDescTypes:   make(map[string]types.SemType),
	}
}

// GenerateModule lowers a lowered HIR module into MIR.
func (g *Generator) GenerateModule(hirMod *hir.Module) *mir.Module {
	if g == nil || g.mod == nil || hirMod == nil {
		return nil
	}

	g.loadHeapReturns()

	initNodes, globals := g.collectGlobals(hirMod)

	mirMod := &mir.Module{
		ImportPath: g.mod.ImportPath,
		Location:   hirMod.Location,
		Globals:    globals,
	}

	for _, item := range hirMod.Items {
		switch n := item.(type) {
		case *hir.FuncDecl:
			if fn := g.lowerFuncDecl(n); fn != nil {
				mirMod.Functions = append(mirMod.Functions, fn)
			}
		case *hir.MethodDecl:
			if fn := g.lowerMethodDecl(n); fn != nil {
				mirMod.Functions = append(mirMod.Functions, fn)
			}
		}
	}
	if initFn := g.buildInitFunction(initNodes, hirMod.Location); initFn != nil {
		mirMod.Functions = append(mirMod.Functions, initFn)
	}
	if len(g.extraFns) > 0 {
		mirMod.Functions = append(mirMod.Functions, g.extraFns...)
	}
	if len(g.vtables) > 0 {
		mirMod.VTables = make([]mir.VTable, 0, len(g.vtables))
		for _, table := range g.vtables {
			if table == nil {
				continue
			}
			mirMod.VTables = append(mirMod.VTables, *table)
		}
	}
	if len(g.typeIDGlobals) > 0 {
		mirMod.TypeIDs = make(map[string]string, len(g.typeIDGlobals))
		for typeID, globalName := range g.typeIDGlobals {
			mirMod.TypeIDs[globalName] = typeID
		}
	}
	if len(g.typeDescTypes) > 0 {
		mirMod.TypeDescriptors = make(map[string]types.SemType, len(g.typeDescTypes))
		for globalName, typ := range g.typeDescTypes {
			mirMod.TypeDescriptors[globalName] = typ
		}
	}

	g.injectEntryInitCall(mirMod)

	return mirMod
}

func (g *Generator) lowerFuncDecl(decl *hir.FuncDecl) *mir.Function {
	if decl == nil || decl.Name == nil || decl.Body == nil {
		return nil
	}

	retType := g.returnType(decl.Type)
	if heapRet, ok := g.heapReturnType(decl.Name.Name); ok {
		retType = types.NewReference(heapRet)
	}
	fn := &mir.Function{
		Name:     decl.Name.Name,
		Return:   retType,
		Location: decl.Location,
	}

	fn.Params = g.lowerParams(decl.Type)
	g.applyRefReturnABI(fn, retType, decl.Location, g.isHeapReturnFunc(fn.Name))
	g.applyLargeReturnABI(fn, retType, decl.Location)
	builder := newFunctionBuilder(g, fn)
	builder.buildFuncBody(decl.Body)

	return fn
}

func (g *Generator) lowerMethodDecl(decl *hir.MethodDecl) *mir.Function {
	if decl == nil || decl.Name == nil || decl.Body == nil {
		return nil
	}

	retType := g.returnType(decl.Type)
	fnName := g.methodName(decl)
	if heapRet, ok := g.heapReturnType(fnName); ok {
		retType = types.NewReference(heapRet)
	}
	fn := &mir.Function{
		Name:     fnName,
		Return:   retType,
		Location: decl.Location,
	}

	if decl.Receiver != nil {
		recvType := decl.Receiver.Type
		_, recvIsRef := types.UnwrapType(recvType).(*types.ReferenceType)
		if needsByRefType(recvType) {
			recvType = types.NewReference(recvType)
		}
		recv := g.newParam(decl.Receiver.Name, recvType, decl.Receiver.Location)
		fn.Receiver = &recv
		fn.Params = append(fn.Params, recv)
		if recvIsRef {
			fn.Params = append(fn.Params, g.newParam(refHeapParamName(-1), types.TypeU64, decl.Receiver.Location))
		}
	}

	fn.Params = append(fn.Params, g.lowerParams(decl.Type)...)
	g.applyRefReturnABI(fn, retType, decl.Location, g.isHeapReturnFunc(fn.Name))
	g.applyLargeReturnABI(fn, retType, decl.Location)
	builder := newFunctionBuilder(g, fn)
	builder.buildFuncBody(decl.Body)

	return fn
}

func (g *Generator) methodName(decl *hir.MethodDecl) string {
	if decl == nil || decl.Name == nil {
		return ""
	}
	recvName := g.receiverTypeName(decl.Receiver)
	if recvName == "" {
		return decl.Name.Name
	}
	return recvName + "_" + decl.Name.Name
}

func (g *Generator) receiverTypeName(recv *hir.Param) string {
	if recv == nil || recv.Type == nil {
		return ""
	}
	recvType := recv.Type
	if ref, ok := recvType.(*types.ReferenceType); ok {
		recvType = ref.Inner
	}
	if named, ok := recvType.(*types.NamedType); ok {
		return named.Name
	}
	return ""
}

func (g *Generator) lowerParams(fnType *types.FunctionType) []mir.Param {
	if fnType == nil || len(fnType.Params) == 0 {
		return nil
	}

	params := make([]mir.Param, 0, len(fnType.Params))
	for i, param := range fnType.Params {
		paramType := param.Type
		_, isUserRef := types.UnwrapType(paramType).(*types.ReferenceType)

		// Convert variadic parameters (...T) to slice type ([]T) for MIR
		// The FunctionType keeps IsVariadic=true for call sites,
		// but MIR needs the actual slice type for the function signature
		if param.IsVariadic {
			paramType = types.NewArray(paramType, -1) // []T
		}

		if needsByRefType(paramType) {
			paramType = types.NewReference(paramType)
		}
		params = append(params, g.newParam(param.Name, paramType, source.Location{}))
		if isUserRef {
			params = append(params, g.newParam(refHeapParamName(i), types.TypeU64, source.Location{}))
		}
	}
	return params
}

func (g *Generator) returnType(fnType *types.FunctionType) types.SemType {
	if fnType == nil || fnType.Return == nil {
		return types.TypeVoid
	}
	return fnType.Return
}

func (g *Generator) closureForFuncLit(lit *hir.FuncLit) *closureInfo {
	if lit == nil {
		return nil
	}
	if info, ok := g.funcLits[lit.ID]; ok {
		return info
	}

	fnType, ok := types.UnwrapType(lit.Type).(*types.FunctionType)
	if !ok || fnType == nil {
		if g.ctx != nil {
			g.ctx.ReportError("mir: func literal missing function type", &lit.Location)
		}
		return nil
	}

	captureIdents := lit.Captures
	if len(captureIdents) == 0 {
		captureIdents = g.inferFuncLitCaptures(lit)
	}

	fields := []types.StructField{{Name: "__fn", Type: types.TypeU64}}
	captures := make([]captureInfo, 0, len(captureIdents))
	for i, ident := range captureIdents {
		if ident == nil {
			continue
		}
		fieldType := types.NewReference(ident.Type)
		fields = append(fields, types.StructField{
			Name: fmt.Sprintf("__cap%d", i),
			Type: fieldType,
		})
	}

	envType := &types.StructType{Fields: fields}
	layout := g.layout.StructLayout(envType)
	for i, ident := range captureIdents {
		if ident == nil {
			continue
		}
		fieldType := types.NewReference(ident.Type)
		name := fmt.Sprintf("__cap%d", i)
		offset, ok := layout.FieldOffset(name)
		if !ok {
			continue
		}
		captures = append(captures, captureInfo{
			ident:  ident,
			offset: offset,
			typ:    fieldType,
		})
	}

	info := &closureInfo{
		name:     lit.ID,
		envType:  envType,
		captures: captures,
	}
	g.funcLits[lit.ID] = info

	retType := g.returnType(fnType)
	fn := &mir.Function{
		Name:     info.name,
		Return:   retType,
		Location: lit.Location,
	}

	envParam := g.newParam("__env", types.NewReference(envType), lit.Location)
	fn.Params = append(fn.Params, envParam)
	fn.Params = append(fn.Params, g.lowerParams(fnType)...)
	g.applyRefReturnABI(fn, retType, lit.Location, false)
	g.applyLargeReturnABI(fn, retType, lit.Location)

	builder := newFunctionBuilder(g, fn)
	builder.setClosureEnv(envParam.ID, captures)
	builder.buildFuncBody(lit.Body)

	g.extraFns = append(g.extraFns, fn)

	return info
}

func (g *Generator) inferFuncLitCaptures(lit *hir.FuncLit) []*hir.Ident {
	if g == nil || lit == nil || lit.Body == nil {
		return nil
	}

	locals := make(map[*symbols.Symbol]struct{})
	seen := make(map[*symbols.Symbol]struct{})
	captures := make([]*hir.Ident, 0)

	var collectLocals func(hir.Node)
	collectLocals = func(node hir.Node) {
		if node == nil {
			return
		}
		switch n := node.(type) {
		case *hir.Block:
			for _, stmt := range n.Nodes {
				collectLocals(stmt)
			}
		case *hir.DeclStmt:
			collectLocals(n.Decl)
		case *hir.VarDecl:
			for _, item := range n.Decls {
				if item.Name != nil && item.Name.Symbol != nil {
					locals[item.Name.Symbol] = struct{}{}
				}
			}
		case *hir.ConstDecl:
			for _, item := range n.Decls {
				if item.Name != nil && item.Name.Symbol != nil {
					locals[item.Name.Symbol] = struct{}{}
				}
			}
		case *hir.IfStmt:
			collectLocals(n.Body)
			collectLocals(n.Else)
		case *hir.ForStmt:
			collectLocals(n.Iterator)
			collectLocals(n.Body)
		case *hir.WhileStmt:
			collectLocals(n.Body)
		case *hir.MatchStmt:
			for _, clause := range n.Cases {
				collectLocals(clause.Body)
			}
		}
	}

	var visitExpr func(hir.Expr)
	var visitNode func(hir.Node)

	visitExpr = func(expr hir.Expr) {
		if expr == nil {
			return
		}
		switch e := expr.(type) {
		case *hir.Ident:
			sym := e.Symbol
			if sym == nil {
				return
			}
			switch sym.Kind {
			case symbols.SymbolFunction, symbols.SymbolType, symbols.SymbolParameter, symbols.SymbolReceiver:
				return
			}
			if _, ok := locals[sym]; ok {
				return
			}
			if g.mod != nil && g.mod.ModuleScope != nil && sym.DeclaredScope == g.mod.ModuleScope {
				return
			}
			if _, ok := seen[sym]; ok {
				return
			}
			seen[sym] = struct{}{}
			captures = append(captures, e)
		case *hir.FuncLit:
			return
		case *hir.BinaryExpr:
			visitExpr(e.X)
			visitExpr(e.Y)
		case *hir.UnaryExpr:
			visitExpr(e.X)
		case *hir.PrefixExpr:
			visitExpr(e.X)
		case *hir.PostfixExpr:
			visitExpr(e.X)
		case *hir.CallExpr:
			visitExpr(e.Fun)
			for _, arg := range e.Args {
				visitExpr(arg)
			}
			if e.Catch != nil {
				if e.Catch.Handler != nil {
					for _, node := range e.Catch.Handler.Nodes {
						visitNode(node)
					}
				}
				visitExpr(e.Catch.Fallback)
			}
		case *hir.SelectorExpr:
			visitExpr(e.X)
		case *hir.ScopeResolutionExpr:
			visitExpr(e.X)
		case *hir.IndexExpr:
			visitExpr(e.X)
			visitExpr(e.Index)
		case *hir.CastExpr:
			visitExpr(e.X)
		case *hir.ParenExpr:
			visitExpr(e.X)
		case *hir.CompositeLit:
			for _, elt := range e.Elts {
				visitExpr(elt)
			}
		case *hir.KeyValueExpr:
			visitExpr(e.Value)
		case *hir.OptionalSome:
			visitExpr(e.Value)
		case *hir.OptionalIsSome:
			visitExpr(e.Value)
		case *hir.OptionalIsNone:
			visitExpr(e.Value)
		case *hir.OptionalUnwrap:
			visitExpr(e.Value)
			visitExpr(e.Default)
		case *hir.ResultOk:
			visitExpr(e.Value)
		case *hir.ResultErr:
			visitExpr(e.Value)
		case *hir.ResultUnwrap:
			visitExpr(e.Value)
			if e.Catch != nil {
				if e.Catch.Handler != nil {
					for _, node := range e.Catch.Handler.Nodes {
						visitNode(node)
					}
				}
				visitExpr(e.Catch.Fallback)
			}
		case *hir.CoalescingExpr:
			visitExpr(e.Cond)
			visitExpr(e.Default)
		case *hir.RangeExpr:
			visitExpr(e.Start)
			visitExpr(e.End)
			visitExpr(e.Incr)
		case *hir.ArrayLenExpr:
			visitExpr(e.X)
		case *hir.StringLenExpr:
			visitExpr(e.X)
		case *hir.MapIterInitExpr:
			visitExpr(e.Map)
		case *hir.MapIterNextExpr:
			visitExpr(e.Map)
			visitExpr(e.Iter)
		}
	}

	visitNode = func(node hir.Node) {
		if node == nil {
			return
		}
		switch n := node.(type) {
		case *hir.Block:
			for _, stmt := range n.Nodes {
				visitNode(stmt)
			}
		case *hir.DeclStmt:
			visitNode(n.Decl)
		case *hir.VarDecl:
			for _, item := range n.Decls {
				visitExpr(item.Value)
			}
		case *hir.ConstDecl:
			for _, item := range n.Decls {
				visitExpr(item.Value)
			}
		case *hir.AssignStmt:
			visitExpr(n.Lhs)
			visitExpr(n.Rhs)
		case *hir.ReturnStmt:
			visitExpr(n.Result)
		case *hir.ExprStmt:
			visitExpr(n.X)
		case *hir.IfStmt:
			visitExpr(n.Cond)
			visitNode(n.Body)
			visitNode(n.Else)
		case *hir.ForStmt:
			visitNode(n.Iterator)
			visitExpr(n.Range)
			visitNode(n.Body)
		case *hir.WhileStmt:
			visitExpr(n.Cond)
			visitNode(n.Body)
		case *hir.MatchStmt:
			visitExpr(n.Expr)
			for _, clause := range n.Cases {
				if clause.Pattern != nil {
					visitExpr(clause.Pattern)
				}
				visitNode(clause.Body)
			}
		case *hir.DeferStmt:
			visitExpr(n.Call)
			if n.Catch != nil {
				if n.Catch.Handler != nil {
					for _, node := range n.Catch.Handler.Nodes {
						visitNode(node)
					}
				}
				visitExpr(n.Catch.Fallback)
			}
		}
	}

	collectLocals(lit.Body)
	visitNode(lit.Body)
	return captures
}

func (g *Generator) funcValueWrapper(name string, fnType *types.FunctionType, envType *types.StructType, loc source.Location) string {
	if g == nil || fnType == nil {
		return ""
	}
	key := name + "|" + fnType.String()
	if wrapper, ok := g.funcWraps[key]; ok {
		return wrapper
	}
	g.wrapID++
	wrapper := fmt.Sprintf("__func_wrap__%d", g.wrapID)
	g.funcWraps[key] = wrapper

	retType := g.returnType(fnType)
	fn := &mir.Function{
		Name:     wrapper,
		Return:   retType,
		Location: loc,
	}

	envParam := g.newParam("__env", types.NewReference(envType), loc)
	fn.Params = append(fn.Params, envParam)
	origParams := g.lowerParams(fnType)
	fn.Params = append(fn.Params, origParams...)
	g.applyRefReturnABI(fn, retType, loc, false)
	g.applyLargeReturnABI(fn, retType, loc)

	entry := &mir.Block{
		ID:       g.nextBlockID(),
		Name:     "entry",
		Location: loc,
	}

	callArgs := make([]mir.ValueID, 0, len(origParams)+3)
	if _, ok := types.UnwrapType(retType).(*types.ReferenceType); ok {
		outParam := mir.InvalidValue
		outHeapParam := mir.InvalidValue
		for _, param := range fn.Params {
			switch param.Name {
			case "__out":
				outParam = param.ID
			case outHeapParamName:
				outHeapParam = param.ID
			}
		}
		if outParam != mir.InvalidValue {
			callArgs = append(callArgs, outParam)
		}
		if outHeapParam != mir.InvalidValue {
			callArgs = append(callArgs, outHeapParam)
		}
	}
	if needsByRefType(retType) {
		for _, param := range fn.Params {
			if param.Name == "__ret" {
				callArgs = append(callArgs, param.ID)
				break
			}
		}
	}
	for _, param := range origParams {
		callArgs = append(callArgs, param.ID)
	}

	callResult := mir.InvalidValue
	callType := retType
	if _, ok := types.UnwrapType(retType).(*types.ReferenceType); ok {
		callResult = g.nextValueID()
	} else if needsByRefType(retType) || retType == nil || retType.Equals(types.TypeVoid) {
		callType = types.TypeVoid
	} else {
		callResult = g.nextValueID()
	}
	entry.Instrs = append(entry.Instrs, &mir.Call{
		Result:   callResult,
		Target:   name,
		Args:     callArgs,
		Type:     callType,
		Location: loc,
	})
	if callResult != mir.InvalidValue {
		entry.Term = &mir.Return{HasValue: true, Value: callResult, Location: loc}
	} else {
		entry.Term = &mir.Return{HasValue: false, Location: loc}
	}

	fn.Blocks = []*mir.Block{entry}
	g.extraFns = append(g.extraFns, fn)

	return wrapper
}

func (g *Generator) applyLargeReturnABI(fn *mir.Function, retType types.SemType, loc source.Location) {
	if fn == nil || !needsByRefType(retType) {
		return
	}
	outParam := g.newParam("__ret", types.NewReference(retType), loc)
	insertAt := 0
	if len(fn.Params) > 0 && fn.Params[0].IsEnv {
		insertAt = 1
	}
	params := make([]mir.Param, 0, len(fn.Params)+1)
	params = append(params, fn.Params[:insertAt]...)
	params = append(params, outParam)
	params = append(params, fn.Params[insertAt:]...)
	fn.Params = params
	fn.Return = types.TypeVoid
}

func (g *Generator) applyRefReturnABI(fn *mir.Function, retType types.SemType, loc source.Location, skip bool) {
	if fn == nil {
		return
	}
	if skip {
		return
	}
	ref, ok := types.UnwrapType(retType).(*types.ReferenceType)
	if !ok || ref == nil {
		return
	}
	outParam := g.newParam("__out", types.NewReference(ref.Inner), loc)
	outHeapParam := g.newParam(outHeapParamName, types.NewReference(types.TypeU64), loc)
	insertAt := 0
	if len(fn.Params) > 0 && fn.Params[0].IsEnv {
		insertAt = 1
	}
	params := make([]mir.Param, 0, len(fn.Params)+1)
	params = append(params, fn.Params[:insertAt]...)
	params = append(params, outParam, outHeapParam)
	params = append(params, fn.Params[insertAt:]...)
	fn.Params = params
}

func (g *Generator) newParam(name string, typ types.SemType, loc source.Location) mir.Param {
	id := g.nextValueID()
	return mir.Param{
		ID:       id,
		Name:     name,
		Type:     typ,
		IsEnv:    false,
		Location: loc,
	}
}

func (g *Generator) nextValueID() mir.ValueID {
	g.nextValue++
	return g.nextValue
}

func (g *Generator) nextBlockID() mir.BlockID {
	g.nextBlock++
	return g.nextBlock
}
