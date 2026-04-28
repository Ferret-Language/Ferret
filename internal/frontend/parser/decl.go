package parser

import (
	"fmt"

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
		aliasTok := p.expect(tokens.IDENT, "expected import alias")
		aliasIdent = &ast.Ident{Path: []string{aliasTok.Literal}, Location: p.locOfToken(aliasTok)}
	}
	p.match(tokens.SEMICOLON)
	return &ast.ImportDecl{Path: pathExpr, Alias: aliasIdent, Location: p.locFrom(start)}
}

func (p *Parser) parseTypeDecl(doc *ast.CommentGroup, attrs []ast.Attribute) ast.Decl {
	start := p.expect(tokens.TYPE, "expected 'type'").Start
	nameTok := p.expect(tokens.IDENT, "expected type name")
	typeParams := p.parseTypeParams()
	spec := p.parseTypeSpec()
	return &ast.TypeDecl{
		Name:       &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
		TypeParams: typeParams,
		Doc:        doc,
		Attrs:      attrs,
		Type:       spec,
		Location:   p.locFrom(start),
	}
}

func (p *Parser) parseLetDecl(doc *ast.CommentGroup, attrs []ast.Attribute) ast.Decl {
	start := p.expect(tokens.LET, "expected 'let'").Start
	isAtomic := p.match(tokens.ATOMIC)
	isMut := p.match(tokens.MUT)
	if isAtomic && isMut {
		p.errorAt(p.locOfToken(p.previous()), "atomic bindings do not use 'mut'")
	}
	nameTok := p.expect(tokens.IDENT, "expected variable name")
	name := nameTok.Literal
	var typ ast.TypeExpr
	if p.match(tokens.COLON) {
		typ = p.parseType()
	}
	var value ast.Expr
	if p.match(tokens.ASSIGN) {
		value = p.parseExprUntil(precLowest)
	}
	p.match(tokens.SEMICOLON)
	return &ast.LetDecl{
		Name:     &ast.Ident{Path: []string{name}, Location: p.locOfToken(nameTok)},
		Doc:      doc,
		Attrs:    attrs,
		IsMut:    isMut,
		IsAtomic: isAtomic,
		Type:     typ,
		Value:    value,
		Location: p.locFrom(start),
	}
}

func (p *Parser) parseConstDecl(doc *ast.CommentGroup, attrs []ast.Attribute) ast.Decl {
	start := p.expect(tokens.CONST, "expected 'const'").Start
	nameTok := p.expect(tokens.IDENT, "expected constant name")
	name := nameTok.Literal
	var typ ast.TypeExpr
	if p.match(tokens.COLON) {
		typ = p.parseType()
	}
	var value ast.Expr
	if p.match(tokens.ASSIGN) {
		value = p.parseExprUntil(precLowest)
	}
	p.match(tokens.SEMICOLON)
	return &ast.ConstDecl{
		Name:     &ast.Ident{Path: []string{name}, Location: p.locOfToken(nameTok)},
		Doc:      doc,
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
		loc := p.locFrom(start)
		p.errorAt(loc, "legacy receiver syntax has been removed; use attached method form `fn Type::Method(...)`")
	} else if p.hasAttachedOwnerAhead() {
		owner = p.parseAttachedOwner()
	}
	nameTok := p.expect(tokens.IDENT, "expected function or method name")
	typeParams := p.parseTypeParams()
	if owner != nil {
		recv, params, isStaticMethod = p.parseAttachedMethodParams(owner)
	} else {
		params = p.parseParams()
	}
	var result ast.TypeExpr
	if p.match(tokens.ARROW) {
		result = p.parseType()
	} else if p.startsType() {
		loc := p.locOfToken(p.current())
		p.errorAt(loc, "expected '->' before function return type")
		result = p.parseType()
	}
	isExtern, externName := foreignAttr(attrs)
	var body *ast.BlockStmt
	if p.at(tokens.LBRACE) {
		body = p.parseBlock()
	} else if isExtern {
		p.match(tokens.SEMICOLON)
	} else {
		p.errorHere("expected function body")
	}
	return &ast.FuncDecl{
		Receiver:   recv,
		OwnerType:  owner,
		IsStatic:   isStaticMethod,
		Name:       &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
		TypeParams: typeParams,
		Doc:        doc,
		Attrs:      attrs,
		IsUnsafe:   isUnsafe,
		IsExtern:   isExtern,
		ExternName: externName,
		Params:     params,
		Result:     result,
		Body:       body,
		Location:   p.locFrom(start),
	}
}

