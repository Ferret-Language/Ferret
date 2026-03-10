package ast

import (
	"encoding/json"

	"compiler/internal/source"
)

func MarshalModuleJSON(mod *Module) ([]byte, error) {
	return json.MarshalIndent(DebugModule(mod), "", "  ")
}

func DebugModule(mod *Module) map[string]any {
	if mod == nil {
		return nil
	}
	imports := make([]any, 0, len(mod.Imports))
	for _, imp := range mod.Imports {
		imports = append(imports, map[string]any{
			"kind":  "ImportDecl",
			"path":  imp.Path,
			"alias": imp.Alias,
			"loc":   debugLoc(imp.Location),
		})
	}
	decls := make([]any, 0, len(mod.Decls))
	for _, decl := range mod.Decls {
		decls = append(decls, debugDecl(decl))
	}
	return map[string]any{
		"kind":      "Module",
		"file_path": mod.FilePath,
		"imports":   imports,
		"decls":     decls,
	}
}

func debugDecl(decl Decl) any {
	switch d := decl.(type) {
	case *LetDecl:
		return map[string]any{
			"kind":   "LetDecl",
			"name":   d.Name,
			"is_mut": d.IsMut,
			"type":   debugType(d.Type),
			"value":  debugExpr(d.Value),
			"loc":    debugLoc(d.Location),
		}
	case *ConstDecl:
		return map[string]any{
			"kind":  "ConstDecl",
			"name":  d.Name,
			"type":  debugType(d.Type),
			"value": debugExpr(d.Value),
			"loc":   debugLoc(d.Location),
		}
	case *TypeDecl:
		return map[string]any{
			"kind": "TypeDecl",
			"name": d.Name,
			"type": debugType(d.Type),
			"loc":  debugLoc(d.Location),
		}
	case *FuncDecl:
		params := make([]any, 0, len(d.Params))
		for _, param := range d.Params {
			params = append(params, debugParam(param))
		}
		var recv any
		if d.Receiver != nil {
			recv = map[string]any{
				"name": d.Receiver.Name,
				"type": debugType(d.Receiver.Type),
				"loc":  debugLoc(d.Receiver.Location),
			}
		}
		return map[string]any{
			"kind":           "FuncDecl",
			"name":           d.Name,
			"receiver":       recv,
			"is_constructor": d.IsConstructor,
			"is_destructor":  d.IsDestructor,
			"params":         params,
			"result":         debugType(d.Result),
			"body":           debugStmt(d.Body),
			"loc":            debugLoc(d.Location),
		}
	default:
		return map[string]any{"kind": "<unknown-decl>"}
	}
}

func debugParam(p Param) any {
	return map[string]any{
		"name":        p.Name,
		"is_comptime": p.IsComptime,
		"type":        debugType(p.Type),
		"loc":         debugLoc(p.Location),
	}
}

