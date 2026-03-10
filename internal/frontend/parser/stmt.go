package parser

import (
	"compiler/internal/frontend/ast"
	"compiler/internal/source"
	"compiler/internal/tokens"
)

func sourceSpan(left, right source.Location) source.Location {
	return source.NewLocation(left.File, *left.Start, *right.End)
}

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
	if p.at(tokens.IDENT) && p.peekN(1).Kind == tokens.COLON {
		return p.parseLabelStmt()
	}
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
	case tokens.WHILE:
		return p.parseWhileStmt()
	case tokens.FOR:
		return p.parseForStmt()
	case tokens.DEFER:
		return p.parseDeferStmt()
	case tokens.LOCK:
		return p.parseLockStmt()
	case tokens.UNSAFE:
		if p.peekN(1).Kind == tokens.LBRACE {
			return p.parseUnsafeStmt()
		}
	case tokens.BREAK:
		return p.parseBreakStmt()
	case tokens.CONTINUE:
		return p.parseContinueStmt()
	}

	startPos := p.pos
	left := p.parseExpr(precLowest)
	if p.match(tokens.ASSIGN) {
		right := p.parseExpr(precLowest)
		return &ast.AssignStmt{Left: left, Right: right, Location: sourceSpan(left.Loc(), right.Loc())}
	}
	if _, ok := left.(*ast.BadExpr); ok && p.pos == startPos {
		p.synchronizeStmt()
		return nil
	}
	return &ast.ExprStmt{Value: left, Location: left.Loc()}
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
	var elseStmt ast.Stmt
	if p.match(tokens.ELSE) {
		if p.at(tokens.IF) {
			elseStmt = p.parseIfStmt()
		} else {
			elseStmt = p.parseBlock()
		}
	}
	return &ast.IfStmt{Cond: cond, Then: thenBlock, Else: elseStmt, Location: p.locFrom(start)}
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

func (p *Parser) parseWhileStmt() ast.Stmt {
	start := p.advance().Start
	cond := p.parseExpr(precLowest)
	body := p.parseBlock()
	return &ast.WhileStmt{Cond: cond, Body: body, Location: p.locFrom(start)}
}

func (p *Parser) parseForStmt() ast.Stmt {
	start := p.advance().Start
	if p.at(tokens.LBRACE) {
		return &ast.ForStmt{Body: p.parseBlock(), Location: p.locFrom(start)}
	}

	first := p.parseForHeaderStmt()
	if p.match(tokens.SEMICOLON) {
		var cond ast.Expr
		if !p.at(tokens.SEMICOLON) && !p.at(tokens.LBRACE) && !p.at(tokens.EOF) {
			cond = p.parseExpr(precLowest)
		}
		p.expect(tokens.SEMICOLON, "expected ';' after for condition")
		var post ast.Stmt
		if !p.at(tokens.LBRACE) && !p.at(tokens.EOF) {
			post = p.parseForHeaderStmt()
		}
		body := p.parseBlock()
		return &ast.ForStmt{Init: first, Cond: cond, Post: post, Body: body, Location: p.locFrom(start)}
	}

	condExpr := p.extractForCondition(first)
	body := p.parseBlock()
	return &ast.ForStmt{Cond: condExpr, Body: body, Location: p.locFrom(start)}
}

func (p *Parser) parseDeferStmt() ast.Stmt {
	start := p.advance().Start
	if p.at(tokens.LBRACE) {
		return &ast.DeferStmt{Body: p.parseBlock(), Location: p.locFrom(start)}
	}
	expr := p.parseExpr(precLowest)
	return &ast.DeferStmt{
		Body:     &ast.ExprStmt{Value: expr, Location: expr.Loc()},
		Location: p.locFrom(start),
	}
}

func (p *Parser) parseLockStmt() ast.Stmt {
	start := p.advance().Start
	value := p.parseExpr(precLowest)
	p.expect(tokens.AS, "expected 'as' in lock statement")
	name := p.expectIdent("expected lock guard name").Literal
	body := p.parseBlock()
	return &ast.LockStmt{Value: value, Name: name, Body: body, Location: p.locFrom(start)}
}

func (p *Parser) parseUnsafeStmt() ast.Stmt {
	start := p.advance().Start
	body := p.parseBlock()
	return &ast.UnsafeStmt{Body: body, Location: p.locFrom(start)}
}

func (p *Parser) parseBreakStmt() ast.Stmt {
	start := p.advance().Start
	label := ""
	if p.at(tokens.IDENT) {
		label = p.advance().Literal
	}
	return &ast.BreakStmt{Label: label, Location: p.locFrom(start)}
}

func (p *Parser) parseContinueStmt() ast.Stmt {
	start := p.advance().Start
	label := ""
	if p.at(tokens.IDENT) {
		label = p.advance().Literal
	}
	return &ast.ContinueStmt{Label: label, Location: p.locFrom(start)}
}

func (p *Parser) parseForHeaderStmt() ast.Stmt {
	switch p.current().Kind {
	case tokens.LET:
		return p.parseLetStmt()
	case tokens.CONST:
		return p.parseConstStmt()
	default:
		left := p.parseExpr(precLowest)
		if p.match(tokens.ASSIGN) {
			right := p.parseExpr(precLowest)
			return &ast.AssignStmt{Left: left, Right: right, Location: sourceSpan(left.Loc(), right.Loc())}
		}
		return &ast.ExprStmt{Value: left, Location: left.Loc()}
	}
}

func (p *Parser) parseLabelStmt() ast.Stmt {
	start := p.current().Start
	name := p.expectIdent("expected label name").Literal
	p.expect(tokens.COLON, "expected ':' after label")
	stmt := p.parseStmt()
	return &ast.LabelStmt{Name: name, Stmt: stmt, Location: p.locFrom(start)}
}

func (p *Parser) extractForCondition(stmt ast.Stmt) ast.Expr {
	switch s := stmt.(type) {
	case nil:
		return nil
	case *ast.ExprStmt:
		return s.Value
	default:
		p.errorAt(s.Loc(), "expected for condition or ';' after for initializer")
		return &ast.BadExpr{Location: s.Loc()}
	}
}
