package hir

import (
	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/source"
	"compiler/internal/frontend/ast"
	"compiler/internal/tokens"
	"fmt"
)

type MethodLookup func(receiver typeinfo.Type, methodName string) (path []string, receiverType typeinfo.Type, ok bool)

type generator struct {
	key          string
	importPath   string
	filePath     string
	types        *typeinfo.ModuleInfo
	bindings     *binding.ModuleInfo
	lookupMethod MethodLookup

	currentFn     *ast.FuncDecl
	currentResult typeinfo.Type
	inFunction    bool
	nextLambda    int
	lambdaNames   map[ast.Expr]string
	localNames    map[symbols.SymbolID]string
	localIDs      map[symbols.SymbolID]int
	usedNames     map[string]struct{}
	synthFuncs    []*Func
	synthClosures []*Closure
}

func Generate(key, importPath, filePath string, astMod *ast.Module, types *typeinfo.ModuleInfo, bindings *binding.ModuleInfo, lookupMethod MethodLookup) *Module {
	if astMod == nil || types == nil {
		return nil
	}
	g := &generator{
		key:          key,
		importPath:   importPath,
		filePath:     filePath,
		types:        types,
		bindings:     bindings,
		lookupMethod: lookupMethod,
		lambdaNames:  make(map[ast.Expr]string),
		localNames:   make(map[symbols.SymbolID]string),
	}
	out := &Module{
		Key:        key,
		ImportPath: importPath,
		FilePath:   filePath,
		Source:     astMod,
		Types:      make([]*TypeDecl, 0),
		Globals:    make([]*Global, 0),
		Closures:   make([]*Closure, 0),
		Functions:  make([]*Func, 0),
	}
	for _, decl := range astMod.Decls {
		switch d := decl.(type) {
		case *ast.TypeDecl:
			out.Types = append(out.Types, g.generateTypeDecl(d))
		case *ast.LetDecl:
			out.Globals = append(out.Globals, g.generateLetDecl(d))
		case *ast.ConstDecl:
			out.Globals = append(out.Globals, g.generateConstDecl(d))
		case *ast.FuncDecl:
			out.Functions = append(out.Functions, g.generateFunc(d))
		}
	}
	out.Closures = append(out.Closures, g.synthClosures...)
	out.Functions = append(out.Functions, g.synthFuncs...)
	return out
}

func (g *generator) isFunctionLocal(sym *symbols.Symbol) bool {
	if g == nil || !g.inFunction || sym == nil {
		return false
	}
	switch sym.Kind {
	case symbols.SymbolParam:
		return true
	case symbols.SymbolVar, symbols.SymbolConst:
		switch sym.Node.(type) {
		case *ast.LetStmt, *ast.ConstStmt, *ast.LockStmt:
			return true
		case *ast.LetDecl, *ast.ConstDecl:
			return false
		default:
			// For/ catch binders don't currently have a dedicated AST node.
			return sym.Node == nil
		}
	default:
		return false
	}
}

func (g *generator) beginFunction(fn *ast.FuncDecl) {
	g.currentFn = fn
	g.currentResult = nil
	g.inFunction = true
	g.usedNames = make(map[string]struct{})
	g.localNames = make(map[symbols.SymbolID]string)
	g.localIDs = make(map[symbols.SymbolID]int)

	if fn == nil || g.bindings == nil {
		return
	}
	for _, sym := range g.bindings.FunctionLocals[fn] {
		if sym == nil {
			continue
		}
		// Local consts are compile-time only and should not allocate runtime
		// storage slots in MIR; keep them name-based (mangled) instead.
		if sym.Kind == symbols.SymbolConst {
			continue
		}
		if _, ok := g.localIDs[sym.ID]; ok {
			continue
		}
		g.localIDs[sym.ID] = len(g.localIDs)
	}
}

func (g *generator) beginSyntheticFunction(result typeinfo.Type) {
	g.beginFunction(nil)
	g.currentResult = result
}

func (g *generator) nextLambdaName() string {
	g.nextLambda++
	return fmt.Sprintf("__lambda%d", g.nextLambda)
}

func (g *generator) lambdaName(expr ast.Expr) (string, bool) {
	if g == nil || expr == nil {
		return "", false
	}
	name, ok := g.lambdaNames[expr]
	return name, ok && name != ""
}

func (g *generator) ensureLocalID(sym *symbols.Symbol) (int, bool) {
	if g == nil || sym == nil || !g.isFunctionLocal(sym) || sym.Kind == symbols.SymbolConst {
		return -1, false
	}
	if id, ok := g.localIDs[sym.ID]; ok {
		return id, true
	}
	id := len(g.localIDs)
	g.localIDs[sym.ID] = id
	return id, true
}

func (g *generator) mangleLocal(sym *symbols.Symbol) string {
	if g == nil || sym == nil {
		return ""
	}
	if cached, ok := g.localNames[sym.ID]; ok && cached != "" {
		return cached
	}

	// Keep user-facing names stable when possible. Only introduce a suffix when
	// there is a collision within the same function (shadowing).
	out := sym.Name
	if g.usedNames == nil {
		g.usedNames = make(map[string]struct{})
	}
	if _, exists := g.usedNames[out]; !exists {
		g.usedNames[out] = struct{}{}
		g.localNames[sym.ID] = out
		return out
	}

	if sym.Location.Start != nil {
		out = fmt.Sprintf("%s#%d:%d", sym.Name, sym.Location.Start.Line, sym.Location.Start.Column)
	} else {
		out = fmt.Sprintf("%s#shadow", sym.Name)
	}
	// Be defensive: ensure uniqueness even if two binders share a location.
	for {
		if _, exists := g.usedNames[out]; !exists {
			break
		}
		out += "_"
	}
	g.usedNames[out] = struct{}{}
	g.localNames[sym.ID] = out
	return out
}

