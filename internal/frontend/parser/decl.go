package parser

import (
	"compiler/internal/frontend/ast"
	"compiler/internal/tokens"
)

func (p *Parser) parseImportDecl() *ast.ImportDecl {
	start := p.expect(tokens.IMPORT, "expected 'import'").Start
	pathTok := p.current()
	path := ""
	switch pathTok.Kind {
	case tokens.STRING:
		path = p.advance().Literal
	case tokens.IDENT:
		pathParts := p.parseNamePath()
		for i, part := range pathParts {
			if i > 0 {
				path += "::"
			}
			path += part
		}
	default:
		p.errorHere("expected import path")
	}
	alias := ""
	if p.match(tokens.AS) {
		alias = p.expectIdent("expected import alias").Literal
	}
	p.match(tokens.SEMICOLON)
	return &ast.ImportDecl{Path: path, Alias: alias, Location: p.locFrom(start)}
}

func (p *Parser) parseTypeDecl() ast.Decl {
	start := p.expect(tokens.TYPE, "expected 'type'").Start
	name := p.expectIdent("expected type name").Literal
	spec := p.parseTypeSpec()
	return &ast.TypeDecl{
		Name:     name,
		Type:     spec,
		Location: p.locFrom(start),
	}
}

func (p *Parser) parseLetDecl() ast.Decl {
	start := p.expect(tokens.LET, "expected 'let'").Start
	isMut := p.match(tokens.MUT)
	name := p.expectIdent("expected variable name").Literal
	var typ ast.TypeExpr
	if p.match(tokens.COLON) {
		typ = p.parseType()
	}
	var value ast.Expr
	if p.match(tokens.ASSIGN) {
		value = p.parseExpr(precLowest)
	}
	p.match(tokens.SEMICOLON)
	return &ast.LetDecl{
		Name:     name,
		IsMut:    isMut,
		Type:     typ,
		Value:    value,
		Location: p.locFrom(start),
	}
}

func (p *Parser) parseConstDecl() ast.Decl {
	start := p.expect(tokens.CONST, "expected 'const'").Start
	name := p.expectIdent("expected constant name").Literal
	var typ ast.TypeExpr
	if p.match(tokens.COLON) {
		typ = p.parseType()
	}
	var value ast.Expr
	if p.match(tokens.ASSIGN) {
		value = p.parseExpr(precLowest)
	}
	p.match(tokens.SEMICOLON)
	return &ast.ConstDecl{
		Name:     name,
		Type:     typ,
		Value:    value,
		Location: p.locFrom(start),
	}
}

func (p *Parser) parseFuncDecl() ast.Decl {
	start := p.expect(tokens.FN, "expected 'fn'").Start
	var recv *ast.Receiver
	if p.match(tokens.LPAREN) {
		recv = p.parseReceiver()
		p.expect(tokens.RPAREN, "expected ')' after receiver")
	}
	isDestructor := p.match(tokens.TILDE)
	nameTok := p.expectIdent("expected function or method name")
	isConstructor := recv != nil && !isDestructor && receiverNamedType(recv.Type) == nameTok.Literal
	params := p.parseParams()
	var result ast.TypeExpr
	if p.startsType() {
		result = p.parseType()
	}
	body := p.parseBlock()
	return &ast.FuncDecl{
		Receiver:      recv,
		Name:          nameTok.Literal,
		IsConstructor: isConstructor,
		IsDestructor:  isDestructor,
		Params:        params,
		Result:        result,
		Body:          body,
		Location:      p.locFrom(start),
	}
}

func receiverNamedType(typ ast.TypeExpr) string {
	switch t := typ.(type) {
	case *ast.NamedType:
		if len(t.Path) == 0 {
			return ""
		}
		return t.Path[len(t.Path)-1]
	case *ast.PointerType:
		return receiverNamedType(t.Inner)
	default:
		return ""
	}
}

func (p *Parser) parseReceiver() *ast.Receiver {
	start := p.current().Start
	nameTok := p.expectIdent("expected receiver name")
	recvType := p.parseType()
	return &ast.Receiver{
		Name:     nameTok.Literal,
		Type:     recvType,
		Location: p.locFrom(start),
	}
}

func (p *Parser) parseParams() []ast.Param {
	p.expect(tokens.LPAREN, "expected '('")
	params := make([]ast.Param, 0)
	for !p.at(tokens.RPAREN) && !p.at(tokens.EOF) {
		paramStart := p.current().Start
		isComptime := p.match(tokens.COMPTIME)
		nameTok := p.expectIdent("expected parameter name")
		paramType := p.parseType()
		params = append(params, ast.Param{
			Name:       nameTok.Literal,
			IsComptime: isComptime,
			Type:       paramType,
			Location:   p.locFrom(paramStart),
		})
		if !p.consumeExprListSeparator(tokens.RPAREN, "parameter") {
			break
		}
	}
	p.expect(tokens.RPAREN, "expected ')'")
	return params
}

