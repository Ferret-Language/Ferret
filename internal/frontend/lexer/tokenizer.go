package lexer

import (
	"fmt"
	"regexp"

	"compiler/internal/diagnostics"
	"compiler/internal/source"
	"compiler/internal/tokens"
	"compiler/internal/utils/numeric"
)

type regexHandler func(lex *Lexer, regex *regexp.Regexp)

type regexPattern struct {
	regex   *regexp.Regexp
	handler regexHandler
}

type Lexer struct {
	//	Errors     []error
	diagnostics *diagnostics.DiagnosticBag
	Tokens      []tokens.Token
	Position    source.Position
	sourceCode  []byte
	patterns    []regexPattern
	FilePath    string
}

func (lex *Lexer) advance(match string) {
	lex.Position.Advance(match)
}

func (lex *Lexer) push(token tokens.Token) {
	lex.Tokens = append(lex.Tokens, token)
}

func (lex *Lexer) remainder() string {
	return string(lex.sourceCode)[lex.Position.Index:]
}

func (lex *Lexer) atEOF() bool {
	return lex.Position.Index >= len(lex.sourceCode)
}

func New(filepath, content string, diag *diagnostics.DiagnosticBag) *Lexer {
	//create the lexer
	lex := &Lexer{
		sourceCode: []byte(content),
		Tokens:     make([]tokens.Token, 0),
		Position: source.Position{
			Line:   1,
			Column: 1,
			Index:  0,
		},

		diagnostics: diag,

		FilePath: filepath,

		patterns: []regexPattern{
			//{regexp.MustCompile(`\n`), skipHandler}, // newlines
			{regexp.MustCompile(`\s+`), skipHandler},                                     // whitespace
			{regexp.MustCompile(`//[^\n\r]*`), commentHandler},                           // single line comments (Unicode support)
			{regexp.MustCompile(`(?s)/\*.*?\*/`), commentHandler},                        // multi line comments (Unicode support)
			{regexp.MustCompile(`"[^"]*"`), stringHandler},                               // string literals (Unicode support)
			{regexp.MustCompile(`'(?:\\x[0-9a-fA-F]{2}|\\.|[\x00-\x7F])'`), byteHandler}, // byte literals (ASCII + escapes, 8-bit)
			{regexp.MustCompile(numeric.NumberPattern), numberHandler},                   // numbers (hex, octal, binary, float, integer)
			{regexp.MustCompile(`[a-zA-Z_][a-zA-Z0-9_]*`), identifierHandler},            // identifiers
			{regexp.MustCompile(`\+\+`), defaultHandler(tokens.PLUS_PLUS_TOKEN)},
			{regexp.MustCompile(`\-\-`), defaultHandler(tokens.MINUS_MINUS_TOKEN)},
			{regexp.MustCompile(`\->`), defaultHandler(tokens.ARROW_TOKEN)},
			{regexp.MustCompile(`\=>`), defaultHandler(tokens.FAT_ARROW_TOKEN)},
			{regexp.MustCompile(`::`), defaultHandler(tokens.SCOPE_TOKEN)}, // Add scope resolution operator before single colon
			{regexp.MustCompile(`!=`), defaultHandler(tokens.NOT_EQUAL_TOKEN)},
			{regexp.MustCompile(`\+=`), defaultHandler(tokens.PLUS_EQUALS_TOKEN)},
			{regexp.MustCompile(`-=`), defaultHandler(tokens.MINUS_EQUALS_TOKEN)},
			{regexp.MustCompile(`\*\*=`), defaultHandler(tokens.POW_EQUALS_TOKEN)}, // Must come before **
			{regexp.MustCompile(`\*=`), defaultHandler(tokens.MUL_EQUALS_TOKEN)},
			{regexp.MustCompile(`/=`), defaultHandler(tokens.DIV_EQUALS_TOKEN)},
			{regexp.MustCompile(`%=`), defaultHandler(tokens.MOD_EQUALS_TOKEN)},
			{regexp.MustCompile(`\^=`), defaultHandler(tokens.EXP_EQUALS_TOKEN)},
			{regexp.MustCompile(`\*\*`), defaultHandler(tokens.EXP_TOKEN)},
			{regexp.MustCompile(`\.\.\.`), defaultHandler(tokens.THREE_DOT_TOKEN)},
			{regexp.MustCompile(`\.\.=`), defaultHandler(tokens.RANGE_INCLUSIVE_TOKEN)},
			{regexp.MustCompile(`\.\.`), defaultHandler(tokens.RANGE_TOKEN)},
			{regexp.MustCompile(`&&`), defaultHandler(tokens.AND_TOKEN)},
			{regexp.MustCompile(`\|\|`), defaultHandler(tokens.OR_TOKEN)},
			{regexp.MustCompile(`&`), defaultHandler(tokens.BIT_AND_TOKEN)},
			{regexp.MustCompile(`\|`), defaultHandler(tokens.BIT_OR_TOKEN)},
			{regexp.MustCompile(`\^`), defaultHandler(tokens.BIT_XOR_TOKEN)},
			{regexp.MustCompile(`!`), defaultHandler(tokens.NOT_TOKEN)},
			{regexp.MustCompile(`\-`), defaultHandler(tokens.MINUS_TOKEN)},
			{regexp.MustCompile(`\+`), defaultHandler(tokens.PLUS_TOKEN)},
			{regexp.MustCompile(`\*`), defaultHandler(tokens.MUL_TOKEN)},
			{regexp.MustCompile(`/`), defaultHandler(tokens.DIV_TOKEN)},
			{regexp.MustCompile(`%`), defaultHandler(tokens.MOD_TOKEN)},
			{regexp.MustCompile(`<=`), defaultHandler(tokens.LESS_EQUAL_TOKEN)},
			{regexp.MustCompile(`<`), defaultHandler(tokens.LESS_TOKEN)},
			{regexp.MustCompile(`>=`), defaultHandler(tokens.GREATER_EQUAL_TOKEN)},
			{regexp.MustCompile(`>`), defaultHandler(tokens.GREATER_TOKEN)},
			{regexp.MustCompile(`==`), defaultHandler(tokens.DOUBLE_EQUAL_TOKEN)},
			{regexp.MustCompile(`:=`), defaultHandler(tokens.WALRUS_TOKEN)},
			{regexp.MustCompile(`=`), defaultHandler(tokens.EQUALS_TOKEN)},
			{regexp.MustCompile(`:`), defaultHandler(tokens.COLON_TOKEN)},
			{regexp.MustCompile(`;`), defaultHandler(tokens.SEMICOLON_TOKEN)},
			{regexp.MustCompile(`\(`), defaultHandler(tokens.OPEN_PAREN)},
			{regexp.MustCompile(`\)`), defaultHandler(tokens.CLOSE_PAREN)},
			{regexp.MustCompile(`\[`), defaultHandler(tokens.OPEN_BRACKET)},
			{regexp.MustCompile(`\]`), defaultHandler(tokens.CLOSE_BRACKET)},
			{regexp.MustCompile(`\{`), defaultHandler(tokens.OPEN_CURLY)},
			{regexp.MustCompile(`\}`), defaultHandler(tokens.CLOSE_CURLY)},
			{regexp.MustCompile(","), defaultHandler(tokens.COMMA_TOKEN)},
			{regexp.MustCompile(`\.`), defaultHandler(tokens.DOT_TOKEN)},
			{regexp.MustCompile(`\?\?`), defaultHandler(tokens.COALESCING_TOKEN)},
			{regexp.MustCompile(`\?`), defaultHandler(tokens.QUESTION_TOKEN)},
		},
	}
	return lex
}