func (g *generator) localSymbol(node ast.Node) (*symbols.Symbol, bool) {
	if g == nil || g.bindings == nil || node == nil {
		return nil, false
	}
	res := g.bindings.Nodes[node]
	if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
		return nil, false
	}
	return res.Symbol, true
}

func (g *generator) localIDFromIdent(ident *ast.Ident) (int, bool) {
	if g == nil || ident == nil {
		return -1, false
	}
	if !g.inFunction {
		return -1, false
	}
	sym, ok := g.localSymbol(ident)
	if !ok || !g.isFunctionLocal(sym) {
		return -1, false
	}
	return g.ensureLocalID(sym)
}

func (g *generator) maybeMangledLocalName(id *ast.Ident) string {
	if id == nil {
		return ""
	}
	if !g.inFunction {
		return id.Text()
	}
	sym, ok := g.localSymbol(id)
	if !ok || !g.isFunctionLocal(sym) {
		return id.Text()
	}
	return g.mangleLocal(sym)
}

func (g *generator) maybeLocalID(id *ast.Ident) int {
	if localID, ok := g.localIDFromIdent(id); ok {
		return localID
	}
	return -1
}

func (g *generator) generateTypeDecl(d *ast.TypeDecl) *TypeDecl {
	if d == nil {
		return nil
	}
	out := &TypeDecl{
		Name:       d.Name.Text(),
		Named:      &typeinfo.NamedType{ModuleKey: g.key, Name: d.Name.Text(), Decl: d},
		Underlying: typeFromTypeExpr(g.types, d.Type),
		Location:   d.Location,
		Source:     d,
	}
	switch t := d.Type.(type) {
	case *ast.StructType:
		out.Struct = g.generateStructTypeDecl(t)
	case *ast.InterfaceType:
		out.Interface = generateInterfaceTypeDecl(g.types, t)
	case *ast.EnumType:
		out.Enum = generateEnumTypeDecl(t)
	case *ast.UnionType:
		out.Union = generateUnionTypeDecl(g.types, t)
	case *ast.ErrorType:
		out.Error = generateErrorTypeDecl(t)
	}
	return out
}

func (g *generator) generateStructTypeDecl(t *ast.StructType) *StructTypeDecl {
	if t == nil {
		return nil
	}
	out := &StructTypeDecl{
		Fields: make([]*StructFieldDecl, 0, len(t.Fields)),
	}
	for _, field := range t.Fields {
		if field == nil {
			continue
		}
		out.Fields = append(out.Fields, &StructFieldDecl{
			Name:     field.Name.Text(),
			Type:     typeFromTypeExpr(g.types, field.Type),
			Default:  g.generateExpr(field.Default),
			Location: field.Location,
		})
	}
	return out
}

func generateInterfaceTypeDecl(types *typeinfo.ModuleInfo, t *ast.InterfaceType) *InterfaceTypeDecl {
	if t == nil {
		return nil
	}
	out := &InterfaceTypeDecl{Methods: make([]*InterfaceMethodDecl, 0, len(t.Methods))}
	for _, method := range t.Methods {
		if method == nil {
			continue
		}
		entry := &InterfaceMethodDecl{
			Receiver: method.Receiver,
			Static:   method.Static,
			Name:     method.Name.Text(),
			Result:   typeFromTypeExpr(types, method.Result),
			Location: method.Location,
			Params:   make([]*Param, 0, len(method.Params)),
		}
		for _, param := range method.Params {
			entry.Params = append(entry.Params, &Param{
				Name:      param.Name.Text(),
				Type:      typeFromTypeExpr(types, param.Type),
				IsMutable: param.IsMut,
				Location:  param.Location,
			})
		}
		out.Methods = append(out.Methods, entry)
	}
	return out
}

func generateEnumTypeDecl(t *ast.EnumType) *EnumTypeDecl {
	if t == nil {
		return nil
	}
	out := &EnumTypeDecl{Variants: make([]string, 0, len(t.Variants))}
	for _, variant := range t.Variants {
		if variant != nil {
			out.Variants = append(out.Variants, variant.Name.Text())
		}
	}
	return out
}

func generateUnionTypeDecl(types *typeinfo.ModuleInfo, t *ast.UnionType) *UnionTypeDecl {
	if t == nil {
		return nil
	}
	out := &UnionTypeDecl{Members: make([]typeinfo.Type, 0, len(t.Members))}
	for _, member := range t.Members {
		out.Members = append(out.Members, typeFromTypeExpr(types, member))
	}
	return out
}

func generateErrorTypeDecl(t *ast.ErrorType) *ErrorTypeDecl {
	if t == nil {
		return nil
	}
	out := &ErrorTypeDecl{Members: make([]string, 0, len(t.Members))}
	for _, member := range t.Members {
		if member != nil {
			out.Members = append(out.Members, member.Name.Text())
		}
	}
	return out
}

func (g *generator) generateLetDecl(d *ast.LetDecl) *Global {
	if d == nil {
		return nil
	}
	return &Global{
		Name:     d.Name.Text(),
		Mutable:  d.IsMut,
		Constant: false,
		Type:     effectiveType(g.types, d.Type, d.Value),
		Value:    g.generateExpr(d.Value),
		Location: d.Location,
		Source:   d,
	}
}

func (g *generator) generateConstDecl(d *ast.ConstDecl) *Global {
	if d == nil {
		return nil
	}
	return &Global{
		Name:     d.Name.Text(),
		Mutable:  false,
		Constant: true,
		Type:     effectiveType(g.types, d.Type, d.Value),
		Value:    g.generateConstValue(d.Value),
		Location: d.Location,
		Source:   d,
	}
}

