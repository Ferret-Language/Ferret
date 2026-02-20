package parser

import (
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/source"
	"compiler/internal/tokens"
	"compiler/internal/utils"
	"fmt"
)

// parseType parses a type expression
func (p *Parser) parseType() ast.TypeNode {
	return p.parseTypeWithOptional()
}

func (p *Parser) parseTypeWithOptional() ast.TypeNode {
	var optionalStart *source.Position
	var optionalTokEnd *source.Position
	if p.match(tokens.QUESTION_TOKEN) {
		optTok := p.advance()
		optionalStart = &optTok.Start
		optionalTokEnd = &optTok.End
	}

	tok := p.peek()

	var t ast.TypeNode

	// Check for heap type #T
	if p.match(tokens.HASH_TOKEN) {
		hash := p.advance()
		baseType := p.parseTypeWithOptional()
		var endPos *source.Position
		if baseType != nil && baseType.Loc() != nil && baseType.Loc().End != nil {
			endPos = baseType.Loc().End
		} else {
			endPos = &hash.End
		}
		t = &ast.HeapType{
			Base:     baseType,
			Location: *source.NewLocation(&p.filepath, &hash.Start, endPos),
		}
	} else if p.match(tokens.BIT_AND_TOKEN) {
		// Check for reference type &T or &mut T
		ampersand := p.advance()
		isMutable := false

		// Check if followed by 'mut' keyword
		if p.match(tokens.MUT_TOKEN) {
			p.advance()
			isMutable = true
		}

		baseType := p.parseTypeWithOptional()
		// Handle case where baseType might be Invalid with potential nil location issues
		var endPos *source.Position
		if baseType != nil && baseType.Loc() != nil && baseType.Loc().End != nil {
			endPos = baseType.Loc().End
		} else {
			endPos = &ampersand.End
		}
		t = &ast.ReferenceType{
			Base:     baseType,
			Mutable:  isMutable,
			Location: *source.NewLocation(&p.filepath, &ampersand.Start, endPos),
		}
	} else {
		switch tok.Kind {
		case tokens.IDENTIFIER_TOKEN:
			// Type identifier - convert IdentifierExpr to support both Expr() and TypeExpr()
			ident := p.parseIdentifier()
			var base ast.TypeNode = ident
			// Check for scope resolution (module::Type)
			if p.match(tokens.SCOPE_TOKEN) {
				// IdentifierExpr implements both Expression and TypeNode
				base = p.parseScopeResolutionExpr(ident)
			}

			// Generic type application: Name<T1, T2>
			if p.match(tokens.LESS_TOKEN) {
				typeArgs := p.parseTypeArgs()
				end := p.previous().End
				base = &ast.AppliedType{
					Base:     base,
					Args:     typeArgs,
					Location: *source.NewLocation(&p.filepath, base.Loc().Start, &end),
				}
			}
			t = base

		case tokens.OPEN_BRACKET:
			t = p.parseArrayType()

		case tokens.STRUCT_TOKEN:
			t = p.parseStructType()

		case tokens.INTERFACE_TOKEN:
			t = p.parseInterfaceType()

		case tokens.ENUM_TOKEN:
			t = p.parseEnumType()

		case tokens.MAP_TOKEN:
			t = p.parseMapType()

		case tokens.UNION_TOKEN:
			t = p.parseUnionType()

		case tokens.FUNCTION_TOKEN:
			start := p.advance().Start
			t = p.parseFuncType(start)

		default:
			//p.error(fmt.Sprintf("expected type, got %s", tok.Value))
			p.diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("expected type, got %s", tok.Value)).
					WithCode(diagnostics.ErrMissingType).
					WithPrimaryLabel(source.NewLocation(&p.filepath, &tok.Start, &tok.End), fmt.Sprintf("expected type here, got `%s`", tok.Value)),
			)
			// Return a placeholder identifier type instead of recursing
			// This prevents stack overflow on invalid syntax
			return &ast.Invalid{
				Location: p.makeLocation(tok.Start),
			}
		}
	}

	// Apply prefix optional type ?T
	if optionalStart != nil && t != nil {
		endPos := optionalTokEnd
		if t.Loc() != nil && t.Loc().End != nil {
			endPos = t.Loc().End
		}
		t = &ast.OptionalType{
			Base:     t,
			Location: *source.NewLocation(&p.filepath, optionalStart, endPos),
		}
	}

	// Reject postfix optional type T?
	if p.match(tokens.QUESTION_TOKEN) {
		q := p.advance()
		loc := source.NewLocation(&p.filepath, &q.Start, &q.End)
		p.diagnostics.Add(
			diagnostics.NewError("postfix optional types are no longer supported").
				WithCode(diagnostics.ErrInvalidType).
				WithPrimaryLabel(loc, "use prefix optional syntax").
				WithHelp("write '?T' instead of 'T?'"),
		)
		if t != nil {
			endPos := &q.End
			startPos := &q.Start
			if t.Loc() != nil {
				if t.Loc().Start != nil {
					startPos = t.Loc().Start
				}
				if t.Loc().End != nil {
					endPos = &q.End
				}
			}
			t = &ast.OptionalType{
				Base:     t,
				Location: *source.NewLocation(&p.filepath, startPos, endPos),
			}
		}
	}

	// Check for result type: E ! T
	// Error type first, then success type (consistent with expr! syntax)
	// Both types are required, no optional syntax
	// Example: str ! i32 means "returns i32 on success, str on error"
	if p.match(tokens.NOT_TOKEN) {
		notToken := p.advance() // consume '!'

		// Success type is required after '!'
		if p.match(tokens.SEMICOLON_TOKEN, tokens.CLOSE_PAREN, tokens.COMMA_TOKEN, tokens.OPEN_CURLY) {
			loc := source.NewLocation(&p.filepath, &notToken.End, &notToken.End)
			p.diagnostics.Add(
				diagnostics.NewError("expected success type after '!'").
					WithPrimaryLabel(loc, ""),
			)
			return t
		}

		successType := p.parseType()
		if successType == nil {
			loc := source.NewLocation(&p.filepath, &notToken.End, &notToken.End)
			p.diagnostics.Add(
				diagnostics.NewError("expected success type after '!'").
					WithPrimaryLabel(loc, ""),
			)
			return t
		}

		var endPos *source.Position
		if successType.Loc() != nil {
			endPos = successType.Loc().End
		} else {
			endPos = &notToken.End
		}

		// t is the error type, successType is the success type
		// But AST expects Value (success) then Error, so we swap
		return &ast.ResultType{
			Error:    t,           // Error type (what was parsed first)
			Value:    successType, // Success type (what was parsed after !)
			Location: *source.NewLocation(&p.filepath, t.Loc().Start, endPos),
		}
	}

	return t
}

