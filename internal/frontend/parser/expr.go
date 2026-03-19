package parser

import (
	"compiler/internal/core/source"
	"compiler/internal/frontend/ast"
	"compiler/internal/tokens"
)

const (
	_ int = iota
	precLowest
	precCatch
	precCoalesce
	precOr
	precAnd
	precEqual
	precCompare
	precSum
	precProduct
	precPrefix
	precPostfix
)

func (p *Parser) parseExpr(precedence int) ast.Expr {
	return p.parseExprUntil(precedence)
}

func (p *Parser) parseExprUntil(precedence int, stopKinds ...tokens.Kind) ast.Expr {
	left := p.parsePrefix()
	for !p.at(tokens.SEMICOLON) && !p.at(tokens.RBRACE) && !p.at(tokens.EOF) && !p.atAny(stopKinds...) && precedence < p.currentPrecedenceForExpr(left) {
		if p.compositeValueDepth > 0 && precedence == precLowest && p.atCompositeFieldBoundary() {
			return left
		}
		switch p.current().Kind {
		case tokens.LT:
			if p.hasGenericAngleCallAhead(left) {
				left = p.parseCallWithAngleTypeArgs(left)
			} else {
				left = p.parseBinary(left)
			}
		case tokens.PLUS, tokens.MINUS, tokens.ASTERISK, tokens.SLASH, tokens.PERCENT,
			tokens.EQ, tokens.NEQ, tokens.LE, tokens.GT, tokens.GE,
			tokens.ANDAND, tokens.OROR, tokens.QQ:
			left = p.parseBinary(left)
		case tokens.CATCH:
			left = p.parseCatch(left)
		case tokens.LPAREN:
			left = p.parseCall(left)
		case tokens.LBRACK:
			if p.hasGenericCallAhead() {
				left = p.parseCallWithTypeArgs(left)
			} else {
				left = p.parseIndexExpr(left)
			}
		case tokens.DOT:
			left = p.parseSelector(left)
		case tokens.BB:
			tok := p.advance()
			left = &ast.PostfixExpr{Left: left, Op: tok.Literal, Location: p.makeExprLoc(*left.Loc().Start)}
		case tokens.AS:
			left = p.parseCast(left)
		case tokens.IS:
			left = p.parseIs(left)
		default:
			return left
		}
	}
	return left
}

func (p *Parser) parsePrefix() ast.Expr {
	start := p.current().Start
	switch p.current().Kind {
	case tokens.IDENT:
		if p.current().Literal == "copy" {
			p.advance()
			return &ast.PrefixExpr{Op: "copy", Right: p.parseExpr(precPrefix), Location: p.locFrom(start)}
		}
		return &ast.Ident{Path: p.parseNamePath(), Location: p.locFrom(start)}
	case tokens.NUMBER:
		tok := p.advance()
		return &ast.NumberLit{Value: tok.Literal, Location: p.locFrom(start)}
	case tokens.STRING:
		tok := p.advance()
		return &ast.StringLit{Value: tok.Literal, Location: p.locFrom(start)}
	case tokens.NONE:
		p.advance()
		return &ast.NoneLit{Location: p.locFrom(start)}
	case tokens.LPAREN:
		p.advance()
		expr := p.parseExpr(precLowest)
		p.expect(tokens.RPAREN, "expected ')'")
		return expr
	case tokens.DOT:
		return p.parseCompositeLit()
	case tokens.LBRACK:
		return p.parseBracketCompositeLit()
	case tokens.MATCH:
		return p.parseMatchExpr()
	case tokens.AMP:
		p.advance()
		op := "&"
		if p.match(tokens.MUT) {
			op = "&mut"
		}
		return &ast.PrefixExpr{Op: op, Right: p.parseExpr(precPrefix), Location: p.locFrom(start)}
	case tokens.AT:
		p.advance()
		op := "@"
		if p.match(tokens.MUT) {
			op = "@mut"
		}
		return &ast.PrefixExpr{Op: op, Right: p.parseExpr(precPrefix), Location: p.locFrom(start)}
	case tokens.ASTERISK, tokens.MINUS, tokens.BANG, tokens.QUESTION:
		tok := p.advance()
		return &ast.PrefixExpr{Op: tok.Literal, Right: p.parseExpr(precPrefix), Location: p.locFrom(start)}
	case tokens.COMPTIME:
		p.advance()
		return &ast.PrefixExpr{Op: "comptime", Right: p.parseExpr(precPrefix), Location: p.locFrom(start)}
	default:
		loc := source.NewLocation(p.file, start, p.current().End)
		p.errorAt(loc, "expected expression")
		if p.recoveryBoundaryForExpr() {
			return &ast.BadExpr{Location: loc}
		}
		p.advance()
		return &ast.BadExpr{Location: p.locFrom(start)}
	}
}

