package hir

import (
	"compiler/internal/semantics/symbols"
	"compiler/internal/source"
	"compiler/internal/tokens"
	"compiler/internal/types"
)

// Node is the base interface for all HIR nodes.
type Node interface {
	hirNode()
	Loc() *source.Location
}

// Expr represents a HIR expression node.
type Expr interface {
	Node
	hirExpr()
}

// Stmt represents a HIR statement node.
type Stmt interface {
	Node
	hirStmt()
}

// Decl represents a HIR declaration node.
type Decl interface {
	Node
	hirDecl()
}

// Module is the root HIR node for a single source module.
type Module struct {
	ImportPath string
	Items      []Node
	Location   source.Location
}

func (m *Module) hirNode()              {}
func (m *Module) Loc() *source.Location { return &m.Location }

// Invalid represents an invalid node carried forward for error recovery.
type Invalid struct {
	Location source.Location
}

func (i *Invalid) hirNode()              {}
func (i *Invalid) hirExpr()              {}
func (i *Invalid) hirStmt()              {}
func (i *Invalid) hirDecl()              {}
func (i *Invalid) Loc() *source.Location { return &i.Location }

// Ident represents a resolved identifier reference.
type Ident struct {
	Name     string
	Symbol   *symbols.Symbol
	Type     types.SemType
	Location source.Location
}

func (i *Ident) hirNode()              {}
func (i *Ident) hirExpr()              {}
func (i *Ident) Loc() *source.Location { return &i.Location }

// LiteralKind defines the kinds of literal values in HIR.
type LiteralKind int

const (
	LiteralInt LiteralKind = iota
	LiteralFloat
	LiteralString
	LiteralByte
	LiteralBool
	LiteralNone
)

// Literal represents a basic literal.
type Literal struct {
	Kind     LiteralKind
	Value    string
	Type     types.SemType
	Location source.Location
}

func (l *Literal) hirNode()              {}
func (l *Literal) hirExpr()              {}
func (l *Literal) Loc() *source.Location { return &l.Location }

// OptionalNone represents a typed none value for an optional type.
type OptionalNone struct {
	Type     types.SemType
	Location source.Location
}

func (o *OptionalNone) hirNode()              {}
func (o *OptionalNone) hirExpr()              {}
func (o *OptionalNone) Loc() *source.Location { return &o.Location }

// OptionalSome wraps a value into an optional type.
type OptionalSome struct {
	Value    Expr
	Type     types.SemType
	Location source.Location
}

func (o *OptionalSome) hirNode()              {}
func (o *OptionalSome) hirExpr()              {}
func (o *OptionalSome) Loc() *source.Location { return &o.Location }

// OptionalIsSome checks if an optional has a value.
type OptionalIsSome struct {
	Value    Expr
	Type     types.SemType
	Location source.Location
}

func (o *OptionalIsSome) hirNode()              {}
func (o *OptionalIsSome) hirExpr()              {}
func (o *OptionalIsSome) Loc() *source.Location { return &o.Location }

// OptionalIsNone checks if an optional is none.
type OptionalIsNone struct {
	Value    Expr
	Type     types.SemType
	Location source.Location
}

func (o *OptionalIsNone) hirNode()              {}
func (o *OptionalIsNone) hirExpr()              {}
func (o *OptionalIsNone) Loc() *source.Location { return &o.Location }

// OptionalUnwrap unwraps an optional with an optional fallback.
// If Default is nil, it represents a forced unwrap (assumed safe by type checking).
type OptionalUnwrap struct {
	Value    Expr
	Default  Expr
	Type     types.SemType
	Location source.Location
}

func (o *OptionalUnwrap) hirNode()              {}
func (o *OptionalUnwrap) hirExpr()              {}
func (o *OptionalUnwrap) Loc() *source.Location { return &o.Location }

// UnionVariantCheck checks if a union value holds a specific variant.
// Returns a boolean indicating if the union's tag matches the expected variant.
type UnionVariantCheck struct {
	Value        Expr          // The union value to check
	VariantIndex int           // Index of the variant to check for
	UnionType    types.SemType // The union type
	VariantType  types.SemType // The expected variant type
	Location     source.Location
}

func (u *UnionVariantCheck) hirNode()              {}
func (u *UnionVariantCheck) hirExpr()              {}
func (u *UnionVariantCheck) Loc() *source.Location { return &u.Location }