func (g *generator) generateFunc(d *ast.FuncDecl) *Func {
	if d == nil {
		return nil
	}
	var selfType typeinfo.Type
	if d.OwnerType != nil {
		selfType = typeFromTypeExpr(g.types, d.OwnerType)
	}
	resultType := hirInstantiateSelfType(typeFromTypeExpr(g.types, d.Result), selfType)
	if resultType == nil {
		resultType = &typeinfo.BuiltinType{Name: "void"}
	}

	prevFn, prevResult, prevActive, prevUsed, prevNames, prevIDs := g.currentFn, g.currentResult, g.inFunction, g.usedNames, g.localNames, g.localIDs
	g.beginFunction(d)
	g.currentResult = resultType
	defer func() {
		g.currentFn, g.currentResult, g.inFunction, g.usedNames, g.localNames, g.localIDs = prevFn, prevResult, prevActive, prevUsed, prevNames, prevIDs
	}()

	fn := &Func{
		Name:       d.Name.Text(),
		IsStatic:   d.IsStatic,
		IsUnsafe:   d.IsUnsafe,
		IsExtern:   d.IsExtern,
		ExternName: d.ExternName,
		Result:     resultType,
		Body:       g.generateBlock(d.Body),
		LocalCount: len(g.localIDs),
		Location:   d.Location,
		Source:     d,
	}
	if d.OwnerType != nil && len(d.OwnerType.Path) > 0 {
		fn.OwnerType = d.OwnerType.Path[len(d.OwnerType.Path)-1]
	}
	if d.Receiver != nil {
		fn.Receiver = &Param{
			Name:     g.maybeMangledLocalName(d.Receiver.Name),
			LocalID:  g.maybeLocalID(d.Receiver.Name),
			Type:     hirInstantiateSelfType(typeFromTypeExpr(g.types, d.Receiver.Type), selfType),
			Location: d.Receiver.Location,
		}
	}
	fn.Params = make([]*Param, 0, len(d.Params))
	for _, param := range d.Params {
		fn.Params = append(fn.Params, &Param{
			Name:      g.maybeMangledLocalName(param.Name),
			LocalID:   g.maybeLocalID(param.Name),
			Type:      hirInstantiateSelfType(effectiveType(g.types, param.Type, param.Default), selfType),
			IsMutable: param.IsMut,
			Location:  param.Location,
		})
	}
	return fn
}

func (g *generator) generateLambdaFunc(expr *ast.LambdaExpr, fnType *typeinfo.FuncType) *Func {
	if expr == nil || fnType == nil {
		return nil
	}
	prevFn, prevResult, prevActive, prevUsed, prevNames, prevIDs := g.currentFn, g.currentResult, g.inFunction, g.usedNames, g.localNames, g.localIDs
	g.beginSyntheticFunction(fnType.Result)
	defer func() {
		g.currentFn, g.currentResult, g.inFunction, g.usedNames, g.localNames, g.localIDs = prevFn, prevResult, prevActive, prevUsed, prevNames, prevIDs
	}()

	fn := &Func{
		Name:       g.nextLambdaName(),
		Result:     fnType.Result,
		Body:       g.generateLambdaBody(expr, fnType.Result),
		LocalCount: len(g.localIDs),
		Location:   expr.Location,
	}
	g.lambdaNames[expr] = fn.Name
	captures := g.lambdaCaptureSymbols(expr)
	if len(captures) > 0 {
		closure := &Closure{
			Name:       fn.Name + "__closure",
			FuncName:   fn.Name,
			Captures:   make([]*Param, 0, len(captures)),
			Location:   expr.Location,
			LambdaExpr: expr,
		}
		for _, captured := range captures {
			if captured == nil {
				continue
			}
			captureType := typeinfo.Type(typeinfo.UnknownType{})
			if g.types != nil {
				if typ := g.types.Symbols[captured.ID]; typ != nil {
					captureType = typ
				}
			}
			localID := -1
			if id, ok := g.ensureLocalID(captured); ok {
				localID = id
			}
			closure.Captures = append(closure.Captures, &Param{
				Name:      g.mangleLocal(captured),
				LocalID:   localID,
				Type:      captureType,
				IsMutable: expr.IsMove && g.symbolMutableForLambdaCapture(captured),
				Location:  captured.Location,
			})
		}
		g.synthClosures = append(g.synthClosures, closure)
	}
	fn.Params = make([]*Param, 0, len(captures)+len(expr.Params))
	for _, captured := range captures {
		captureType := typeinfo.Type(typeinfo.UnknownType{})
		if g.types != nil && captured != nil {
			if typ := g.types.Symbols[captured.ID]; typ != nil {
				captureType = typ
			}
		}
		name := ""
		localID := -1
		if captured != nil {
			name = g.mangleLocal(captured)
			if id, ok := g.ensureLocalID(captured); ok {
				localID = id
			}
		}
		fn.Params = append(fn.Params, &Param{
			Name:      name,
			LocalID:   localID,
			Type:      captureType,
			IsMutable: expr.IsMove && captured != nil && g.symbolMutableForLambdaCapture(captured),
			Location:  expr.Location,
		})
	}
	for i, param := range expr.Params {
		paramType := typeinfo.Type(typeinfo.UnknownType{})
		if i < len(fnType.Params) && fnType.Params[i].Type != nil {
			paramType = fnType.Params[i].Type
		}
		fn.Params = append(fn.Params, &Param{
			Name:      g.maybeMangledLocalName(param.Name),
			LocalID:   g.maybeLocalID(param.Name),
			Type:      paramType,
			IsMutable: param.IsMut,
			Location:  param.Location,
		})
	}
	fn.LocalCount = len(g.localIDs)
	return fn
}

