package parser

import (
	"compiler/internal/core/diagnostics"
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
	if p.match(tokens.MOVE) {
		p.errorHere("`type Name move ...` is no longer supported")
	}
	spec := p.parseTypeSpec()
	return &ast.TypeDecl{
		Name:     &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
		Attrs:    attrs,
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
	var owner *ast.NamedType
	isStaticMethod := false
	var params []ast.Param
	if p.match(tokens.LPAREN) {
		recv = p.parseReceiver()
		p.expect(tokens.RPAREN, "expected ')' after receiver")
	} else if p.at(tokens.IDENT) && p.peekN(1).Kind == tokens.DCOLON {
		owner = p.parseAttachedOwner()
	}
	isDestructor := p.match(tokens.TILDE)
	nameTok := p.expectIdent("expected function or method name")
	isConstructor := recv != nil && !isDestructor && receiverNamedType(recv.Type) == nameTok.Literal
	if owner != nil {
		recv, params, isStaticMethod = p.parseAttachedMethodParams(owner)
		isConstructor = recv != nil && !isDestructor && receiverNamedType(recv.Type) == nameTok.Literal
	} else {
		params = p.parseParams()
	}
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
		OwnerType:     owner,
		IsStatic:      isStaticMethod,
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

func (p *Parser) parseAttachedOwner() *ast.NamedType {
	start := p.current().Start
	path := []string{p.expectIdent("expected attached method owner type").Literal}
	for p.at(tokens.DCOLON) && p.peekN(1).Kind == tokens.IDENT && p.peekN(2).Kind == tokens.DCOLON {
		p.advance()
		path = append(path, p.expectIdent("expected attached method owner segment").Literal)
	}
	p.expect(tokens.DCOLON, "expected '::' after attached method owner")
	return &ast.NamedType{Path: path, Location: p.locFrom(start)}
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

func cloneNamedType(t *ast.NamedType) *ast.NamedType {
	if t == nil {
		return nil
	}
	path := make([]string, len(t.Path))
	copy(path, t.Path)
	return &ast.NamedType{Path: path, Location: t.Location}
}

func (p *Parser) parseAttachedMethodParams(owner *ast.NamedType) (*ast.Receiver, []ast.Param, bool) {
	p.expect(tokens.LPAREN, "expected '('")
	recv, isStatic := p.parseAttachedReceiver(owner)
	params := make([]ast.Param, 0)
	if recv != nil && p.at(tokens.COMMA) {
		p.advance()
	}
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
	return recv, params, isStatic
}

func (p *Parser) parseAttachedReceiver(owner *ast.NamedType) (*ast.Receiver, bool) {
	start := p.current().Start
	switch p.current().Kind {
	case tokens.RPAREN:
		return nil, true
	case tokens.AMP:
		p.advance()
		mutable := p.match(tokens.MUT)
		nameTok := p.expectIdent("expected receiver name")
		recv := &ast.Receiver{
			Name:     &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
			Type:     &ast.RefType{Mutable: mutable, Inner: cloneNamedType(owner), Location: p.locFrom(start)},
			Location: p.locFrom(start),
		}
		p.warnNonSelfReceiver(recv.Name)
		return recv, false
	case tokens.ASTERISK:
		p.advance()
		nameTok := p.expectIdent("expected receiver name")
		recv := &ast.Receiver{
			Name:     &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
			Type:     &ast.PointerType{Inner: cloneNamedType(owner), Location: p.locFrom(start)},
			Location: p.locFrom(start),
		}
		p.warnNonSelfReceiver(recv.Name)
		return recv, false
	case tokens.IDENT:
		if p.peekN(1).Kind != tokens.COMMA && p.peekN(1).Kind != tokens.RPAREN {
			return nil, true
		}
		nameTok := p.advance()
		recv := &ast.Receiver{
			Name:     &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
			Type:     cloneNamedType(owner),
			Location: p.locFrom(start),
		}
		p.warnNonSelfReceiver(recv.Name)
		return recv, false
	default:
		return nil, true
	}
}

func (p *Parser) warnNonSelfReceiver(name *ast.Ident) {
	if name == nil || name.Text() == "self" {
		return
	}
	loc := name.Loc()
	p.diag.Add(
		diagnostics.NewWarning("receiver parameter should be named `self`").
			WithCode(diagnostics.WarnNonSelfReceiverName).
			WithPrimaryLabel(&loc, "rename this receiver to `self` for consistency"),
	)
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
		receiver, params, isStatic := p.parseInterfaceMethodParams()
		var result ast.TypeExpr
		if p.startsType() {
			result = p.parseType()
		}
		p.match(tokens.SEMICOLON)
		methods = append(methods, &ast.InterfaceMethod{
			Receiver: receiver,
			Static:   isStatic,
			Name:     &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
			Params:   params,
			Result:   result,
			Location: p.locFrom(methodStart),
		})
	}
	p.expect(tokens.RBRACE, "expected '}'")
	return &ast.InterfaceType{Methods: methods, Location: p.locFrom(start)}
}

func (p *Parser) parseInterfaceMethodParams() (string, []ast.Param, bool) {
	p.expect(tokens.LPAREN, "expected '('")
	receiver := ""
	isStatic := true
	switch p.current().Kind {
	case tokens.AMP:
		isStatic = false
		p.advance()
		receiver = "&"
		if p.match(tokens.MUT) {
			receiver = "&mut "
		}
		nameTok := p.expectIdent("expected receiver name")
		p.warnNonSelfReceiver(&ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)})
		if p.at(tokens.COMMA) {
			p.advance()
		}
	case tokens.ASTERISK:
		isStatic = false
		p.advance()
		nameTok := p.expectIdent("expected receiver name")
		p.warnNonSelfReceiver(&ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)})
		receiver = "*"
		if p.at(tokens.COMMA) {
			p.advance()
		}
	case tokens.IDENT:
		if p.peekN(1).Kind == tokens.COMMA || p.peekN(1).Kind == tokens.RPAREN {
			isStatic = false
			nameTok := p.advance()
			p.warnNonSelfReceiver(&ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)})
			if p.at(tokens.COMMA) {
				p.advance()
			}
		}
	}
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
	return receiver, params, isStatic
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
