package hir

import (
	"compiler/internal/frontend/ast"
	"compiler/internal/semantics/typeinfo"
)

func Generate(key, importPath, filePath string, astMod *ast.Module, types *typeinfo.ModuleInfo) *Module {
	if astMod == nil || types == nil {
		return nil
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
			out.Types = append(out.Types, generateTypeDecl(key, types, d))
		case *ast.LetDecl:
			out.Globals = append(out.Globals, generateLetDecl(types, d))
		case *ast.ConstDecl:
			out.Globals = append(out.Globals, generateConstDecl(types, d))
		case *ast.FuncDecl:
			out.Functions = append(out.Functions, generateFunc(types, d))
		}
	}
	return out
}

func generateTypeDecl(key string, types *typeinfo.ModuleInfo, d *ast.TypeDecl) *TypeDecl {
	if d == nil {
		return nil
	}
	out := &TypeDecl{
		Name:       d.Name,
		Named:      &typeinfo.NamedType{ModuleKey: key, Name: d.Name, Decl: d},
		Underlying: syntaxType(types, d.Type),
		Location:   d.Location,
		Source:     d,
	}
	switch t := d.Type.(type) {
	case *ast.StructType:
		out.Struct = generateStructTypeDecl(types, t)
	case *ast.InterfaceType:
		out.Interface = generateInterfaceTypeDecl(types, t)
	case *ast.EnumType:
		out.Enum = generateEnumTypeDecl(t)
	case *ast.UnionType:
		out.Union = generateUnionTypeDecl(types, t)
	case *ast.ErrorType:
		out.Error = generateErrorTypeDecl(t)
	}
	return out
}

