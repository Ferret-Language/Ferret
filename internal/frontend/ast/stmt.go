package ast

import (
	"encoding/json"
	"fmt"
	"os"

	"compiler/internal/source"
	"compiler/internal/tokens"
)

// Module represents a Ferret source file (pure syntax tree)
// This is the output of the Parser phase - contains only syntactic information.
// Semantic information (imports, symbols, types) lives in SemanticModel in the context package.
type Module struct {
	FullPath   string // the physical full path to the file
	ImportPath string // the logical path to the module
	Alias      string // module alias
	Nodes      []Node // top-level declarations and statements
	source.Location
}

func (m *Module) SaveAST() error {
	file, err := os.Create(m.FullPath + ".ast.json")
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ") // pretty-print
	if err := encoder.Encode(m); err != nil {
		return fmt.Errorf("failed to encode AST to JSON: %w", err)
	}
	return nil
}

func (m *Module) INode()                {} // Implements Node interface
func (m *Module) Stmt()                 {} // Stmt is a marker interface for all statements
func (m *Module) Loc() *source.Location { return &m.Location }

// DeclStmt represents a declaration statement
type DeclStmt struct {
	Decl Decl // can be VarDecl, ConstDecl, TypeDecl, FuncDecl
	source.Location
}

func (d *DeclStmt) INode()                {} // Implements Node interface
func (d *DeclStmt) Stmt()                 {} // Stmt is a marker interface for all statements
func (d *DeclStmt) Loc() *source.Location { return &d.Location }

type DeclItem struct {
	Name  *IdentifierExpr // variable name
	Type  TypeNode        // explicit type (can be nil for type inference)
	Value Expression      // initial value (can be nil)
}

// VarDecl represents a variable declaration (let keyword)
type VarDecl struct {
	Decls []DeclItem
	Doc   *CommentGroup
	source.Location
}

func (v *VarDecl) INode()                {} // Implements Node interface
func (v *VarDecl) Stmt()                 {} // Stmt is a marker interface for all statements
func (v *VarDecl) Decl()                 {} // Decl is a marker interface for all declarations
func (v *VarDecl) Loc() *source.Location { return &v.Location }

// ConstDecl represents a constant declaration (const keyword)
type ConstDecl struct {
	Decls []DeclItem
	Doc   *CommentGroup
	source.Location
}

func (c *ConstDecl) INode()                {} // Implements Node interface
func (c *ConstDecl) Stmt()                 {} // Stmt is a marker interface for all statements
func (c *ConstDecl) Decl()                 {} // Decl is a marker interface for all declarations
func (c *ConstDecl) Loc() *source.Location { return &c.Location }

// AssignStmt represents an assignment statement
type AssignStmt struct {
	Lhs Expression    // left-hand side (can be IdentifierExpr, SelectorExpr, etc.)
	Rhs Expression    // right-hand side
	Op  *tokens.Token // assignment operator (nil for =, otherwise +=, -=, etc., or ++/-- for increment/decrement)
	source.Location
}

func (a *AssignStmt) INode()                {} // Implements Node interface
func (a *AssignStmt) Stmt()                 {} // Stmt is a marker interface for all statements
func (a *AssignStmt) Loc() *source.Location { return &a.Location }

// TypeDecl represents a type declaration statement
type TypeDecl struct {
	Name *IdentifierExpr // type name
	Type TypeNode        // the type being defined
	Doc  *CommentGroup
	source.Location
}

func (t *TypeDecl) INode()                {} // Implements Node interface
func (t *TypeDecl) Stmt()                 {} // Stmt is a marker interface for all statements
func (t *TypeDecl) Decl()                 {} // Decl is a marker interface for all declarations
func (t *TypeDecl) Loc() *source.Location { return &t.Location }

// ReturnStmt represents a return statement
type ReturnStmt struct {
	Result  Expression // return value (can be nil for void functions)
	IsError bool       // true if returning error (return ... !)
	source.Location
}

func (r *ReturnStmt) INode()                {} // Implements Node interface
func (r *ReturnStmt) Stmt()                 {} // Stmt is a marker method for statements
func (r *ReturnStmt) Loc() *source.Location { return &r.Location }

// ImportStmt represents an import statement
type ImportStmt struct {
	Path           *BasicLit       // import path (STRING literal)
	Alias          *IdentifierExpr // import alias (can be nil)
	LocationOnDisk string          // resolved file path
	source.Location
}

func (i *ImportStmt) INode()                {} // Implements Node interface
func (i *ImportStmt) Stmt()                 {} // Stmt is a marker interface for all statements
func (i *ImportStmt) Loc() *source.Location { return &i.Location }

// BreakStmt represents a break statement
type BreakStmt struct {
	source.Location
}

func (b *BreakStmt) INode()                {} // Implements Node interface
func (b *BreakStmt) Stmt()                 {} // Stmt is a marker interface for all statements
func (b *BreakStmt) Loc() *source.Location { return &b.Location }

// ContinueStmt represents a continue statement
type ContinueStmt struct {
	source.Location
}

func (c *ContinueStmt) INode()                {} // Implements Node interface
func (c *ContinueStmt) Stmt()                 {} // Stmt is a marker interface for all statements
func (c *ContinueStmt) Loc() *source.Location { return &c.Location }

// DeferStmt represents a defer statement: defer funcCall() [catch err { ... }];
// Defer statements execute in LIFO order when the function exits.
// Catch blocks in defer are diagnostic-only and cannot alter control flow.
type DeferStmt struct {
	Call  *CallExpr    // deferred function call (required)
	Catch *CatchClause // optional catch block (diagnostic-only)
	source.Location
}

func (d *DeferStmt) INode()                {} // Implements Node interface
func (d *DeferStmt) Stmt()                 {} // Stmt is a marker interface for all statements
func (d *DeferStmt) Loc() *source.Location { return &d.Location }

// ExprStmt represents an expression used as a statement
type ExprStmt struct {
	X Expression
	source.Location
}

func (e *ExprStmt) INode()                {} // Implements Node interface
func (e *ExprStmt) Stmt()                 {} // Stmt is a marker interface for all statements
func (e *ExprStmt) Loc() *source.Location { return &e.Location }