func (p *Parser) parseUnionType() *ast.UnionType {
	tok := p.expect(tokens.UNION_TOKEN)
	p.expect(tokens.OPEN_CURLY)

	variants := []ast.TypeNode{}

	for !(p.match(tokens.CLOSE_CURLY) || p.isAtEnd()) {
		typ := p.parseType()
		variants = append(variants, typ)

		if p.match(tokens.CLOSE_CURLY) {
			break
		}

		if p.checkTrailing(tokens.COMMA_TOKEN, tokens.CLOSE_CURLY, "union type") {
			p.advance() // skip the token
			break
		}

		p.expect(tokens.COMMA_TOKEN)
	}

	end := p.expect(tokens.CLOSE_CURLY)

	return &ast.UnionType{
		Variants: variants,
		Location: *source.NewLocation(&p.filepath, &tok.Start, &end.End),
	}
}

func (p *Parser) parseArrayType() *ast.ArrayType {

	tok := p.expect(tokens.OPEN_BRACKET)

	var size ast.Expression
	if !p.match(tokens.CLOSE_BRACKET) {
		size = p.parseExpr()
	}

	p.expect(tokens.CLOSE_BRACKET)

	elem := p.parseType()

	return &ast.ArrayType{
		Len:      size, // nil for dynamic arrays []T
		ElType:   elem,
		Location: *source.NewLocation(&p.filepath, &tok.Start, elem.Loc().End),
	}
}