func debugStmt(stmt Stmt) any {
	switch s := stmt.(type) {
	case nil:
		return nil
	case *BlockStmt:
		stmts := make([]any, 0, len(s.Stmts))
		for _, child := range s.Stmts {
			stmts = append(stmts, debugStmt(child))
		}
		return map[string]any{"kind": "BlockStmt", "stmts": stmts, "loc": debugLoc(s.Location)}
	case *LetStmt:
		return map[string]any{"kind": "LetStmt", "name": s.Name, "is_mut": s.IsMut, "type": debugType(s.Type), "value": debugExpr(s.Value), "loc": debugLoc(s.Location)}
	case *ConstStmt:
		return map[string]any{"kind": "ConstStmt", "name": s.Name, "type": debugType(s.Type), "value": debugExpr(s.Value), "loc": debugLoc(s.Location)}
	case *ReturnStmt:
		return map[string]any{"kind": "ReturnStmt", "value": debugExpr(s.Value), "loc": debugLoc(s.Location)}
	case *ExprStmt:
		return map[string]any{"kind": "ExprStmt", "value": debugExpr(s.Value), "loc": debugLoc(s.Location)}
	case *AssignStmt:
		return map[string]any{"kind": "AssignStmt", "left": debugExpr(s.Left), "right": debugExpr(s.Right), "loc": debugLoc(s.Location)}
	case *IfStmt:
		return map[string]any{"kind": "IfStmt", "cond": debugExpr(s.Cond), "then": debugStmt(s.Then), "else": debugStmt(s.Else), "loc": debugLoc(s.Location)}
	case *SwitchStmt:
		cases := make([]any, 0, len(s.Cases))
		for _, c := range s.Cases {
			if c == nil {
				cases = append(cases, nil)
				continue
			}
			cases = append(cases, map[string]any{"expr": debugExpr(c.Expr), "body": debugStmt(c.Body), "loc": debugLoc(c.Location)})
		}
		return map[string]any{"kind": "SwitchStmt", "value": debugExpr(s.Value), "cases": cases, "loc": debugLoc(s.Location)}
	case *WhileStmt:
		return map[string]any{"kind": "WhileStmt", "cond": debugExpr(s.Cond), "body": debugStmt(s.Body), "loc": debugLoc(s.Location)}
	case *ForStmt:
		return map[string]any{"kind": "ForStmt", "init": debugStmt(s.Init), "cond": debugExpr(s.Cond), "post": debugStmt(s.Post), "body": debugStmt(s.Body), "loc": debugLoc(s.Location)}
	case *LabelStmt:
		return map[string]any{"kind": "LabelStmt", "name": s.Name, "stmt": debugStmt(s.Stmt), "loc": debugLoc(s.Location)}
	case *BreakStmt:
		return map[string]any{"kind": "BreakStmt", "label": s.Label, "loc": debugLoc(s.Location)}
	case *ContinueStmt:
		return map[string]any{"kind": "ContinueStmt", "label": s.Label, "loc": debugLoc(s.Location)}
	case *DeferStmt:
		return map[string]any{"kind": "DeferStmt", "body": debugStmt(s.Body), "loc": debugLoc(s.Location)}
	case *LockStmt:
		return map[string]any{"kind": "LockStmt", "value": debugExpr(s.Value), "name": s.Name, "body": debugStmt(s.Body), "loc": debugLoc(s.Location)}
	case *UnsafeStmt:
		return map[string]any{"kind": "UnsafeStmt", "body": debugStmt(s.Body), "loc": debugLoc(s.Location)}
	default:
		return map[string]any{"kind": "<unknown-stmt>"}
	}
}

func debugExpr(expr Expr) any {
	switch e := expr.(type) {
	case nil:
		return nil
	case *Ident:
		return map[string]any{"kind": "Ident", "path": e.Path, "loc": debugLoc(e.Location)}
	case *BadExpr:
		return map[string]any{"kind": "BadExpr", "loc": debugLoc(e.Location)}
	case *NumberLit:
		return map[string]any{"kind": "NumberLit", "value": e.Value, "loc": debugLoc(e.Location)}
	case *StringLit:
		return map[string]any{"kind": "StringLit", "value": e.Value, "loc": debugLoc(e.Location)}
	case *NoneLit:
		return map[string]any{"kind": "NoneLit", "loc": debugLoc(e.Location)}
	case *PrefixExpr:
		return map[string]any{"kind": "PrefixExpr", "op": e.Op, "right": debugExpr(e.Right), "loc": debugLoc(e.Location)}
	case *UnsafeExpr:
		return map[string]any{"kind": "UnsafeExpr", "value": debugExpr(e.Value), "loc": debugLoc(e.Location)}
	case *BinaryExpr:
		return map[string]any{"kind": "BinaryExpr", "left": debugExpr(e.Left), "op": e.Op, "right": debugExpr(e.Right), "loc": debugLoc(e.Location)}
	case *PostfixExpr:
		return map[string]any{"kind": "PostfixExpr", "left": debugExpr(e.Left), "op": e.Op, "loc": debugLoc(e.Location)}
	case *CallExpr:
		args := make([]any, 0, len(e.Args))
		for _, arg := range e.Args {
			args = append(args, debugExpr(arg))
		}
		typeArgs := make([]any, 0, len(e.TypeArgs))
		for _, typ := range e.TypeArgs {
			typeArgs = append(typeArgs, debugType(typ))
		}
		return map[string]any{"kind": "CallExpr", "callee": debugExpr(e.Callee), "type_args": typeArgs, "args": args, "loc": debugLoc(e.Location)}
	case *SelectorExpr:
		return map[string]any{"kind": "SelectorExpr", "left": debugExpr(e.Left), "name": e.Name, "loc": debugLoc(e.Location)}
	case *CompositeLit:
		items := make([]any, 0, len(e.Items))
		for _, item := range e.Items {
			items = append(items, map[string]any{"name": item.Name, "value": debugExpr(item.Value)})
		}
		return map[string]any{"kind": "CompositeLit", "items": items, "loc": debugLoc(e.Location)}
	default:
		return map[string]any{"kind": "<unknown-expr>"}
	}
}

