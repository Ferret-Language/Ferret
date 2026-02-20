package ast

import (
	"compiler/internal/source"
	"compiler/internal/tokens"
)

// ConstraintExpr is the base interface for constraint declarations.
// Constraints are compile-time only and currently used for generic bounds.
type ConstraintExpr interface {
	Node
	ConstraintExpr()
}

// ConstraintDecl represents a top-level constraint declaration:
// constraint Name = <constraint-expr>;
type ConstraintDecl struct {
	Name *IdentifierExpr
	Expr ConstraintExpr
	Doc  *CommentGroup
	source.Location
}

func (c *ConstraintDecl) INode()                {}
func (c *ConstraintDecl) Stmt()                 {}
func (c *ConstraintDecl) Decl()                 {}
func (c *ConstraintDecl) Loc() *source.Location { return &c.Location }

// ConstraintTypeTerm represents a single term in a constraint expression.
// The term can wrap a type-like node (identifier, union, interface, etc.).
// Approx indicates support for underlying-type matching via '~'.
type ConstraintTypeTerm struct {
	Approx bool
	Type   TypeNode
	source.Location
}

func (c *ConstraintTypeTerm) INode()                {}
func (c *ConstraintTypeTerm) ConstraintExpr()       {}
func (c *ConstraintTypeTerm) Loc() *source.Location { return &c.Location }

// ConstraintUnionExpr represents constraint unions:
// union { T1, ~T2, ... }
type ConstraintUnionExpr struct {
	Terms []*ConstraintTypeTerm
	source.Location
}

func (c *ConstraintUnionExpr) INode()                {}
func (c *ConstraintUnionExpr) ConstraintExpr()       {}
func (c *ConstraintUnionExpr) Loc() *source.Location { return &c.Location }

// ConstraintBinaryExpr composes constraints with binary operators.
// Currently only '&' (intersection) is supported.
type ConstraintBinaryExpr struct {
	Left  ConstraintExpr
	Right ConstraintExpr
	Op    tokens.TOKEN
	source.Location
}

func (c *ConstraintBinaryExpr) INode()                {}
func (c *ConstraintBinaryExpr) ConstraintExpr()       {}
func (c *ConstraintBinaryExpr) Loc() *source.Location { return &c.Location }
