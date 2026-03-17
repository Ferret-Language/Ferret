package hir

import (
	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/frontend/ast"
	"compiler/internal/tokens"
	"fmt"
)

type MethodLookup func(receiver typeinfo.Type, methodName string) ([]string, bool)

type generator struct {
	key          string
	importPath   string
	filePath     string
	types        *typeinfo.ModuleInfo
	bindings     *binding.ModuleInfo
	lookupMethod MethodLookup

	currentFn  *ast.FuncDecl
	localNames map[*symbols.Symbol]string
	localIDs   map[*symbols.Symbol]int
	usedNames  map[string]struct{}
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
		localNames:   make(map[*symbols.Symbol]string),
	}
	out := &Module{
		Key:        key,
		ImportPath: importPath,
		FilePath:   filePath,
		Source:     astMod,
		Types:      make([]*TypeDecl, 0),
		Globals:    make([]*Global, 0),
		Functions:  make([]*Func, 0),
	}
	for _, decl := range astMod.Decls {
		switch d := decl.(type) {
		case *ast.TypeDecl:
			out.Types = append(out.Types, g.generateTypeDecl(d))
			out.Globals = append(out.Globals, g.generateStaticFieldGlobals(d)...)
		case *ast.LetDecl:
			out.Globals = append(out.Globals, g.generateLetDecl(d))
		case *ast.ConstDecl:
			out.Globals = append(out.Globals, g.generateConstDecl(d))
		case *ast.FuncDecl:
			out.Functions = append(out.Functions, g.generateFunc(d))
		}
	}
	return out
}

func (g *generator) isFunctionLocal(sym *symbols.Symbol) bool {
	if g == nil || g.currentFn == nil || sym == nil {
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
	g.usedNames = make(map[string]struct{})
	g.localNames = make(map[*symbols.Symbol]string)
	g.localIDs = make(map[*symbols.Symbol]int)

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
		if _, ok := g.localIDs[sym]; ok {
			continue
		}
		g.localIDs[sym] = len(g.localIDs)
	}
}

func (g *generator) endFunction() {
	g.currentFn = nil
	g.usedNames = nil
	g.localNames = nil
	g.localIDs = nil
}

