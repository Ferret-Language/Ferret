package parser

import (
	"fmt"
	"strings"

	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/source"
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
		if len(attrs) > 0 {
			p.errorAt(attrs[0].Location, "attributes are only supported on function declarations")
		}
		return p.parseLetDecl()
	case tokens.CONST:
		if len(attrs) > 0 {
			p.errorAt(attrs[0].Location, "attributes are only supported on function declarations")
		}
		return p.parseConstDecl()
	case tokens.TYPE:
		if len(attrs) > 0 {
			p.errorAt(attrs[0].Location, "attributes are only supported on function declarations")
		}
		return p.parseTypeDecl()
	case tokens.UNSAFE:
		if p.peekN(1).Kind == tokens.FN {
			return p.parseFuncDecl(doc, attrs)
		}
	case tokens.FN:
		return p.parseFuncDecl(doc, attrs)
	}
	if len(attrs) > 0 {
		p.errorAt(attrs[0].Location, "attributes are only supported on function declarations")
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
		name := p.expectIdent("expected attribute name").Literal
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
	for _, kind := range kinds {
		if p.at(kind) {
			return true
		}
	}
	return false
}

func (p *Parser) match(kinds ...tokens.Kind) bool {
	for _, kind := range kinds {
		if p.at(kind) {
			p.advance()
			return true
		}
	}
	return false
}

func (p *Parser) expect(kind tokens.Kind, message string) tokens.Token {
	if p.at(kind) {
		return p.advance()
	}
	p.errorHere(message)
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

func (p *Parser) makeExprLoc(start source.Position) source.Location {
	return source.NewLocation(p.file, start, p.previous().End)
}

func (p *Parser) errorHere(message string) {
	tok := p.current()
	loc := source.NewLocation(p.file, tok.Start, tok.End)
	p.errorAt(loc, message)
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
		case tokens.RBRACE, tokens.LET, tokens.CONST, tokens.RETURN, tokens.IF, tokens.SWITCH, tokens.WHILE, tokens.FOR, tokens.DEFER, tokens.LOCK, tokens.UNSAFE, tokens.BREAK, tokens.CONTINUE:
			return
		default:
			p.advance()
		}
	}
}

func (p *Parser) advanceUntil(kinds ...tokens.Kind) {
	for !p.at(tokens.EOF) {
		for _, kind := range kinds {
			if p.at(kind) {
				return
			}
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

func (p *Parser) consumeListSeparator(end tokens.Kind, itemName string, canStartNext bool) bool {
	if p.match(tokens.COMMA) {
		return true
	}
	if p.at(end) || p.at(tokens.EOF) {
		return false
	}
	tok := p.current()
	loc := source.NewLocation(p.file, tok.Start, tok.End)
	p.errorAt(loc, "expected ',' or '"+string(end)+"' after "+itemName)
	if !canStartNext {
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
