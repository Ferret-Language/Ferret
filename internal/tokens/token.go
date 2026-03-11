package tokens

import (
	"fmt"

	"compiler/internal/source"
)

type Kind string

const (
	ILLEGAL Kind = "ILLEGAL"
	EOF     Kind = "EOF"

	IDENT       Kind = "IDENT"
	NUMBER      Kind = "NUMBER"
	STRING      Kind = "STRING"
	CHAR        Kind = "CHAR"
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
	TAKE      Kind = "TAKE"
	OWN       Kind = "OWN"
	RAW       Kind = "RAW"
	MUT       Kind = "MUT"
	STATIC    Kind = "STATIC"
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
	"take":      TAKE,
	"own":       OWN,
	"raw":       RAW,
	"mut":       MUT,
	"static":    STATIC,
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
	case "bool", "char", "u8", "u16", "u32", "u64", "usize", "i8", "i16", "i32", "i64", "isize", "f32", "f64", "void":
		return true
	default:
		return false
	}
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
