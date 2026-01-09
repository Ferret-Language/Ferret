package tokens

import (
	"fmt"
	"os"

	"compiler/colors"
	"compiler/internal/source"
	"compiler/internal/types"
	"compiler/internal/utils/numeric"
)

type TOKEN string

const (
	//keywords
	LET_TOKEN        TOKEN = "let"
	CONST_TOKEN      TOKEN = "const"
	TYPE_TOKEN       TOKEN = "type"
	IF_TOKEN         TOKEN = "if"
	ELSE_TOKEN       TOKEN = "else"
	FOR_TOKEN        TOKEN = "for"
	IN_TOKEN         TOKEN = "in"
	FOREACH_TOKEN    TOKEN = "foreach"
	WHILE_TOKEN      TOKEN = "while"
	DO_TOKEN         TOKEN = "do"
	MATCH_TOKEN      TOKEN = "match"
	IDENTIFIER_TOKEN TOKEN = "identifier"
	PRIVATE_TOKEN    TOKEN = "priv"
	RETURN_TOKEN     TOKEN = "return"
	BREAK_TOKEN      TOKEN = "break"
	CONTINUE_TOKEN   TOKEN = "continue"
	IMPORT_TOKEN     TOKEN = "import"
	AS_TOKEN         TOKEN = "as"
	MODULE_TOKEN     TOKEN = "mod"
	CATCH_TOKEN      TOKEN = "catch"
	//data types
	NUMBER_TOKEN    TOKEN = "numeric literal"
	STRING_TOKEN    TOKEN = "string literal"
	BYTE_TOKEN      TOKEN = "byte literal"
	COMMENT_TOKEN   TOKEN = "comment"
	STRUCT_TOKEN    TOKEN = TOKEN(types.TYPE_STRUCT)
	FUNCTION_TOKEN  TOKEN = TOKEN(types.TYPE_FUNC)
	INTERFACE_TOKEN TOKEN = TOKEN(types.TYPE_INTERFACE)
	UNION_TOKEN     TOKEN = "union"
	IS_TOKEN        TOKEN = "is"
	ENUM_TOKEN      TOKEN = TOKEN(types.TYPE_ENUM)
	MAP_TOKEN       TOKEN = TOKEN(types.TYPE_MAP)

	THREE_DOT_TOKEN TOKEN = "..."

	//array range operators
	RANGE_TOKEN           TOKEN = ".."  // exclusive end (0..10 -> [0,1,2,...,9])
	RANGE_INCLUSIVE_TOKEN TOKEN = "..=" // inclusive end (0..=10 -> [0,1,2,...,10])
	//increment and decrement
	PLUS_PLUS_TOKEN   TOKEN = "++"
	MINUS_MINUS_TOKEN TOKEN = "--"
	//Binary operators
	AND_TOKEN TOKEN = "&&"
	OR_TOKEN  TOKEN = "||"
	//bitwise operators
	BIT_AND_TOKEN TOKEN = "&"
	BIT_OR_TOKEN  TOKEN = "|"
	BIT_XOR_TOKEN TOKEN = "^"
	//reference operators
	MUT_TOKEN  TOKEN = "mut"
	STAR_TOKEN TOKEN = "*" // dereference operator (also used for multiplication)
	//unary operators
	NOT_TOKEN TOKEN = "!"
	AT_TOKEN  TOKEN = "@"
	//arithmetic operators
	EXP_TOKEN   TOKEN = "**"
	MINUS_TOKEN TOKEN = "-"
	PLUS_TOKEN  TOKEN = "+"
	MUL_TOKEN   TOKEN = "*" // multiplication (same token as STAR_TOKEN for dereference)
	DIV_TOKEN   TOKEN = "/"
	MOD_TOKEN   TOKEN = "%"
	//logical operators
	LESS_EQUAL_TOKEN    TOKEN = "<="
	GREATER_EQUAL_TOKEN TOKEN = ">="
	NOT_EQUAL_TOKEN     TOKEN = "!="
	DOUBLE_EQUAL_TOKEN  TOKEN = "=="
	LESS_TOKEN          TOKEN = "<"
	GREATER_TOKEN       TOKEN = ">"
	//assignment
	SCOPE_TOKEN        TOKEN = "::"
	WALRUS_TOKEN       TOKEN = ":="
	COLON_TOKEN        TOKEN = ":"
	EQUALS_TOKEN       TOKEN = "="
	PLUS_EQUALS_TOKEN  TOKEN = "+="
	MINUS_EQUALS_TOKEN TOKEN = "-="
	MUL_EQUALS_TOKEN   TOKEN = "*="
	DIV_EQUALS_TOKEN   TOKEN = "/="
	MOD_EQUALS_TOKEN   TOKEN = "%="
	EXP_EQUALS_TOKEN   TOKEN = "^="
	POW_EQUALS_TOKEN   TOKEN = "**="
	//delimiters
	OPEN_PAREN       TOKEN = "("
	CLOSE_PAREN      TOKEN = ")"
	OPEN_BRACKET     TOKEN = "["
	CLOSE_BRACKET    TOKEN = "]"
	OPEN_CURLY       TOKEN = "{"
	CLOSE_CURLY      TOKEN = "}"
	COMMA_TOKEN      TOKEN = ","
	DOT_TOKEN        TOKEN = "."
	SEMICOLON_TOKEN  TOKEN = ";"
	ARROW_TOKEN      TOKEN = "->"
	FAT_ARROW_TOKEN  TOKEN = "=>"
	COALESCING_TOKEN TOKEN = "??"
	QUESTION_TOKEN   TOKEN = "?"

	EOF_TOKEN TOKEN = "end_of_file"
)