func (p *Parser) parseTestDecl(doc *ast.CommentGroup, attrs []ast.Attribute) ast.Decl {
	start := p.expect(tokens.TEST, "expected 'test'").Start
	nameTok := p.expect(tokens.STRING, "expected test name string literal")
	body := p.parseBlock()
	internalName := fmt.Sprintf("__ferret_test_%d", p.testDeclIndex)
	p.testDeclIndex++
	return &ast.FuncDecl{
		Name:        &ast.Ident{Path: []string{internalName}, Location: p.locOfToken(nameTok)},
		IsTest:      true,
		TestName:    nameTok.Literal,
		Doc:         doc,
		Attrs:       attrs,
		Params:      nil,
		Result:      nil,
		Body:        body,
		Location:    p.locFrom(start),
		IsSynthetic: false,
	}
}

func (p *Parser) parseTypeParams() []ast.TypeParam {
	if !p.match(tokens.LT) {
		return nil
	}
	params := make([]ast.TypeParam, 0)
	for !p.at(tokens.GT) && !p.at(tokens.EOF) {
		start := p.current().Start
		nameTok := p.expect(tokens.IDENT, "expected type parameter name")
		var constraint ast.TypeExpr
		if p.match(tokens.COLON) {
			constraint = p.parseType()
		}
		params = append(params, ast.TypeParam{
			Name:       &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
			Constraint: constraint,
			Location:   p.locFrom(start),
		})
		if !p.consumeListSeparator(tokens.GT, "type parameter", p.at(tokens.IDENT)) {
			break
		}
	}
	p.expect(tokens.GT, "expected '>' after type parameters")
	return params
}

func (p *Parser) parseAttachedOwner() *ast.NamedType {
	start := p.current().Start
	path := []string{p.expect(tokens.IDENT, "expected attached method owner type").Literal}
	var typeArgs []ast.TypeExpr
	for p.at(tokens.DCOLON) && p.peekN(1).Kind == tokens.IDENT {
		scan := p.pos + 2
		if scan < len(p.toks) && p.toks[scan].Kind == tokens.LT {
			depth := 0
			for ; scan < len(p.toks); scan++ {
				switch p.toks[scan].Kind {
				case tokens.LT:
					depth++
				case tokens.GT:
					depth--
					if depth == 0 {
						scan++
						goto scannedOwner
					}
				}
			}
			break
		}
	scannedOwner:
		if scan >= len(p.toks) || p.toks[scan].Kind != tokens.DCOLON {
			break
		}
		p.advance()
		path = append(path, p.expect(tokens.IDENT, "expected attached method owner segment").Literal)
		if p.at(tokens.LT) {
			typeArgs = p.parseAngleTypeArgs("type argument")
		}
	}
	owner := &ast.NamedType{Path: path, TypeArgs: typeArgs, Location: p.locFrom(start)}
	if len(owner.TypeArgs) == 0 && p.at(tokens.LT) {
		owner.TypeArgs = p.parseAngleTypeArgs("type argument")
	}
	p.expect(tokens.DCOLON, "expected '::' after attached method owner")
	return owner
}

func (p *Parser) hasAttachedOwnerAhead() bool {
	if !p.at(tokens.IDENT) {
		return false
	}
	i := p.pos + 1
	if i >= len(p.toks) {
		return false
	}
	// Optional owner type arguments: Type<...>::Method
	if p.toks[i].Kind == tokens.LT {
		depth := 0
		for ; i < len(p.toks); i++ {
			switch p.toks[i].Kind {
			case tokens.LT:
				depth++
			case tokens.GT:
				depth--
				if depth == 0 {
					i++
					goto ownerEnd
				}
			}
		}
		return false
	}
ownerEnd:
	if i >= len(p.toks) || p.toks[i].Kind != tokens.DCOLON {
		return false
	}
	if i+1 < len(p.toks) && p.toks[i+1].Kind == tokens.TILDE {
		return true
	}
	if i+1 >= len(p.toks) || p.toks[i+1].Kind != tokens.IDENT {
		return false
	}
	// If the symbol after :: is followed by :: or <, it's still part of owner path.
	if i+2 < len(p.toks) {
		switch p.toks[i+2].Kind {
		case tokens.DCOLON, tokens.LT:
			return true
		}
	}
	return true
}