// parseBracketCompositeLit parses typed bracket literals like:
//
//	[3]i32{1, 2, 3}
//	[_]i32{1, 2, 3}
//	[]i32{1, 2, 3}
func (p *Parser) parseBracketCompositeLit() ast.Expr {
	start := p.current().Start
	literalType := p.parseType()
	if !p.at(tokens.LBRACE) {
		loc := p.locFrom(start)
		p.errorAt(loc, "expected '{' after array or slice literal")
		return &ast.BadExpr{Location: loc}
	}
	p.advance()
	items := p.parseCompositeItems(tokens.RBRACE)
	return &ast.CompositeLit{Type: literalType, Items: items, Location: p.locFrom(start)}
}

func (p *Parser) parseCompositeLit() ast.Expr {
	start := p.expect(tokens.DOT, "expected '.'").Start
	var literalType ast.TypeExpr
	if !p.at(tokens.LBRACE) {
		typeStart := p.current().Start
		path := p.parseNamePath()
		named := &ast.NamedType{Path: path, Location: p.locFrom(typeStart)}
		if p.at(tokens.LT) {
			named.TypeArgs = p.parseAngleTypeArgs("type argument")
		}
		literalType = named
	}
	p.expect(tokens.LBRACE, "expected '{' after '.'")
	items := p.parseCompositeItems(tokens.RBRACE)
	return &ast.CompositeLit{Type: literalType, Items: items, Location: p.locFrom(start)}
}

func (p *Parser) parseCompositeItems(end tokens.Kind) []ast.CompositeItem {
	items := make([]ast.CompositeItem, 0)
	for !p.at(end) && !p.at(tokens.EOF) {
		if p.match(tokens.DOT) {
			nameTok := p.expectIdent("expected field name")
			p.expect(tokens.ASSIGN, "expected '='")
			p.compositeValueDepth++
			value := p.parseExpr(precLowest)
			p.compositeValueDepth--
			items = append(items, ast.CompositeItem{
				Name:  &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
				Value: value,
			})
		} else {
			p.compositeValueDepth++
			value := p.parseExpr(precLowest)
			p.compositeValueDepth--
			items = append(items, ast.CompositeItem{Value: value})
		}
		if !p.consumeExprListSeparator(end, "composite literal element") {
			break
		}
	}
	p.expect(end, "expected '}'")
	return items
}

func (p *Parser) atCompositeFieldBoundary() bool {
	return p.at(tokens.DOT) && p.peekN(1).Kind == tokens.IDENT && p.peekN(2).Kind == tokens.ASSIGN
}

func (p *Parser) parseBinary(left ast.Expr) ast.Expr {
	tok := p.advance()
	if !p.startsExpr() {
		loc := source.NewLocation(p.file, tok.Start, tok.End)
		p.errorAt(loc, "expected expression after '"+tok.Literal+"'")
		return &ast.BinaryExpr{
			Left:     left,
			Op:       tok.Literal,
			Right:    &ast.BadExpr{Location: loc},
			Location: p.makeExprLoc(*left.Loc().Start),
		}
	}
	prec := precedence(tok.Kind)
	right := p.parseExpr(prec)
	return &ast.BinaryExpr{Left: left, Op: tok.Literal, Right: right, Location: p.makeExprLoc(*left.Loc().Start)}
}

