package parser

import (
	"fmt"

	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/source"
)

func (p *Parser) validateModule(mod *ast.Module) {
	if mod == nil {
		return
	}
	for _, decl := range mod.Decls {
		p.validateDecl(decl)
	}
}

func (p *Parser) validateDecl(decl ast.Decl) {
	switch d := decl.(type) {
	case *ast.LetDecl:
		p.validateType(d.Type)
		p.validateExpr(d.Value)
	case *ast.ConstDecl:
		p.validateExpr(d.Value)
		p.validateType(d.Type)
	case *ast.TypeDecl:
		p.validateType(d.Type)
	case *ast.FuncDecl:
		p.validateType(d.Result)
		for _, param := range d.Params {
			p.validateType(param.Type)
		}
		p.validateStmt(d.Body)
	}
}

func (p *Parser) validateStmt(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		for _, child := range s.Stmts {
			p.validateStmt(child)
		}
	case *ast.LetStmt:
		p.validateType(s.Type)
		p.validateExpr(s.Value)
	case *ast.ConstStmt:
		p.validateType(s.Type)
		p.validateExpr(s.Value)
	case *ast.ReturnStmt:
		p.validateExpr(s.Value)
	case *ast.ExprStmt:
		p.validateExpr(s.Value)
	case *ast.AssignStmt:
		p.validateExpr(s.Left)
		p.validateExpr(s.Right)
	case *ast.IfStmt:
		p.validateExpr(s.Cond)
		p.validateStmt(s.Then)
		p.validateStmt(s.Else)
	case *ast.SwitchStmt:
		p.validateExpr(s.Value)
		for _, c := range s.Cases {
			if c == nil {
				continue
			}
			p.validateExpr(c.Expr)
			p.validateStmt(c.Body)
		}
	case *ast.WhileStmt:
		p.validateExpr(s.Cond)
		p.validateStmt(s.Body)
	case *ast.ForStmt:
		p.validateStmt(s.Init)
		p.validateExpr(s.Cond)
		p.validateStmt(s.Post)
		p.validateStmt(s.Body)
	case *ast.LabelStmt:
		p.validateStmt(s.Stmt)
	case *ast.DeferStmt:
		p.validateStmt(s.Body)
	case *ast.LockStmt:
		p.validateExpr(s.Value)
		p.validateStmt(s.Body)
	case *ast.UnsafeStmt:
		p.validateStmt(s.Body)
	}
}

func (p *Parser) validateExpr(expr ast.Expr) {
	switch e := expr.(type) {
	case *ast.PrefixExpr:
		p.validateExpr(e.Right)
	case *ast.UnsafeExpr:
		p.validateExpr(e.Value)
	case *ast.BinaryExpr:
		p.validateExpr(e.Left)
		p.validateExpr(e.Right)
	case *ast.PostfixExpr:
		p.validateExpr(e.Left)
	case *ast.CallExpr:
		p.validateExpr(e.Callee)
		for _, arg := range e.Args {
			p.validateExpr(arg)
		}
		for _, typeArg := range e.TypeArgs {
			p.validateType(typeArg)
		}
	case *ast.SelectorExpr:
		p.validateExpr(e.Left)
	case *ast.CompositeLit:
		p.validateCompositeLit(e)
	}
}

