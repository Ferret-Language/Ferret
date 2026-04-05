package tokens

import (
	"fmt"
	"strconv"
	"strings"

	"compiler/internal/core/abi"
	"compiler/internal/core/source"
)

type Kind string

const (
	ILLEGAL Kind = "ILLEGAL"
	EOF     Kind = "EOF"

	IDENT       Kind = "IDENT"
	NUMBER      Kind = "NUMBER"
	STRING      Kind = "STRING"
	CHAR        Kind = "CHAR"
	BYTE_CHAR   Kind = "BYTE_CHAR"
	DOC_COMMENT Kind = "DOC_COMMENT"

	ASSIGN       Kind = "="
	PLUS         Kind = "+"
	MINUS        Kind = "-"
	ASTERISK     Kind = "*"
	SLASH        Kind = "/"
	PERCENT      Kind = "%"
	PLUS_ASSIGN  Kind = "+="
	MINUS_ASSIGN Kind = "-="
	STAR_ASSIGN  Kind = "*="
	SLASH_ASSIGN Kind = "/="
	PCT_ASSIGN   Kind = "%="
	PLUS_PLUS    Kind = "++"
	MINUS_MINUS  Kind = "--"
	BANG         Kind = "!"
	QUESTION     Kind = "?"
	AT           Kind = "@"
	AMP          Kind = "&"
	TILDE        Kind = "~"
	LT           Kind = "<"
	GT           Kind = ">"
	EQ           Kind = "=="
	NEQ          Kind = "!="
	LE           Kind = "<="
	GE           Kind = ">="
	ANDAND       Kind = "&&"
	OROR         Kind = "||"
	BAR          Kind = "|"
	PIPE_ARROW   Kind = "|>"
	CARET        Kind = "^"
	CARET_ASSIGN Kind = "^="
	QQ           Kind = "??"
	BB           Kind = "!!"
	FATARROW     Kind = "=>"
	ARROW        Kind = "->"
	COLON        Kind = ":"
	DCOLON       Kind = "::"
	COMMA        Kind = ","
	DOT          Kind = "."
	DOTDOT       Kind = ".."
	DOTDOT_EQ    Kind = "..="
	ELLIPSIS     Kind = "..."
	HASH         Kind = "#"
	SEMICOLON    Kind = ";"

	LPAREN Kind = "("
	RPAREN Kind = ")"
	LBRACE Kind = "{"
	RBRACE Kind = "}"
	LBRACK Kind = "["
	RBRACK Kind = "]"

	IMPORT    Kind = "IMPORT"
	CONST     Kind = "CONST"
	TYPE      Kind = "TYPE"
	STRUCT    Kind = "STRUCT"
	INTERFACE Kind = "INTERFACE"
	ENUM      Kind = "ENUM"
	UNION     Kind = "UNION"
	ERROR     Kind = "ERROR"
	FN        Kind = "FN"
	TEST      Kind = "TEST"
	LET       Kind = "LET"
	IF        Kind = "IF"
	ELSE      Kind = "ELSE"
	MATCH     Kind = "MATCH"
	FOR       Kind = "FOR"
	WHILE     Kind = "WHILE"
	BREAK     Kind = "BREAK"
	CONTINUE  Kind = "CONTINUE"
	RETURN    Kind = "RETURN"
	AS        Kind = "AS"
	IS        Kind = "IS"
	MUT       Kind = "MUT"
	COMPTIME  Kind = "COMPTIME"
	LOCK      Kind = "LOCK"
	DEFER     Kind = "DEFER"
	PANIC     Kind = "PANIC"
	RELEASE   Kind = "RELEASE"
	CATCH     Kind = "CATCH"
	NONE      Kind = "NONE"
	UNSAFE    Kind = "UNSAFE"
)

var keywords = map[string]Kind{
	"import":    IMPORT,
	"const":     CONST,
	"type":      TYPE,
	"struct":    STRUCT,
	"interface": INTERFACE,
	"enum":      ENUM,
	"union":     UNION,
	"error":     ERROR,
	"fn":        FN,
	"test":      TEST,
	"let":       LET,
	"if":        IF,
	"else":      ELSE,
	"match":     MATCH,
	"for":       FOR,
	"while":     WHILE,
	"break":     BREAK,
	"continue":  CONTINUE,
	"return":    RETURN,
	"as":        AS,
	"is":        IS,
	"mut":       MUT,
	"comptime":  COMPTIME,
	"lock":      LOCK,
	"defer":     DEFER,
	"panic":     PANIC,
	"release":   RELEASE,
	"catch":     CATCH,
	"none":      NONE,
	"unsafe":    UNSAFE,
}

func LookupIdent(ident string) Kind {
	if kind, ok := keywords[ident]; ok {
		return kind
	}
	return IDENT
}

func IsKeyword(ident string) bool {
	_, ok := keywords[ident]
	return ok
}

func IsBuiltinType(name string) bool {
	switch name {
	case "bool", "char", "str", "usize", "isize", "f32", "f64", "void":
		return true
	default:
		_, _, ok := ParseIntegerBuiltin(name)
		return ok
	}
}

func ParseIntegerBuiltin(name string) (signed bool, bits int, ok bool) {
	switch name {
	case "isize":
		return true, abi.SizeBits(), true
	case "usize":
		return false, abi.SizeBits(), true
	}
	if len(name) < 2 {
		return false, 0, false
	}
	switch name[0] {
	case 'i':
		signed = true
	case 'u':
		signed = false
	default:
		return false, 0, false
	}
	if strings.HasPrefix(name, "i0") || strings.HasPrefix(name, "u0") {
		return false, 0, false
	}
	n, err := strconv.Atoi(name[1:])
	if err != nil || n < 8 {
		return false, 0, false
	}
	if n&(n-1) != 0 {
		return false, 0, false
	}
	return signed, n, true
}

type Token struct {
	Kind    Kind
	Literal string
	Start   source.Position
	End     source.Position
}

func (t Token) String() string {
	return fmt.Sprintf("%s(%q)", t.Kind, t.Literal)
}
