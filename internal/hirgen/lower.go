package hirgen

import (
	"compiler/internal/context"
	"compiler/internal/frontend/ast"
	"compiler/internal/hir"
	"compiler/internal/phase"
	"compiler/internal/semantics/typeinfo"
)

func Generate(mod *context.Module) *hir.Module {
	if mod == nil || mod.AST == nil || mod.Types == nil {
		return nil
	}
	out := &hir.Module{
		Key:        mod.Key,
		ImportPath: mod.ImportPath,
		FilePath:   mod.FilePath,
		Source:     mod.AST,
		Globals:    make([]*hir.Global, 0),
		Functions:  make([]*hir.Func, 0),
	}
	for _, decl := range mod.AST.Decls {
		switch d := decl.(type) {
		case *ast.LetDecl:
			out.Globals = append(out.Globals, lowerLetDecl(mod, d))
		case *ast.ConstDecl:
			out.Globals = append(out.Globals, lowerConstDecl(mod, d))
		case *ast.FuncDecl:
			out.Functions = append(out.Functions, lowerFunc(mod, d))
		}
	}
	mod.HIR = out
	mod.Phase = phase.PhaseHIRGenerated
	return out
}

func lowerLetDecl(mod *context.Module, d *ast.LetDecl) *hir.Global {
	if d == nil {
		return nil
	}
	return &hir.Global{
		Name:     d.Name,
		Mutable:  d.IsMut,
		Constant: false,
		Type:     effectiveType(mod, d.Type, d.Value),
		Value:    lowerExpr(mod, d.Value),
		Location: d.Location,
		Source:   d,
	}
}

func lowerConstDecl(mod *context.Module, d *ast.ConstDecl) *hir.Global {
	if d == nil {
		return nil
	}
	return &hir.Global{
		Name:     d.Name,
		Mutable:  false,
		Constant: true,
		Type:     effectiveType(mod, d.Type, d.Value),
		Value:    lowerExpr(mod, d.Value),
		Location: d.Location,
		Source:   d,
	}
}

func lowerFunc(mod *context.Module, d *ast.FuncDecl) *hir.Func {
	if d == nil {
		return nil
	}
	fn := &hir.Func{
		Name:     d.Name,
		Result:   syntaxType(mod, d.Result),
		Body:     lowerBlock(mod, d.Body),
		Location: d.Location,
		Source:   d,
	}
	if fn.Result == nil {
		fn.Result = &typeinfo.BuiltinType{Name: "void"}
	}
	if d.Receiver != nil {
		fn.Receiver = &hir.Param{
			Name:     d.Receiver.Name,
			Type:     syntaxType(mod, d.Receiver.Type),
			Location: d.Receiver.Location,
		}
	}
	fn.Params = make([]*hir.Param, 0, len(d.Params))
	for _, param := range d.Params {
		fn.Params = append(fn.Params, &hir.Param{
			Name:       param.Name,
			Type:       syntaxType(mod, param.Type),
			IsComptime: param.IsComptime,
			Location:   param.Location,
		})
	}
	return fn
}

func lowerBlock(mod *context.Module, block *ast.BlockStmt) *hir.BlockStmt {
	if block == nil {
		return nil
	}
	out := &hir.BlockStmt{Stmts: make([]hir.Stmt, 0, len(block.Stmts))}
	out.Location = block.Location
	for _, stmt := range block.Stmts {
		if lowered := lowerStmt(mod, stmt); lowered != nil {
			out.Stmts = append(out.Stmts, lowered)
		}
	}
	return out
}