func (p *Parser) parseStructType() *ast.StructType {

	tok := p.expect(tokens.STRUCT_TOKEN)
	p.expect(tokens.OPEN_CURLY)

	fields := []ast.Field{}

	for !(p.match(tokens.CLOSE_CURLY) || p.isAtEnd()) {
		// Error recovery: Check if we have a dot token
		if !p.match(tokens.DOT_TOKEN) {
			p.error(fmt.Sprintf("expected . for struct field, got %s", p.peek().Value))
			p.advance() // Advance to prevent infinite loop
			continue
		}

		p.expect(tokens.DOT_TOKEN)
		name := p.parseIdentifier()
		p.expect(tokens.COLON_TOKEN)
		typ := p.parseType()

		fields = append(fields, ast.Field{
			Name: name,
			Type: typ,
		})

		if p.match(tokens.CLOSE_CURLY) {
			break
		}

		if p.checkTrailing(tokens.COMMA_TOKEN, tokens.CLOSE_CURLY, "struct type") {
			p.advance() // skip the token
			break
		}

		p.expect(tokens.COMMA_TOKEN)
	}

	end := p.expect(tokens.CLOSE_CURLY)

	structType := &ast.StructType{
		Fields:   fields,
		ID:       utils.GenerateStructLitID(),
		Location: *source.NewLocation(&p.filepath, &tok.Start, &end.End),
	}

	return structType
}

func (p *Parser) parseFuncType(start source.Position) *ast.FuncType {

	p.expect(tokens.OPEN_PAREN)

	var params []ast.Field

	if p.match(tokens.CLOSE_PAREN) {
		p.advance()
	} else {
		params = parseParams(p)
	}

	var result ast.TypeNode
	if p.match(tokens.ARROW_TOKEN) {
		p.advance()
		result = p.parseType()
	}

	return &ast.FuncType{
		Params:   params,
		Result:   result,
		Location: p.makeLocation(start),
	}
}

func parseParams(p *Parser) []ast.Field {

	params := []ast.Field{}

	for !(p.match(tokens.CLOSE_PAREN) || p.isAtEnd()) {

		name := p.parseIdentifier()
		p.expect(tokens.COLON_TOKEN)

		// Check for variadic parameter (...)
		isVariadic := false
		if p.match(tokens.THREE_DOT_TOKEN) {
			isVariadic = true
			p.advance() // consume '...'
		}

		isMove := false

		typ := p.parseType()

		// Optional default value: name: T = expr
		var defaultValue ast.Expression
		if p.match(tokens.EQUALS_TOKEN) {
			p.advance() // consume '='
			defaultValue = p.parseExpr()
		}

		// Handle nil type
		var endPos *source.Position
		if defaultValue != nil && defaultValue.Loc() != nil {
			endPos = defaultValue.Loc().End
		} else if typ != nil && typ.Loc() != nil {
			endPos = typ.Loc().End
		} else {
			endPos = name.End
		}

		params = append(params, ast.Field{
			Name:       name,
			Type:       typ,
			IsVariadic: isVariadic,
			IsMove:     isMove,
			Default:    defaultValue,
			Location:   *source.NewLocation(&p.filepath, name.Start, endPos),
		})

		if p.match(tokens.CLOSE_PAREN) {
			break
		}

		if p.checkTrailing(tokens.COMMA_TOKEN, tokens.CLOSE_PAREN, "function parameters") {
			p.advance() // skip the token
			break
		}

		p.expect(tokens.COMMA_TOKEN)
	}

	p.expect(tokens.CLOSE_PAREN)

	return params
}