func (g *generator) functionAlias(sym *symbols.Symbol) (string, *ast.LambdaExpr, bool) {
	if g == nil || sym == nil {
		return "", nil, false
	}
	switch node := sym.Node.(type) {
	case *ast.LetStmt:
		if node == nil || node.IsMut {
			return "", nil, false
		}
		lambda, ok := node.Value.(*ast.LambdaExpr)
		if !ok || lambda == nil {
			return "", nil, false
		}
		name, ok := g.lambdaName(lambda)
		return name, lambda, ok
	case *ast.ConstStmt:
		lambda, ok := node.Value.(*ast.LambdaExpr)
		if !ok || lambda == nil {
			return "", nil, false
		}
		name, ok := g.lambdaName(lambda)
		return name, lambda, ok
	case *ast.LetDecl:
		if node == nil || node.IsMut {
			return "", nil, false
		}
		lambda, ok := node.Value.(*ast.LambdaExpr)
		if !ok || lambda == nil {
			return "", nil, false
		}
		name, ok := g.lambdaName(lambda)
		return name, lambda, ok
	case *ast.ConstDecl:
		lambda, ok := node.Value.(*ast.LambdaExpr)
		if !ok || lambda == nil {
			return "", nil, false
		}
		name, ok := g.lambdaName(lambda)
		return name, lambda, ok
	default:
		return "", nil, false
	}
}

func (g *generator) symbolMutableForLambdaCapture(sym *symbols.Symbol) bool {
	if sym == nil {
		return false
	}
	if sym.Kind == symbols.SymbolConst {
		return false
	}
	switch node := sym.Node.(type) {
	case *ast.LetDecl:
		return node != nil && node.IsMut
	case *ast.LetStmt:
		return node != nil && node.IsMut
	case *ast.ConstDecl, *ast.ConstStmt:
		return false
	default:
		return sym.Flags.Mutable()
	}
}

func (g *generator) lambdaCaptureSymbols(expr *ast.LambdaExpr) []*symbols.Symbol {
	if g == nil || g.types == nil || expr == nil {
		return nil
	}
	captures, ok := g.types.LookupLambdaCaptures(expr)
	if !ok || len(captures) == 0 {
		return nil
	}
	return captures
}

func (g *generator) capturedArgsForLambda(expr *ast.LambdaExpr) []Expr {
	captures := g.lambdaCaptureSymbols(expr)
	if len(captures) == 0 {
		return nil
	}
	args := make([]Expr, 0, len(captures))
	for _, sym := range captures {
		if sym == nil {
			continue
		}
		path := []string{sym.Name}
		localID := -1
		if g.inFunction && g.isFunctionLocal(sym) {
			path[0] = g.mangleLocal(sym)
			if id, ok := g.ensureLocalID(sym); ok {
				localID = id
			}
		}
		typ := typeinfo.Type(typeinfo.UnknownType{})
		if g.types != nil {
			if symType := g.types.Symbols[sym.ID]; symType != nil {
				typ = symType
			}
		}
		arg := &Ident{Path: path, LocalID: localID}
		arg.ExprType, arg.Location, arg.Source = typ, sym.Location, nil
		args = append(args, arg)
	}
	return args
}

func (g *generator) closureLitForLambda(expr *ast.LambdaExpr, typ typeinfo.Type, loc source.Location) Expr {
	if expr == nil {
		return nil
	}
	name, ok := g.lambdaName(expr)
	if !ok || name == "" {
		return nil
	}
	out := &ClosureLit{
		Name:     name + "__closure",
		FuncName: name,
		Captures: g.capturedArgsForLambda(expr),
	}
	out.ExprType, out.Location, out.Source = typ, loc, nil
	return out
}

func (g *generator) generateLambdaBody(expr *ast.LambdaExpr, result typeinfo.Type) *BlockStmt {
	if expr == nil {
		return nil
	}
	if expr.BodyExpr != nil {
		out := &BlockStmt{Stmts: make([]Stmt, 0, 1)}
		out.Location = expr.Location
		if typeinfo.IsBuiltinNamed(result, "void") {
			stmt := &ExprStmt{Value: g.generateExpr(expr.BodyExpr)}
			stmt.Location = expr.BodyExpr.Loc()
			out.Stmts = append(out.Stmts, stmt)
		} else {
			stmt := &ReturnStmt{Value: g.generateExprForTarget(expr.BodyExpr, result)}
			stmt.Location = expr.BodyExpr.Loc()
			out.Stmts = append(out.Stmts, stmt)
		}
		return out
	}
	if expr.BodyBlock == nil {
		return nil
	}
	if typeinfo.IsBuiltinNamed(result, "void") {
		return g.generateBlock(expr.BodyBlock)
	}
	out := &BlockStmt{Stmts: make([]Stmt, 0, len(expr.BodyBlock.Stmts))}
	out.Location = expr.BodyBlock.Location
	lastIndex := len(expr.BodyBlock.Stmts) - 1
	for i, stmt := range expr.BodyBlock.Stmts {
		if i == lastIndex {
			if exprStmt, ok := stmt.(*ast.ExprStmt); ok && exprStmt != nil {
				ret := &ReturnStmt{Value: g.generateExprForTarget(exprStmt.Value, result)}
				ret.Location = exprStmt.Location
				out.Stmts = append(out.Stmts, ret)
				continue
			}
		}
		if lowered := g.generateStmt(stmt); lowered != nil {
			out.Stmts = append(out.Stmts, lowered)
		}
	}
	return out
}

func hirInstantiateSelfType(typ, selfType typeinfo.Type) typeinfo.Type {
	if selfType == nil {
		return typ
	}
	return typeinfo.RewriteType(typ, func(t typeinfo.Type) typeinfo.Type {
		if _, ok := t.(*typeinfo.SelfType); ok {
			return selfType
		}
		return nil
	}, nil)
}

func (g *generator) generateBlock(block *ast.BlockStmt) *BlockStmt {
	if block == nil {
		return nil
	}
	out := &BlockStmt{Stmts: make([]Stmt, 0, len(block.Stmts))}
	out.Location = block.Location
	for _, stmt := range block.Stmts {
		if lowered := g.generateStmt(stmt); lowered != nil {
			out.Stmts = append(out.Stmts, lowered)
		}
	}
	return out
}

