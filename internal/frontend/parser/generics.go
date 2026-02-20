package parser

import (
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/source"
	"compiler/internal/tokens"
	"fmt"
)

// parseTypeParams parses generic type parameters:
// <T, U: constraint, V: interface { ... }>
func (p *Parser) parseTypeParams() []*ast.TypeParam {
	if !p.match(tokens.LESS_TOKEN) {
		return nil
	}

	lt := p.expect(tokens.LESS_TOKEN)
	params := make([]*ast.TypeParam, 0)

	for !(p.match(tokens.GREATER_TOKEN) || p.isAtEnd()) {
		name := p.parseIdentifier()
		var constraint ast.ConstraintExpr
		if p.match(tokens.COLON_TOKEN) {
			p.advance()
			constraint = p.parseConstraintExpr()
		}

		endPos := name.End
		if constraint != nil && constraint.Loc() != nil && constraint.Loc().End != nil {
			endPos = constraint.Loc().End
		}

		params = append(params, &ast.TypeParam{
			Name:       name,
			Constraint: constraint,
			Location:   *source.NewLocation(&p.filepath, name.Start, endPos),
		})

		if p.match(tokens.GREATER_TOKEN) {
			break
		}

		if p.checkTrailing(tokens.COMMA_TOKEN, tokens.GREATER_TOKEN, "type parameters") {
			p.advance()
			break
		}
		p.expect(tokens.COMMA_TOKEN)
	}

	gt := p.expectError(tokens.GREATER_TOKEN, fmt.Sprintf("expected '>' to close type parameters, found %s", p.peek().Kind))
	if len(params) == 0 {
		p.diagnostics.Add(
			diagnostics.NewError("generic parameter list cannot be empty").
				WithCode(diagnostics.ErrUnexpectedToken).
				WithPrimaryLabel(source.NewLocation(&p.filepath, &lt.Start, &gt.End), "add at least one type parameter"),
		)
	}

	return params
}

// parseTypeArgs parses generic type arguments:
// <i32, map[str]i32, Point<i64>>
func (p *Parser) parseTypeArgs() []ast.TypeNode {
	if !p.match(tokens.LESS_TOKEN) {
		return nil
	}

	lt := p.expect(tokens.LESS_TOKEN)
	args := make([]ast.TypeNode, 0)

	for !(p.match(tokens.GREATER_TOKEN) || p.isAtEnd()) {
		arg := p.parseType()
		args = append(args, arg)

		if p.match(tokens.GREATER_TOKEN) {
			break
		}
		if p.checkTrailing(tokens.COMMA_TOKEN, tokens.GREATER_TOKEN, "type arguments") {
			p.advance()
			break
		}
		p.expect(tokens.COMMA_TOKEN)
	}

	gt := p.expectError(tokens.GREATER_TOKEN, fmt.Sprintf("expected '>' to close type arguments, found %s", p.peek().Kind))
	if len(args) == 0 {
		p.diagnostics.Add(
			diagnostics.NewError("generic type argument list cannot be empty").
				WithCode(diagnostics.ErrUnexpectedToken).
				WithPrimaryLabel(source.NewLocation(&p.filepath, &lt.Start, &gt.End), "add at least one type argument"),
		)
	}
	return args
}

func (p *Parser) canStartGenericCall(expr ast.Expression) bool {
	if expr == nil || !p.match(tokens.LESS_TOKEN) {
		return false
	}

	switch expr.(type) {
	case *ast.IdentifierExpr, *ast.ScopeResolutionExpr, *ast.SelectorExpr:
	default:
		return false
	}

	idx := p.nextNonCommentIndex(p.current)
	if idx >= len(p.tokens) || p.tokens[idx].Kind != tokens.LESS_TOKEN {
		return false
	}

	depth := 0
	sawTokenInside := false

	for idx < len(p.tokens) {
		tok := p.tokens[idx]
		if tok.Kind == tokens.COMMENT_TOKEN {
			idx++
			continue
		}

		switch tok.Kind {
		case tokens.LESS_TOKEN:
			depth++
		case tokens.GREATER_TOKEN:
			depth--
			if depth == 0 {
				next := p.nextNonCommentIndex(idx + 1)
				return sawTokenInside && next < len(p.tokens) && p.tokens[next].Kind == tokens.OPEN_PAREN
			}
			if depth < 0 {
				return false
			}
		case tokens.EOF_TOKEN, tokens.SEMICOLON_TOKEN:
			return false
		default:
			if depth > 0 {
				sawTokenInside = true
			}
		}

		idx++
	}

	return false
}
