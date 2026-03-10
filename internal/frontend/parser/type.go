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
	case tokens.ASTERISK:
		p.advance()
		ptr := &ast.PointerType{Location: p.locFrom(start)}
		for {
			switch p.current().Kind {
			case tokens.OWN:
				ptr.IsOwn = true
				p.advance()
			case tokens.RAW:
				ptr.IsRaw = true
				p.advance()
			case tokens.MUT:
				ptr.IsMut = true
				p.advance()
			default:
				ptr.Inner = p.parseType()
				return ptr
			}
		}
	case tokens.LBRACK:
		p.advance()
		size := p.parseExpr(precLowest)
		p.expect(tokens.RBRACK, "expected ']' after array size")
		return &ast.ArrayType{Size: size, Inner: p.parseType(), Location: p.locFrom(start)}
	case tokens.LPAREN:
		return p.parseTupleType()
	case tokens.STRUCT, tokens.INTERFACE, tokens.ENUM, tokens.UNION, tokens.ERROR:
		return p.parseTypeSpec()
	case tokens.IDENT:
		base := &ast.NamedType{Path: p.parseNamePath(), Location: p.locFrom(start)}
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