func (g *generator) generateStmt(stmt ast.Stmt) Stmt {
	switch s := stmt.(type) {
	case nil:
		return nil
	case *ast.BlockStmt:
		if s.Comptime {
			return nil
		}
		return g.generateBlock(s)
	case *ast.LetStmt:
		targetType := effectiveType(g.types, s.Type, s.Value)
		if _, ok := targetType.(*typeinfo.FuncType); ok && !s.IsMut {
			if _, ok := s.Value.(*ast.LambdaExpr); ok {
				_ = g.generateExpr(s.Value)
				return nil
			}
		}
		out := &LetStmt{Name: g.maybeMangledLocalName(s.Name), LocalID: g.maybeLocalID(s.Name), Mutable: s.IsMut, Type: targetType, Value: g.generateExprForTarget(s.Value, targetType)}
		out.Location = s.Location
		return out
	case *ast.ConstStmt:
		if _, ok := effectiveType(g.types, s.Type, s.Value).(*typeinfo.FuncType); ok {
			if _, ok := s.Value.(*ast.LambdaExpr); ok {
				_ = g.generateExpr(s.Value)
				return nil
			}
		}
		out := &ConstStmt{Name: g.maybeMangledLocalName(s.Name), LocalID: g.maybeLocalID(s.Name), Type: effectiveType(g.types, s.Type, s.Value), Value: g.generateConstValue(s.Value)}
		out.Location = s.Location
		return out
	case *ast.ReturnStmt:
		value := g.generateExprForTarget(s.Value, g.currentResult)
		if comp, ok := value.(*CompositeLit); ok && (typeinfo.IsInvalid(comp.ExprType) || isUnknownType(comp.ExprType)) {
			if g.currentResult != nil {
				comp.ExprType = g.currentResult
			}
		}
		out := &ReturnStmt{Value: value}
		out.Location = s.Location
		return out
	case *ast.ExprStmt:
		out := &ExprStmt{Value: g.generateExpr(s.Value)}
		out.Location = s.Location
		return out
	case *ast.PanicStmt:
		out := &PanicStmt{Value: g.generateExpr(s.Value)}
		out.Location = s.Location
		return out
	case *ast.AssignStmt:
		left := g.generateExpr(s.Left)
		out := &AssignStmt{Left: left, Right: g.generateExprForTarget(s.Right, exprType(g.types, s.Left))}
		out.Location = s.Location
		return out
	case *ast.IfStmt:
		out := &IfStmt{Cond: g.generateExpr(s.Cond), Then: g.generateBlock(s.Then), Else: g.generateStmt(s.Else)}
		out.Location = s.Location
		return out
	case *ast.MatchStmt:
		out := &MatchStmt{Value: g.generateExpr(s.Value), Arms: make([]*MatchArm, 0, len(s.Arms))}
		out.Location = s.Location
		for _, arm := range s.Arms {
			if arm == nil {
				continue
			}
			hirArm := &MatchArm{Wildcard: arm.Wildcard, Body: g.generateBlock(arm.Body)}
			if arm.Pattern != nil {
				hirArm.Pattern = g.generateExpr(arm.Pattern)
			}
			if arm.TypePattern != nil {
				hirArm.TypePattern = g.resolveTypeExpr(arm.TypePattern)
			}
			out.Arms = append(out.Arms, hirArm)
		}
		return out
	case *ast.WhileStmt:
		out := &WhileStmt{Cond: g.generateExpr(s.Cond), Body: g.generateBlock(s.Body)}
		out.Location = s.Location
		return out
	case *ast.ForStmt:
		out := &ForStmt{Iterable: g.generateExpr(s.Iterable), Body: g.generateBlock(s.Body), IndexID: -1, ValueID: -1}
		if s.Index != nil {
			out.IndexName = g.maybeMangledLocalName(s.Index)
			out.IndexID = g.maybeLocalID(s.Index)
		}
		if s.Value != nil {
			out.ValueName = g.maybeMangledLocalName(s.Value)
			out.ValueID = g.maybeLocalID(s.Value)
		}
		out.Location = s.Location
		return out
	case *ast.LabelStmt:
		out := &LabelStmt{Name: s.Name.Text(), Stmt: g.generateStmt(s.Stmt)}
		out.Location = s.Location
		return out
	case *ast.BreakStmt:
		out := &BreakStmt{}
		if s.Label != nil {
			out.Label = s.Label.Text()
		}
		out.Location = s.Location
		return out
	case *ast.ContinueStmt:
		out := &ContinueStmt{}
		if s.Label != nil {
			out.Label = s.Label.Text()
		}
		out.Location = s.Location
		return out
	case *ast.DeferStmt:
		out := &DeferStmt{Body: g.generateStmt(s.Body)}
		out.Location = s.Location
		return out
	case *ast.ReleaseStmt:
		out := &ReleaseStmt{Value: g.generateExpr(s.Value)}
		out.Location = s.Location
		return out
	case *ast.LockStmt:
		out := &LockStmt{Value: g.generateExpr(s.Value), Name: g.maybeMangledLocalName(s.Name), LocalID: g.maybeLocalID(s.Name), Body: g.generateBlock(s.Body)}
		out.Location = s.Location
		return out
	case *ast.UnsafeStmt:
		out := &UnsafeStmt{Body: g.generateBlock(s.Body)}
		out.Location = s.Location
		return out
	default:
		return nil
	}
}

func (g *generator) cachedConstExpr(expr ast.Expr) (Expr, bool) {
	if expr == nil {
		return nil, false
	}
	value, ok := g.types.LookupConstValue(expr)
	if !ok {
		return nil, false
	}
	return g.constExprFromValue(expr, value, exprType(g.types, expr), expr.Loc())
}

