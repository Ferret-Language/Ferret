package parser

import (
	"compiler/internal/frontend/ast"
	"compiler/internal/tokens"
)

func (p *Parser) parseImportDecl() *ast.ImportDecl {
	start := p.expect(tokens.IMPORT, "expected 'import'").Start
	pathTok := p.current()
	var pathExpr ast.Expr
	switch pathTok.Kind {
	case tokens.STRING:
		pathExpr = &ast.StringLit{Value: p.advance().Literal, Location: p.locOfToken(pathTok)}
	case tokens.IDENT:
		pathStart := p.current().Start
		pathExpr = &ast.Ident{Path: p.parseNamePath(), Location: p.locFrom(pathStart)}
	default:
		p.errorHere("expected import path")
	}
	var aliasIdent *ast.Ident
	if p.match(tokens.AS) {
		aliasTok := p.expectIdent("expected import alias")
		aliasIdent = &ast.Ident{Path: []string{aliasTok.Literal}, Location: p.locOfToken(aliasTok)}
	}
	p.match(tokens.SEMICOLON)
	return &ast.ImportDecl{Path: pathExpr, Alias: aliasIdent, Location: p.locFrom(start)}
}

func (p *Parser) parseTypeDecl(attrs []ast.Attribute) ast.Decl {
	start := p.expect(tokens.TYPE, "expected 'type'").Start
	nameTok := p.expectIdent("expected type name")
	isMove := p.match(tokens.MOVE)
	spec := p.parseTypeSpec()
	return &ast.TypeDecl{
		Name:     &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
		Attrs:    attrs,
		IsMove:   isMove,
		Type:     spec,
		Location: p.locFrom(start),
	}
}

func (p *Parser) parseLetDecl(attrs []ast.Attribute) ast.Decl {
	start := p.expect(tokens.LET, "expected 'let'").Start
	isMut := p.match(tokens.MUT)
	nameTok := p.expectIdent("expected variable name")
	name := nameTok.Literal
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
		Name:     &ast.Ident{Path: []string{name}, Location: p.locOfToken(nameTok)},
		Attrs:    attrs,
		IsMut:    isMut,
		Type:     typ,
		Value:    value,
		Location: p.locFrom(start),
	}
}

func (p *Parser) parseConstDecl(attrs []ast.Attribute) ast.Decl {
	start := p.expect(tokens.CONST, "expected 'const'").Start
	nameTok := p.expectIdent("expected constant name")
	name := nameTok.Literal
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
		Name:     &ast.Ident{Path: []string{name}, Location: p.locOfToken(nameTok)},
		Attrs:    attrs,
		Type:     typ,
		Value:    value,
		Location: p.locFrom(start),
	}
}

func (p *Parser) parseFuncDecl(doc *ast.CommentGroup, attrs []ast.Attribute) ast.Decl {
	isUnsafe := p.match(tokens.UNSAFE)
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
	isBuiltin := hasAttr(attrs, "builtin")
	externName := externAttrName(attrs, nameTok.Literal)
	isExtern := externName != ""
	var body *ast.BlockStmt
	if p.at(tokens.LBRACE) {
		body = p.parseBlock()
	} else if isBuiltin || isExtern {
		p.match(tokens.SEMICOLON)
	} else {
		p.errorHere("expected function body")
	}
	return &ast.FuncDecl{
		Receiver:      recv,
		Name:          &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
		Doc:           doc,
		Attrs:         attrs,
		IsUnsafe:      isUnsafe,
		IsBuiltin:     isBuiltin,
		IsExtern:      isExtern,
		ExternName:    externName,
		IsConstructor: isConstructor,
		IsDestructor:  isDestructor,
		Params:        params,
		Result:        result,
		Body:          body,
		Location:      p.locFrom(start),
	}
}

func hasAttr(attrs []ast.Attribute, name string) bool {
	for _, attr := range attrs {
		if attr.Name == name {
			return true
		}
	}
	return false
}

func externAttrName(attrs []ast.Attribute, fallback string) string {
	for _, attr := range attrs {
		if attr.Name != "extern" {
			continue
		}
		if len(attr.Args) > 0 && attr.Args[0] != "" {
			return attr.Args[0]
		}
		return fallback
	}
	return ""
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
	case *ast.RefType:
		return receiverNamedType(t.Inner)
	case *ast.RawPtrType:
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
		Name:     &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
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
			Name:       &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
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
				Name:     &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
				Type:     fieldType,
				Default:  def,
				Location: p.locFrom(fieldStart),
			})
			continue
		}
		fields = append(fields, &ast.FieldDecl{
			Name:     &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
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
			Name:     &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
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
		nameTok := p.expectIdent("expected enum variant")
		variants = append(variants, &ast.EnumVariant{Name: &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)}, Location: p.locFrom(variantStart)})
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
		nameTok := p.expectIdent("expected error member")
		members = append(members, &ast.ErrorMember{Name: &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)}, Location: p.locFrom(memberStart)})
		if !p.consumeExprListSeparator(tokens.RBRACE, "error member") {
			break
		}
	}
	p.expect(tokens.RBRACE, "expected '}'")
	return &ast.ErrorType{Members: members, Location: p.locFrom(start)}
}