func (p *Parser) parseInterfaceType() *ast.InterfaceType {

	tok := p.advance()

	p.expect(tokens.OPEN_CURLY)

	methods := []ast.Field{}

	// Check for empty interface: interface{}
	if p.match(tokens.CLOSE_CURLY) {
		// Empty interface, no methods
		end := p.advance().End
		return &ast.InterfaceType{
			Methods:  methods,
			Location: *source.NewLocation(&p.filepath, &tok.Start, &end),
		}
	}

	for !(p.match(tokens.CLOSE_CURLY) || p.isAtEnd()) {

		// funcname (...) -> ...
		receiverMode := ast.InterfaceReceiverValue
		var methodStart *source.Position
		if p.match(tokens.BIT_AND_TOKEN) {
			amp := p.advance()
			methodStart = &amp.Start
			if p.match(tokens.MUT_TOKEN) {
				p.advance()
				receiverMode = ast.InterfaceReceiverMutRef
			} else {
				receiverMode = ast.InterfaceReceiverRef
			}
		} else if p.match(tokens.BIT_NOT_TOKEN) {
			any := p.advance()
			methodStart = &any.Start
			receiverMode = ast.InterfaceReceiverAny
		}

		name := p.parseIdentifier()

		// Check if parseIdentifier failed (Name is "<error>" and Location not set)
		if name.Name == "<error>" || name.Start == nil {
			// Parser already reported the error, skip to recovery point
			// Try to find next comma or closing brace
			for !p.isAtEnd() && !p.match(tokens.COMMA_TOKEN) && !p.match(tokens.CLOSE_CURLY) {
				p.advance()
			}
			if p.match(tokens.COMMA_TOKEN) {
				p.advance()
				continue
			}
			break
		}

		functype := p.parseFuncType(*name.Start)
		if methodStart == nil {
			methodStart = name.Start
		}

		methods = append(methods, ast.Field{
			Name:         name,
			Type:         functype,
			ReceiverMode: receiverMode,
			Location:     *source.NewLocation(&p.filepath, methodStart, functype.End),
		})

		if p.match(tokens.CLOSE_CURLY) {
			break
		}

		if p.checkTrailing(tokens.SEMICOLON_TOKEN, tokens.CLOSE_CURLY, "interface type") {
			p.advance() // skip the token
			break
		}

		p.expect(tokens.COMMA_TOKEN)
	}

	end := p.expectError(tokens.CLOSE_CURLY, fmt.Sprintf("expected '}' to close interface type, found %s", p.peek().Kind)).End

	return &ast.InterfaceType{
		Methods:  methods,
		ID:       utils.GenerateInterfaceLitID(),
		Location: *source.NewLocation(&p.filepath, &tok.Start, &end),
	}
}

func (p *Parser) parseEnumType() *ast.EnumType {

	tok := p.expect(tokens.ENUM_TOKEN)

	p.expect(tokens.OPEN_CURLY)

	fields := []ast.Field{}

	for !(p.match(tokens.CLOSE_CURLY) || p.isAtEnd()) {
		// Error recovery: Check if we have an identifier
		if !p.match(tokens.IDENTIFIER_TOKEN) {
			p.error(fmt.Sprintf("expected enum variant name, got %s", p.peek().Value))
			p.advance() // Advance to prevent infinite loop
			continue
		}

		name := p.parseIdentifier()
		field := ast.Field{
			Name: name,
		}

		// Check for optional value assignment (e.g., Red = 10)
		if p.match(tokens.EQUALS_TOKEN) {
			p.advance() // consume '='
			field.Value = p.parseExpr()
		}

		fields = append(fields, field)

		if p.match(tokens.CLOSE_CURLY) {
			break
		}

		// Check for trailing comma before closing brace
		if p.checkTrailing(tokens.COMMA_TOKEN, tokens.CLOSE_CURLY, "enum type") {
			p.advance() // skip the token
			break
		}

		p.expect(tokens.COMMA_TOKEN)
	}

	p.expectError(tokens.CLOSE_CURLY, fmt.Sprintf("expected '}' to close enum type, found %s", p.peek().Kind))

	return &ast.EnumType{
		Variants: fields,
		ID:       utils.GenerateEnumLitID(),
		Location: p.makeLocation(tok.Start),
	}
}

func (p *Parser) parseMapType() *ast.MapType {

	tok := p.advance()

	p.expect(tokens.OPEN_BRACKET)

	keyType := p.parseType()

	p.expect(tokens.CLOSE_BRACKET)

	valueType := p.parseType()

	return &ast.MapType{
		Key:      keyType,
		Value:    valueType,
		Location: p.makeLocation(tok.Start),
	}
}
