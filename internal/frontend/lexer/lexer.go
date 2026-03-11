package lexer

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"compiler/internal/diagnostics"
	"compiler/internal/source"
	"compiler/internal/tokens"
	"compiler/internal/utils/numeric"
)

type pattern struct {
	regex   *regexp.Regexp
	handler func(*Lexer, string)
}

type Lexer struct {
	file     string
	input    string
	pos      source.Position
	diag     *diagnostics.Bag
	patterns []pattern
	tokens   []tokens.Token
}

func New(file, input string, diag *diagnostics.Bag) *Lexer {
	if diag == nil {
		diag = diagnostics.NewBag()
	}
	l := &Lexer{
		file:  file,
		input: input,
		pos:   source.NewPosition(),
		diag:  diag,
	}
	l.patterns = []pattern{
		{regexp.MustCompile(`^\s+`), (*Lexer).skip},
		{regexp.MustCompile(`^///[^\n\r]*`), (*Lexer).docComment},
		{regexp.MustCompile(`^//[^\n\r]*`), (*Lexer).skip},
		{regexp.MustCompile(`(?s)^/\*.*?\*/`), (*Lexer).skip},
		{regexp.MustCompile(`^` + numeric.NumberPattern), (*Lexer).number},
		{regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`), (*Lexer).identifier},
		{regexp.MustCompile(`^::`), emit(tokens.DCOLON)},
		{regexp.MustCompile(`^==`), emit(tokens.EQ)},
		{regexp.MustCompile(`^!=`), emit(tokens.NEQ)},
		{regexp.MustCompile(`^<=`), emit(tokens.LE)},
		{regexp.MustCompile(`^>=`), emit(tokens.GE)},
		{regexp.MustCompile(`^&&`), emit(tokens.ANDAND)},
		{regexp.MustCompile(`^\|\|`), emit(tokens.OROR)},
		{regexp.MustCompile(`^\|`), emit(tokens.BAR)},
		{regexp.MustCompile(`^\?\?`), emit(tokens.QQ)},
		{regexp.MustCompile(`^!!`), emit(tokens.BB)},
		{regexp.MustCompile(`^=>`), emit(tokens.FATARROW)},
		{regexp.MustCompile(`^=`), emit(tokens.ASSIGN)},
		{regexp.MustCompile(`^\+`), emit(tokens.PLUS)},
		{regexp.MustCompile(`^-`), emit(tokens.MINUS)},
		{regexp.MustCompile(`^\*`), emit(tokens.ASTERISK)},
		{regexp.MustCompile(`^/`), emit(tokens.SLASH)},
		{regexp.MustCompile(`^%`), emit(tokens.PERCENT)},
		{regexp.MustCompile(`^!`), emit(tokens.BANG)},
		{regexp.MustCompile(`^\?`), emit(tokens.QUESTION)},
		{regexp.MustCompile(`^&`), emit(tokens.AMP)},
		{regexp.MustCompile(`^~`), emit(tokens.TILDE)},
		{regexp.MustCompile(`^<`), emit(tokens.LT)},
		{regexp.MustCompile(`^>`), emit(tokens.GT)},
		{regexp.MustCompile(`^:`), emit(tokens.COLON)},
		{regexp.MustCompile(`^,`), emit(tokens.COMMA)},
		{regexp.MustCompile(`^\.`), emit(tokens.DOT)},
		{regexp.MustCompile(`^#`), emit(tokens.HASH)},
		{regexp.MustCompile(`^;`), emit(tokens.SEMICOLON)},
		{regexp.MustCompile(`^\(`), emit(tokens.LPAREN)},
		{regexp.MustCompile(`^\)`), emit(tokens.RPAREN)},
		{regexp.MustCompile(`^\{`), emit(tokens.LBRACE)},
		{regexp.MustCompile(`^\}`), emit(tokens.RBRACE)},
		{regexp.MustCompile(`^\[`), emit(tokens.LBRACK)},
		{regexp.MustCompile(`^\]`), emit(tokens.RBRACK)},
	}
	return l
}

func emit(kind tokens.Kind) func(*Lexer, string) {
	return func(l *Lexer, match string) {
		start := l.pos
		l.advance(match)
		l.tokens = append(l.tokens, tokens.Token{Kind: kind, Literal: match, Start: start, End: l.pos})
	}
}

func (l *Lexer) Lex() []tokens.Token {
	return l.Tokenize()
}

func (l *Lexer) Tokenize() []tokens.Token {
	for !l.atEOF() {
		if l.remainder()[0] == '"' {
			l.stringLiteral()
			continue
		}

		matched := false
		remainder := l.remainder()
		for _, pattern := range l.patterns {
			if loc := pattern.regex.FindStringIndex(remainder); loc != nil && loc[0] == 0 {
				pattern.handler(l, remainder[:loc[1]])
				matched = true
				break
			}
		}
		if matched {
			continue
		}

		start := l.pos
		_, width := utf8.DecodeRuneInString(remainder)
		if width <= 0 {
			width = 1
		}
		bad := remainder[:width]
		l.advance(bad)
		loc := source.NewLocation(l.file, start, l.pos)
		l.diag.Add(
			diagnostics.NewError("illegal character").
				WithCode(diagnostics.ErrUnexpectedCharacter).
				WithPrimaryLabel(&loc, "remove or replace this character"),
		)
		l.tokens = append(l.tokens, tokens.Token{Kind: tokens.ILLEGAL, Literal: bad, Start: start, End: l.pos})
	}

	l.tokens = append(l.tokens, tokens.Token{Kind: tokens.EOF, Start: l.pos, End: l.pos})
	return append([]tokens.Token(nil), l.tokens...)
}

func (l *Lexer) skip(match string) {
	l.advance(match)
}

func (l *Lexer) identifier(match string) {
	start := l.pos
	l.advance(match)
	l.tokens = append(l.tokens, tokens.Token{
		Kind:    tokens.LookupIdent(match),
		Literal: match,
		Start:   start,
		End:     l.pos,
	})
}

func (l *Lexer) docComment(match string) {
	start := l.pos
	l.advance(match)
	text := strings.TrimSpace(strings.TrimPrefix(match, "///"))
	l.tokens = append(l.tokens, tokens.Token{
		Kind:    tokens.DOC_COMMENT,
		Literal: text,
		Start:   start,
		End:     l.pos,
	})
}

func (l *Lexer) number(match string) {
	start := l.pos
	l.advance(match)
	l.tokens = append(l.tokens, tokens.Token{
		Kind:    tokens.NUMBER,
		Literal: strings.ReplaceAll(match, "_", ""),
		Start:   start,
		End:     l.pos,
	})
}

func (l *Lexer) stringLiteral() {
	start := l.pos
	remainder := l.remainder()
	escaped := false
	for i := 1; i < len(remainder); i++ {
		switch remainder[i] {
		case '\\':
			escaped = !escaped
		case '"':
			if escaped {
				escaped = false
				continue
			}
			match := remainder[:i+1]
			l.advance(match)
			l.tokens = append(l.tokens, tokens.Token{Kind: tokens.STRING, Literal: unescape(match[1 : len(match)-1]), Start: start, End: l.pos})
			return
		case '\n', '\r':
			loc := source.NewLocation(l.file, start, l.pos)
			l.diag.Add(
				diagnostics.NewError("unterminated string literal").
					WithCode(diagnostics.ErrUnterminatedString).
					WithPrimaryLabel(&loc, "string literal is not closed"),
			)
			l.tokens = append(l.tokens, tokens.Token{Kind: tokens.ILLEGAL, Literal: remainder[:i], Start: start, End: l.pos})
			return
		default:
			escaped = false
		}
	}
	l.advance(remainder)
	loc := source.NewLocation(l.file, start, l.pos)
	l.diag.Add(
		diagnostics.NewError("unterminated string literal").
			WithCode(diagnostics.ErrUnterminatedString).
			WithPrimaryLabel(&loc, "string literal is not closed"),
	)
	l.tokens = append(l.tokens, tokens.Token{Kind: tokens.ILLEGAL, Literal: remainder, Start: start, End: l.pos})
}

func unescape(in string) string {
	replacer := strings.NewReplacer(`\\`, `\\`, `\"`, `"`, `\n`, "\n", `\r`, "\r", `\t`, "\t", `\0`, "\x00")
	return replacer.Replace(in)
}

func (l *Lexer) advance(text string) {
	l.pos.Advance(text)
}

func (l *Lexer) remainder() string {
	if l.pos.Index >= len(l.input) {
		return ""
	}
	return l.input[l.pos.Index:]
}

func (l *Lexer) atEOF() bool {
	return l.pos.Index >= len(l.input)
}
