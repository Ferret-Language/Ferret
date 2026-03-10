package parser

import (
	"compiler/internal/frontend/ast"
	"compiler/internal/tokens"
)

func (p *Parser) parseBlock() *ast.BlockStmt {
	start := p.current().Start
	p.expect(tokens.LBRACE, "expected '{'")
	stmts := make([]ast.Stmt, 0)
	for !p.at(tokens.RBRACE) && !p.at(tokens.EOF) {
		stmt := p.parseStmt()
		if stmt != nil {
			stmts = append(stmts, stmt)
		}
		p.match(tokens.SEMICOLON)
	}
	p.expect(tokens.RBRACE, "expected '}'")
	return &ast.BlockStmt{Stmts: stmts, Location: p.locFrom(start)}
}

func (p *Parser) parseStmt() ast.Stmt {
	switch p.current().Kind {
	case tokens.LET:
		return p.parseLetStmt()
	case tokens.CONST:
		return p.parseConstStmt()
	case tokens.RETURN:
		return p.parseReturnStmt()
	case tokens.IF:
		return p.parseIfStmt()
	case tokens.SWITCH:
		return p.parseSwitchStmt()
	default:
		expr := p.parseExpr(precLowest)
		return &ast.ExprStmt{Value: expr, Location: expr.Loc()}
	}
}

func (p *Parser) parseLetStmt() ast.Stmt {
	start := p.advance().Start
	isMut := p.match(tokens.MUT)
	nameTok := p.expectIdent("expected variable name")
	var typ ast.TypeExpr
	if p.match(tokens.COLON) {
		typ = p.parseType()
	}
	var value ast.Expr
	if p.match(tokens.ASSIGN) {
		value = p.parseExpr(precLowest)
	}
	return &ast.LetStmt{Name: nameTok.Literal, IsMut: isMut, Type: typ, Value: value, Location: p.locFrom(start)}
}

func (p *Parser) parseConstStmt() ast.Stmt {
	start := p.advance().Start
	nameTok := p.expectIdent("expected constant name")
	var typ ast.TypeExpr
	if p.match(tokens.COLON) {
		typ = p.parseType()
	}
	var value ast.Expr
	if p.match(tokens.ASSIGN) {
		value = p.parseExpr(precLowest)
	}
	return &ast.ConstStmt{Name: nameTok.Literal, Type: typ, Value: value, Location: p.locFrom(start)}
}

func (p *Parser) parseReturnStmt() ast.Stmt {
	start := p.advance().Start
	var value ast.Expr
	if !p.at(tokens.SEMICOLON) && !p.at(tokens.RBRACE) && !p.at(tokens.EOF) {
		value = p.parseExpr(precLowest)
	}
	return &ast.ReturnStmt{Value: value, Location: p.locFrom(start)}
}

func (p *Parser) parseIfStmt() ast.Stmt {
	start := p.advance().Start
	cond := p.parseExpr(precLowest)
	thenBlock := p.parseBlock()
	var elseBlock *ast.BlockStmt
	if p.match(tokens.ELSE) {
		elseBlock = p.parseBlock()
	}
	return &ast.IfStmt{Cond: cond, Then: thenBlock, Else: elseBlock, Location: p.locFrom(start)}
}

func (p *Parser) parseSwitchStmt() ast.Stmt {
	start := p.advance().Start
	value := p.parseExpr(precLowest)
	p.expect(tokens.LBRACE, "expected '{'")
	cases := make([]*ast.SwitchCase, 0)
	for !p.at(tokens.RBRACE) && !p.at(tokens.EOF) {
		caseStart := p.expect(tokens.CASE, "expected 'case'").Start
		expr := p.parseExpr(precLowest)
		body := p.parseBlock()
		cases = append(cases, &ast.SwitchCase{Expr: expr, Body: body, Location: p.locFrom(caseStart)})
	}
	p.expect(tokens.RBRACE, "expected '}'")
	return &ast.SwitchStmt{Value: value, Cases: cases, Location: p.locFrom(start)}
}
