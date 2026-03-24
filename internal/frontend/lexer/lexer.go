package lexer

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"compiler/internal/core/diagnostics"
	"compiler/internal/core/source"
	"compiler/internal/tokens"
	"compiler/internal/utils/numeric"
)

// regexHandler is called when a pattern matches at the current position.
// It receives the compiled regex so it can use FindString / FindStringSubmatch
// for richer extraction (e.g. capture groups, variable-length content).
type regexHandler func(lex *Lexer, regex *regexp.Regexp)

type regexPattern struct {
	regex   *regexp.Regexp
	handler regexHandler
}

type Lexer struct {
	file     string
	input    string
	pos      source.Position
	diag     *diagnostics.DiagnosticBag
	patterns []regexPattern
	toks     []tokens.Token
}

func New(file, input string, diag *diagnostics.DiagnosticBag) *Lexer {
	if diag == nil {
		diag = diagnostics.NewDiagnosticBag("")
	}
	l := &Lexer{
		file:  file,
		input: input,
		pos:   source.NewPosition(),
		diag:  diag,
	}
	l.patterns = []regexPattern{
		{regexp.MustCompile(`\s+`), skipHandler},
		{regexp.MustCompile(`//[^\n\r]*`), lineCommentHandler},
		{regexp.MustCompile(`(?s)/\*.*?\*/`), blockCommentHandler},
		{regexp.MustCompile(`"(?:\\.|[^"\\])*"`), stringHandler},
		{regexp.MustCompile(numeric.NumberPattern), numberHandler},
		{regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`), identifierHandler},
		// Multi-char operators — must precede their single-char prefixes.
		{regexp.MustCompile(`::`), defaultHandler(tokens.DCOLON)},
		{regexp.MustCompile(`==`), defaultHandler(tokens.EQ)},
		{regexp.MustCompile(`!=`), defaultHandler(tokens.NEQ)},
		{regexp.MustCompile(`<=`), defaultHandler(tokens.LE)},
		{regexp.MustCompile(`>=`), defaultHandler(tokens.GE)},
		{regexp.MustCompile(`&&`), defaultHandler(tokens.ANDAND)},
		{regexp.MustCompile(`\|\|`), defaultHandler(tokens.OROR)},
		{regexp.MustCompile(`\?\?`), defaultHandler(tokens.QQ)},
		{regexp.MustCompile(`!!`), defaultHandler(tokens.BB)},
		{regexp.MustCompile(`=>`), defaultHandler(tokens.FATARROW)},
		{regexp.MustCompile(`\+\+`), defaultHandler(tokens.PLUS_PLUS)},
		{regexp.MustCompile(`--`), defaultHandler(tokens.MINUS_MINUS)},
		{regexp.MustCompile(`\+=`), defaultHandler(tokens.PLUS_ASSIGN)},
		{regexp.MustCompile(`-=`), defaultHandler(tokens.MINUS_ASSIGN)},
		{regexp.MustCompile(`\*=`), defaultHandler(tokens.STAR_ASSIGN)},
		{regexp.MustCompile(`/=`), defaultHandler(tokens.SLASH_ASSIGN)},
		{regexp.MustCompile(`%=`), defaultHandler(tokens.PCT_ASSIGN)},
		// Single-char operators.
		{regexp.MustCompile(`=`), defaultHandler(tokens.ASSIGN)},
		{regexp.MustCompile(`\+`), defaultHandler(tokens.PLUS)},
		{regexp.MustCompile(`-`), defaultHandler(tokens.MINUS)},
		{regexp.MustCompile(`\*`), defaultHandler(tokens.ASTERISK)},
		{regexp.MustCompile(`/`), defaultHandler(tokens.SLASH)},
		{regexp.MustCompile(`%`), defaultHandler(tokens.PERCENT)},
		{regexp.MustCompile(`!`), defaultHandler(tokens.BANG)},
		{regexp.MustCompile(`\?`), defaultHandler(tokens.QUESTION)},
		{regexp.MustCompile(`@`), defaultHandler(tokens.AT)},
		{regexp.MustCompile(`&`), defaultHandler(tokens.AMP)}, {regexp.MustCompile(`^\|>`), defaultHandler(tokens.PIPE_ARROW)}, {regexp.MustCompile(`\|`), defaultHandler(tokens.BAR)}, {regexp.MustCompile(`^\^=`), defaultHandler(tokens.CARET_ASSIGN)},
		{regexp.MustCompile(`^\^`), defaultHandler(tokens.CARET)}, {regexp.MustCompile(`~`), defaultHandler(tokens.TILDE)},
		{regexp.MustCompile(`<`), defaultHandler(tokens.LT)},
		{regexp.MustCompile(`>`), defaultHandler(tokens.GT)},
		{regexp.MustCompile(`:`), defaultHandler(tokens.COLON)},
		{regexp.MustCompile(`,`), defaultHandler(tokens.COMMA)}, {regexp.MustCompile(`^\.\.\.`), defaultHandler(tokens.ELLIPSIS)},
		{regexp.MustCompile(`^\.\.=`), defaultHandler(tokens.DOTDOT_EQ)},
		{regexp.MustCompile(`^\.\.`), defaultHandler(tokens.DOTDOT)}, {regexp.MustCompile(`\.`), defaultHandler(tokens.DOT)},
		{regexp.MustCompile(`#`), defaultHandler(tokens.HASH)},
		{regexp.MustCompile(`;`), defaultHandler(tokens.SEMICOLON)},
		{regexp.MustCompile(`\(`), defaultHandler(tokens.LPAREN)},
		{regexp.MustCompile(`\)`), defaultHandler(tokens.RPAREN)},
		{regexp.MustCompile(`\{`), defaultHandler(tokens.LBRACE)},
		{regexp.MustCompile(`\}`), defaultHandler(tokens.RBRACE)},
		{regexp.MustCompile(`\[`), defaultHandler(tokens.LBRACK)},
		{regexp.MustCompile(`\]`), defaultHandler(tokens.RBRACK)},
	}
	return l
}

// defaultHandler emits a single token with the given kind using the full regex match.
func defaultHandler(kind tokens.Kind) regexHandler {
	return func(l *Lexer, re *regexp.Regexp) {
		match := re.FindString(l.remainder())
		start := l.pos
		l.advanceBy(match)
		l.push(tokens.Token{Kind: kind, Literal: match, Start: start, End: l.pos})
	}
}

func skipHandler(l *Lexer, re *regexp.Regexp) {
	match := re.FindString(l.remainder())
	l.advanceBy(match)
}

func lineCommentHandler(l *Lexer, re *regexp.Regexp) {
	match := re.FindString(l.remainder())
	start := l.pos
	l.advanceBy(match)
	if !l.isStandaloneComment(start.Index) || !l.isDocCandidateAhead(l.pos.Index) {
		return
	}
	prefix := "//"
	if strings.HasPrefix(match, "///") {
		prefix = "///"
	}
	text := strings.TrimSpace(strings.TrimPrefix(match, prefix))
	l.push(tokens.Token{Kind: tokens.DOC_COMMENT, Literal: text, Start: start, End: l.pos})
}

func blockCommentHandler(l *Lexer, re *regexp.Regexp) {
	match := re.FindString(l.remainder())
	start := l.pos
	l.advanceBy(match)
	if !l.isStandaloneComment(start.Index) || !l.isDocCandidateAhead(l.pos.Index) {
		return
	}
	content := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(match, "/*"), "*/"))
	lines := strings.Split(content, "\n")
	for i := range lines {
		line := strings.TrimSpace(lines[i])
		line = strings.TrimPrefix(line, "*")
		lines[i] = strings.TrimSpace(line)
	}
	text := strings.Join(lines, "\n")
	l.push(tokens.Token{Kind: tokens.DOC_COMMENT, Literal: text, Start: start, End: l.pos})
}

