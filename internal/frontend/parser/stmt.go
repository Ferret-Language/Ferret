package parser

import (
	"compiler/internal/core/source"
	"compiler/internal/frontend/ast"
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
		if p.at(tokens.SEMICOLON) {
			p.consumeRedundantSemicolons(0)
			continue
		}
		stmt := p.parseStmt()
		if stmt != nil {
			stmts = append(stmts, stmt)
		}
		p.consumeRedundantSemicolons(1)
	}
	p.expect(tokens.RBRACE, "expected '}'")
	return &ast.BlockStmt{Stmts: stmts, Location: p.locFrom(start)}
}

func (p *Parser) parseStmt() ast.Stmt {
	doc := p.parseDocComment()
	if p.at(tokens.IDENT) && p.peekN(1).Kind == tokens.COLON {
		return p.parseLabelStmt()
	}
	switch p.current().Kind {
	case tokens.LBRACE:
		return p.parseBlock()
	case tokens.LET:
		return p.parseLetStmt(doc)
	case tokens.CONST:
		return p.parseConstStmt(doc)
	case tokens.RETURN:
		return p.parseReturnStmt()
	case tokens.IF:
		return p.parseIfStmt()
	case tokens.MATCH:
		expr := p.parseMatchExpr()
		return &ast.ExprStmt{Value: expr, Location: expr.Loc()}
	case tokens.WHILE:
		return p.parseWhileStmt()
	case tokens.FOR:
		return p.parseForStmt()
	case tokens.DEFER:
		return p.parseDeferStmt()
	case tokens.RELEASE:
		return p.parseReleaseStmt()
	case tokens.PANIC:
		return p.parsePanicStmt()
	case tokens.LOCK:
		return p.parseLockStmt()
	case tokens.UNSAFE:
		if p.peekN(1).Kind == tokens.LBRACE {
			return p.parseUnsafeStmt()
		}
	case tokens.COMPTIME:
		if p.peekN(1).Kind == tokens.LBRACE {
			return p.parseComptimeStmt()
		}
	case tokens.BREAK:
		return p.parseBreakStmt()
	case tokens.CONTINUE:
		return p.parseContinueStmt()
	}

	startPos := p.pos
	left := p.parseExprUntil(precLowest)
	if p.match(tokens.ASSIGN) {
		right := p.parseExprUntil(precLowest)
		return &ast.AssignStmt{Left: left, Right: right, Location: sourceSpan(left.Loc(), right.Loc())}
	}
	// Compound assignment: desugar x += y  →  x = x + y
	if op, ok := compoundAssignOp(p.current().Kind); ok {
		p.advance()
		right := p.parseExprUntil(precLowest)
		loc := sourceSpan(left.Loc(), right.Loc())
		rhs := &ast.BinaryExpr{Left: left, Op: op, Right: right, Location: loc}
		return &ast.AssignStmt{Left: left, Right: rhs, Location: loc}
	}
	// Increment / decrement: desugar x++  →  x = x + 1
	if p.at(tokens.PLUS_PLUS) || p.at(tokens.MINUS_MINUS) {
		op := "+"
		if p.at(tokens.MINUS_MINUS) {
			op = "-"
		}
		tokLoc := p.locOfToken(p.current())
		p.advance()
		one := &ast.NumberLit{Value: "1", Location: tokLoc}
		rhs := &ast.BinaryExpr{Left: left, Op: op, Right: one, Location: tokLoc}
		return &ast.AssignStmt{Left: left, Right: rhs, Location: sourceSpan(left.Loc(), tokLoc)}
	}
	if _, ok := left.(*ast.BadExpr); ok && p.pos == startPos {
		p.synchronizeStmt()
		return nil
	}
	return &ast.ExprStmt{Value: left, Location: left.Loc()}
}

func (p *Parser) parseLetStmt(doc *ast.CommentGroup) ast.Stmt {
	start := p.advance().Start
	isMut := p.match(tokens.MUT)
	nameTok := p.expect(tokens.IDENT, "expected variable name")
	var typ ast.TypeExpr
	if p.match(tokens.COLON) {
		typ = p.parseType()
	}
	var value ast.Expr
	if p.match(tokens.ASSIGN) {
		value = p.parseExprUntil(precLowest)
	}
	return &ast.LetStmt{
		Name:     &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
		Doc:      doc,
		IsMut:    isMut,
		Type:     typ,
		Value:    value,
		Location: p.locFrom(start),
	}
}

func (p *Parser) parseConstStmt(doc *ast.CommentGroup) ast.Stmt {
	start := p.advance().Start
	nameTok := p.expect(tokens.IDENT, "expected constant name")
	var typ ast.TypeExpr
	if p.match(tokens.COLON) {
		typ = p.parseType()
	}
	var value ast.Expr
	if p.match(tokens.ASSIGN) {
		value = p.parseExprUntil(precLowest)
	}
	return &ast.ConstStmt{
		Name:     &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
		Doc:      doc,
		Type:     typ,
		Value:    value,
		Location: p.locFrom(start),
	}
}

func (p *Parser) parseReturnStmt() ast.Stmt {
	start := p.advance().Start
	var value ast.Expr
	if !p.at(tokens.SEMICOLON) && !p.at(tokens.RBRACE) && !p.at(tokens.EOF) {
		value = p.parseExprUntil(precLowest)
	}
	return &ast.ReturnStmt{Value: value, Location: p.locFrom(start)}
}

