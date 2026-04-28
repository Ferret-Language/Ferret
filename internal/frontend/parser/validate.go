package parser

import (
	"fmt"
	"reflect"
	"strings"

	"compiler/internal/core/diagnostics"
	"compiler/internal/core/source"
	"compiler/internal/frontend/ast"
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
		p.validateDeclAttributes(d.Attrs, "let")
		p.validateType(d.Type)
		p.validateExpr(d.Value)
	case *ast.ConstDecl:
		p.validateDeclAttributes(d.Attrs, "const")
		p.validateExpr(d.Value)
		p.validateType(d.Type)
	case *ast.TypeDecl:
		p.validateDeclAttributes(d.Attrs, "type")
		p.validateTypeParams(d.TypeParams)
		p.validateType(d.Type)
	case *ast.FuncDecl:
		p.validateDeclAttributes(d.Attrs, "fn")
		p.validateTypeParams(d.TypeParams)
		p.validateType(d.Result)
		for _, param := range d.Params {
			p.validateType(param.Type)
			p.validateExpr(param.Default)
		}
		if d.IsTest {
			if d.IsUnsafe {
				p.errorAt(d.Location, "test declarations cannot be marked unsafe")
			}
			if d.IsExtern {
				p.errorAt(d.Location, "test declarations cannot be extern")
			}
			if len(d.TypeParams) > 0 || len(d.Params) > 0 || d.Result != nil || d.Receiver != nil || d.OwnerType != nil {
				p.errorAt(d.Location, "test declarations cannot declare receivers, parameters, type parameters, or result types")
			}
		}
		p.validateStmt(d.Body)
	}
}

func (p *Parser) validateDeclAttributes(attrs []ast.Attribute, declKind string) {
	if len(attrs) == 0 {
		return
	}
	for _, attr := range attrs {
		spec, ok := LookupDeclAttributeSpec(attr.Name)
		if !ok {
			p.errorAt(attr.Location, fmt.Sprintf("unknown attribute %q", attr.Name))
			continue
		}
		if spec.FunctionOnly && declKind != "fn" {
			p.errorAt(attr.Location, fmt.Sprintf("#[%s] is only valid on function declarations", attr.Name))
			continue
		}
		if spec.MaxArgs >= 0 && len(attr.Args) > spec.MaxArgs {
			if spec.NoArgsMessage != "" {
				p.errorAt(attr.Location, spec.NoArgsMessage)
				continue
			}
			p.errorAt(attr.Location, fmt.Sprintf("#[%s] accepts at most one argument", attr.Name))
		}
	}
}

func (p *Parser) validateTypeParams(params []ast.TypeParam) {
	if len(params) == 0 {
		return
	}
	seen := make(map[string]source.Location, len(params))
	for _, param := range params {
		if param.Name == nil {
			continue
		}
		name := param.Name.Text()
		if prev, ok := seen[name]; ok {
			p.reportDuplicateDeclName(
				fmt.Sprintf("duplicate type parameter %q", name),
				param.Location,
				prev,
			)
		} else {
			seen[name] = param.Location
		}
		p.validateType(param.Constraint)
	}
}

func (p *Parser) validateStmt(stmt ast.Stmt) {
	if stmt == nil || (reflect.ValueOf(stmt).Kind() == reflect.Pointer && reflect.ValueOf(stmt).IsNil()) {
		return
	}
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
	case *ast.MatchStmt:
		p.validateExpr(s.Value)
		for _, arm := range s.Arms {
			if arm == nil {
				continue
			}
			if arm.TypePattern != nil {
				p.validateType(arm.TypePattern)
			} else if !arm.Wildcard {
				p.validateExpr(arm.Pattern)
			}
			p.validateStmt(arm.Body)
		}
	case *ast.WhileStmt:
		p.validateExpr(s.Cond)
		p.validateStmt(s.Body)
	case *ast.ForStmt:
		p.validateExpr(s.Iterable)
		p.validateStmt(s.Body)
	case *ast.LabelStmt:
		p.validateStmt(s.Stmt)
	case *ast.DeferStmt:
		p.validateStmt(s.Body)
	case *ast.ReleaseStmt:
		p.validateExpr(s.Value)
	case *ast.PanicStmt:
		if s.Value == nil {
			p.errorAt(s.Location, "panic requires a payload")
		}
		p.validateExpr(s.Value)
	case *ast.LockStmt:
		p.validateExpr(s.Value)
		p.validateStmt(s.Body)
	case *ast.UnsafeStmt:
		p.validateStmt(s.Body)
	}
}

func (p *Parser) validateExpr(expr ast.Expr) {
	if expr == nil || (reflect.ValueOf(expr).Kind() == reflect.Pointer && reflect.ValueOf(expr).IsNil()) {
		return
	}
	switch e := expr.(type) {
	case *ast.Ident:
		for _, typeArg := range e.TypeArgs {
			p.validateType(typeArg)
		}
	case *ast.PrefixExpr:
		p.validateExpr(e.Right)
	case *ast.BinaryExpr:
		p.validateExpr(e.Left)
		p.validateExpr(e.Right)
	case *ast.RangeExpr:
		p.validateExpr(e.Start)
		p.validateExpr(e.End)
		p.validateExpr(e.Step)
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
	case *ast.CastExpr:
		p.validateExpr(e.Left)
		p.validateType(e.Type)
	case *ast.IsExpr:
		p.validateExpr(e.Left)
		p.validateType(e.Type)
	case *ast.MatchExpr:
		p.validateExpr(e.Value)
		for _, arm := range e.Arms {
			if arm == nil {
				continue
			}
			if arm.TypePattern != nil {
				p.validateType(arm.TypePattern)
			} else if !arm.Wildcard {
				p.validateExpr(arm.Pattern)
			}
			p.validateStmt(arm.Body)
		}
	case *ast.CatchExpr:
		p.validateExpr(e.Left)
		p.validateExpr(e.Fallback)
		p.validateStmt(e.Handler)
	case *ast.CompositeLit:
		p.validateCompositeLit(e)
	case *ast.IndexExpr:
		p.validateExpr(e.Left)
		p.validateExpr(e.Index)
	}
}