func defaultHandler(token tokens.TOKEN) regexHandler {

	return func(lex *Lexer, _ *regexp.Regexp) {

		start := lex.Position
		lex.advance(string(token))
		end := lex.Position

		lex.push(tokens.NewToken(token, string(token), start, end))
	}
}

func identifierHandler(lex *Lexer, regex *regexp.Regexp) {
	identifier := regex.FindString(lex.remainder())
	start := lex.Position
	lex.advance(identifier)
	end := lex.Position
	if tokens.IsKeyword(identifier) {
		lex.push(tokens.NewToken(tokens.TOKEN(identifier), identifier, start, end))
	} else {
		lex.push(tokens.NewToken(tokens.IDENTIFIER_TOKEN, identifier, start, end))
	}
}

func numberHandler(lex *Lexer, regex *regexp.Regexp) {
	match := regex.FindString(lex.remainder())
	start := lex.Position
	lex.advance(match)
	end := lex.Position
	//find the number is a float or an integer
	lex.push(tokens.NewToken(tokens.NUMBER_TOKEN, match, start, end))
}

func stringHandler(lex *Lexer, regex *regexp.Regexp) {
	match := regex.FindString(lex.remainder())
	//exclude the quotes
	stringLiteral := match[1 : len(match)-1]
	start := lex.Position
	lex.advance(match)
	end := lex.Position

	// Process escape sequences
	processed := processStringEscapes(stringLiteral)
	lex.push(tokens.NewToken(tokens.STRING_TOKEN, processed, start, end))
}

