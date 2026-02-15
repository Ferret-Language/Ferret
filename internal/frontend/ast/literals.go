package ast

import "compiler/internal/source"

type LiteralKind int

const (
	INT LiteralKind = iota
	FLOAT
	IMAG
	STRING
	BYTE
	CHAR
	BOOL
	NONE // represents the 'none' keyword
)

// BasicLit represents a literal of basic type (int, float, string, bool, none)
type BasicLit struct {
	Kind  LiteralKind
	Value string // the literal value as a string
	source.Location
}

func (b *BasicLit) INode()                {} // Implements Node interface
func (b *BasicLit) Expr()                 {} // Expr is a marker interface for all expressions
func (b *BasicLit) Loc() *source.Location { return &b.Location }

// CompositeLit represents a composite literal (array, struct, or map)
// Examples:
//   - Array:  []i32{1, 2, 3}
//   - Struct: Point{.x = 1, .y = 2}
//   - Map:    map[str]i32{"a" => 1, "b" => 2}
//
// For arrays, Elts contains plain Expression values.
// For structs and maps, Elts contains KeyValueExpr nodes.
type CompositeLit struct {
	Type TypeNode     // type of the composite literal (AST node, can be nil for inferred types)
	Elts []Expression // list of composite elements (can be plain values or KeyValueExpr)
	source.Location
}

func (c *CompositeLit) INode()                {} // Implements Node interface
func (c *CompositeLit) Expr()                 {} // Expr is a marker interface for all expressions
func (c *CompositeLit) Loc() *source.Location { return &c.Location }

// KeyValueExpr represents a key-value pair in a composite literal
// Used for:
//   - Struct fields: .field = value  (Key is identifier, Value is expression)
//   - Map entries:   key => value    (Key and Value are both expressions)
type KeyValueExpr struct {
	Key   Expression // field name (for structs) or map key (for maps)
	Value Expression // field value or map value
	source.Location
}

func (k *KeyValueExpr) INode()                {} // Implements Node interface
func (k *KeyValueExpr) Expr()                 {} // Expr is a marker interface for all expressions
func (k *KeyValueExpr) Loc() *source.Location { return &k.Location }

// FuncLit represents a function literal (anonymous function/lambda)
type FuncLit struct {
	ID    IdentifierExpr // unique identifier for the function literal
	Type  *FuncType      // function signature (AST node)
	Body  *Block         // function body
	Scope SymbolTable    // Symbol table for function scope (filled during collection)
	source.Location
}

func (f *FuncLit) INode()                {} // Implements Node interface
func (f *FuncLit) Expr()                 {} // Expr is a marker interface for all expressions
func (f *FuncLit) Loc() *source.Location { return &f.Location }
