package ast

import (
	"compiler/internal/source"
)

// BlockStmt represents a block of statements
type Block struct {
	Nodes []Node
	Scope SymbolTable // Symbol table for this block scope (filled during collection)
	source.Location
}

func (b *Block) INode()                {} // Implements Node interface
func (b *Block) Block()                {} // Block is a marker method for statements
func (b *Block) Loc() *source.Location { return &b.Location }

// FuncDecl represents both named and anonymous function declarations
type FuncDecl struct {
	Name  *IdentifierExpr
	Type  *FuncType // Function signature
	Body  *Block    // Function body
	Scope SymbolTable
	Doc   *CommentGroup
	source.Location
}

func (f *FuncDecl) INode()                {} // Implements Node interface
func (f *FuncDecl) Block()                {} // Block is a marker interface for all expressions
func (f *FuncDecl) Decl()                 {} // Decl is a marker interface for all declarations
func (f *FuncDecl) Loc() *source.Location { return &f.Location }

// MethodDecl represents a method declaration with a receiver
// Example: fn (r Rect) area() -> f64 { return r.width * r.height; }
type MethodDecl struct {
	Receiver *Field          // method receiver (e.g., r Rect)
	Name     *IdentifierExpr // method name
	Type     *FuncType       // function signature (parameters and return type)
	Body     *Block          // method body
	Scope    SymbolTable
	Doc      *CommentGroup
	source.Location
}

func (m *MethodDecl) INode()                {} // Implements Node interface
func (m *MethodDecl) Block()                {} // Block is a marker interface for block constructs
func (m *MethodDecl) Decl()                 {} // Decl is a marker interface for all declarations
func (m *MethodDecl) Loc() *source.Location { return &m.Location }

// IfStmt represents an if statement with optional else and else-if branches
type IfStmt struct {
	Cond  Expression  // condition
	Body  *Block      // then branch
	Else  Node        // else branch (can be another IfStmt or Block)
	Scope SymbolTable // Symbol table for if scope (filled during collection)
	source.Location
}

func (i *IfStmt) INode()                {} // Implements Node interface
func (i *IfStmt) Block()                {} // Block is a marker interface for all statements
func (i *IfStmt) Loc() *source.Location { return &i.Location }

// ForStmt represents a for-in loop: for i in range { }
// Range can be start..end or start..end:incr
// Iterator is a VarDecl that binds new loop variables scoped to the loop body.
type ForStmt struct {
	Iterator Node        // loop variable declaration
	Range    Expression  // range expression (e.g., 0..10 or 0..10:2)
	Body     *Block      // loop body
	Scope    SymbolTable // Symbol table for for loop scope (filled during collection)
	source.Location
}

func (f *ForStmt) INode()                {} // Implements Node interface
func (f *ForStmt) Block()                {} // Block is a marker interface for all statements
func (f *ForStmt) Loc() *source.Location { return &f.Location }

// WhileStmt represents a while loop: while cond { }
type WhileStmt struct {
	Cond  Expression  // condition (required; may be nil during error recovery)
	Body  *Block      // loop body
	Scope SymbolTable // Symbol table for while loop scope (filled during collection)
	source.Location
}

func (w *WhileStmt) INode()                {} // Implements Node interface
func (w *WhileStmt) Block()                {} // Block is a marker interface for all statements
func (w *WhileStmt) Loc() *source.Location { return &w.Location }

// MatchStmt represents a match statement (Ferret's pattern matching)
type MatchStmt struct {
	Expr  Expression    // expression to match
	Cases []*CaseClause // match cases
	source.Location
}

func (w *MatchStmt) INode()                {} // Implements Node interface
func (w *MatchStmt) Block()                {} // Block is a marker interface for all statements
func (w *MatchStmt) Loc() *source.Location { return &w.Location }

// CaseClause represents a case in a when statement
type CaseClause struct {
	Pattern Expression // pattern to match (nil for default case)
	Body    *Block     // case body
	source.Location
}
