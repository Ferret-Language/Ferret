package parser

import (
	"compiler/internal/frontend/ast"
	"compiler/internal/source"
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
	for !p.at(tokens.SEMICOLON) && !p.at(tokens.RBRACE) && !p.at(tokens.EOF) && !p.atAny(stopKinds...) && precedence < p.currentPrecedence() {
		if p.compositeValueDepth > 0 && precedence == precLowest && p.atCompositeFieldBoundary() {
			return left
		}
		switch p.current().Kind {
		case tokens.PLUS, tokens.MINUS, tokens.ASTERISK, tokens.SLASH, tokens.PERCENT,
			tokens.EQ, tokens.NEQ, tokens.LT, tokens.LE, tokens.GT, tokens.GE,
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
		return p.parseArrayLit()
	case tokens.AMP:
		p.advance()
		op := "&"
		if p.match(tokens.MUT) {
			op = "&mut"
		}
		return &ast.PrefixExpr{Op: op, Right: p.parseExpr(precPrefix), Location: p.locFrom(start)}
	case tokens.ASTERISK, tokens.MINUS, tokens.BANG, tokens.QUESTION:
		tok := p.advance()
		return &ast.PrefixExpr{Op: tok.Literal, Right: p.parseExpr(precPrefix), Location: p.locFrom(start)}
	case tokens.TAKE:
		p.advance()
		return &ast.PrefixExpr{Op: "take", Right: p.parseExpr(precPrefix), Location: p.locFrom(start)}
	case tokens.COMPTIME:
		p.advance()
		return &ast.PrefixExpr{Op: "comptime", Right: p.parseExpr(precPrefix), Location: p.locFrom(start)}
	case tokens.UNSAFE:
		p.advance()
		inner := p.parseExpr(precPrefix)
		return &ast.UnsafeExpr{Inner: inner, Location: p.locFrom(start)}
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

// parseArrayLit parses [expr, expr, ...] as a positional CompositeLit.
func (p *Parser) parseArrayLit() ast.Expr {
	start := p.expect(tokens.LBRACK, "expected '['").Start
	items := make([]ast.CompositeItem, 0)
	for !p.at(tokens.RBRACK) && !p.at(tokens.EOF) {
		value := p.parseExpr(precLowest)
		items = append(items, ast.CompositeItem{Value: value})
		if !p.consumeExprListSeparator(tokens.RBRACK, "array literal element") {
			break
		}
	}
	p.expect(tokens.RBRACK, "expected ']'")
	return &ast.CompositeLit{Items: items, Location: p.locFrom(start)}
}

func (p *Parser) parseCompositeLit() ast.Expr {
	start := p.expect(tokens.DOT, "expected '.'").Start
	p.expect(tokens.LBRACE, "expected '{' after '.'")
	items := make([]ast.CompositeItem, 0)
	for !p.at(tokens.RBRACE) && !p.at(tokens.EOF) {
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
		if !p.consumeExprListSeparator(tokens.RBRACE, "composite literal element") {
			break
		}
	}
	p.expect(tokens.RBRACE, "expected '}'")
	return &ast.CompositeLit{Items: items, Location: p.locFrom(start)}
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

func (p *Parser) currentPrecedence() int {
	return precedence(p.current().Kind)
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
	default:
		return precLowest
	}
}

func (p *Parser) startsExpr() bool {
	switch p.current().Kind {
	case tokens.IDENT, tokens.NUMBER, tokens.STRING, tokens.NONE,
		tokens.LPAREN, tokens.DOT, tokens.AMP, tokens.ASTERISK,
		tokens.MINUS, tokens.BANG, tokens.QUESTION, tokens.TAKE, tokens.COMPTIME, tokens.UNSAFE:
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