// processStringEscapes processes escape sequences in string literals
// Supports: \n, \r, \t, \0, \\, \", \xHH (hex)
func processStringEscapes(s string) string {
	var result []byte
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				result = append(result, '\n')
				i += 2
			case 'r':
				result = append(result, '\r')
				i += 2
			case 't':
				result = append(result, '\t')
				i += 2
			case '0':
				result = append(result, 0)
				i += 2
			case '\\':
				result = append(result, '\\')
				i += 2
			case '"':
				result = append(result, '"')
				i += 2
			case 'x':
				// Hex escape: \xHH
				if i+3 < len(s) {
					hexStr := s[i+2 : i+4]
					value := 0
					valid := true
					for _, ch := range hexStr {
						value *= 16
						if ch >= '0' && ch <= '9' {
							value += int(ch - '0')
						} else if ch >= 'a' && ch <= 'f' {
							value += int(ch - 'a' + 10)
						} else if ch >= 'A' && ch <= 'F' {
							value += int(ch - 'A' + 10)
						} else {
							valid = false
							break
						}
					}
					if valid {
						result = append(result, byte(value))
						i += 4
						continue
					}
				}
				// Invalid hex escape, keep as-is
				result = append(result, s[i])
				i++
			default:
				// Unknown escape, keep as-is
				result = append(result, s[i])
				i++
			}
		} else {
			result = append(result, s[i])
			i++
		}
	}
	return string(result)
}

func byteHandler(lex *Lexer, regex *regexp.Regexp) {
	match := regex.FindString(lex.remainder())
	//exclude the quotes
	byteLiteral := match[1 : len(match)-1]
	start := lex.Position
	lex.advance(match)
	end := lex.Position

	// Parse escape sequences and validate size
	parsedValue, byteValue, err := parseByteEscape(byteLiteral)
	if err != nil {
		lex.diagnostics.Add(
			diagnostics.NewError(err.Error()).
				WithPrimaryLabel(source.NewLocation(&lex.FilePath, &start, &end),
					fmt.Sprintf("invalid byte literal '%s'", byteLiteral)),
		)
		lex.push(tokens.NewToken(tokens.BYTE_TOKEN, byteLiteral, start, end))
		return
	}

	// Validate that byte value fits in 8 bits (0-255)
	if byteValue > 255 {
		lex.diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("byte literal value %d does not fit in byte (0-255)", byteValue)).
				WithPrimaryLabel(source.NewLocation(&lex.FilePath, &start, &end),
					"value too large for byte type"),
		)
	}

	lex.push(tokens.NewToken(tokens.BYTE_TOKEN, parsedValue, start, end))
}