func (g *generator) mangleLocal(sym *symbols.Symbol) string {
	if g == nil || sym == nil {
		return ""
	}
	if cached, ok := g.localNames[sym]; ok && cached != "" {
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
		g.localNames[sym] = out
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
	g.localNames[sym] = out
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
	if g.currentFn == nil {
		return -1, false
	}
	sym, ok := g.localSymbol(ident)
	if !ok || !g.isFunctionLocal(sym) {
		return -1, false
	}
	id, ok := g.localIDs[sym]
	if !ok {
		return -1, false
	}
	return id, true
}

func (g *generator) maybeMangledLocalName(id *ast.Ident) string {
	if id == nil {
		return ""
	}
	if g.currentFn == nil {
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

func staticFieldGlobalName(typeName, fieldName string) string {
	if typeName == "" {
		return fieldName
	}
	if fieldName == "" {
		return typeName
	}
	return typeName + "__" + fieldName
}

func (g *generator) generateStaticFieldGlobals(d *ast.TypeDecl) []*Global {
	if d == nil {
		return nil
	}
	st, ok := d.Type.(*ast.StructType)
	if !ok || len(st.StaticFields) == 0 {
		return nil
	}
	out := make([]*Global, 0, len(st.StaticFields))
	for _, field := range st.StaticFields {
		if field == nil {
			continue
		}
		out = append(out, &Global{
			Name:     staticFieldGlobalName(d.Name.Text(), field.Name.Text()),
			Mutable:  false,
			Constant: true,
			Type:     syntaxType(g.types, field.Type),
			Value:    g.generateExpr(field.Default),
			Location: field.Location,
			Source:   nil,
		})
	}
	return out
}

func (g *generator) generateTypeDecl(d *ast.TypeDecl) *TypeDecl {
	if d == nil {
		return nil
	}
	out := &TypeDecl{
		Name:       d.Name.Text(),
		Named:      &typeinfo.NamedType{ModuleKey: g.key, Name: d.Name.Text(), Decl: d},
		Underlying: syntaxType(g.types, d.Type),
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
		Fields:       make([]*StructFieldDecl, 0, len(t.Fields)),
		StaticFields: make([]*StructFieldDecl, 0, len(t.StaticFields)),
	}
	for _, field := range t.Fields {
		if field == nil {
			continue
		}
		out.Fields = append(out.Fields, &StructFieldDecl{
			Name:     field.Name.Text(),
			Type:     syntaxType(g.types, field.Type),
			Default:  g.generateExpr(field.Default),
			Location: field.Location,
		})
	}
	for _, field := range t.StaticFields {
		if field == nil {
			continue
		}
		out.StaticFields = append(out.StaticFields, &StructFieldDecl{
			Name:     field.Name.Text(),
			Type:     syntaxType(g.types, field.Type),
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
			Name:     method.Name.Text(),
			Result:   syntaxType(types, method.Result),
			Location: method.Location,
			Params:   make([]*Param, 0, len(method.Params)),
		}
		for _, param := range method.Params {
			entry.Params = append(entry.Params, &Param{
				Name:       param.Name.Text(),
				Type:       syntaxType(types, param.Type),
				IsComptime: param.IsComptime,
				Location:   param.Location,
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
		out.Members = append(out.Members, syntaxType(types, member))
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
		Value:    g.generateExpr(d.Value),
		Location: d.Location,
		Source:   d,
	}
}

func (g *generator) generateFunc(d *ast.FuncDecl) *Func {
	if d == nil {
		return nil
	}
	prevFn, prevUsed, prevNames, prevIDs := g.currentFn, g.usedNames, g.localNames, g.localIDs
	g.beginFunction(d)
	defer func() {
		g.currentFn, g.usedNames, g.localNames, g.localIDs = prevFn, prevUsed, prevNames, prevIDs
	}()

	fn := &Func{
		Name:       d.Name.Text(),
		IsUnsafe:   d.IsUnsafe,
		IsBuiltin:  d.IsBuiltin,
		IsExtern:   d.IsExtern,
		ExternName: d.ExternName,
		Result:     syntaxType(g.types, d.Result),
		Body:       g.generateBlock(d.Body),
		LocalCount: len(g.localIDs),
		Location:   d.Location,
		Source:     d,
	}
	if d.IsDestructor {
		fn.Name = "~" + d.Name.Text()
	}
	if fn.Result == nil {
		fn.Result = &typeinfo.BuiltinType{Name: "void"}
	}
	if d.Receiver != nil {
		fn.Receiver = &Param{
			Name:     g.maybeMangledLocalName(d.Receiver.Name),
			LocalID:  g.maybeLocalID(d.Receiver.Name),
			Type:     syntaxType(g.types, d.Receiver.Type),
			Location: d.Receiver.Location,
		}
	}
	fn.Params = make([]*Param, 0, len(d.Params))
	for _, param := range d.Params {
		fn.Params = append(fn.Params, &Param{
			Name:       g.maybeMangledLocalName(param.Name),
			LocalID:    g.maybeLocalID(param.Name),
			Type:       syntaxType(g.types, param.Type),
			IsComptime: param.IsComptime,
			Location:   param.Location,
		})
	}
	return fn
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
		if deferred := g.generateAutoDestructorDefer(stmt); deferred != nil {
			out.Stmts = append(out.Stmts, deferred)
		}
	}
	return out
}

func (g *generator) generateStmt(stmt ast.Stmt) Stmt {
	switch s := stmt.(type) {
	case nil:
		return nil
	case *ast.BlockStmt:
		return g.generateBlock(s)
	case *ast.LetStmt:
		out := &LetStmt{Name: g.maybeMangledLocalName(s.Name), LocalID: g.maybeLocalID(s.Name), Mutable: s.IsMut, Type: effectiveType(g.types, s.Type, s.Value), Value: g.generateExpr(s.Value)}
		out.Location = s.Location
		return out
	case *ast.ConstStmt:
		out := &ConstStmt{Name: g.maybeMangledLocalName(s.Name), LocalID: g.maybeLocalID(s.Name), Type: effectiveType(g.types, s.Type, s.Value), Value: g.generateExpr(s.Value)}
		out.Location = s.Location
		return out
	case *ast.ReturnStmt:
		out := &ReturnStmt{Value: g.generateExpr(s.Value)}
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
		out := &AssignStmt{Left: g.generateExpr(s.Left), Right: g.generateExpr(s.Right)}
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

func (g *generator) generateAutoDestructorDefer(stmt ast.Stmt) Stmt {
	letStmt, ok := stmt.(*ast.LetStmt)
	if !ok || letStmt == nil || letStmt.Name == nil {
		return nil
	}
	methodName, ok := g.destructorMethodName(effectiveType(g.types, letStmt.Type, letStmt.Value))
	if !ok {
		return nil
	}
	ident := &Ident{Path: []string{g.maybeMangledLocalName(letStmt.Name)}}
	ident.LocalID = g.maybeLocalID(letStmt.Name)
	ident.ExprType = effectiveType(g.types, letStmt.Type, letStmt.Value)
	ident.Location = letStmt.Name.Location
	call := &CallExpr{
		Callee: &SelectorExpr{
			Left: ident,
			Name: methodName,
		},
		Args: nil,
	}
	call.ExprType = &typeinfo.BuiltinType{Name: "void"}
	call.Location = letStmt.Location
	call.Source = nil
	call.Callee.(*SelectorExpr).ExprType = &typeinfo.FuncType{
		Result: &typeinfo.BuiltinType{Name: "void"},
	}
	call.Callee.(*SelectorExpr).Location = letStmt.Location
	deferred := &DeferStmt{Body: &ExprStmt{Value: call}}
	deferred.Location = letStmt.Location
	return deferred
}

func (g *generator) destructorMethodName(typ typeinfo.Type) (string, bool) {
	ptr, ok := typ.(*typeinfo.PointerType)
	if !ok || ptr == nil || !ptr.IsOwn || ptr.IsRaw || g.lookupMethod == nil {
		return "", false
	}
	named, ok := ptr.Inner.(*typeinfo.NamedType)
	if !ok || named == nil {
		return "", false
	}
	name := "~" + named.Name
	if _, ok := g.lookupMethod(ptr, name); !ok {
		return "", false
	}
	return name, true
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
		path := append([]string{}, e.Path...)
		localID := -1
		if len(path) == 1 && g.currentFn != nil {
			if sym, ok := g.localSymbol(e); ok && g.isFunctionLocal(sym) {
				path[0] = g.mangleLocal(sym)
				if id, ok := g.localIDs[sym]; ok {
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
	case *ast.NoneLit:
		out := &NoneLit{}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.PrefixExpr:
		out := &PrefixExpr{Op: e.Op, Right: g.generateExpr(e.Right)}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.BinaryExpr:
		out := &BinaryExpr{Left: g.generateExpr(e.Left), Op: e.Op, Right: g.generateExpr(e.Right)}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.PostfixExpr:
		out := &PostfixExpr{Left: g.generateExpr(e.Left), Op: e.Op}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.CallExpr:
		out := &CallExpr{Callee: g.generateExpr(e.Callee), Args: make([]Expr, 0, len(e.Args))}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		for _, arg := range e.Args {
			out.Args = append(out.Args, g.generateExpr(arg))
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
		out := &CompositeLit{Items: make([]CompositeItem, 0, len(e.Items))}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		if path, ok := g.constructorMethodPath(typ); ok {
			out.ConstructorPath = append([]string(nil), path...)
		}
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
				out.Items = append(out.Items, CompositeItem{Name: name, Value: g.generateExpr(item.Value)})
			}
			for _, field := range fields {
				if field.Default == nil {
					continue
				}
				if _, exists := used[field.Name]; exists {
					continue
				}
				out.Items = append(out.Items, CompositeItem{Name: field.Name, Value: g.generateExpr(field.Default)})
			}
			return out
		}
		for _, item := range e.Items {
			out.Items = append(out.Items, CompositeItem{Name: ast.ExprText(item.Name), Value: g.generateExpr(item.Value)})
		}
		return out
	case *ast.IndexExpr:
		out := &IndexExpr{Left: g.generateExpr(e.Left), Index: g.generateExpr(e.Index)}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	default:
		return nil
	}
}

func (g *generator) staticIsResult(left, target typeinfo.Type) bool {
	if left == nil || target == nil {
		return false
	}
	if typeinfo.Equal(left, target) {
		return true
	}
	targetIface, ok := unwrapNamed(g.types, target).(*typeinfo.InterfaceType)
	if !ok {
		return false
	}
	if srcIface, ok := unwrapNamed(g.types, left).(*typeinfo.InterfaceType); ok {
		for _, method := range targetIface.OrderedMethods {
			if method == nil || method.Type == nil {
				continue
			}
			got := srcIface.Methods[method.Name]
			if got == nil || srcIface.MethodReceivers[method.Name] != method.Receiver || !hirInterfaceMethodCompatible(method.Type, got) {
				return false
			}
		}
		return true
	}
	if g.lookupMethod == nil {
		return false
	}
	for _, method := range targetIface.OrderedMethods {
		if method == nil {
			continue
		}
		if _, ok := g.lookupMethod(left, method.Name); !ok {
			return false
		}
	}
	return true
}

func hirInterfaceMethodCompatible(expected, got *typeinfo.FuncType) bool {
	if expected == nil || got == nil {
		return false
	}
	if expected.IsUnsafe != got.IsUnsafe {
		return false
	}
	if len(expected.Params) != len(got.Params) || len(expected.ComptimeParams) != len(got.ComptimeParams) {
		return false
	}
	for i := range expected.Params {
		if !typeinfo.Equal(expected.Params[i], got.Params[i]) {
			return false
		}
	}
	for i := range expected.ComptimeParams {
		if expected.ComptimeParams[i] != got.ComptimeParams[i] {
			return false
		}
	}
	return typeinfo.Equal(expected.Result, got.Result)
}

func (g *generator) constructorMethodPath(typ typeinfo.Type) ([]string, bool) {
	if g == nil || g.lookupMethod == nil {
		return nil, false
	}
	named, ok := typ.(*typeinfo.NamedType)
	if !ok || named == nil {
		return nil, false
	}
	path, ok := g.lookupMethod(named, named.Name)
	if !ok {
		return nil, false
	}
	return path, true
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

func syntaxType(types *typeinfo.ModuleInfo, expr ast.TypeExpr) typeinfo.Type {
	if types == nil || expr == nil {
		return nil
	}
	if typ, ok := types.Nodes[expr]; ok {
		return typ
	}
	return nil
}

func (g *generator) resolveTypeExpr(expr ast.TypeExpr) typeinfo.Type {
	if typ := syntaxType(g.types, expr); typ != nil {
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
		return syntaxType(g.types, expr)
	}
}

func effectiveType(types *typeinfo.ModuleInfo, syntax ast.TypeExpr, value ast.Expr) typeinfo.Type {
	if typ := syntaxType(types, syntax); typ != nil {
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
	fields := make([]structLiteralField, 0, len(structDecl.Fields))
	for _, field := range structDecl.Fields {
		if field == nil || field.Name == nil {
			continue
		}
		fields = append(fields, structLiteralField{Name: field.Name.Text(), Default: field.Default})
	}
	return fields, true
}

func unwrapNamed(types *typeinfo.ModuleInfo, typ typeinfo.Type) typeinfo.Type {
	named, ok := typ.(*typeinfo.NamedType)
	if !ok || named == nil || named.Decl == nil {
		return typ
	}
	if types == nil {
		return typ
	}
	if underlying, ok := types.Nodes[named.Decl.Type]; ok && underlying != nil {
		return underlying
	}
	return typ
}