func foreignAttr(attrs []ast.Attribute) (bool, string) {
	seen := false
	linkName := ""
	for _, attr := range attrs {
		if attr.Name != "extern" && attr.Name != "builtin" {
			continue
		}
		seen = true
		if len(attr.Args) > 0 && attr.Args[0] != "" {
			linkName = attr.Args[0]
		}
	}
	return seen, linkName
}

func (p *Parser) parseReceiver() *ast.Receiver {
	start := p.current().Start
	nameTok := p.expect(tokens.IDENT, "expected receiver name")
	p.expect(tokens.COLON, "expected ':' after receiver name")
	recvType := p.parseType()
	return &ast.Receiver{
		Name:     &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
		Type:     recvType,
		Location: p.locFrom(start),
	}
}

func (p *Parser) parseParams() []ast.Param {
	p.expect(tokens.LPAREN, "expected '('")
	params := p.parseNamedParams()
	p.expect(tokens.RPAREN, "expected ')'")
	return params
}

func (p *Parser) parseParamType() (ast.TypeExpr, bool) {
	if !p.at(tokens.ELLIPSIS) {
		return p.parseType(), false
	}
	start := p.current().Start
	p.advance()
	if p.match(tokens.MUT) {
		p.errorAt(p.locOfToken(p.previous()), "variadic slice syntax is ...T; put mutability on the binding")
	}
	inner := p.parseType()
	return &ast.SliceType{Inner: inner, Location: p.locFrom(start)}, true
}

func (p *Parser) parseNamedParam() ast.Param {
	paramStart := p.current().Start
	if p.at(tokens.COMPTIME) {
		p.errorHere("expected parameter name")
		p.advance()
	}
	isMut := p.match(tokens.MUT)
	nameTok := p.expect(tokens.IDENT, "expected parameter name")
	var paramType ast.TypeExpr
	isVariadic := false
	if p.match(tokens.COLON) {
		paramType, isVariadic = p.parseParamType()
	}
	var def ast.Expr
	if p.match(tokens.ASSIGN) {
		if isVariadic {
			p.errorAt(p.locOfToken(p.previous()), "variadic parameter cannot have a default value")
		}
		def = p.parseExprUntil(precLowest, tokens.COMMA, tokens.RPAREN)
	} else if paramType == nil {
		p.errorAt(p.locOfToken(nameTok), "parameter must declare a type or default value")
	}
	return ast.Param{
		Name:       &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
		IsMut:      isMut,
		IsVariadic: isVariadic,
		Type:       paramType,
		Default:    def,
		Location:   p.locFrom(paramStart),
	}
}

func (p *Parser) parseNamedParams() []ast.Param {
	params := make([]ast.Param, 0)
	seenVariadic := false
	seenDefault := false
	for !p.at(tokens.RPAREN) && !p.at(tokens.EOF) {
		if seenVariadic {
			loc := p.locOfToken(p.current())
			p.errorAt(loc, "variadic parameter must be the last parameter")
		}
		param := p.parseNamedParam()
		params = append(params, param)
		if param.IsVariadic {
			seenVariadic = true
		}
		if param.Default != nil {
			seenDefault = true
		} else if seenDefault && !param.IsVariadic {
			p.errorAt(param.Location, "parameter without default cannot follow parameter with default value")
		}
		if !p.consumeListSeparator(tokens.RPAREN, "parameter", p.startsNamedParam()) {
			break
		}
	}
	return params
}

func cloneNamedType(t *ast.NamedType) *ast.NamedType {
	if t == nil {
		return nil
	}
	path := make([]string, len(t.Path))
	copy(path, t.Path)
	typeArgs := make([]ast.TypeExpr, 0, len(t.TypeArgs))
	typeArgs = append(typeArgs, t.TypeArgs...)
	return &ast.NamedType{Path: path, TypeArgs: typeArgs, Location: t.Location}
}