func (p *Parser) validateType(typ ast.TypeExpr) {
	switch t := typ.(type) {
	case *ast.PointerType:
		p.validateType(t.Inner)
	case *ast.OptionalType:
		p.validateType(t.Inner)
	case *ast.ErrorUnionType:
		p.validateType(t.Error)
		p.validateType(t.Value)
	case *ast.ArrayType:
		p.validateExpr(t.Size)
		p.validateType(t.Inner)
	case *ast.TupleType:
		for _, elem := range t.Elems {
			p.validateType(elem)
		}
	case *ast.StructType:
		seenFields := make(map[string]source.Location)
		for _, field := range t.Fields {
			if field == nil {
				continue
			}
			if prev, ok := seenFields[field.Name]; ok {
				p.reportDuplicateDeclName(
					fmt.Sprintf("duplicate field %q", field.Name),
					field.Location,
					prev,
				)
			} else {
				seenFields[field.Name] = field.Location
			}
			p.validateType(field.Type)
			p.validateExpr(field.Default)
		}
		for _, field := range t.StaticFields {
			if field == nil {
				continue
			}
			if prev, ok := seenFields[field.Name]; ok {
				p.reportDuplicateDeclName(
					fmt.Sprintf("duplicate field %q", field.Name),
					field.Location,
					prev,
				)
			} else {
				seenFields[field.Name] = field.Location
			}
			p.validateType(field.Type)
			p.validateExpr(field.Default)
		}
	case *ast.InterfaceType:
		seen := make(map[string]*ast.InterfaceMethod)
		for _, method := range t.Methods {
			if method == nil {
				continue
			}
			if prev, ok := seen[method.Name]; ok {
				p.reportDuplicateDeclName(
					fmt.Sprintf("duplicate interface method %q", method.Name),
					method.Location,
					prev.Location,
				)
			} else {
				seen[method.Name] = method
			}
			for _, param := range method.Params {
				p.validateType(param.Type)
			}
			p.validateType(method.Result)
		}
	case *ast.EnumType:
		seen := make(map[string]*ast.EnumVariant)
		for _, variant := range t.Variants {
			if variant == nil {
				continue
			}
			if prev, ok := seen[variant.Name]; ok {
				p.reportDuplicateDeclName(
					fmt.Sprintf("duplicate enum variant %q", variant.Name),
					variant.Location,
					prev.Location,
				)
			} else {
				seen[variant.Name] = variant
			}
		}
	case *ast.ErrorType:
		seen := make(map[string]*ast.ErrorMember)
		for _, member := range t.Members {
			if member == nil {
				continue
			}
			if prev, ok := seen[member.Name]; ok {
				p.reportDuplicateDeclName(
					fmt.Sprintf("duplicate error member %q", member.Name),
					member.Location,
					prev.Location,
				)
			} else {
				seen[member.Name] = member
			}
		}
	case *ast.UnionType:
		seen := make(map[string]ast.TypeExpr)
		for _, member := range t.Members {
			p.validateType(member)
			key := renderType(member)
			if prev, ok := seen[key]; ok {
				p.reportDuplicateDeclName(
					fmt.Sprintf("duplicate union member %q", key),
					member.Loc(),
					prev.Loc(),
				)
			} else {
				seen[key] = member
			}
		}
	}
}

func (p *Parser) validateCompositeLit(lit *ast.CompositeLit) {
	if lit == nil {
		return
	}
	seenNamed := false
	seenPositional := false
	seenFields := make(map[string]ast.CompositeItem)
	for _, item := range lit.Items {
		p.validateExpr(item.Value)
		if item.Name == "" {
			seenPositional = true
			continue
		}
		seenNamed = true
		if prev, ok := seenFields[item.Name]; ok {
			p.reportDuplicateDeclName(
				fmt.Sprintf("duplicate composite field %q", item.Name),
				item.Value.Loc(),
				prev.Value.Loc(),
			)
		} else {
			seenFields[item.Name] = item
		}
	}
	if seenNamed && seenPositional {
		p.diag.Add(
			diagnostics.NewError("cannot mix named and positional composite elements").
				WithCode(diagnostics.ErrInvalidExpression).
				WithPrimaryLabel(&lit.Location, "use either all named fields or all positional values"),
		)
	}
}

func (p *Parser) reportDuplicateDeclName(message string, currentLoc, previousLoc source.Location) {
	diag := diagnostics.NewError(message).
		WithCode(diagnostics.ErrInvalidDeclaration).
		WithPrimaryLabel(&currentLoc, "duplicate declaration")
	diag.WithSecondaryLabel(&previousLoc, "previous declaration is here")
	p.diag.Add(diag)
}

func renderType(typ ast.TypeExpr) string {
	switch t := typ.(type) {
	case nil:
		return "<nil>"
	case *ast.NamedType:
		return fmt.Sprintf("named:%v", t.Path)
	case *ast.PointerType:
		return fmt.Sprintf("ptr(own=%t,raw=%t,mut=%t,%s)", t.IsOwn, t.IsRaw, t.IsMut, renderType(t.Inner))
	case *ast.OptionalType:
		return "?" + renderType(t.Inner)
	case *ast.ErrorUnionType:
		return renderType(t.Error) + "!" + renderType(t.Value)
	case *ast.ArrayType:
		return "[" + renderExpr(t.Size) + "]" + renderType(t.Inner)
	case *ast.TupleType:
		parts := make([]string, 0, len(t.Elems))
		for _, elem := range t.Elems {
			parts = append(parts, renderType(elem))
		}
		return "(" + fmt.Sprint(parts) + ")"
	case *ast.StructType:
		return "struct"
	case *ast.InterfaceType:
		return "interface"
	case *ast.EnumType:
		return "enum"
	case *ast.UnionType:
		return "union"
	case *ast.ErrorType:
		return "error"
	default:
		return fmt.Sprintf("%T", typ)
	}
}

func renderExpr(expr ast.Expr) string {
	switch e := expr.(type) {
	case nil:
		return "<nil>"
	case *ast.NumberLit:
		return e.Value
	case *ast.StringLit:
		return e.Value
	case *ast.Ident:
		return fmt.Sprint(e.Path)
	default:
		return fmt.Sprintf("%T", expr)
	}
}