// UnionExtract extracts the value from a union assuming it holds a specific variant.
// This is used after a successful UnionVariantCheck (e.g., inside an `if u is T` block).
type UnionExtract struct {
	Value        Expr          // The union value to extract from
	VariantIndex int           // Index of the variant to extract
	UnionType    types.SemType // The union type
	Type         types.SemType // The extracted variant type (result type)
	Location     source.Location
}

func (u *UnionExtract) hirNode()              {}
func (u *UnionExtract) hirExpr()              {}
func (u *UnionExtract) Loc() *source.Location { return &u.Location }

// ResultOk wraps a value into the ok variant of a result.
type ResultOk struct {
	Value    Expr
	Type     types.SemType
	Location source.Location
}

func (r *ResultOk) hirNode()              {}
func (r *ResultOk) hirExpr()              {}
func (r *ResultOk) Loc() *source.Location { return &r.Location }

// ResultErr wraps a value into the error variant of a result.
type ResultErr struct {
	Value    Expr
	Type     types.SemType
	Location source.Location
}

func (r *ResultErr) hirNode()              {}
func (r *ResultErr) hirExpr()              {}
func (r *ResultErr) Loc() *source.Location { return &r.Location }

// ResultUnwrap handles a result value with a catch clause.
type ResultUnwrap struct {
	Value    Expr
	Catch    *CatchClause
	Type     types.SemType
	Location source.Location
}

func (r *ResultUnwrap) hirNode()              {}
func (r *ResultUnwrap) hirExpr()              {}
func (r *ResultUnwrap) Loc() *source.Location { return &r.Location }

// ResultPropagate handles error propagation (expr!!).
// Unwraps the ok value or returns the error to the enclosing function.
type ResultPropagate struct {
	Value    Expr
	Type     types.SemType // the Ok type (result of the expression)
	Location source.Location
}

func (r *ResultPropagate) hirNode()              {}
func (r *ResultPropagate) hirExpr()              {}
func (r *ResultPropagate) Loc() *source.Location { return &r.Location }

// BinaryExpr represents a binary operation.
type BinaryExpr struct {
	X          Expr
	Op         tokens.Token
	Y          Expr
	Type       types.SemType
	TargetType types.SemType // For 'is' operator: the type being checked against
	Location   source.Location
}

func (b *BinaryExpr) hirNode()              {}
func (b *BinaryExpr) hirExpr()              {}
func (b *BinaryExpr) Loc() *source.Location { return &b.Location }

// UnaryExpr represents a unary operation.
type UnaryExpr struct {
	Op       tokens.Token
	X        Expr
	Type     types.SemType
	Location source.Location
}

func (u *UnaryExpr) hirNode()              {}
func (u *UnaryExpr) hirExpr()              {}
func (u *UnaryExpr) Loc() *source.Location { return &u.Location }

// SpreadExpr represents a spread argument expression (...x).
// Note: Only valid in call arguments.
type SpreadExpr struct {
	X        Expr
	Type     types.SemType
	Location source.Location
}

func (s *SpreadExpr) hirNode()              {}
func (s *SpreadExpr) hirExpr()              {}
func (s *SpreadExpr) Loc() *source.Location { return &s.Location }

// DerefExpr represents a dereference operation (*x).
type DerefExpr struct {
	X        Expr
	Type     types.SemType
	Location source.Location
}

func (d *DerefExpr) hirNode()              {}
func (d *DerefExpr) hirExpr()              {}
func (d *DerefExpr) Loc() *source.Location { return &d.Location }

// PrefixExpr represents a prefix ++/-- expression.
type PrefixExpr struct {
	Op       tokens.Token
	X        Expr
	Type     types.SemType
	Location source.Location
}

func (p *PrefixExpr) hirNode()              {}
func (p *PrefixExpr) hirExpr()              {}
func (p *PrefixExpr) Loc() *source.Location { return &p.Location }

// PostfixExpr represents a postfix ++/-- expression.
type PostfixExpr struct {
	X        Expr
	Op       tokens.Token
	Type     types.SemType
	Location source.Location
}

func (p *PostfixExpr) hirNode()              {}
func (p *PostfixExpr) hirExpr()              {}
func (p *PostfixExpr) Loc() *source.Location { return &p.Location }