func (g *generator) constExprFromValue(expr ast.Expr, value typeinfo.ConstValue, typ typeinfo.Type, loc source.Location) (Expr, bool) {
	switch value.Kind {
	case typeinfo.ConstInt:
		if value.Int == nil {
			return nil, false
		}
		out := &NumberLit{Value: value.Int.String()}
		out.ExprType, out.Location, out.Source = typ, loc, expr
		return out, true
	case typeinfo.ConstBool:
		name := "false"
		if value.Bool {
			name = "true"
		}
		out := &Ident{Path: []string{name}, LocalID: -1}
		out.ExprType, out.Location, out.Source = typ, loc, expr
		return out, true
	case typeinfo.ConstString:
		out := &StringLit{Value: value.String}
		out.ExprType, out.Location, out.Source = typ, loc, expr
		return out, true
	case typeinfo.ConstNone:
		out := &NoneLit{}
		out.ExprType, out.Location, out.Source = typ, loc, expr
		return out, true
	case typeinfo.ConstSequence:
		out := &CompositeLit{Items: make([]CompositeItem, 0, len(value.Elems))}
		out.ExprType, out.Location, out.Source = typ, loc, expr
		for i, elem := range value.Elems {
			elemType := g.constSequenceElemType(typ, i)
			child, ok := g.constExprFromValue(nil, elem, elemType, loc)
			if !ok {
				return nil, false
			}
			out.Items = append(out.Items, CompositeItem{Value: child})
		}
		return out, true
	case typeinfo.ConstObject:
		out := &CompositeLit{Items: make([]CompositeItem, 0, len(value.Fields))}
		out.ExprType, out.Location, out.Source = typ, loc, expr
		for i, field := range value.Fields {
			fieldType := g.constObjectFieldType(typ, i)
			child, ok := g.constExprFromValue(nil, field, fieldType, loc)
			if !ok {
				return nil, false
			}
			name := ""
			if i < len(value.FieldNames) {
				name = value.FieldNames[i]
			}
			out.Items = append(out.Items, CompositeItem{Name: name, Value: child})
		}
		return out, true
	default:
		return nil, false
	}
}

func (g *generator) constMaterializeType(typ typeinfo.Type) typeinfo.Type {
	switch t := typ.(type) {
	case *typeinfo.NamedType:
		if t != nil && t.Decl != nil {
			if resolved := typeFromTypeExpr(g.types, t.Decl.Type); resolved != nil {
				return g.constMaterializeType(resolved)
			}
		}
	case *typeinfo.RefType:
		if t != nil && t.Inner != nil {
			return g.constMaterializeType(t.Inner)
		}
	case *typeinfo.PointerType:
		if t != nil && t.Inner != nil {
			return g.constMaterializeType(t.Inner)
		}
	case *typeinfo.RawPtrType:
		if t != nil && t.Inner != nil {
			return g.constMaterializeType(t.Inner)
		}
	}
	return typ
}

func (g *generator) constSequenceElemType(typ typeinfo.Type, index int) typeinfo.Type {
	switch t := g.constMaterializeType(typ).(type) {
	case *typeinfo.ArrayType:
		if t != nil && t.Inner != nil {
			return t.Inner
		}
	case *typeinfo.SliceType:
		if t != nil && t.Inner != nil {
			return t.Inner
		}
	case *typeinfo.TupleType:
		if t != nil && index >= 0 && index < len(t.Elems) && t.Elems[index] != nil {
			return t.Elems[index]
		}
	}
	return typeinfo.UnknownType{}
}

func (g *generator) constObjectFieldType(typ typeinfo.Type, index int) typeinfo.Type {
	if t, ok := g.constMaterializeType(typ).(*typeinfo.StructType); ok && t != nil && index >= 0 && index < len(t.OrderedFields) {
		if field := t.OrderedFields[index]; field != nil && field.Type != nil {
			return field.Type
		}
	}
	return typeinfo.UnknownType{}
}

func (g *generator) generateConstValue(expr ast.Expr) Expr {
	if out, ok := g.cachedConstExpr(expr); ok {
		return out
	}
	value := g.generateExpr(expr)
	if expr == nil || value == nil {
		return value
	}
	out := &PrefixExpr{Op: "comptime", Right: value}
	out.ExprType, out.Location, out.Source = exprType(g.types, expr), expr.Loc(), expr
	return out
}

