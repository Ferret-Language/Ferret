package parser

import (
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
	left := p.parsePrefix()
	for !p.at(tokens.SEMICOLON) && !p.at(tokens.RBRACE) && !p.at(tokens.EOF) && precedence < p.currentPrecedence() {
		switch p.current().Kind {
		case tokens.PLUS, tokens.MINUS, tokens.ASTERISK, tokens.SLASH, tokens.PERCENT,
			tokens.EQ, tokens.NEQ, tokens.LT, tokens.LE, tokens.GT, tokens.GE,
			tokens.ANDAND, tokens.OROR, tokens.QQ, tokens.CATCH:
			left = p.parseBinary(left)
		case tokens.LPAREN:
			left = p.parseCall(left)
		case tokens.LBRACK:
			left = p.parseCallWithTypeArgs(left)
		case tokens.DOT:
			left = p.parseSelector(left)
		case tokens.BB:
			tok := p.advance()
			left = &ast.PostfixExpr{Left: left, Op: tok.Literal, Location: p.makeExprLoc(*left.Loc().Start)}
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
	default:
		p.errorHere("expected expression")
		p.advance()
		return &ast.Ident{Path: []string{"<error>"}, Location: p.locFrom(start)}
	}
}

func (p *Parser) parseCompositeLit() ast.Expr {
	start := p.expect(tokens.DOT, "expected '.'").Start
	p.expect(tokens.LBRACE, "expected '{' after '.'")
	items := make([]ast.CompositeItem, 0)
	for !p.at(tokens.RBRACE) && !p.at(tokens.EOF) {
		if p.match(tokens.DOT) {
			name := p.expectIdent("expected field name").Literal
			p.expect(tokens.ASSIGN, "expected '='")
			items = append(items, ast.CompositeItem{Name: name, Value: p.parseExpr(precLowest)})
		} else {
			items = append(items, ast.CompositeItem{Value: p.parseExpr(precLowest)})
		}
		if !p.match(tokens.COMMA) {
			break
		}
	}
	p.expect(tokens.RBRACE, "expected '}'")
	return &ast.CompositeLit{Items: items, Location: p.locFrom(start)}
}

func (p *Parser) parseBinary(left ast.Expr) ast.Expr {
	tok := p.advance()
	prec := precedence(tok.Kind)
	right := p.parseExpr(prec)
	return &ast.BinaryExpr{Left: left, Op: tok.Literal, Right: right, Location: p.makeExprLoc(*left.Loc().Start)}
}

func (p *Parser) parseCall(left ast.Expr) ast.Expr {
	start := *left.Loc().Start
	args := p.parseArgList()
	return &ast.CallExpr{Callee: left, Args: args, Location: p.makeExprLoc(start)}
}

func (p *Parser) parseCallWithTypeArgs(left ast.Expr) ast.Expr {
	start := *left.Loc().Start
	p.expect(tokens.LBRACK, "expected '['")
	typeArgs := make([]ast.TypeExpr, 0)
	for !p.at(tokens.RBRACK) && !p.at(tokens.EOF) {
		typeArgs = append(typeArgs, p.parseType())
		if !p.match(tokens.COMMA) {
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
		if !p.match(tokens.COMMA) {
			break
		}
	}
	p.expect(tokens.RPAREN, "expected ')'")
	return args
}

func (p *Parser) parseSelector(left ast.Expr) ast.Expr {
	start := *left.Loc().Start
	p.expect(tokens.DOT, "expected '.'")
	name := p.expectIdent("expected selector name").Literal
	return &ast.SelectorExpr{Left: left, Name: name, Location: p.makeExprLoc(start)}
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
	default:
		return precLowest
	}
}
