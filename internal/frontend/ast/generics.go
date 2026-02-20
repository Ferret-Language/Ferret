package ast

import (
	"compiler/internal/source"
)

// TypeParam represents a single generic type parameter declaration.
// Examples:
//   - T
//   - T: numeric
//   - T: union { i32, i64 }
type TypeParam struct {
	Name       *IdentifierExpr
	Constraint ConstraintExpr // nil means unconstrained
	source.Location
}

func (t *TypeParam) INode()                {}
func (t *TypeParam) Loc() *source.Location { return &t.Location }

// AppliedType represents a generic type application:
// Base<Arg1, Arg2, ...>
// Examples:
//   - Point<i32>
//   - io::Result<str, i32>
type AppliedType struct {
	Base TypeNode
	Args []TypeNode
	source.Location
}

func (a *AppliedType) INode()                {}
func (a *AppliedType) TypeExpr()             {}
func (a *AppliedType) Loc() *source.Location { return &a.Location }
