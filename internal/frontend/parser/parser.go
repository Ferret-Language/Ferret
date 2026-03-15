package parser

import (
	"fmt"
	"slices"
	"strings"

	"compiler/internal/core/diagnostics"
	"compiler/internal/core/source"
	"compiler/internal/frontend/ast"
	"compiler/internal/tokens"
)

type Parser struct {
	file                string
	toks                []tokens.Token
	pos                 int
	compositeValueDepth int
	diag                *diagnostics.Bag
}

func Parse(file string, toks []tokens.Token, diag *diagnostics.Bag) *ast.Module {
	return New(file, toks, diag).ParseModule()
}

func New(file string, toks []tokens.Token, diag *diagnostics.Bag) *Parser {
	if diag == nil {
		diag = diagnostics.NewBag()
	}
	return &Parser{file: file, toks: toks, diag: diag}
}

func (p *Parser) ParseModule() *ast.Module {
	mod := &ast.Module{
		FilePath: p.file,
		Imports:  make([]*ast.ImportDecl, 0),
		Decls:    make([]ast.Decl, 0),
	}
	seenDecl := false
	for !p.at(tokens.EOF) {
		p.skipDocCommentsBeforeImports()
		if p.at(tokens.IMPORT) {
			if seenDecl {
				p.errorHere("imports must appear before declarations")
				p.parseImportDecl() // consume tokens but discard
				continue
			}
			if imp := p.parseImportDecl(); imp != nil {
				mod.Imports = append(mod.Imports, imp)
			}
			continue
		}
		seenDecl = true
		decl := p.parseDecl()
		if decl != nil {
			mod.Decls = append(mod.Decls, decl)
		} else {
			p.synchronizeTopLevel()
		}
	}
	p.validateModule(mod)
	return mod
}

func (p *Parser) parseDecl() ast.Decl {
	doc := p.parseDocComment()
	attrs := p.parseAttributes()
	switch p.current().Kind {
	case tokens.LET:
		return p.parseLetDecl(attrs)
	case tokens.CONST:
		return p.parseConstDecl(attrs)
	case tokens.TYPE:
		return p.parseTypeDecl(attrs)
	case tokens.UNSAFE:
		if p.peekN(1).Kind == tokens.FN {
			return p.parseFuncDecl(doc, attrs)
		}
	case tokens.FN:
		return p.parseFuncDecl(doc, attrs)
	}
	p.errorHere("expected top-level declaration")
	p.advance()
	return nil
}

func (p *Parser) skipDocCommentsBeforeImports() {
	for p.at(tokens.DOC_COMMENT) && p.peekN(1).Kind == tokens.IMPORT {
		p.advance()
	}
}

func (p *Parser) parseDocComment() *ast.CommentGroup {
	if !p.at(tokens.DOC_COMMENT) {
		return nil
	}
	start := p.current().Start
	parts := make([]string, 0, 1)
	for p.at(tokens.DOC_COMMENT) {
		parts = append(parts, p.advance().Literal)
	}
	return &ast.CommentGroup{
		Text:     strings.Join(parts, "\n"),
		Location: p.locFrom(start),
	}
}

func (p *Parser) parseAttributes() []ast.Attribute {
	attrs := make([]ast.Attribute, 0)
	for p.at(tokens.HASH) {
		start := p.advance().Start
		p.expect(tokens.LBRACK, "expected '[' after '#'")
		name := p.parseAttributeName()
		args := make([]string, 0)
		if p.match(tokens.LPAREN) {
			for !p.at(tokens.RPAREN) && !p.at(tokens.EOF) {
				switch p.current().Kind {
				case tokens.STRING, tokens.IDENT:
					args = append(args, p.advance().Literal)
				default:
					p.errorHere("expected attribute argument")
					p.advanceUntil(tokens.COMMA, tokens.RPAREN)
				}
				if p.at(tokens.COMMA) {
					p.advance()
					continue
				}
				break
			}
			p.expect(tokens.RPAREN, "expected ')' after attribute arguments")
		}
		p.expect(tokens.RBRACK, "expected ']' after attribute")
		attrs = append(attrs, ast.Attribute{Name: name, Args: args, Location: p.locFrom(start)})
	}
	return attrs
}