func (p *Parser) parseAttachedMethodParams(owner *ast.NamedType) (*ast.Receiver, []ast.Param, bool) {
	p.expect(tokens.LPAREN, "expected '('")
	recv, isStatic := p.parseAttachedReceiver(owner)
	if recv != nil && p.at(tokens.COMMA) {
		p.advance()
	}
	params := p.parseNamedParams()
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
		nameTok := p.expect(tokens.IDENT, "expected receiver name")
		recv := &ast.Receiver{
			Name:     &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
			Type:     &ast.RefType{Mutable: mutable, Inner: cloneNamedType(owner), Location: p.locFrom(start)},
			Location: p.locFrom(start),
		}
		p.warnNonSelfReceiver(recv.Name)
		return recv, false
	case tokens.ASTERISK:
		p.advance()
		nameTok := p.expect(tokens.IDENT, "expected receiver name")
		recv := &ast.Receiver{
			Name:     &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
			Type:     &ast.PointerType{Inner: cloneNamedType(owner), Location: p.locFrom(start)},
			Location: p.locFrom(start),
		}
		p.warnNonSelfReceiver(recv.Name)
		return recv, false
	case tokens.CARET:
		p.advance()
		isConst := p.match(tokens.CONST)
		nameTok := p.expect(tokens.IDENT, "expected receiver name")
		recv := &ast.Receiver{
			Name:     &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
			Type:     &ast.RawPtrType{Const: isConst, Inner: cloneNamedType(owner), Location: p.locFrom(start)},
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
	for !p.at(tokens.RBRACE) && !p.at(tokens.EOF) {
		fieldStart := p.current().Start
		if !p.at(tokens.IDENT) {
			p.errorHere("expected struct field")
			p.synchronizeTypeBody(tokens.RBRACE)
			continue
		}
		nameTok := p.expect(tokens.IDENT, "expected field name")
		p.expect(tokens.COLON, "expected ':' after field name")
		fieldType := p.parseType()
		var def ast.Expr
		if p.match(tokens.ASSIGN) {
			def = p.parseExprUntil(precLowest)
		}
		if p.match(tokens.COMMA) && p.at(tokens.RBRACE) {
			loc := p.locOfToken(p.previous())
			p.infoAt(loc, diagnostics.InfoTrailingComma, "trailing comma is unnecessary", "remove this trailing comma")
		}
		fields = append(fields, &ast.FieldDecl{
			Name:     &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
			Type:     fieldType,
			Default:  def,
			Location: p.locFrom(fieldStart),
		})
	}
	p.expect(tokens.RBRACE, "expected '}'")
	return &ast.StructType{Fields: fields, Location: p.locFrom(start)}
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
		nameTok := p.expect(tokens.IDENT, "expected interface method name")
		receiver, params, inferredStatic := p.parseInterfaceMethodParams()
		var result ast.TypeExpr
		if p.match(tokens.ARROW) {
			result = p.parseType()
		} else if p.startsType() {
			loc := p.locOfToken(p.current())
			p.errorAt(loc, "expected '->' before interface method return type")
			result = p.parseType()
		}
		if p.match(tokens.COMMA) && p.at(tokens.RBRACE) {
			loc := p.locOfToken(p.previous())
			p.infoAt(loc, diagnostics.InfoTrailingComma, "trailing comma is unnecessary", "remove this trailing comma")
		}
		methods = append(methods, &ast.InterfaceMethod{
			Receiver: receiver,
			Static:   inferredStatic,
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
		nameTok := p.expect(tokens.IDENT, "expected receiver name")
		p.warnNonSelfReceiver(&ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)})
		if p.at(tokens.COMMA) {
			p.advance()
		}
	case tokens.ASTERISK:
		isStatic = false
		p.advance()
		nameTok := p.expect(tokens.IDENT, "expected receiver name")
		p.warnNonSelfReceiver(&ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)})
		receiver = "*"
		if p.at(tokens.COMMA) {
			p.advance()
		}
	case tokens.CARET:
		isStatic = false
		p.advance()
		receiver = "^"
		if p.match(tokens.CONST) {
			receiver = "^const "
		}
		nameTok := p.expect(tokens.IDENT, "expected receiver name")
		p.warnNonSelfReceiver(&ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)})
		if p.at(tokens.COMMA) {
			p.advance()
		}
	case tokens.IDENT:
		if p.peekN(1).Kind == tokens.COMMA || p.peekN(1).Kind == tokens.RPAREN {
			isStatic = false
			nameTok := p.advance()
			p.warnNonSelfReceiver(&ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)})
			receiver = ""
			if p.at(tokens.COMMA) {
				p.advance()
			}
		}
	}
	params := make([]ast.Param, 0)
	seenVariadic := false
	for !p.at(tokens.RPAREN) && !p.at(tokens.EOF) {
		if seenVariadic {
			loc := p.locOfToken(p.current())
			p.errorAt(loc, "variadic parameter must be the last parameter")
		}
		paramStart := p.current().Start
		var (
			paramName  *ast.Ident
			paramType  ast.TypeExpr
			isVariadic bool
		)
		if p.at(tokens.IDENT) && p.peekN(1).Kind == tokens.COLON {
			nameTok := p.advance()
			p.expect(tokens.COLON, "expected ':' after parameter name")
			paramName = &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)}
			paramType, isVariadic = p.parseParamType()
		} else {
			paramType, isVariadic = p.parseParamType()
		}
		params = append(params, ast.Param{
			Name:       paramName,
			IsVariadic: isVariadic,
			Type:       paramType,
			Location:   p.locFrom(paramStart),
		})
		if isVariadic {
			seenVariadic = true
		}
		if !p.consumeListSeparator(tokens.RPAREN, "parameter", p.startsInterfaceParam()) {
			break
		}
	}
	p.expect(tokens.RPAREN, "expected ')'")
	return receiver, params, isStatic
}