func debugType(typ TypeExpr) any {
	switch t := typ.(type) {
	case nil:
		return nil
	case *NamedType:
		return map[string]any{"kind": "NamedType", "path": t.Path, "loc": debugLoc(t.Location)}
	case *PointerType:
		return map[string]any{"kind": "PointerType", "is_own": t.IsOwn, "is_raw": t.IsRaw, "is_mut": t.IsMut, "inner": debugType(t.Inner), "loc": debugLoc(t.Location)}
	case *OptionalType:
		return map[string]any{"kind": "OptionalType", "inner": debugType(t.Inner), "loc": debugLoc(t.Location)}
	case *ErrorUnionType:
		return map[string]any{"kind": "ErrorUnionType", "error": debugType(t.Error), "value": debugType(t.Value), "loc": debugLoc(t.Location)}
	case *ArrayType:
		return map[string]any{"kind": "ArrayType", "size": debugExpr(t.Size), "inner": debugType(t.Inner), "loc": debugLoc(t.Location)}
	case *TupleType:
		elems := make([]any, 0, len(t.Elems))
		for _, elem := range t.Elems {
			elems = append(elems, debugType(elem))
		}
		return map[string]any{"kind": "TupleType", "elems": elems, "loc": debugLoc(t.Location)}
	case *StructType:
		fields := make([]any, 0, len(t.Fields))
		for _, field := range t.Fields {
			fields = append(fields, debugField(field))
		}
		staticFields := make([]any, 0, len(t.StaticFields))
		for _, field := range t.StaticFields {
			staticFields = append(staticFields, debugStaticField(field))
		}
		return map[string]any{"kind": "StructType", "fields": fields, "static_fields": staticFields, "loc": debugLoc(t.Location)}
	case *InterfaceType:
		methods := make([]any, 0, len(t.Methods))
		for _, method := range t.Methods {
			if method == nil {
				methods = append(methods, nil)
				continue
			}
			params := make([]any, 0, len(method.Params))
			for _, param := range method.Params {
				params = append(params, debugParam(param))
			}
			methods = append(methods, map[string]any{"name": method.Name, "params": params, "result": debugType(method.Result), "loc": debugLoc(method.Location)})
		}
		return map[string]any{"kind": "InterfaceType", "methods": methods, "loc": debugLoc(t.Location)}
	case *EnumType:
		variants := make([]any, 0, len(t.Variants))
		for _, v := range t.Variants {
			if v == nil {
				variants = append(variants, nil)
				continue
			}
			variants = append(variants, map[string]any{"name": v.Name, "loc": debugLoc(v.Location)})
		}
		return map[string]any{"kind": "EnumType", "variants": variants, "loc": debugLoc(t.Location)}
	case *UnionType:
		members := make([]any, 0, len(t.Members))
		for _, m := range t.Members {
			members = append(members, debugType(m))
		}
		return map[string]any{"kind": "UnionType", "members": members, "loc": debugLoc(t.Location)}
	case *ErrorType:
		members := make([]any, 0, len(t.Members))
		for _, m := range t.Members {
			if m == nil {
				members = append(members, nil)
				continue
			}
			members = append(members, map[string]any{"name": m.Name, "loc": debugLoc(m.Location)})
		}
		return map[string]any{"kind": "ErrorType", "members": members, "loc": debugLoc(t.Location)}
	default:
		return map[string]any{"kind": "<unknown-type>"}
	}
}

func debugField(field *FieldDecl) any {
	if field == nil {
		return nil
	}
	return map[string]any{"name": field.Name, "type": debugType(field.Type), "default": debugExpr(field.Default), "loc": debugLoc(field.Location)}
}

func debugStaticField(field *StaticFieldDecl) any {
	if field == nil {
		return nil
	}
	return map[string]any{"name": field.Name, "type": debugType(field.Type), "default": debugExpr(field.Default), "loc": debugLoc(field.Location)}
}

func debugLoc(loc source.Location) any {
	start := map[string]any{"line": loc.Start.Line, "column": loc.Start.Column, "index": loc.Start.Index}
	end := map[string]any{"line": loc.End.Line, "column": loc.End.Column, "index": loc.End.Index}
	file := ""
	if loc.Filename != nil {
		file = *loc.Filename
	}
	return map[string]any{"file": file, "start": start, "end": end}
}