func (p *Parser) parseAttributeName() string {
	switch p.current().Kind {
	case tokens.IDENT, tokens.IF:
		return p.advance().Literal
	default:
		p.errorHere("expected attribute name")
		return p.current().Literal
	}
}

func (p *Parser) current() tokens.Token {
	if len(p.toks) == 0 {
		return tokens.Token{Kind: tokens.EOF}
	}
	if p.pos >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[p.pos]
}

func (p *Parser) previous() tokens.Token {
	if p.pos == 0 || len(p.toks) == 0 {
		return tokens.Token{Kind: tokens.EOF}
	}
	return p.toks[p.pos-1]
}

func (p *Parser) peekN(n int) tokens.Token {
	idx := p.pos + n
	if len(p.toks) == 0 {
		return tokens.Token{Kind: tokens.EOF}
	}
	if idx >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[idx]
}

func (p *Parser) advance() tokens.Token {
	tok := p.current()
	if p.pos < len(p.toks) {
		p.pos++
	}
	return tok
}

func (p *Parser) at(kind tokens.Kind) bool {
	return p.current().Kind == kind
}

func (p *Parser) atAny(kinds ...tokens.Kind) bool {
	return slices.ContainsFunc(kinds, p.at)
}

// hasGenericCallAhead reports whether the '[' at the current position begins a
// generic-call type-argument list, i.e. whether the matching ']' is
// immediately followed by '('.  If it is NOT followed by '(', the '[...]' is
// an array index expression instead.
func (p *Parser) hasGenericCallAhead() bool {
	depth := 0
	for i := p.pos; i < len(p.toks); i++ {
		switch p.toks[i].Kind {
		case tokens.LBRACK:
			depth++
		case tokens.RBRACK:
			depth--
			if depth == 0 {
				// token right after the matching ']'
				next := i + 1
				if next < len(p.toks) {
					return p.toks[next].Kind == tokens.LPAREN
				}
				return false
			}
		case tokens.EOF:
			return false
		}
	}
	return false
}

func (p *Parser) match(kinds ...tokens.Kind) bool {
	if slices.ContainsFunc(kinds, p.at) {
		p.advance()
		return true
	}
	return false
}

func (p *Parser) expect(kind tokens.Kind, message string) tokens.Token {
	if p.at(kind) {
		return p.advance()
	}
	p.errorExpected(message)
	return p.current()
}

func (p *Parser) expectIdent(message string) tokens.Token {
	return p.expect(tokens.IDENT, message)
}

func (p *Parser) locFrom(start source.Position) source.Location {
	end := start
	if p.pos > 0 {
		end = p.previous().End
	}
	return source.NewLocation(p.file, start, end)
}

func (p *Parser) locOfToken(tok tokens.Token) source.Location {
	return source.NewLocation(p.file, tok.Start, tok.End)
}

func (p *Parser) makeExprLoc(start source.Position) source.Location {
	return source.NewLocation(p.file, start, p.previous().End)
}

func (p *Parser) errorHere(message string) {
	tok := p.current()
	loc := source.NewLocation(p.file, tok.Start, tok.End)
	p.errorAt(loc, message)
}

func (p *Parser) errorExpected(message string) {
	insert := p.expectedInsertionPoint()
	loc := source.NewLocation(p.file, insert, insert)
	p.errorAt(loc, message)
}

func (p *Parser) expectedInsertionPoint() source.Position {
	tok := p.current()
	insert := tok.Start
	if p.pos > 0 {
		prevEnd := p.previous().End
		if prevEnd.Index <= tok.Start.Index {
			insert = prevEnd
		}
	}
	return insert
}