func lowerStmt(mod *context.Module, stmt ast.Stmt) hir.Stmt {
	switch s := stmt.(type) {
	case nil:
		return nil
	case *ast.BlockStmt:
		return lowerBlock(mod, s)
	case *ast.LetStmt:
		out := &hir.LetStmt{Name: s.Name, Mutable: s.IsMut, Type: effectiveType(mod, s.Type, s.Value), Value: lowerExpr(mod, s.Value)}
		out.Location = s.Location
		return out
	case *ast.ConstStmt:
		out := &hir.ConstStmt{Name: s.Name, Type: effectiveType(mod, s.Type, s.Value), Value: lowerExpr(mod, s.Value)}
		out.Location = s.Location
		return out
	case *ast.ReturnStmt:
		out := &hir.ReturnStmt{Value: lowerExpr(mod, s.Value)}
		out.Location = s.Location
		return out
	case *ast.ExprStmt:
		out := &hir.ExprStmt{Value: lowerExpr(mod, s.Value)}
		out.Location = s.Location
		return out
	case *ast.AssignStmt:
		out := &hir.AssignStmt{Left: lowerExpr(mod, s.Left), Right: lowerExpr(mod, s.Right)}
		out.Location = s.Location
		return out
	case *ast.IfStmt:
		out := &hir.IfStmt{Cond: lowerExpr(mod, s.Cond), Then: lowerBlock(mod, s.Then), Else: lowerStmt(mod, s.Else)}
		out.Location = s.Location
		return out
	case *ast.SwitchStmt:
		out := &hir.SwitchStmt{Value: lowerExpr(mod, s.Value), Cases: make([]*hir.SwitchCase, 0, len(s.Cases))}
		out.Location = s.Location
		for _, kase := range s.Cases {
			if kase == nil {
				continue
			}
			out.Cases = append(out.Cases, &hir.SwitchCase{Expr: lowerExpr(mod, kase.Expr), Body: lowerBlock(mod, kase.Body)})
		}
		return out
	case *ast.WhileStmt:
		out := &hir.WhileStmt{Cond: lowerExpr(mod, s.Cond), Body: lowerBlock(mod, s.Body)}
		out.Location = s.Location
		return out
	case *ast.ForStmt:
		out := &hir.ForStmt{Init: lowerStmt(mod, s.Init), Cond: lowerExpr(mod, s.Cond), Post: lowerStmt(mod, s.Post), Body: lowerBlock(mod, s.Body)}
		out.Location = s.Location
		return out
	case *ast.LabelStmt:
		out := &hir.LabelStmt{Name: s.Name, Stmt: lowerStmt(mod, s.Stmt)}
		out.Location = s.Location
		return out
	case *ast.BreakStmt:
		out := &hir.BreakStmt{Label: s.Label}
		out.Location = s.Location
		return out
	case *ast.ContinueStmt:
		out := &hir.ContinueStmt{Label: s.Label}
		out.Location = s.Location
		return out
	case *ast.DeferStmt:
		out := &hir.DeferStmt{Body: lowerStmt(mod, s.Body)}
		out.Location = s.Location
		return out
	case *ast.LockStmt:
		out := &hir.LockStmt{Value: lowerExpr(mod, s.Value), Name: s.Name, Body: lowerBlock(mod, s.Body)}
		out.Location = s.Location
		return out
	case *ast.UnsafeStmt:
		out := &hir.UnsafeStmt{Body: lowerBlock(mod, s.Body)}
		out.Location = s.Location
		return out
	default:
		return nil
	}
}

func lowerExpr(mod *context.Module, expr ast.Expr) hir.Expr {
	typ := exprType(mod, expr)
	switch e := expr.(type) {
	case nil:
		return nil
	case *ast.BadExpr:
		out := &hir.BadExpr{}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.Ident:
		out := &hir.Ident{Path: append([]string{}, e.Path...)}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.NumberLit:
		out := &hir.NumberLit{Value: e.Value}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.StringLit:
		out := &hir.StringLit{Value: e.Value}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.NoneLit:
		out := &hir.NoneLit{}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.PrefixExpr:
		out := &hir.PrefixExpr{Op: e.Op, Right: lowerExpr(mod, e.Right)}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.UnsafeExpr:
		out := &hir.UnsafeExpr{Value: lowerExpr(mod, e.Value)}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.BinaryExpr:
		out := &hir.BinaryExpr{Left: lowerExpr(mod, e.Left), Op: e.Op, Right: lowerExpr(mod, e.Right)}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.PostfixExpr:
		out := &hir.PostfixExpr{Left: lowerExpr(mod, e.Left), Op: e.Op}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.CallExpr:
		out := &hir.CallExpr{Callee: lowerExpr(mod, e.Callee), Args: make([]hir.Expr, 0, len(e.Args))}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		for _, arg := range e.Args {
			out.Args = append(out.Args, lowerExpr(mod, arg))
		}
		return out
	case *ast.SelectorExpr:
		out := &hir.SelectorExpr{Left: lowerExpr(mod, e.Left), Name: e.Name}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.CastExpr:
		out := &hir.CastExpr{Left: lowerExpr(mod, e.Left)}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		return out
	case *ast.CompositeLit:
		out := &hir.CompositeLit{Items: make([]hir.CompositeItem, 0, len(e.Items))}
		out.ExprType, out.Location, out.Source = typ, e.Location, e
		for _, item := range e.Items {
			out.Items = append(out.Items, hir.CompositeItem{Name: item.Name, Value: lowerExpr(mod, item.Value)})
		}
		return out
	default:
		return nil
	}
}

func exprType(mod *context.Module, expr ast.Expr) typeinfo.Type {
	if mod == nil || mod.Types == nil || expr == nil {
		return typeinfo.UnknownType{}
	}
	if typ, ok := mod.Types.Nodes[expr]; ok && typ != nil {
		return typ
	}
	return typeinfo.UnknownType{}
}

func syntaxType(mod *context.Module, expr ast.TypeExpr) typeinfo.Type {
	if mod == nil || mod.Types == nil || expr == nil {
		return nil
	}
	if typ, ok := mod.Types.Nodes[expr]; ok {
		return typ
	}
	return nil
}

func effectiveType(mod *context.Module, syntax ast.TypeExpr, value ast.Expr) typeinfo.Type {
	if typ := syntaxType(mod, syntax); typ != nil {
		return typ
	}
	if value != nil {
		return exprType(mod, value)
	}
	return typeinfo.UnknownType{}
}