func (p *Parser) parseCall(left ast.Expr) ast.Expr {
	start := *left.Loc().Start
	args := p.parseArgList()
	return &ast.CallExpr{Callee: left, Args: args, Location: p.makeExprLoc(start)}
}

func (p *Parser) parseIndexExpr(left ast.Expr) ast.Expr {
	start := *left.Loc().Start
	p.expect(tokens.LBRACK, "expected '[' for index expression")
	index := p.parseExpr(precLowest)
	p.expect(tokens.RBRACK, "expected ']' after index")
	return &ast.IndexExpr{Left: left, Index: index, Location: p.makeExprLoc(start)}
}

func (p *Parser) parseCallWithTypeArgs(left ast.Expr) ast.Expr {
	start := *left.Loc().Start
	p.expect(tokens.LBRACK, "expected '['")
	typeArgs := make([]ast.TypeExpr, 0)
	for !p.at(tokens.RBRACK) && !p.at(tokens.EOF) {
		typeArgs = append(typeArgs, p.parseType())
		if !p.consumeTypeListSeparator(tokens.RBRACK, "type argument") {
			break
		}
	}
	p.expect(tokens.RBRACK, "expected ']' after type arguments")
	call, _ := p.parseCall(left).(*ast.CallExpr)
	call.TypeArgs = typeArgs
	call.Location = p.makeExprLoc(start)
	return call
}

func (p *Parser) parseCallWithAngleTypeArgs(left ast.Expr) ast.Expr {
	start := *left.Loc().Start
	typeArgs := p.parseAngleTypeArgs("type argument")
	call, _ := p.parseCall(left).(*ast.CallExpr)
	call.TypeArgs = typeArgs
	call.Location = p.makeExprLoc(start)
	return call
}

func (p *Parser) parseArgList() []ast.Expr {
	p.expect(tokens.LPAREN, "expected '('")
	args := make([]ast.Expr, 0)
	for !p.at(tokens.RPAREN) && !p.at(tokens.EOF) {
		args = append(args, p.parseExpr(precLowest))
		if !p.consumeExprListSeparator(tokens.RPAREN, "argument") {
			break
		}
	}
	p.expect(tokens.RPAREN, "expected ')'")
	return args
}

func (p *Parser) parseSelector(left ast.Expr) ast.Expr {
	start := *left.Loc().Start
	p.expect(tokens.DOT, "expected '.'")
	nameTok := p.expectIdent("expected selector name")
	return &ast.SelectorExpr{
		Left:     left,
		Name:     &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
		Location: p.makeExprLoc(start),
	}
}

func (p *Parser) parseCast(left ast.Expr) ast.Expr {
	start := *left.Loc().Start
	p.expect(tokens.AS, "expected 'as'")
	typ := p.parseType()
	return &ast.CastExpr{Left: left, Type: typ, Location: p.makeExprLoc(start)}
}

func (p *Parser) parseIs(left ast.Expr) ast.Expr {
	start := *left.Loc().Start
	p.expect(tokens.IS, "expected 'is'")
	typ := p.parseType()
	return &ast.IsExpr{Left: left, Type: typ, Location: p.makeExprLoc(start)}
}

func (p *Parser) parseMatchExpr() ast.Expr {
	start := p.advance().Start
	value := p.parseExpr(precLowest)
	arms := p.parseMatchArms()
	return &ast.MatchExpr{Value: value, Arms: arms, Location: p.locFrom(start)}
}