func (p *Parser) synchronizeTypeBody(end tokens.Kind) {
	for !p.at(tokens.EOF) {
		if p.at(tokens.COMMA) {
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
		nameTok := p.expect(tokens.IDENT, "expected enum variant")
		variants = append(variants, &ast.EnumVariant{Name: &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)}, Location: p.locFrom(variantStart)})
		if !p.consumeListSeparator(tokens.RBRACE, "enum variant", p.startsExpr()) {
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
		if !p.consumeUnionMemberSeparator() {
			break
		}
	}
	p.expect(tokens.RBRACE, "expected '}'")
	return &ast.UnionType{Members: members, Location: p.locFrom(start)}
}

func (p *Parser) consumeUnionMemberSeparator() bool {
	if p.match(tokens.COMMA) {
		if p.at(tokens.RBRACE) {
			loc := p.locOfToken(p.previous())
			p.infoAt(loc, diagnostics.InfoTrailingComma, "trailing comma is unnecessary", "remove this trailing comma")
		}
		return true
	}
	if p.at(tokens.RBRACE) || p.at(tokens.EOF) {
		return false
	}
	// Ferret allows newline-separated union members without explicit separators.
	if p.startsType() {
		return true
	}
	tok := p.current()
	loc := p.locOfToken(tok)
	p.errorAt(loc, "expected ',' or '}' after union member")
	return !p.at(tokens.RBRACE) && !p.at(tokens.EOF)
}

func (p *Parser) parseErrorType() ast.TypeExpr {
	start := p.advance().Start
	p.expect(tokens.LBRACE, "expected '{'")
	members := make([]*ast.ErrorMember, 0)
	for !p.at(tokens.RBRACE) && !p.at(tokens.EOF) {
		memberStart := p.current().Start
		nameTok := p.expect(tokens.IDENT, "expected error member")
		members = append(members, &ast.ErrorMember{Name: &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)}, Location: p.locFrom(memberStart)})
		if !p.consumeListSeparator(tokens.RBRACE, "error member", p.startsExpr()) {
			break
		}
	}
	p.expect(tokens.RBRACE, "expected '}'")
	return &ast.ErrorType{Members: members, Location: p.locFrom(start)}
}
