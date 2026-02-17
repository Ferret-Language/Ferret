package ast

import (
	"compiler/internal/source"
)

// DataType is a marker interface for type expressions
// Types are NOT expressions - they are a separate syntactic category
type DataType interface {
	Node
	TypeExpr()
}

type Invalid struct {
	source.Location
}

func (i *Invalid) INode()                {} // Implements Node interface
func (i *Invalid) Expr()                 {} // Can be used as expression (for error recovery)
func (i *Invalid) TypeExpr()             {} // Type nodes implement TypeExpr
func (i *Invalid) Loc() *source.Location { return &i.Location }

// UserDefinedType represents an identifier (can be a type name or variable name)
type UserDefinedType struct {
	Name string
	source.Location
}

func (i *UserDefinedType) INode()                {} // Implements Node interface
func (i *UserDefinedType) Expr()                 {} // Can be used as expression (variable reference)
func (i *UserDefinedType) TypeExpr()             {} // Can be used as type (type name)
func (i *UserDefinedType) Loc() *source.Location { return &i.Location }

// ArrayType represents array type [size]ElementType or []ElementType
type ArrayType struct {
	Len    Expression // nil means dynamic array []T, otherwise [N]T
	ElType TypeNode   // element type
	source.Location
}

func (a *ArrayType) INode()                {} // Implements Node interface
func (a *ArrayType) TypeExpr()             {} // Type nodes implement TypeExpr
func (a *ArrayType) Loc() *source.Location { return &a.Location }

// OptionalType represents optional/nullable type ?T
type OptionalType struct {
	Base TypeNode // The base type that is optional
	source.Location
}

func (o *OptionalType) INode()                {} // Implements Node interface
func (o *OptionalType) TypeExpr()             {} // Type nodes implement TypeExpr
func (o *OptionalType) Loc() *source.Location { return &o.Location }

// UnionType represents union type union { T1, T2, ..., TN }
type UnionType struct {
	Variants []TypeNode // The types in the union
	source.Location
}

func (u *UnionType) INode()                {} // Implements Node interface
func (u *UnionType) TypeExpr()             {} // Type nodes implement TypeExpr
func (u *UnionType) Loc() *source.Location { return &u.Location }

// ReferenceType represents reference type &T (C#-style reference)
type ReferenceType struct {
	Base    TypeNode // The base type being referenced
	Mutable bool     // True if this is a mutable reference
	source.Location
}

func (r *ReferenceType) INode()                {} // Implements Node interface
func (r *ReferenceType) TypeExpr()             {} // Type nodes implement TypeExpr
func (r *ReferenceType) Loc() *source.Location { return &r.Location }

// HeapType represents boxed heap ownership type #T
type HeapType struct {
	Base TypeNode // The inner payload type
	source.Location
}

func (h *HeapType) INode()                {} // Implements Node interface
func (h *HeapType) TypeExpr()             {} // Type nodes implement TypeExpr
func (h *HeapType) Loc() *source.Location { return &h.Location }

// ResultType represents error-returning type T ! E
type ResultType struct {
	Value TypeNode // The value type
	Error TypeNode // The error type
	source.Location
}

func (e *ResultType) INode()                {} // Implements Node interface
func (e *ResultType) TypeExpr()             {} // Type nodes implement TypeExpr
func (e *ResultType) Loc() *source.Location { return &e.Location }

// Field represents a single field in a struct or parameter in a function
type Field struct {
	Name       *IdentifierExpr // field/parameter names (can be nil for anonymous fields)
	Type       TypeNode        // field type
	Value      Expression      // initial value (for struct literals), nil otherwise
	IsVariadic bool            // true if this is a variadic parameter (...type)
	IsMove     bool            // true if parameter is move-qualified (@T)
	// ReceiverMode is used by interface methods to constrain required method receiver kind.
	// Value is the default when no prefix is provided.
	ReceiverMode InterfaceReceiverMode
	Default      Expression // default parameter value (functions only), nil otherwise
	source.Location
}

func (f *Field) INode()                {} // Implements Node interface
func (f *Field) Loc() *source.Location { return &f.Location }

// StructType represents a struct type definition
type StructType struct {
	Fields []Field
	ID     string // Unique ID for anonymous structs (e.g., in method receivers)
	source.Location
}

func (s *StructType) INode() {} // Implements Node interface
// func (s *StructType) Expr()                 {} // Can be used in expression context (for anonymous struct types)
func (s *StructType) TypeExpr()             {} // Type nodes implement TypeExpr
func (s *StructType) Loc() *source.Location { return &s.Location }

// InterfaceType represents an interface type definition
type InterfaceType struct {
	Methods []Field // interface methods
	ID      string  // Unique ID for anonymous interface
	source.Location
}

// InterfaceReceiverMode defines the receiver category required by an interface method.
type InterfaceReceiverMode uint8

const (
	InterfaceReceiverValue  InterfaceReceiverMode = iota // Method value receiver: `Method(...)`
	InterfaceReceiverRef                                 // Shared ref receiver: `&Method(...)`
	InterfaceReceiverMutRef                              // Mutable ref receiver: `&mut Method(...)`
	InterfaceReceiverMove                                // Move receiver: `@Method(...)`
	InterfaceReceiverAny                                 // Any receiver mode: `~Method(...)`
)

func (i *InterfaceType) INode()                {} // Implements Node interface
func (i *InterfaceType) TypeExpr()             {} // Type nodes implement TypeExpr
func (i *InterfaceType) Loc() *source.Location { return &i.Location }

// FuncType represents a function type signature
type FuncType struct {
	Params []Field  // function parameters
	Result TypeNode // return types (can be nil for no return)
	source.Location
}

func (f *FuncType) INode()                {} // Implements Node interface
func (f *FuncType) TypeExpr()             {} // Type nodes implement TypeExpr
func (f *FuncType) Loc() *source.Location { return &f.Location }

// MapType represents a map type map[KeyType]ValueType
type MapType struct {
	Key   TypeNode // key type
	Value TypeNode // value type
	source.Location
}

func (m *MapType) INode()                {} // Implements Node interface
func (m *MapType) Expr()                 {} // Can be used in expression context (for composite literals)
func (m *MapType) TypeExpr()             {} // Type nodes implement TypeExpr
func (m *MapType) Loc() *source.Location { return &m.Location }

// EnumType represents an enum type definition
type EnumType struct {
	Variants []Field
	ID       string // Unique ID for anonymous enum
	source.Location
}

func (e *EnumType) INode()                {} // Implements Node interface
func (e *EnumType) Expr()                 {} // Can be used in expression context (for anonymous enum types)
func (e *EnumType) TypeExpr()             {} // Type nodes implement TypeExpr
func (e *EnumType) Loc() *source.Location { return &e.Location }
