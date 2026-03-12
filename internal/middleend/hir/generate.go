package hir

import (
	"compiler/internal/frontend/ast"
	"compiler/internal/semantics/binding"
	"compiler/internal/semantics/typeinfo"
)

type MethodLookup func(receiver typeinfo.Type, methodName string) ([]string, bool)

type generator struct {
	key          string
	importPath   string
	filePath     string
	types        *typeinfo.ModuleInfo
	bindings     *binding.ModuleInfo
	lookupMethod MethodLookup
}

func Generate(key, importPath, filePath string, astMod *ast.Module, types *typeinfo.ModuleInfo, bindings *binding.ModuleInfo, lookupMethod MethodLookup) *Module {
	if astMod == nil || types == nil {
		return nil
	}
	g := &generator{key: key, importPath: importPath, filePath: filePath, types: types, bindings: bindings, lookupMethod: lookupMethod}
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
		IsMove:     d.IsMove,
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
	fn := &Func{
		Name:       d.Name.Text(),
		IsUnsafe:   d.IsUnsafe,
		IsBuiltin:  d.IsBuiltin,
		IsExtern:   d.IsExtern,
		ExternName: d.ExternName,
		Result:     syntaxType(g.types, d.Result),
		Body:       g.generateBlock(d.Body),
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
			Name:     d.Receiver.Name.Text(),
			Type:     syntaxType(g.types, d.Receiver.Type),
			Location: d.Receiver.Location,
		}
	}
	fn.Params = make([]*Param, 0, len(d.Params))
	for _, param := range d.Params {
		fn.Params = append(fn.Params, &Param{
			Name:       param.Name.Text(),
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
		out := &LetStmt{Name: s.Name.Text(), Mutable: s.IsMut, Type: effectiveType(g.types, s.Type, s.Value), Value: g.generateExpr(s.Value)}
		out.Location = s.Location
		return out
	case *ast.ConstStmt:
		out := &ConstStmt{Name: s.Name.Text(), Type: effectiveType(g.types, s.Type, s.Value), Value: g.generateExpr(s.Value)}
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
			out.Arms = append(out.Arms, &MatchArm{Wildcard: arm.Wildcard, Pattern: g.generateExpr(arm.Pattern), Body: g.generateBlock(arm.Body)})
		}
		return out
	case *ast.WhileStmt:
		out := &WhileStmt{Cond: g.generateExpr(s.Cond), Body: g.generateBlock(s.Body)}
		out.Location = s.Location
		return out
	case *ast.ForStmt:
		out := &ForStmt{Iterable: g.generateExpr(s.Iterable), Body: g.generateBlock(s.Body)}
		if s.Index != nil {
			out.IndexName = s.Index.Text()
		}
		if s.Value != nil {
			out.ValueName = s.Value.Text()
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
		out := &LockStmt{Value: g.generateExpr(s.Value), Name: s.Name.Text(), Body: g.generateBlock(s.Body)}
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
	ident := &Ident{Path: []string{letStmt.Name.Text()}}
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
		Result:           &typeinfo.BuiltinType{Name: "void"},
		ImplicitReceiver: &typeinfo.PointerType{IsOwn: true, Inner: ident.ExprType},
	}
	call.Callee.(*SelectorExpr).Location = letStmt.Location
	deferred := &DeferStmt{Body: &ExprStmt{Value: call}}
	deferred.Location = letStmt.Location
	return deferred
}

func (g *generator) destructorMethodName(typ typeinfo.Type) (string, bool) {
	named, ok := typ.(*typeinfo.NamedType)
	if !ok || named == nil || g.lookupMethod == nil {
		return "", false
	}
	name := "~" + named.Name
	if _, ok := g.lookupMethod(named, name); !ok {
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
		out := &Ident{Path: append([]string{}, e.Path...)}
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
	case *ast.CatchExpr:
		out := &CatchExpr{
			Left:     g.generateExpr(e.Left),
			Fallback: g.generateExpr(e.Fallback),
			Handler:  g.generateBlock(e.Handler),
		}
		if e.Payload != nil {
			out.PayloadName = e.Payload.Text()
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