// CallExpr represents a function call.
type CallExpr struct {
	Fun      Expr
	Args     []Expr
	Catch    *CatchClause
	Type     types.SemType
	Location source.Location
}

func (c *CallExpr) hirNode()              {}
func (c *CallExpr) hirExpr()              {}
func (c *CallExpr) Loc() *source.Location { return &c.Location }

// CatchClause represents the catch part of error handling.
type CatchClause struct {
	ErrIdent *Ident
	Handler  *Block
	Fallback Expr
	Location source.Location
}

func (c *CatchClause) hirNode()              {}
func (c *CatchClause) Loc() *source.Location { return &c.Location }

// SelectorExpr represents field/method selection (x.field).
type SelectorExpr struct {
	X        Expr
	Field    *Ident
	Type     types.SemType
	Location source.Location
}

func (s *SelectorExpr) hirNode()              {}
func (s *SelectorExpr) hirExpr()              {}
func (s *SelectorExpr) Loc() *source.Location { return &s.Location }

// RangeExpr represents a range expression.
type RangeExpr struct {
	Start     Expr
	End       Expr
	Incr      Expr
	Inclusive bool
	Type      types.SemType
	Location  source.Location
}

func (r *RangeExpr) hirNode()              {}
func (r *RangeExpr) hirExpr()              {}
func (r *RangeExpr) Loc() *source.Location { return &r.Location }

// ArrayLenExpr represents getting the length of an array expression.
type ArrayLenExpr struct {
	X        Expr
	Type     types.SemType
	Location source.Location
}

func (a *ArrayLenExpr) hirNode()              {}
func (a *ArrayLenExpr) hirExpr()              {}
func (a *ArrayLenExpr) Loc() *source.Location { return &a.Location }

// StringLenExpr represents getting the length of a string expression.
type StringLenExpr struct {
	X        Expr
	Type     types.SemType
	Location source.Location
}

func (s *StringLenExpr) hirNode()              {}
func (s *StringLenExpr) hirExpr()              {}
func (s *StringLenExpr) Loc() *source.Location { return &s.Location }

// MapIterInitExpr represents initializing a map iterator.
type MapIterInitExpr struct {
	Map      Expr
	Type     types.SemType
	Location source.Location
}

func (m *MapIterInitExpr) hirNode()              {}
func (m *MapIterInitExpr) hirExpr()              {}
func (m *MapIterInitExpr) Loc() *source.Location { return &m.Location }

// MapIterNextExpr advances a map iterator and yields whether another entry exists.
type MapIterNextExpr struct {
	Map      Expr
	Iter     Expr
	Key      *Ident
	Value    *Ident
	Type     types.SemType
	Location source.Location
}

func (m *MapIterNextExpr) hirNode()              {}
func (m *MapIterNextExpr) hirExpr()              {}
func (m *MapIterNextExpr) Loc() *source.Location { return &m.Location }

// IndexExpr represents an index operation (arr[i]).
type IndexExpr struct {
	X        Expr
	Index    Expr
	Type     types.SemType
	Location source.Location
}

func (i *IndexExpr) hirNode()              {}
func (i *IndexExpr) hirExpr()              {}
func (i *IndexExpr) Loc() *source.Location { return &i.Location }

// CastExpr represents an explicit cast.
type CastExpr struct {
	X          Expr
	TargetType types.SemType
	Type       types.SemType
	Location   source.Location
}

func (c *CastExpr) hirNode()              {}
func (c *CastExpr) hirExpr()              {}
func (c *CastExpr) Loc() *source.Location { return &c.Location }

// CoalescingExpr represents the coalescing operator (a ?? b).
type CoalescingExpr struct {
	Cond     Expr
	Default  Expr
	Type     types.SemType
	Location source.Location
}

func (e *CoalescingExpr) hirNode()              {}
func (e *CoalescingExpr) hirExpr()              {}
func (e *CoalescingExpr) Loc() *source.Location { return &e.Location }

// ForkExpr represents a fork expression.
type ForkExpr struct {
	Call     Expr
	Type     types.SemType
	Location source.Location
}

func (f *ForkExpr) hirNode()              {}
func (f *ForkExpr) hirExpr()              {}
func (f *ForkExpr) Loc() *source.Location { return &f.Location }