func identifierHandler(l *Lexer, re *regexp.Regexp) {
	match := re.FindString(l.remainder())
	start := l.pos
	l.advanceBy(match)
	l.push(tokens.Token{Kind: tokens.LookupIdent(match), Literal: match, Start: start, End: l.pos})
}

func numberHandler(l *Lexer, re *regexp.Regexp) {
	match := re.FindString(l.remainder())
	start := l.pos
	l.advanceBy(match)
	l.push(tokens.Token{Kind: tokens.NUMBER, Literal: strings.ReplaceAll(match, "_", ""), Start: start, End: l.pos})
}

func stringHandler(l *Lexer, re *regexp.Regexp) {
	match := re.FindString(l.remainder())
	start := l.pos
	l.advanceBy(match)
	inner := match[1 : len(match)-1] // strip quotes
	l.push(tokens.Token{Kind: tokens.STRING, Literal: unescapeString(inner), Start: start, End: l.pos})
}

func (l *Lexer) Tokenize() []tokens.Token {
	for !l.atEOF() {
		matched := false
		rem := l.remainder()
		for _, p := range l.patterns {
			loc := p.regex.FindStringIndex(rem)
			if loc != nil && loc[0] == 0 {
				p.handler(l, p.regex)
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		// Unrecognized character — report and skip.
		start := l.pos
		_, width := utf8.DecodeRuneInString(rem)
		if width < 1 {
			width = 1
		}
		bad := rem[:width]
		l.advanceBy(bad)
		loc := source.NewLocation(l.file, start, l.pos)
		l.diag.Add(
			diagnostics.NewError(fmt.Sprintf("illegal character %q", bad)).
				WithCode(diagnostics.ErrUnexpectedCharacter).
				WithPrimaryLabel(&loc, "remove or replace this character"),
		)
		l.push(tokens.Token{Kind: tokens.ILLEGAL, Literal: bad, Start: start, End: l.pos})
	}
	l.push(tokens.Token{Kind: tokens.EOF, Start: l.pos, End: l.pos})
	return append([]tokens.Token(nil), l.toks...)
}

func (l *Lexer) push(t tokens.Token) {
	l.toks = append(l.toks, t)
}

func (l *Lexer) isStandaloneComment(index int) bool {
	if index <= 0 || index > len(l.input) {
		return true
	}
	lineStart := strings.LastIndexByte(l.input[:index], '\n')
	if lineStart < 0 {
		lineStart = 0
	} else {
		lineStart++
	}
	return strings.TrimSpace(l.input[lineStart:index]) == ""
}

func (l *Lexer) isDocCandidateAhead(index int) bool {
	i := index
	for i < len(l.input) {
		switch l.input[i] {
		case ' ', '\t', '\r', '\n':
			i++
			continue
		case '#':
			return true
		case '/':
			if i+1 < len(l.input) && (l.input[i+1] == '/' || l.input[i+1] == '*') {
				return true
			}
			return false
		}
		break
	}
	if i >= len(l.input) {
		return false
	}
	start := i
	for i < len(l.input) {
		ch := l.input[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' {
			i++
			continue
		}
		break
	}
	word := l.input[start:i]
	switch word {
	case "fn", "unsafe", "type", "let", "const", "import":
		return true
	default:
		return false
	}
}

func (l *Lexer) advanceBy(text string) {
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

func unescapeString(s string) string {
	var out []byte
	i := 0
	for i < len(s) {
		if s[i] != '\\' || i+1 >= len(s) {
			out = append(out, s[i])
			i++
			continue
		}
		switch s[i+1] {
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case '0':
			out = append(out, 0)
		case '\\':
			out = append(out, '\\')
		case '"':
			out = append(out, '"')
		case 'x':
			if i+3 < len(s) {
				h := s[i+2 : i+4]
				var v byte
				if _, err := fmt.Sscanf(h, "%02x", &v); err == nil {
					out = append(out, v)
					i += 4
					continue
				}
			}
			out = append(out, s[i])
			i++
			continue
		default:
			out = append(out, s[i]) // keep backslash
			i++
			continue
		}
		i += 2
	}
	return string(out)
}