func (p *Parser) errorAt(loc source.Location, message string) {
	p.diag.Add(
		diagnostics.NewError(message).
			WithCode(diagnostics.ErrUnexpectedToken).
			WithPrimaryLabel(&loc, message),
	)
}

func (p *Parser) synchronizeTopLevel() {
	for !p.at(tokens.EOF) {
		switch p.current().Kind {
		case tokens.IMPORT, tokens.LET, tokens.CONST, tokens.TYPE, tokens.FN:
			return
		default:
			p.advance()
		}
	}
}

func (p *Parser) synchronizeStmt() {
	for !p.at(tokens.EOF) {
		switch p.current().Kind {
		case tokens.SEMICOLON:
			p.advance()
			return
		case tokens.RBRACE, tokens.LET, tokens.CONST, tokens.RETURN, tokens.IF, tokens.MATCH, tokens.WHILE, tokens.FOR, tokens.DEFER, tokens.LOCK, tokens.UNSAFE, tokens.BREAK, tokens.CONTINUE:
			return
		default:
			p.advance()
		}
	}
}

func (p *Parser) advanceUntil(kinds ...tokens.Kind) {
	for !p.at(tokens.EOF) {
		if slices.ContainsFunc(kinds, p.at) {
			return
		}
		p.advance()
	}
}

func (p *Parser) consumeExprListSeparator(end tokens.Kind, itemName string) bool {
	return p.consumeListSeparator(end, itemName, p.startsExpr())
}

func (p *Parser) consumeTypeListSeparator(end tokens.Kind, itemName string) bool {
	return p.consumeListSeparator(end, itemName, p.startsType())
}

func (p *Parser) isRecoveryBoundary(kind tokens.Kind) bool {
	switch kind {
	case tokens.RBRACE,
		tokens.SEMICOLON,
		tokens.LET,
		tokens.CONST,
		tokens.RETURN,
		tokens.IF,
		tokens.MATCH,
		tokens.WHILE,
		tokens.FOR,
		tokens.DEFER,
		tokens.LOCK,
		tokens.UNSAFE,
		tokens.BREAK,
		tokens.CONTINUE,
		tokens.IMPORT,
		tokens.TYPE,
		tokens.FN:
		return true
	default:
		return false
	}
}

func (p *Parser) consumeListSeparator(end tokens.Kind, itemName string, canStartNext bool) bool {
	if p.match(tokens.COMMA) {
		return true
	}
	if p.at(end) || p.at(tokens.EOF) {
		return false
	}
	tok := p.current()
	loc := source.NewLocation(p.file, tok.Start, tok.End)
	if !canStartNext {
		insert := p.expectedInsertionPoint()
		loc = source.NewLocation(p.file, insert, insert)
	}
	p.errorAt(loc, "expected ',' or '"+string(end)+"' after "+itemName)
	if !canStartNext {
		if p.isRecoveryBoundary(p.current().Kind) {
			return false
		}
		p.advanceUntil(tokens.COMMA, end)
		p.match(tokens.COMMA)
	}
	return !p.at(end) && !p.at(tokens.EOF)
}

func (p *Parser) parseNamePath() []string {
	path := []string{p.expectIdent("expected identifier").Literal}
	for p.match(tokens.DCOLON) {
		path = append(path, p.expectIdent("expected identifier after ::").Literal)
	}
	return path
}

func (p *Parser) startsType() bool {
	switch p.current().Kind {
	case tokens.IDENT, tokens.QUESTION, tokens.OWN, tokens.RAW, tokens.ASTERISK,
		tokens.LBRACK, tokens.LPAREN, tokens.STRUCT, tokens.INTERFACE, tokens.ENUM,
		tokens.UNION, tokens.ERROR:
		return true
	default:
		return false
	}
}

func (p *Parser) unexpected(kind tokens.Kind) string {
	return fmt.Sprintf("unexpected token %s", kind)
}