func generateStructTypeDecl(types *typeinfo.ModuleInfo, t *ast.StructType) *StructTypeDecl {
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
			Name:     field.Name,
			Type:     syntaxType(types, field.Type),
			Default:  generateExpr(types, field.Default),
			Location: field.Location,
		})
	}
	for _, field := range t.StaticFields {
		if field == nil {
			continue
		}
		out.StaticFields = append(out.StaticFields, &StructFieldDecl{
			Name:     field.Name,
			Type:     syntaxType(types, field.Type),
			Default:  generateExpr(types, field.Default),
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
			Name:     method.Name,
			Result:   syntaxType(types, method.Result),
			Location: method.Location,
			Params:   make([]*Param, 0, len(method.Params)),
		}
		for _, param := range method.Params {
			entry.Params = append(entry.Params, &Param{
				Name:       param.Name,
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
			out.Variants = append(out.Variants, variant.Name)
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
			out.Members = append(out.Members, member.Name)
		}
	}
	return out
}

func generateLetDecl(types *typeinfo.ModuleInfo, d *ast.LetDecl) *Global {
	if d == nil {
		return nil
	}
	return &Global{
		Name:     d.Name,
		Mutable:  d.IsMut,
		Constant: false,
		Type:     effectiveType(types, d.Type, d.Value),
		Value:    generateExpr(types, d.Value),
		Location: d.Location,
		Source:   d,
	}
}

func generateConstDecl(types *typeinfo.ModuleInfo, d *ast.ConstDecl) *Global {
	if d == nil {
		return nil
	}
	return &Global{
		Name:     d.Name,
		Mutable:  false,
		Constant: true,
		Type:     effectiveType(types, d.Type, d.Value),
		Value:    generateExpr(types, d.Value),
		Location: d.Location,
		Source:   d,
	}
}

func generateFunc(types *typeinfo.ModuleInfo, d *ast.FuncDecl) *Func {
	if d == nil {
		return nil
	}
	fn := &Func{
		Name:     d.Name,
		Result:   syntaxType(types, d.Result),
		Body:     generateBlock(types, d.Body),
		Location: d.Location,
		Source:   d,
	}
	if fn.Result == nil {
		fn.Result = &typeinfo.BuiltinType{Name: "void"}
	}
	if d.Receiver != nil {
		fn.Receiver = &Param{
			Name:     d.Receiver.Name,
			Type:     syntaxType(types, d.Receiver.Type),
			Location: d.Receiver.Location,
		}
	}
	fn.Params = make([]*Param, 0, len(d.Params))
	for _, param := range d.Params {
		fn.Params = append(fn.Params, &Param{
			Name:       param.Name,
			Type:       syntaxType(types, param.Type),
			IsComptime: param.IsComptime,
			Location:   param.Location,
		})
	}
	return fn
}

func generateBlock(types *typeinfo.ModuleInfo, block *ast.BlockStmt) *BlockStmt {
	if block == nil {
		return nil
	}
	out := &BlockStmt{Stmts: make([]Stmt, 0, len(block.Stmts))}
	out.Location = block.Location
	for _, stmt := range block.Stmts {
		if lowered := generateStmt(types, stmt); lowered != nil {
			out.Stmts = append(out.Stmts, lowered)
		}
	}
	return out
}

func generateStmt(types *typeinfo.ModuleInfo, stmt ast.Stmt) Stmt {
	switch s := stmt.(type) {
	case nil:
		return nil
	case *ast.BlockStmt:
		return generateBlock(types, s)
	case *ast.LetStmt:
		out := &LetStmt{Name: s.Name, Mutable: s.IsMut, Type: effectiveType(types, s.Type, s.Value), Value: generateExpr(types, s.Value)}
		out.Location = s.Location
		return out
	case *ast.ConstStmt:
		out := &ConstStmt{Name: s.Name, Type: effectiveType(types, s.Type, s.Value), Value: generateExpr(types, s.Value)}
		out.Location = s.Location
		return out
	case *ast.ReturnStmt:
		out := &ReturnStmt{Value: generateExpr(types, s.Value)}
		out.Location = s.Location
		return out
	case *ast.ExprStmt:
		out := &ExprStmt{Value: generateExpr(types, s.Value)}
		out.Location = s.Location
		return out
	case *ast.AssignStmt:
		out := &AssignStmt{Left: generateExpr(types, s.Left), Right: generateExpr(types, s.Right)}
		out.Location = s.Location
		return out
	case *ast.IfStmt:
		out := &IfStmt{Cond: generateExpr(types, s.Cond), Then: generateBlock(types, s.Then), Else: generateStmt(types, s.Else)}
		out.Location = s.Location
		return out
	case *ast.SwitchStmt:
		out := &SwitchStmt{Value: generateExpr(types, s.Value), Cases: make([]*SwitchCase, 0, len(s.Cases))}
		out.Location = s.Location
		for _, kase := range s.Cases {
			if kase == nil {
				continue
			}
			out.Cases = append(out.Cases, &SwitchCase{Expr: generateExpr(types, kase.Expr), Body: generateBlock(types, kase.Body)})
		}
		return out
	case *ast.WhileStmt:
		out := &WhileStmt{Cond: generateExpr(types, s.Cond), Body: generateBlock(types, s.Body)}
		out.Location = s.Location
		return out
	case *ast.ForStmt:
		out := &ForStmt{Init: generateStmt(types, s.Init), Cond: generateExpr(types, s.Cond), Post: generateStmt(types, s.Post), Body: generateBlock(types, s.Body)}
		out.Location = s.Location
		return out
	case *ast.LabelStmt:
		out := &LabelStmt{Name: s.Name, Stmt: generateStmt(types, s.Stmt)}
		out.Location = s.Location
		return out
	case *ast.BreakStmt:
		out := &BreakStmt{Label: s.Label}
		out.Location = s.Location
		return out
	case *ast.ContinueStmt:
		out := &ContinueStmt{Label: s.Label}
		out.Location = s.Location
		return out
	case *ast.DeferStmt:
		out := &DeferStmt{Body: generateStmt(types, s.Body)}
		out.Location = s.Location
		return out
	case *ast.LockStmt:
		out := &LockStmt{Value: generateExpr(types, s.Value), Name: s.Name, Body: generateBlock(types, s.Body)}
		out.Location = s.Location
		return out
	case *ast.UnsafeStmt:
		out := &UnsafeStmt{Body: generateBlock(types, s.Body)}
		out.Location = s.Location
		return out
	default:
		return nil
	}
}

func generateExpr(types *typeinfo.ModuleInfo, expr ast.Expr) Expr {
	typ := exprType(types, expr)
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
		out := &PrefixExpr{Op: e.Op, Right: generateExpr(types, e.Right)}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.UnsafeExpr:
		out := &UnsafeExpr{Value: generateExpr(types, e.Value)}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.BinaryExpr:
		out := &BinaryExpr{Left: generateExpr(types, e.Left), Op: e.Op, Right: generateExpr(types, e.Right)}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.PostfixExpr:
		out := &PostfixExpr{Left: generateExpr(types, e.Left), Op: e.Op}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.CallExpr:
		out := &CallExpr{Callee: generateExpr(types, e.Callee), Args: make([]Expr, 0, len(e.Args))}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		for _, arg := range e.Args {
			out.Args = append(out.Args, generateExpr(types, arg))
		}
		return out
	case *ast.SelectorExpr:
		out := &SelectorExpr{Left: generateExpr(types, e.Left), Name: e.Name}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.CastExpr:
		out := &CastExpr{Left: generateExpr(types, e.Left)}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.CompositeLit:
		out := &CompositeLit{Items: make([]CompositeItem, 0, len(e.Items))}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		for _, item := range e.Items {
			out.Items = append(out.Items, CompositeItem{Name: item.Name, Value: generateExpr(types, item.Value)})
		}
		return out
	default:
		return nil
	}
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
