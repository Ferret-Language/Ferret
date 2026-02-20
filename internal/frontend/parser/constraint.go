package parser

import (
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/source"
	"compiler/internal/tokens"
	"fmt"
)

// parseConstraintDecl parses:
// constraint Name = <constraint-expr>;
func (p *Parser) parseConstraintDecl() *ast.ConstraintDecl {
	start := p.expect(tokens.CONSTRAINT_TOKEN).Start
	name := p.parseIdentifier()
	p.expect(tokens.EQUALS_TOKEN)

	expr := p.parseConstraintExpr()

	semi := p.expect(tokens.SEMICOLON_TOKEN)
	end := &semi.End
	if expr != nil && expr.Loc() != nil && expr.Loc().End != nil {
		end = expr.Loc().End
	}

	return &ast.ConstraintDecl{
		Name:     name,
		Expr:     expr,
		Location: *source.NewLocation(&p.filepath, &start, end),
	}
}

// parseConstraintExpr parses intersection constraints:
// term (& term)*
func (p *Parser) parseConstraintExpr() ast.ConstraintExpr {
	left := p.parseConstraintTerm()
	for p.match(tokens.BIT_AND_TOKEN) {
		op := p.advance()
		right := p.parseConstraintTerm()
		if left == nil || right == nil {
			break
		}
		left = &ast.ConstraintBinaryExpr{
			Left:     left,
			Right:    right,
			Op:       op.Kind,
			Location: *source.NewLocation(&p.filepath, left.Loc().Start, right.Loc().End),
		}
	}
	return left
}

func (p *Parser) parseConstraintTerm() ast.ConstraintExpr {
	tok := p.peek()
	termStart := &tok.Start
	approx := false
	if p.match(tokens.BIT_NOT_TOKEN) {
		tilde := p.advance()
		termStart = &tilde.Start
		approx = true
	}

	if p.match(tokens.UNION_TOKEN) {
		unionExpr := p.parseConstraintUnionExpr()
		if approx {
			p.diagnostics.Add(
				diagnostics.NewError("underlying-type marker '~' cannot be applied to a union expression").
					WithCode(diagnostics.ErrInvalidType).
					WithPrimaryLabel(source.NewLocation(&p.filepath, termStart, termStart), "remove '~' before 'union'"),
			)
		}
		return unionExpr
	}

	if p.match(tokens.OPEN_PAREN) {
		p.advance()
		expr := p.parseConstraintExpr()
		p.expect(tokens.CLOSE_PAREN)
		if approx {
			p.diagnostics.Add(
				diagnostics.NewError("underlying-type marker '~' cannot be applied to a grouped constraint").
					WithCode(diagnostics.ErrInvalidType).
					WithPrimaryLabel(source.NewLocation(&p.filepath, termStart, termStart), "remove '~' before '('"),
			)
		}
		return expr
	}

	typ := p.parseConstraintTypeTerm()
	if typ == nil {
		bad := p.peek()
		p.diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("expected constraint term, found %s", bad.Kind)).
				WithCode(diagnostics.ErrInvalidType).
				WithPrimaryLabel(source.NewLocation(&p.filepath, &bad.Start, &bad.End), "invalid constraint term"),
		)
		invalid := &ast.Invalid{Location: *source.NewLocation(&p.filepath, &bad.Start, &bad.End)}
		return &ast.ConstraintTypeTerm{
			Approx:   approx,
			Type:     invalid,
			Location: *source.NewLocation(&p.filepath, termStart, &bad.End),
		}
	}

	end := termStart
	if typ.Loc() != nil && typ.Loc().End != nil {
		end = typ.Loc().End
	}
	return &ast.ConstraintTypeTerm{
		Approx:   approx,
		Type:     typ,
		Location: *source.NewLocation(&p.filepath, termStart, end),
	}
}

func (p *Parser) parseConstraintUnionExpr() ast.ConstraintExpr {
	start := p.expect(tokens.UNION_TOKEN).Start
	p.expect(tokens.OPEN_CURLY)

	terms := make([]*ast.ConstraintTypeTerm, 0)
	for !(p.match(tokens.CLOSE_CURLY) || p.isAtEnd()) {
		term := p.parseConstraintUnionTerm()
		if term != nil {
			terms = append(terms, term)
		}

		if p.match(tokens.CLOSE_CURLY) {
			break
		}
		if p.checkTrailing(tokens.COMMA_TOKEN, tokens.CLOSE_CURLY, "constraint union") {
			p.advance()
			break
		}
		p.expect(tokens.COMMA_TOKEN)
	}

	end := p.expectError(tokens.CLOSE_CURLY, fmt.Sprintf("expected '}' to close constraint union, found %s", p.peek().Kind)).End
	return &ast.ConstraintUnionExpr{
		Terms:    terms,
		Location: *source.NewLocation(&p.filepath, &start, &end),
	}
}

func (p *Parser) parseConstraintUnionTerm() *ast.ConstraintTypeTerm {
	tok := p.peek()
	termStart := &tok.Start
	approx := false
	if p.match(tokens.BIT_NOT_TOKEN) {
		tilde := p.advance()
		termStart = &tilde.Start
		approx = true
	}

	typ := p.parseConstraintTypeTerm()
	if typ == nil {
		bad := p.peek()
		p.diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("expected union term type, found %s", bad.Kind)).
				WithCode(diagnostics.ErrInvalidType).
				WithPrimaryLabel(source.NewLocation(&p.filepath, &bad.Start, &bad.End), "invalid union term"),
		)
		invalid := &ast.Invalid{Location: *source.NewLocation(&p.filepath, &bad.Start, &bad.End)}
		return &ast.ConstraintTypeTerm{
			Approx:   approx,
			Type:     invalid,
			Location: *source.NewLocation(&p.filepath, termStart, &bad.End),
		}
	}

	end := termStart
	if typ.Loc() != nil && typ.Loc().End != nil {
		end = typ.Loc().End
	}

	return &ast.ConstraintTypeTerm{
		Approx:   approx,
		Type:     typ,
		Location: *source.NewLocation(&p.filepath, termStart, end),
	}
}

// parseConstraintTypeTerm parses a single, non-composite constraint term.
// This allows type literals (union/interface/array/etc.) and named references.
func (p *Parser) parseConstraintTypeTerm() ast.TypeNode {
	switch p.peek().Kind {
	case tokens.UNION_TOKEN, tokens.INTERFACE_TOKEN, tokens.STRUCT_TOKEN, tokens.ENUM_TOKEN, tokens.MAP_TOKEN, tokens.FUNCTION_TOKEN, tokens.OPEN_BRACKET, tokens.HASH_TOKEN, tokens.QUESTION_TOKEN, tokens.BIT_AND_TOKEN:
		return p.parseType()
	case tokens.IDENTIFIER_TOKEN:
		ident := p.parseIdentifier()
		var node ast.TypeNode = ident
		if p.match(tokens.SCOPE_TOKEN) {
			node = p.parseScopeResolutionExpr(ident)
		}
		return node
	default:
		return nil
	}
}
