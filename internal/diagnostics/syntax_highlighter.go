package diagnostics

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"

	"compiler/colors"
	"compiler/internal/tokens"
)

type keytype int

const (
	STRING keytype = iota
	CHAR
	NUMBER
	KEYWORD
	TYPE
	TEXT
	OP
	COMMENT
)

var colorMap = map[keytype]colors.COLOR{
	STRING:  colors.LIGHT_GREEN,
	CHAR:    colors.LIGHT_ORANGE,
	NUMBER:  colors.LIGHT_ORANGE,
	KEYWORD: colors.PURPLE,
	TEXT:    colors.WHITE,
	OP:      colors.WHITE,
	COMMENT: colors.GREY,
	TYPE:    colors.CYAN,
}

// SyntaxHighlighter provides syntax highlighting for Ferret code snippets
type SyntaxHighlighter struct {
	enabled bool
}

// NewSyntaxHighlighter creates a new syntax highlighter
func NewSyntaxHighlighter(enabled bool) *SyntaxHighlighter {
	return &SyntaxHighlighter{enabled: enabled}
}

// Enable turns on syntax highlighting
func (sh *SyntaxHighlighter) Enable() {
	sh.enabled = true
}

// Disable turns off syntax highlighting
func (sh *SyntaxHighlighter) Disable() {
	sh.enabled = false
}

// IsEnabled returns whether syntax highlighting is enabled
func (sh *SyntaxHighlighter) IsEnabled() bool {
	return sh.enabled
}

// Token represents a highlighted token
type Token struct {
	Text  string
	Color colors.COLOR
}

// Highlight applies syntax highlighting to a line of code
// Returns a slice of tokens with their associated colors
func (sh *SyntaxHighlighter) Highlight(line string) []Token {
	if !sh.enabled {
		return []Token{{Text: line, Color: colors.WHITE}}
	}

	var tokensSlice []Token
	i := 0

	for i < len(line) {
		// Skip whitespace
		if unicode.IsSpace(rune(line[i])) {
			start := i
			for i < len(line) && unicode.IsSpace(rune(line[i])) {
				i++
			}
			tokensSlice = append(tokensSlice, Token{Text: line[start:i], Color: colorMap[TEXT]})
			continue
		}

		// String literals (double quotes)
		if line[i] == '"' {
			start := i
			i++
			for i < len(line) && line[i] != '"' {
				if line[i] == '\\' && i+1 < len(line) {
					i += 2 // Skip escaped character
				} else {
					i++
				}
			}
			if i < len(line) {
				i++ // Include closing quote
			}
			tokensSlice = append(tokensSlice, Token{Text: line[start:i], Color: colorMap[STRING]})
			continue
		}

		// Mutable reference operator (&') should not start a char literal
		if line[i] == '&' && i+1 < len(line) && line[i+1] == '\'' {
			tokensSlice = append(tokensSlice, Token{Text: line[i : i+2], Color: colorMap[OP]})
			i += 2
			continue
		}

		// Character literals (single quotes)
		if line[i] == '\'' {
			start := i
			i++
			for i < len(line) && line[i] != '\'' {
				if line[i] == '\\' && i+1 < len(line) {
					i += 2 // Skip escaped character
				} else {
					i++
				}
			}
			if i < len(line) {
				i++ // Include closing quote
			}
			tokensSlice = append(tokensSlice, Token{Text: line[start:i], Color: colorMap[CHAR]})
			continue
		}

		// Numbers (integers and floats)
		if unicode.IsDigit(rune(line[i])) || (line[i] == '.' && i+1 < len(line) && unicode.IsDigit(rune(line[i+1]))) {
			start := i
			for i < len(line) && (unicode.IsDigit(rune(line[i])) || line[i] == '.' || line[i] == '_') {
				i++
			}
			tokensSlice = append(tokensSlice, Token{Text: line[start:i], Color: colorMap[NUMBER]})
			continue
		}

		// Identifiers (keywords, types, or regular identifiers)
		if unicode.IsLetter(rune(line[i])) || line[i] == '_' {
			start := i
			for i < len(line) && (unicode.IsLetter(rune(line[i])) || unicode.IsDigit(rune(line[i])) || line[i] == '_') {
				i++
			}
			word := line[start:i]

			// Determine color based on token type using tokens package
			var color colors.COLOR
			if tokens.IsKeyword(word) {
				color = colorMap[KEYWORD]
			} else if tokens.IsBuiltinType(word) {
				color = colorMap[TYPE]
			} else {
				color = colors.WHITE // Regular identifiers
			}
			tokensSlice = append(tokensSlice, Token{Text: word, Color: color})
			continue
		}

		// Comments
		if i+1 < len(line) && line[i] == '/' && line[i+1] == '/' {
			tokensSlice = append(tokensSlice, Token{Text: line[i:], Color: colorMap[COMMENT]})
			break
		}

		// Operators and punctuation (keep white)
		start := i
		i++
		tokensSlice = append(tokensSlice, Token{Text: line[start:i], Color: colorMap[OP]})
	}

	return tokensSlice
}

// HighlightLine returns a highlighted line as a string ready for printing
func (sh *SyntaxHighlighter) HighlightLine(line string) string {
	if !sh.enabled {
		return line
	}

	tokens := sh.Highlight(line)
	var result strings.Builder

	for _, token := range tokens {
		token.Color.Fprint(&result, token.Text)
	}

	return result.String()
}

// HighlightWithColor applies syntax highlighting and returns colored string
// This is useful for printing directly to output
func (sh *SyntaxHighlighter) HighlightWithColor(line string, writer io.Writer) {
	if !sh.enabled {
		fmt.Fprint(writer, line)
		return
	}

	tokens := sh.Highlight(line)
	for _, token := range tokens {
		token.Color.Fprint(writer, token.Text)
	}
}

// HighlightWithBaseColor applies syntax highlighting with a base color override.
// Tokens that are normally white inherit the base color.
func (sh *SyntaxHighlighter) HighlightWithBaseColor(line string, writer io.Writer, base colors.COLOR) {
	if !sh.enabled {
		if base != "" {
			base.Fprint(writer, line)
		} else {
			fmt.Fprint(writer, line)
		}
		return
	}

	tokens := sh.Highlight(line)
	for _, token := range tokens {
		color := token.Color
		if base != "" && color == colors.WHITE {
			color = base
		}
		color.Fprint(writer, token.Text)
	}
}

// StripColors removes ANSI color codes from a string
// Useful for testing or when colors are not desired
var colorCodeRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func StripColors(s string) string {
	return colorCodeRegex.ReplaceAllString(s, "")
}