func (p *Parser) validateType(typ ast.TypeExpr) {
	ast.WalkType(typ, func(current ast.TypeExpr) bool {
		switch t := current.(type) {
		case *ast.ArrayType:
			if ident, ok := t.Size.(*ast.Ident); !ok || ident.Text() != "_" {
				p.validateExpr(t.Size)
			}
		case *ast.MapType:
			p.validateType(t.Key)
			p.validateType(t.Value)
		case *ast.StructType:
			seenFields := make(map[string]source.Location)
			for _, field := range t.Fields {
				if field == nil {
					continue
				}
				name := field.Name.Text()
				if prev, ok := seenFields[name]; ok {
					p.reportDuplicateDeclName(
						fmt.Sprintf("duplicate field %q", name),
						field.Location,
						prev,
					)
				} else {
					seenFields[name] = field.Location
				}
				p.validateExpr(field.Default)
			}
		case *ast.InterfaceType:
			seen := make(map[string]*ast.InterfaceMethod)
			for _, method := range t.Methods {
				if method == nil {
					continue
				}
				name := method.Name.Text()
				if prev, ok := seen[name]; ok {
					p.reportDuplicateDeclName(
						fmt.Sprintf("duplicate interface method %q", name),
						method.Location,
						prev.Location,
					)
				} else {
					seen[name] = method
				}
				for _, param := range method.Params {
					if param.Default != nil {
						p.errorAt(param.Location, "interface method parameters cannot have default values")
					}
				}
			}
		case *ast.EnumType:
			seen := make(map[string]*ast.EnumVariant)
			for _, variant := range t.Variants {
				if variant == nil {
					continue
				}
				name := variant.Name.Text()
				if prev, ok := seen[name]; ok {
					p.reportDuplicateDeclName(
						fmt.Sprintf("duplicate enum variant %q", name),
						variant.Location,
						prev.Location,
					)
				} else {
					seen[name] = variant
				}
			}
		case *ast.ErrorType:
			seen := make(map[string]*ast.ErrorMember)
			for _, member := range t.Members {
				if member == nil {
					continue
				}
				name := member.Name.Text()
				if prev, ok := seen[name]; ok {
					p.reportDuplicateDeclName(
						fmt.Sprintf("duplicate error member %q", name),
						member.Location,
						prev.Location,
					)
				} else {
					seen[name] = member
				}
			}
		case *ast.UnionType:
			seen := make(map[string]ast.TypeExpr)
			for _, member := range t.Members {
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
		return true
	})
}

func (p *Parser) validateCompositeLit(lit *ast.CompositeLit) {
	if lit == nil {
		return
	}
	seenNamed := false
	seenKeyed := false
	seenPositional := false
	seenFields := make(map[string]ast.CompositeItem)
	for _, item := range lit.Items {
		p.validateExpr(item.Key)
		p.validateExpr(item.Value)
		if item.Key != nil {
			seenKeyed = true
			continue
		}
		if item.Name == nil {
			seenPositional = true
			continue
		}
		seenNamed = true
		name := item.Name.Text()
		if prev, ok := seenFields[name]; ok {
			p.reportDuplicateDeclName(
				fmt.Sprintf("duplicate composite field %q", name),
				item.Value.Loc(),
				prev.Value.Loc(),
			)
		} else {
			seenFields[name] = item
		}
	}
	if seenNamed && seenPositional {
		p.diag.Add(
			diagnostics.NewError("cannot mix named and positional composite elements").
				WithCode(diagnostics.ErrInvalidExpression).
				WithPrimaryLabel(&lit.Location, "use either all named fields or all positional values"),
		)
	}
	if seenKeyed && seenNamed {
		p.diag.Add(
			diagnostics.NewError("cannot mix keyed and named composite elements").
				WithCode(diagnostics.ErrInvalidExpression).
				WithPrimaryLabel(&lit.Location, "use either map entries or named fields"),
		)
	}
	if seenKeyed && seenPositional {
		p.diag.Add(
			diagnostics.NewError("cannot mix keyed and positional composite elements").
				WithCode(diagnostics.ErrInvalidExpression).
				WithPrimaryLabel(&lit.Location, "use either map entries or positional values"),
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
		if len(t.TypeArgs) == 0 {
			return fmt.Sprintf("named:%v", t.Path)
		}
		args := make([]string, 0, len(t.TypeArgs))
		for _, arg := range t.TypeArgs {
			args = append(args, renderType(arg))
		}
		return fmt.Sprintf("named:%v<%s>", t.Path, strings.Join(args, ","))
	case *ast.PointerType:
		return fmt.Sprintf("ptr(%s)", renderType(t.Inner))
	case *ast.RefType:
		prefix := "&"
		if t.Mutable {
			prefix = "&mut "
		}
		return prefix + renderType(t.Inner)
	case *ast.AtomicType:
		return "atomic " + renderType(t.Inner)
	case *ast.RawPtrType:
		if t.Const {
			return "^const " + renderType(t.Inner)
		}
		return "^" + renderType(t.Inner)
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
	case *ast.CharLit:
		return e.Value
	case *ast.Ident:
		return fmt.Sprint(e.Path)
	default:
		return fmt.Sprintf("%T", expr)
	}
}