func (g *generator) generateExpr(expr ast.Expr) Expr {
	typ := exprType(g.types, expr)
	switch e := expr.(type) {
	case nil:
		return nil
	case *ast.BadExpr:
		out := &BadExpr{}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.Ident:
		if sym, ok := g.localSymbol(e); ok {
			if _, lambda, ok := g.functionAlias(sym); ok && lambda != nil {
				if out := g.closureLitForLambda(lambda, typ, e.Location); out != nil {
					return out
				}
			}
		}
		path := append([]string{}, e.Path...)
		localID := -1
		if len(path) == 1 && g.inFunction {
			if sym, ok := g.localSymbol(e); ok && g.isFunctionLocal(sym) {
				path[0] = g.mangleLocal(sym)
				if id, ok := g.ensureLocalID(sym); ok {
					localID = id
				}
			}
		}
		out := &Ident{Path: path, LocalID: localID}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.NumberLit:
		out := &NumberLit{Value: e.Value}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.StringLit:
		out := &StringLit{Value: e.Value}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.CharLit:
		runes := []rune(e.Value)
		value := "0"
		if len(runes) == 1 {
			value = fmt.Sprintf("%d", runes[0])
		}
		out := &NumberLit{Value: value}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.NoneLit:
		out := &NoneLit{}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.PrefixExpr:
		if e.Op == "comptime" {
			if out, ok := g.cachedConstExpr(e); ok {
				return out
			}
		}
		out := &PrefixExpr{Op: e.Op, Right: g.generateExpr(e.Right)}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.SpreadExpr:
		spreadType := exprType(g.types, e.Right)
		out := &PrefixExpr{Op: "...", Right: g.generateExpr(e.Right)}
		out.ExprType, out.Location, out.Source = spreadType, e.Location, e
		return out
	case *ast.BinaryExpr:
		out := &BinaryExpr{Left: g.generateExpr(e.Left), Op: e.Op, Right: g.generateExpr(e.Right)}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.RangeExpr:
		out := &RangeExpr{
			Start:     g.generateExpr(e.Start),
			End:       g.generateExpr(e.End),
			Step:      g.generateExpr(e.Step),
			Inclusive: e.Inclusive,
		}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.PostfixExpr:
		out := &PostfixExpr{Left: g.generateExpr(e.Left), Op: e.Op}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.CallExpr:
		args := e.Args
		if expanded, ok := g.types.LookupCallArgs(e); ok {
			args = expanded
		}
		calleeType, _ := exprType(g.types, e.Callee).(*typeinfo.FuncType)
		out := &CallExpr{Callee: g.generateExpr(e.Callee), Args: make([]Expr, 0, len(args))}
		if recv, ok := g.types.LookupMethodReceiver(e); ok {
			out.MethodReceiver = recv
		}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		for i, arg := range args {
			target := typeinfo.Type(nil)
			if calleeType != nil && len(calleeType.Params) != 0 {
				paramIndex := i
				if paramIndex >= len(calleeType.Params) {
					paramIndex = len(calleeType.Params) - 1
				}
				if paramIndex >= 0 && paramIndex < len(calleeType.Params) {
					target = calleeType.Params[paramIndex].Type
				}
			}
			out.Args = append(out.Args, g.generateExprForTarget(arg, target))
		}
		return out
	case *ast.SelectorExpr:
		out := &SelectorExpr{Left: g.generateExpr(e.Left), Name: e.Name.Text()}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.CastExpr:
		out := &CastExpr{Left: g.generateExpr(e.Left)}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.IsExpr:
		out := &IsExpr{
			Left:   g.generateExpr(e.Left),
			Target: g.resolveTypeExpr(e.Type),
		}
		if value, ok := g.types.LookupBool(e); ok {
			out.StaticKnown = true
			out.StaticValue = value
		}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.MatchExpr:
		out := &MatchExpr{Value: g.generateExpr(e.Value), Arms: make([]*MatchArm, 0, len(e.Arms))}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		for _, arm := range e.Arms {
			if arm == nil {
				continue
			}
			hirArm := &MatchArm{Wildcard: arm.Wildcard, Body: g.generateBlock(arm.Body)}
			if arm.Pattern != nil {
				hirArm.Pattern = g.generateExpr(arm.Pattern)
			}
			if arm.TypePattern != nil {
				hirArm.TypePattern = g.resolveTypeExpr(arm.TypePattern)
			}
			out.Arms = append(out.Arms, hirArm)
		}
		return out
	case *ast.CatchExpr:
		out := &CatchExpr{
			Left:      g.generateExpr(e.Left),
			Fallback:  g.generateExpr(e.Fallback),
			Handler:   g.generateBlock(e.Handler),
			PayloadID: -1,
		}
		if e.Payload != nil {
			out.PayloadName = g.maybeMangledLocalName(e.Payload)
			out.PayloadID = g.maybeLocalID(e.Payload)
		}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.CompositeLit:
		if typeinfo.IsInvalid(typ) || isUnknownType(typ) {
			if e.Type != nil {
				typ = g.resolveTypeExpr(e.Type)
			}
		}
		out := &CompositeLit{Items: make([]CompositeItem, 0, len(e.Items))}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		if fields, ok := g.structLiteralFields(typ); ok {
			used := make(map[string]struct{}, len(fields))
			positional := 0
			for _, item := range e.Items {
				name := ast.ExprText(item.Name)
				if name == "" {
					for positional < len(fields) {
						candidate := fields[positional].Name
						positional++
						if _, exists := used[candidate]; exists {
							continue
						}
						name = candidate
						break
					}
				}
				if name != "" {
					used[name] = struct{}{}
				}
				fieldType := typeinfo.Type(nil)
				for _, field := range fields {
					if field.Name == name {
						fieldType = field.Type
						break
					}
				}
				out.Items = append(out.Items, CompositeItem{Name: name, Value: g.generateExprForTarget(item.Value, fieldType)})
			}
			for _, field := range fields {
				if field.Default == nil {
					continue
				}
				if _, exists := used[field.Name]; exists {
					continue
				}
				out.Items = append(out.Items, CompositeItem{Name: field.Name, Value: g.generateExprForTarget(field.Default, field.Type)})
			}
			return out
		}
		switch resolved := typ.(type) {
		case *typeinfo.ArrayType:
			for _, item := range e.Items {
				out.Items = append(out.Items, CompositeItem{Name: ast.ExprText(item.Name), Key: g.generateExpr(item.Key), Value: g.generateExprForTarget(item.Value, resolved.Inner)})
			}
		case *typeinfo.SliceType:
			for _, item := range e.Items {
				out.Items = append(out.Items, CompositeItem{Name: ast.ExprText(item.Name), Key: g.generateExpr(item.Key), Value: g.generateExprForTarget(item.Value, resolved.Inner)})
			}
		case *typeinfo.MapType:
			for _, item := range e.Items {
				out.Items = append(out.Items, CompositeItem{
					Name:  ast.ExprText(item.Name),
					Key:   g.generateExprForTarget(item.Key, resolved.Key),
					Value: g.generateExprForTarget(item.Value, resolved.Value),
				})
			}
		case *typeinfo.TupleType:
			for i, item := range e.Items {
				target := typeinfo.Type(nil)
				if i >= 0 && i < len(resolved.Elems) {
					target = resolved.Elems[i]
				}
				out.Items = append(out.Items, CompositeItem{Name: ast.ExprText(item.Name), Key: g.generateExpr(item.Key), Value: g.generateExprForTarget(item.Value, target)})
			}
		default:
			for _, item := range e.Items {
				out.Items = append(out.Items, CompositeItem{Name: ast.ExprText(item.Name), Key: g.generateExpr(item.Key), Value: g.generateExpr(item.Value)})
			}
		}
		return out
	case *ast.IndexExpr:
		out := &IndexExpr{Left: g.generateExpr(e.Left), Index: g.generateExpr(e.Index)}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.LambdaExpr:
		fnType, _ := typ.(*typeinfo.FuncType)
		if fnType == nil {
			return nil
		}
		fn := g.generateLambdaFunc(e, fnType)
		if fn == nil {
			return nil
		}
		g.synthFuncs = append(g.synthFuncs, fn)
		return g.closureLitForLambda(e, typ, e.Location)
	default:
		return nil
	}
}