func (p *Parser) parseIfStmt() ast.Stmt {
	start := p.advance().Start
	cond := p.parseExprUntil(precLowest)
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

func (p *Parser) parseWhileStmt() ast.Stmt {
	start := p.advance().Start
	cond := p.parseExprUntil(precLowest)
	body := p.parseBlock()
	return &ast.WhileStmt{Cond: cond, Body: body, Location: p.locFrom(start)}
}

func (p *Parser) parseForStmt() ast.Stmt {
	start := p.advance().Start
	iterable := p.parseExprUntil(precLowest, tokens.BAR)
	p.expect(tokens.BAR, "expected '|' after for iterable")
	firstTok := p.expect(tokens.IDENT, "expected loop binding name")
	valueIdent := &ast.Ident{Path: []string{firstTok.Literal}, Location: p.locOfToken(firstTok)}
	var indexIdent *ast.Ident
	if p.match(tokens.COMMA) {
		indexIdent = valueIdent
		valueTok := p.expect(tokens.IDENT, "expected loop value binding name")
		valueIdent = &ast.Ident{Path: []string{valueTok.Literal}, Location: p.locOfToken(valueTok)}
	}
	p.expect(tokens.BAR, "expected closing '|' after loop bindings")
	body := p.parseBlock()
	return &ast.ForStmt{Iterable: iterable, Index: indexIdent, Value: valueIdent, Body: body, Location: p.locFrom(start)}
}

func (p *Parser) parseDeferStmt() ast.Stmt {
	start := p.advance().Start
	var body ast.Stmt
	if p.at(tokens.LBRACE) {
		body = p.parseBlock()
	} else {
		body = p.parseStmt()
	}
	return &ast.DeferStmt{
		Body:     body,
		Location: p.locFrom(start),
	}
}

func (p *Parser) parseReleaseStmt() ast.Stmt {
	start := p.advance().Start
	value := p.parseExprUntil(precLowest)
	return &ast.ReleaseStmt{Value: value, Location: p.locFrom(start)}
}

func (p *Parser) parsePanicStmt() ast.Stmt {
	start := p.advance().Start
	var value ast.Expr
	if p.match(tokens.LPAREN) {
		p.errorAt(p.locFrom(start), "panic payload must not use parentheses")
		if !p.at(tokens.RPAREN) && !p.at(tokens.EOF) {
			value = p.parseExprUntil(precLowest)
		}
		p.expect(tokens.RPAREN, "expected ')' after panic payload")
	} else {
		value = p.parseExprUntil(precLowest)
	}
	if value == nil {
		p.errorAt(p.locFrom(start), "panic requires a payload")
	}
	return &ast.PanicStmt{Value: value, Location: p.locFrom(start)}
}

func (p *Parser) parseLockStmt() ast.Stmt {
	start := p.advance().Start
	value := p.parseExprUntil(precLowest, tokens.AS)
	p.expect(tokens.AS, "expected 'as' in lock statement")
	nameTok := p.expect(tokens.IDENT, "expected lock guard name")
	body := p.parseBlock()
	return &ast.LockStmt{
		Value:    value,
		Name:     &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
		Body:     body,
		Location: p.locFrom(start),
	}
}

func (p *Parser) parseUnsafeStmt() ast.Stmt {
	start := p.advance().Start
	body := p.parseBlock()
	return &ast.UnsafeStmt{Body: body, Location: p.locFrom(start)}
}

func (p *Parser) parseComptimeStmt() ast.Stmt {
	start := p.advance().Start
	body := p.parseBlock()
	var mark func(ast.Stmt) ast.Stmt
	mark = func(stmt ast.Stmt) ast.Stmt {
		switch s := stmt.(type) {
		case nil:
			return nil
		case *ast.ExprStmt:
			return s
		case *ast.BlockStmt:
			s.Comptime = true
			for i, child := range s.Stmts {
				s.Stmts[i] = mark(child)
			}
			return s
		default:
			loc := s.Loc()
			p.errorAt(loc, "comptime block currently supports expression statements only")
			return s
		}
	}
	body.Comptime = true
	body.Location = p.locFrom(start)
	for i, stmt := range body.Stmts {
		body.Stmts[i] = mark(stmt)
	}
	return body
}

func (p *Parser) parseBreakStmt() ast.Stmt {
	start := p.advance().Start
	var label *ast.Ident
	if p.at(tokens.IDENT) {
		labelTok := p.advance()
		label = &ast.Ident{Path: []string{labelTok.Literal}, Location: p.locOfToken(labelTok)}
	}
	return &ast.BreakStmt{Label: label, Location: p.locFrom(start)}
}

func (p *Parser) parseContinueStmt() ast.Stmt {
	start := p.advance().Start
	var label *ast.Ident
	if p.at(tokens.IDENT) {
		labelTok := p.advance()
		label = &ast.Ident{Path: []string{labelTok.Literal}, Location: p.locOfToken(labelTok)}
	}
	return &ast.ContinueStmt{Label: label, Location: p.locFrom(start)}
}

func (p *Parser) parseLabelStmt() ast.Stmt {
	start := p.current().Start
	nameTok := p.expect(tokens.IDENT, "expected label name")
	p.expect(tokens.COLON, "expected ':' after label")
	stmt := p.parseStmt()
	return &ast.LabelStmt{
		Name:     &ast.Ident{Path: []string{nameTok.Literal}, Location: p.locOfToken(nameTok)},
		Stmt:     stmt,
		Location: p.locFrom(start),
	}
}

// compoundAssignOp maps a compound-assignment token to its binary operator.
func compoundAssignOp(k tokens.Kind) (string, bool) {
	switch k {
	case tokens.PLUS_ASSIGN:
		return "+", true
	case tokens.MINUS_ASSIGN:
		return "-", true
	case tokens.STAR_ASSIGN:
		return "*", true
	case tokens.SLASH_ASSIGN:
		return "/", true
	case tokens.PCT_ASSIGN:
		return "%", true
	}
	return "", false
}
