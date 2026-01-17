package parser

import (
	"compiler/internal/frontend/ast"
	"compiler/internal/source"
	"compiler/internal/tokens"
)

// parseForStmt parses: for i in range { } or for i, v in range { }
// Iterator variables are always bound as new variables (like Rust), similar to function parameters
func (p *Parser) parseForStmt() *ast.ForStmt {
	start := p.expect(tokens.FOR_TOKEN).Start

	// Parse first iterator variable name (index or value)
	firstTok := p.expect(tokens.IDENTIFIER_TOKEN)

	// Check for comma (indicates index, value pair)
	var secondTok tokens.Token
	hasSecond := false
	if p.match(tokens.COMMA_TOKEN) {
		p.advance()
		secondTok = p.expect(tokens.IDENTIFIER_TOKEN)
		hasSecond = true
	}

	// Expect 'in' keyword
	p.expect(tokens.IN_TOKEN)

	// Parse range expression (e.g., 0..10 or 0..10:2)
	rangeExpr := p.parseRangeExpr()

	// Parse body
	body := p.parseBlock()

	// Always create VarDecl for iterator variables (always binds new variables, like function parameters)
	declItems := []ast.DeclItem{
		{
			Name:  &ast.IdentifierExpr{Name: firstTok.Value, Location: *source.NewLocation(&p.filepath, &firstTok.Start, &firstTok.End)},
			Type:  nil, // Type inferred from range
			Value: nil, // Value comes from range iteration
		},
	}
	if hasSecond {
		declItems = append(declItems, ast.DeclItem{
			Name:  &ast.IdentifierExpr{Name: secondTok.Value, Location: *source.NewLocation(&p.filepath, &secondTok.Start, &secondTok.End)},
			Type:  nil, // Type inferred from range element type
			Value: nil, // Value comes from range iteration
		})
	}
	iterator := &ast.VarDecl{
		Decls:    declItems,
		Location: *source.NewLocation(&p.filepath, &firstTok.Start, &firstTok.End),
	}

	return &ast.ForStmt{
		Iterator: iterator,
		Range:    rangeExpr,
		Body:     body,
		Location: *source.NewLocation(&p.filepath, &start, body.Location.End),
	}
}

// parseRangeExpr parses range expression in for loops
// It accepts any expression (range, identifier, array literal, etc.)
func (p *Parser) parseRangeExpr() ast.Expression {
	// Parse as a normal expression which will handle ranges, identifiers, etc.
	return p.parseExpr()
}

// parseWhileStmt parses: while cond { }
func (p *Parser) parseWhileStmt() *ast.WhileStmt {
	start := p.expect(tokens.WHILE_TOKEN).Start

	// Parse condition (required, but allow nil for error recovery).
	var cond ast.Expression
	if !p.match(tokens.OPEN_CURLY) {
		cond = p.parseExpr()
	}

	// Parse body
	body := p.parseBlock()

	return &ast.WhileStmt{
		Cond:     cond,
		Body:     body,
		Location: *source.NewLocation(&p.filepath, &start, body.Location.End),
	}
}

// parseMatchStmt: match expr { pattern => body, ... }
func (p *Parser) parseMatchStmt() *ast.MatchStmt {
	start := p.expect(tokens.MATCH_TOKEN).Start

	// Parse the expression to match
	expr := p.parseExpr()

	// Expect opening curly brace
	p.expect(tokens.OPEN_CURLY)

	// Parse cases
	cases := []*ast.CaseClause{}
	for !p.match(tokens.CLOSE_CURLY) && !p.isAtEnd() {
		caseStart := p.peek().Start

		// Parse pattern (can be expression, type check, range check, or underscore for default)
		var pattern ast.Expression
		if p.match(tokens.IDENTIFIER_TOKEN) && p.peek().Value == "_" {
			// Default case: _
			p.advance()
			pattern = nil // nil pattern indicates default case
		} else if p.match(tokens.IS_TOKEN) {
			// Type check pattern: is Type
			p.advance() // consume 'is'
			typeExpr := p.parseType()
			// Create a special marker expression for type checks
			pattern = &ast.TypeCheckPattern{
				Type:     typeExpr,
				Location: *source.NewLocation(&p.filepath, &caseStart, p.safeLoc(typeExpr).End),
			}
		} else if p.match(tokens.IN_TOKEN) {
			// Range check pattern: in range
			p.advance() // consume 'in'
			rangeExpr := p.parseExpr()
			// Create a special marker expression for range checks
			pattern = &ast.RangeCheckPattern{
				Range:    rangeExpr,
				Location: *source.NewLocation(&p.filepath, &caseStart, p.safeLoc(rangeExpr).End),
			}
		} else {
			// Regular pattern expression (value match)
			pattern = p.parseExpr()
		}

		// Expect fat arrow =>
		p.expect(tokens.FAT_ARROW_TOKEN)

		// Parse case body (can be a single expression/statement or a block)
		var body *ast.Block
		if p.match(tokens.OPEN_CURLY) {
			// Block body
			body = p.parseBlock()
		} else {
			// Single expression/statement body
			// First check if it's an assignment (has = before comma/close brace)
			// We need to peek ahead to see if there's an assignment
			var stmt ast.Node

			// Try to parse as assignment first
			lhs := p.parseExpr()
			if lhs == nil {
				// Error parsing expression, skip this case
				p.error("expected expression in match case body")
				// Try to recover by skipping to next case or closing brace
				for !p.match(tokens.COMMA_TOKEN, tokens.CLOSE_CURLY) && !p.isAtEnd() {
					p.advance()
				}
				continue
			}

			if p.match(tokens.EQUALS_TOKEN) {
				// It's an assignment statement
				p.advance() // consume =
				rhs := p.parseExpr()
				if rhs == nil {
					p.error("expected expression after '=' in assignment")
					// Try to recover
					for !p.match(tokens.COMMA_TOKEN, tokens.CLOSE_CURLY) && !p.isAtEnd() {
						p.advance()
					}
					continue
				}
				stmt = &ast.AssignStmt{
					Lhs:      lhs,
					Rhs:      rhs,
					Location: *source.NewLocation(&p.filepath, p.safeLoc(lhs).Start, p.safeLoc(rhs).End),
				}
			} else {
				// It's just an expression statement
				stmt = &ast.ExprStmt{
					X:        lhs,
					Location: *source.NewLocation(&p.filepath, p.safeLoc(lhs).Start, p.safeLoc(lhs).End),
				}
			}

			// Check if there's a semicolon (optional for match cases)
			if p.match(tokens.SEMICOLON_TOKEN) {
				p.advance()
			}

			// Wrap in a Block
			body = &ast.Block{
				Nodes:    []ast.Node{stmt},
				Location: *source.NewLocation(&p.filepath, p.safeLoc(stmt).Start, p.safeLoc(stmt).End),
			}
		}

		// Create case clause
		caseEnd := body.Location.End
		cases = append(cases, &ast.CaseClause{
			Pattern:  pattern,
			Body:     body,
			Location: *source.NewLocation(&p.filepath, &caseStart, caseEnd),
		})

		// Check for comma separator (optional, for readability)
		if p.match(tokens.COMMA_TOKEN) {
			p.advance()
		}
	}

	end := p.expect(tokens.CLOSE_CURLY).End

	return &ast.MatchStmt{
		Expr:     expr,
		Cases:    cases,
		Location: *source.NewLocation(&p.filepath, &start, &end),
	}
}