func (g *generator) generateExprForTarget(expr ast.Expr, target typeinfo.Type) Expr {
	value := g.generateExpr(expr)
	if expr == nil || value == nil || target == nil {
		return value
	}
	errUnionTarget := target
	errUnion, ok := target.(*typeinfo.ErrorUnionType)
	if !ok || errUnion == nil {
		if named, ok := target.(*typeinfo.NamedType); ok && named != nil && named.Decl != nil {
			if resolved, ok := typeFromTypeExpr(g.types, named.Decl.Type).(*typeinfo.ErrorUnionType); ok && resolved != nil {
				errUnion = resolved
			}
		}
	}
	if errUnion == nil {
		return value
	}
	sourceType := exprType(g.types, expr)
	if sourceType == nil || typeinfo.Equal(errUnionTarget, sourceType) {
		return value
	}
	tag := "0"
	switch {
	case typeinfo.Assignable(errUnion.Error, sourceType):
		tag = "0"
	case typeinfo.Assignable(errUnion.Value, sourceType):
		tag = "1"
	default:
		return value
	}
	tagExpr := &NumberLit{Value: tag}
	tagExpr.ExprType, tagExpr.Location, tagExpr.Source = &typeinfo.BuiltinType{Name: "i32"}, expr.Loc(), expr
	out := &CompositeLit{Items: []CompositeItem{
		{Value: tagExpr},
		{Value: value},
	}}
	out.ExprType, out.Location, out.Source = errUnionTarget, expr.Loc(), expr
	return out
}

func isUnknownType(typ typeinfo.Type) bool {
	if typ == nil {
		return true
	}
	_, ok := typ.(typeinfo.UnknownType)
	return ok
}

func exprType(types *typeinfo.ModuleInfo, expr ast.Expr) typeinfo.Type {
	if types == nil || expr == nil {
		return typeinfo.UnknownType{}
	}
	if typ, ok := types.Nodes[expr]; ok && typ != nil {
		return typ
	}
	return typeinfo.UnknownType{}
}

func typeFromTypeExpr(types *typeinfo.ModuleInfo, expr ast.TypeExpr) typeinfo.Type {
	if types == nil || expr == nil {
		return nil
	}
	if typ, ok := types.Nodes[expr]; ok {
		return typ
	}
	return nil
}

func (g *generator) resolveTypeExpr(expr ast.TypeExpr) typeinfo.Type {
	if typ := typeFromTypeExpr(g.types, expr); typ != nil {
		return typ
	}
	switch t := expr.(type) {
	case nil:
		return nil
	case *ast.NamedType:
		if len(t.Path) == 1 && t.Path[0] == "str" {
			return &typeinfo.StringType{}
		}
		if len(t.Path) == 1 && tokens.IsBuiltinType(t.Path[0]) {
			return &typeinfo.BuiltinType{Name: t.Path[0]}
		}
		if g.bindings == nil {
			return nil
		}
		resolution := g.bindings.Nodes[t]
		if resolution == nil || resolution.Symbol == nil || resolution.Symbol.Kind != symbols.SymbolType {
			return nil
		}
		decl, _ := resolution.Symbol.Node.(*ast.TypeDecl)
		return &typeinfo.NamedType{
			ModuleKey: resolution.ModuleKey,
			Name:      resolution.Symbol.Name,
			Decl:      decl,
		}
	default:
		return typeFromTypeExpr(g.types, expr)
	}
}

func effectiveType(types *typeinfo.ModuleInfo, syntax ast.TypeExpr, value ast.Expr) typeinfo.Type {
	if typ := typeFromTypeExpr(types, syntax); typ != nil {
		return typ
	}
	if value != nil {
		return exprType(types, value)
	}
	return typeinfo.UnknownType{}
}

type structLiteralField struct {
	Name    string
	Default ast.Expr
	Type    typeinfo.Type
}

func (g *generator) structLiteralFields(typ typeinfo.Type) ([]structLiteralField, bool) {
	named, ok := typ.(*typeinfo.NamedType)
	if !ok || named == nil || named.Decl == nil {
		return nil, false
	}
	structDecl, ok := named.Decl.Type.(*ast.StructType)
	if !ok || structDecl == nil {
		return nil, false
	}
	structType, _ := typeFromTypeExpr(g.types, named.Decl.Type).(*typeinfo.StructType)
	fields := make([]structLiteralField, 0, len(structDecl.Fields))
	for i, field := range structDecl.Fields {
		if field == nil || field.Name == nil {
			continue
		}
		fieldType := typeinfo.Type(nil)
		if structType != nil && i >= 0 && i < len(structType.OrderedFields) && structType.OrderedFields[i] != nil {
			fieldType = structType.OrderedFields[i].Type
		}
		fields = append(fields, structLiteralField{Name: field.Name.Text(), Default: field.Default, Type: fieldType})
	}
	return fields, true
}