func commentHandler(lex *Lexer, regex *regexp.Regexp) {
	match := regex.FindString(lex.remainder())
	start := lex.Position
	lex.advance(match)
	end := lex.Position

	text := normalizeCommentText(match)
	lex.push(tokens.NewToken(tokens.COMMENT_TOKEN, text, start, end))
}

func normalizeCommentText(raw string) string {
	if len(raw) < 2 {
		return raw
	}
	if raw[0] == '/' && raw[1] == '/' {
		text := raw[2:]
		if len(text) > 0 && text[0] == ' ' {
			text = text[1:]
		}
		return text
	}
	if len(raw) >= 4 && raw[0] == '/' && raw[1] == '*' {
		return raw[2 : len(raw)-2]
	}
	return raw
}

// parseByteEscape parses escape sequences in byte literals and returns the parsed string and numeric value
func parseByteEscape(literal string) (string, int, error) {
	if len(literal) == 0 {
		return "", 0, fmt.Errorf("empty byte literal")
	}

	// Handle escape sequences
	if literal[0] == '\\' && len(literal) > 1 {
		switch literal[1] {
		case 'n':
			return "\n", 10, nil
		case 'r':
			return "\r", 13, nil
		case 't':
			return "\t", 9, nil
		case '0':
			return "\x00", 0, nil
		case '\\':
			return "\\", 92, nil
		case '\'':
			return "'", 39, nil
		case '"':
			return "\"", 34, nil
		case 'x':
			// Hex escape: \xHH
			if len(literal) < 4 {
				return "", 0, fmt.Errorf("incomplete hex escape sequence")
			}
			hexStr := literal[2:4]
			value := 0
			for _, ch := range hexStr {
				value *= 16
				if ch >= '0' && ch <= '9' {
					value += int(ch - '0')
				} else if ch >= 'a' && ch <= 'f' {
					value += int(ch - 'a' + 10)
				} else if ch >= 'A' && ch <= 'F' {
					value += int(ch - 'A' + 10)
				} else {
					return "", 0, fmt.Errorf("invalid hex digit in escape sequence")
				}
			}
			return fmt.Sprintf("\\x%02x", value), value, nil
		default:
			return "", 0, fmt.Errorf("unknown escape sequence '\\%c'", literal[1])
		}
	}

	// Regular character - must be ASCII
	if len(literal) == 1 {
		ch := literal[0]
		if ch > 127 {
			return "", 0, fmt.Errorf("byte literal must be ASCII (0-127)")
		}
		return literal, int(ch), nil
	}

	// Multi-byte UTF-8 character not allowed
	return "", 0, fmt.Errorf("byte literal must be a single ASCII character")
}

// skipHandler processes a token that should be skipped by the lexer.
func skipHandler(lex *Lexer, regex *regexp.Regexp) {
	match := regex.FindString(lex.remainder())
	lex.advance(match)
}

// Tokenize reads the source code from the specified file and tokenizes it.
func (lex *Lexer) Tokenize(debug bool) []tokens.Token {

	for !lex.atEOF() {

		matched := false

		for _, pattern := range lex.patterns {

			loc := pattern.regex.FindStringIndex(lex.remainder())

			if loc != nil && loc[0] == 0 {
				pattern.handler(lex, pattern.regex)
				matched = true
				break
			}
		}

		if !matched {
			// Add error diagnostic instead of panic
			tok := lex.remainder()[0]
			errMsg := fmt.Sprintf("unrecognized character '%c'", tok)
			lex.diagnostics.Add(
				diagnostics.NewError(errMsg).WithPrimaryLabel(source.NewLocation(
					&lex.FilePath,
					&lex.Position,
					&lex.Position,
				), ""),
			)
			// Skip the bad character and continue tokenizing to find more errors
			lex.advance(string(tok))
		}
	}

	lex.push(tokens.NewToken(tokens.EOF_TOKEN, "end of file", lex.Position, lex.Position))

	if debug {
		for _, token := range lex.Tokens {
			token.Debug(lex.FilePath)
		}
	}

	return lex.Tokens
}