func (p *Parser) parseMatchArms() []*ast.MatchArm {
	p.expect(tokens.LBRACE, "expected '{'")
	arms := make([]*ast.MatchArm, 0)
	for !p.at(tokens.RBRACE) && !p.at(tokens.EOF) {
		armStart := p.current().Start
		var pattern ast.Expr
		var typePattern ast.TypeExpr
		wildcard := false
		if p.at(tokens.IDENT) && p.current().Literal == "_" {
			wildcard = true
			p.advance()
		} else if p.match(tokens.IS) {
			typePattern = p.parseType()
		} else {
			pattern = p.parseExprUntil(precLowest, tokens.FATARROW)
		}
		p.expect(tokens.FATARROW, "expected '=>' after match pattern")
		var body *ast.BlockStmt
		if p.at(tokens.LBRACE) {
			body = p.parseBlock()
		} else {
			bodyExpr := p.parseExpr(precLowest)
			body = &ast.BlockStmt{
				Stmts:    []ast.Stmt{&ast.ExprStmt{Value: bodyExpr, Location: bodyExpr.Loc()}},
				Location: bodyExpr.Loc(),
			}
		}
		arms = append(arms, &ast.MatchArm{
			Pattern:     pattern,
			TypePattern: typePattern,
			Wildcard:    wildcard,
			Body:        body,
			Location:    p.locFrom(armStart),
		})
	}
	p.expect(tokens.RBRACE, "expected '}'")
	return arms
}

func (p *Parser) currentPrecedence() int {
	return precedence(p.current().Kind)
}

func (p *Parser) currentPrecedenceForExpr(left ast.Expr) int {
	if p.at(tokens.LT) && p.hasGenericAngleCallAhead(left) {
		return precPostfix
	}
	return p.currentPrecedence()
}

func precedence(kind tokens.Kind) int {
	switch kind {
	case tokens.CATCH:
		return precCatch
	case tokens.QQ:
		return precCoalesce
	case tokens.OROR:
		return precOr
	case tokens.ANDAND:
		return precAnd
	case tokens.EQ, tokens.NEQ:
		return precEqual
	case tokens.LT, tokens.LE, tokens.GT, tokens.GE:
		return precCompare
	case tokens.PLUS, tokens.MINUS:
		return precSum
	case tokens.ASTERISK, tokens.SLASH, tokens.PERCENT:
		return precProduct
	case tokens.LPAREN, tokens.LBRACK, tokens.DOT, tokens.BB:
		return precPostfix
	case tokens.AS:
		return precPostfix
	case tokens.IS:
		return precCompare
	default:
		return precLowest
	}
}

func (p *Parser) startsExpr() bool {
	switch p.current().Kind {
	case tokens.IDENT, tokens.NUMBER, tokens.STRING, tokens.NONE,
		tokens.LPAREN, tokens.DOT, tokens.AMP, tokens.AT, tokens.ASTERISK,
		tokens.MINUS, tokens.BANG, tokens.QUESTION, tokens.COMPTIME, tokens.UNSAFE, tokens.MATCH:
		return true
	default:
		return false
	}
}

func (p *Parser) parseCatch(left ast.Expr) ast.Expr {
	start := *left.Loc().Start
	p.expect(tokens.CATCH, "expected 'catch'")
	if p.match(tokens.BAR) {
		nameTok := p.expectIdent("expected catch payload name")
		p.expect(tokens.BAR, "expected closing '|' after catch payload")
		handler := p.parseBlock()
		return &ast.CatchExpr{
			Left:     left,
			Payload:  &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
			Handler:  handler,
			Location: p.makeExprLoc(start),
		}
	}
	fallback := p.parseExpr(precCatch)
	return &ast.CatchExpr{
		Left:     left,
		Fallback: fallback,
		Location: p.makeExprLoc(start),
	}
}

func (p *Parser) recoveryBoundaryForExpr() bool {
	switch p.current().Kind {
	case tokens.EOF, tokens.SEMICOLON, tokens.RBRACE,
		tokens.LET, tokens.CONST, tokens.RETURN, tokens.IF, tokens.ELSE,
		tokens.MATCH, tokens.WHILE, tokens.FOR,
		tokens.BREAK, tokens.CONTINUE:
		return true
	default:
		return false
	}
}