// ScopeResolutionExpr represents scope resolution (module::symbol).
type ScopeResolutionExpr struct {
	X        Expr
	Selector *Ident
	Type     types.SemType
	Location source.Location
}

func (s *ScopeResolutionExpr) hirNode()              {}
func (s *ScopeResolutionExpr) hirExpr()              {}
func (s *ScopeResolutionExpr) Loc() *source.Location { return &s.Location }

// ParenExpr represents a parenthesized expression.
type ParenExpr struct {
	X        Expr
	Type     types.SemType
	Location source.Location
}

func (p *ParenExpr) hirNode()              {}
func (p *ParenExpr) hirExpr()              {}
func (p *ParenExpr) Loc() *source.Location { return &p.Location }

// CompositeLit represents composite literals (structs, arrays, maps).
type CompositeLit struct {
	Type     types.SemType
	Elts     []Expr
	Location source.Location
}

func (c *CompositeLit) hirNode()              {}
func (c *CompositeLit) hirExpr()              {}
func (c *CompositeLit) Loc() *source.Location { return &c.Location }

// KeyValueExpr represents key/value pairs in composite literals.
type KeyValueExpr struct {
	Key      Expr
	Value    Expr
	Location source.Location
}

func (k *KeyValueExpr) hirNode()              {}
func (k *KeyValueExpr) hirExpr()              {}
func (k *KeyValueExpr) Loc() *source.Location { return &k.Location }

// FuncLit represents a function literal.
type FuncLit struct {
	ID       string
	Type     types.SemType
	Captures []*Ident
	Body     *Block
	Location source.Location
}

func (f *FuncLit) hirNode()              {}
func (f *FuncLit) hirExpr()              {}
func (f *FuncLit) Loc() *source.Location { return &f.Location }

// Block represents a block of statements.
type Block struct {
	Nodes        []Node
	NarrowingKey string
	Location     source.Location
}

func (b *Block) hirNode()              {}
func (b *Block) hirStmt()              {}
func (b *Block) Loc() *source.Location { return &b.Location }

// DeclStmt wraps a declaration as a statement.
type DeclStmt struct {
	Decl     Decl
	Location source.Location
}

func (d *DeclStmt) hirNode()              {}
func (d *DeclStmt) hirStmt()              {}
func (d *DeclStmt) Loc() *source.Location { return &d.Location }

// DeclItem represents a variable/constant declaration item.
type DeclItem struct {
	Name  *Ident
	Type  types.SemType
	Value Expr
}

// VarDecl represents a variable declaration.
type VarDecl struct {
	Decls    []DeclItem
	Location source.Location
}

func (v *VarDecl) hirNode()              {}
func (v *VarDecl) hirStmt()              {}
func (v *VarDecl) hirDecl()              {}
func (v *VarDecl) Loc() *source.Location { return &v.Location }

// ConstDecl represents a constant declaration.
type ConstDecl struct {
	Decls    []DeclItem
	Location source.Location
}

func (c *ConstDecl) hirNode()              {}
func (c *ConstDecl) hirStmt()              {}
func (c *ConstDecl) hirDecl()              {}
func (c *ConstDecl) Loc() *source.Location { return &c.Location }

// TypeDecl represents a type declaration.
type TypeDecl struct {
	Name     *Ident
	Type     types.SemType
	Location source.Location
}

func (t *TypeDecl) hirNode()              {}
func (t *TypeDecl) hirStmt()              {}
func (t *TypeDecl) hirDecl()              {}
func (t *TypeDecl) Loc() *source.Location { return &t.Location }

// AssignStmt represents an assignment statement.
type AssignStmt struct {
	Lhs      Expr
	Rhs      Expr
	Op       *tokens.Token
	Location source.Location
}

func (a *AssignStmt) hirNode()              {}
func (a *AssignStmt) hirStmt()              {}
func (a *AssignStmt) Loc() *source.Location { return &a.Location }

// ReturnStmt represents a return statement.
type ReturnStmt struct {
	Result   Expr
	IsError  bool
	Location source.Location
}

func (r *ReturnStmt) hirNode()              {}
func (r *ReturnStmt) hirStmt()              {}
func (r *ReturnStmt) Loc() *source.Location { return &r.Location }

// ImportStmt represents an import statement.
type ImportStmt struct {
	Path           string
	Alias          string
	LocationOnDisk string
	Location       source.Location
}