func (p *Parser) parseTypeSpec() ast.TypeExpr {
	switch p.current().Kind {
	case tokens.STRUCT:
		return p.parseStructType()
	case tokens.INTERFACE:
		return p.parseInterfaceType()
	case tokens.ENUM:
		return p.parseEnumType()
	case tokens.UNION:
		return p.parseUnionType()
	case tokens.ERROR:
		return p.parseErrorType()
	default:
		return p.parseType()
	}
}

func (p *Parser) parseStructType() ast.TypeExpr {
	start := p.advance().Start
	p.expect(tokens.LBRACE, "expected '{'")
	fields := make([]*ast.FieldDecl, 0)
	staticFields := make([]*ast.StaticFieldDecl, 0)
	for !p.at(tokens.RBRACE) && !p.at(tokens.EOF) {
		if !p.at(tokens.STATIC) && !p.at(tokens.IDENT) {
			p.errorHere("expected struct field")
			p.synchronizeTypeBody(tokens.RBRACE)
			continue
		}
		fieldStart := p.current().Start
		isStatic := p.match(tokens.STATIC)
		nameTok := p.expectIdent("expected field name")
		fieldType := p.parseType()
		var def ast.Expr
		if p.match(tokens.ASSIGN) {
			def = p.parseExpr(precLowest)
		}
		p.match(tokens.SEMICOLON)
		if isStatic {
			staticFields = append(staticFields, &ast.StaticFieldDecl{
				Name:     nameTok.Literal,
				Type:     fieldType,
				Default:  def,
				Location: p.locFrom(fieldStart),
			})
			continue
		}
		fields = append(fields, &ast.FieldDecl{
			Name:     nameTok.Literal,
			Type:     fieldType,
			Default:  def,
			Location: p.locFrom(fieldStart),
		})
	}
	p.expect(tokens.RBRACE, "expected '}'")
	return &ast.StructType{Fields: fields, StaticFields: staticFields, Location: p.locFrom(start)}
}

func (p *Parser) parseInterfaceType() ast.TypeExpr {
	start := p.advance().Start
	p.expect(tokens.LBRACE, "expected '{'")
	methods := make([]*ast.InterfaceMethod, 0)
	for !p.at(tokens.RBRACE) && !p.at(tokens.EOF) {
		if !p.at(tokens.IDENT) {
			p.errorHere("expected interface method")
			p.synchronizeTypeBody(tokens.RBRACE)
			continue
		}
		methodStart := p.current().Start
		nameTok := p.expectIdent("expected interface method name")
		params := p.parseParams()
		var result ast.TypeExpr
		if p.startsType() {
			result = p.parseType()
		}
		p.match(tokens.SEMICOLON)
		methods = append(methods, &ast.InterfaceMethod{
			Name:     nameTok.Literal,
			Params:   params,
			Result:   result,
			Location: p.locFrom(methodStart),
		})
	}
	p.expect(tokens.RBRACE, "expected '}'")
	return &ast.InterfaceType{Methods: methods, Location: p.locFrom(start)}
}

func (p *Parser) synchronizeTypeBody(end tokens.Kind) {
	for !p.at(tokens.EOF) {
		if p.at(tokens.SEMICOLON) {
			p.advance()
			return
		}
		if p.at(end) {
			return
		}
		p.advance()
	}
}

func (p *Parser) parseEnumType() ast.TypeExpr {
	start := p.advance().Start
	p.expect(tokens.LBRACE, "expected '{'")
	variants := make([]*ast.EnumVariant, 0)
	for !p.at(tokens.RBRACE) && !p.at(tokens.EOF) {
		variantStart := p.current().Start
		name := p.expectIdent("expected enum variant").Literal
		variants = append(variants, &ast.EnumVariant{Name: name, Location: p.locFrom(variantStart)})
		if !p.consumeExprListSeparator(tokens.RBRACE, "enum variant") {
			break
		}
	}
	p.expect(tokens.RBRACE, "expected '}'")
	return &ast.EnumType{Variants: variants, Location: p.locFrom(start)}
}

func (p *Parser) parseUnionType() ast.TypeExpr {
	start := p.advance().Start
	p.expect(tokens.LBRACE, "expected '{'")
	members := make([]ast.TypeExpr, 0)
	for !p.at(tokens.RBRACE) && !p.at(tokens.EOF) {
		members = append(members, p.parseType())
		if !p.consumeTypeListSeparator(tokens.RBRACE, "union member") {
			break
		}
	}
	p.expect(tokens.RBRACE, "expected '}'")
	return &ast.UnionType{Members: members, Location: p.locFrom(start)}
}

func (p *Parser) parseErrorType() ast.TypeExpr {
	start := p.advance().Start
	p.expect(tokens.LBRACE, "expected '{'")
	members := make([]*ast.ErrorMember, 0)
	for !p.at(tokens.RBRACE) && !p.at(tokens.EOF) {
		memberStart := p.current().Start
		name := p.expectIdent("expected error member").Literal
		members = append(members, &ast.ErrorMember{Name: name, Location: p.locFrom(memberStart)})
		if !p.consumeExprListSeparator(tokens.RBRACE, "error member") {
			break
		}
	}
	p.expect(tokens.RBRACE, "expected '}'")
	return &ast.ErrorType{Members: members, Location: p.locFrom(start)}
}