var keyWordsMap map[TOKEN]bool = map[TOKEN]bool{
	LET_TOKEN:       true,
	CONST_TOKEN:     true,
	IF_TOKEN:        true,
	ELSE_TOKEN:      true,
	FOR_TOKEN:       true,
	IN_TOKEN:        true,
	WHILE_TOKEN:     true,
	MATCH_TOKEN:     true,
	TYPE_TOKEN:      true,
	STRUCT_TOKEN:    true,
	PRIVATE_TOKEN:   true,
	INTERFACE_TOKEN: true,
	UNION_TOKEN:     true,
	IS_TOKEN:        true,
	ENUM_TOKEN:      true,
	MAP_TOKEN:       true,
	CATCH_TOKEN:     true,
	FUNCTION_TOKEN:  true,
	RETURN_TOKEN:    true,
	BREAK_TOKEN:     true,
	CONTINUE_TOKEN:  true,
	IMPORT_TOKEN:    true,
	AS_TOKEN:        true,
	MUT_TOKEN:       true,
}

var builtinTypes map[string]bool = map[string]bool{
	string(types.TYPE_U8):   true,
	string(types.TYPE_U16):  true,
	string(types.TYPE_U32):  true,
	string(types.TYPE_U64):  true,
	string(types.TYPE_U128): true,
	string(types.TYPE_U256): true,

	string(types.TYPE_I8):   true,
	string(types.TYPE_I16):  true,
	string(types.TYPE_I32):  true,
	string(types.TYPE_I64):  true,
	string(types.TYPE_I128): true,
	string(types.TYPE_I256): true,

	string(types.TYPE_F32):  true,
	string(types.TYPE_F64):  true,
	string(types.TYPE_F128): true,
	string(types.TYPE_F256): true,

	string(types.TYPE_BOOL): true,

	string(types.TYPE_STRING): true,

	string(types.TYPE_BYTE): true,

	string(types.TYPE_VOID): true,
}

func IsKeyword(token string) bool {
	if _, ok := keyWordsMap[TOKEN(token)]; ok {
		return true
	}
	return false
}

// IsNumber checks if a token represents any valid numeric literal
// Supports: decimal, hex (0x), octal (0o), binary (0b), floats, scientific notation
// Allows underscores for readability and handles large integers (128/256-bit)
func IsNumber(token string) bool {
	if len(token) == 0 {
		return false
	}

	// Check all supported number formats using numeric package regexes
	return numeric.IsDecimal(token) ||
		numeric.IsHexadecimal(token) ||
		numeric.IsOctal(token) ||
		numeric.IsBinary(token) ||
		numeric.IsFloat(token)
}

// IsInteger checks if a token is an integer (not a float)
func IsInteger(token string) bool {
	if len(token) == 0 {
		return false
	}

	return numeric.IsDecimal(token) ||
		numeric.IsHexadecimal(token) ||
		numeric.IsOctal(token) ||
		numeric.IsBinary(token)
}

// IsFloat checks if a token is a floating-point number
func IsFloat(token string) bool {
	return numeric.IsFloat(token)
}

func IsString(token string) bool {
	if len(token) < 2 {
		return false
	}
	return token[0] == '"' && token[len(token)-1] == '"'
}

// IsCharLiteral checks if a token is a character literal
func IsCharLiteral(token string) bool {
	if len(token) < 2 {
		return false
	}
	return token[0] == '\'' && token[len(token)-1] == '\''
}

// IsComment checks if a token starts with a comment
func IsComment(token string) bool {
	return len(token) >= 2 && token[0] == '/' && token[1] == '/'
}

func IsBuiltinType(token string) bool {
	if _, ok := builtinTypes[token]; ok {
		return true
	}
	return false
}

type Token struct {
	Kind  TOKEN
	Value string
	Start source.Position
	End   source.Position
}

func (t *Token) Debug(filename string) {
	colors.GREY.Fprintf(os.Stderr, "%s:%d:%d ", filename, t.Start.Line, t.Start.Column)
	if t.Value == string(t.Kind) {
		fmt.Fprintf(os.Stderr, "%q\n", t.Value)
	} else {
		fmt.Fprintf(os.Stderr, "%q ('%v')\n", t.Value, t.Kind)
	}
}

func NewToken(kind TOKEN, value string, start source.Position, end source.Position) Token {
	return Token{
		Kind:  kind,
		Value: value,
		Start: start,
		End:   end,
	}
}