func (i *ImportStmt) hirNode()              {}
func (i *ImportStmt) hirStmt()              {}
func (i *ImportStmt) Loc() *source.Location { return &i.Location }

// BreakStmt represents a break statement.
type BreakStmt struct {
	Location source.Location
}

func (b *BreakStmt) hirNode()              {}
func (b *BreakStmt) hirStmt()              {}
func (b *BreakStmt) Loc() *source.Location { return &b.Location }

// ContinueStmt represents a continue statement.
type ContinueStmt struct {
	Location source.Location
}

func (c *ContinueStmt) hirNode()              {}
func (c *ContinueStmt) hirStmt()              {}
func (c *ContinueStmt) Loc() *source.Location { return &c.Location }

// DeferStmt represents a defer statement.
type DeferStmt struct {
	Call     *CallExpr
	Catch    *CatchClause // optional catch block for diagnostic-only error handling
	Location source.Location
}

func (d *DeferStmt) hirNode()              {}
func (d *DeferStmt) hirStmt()              {}
func (d *DeferStmt) Loc() *source.Location { return &d.Location }

// ExprStmt represents an expression statement.
type ExprStmt struct {
	X        Expr
	Location source.Location
}

func (e *ExprStmt) hirNode()              {}
func (e *ExprStmt) hirStmt()              {}
func (e *ExprStmt) Loc() *source.Location { return &e.Location }

// FuncDecl represents a function declaration.
type FuncDecl struct {
	Name     *Ident
	Type     *types.FunctionType
	Body     *Block
	Location source.Location
}

func (f *FuncDecl) hirNode()              {}
func (f *FuncDecl) hirDecl()              {}
func (f *FuncDecl) Loc() *source.Location { return &f.Location }

// Param represents a named parameter.
type Param struct {
	Name       string
	Type       types.SemType
	IsVariadic bool
	IsMove     bool
	Location   source.Location
}

// MethodDecl represents a method declaration.
type MethodDecl struct {
	Receiver *Param
	Name     *Ident
	Type     *types.FunctionType
	Body     *Block
	Location source.Location
}

func (m *MethodDecl) hirNode()              {}
func (m *MethodDecl) hirDecl()              {}
func (m *MethodDecl) Loc() *source.Location { return &m.Location }

// IfStmt represents an if statement.
type IfStmt struct {
	Cond     Expr
	Body     *Block
	Else     Node
	Location source.Location
}

func (i *IfStmt) hirNode()              {}
func (i *IfStmt) hirStmt()              {}
func (i *IfStmt) Loc() *source.Location { return &i.Location }

// ForStmt represents a for-in loop.
type ForStmt struct {
	Iterator Node
	Range    Expr
	Body     *Block
	Location source.Location
}

func (f *ForStmt) hirNode()              {}
func (f *ForStmt) hirStmt()              {}
func (f *ForStmt) Loc() *source.Location { return &f.Location }

// WhileStmt represents a while loop.
type WhileStmt struct {
	Cond     Expr
	Body     *Block
	Location source.Location
}

func (w *WhileStmt) hirNode()              {}
func (w *WhileStmt) hirStmt()              {}
func (w *WhileStmt) Loc() *source.Location { return &w.Location }

// MatchStmt represents a match statement.
type MatchStmt struct {
	Expr     Expr
	Cases    []CaseClause
	Location source.Location
}

func (m *MatchStmt) hirNode()              {}
func (m *MatchStmt) hirStmt()              {}
func (m *MatchStmt) Loc() *source.Location { return &m.Location }

// CaseClause represents a single match case.
type CaseClause struct {
	Pattern  Expr
	Body     *Block
	Location source.Location
}

// TypeCheckPattern represents a type check pattern in match (is Type)
type TypeCheckPattern struct {
	Type     types.SemType
	Location source.Location
}

func (t *TypeCheckPattern) hirNode()              {}
func (t *TypeCheckPattern) hirExpr()              {}
func (t *TypeCheckPattern) Loc() *source.Location { return &t.Location }

// RangeCheckPattern represents a range check pattern in match (in Range)
type RangeCheckPattern struct {
	Range    Expr // RangeExpr
	Location source.Location
}

func (r *RangeCheckPattern) hirNode()              {}
func (r *RangeCheckPattern) hirExpr()              {}
func (r *RangeCheckPattern) Loc() *source.Location { return &r.Location }
