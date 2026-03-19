package parser

import (
	"compiler/internal/frontend/ast"
	"compiler/internal/tokens"
)

func (p *Parser) parseType() ast.TypeExpr {
	start := p.current().Start
	switch p.current().Kind {
	case tokens.QUESTION:
		p.advance()
		return &ast.OptionalType{Inner: p.parseType(), Location: p.locFrom(start)}
	case tokens.AMP:
		p.advance()
		ref := &ast.RefType{Location: p.locFrom(start)}
		if p.match(tokens.MUT) {
			ref.Mutable = true
		}
		ref.Inner = p.parseType()
		return ref
	case tokens.CARET:
		p.advance()
		return &ast.RawPtrType{Inner: p.parseType(), Location: p.locFrom(start)}
	case tokens.ASTERISK:
		p.advance()
		return &ast.PointerType{Inner: p.parseType(), Location: p.locFrom(start)}
	case tokens.LBRACK:
		p.advance()
		if p.at(tokens.RBRACK) {
			p.advance()
			return &ast.SliceType{Inner: p.parseType(), Location: p.locFrom(start)}
		}
		var size ast.Expr
		if p.at(tokens.IDENT) && p.current().Literal == "_" {
			size = &ast.Ident{Path: []string{"_"}, Location: p.locFrom(p.current().Start)}
			p.advance()
		} else {
			size = p.parseExpr(precLowest)
		}
		p.expect(tokens.RBRACK, "expected ']' after array size")
		return &ast.ArrayType{Size: size, Inner: p.parseType(), Location: p.locFrom(start)}
	case tokens.LPAREN:
		return p.parseTupleType()
	case tokens.STRUCT, tokens.INTERFACE, tokens.ENUM, tokens.UNION, tokens.ERROR:
		return p.parseTypeSpec()
	case tokens.IDENT:
		if p.current().Literal == "Self" {
			p.advance()
			return &ast.SelfType{Location: p.locFrom(start)}
		}
		base := &ast.NamedType{Path: p.parseNamePath(), Location: p.locFrom(start)}
		if p.at(tokens.LT) {
			base.TypeArgs = p.parseAngleTypeArgs("type argument")
		}
		if p.match(tokens.BANG) {
			return &ast.ErrorUnionType{Error: base, Value: p.parseType(), Location: p.locFrom(start)}
		}
		return base
	default:
		p.errorHere("expected type")
		p.advance()
		return &ast.NamedType{Path: []string{"<error>"}, Location: p.locFrom(start)}
	}
}

func (p *Parser) parseTupleType() ast.TypeExpr {
	start := p.current().Start
	p.expect(tokens.LPAREN, "expected '('")
	elems := make([]ast.TypeExpr, 0)
	for !p.at(tokens.RPAREN) && !p.at(tokens.EOF) {
		elems = append(elems, p.parseType())
		if !p.consumeTypeListSeparator(tokens.RPAREN, "tuple element") {
			break
		}
	}
	p.expect(tokens.RPAREN, "expected ')'")
	return &ast.TupleType{Elems: elems, Location: p.locFrom(start)}
}

func (p *Parser) parseAngleTypeArgs(itemKind string) []ast.TypeExpr {
	p.expect(tokens.LT, "expected '<'")
	args := make([]ast.TypeExpr, 0)
	for !p.at(tokens.GT) && !p.at(tokens.EOF) {
		args = append(args, p.parseType())
		if !p.consumeTypeListSeparator(tokens.GT, itemKind) {
			break
		}
	}
	p.expect(tokens.GT, "expected '>' after type arguments")
	return args
}
