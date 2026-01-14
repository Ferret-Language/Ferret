package mir

import (
	"compiler/internal/source"
	"compiler/internal/tokens"
	"compiler/internal/types"
)

// Instr is the base interface for MIR instructions.
type Instr interface {
	mirInstr()
	Loc() *source.Location
}

// Const defines a typed constant value.
type Const struct {
	Result   ValueID
	Type     types.SemType
	Value    string
	Location source.Location
}

func (c *Const) mirInstr()             {}
func (c *Const) Loc() *source.Location { return &c.Location }

// Binary performs a binary operation.
type Binary struct {
	Result   ValueID
	Op       tokens.TOKEN
	Left     ValueID
	Right    ValueID
	Type     types.SemType
	Location source.Location
}

func (b *Binary) mirInstr()             {}
func (b *Binary) Loc() *source.Location { return &b.Location }

// Unary performs a unary operation.
type Unary struct {
	Result   ValueID
	Op       tokens.TOKEN
	X        ValueID
	Type     types.SemType
	Location source.Location
}

func (u *Unary) mirInstr()             {}
func (u *Unary) Loc() *source.Location { return &u.Location }

// Cast converts a value to another type.
type Cast struct {
	Result   ValueID
	X        ValueID
	Type     types.SemType
	Location source.Location
}

func (c *Cast) mirInstr()             {}
func (c *Cast) Loc() *source.Location { return &c.Location }

// Alloca reserves stack storage for a value of Type.
type Alloca struct {
	Result   ValueID
	Type     types.SemType
	Location source.Location
}

func (a *Alloca) mirInstr()             {}
func (a *Alloca) Loc() *source.Location { return &a.Location }

// Load reads a value from a pointer.
type Load struct {
	Result   ValueID
	Addr     ValueID
	Type     types.SemType
	Location source.Location
}

func (l *Load) mirInstr()             {}
func (l *Load) Loc() *source.Location { return &l.Location }

// PtrAdd computes an address by adding a byte offset to a base pointer.
type PtrAdd struct {
	Result   ValueID
	Base     ValueID
	Offset   int
	Elem     types.SemType
	Location source.Location
}

func (p *PtrAdd) mirInstr()             {}
func (p *PtrAdd) Loc() *source.Location { return &p.Location }

// PtrOffset computes an address by adding a byte offset value to a base pointer.
type PtrOffset struct {
	Result   ValueID
	Base     ValueID
	Offset   ValueID
	Elem     types.SemType
	Location source.Location
}

func (p *PtrOffset) mirInstr()             {}
func (p *PtrOffset) Loc() *source.Location { return &p.Location }

// Store writes a value to a pointer.
type Store struct {
	Addr     ValueID
	Value    ValueID
	Location source.Location
}

func (s *Store) mirInstr()             {}
func (s *Store) Loc() *source.Location { return &s.Location }

// Call represents a direct function call.
type Call struct {
	Result   ValueID
	Target   string
	Args     []ValueID
	Type     types.SemType
	Location source.Location
}

func (c *Call) mirInstr()             {}
func (c *Call) Loc() *source.Location { return &c.Location }

// CallIndirect represents a call through a function value.
type CallIndirect struct {
	Result   ValueID
	Callee   ValueID
	Args     []ValueID
	Type     types.SemType
	Location source.Location
}

func (c *CallIndirect) mirInstr()             {}
func (c *CallIndirect) Loc() *source.Location { return &c.Location }

// Phi merges values from predecessor blocks.
type Phi struct {
	Result   ValueID
	Type     types.SemType
	Incoming []PhiIncoming
	Location source.Location
}

// PhiIncoming maps a predecessor block to an incoming value.
type PhiIncoming struct {
	Pred  BlockID
	Value ValueID
}

func (p *Phi) mirInstr()             {}
func (p *Phi) Loc() *source.Location { return &p.Location }

// ArrayGet reads an element from an array or slice.
type ArrayGet struct {
	Result   ValueID
	Array    ValueID
	Index    ValueID
	Type     types.SemType
	Location source.Location
}

func (a *ArrayGet) mirInstr()             {}
func (a *ArrayGet) Loc() *source.Location { return &a.Location }

// ArraySet writes an element into an array or slice.
type ArraySet struct {
	Array    ValueID
	Index    ValueID
	Value    ValueID
	Location source.Location
}

func (a *ArraySet) mirInstr()             {}
func (a *ArraySet) Loc() *source.Location { return &a.Location }

// MapGet reads a value from a map (returns optional value type).
type MapGet struct {
	Result   ValueID
	Map      ValueID
	Key      ValueID
	Type     types.SemType
	Location source.Location
}

func (m *MapGet) mirInstr()             {}
func (m *MapGet) Loc() *source.Location { return &m.Location }

// MapSet writes a key/value pair into a map.
type MapSet struct {
	Map      ValueID
	Key      ValueID
	Value    ValueID
	Location source.Location
}

func (m *MapSet) mirInstr()             {}
func (m *MapSet) Loc() *source.Location { return &m.Location }

// OptionalNone creates an empty optional.
type OptionalNone struct {
	Result   ValueID
	Type     types.SemType
	Location source.Location
}

func (o *OptionalNone) mirInstr()             {}
func (o *OptionalNone) Loc() *source.Location { return &o.Location }

// OptionalSome wraps a value in an optional.
type OptionalSome struct {
	Result   ValueID
	Value    ValueID
	Type     types.SemType
	Location source.Location
}

func (o *OptionalSome) mirInstr()             {}
func (o *OptionalSome) Loc() *source.Location { return &o.Location }

// OptionalIsSome checks if an optional has a value.
type OptionalIsSome struct {
	Result   ValueID
	Value    ValueID
	Location source.Location
}

func (o *OptionalIsSome) mirInstr()             {}
func (o *OptionalIsSome) Loc() *source.Location { return &o.Location }

// OptionalUnwrap extracts a value from an optional, optionally with a default.
type OptionalUnwrap struct {
	Result     ValueID
	Value      ValueID
	Default    ValueID
	HasDefault bool
	Type       types.SemType
	Location   source.Location
}

func (o *OptionalUnwrap) mirInstr()             {}
func (o *OptionalUnwrap) Loc() *source.Location { return &o.Location }

// UnionVariantCheck checks if a union value holds a specific variant.
// Emits code to compare the union's tag against the expected variant index.
type UnionVariantCheck struct {
	Result       ValueID       // Boolean result
	Value        ValueID       // The union value
	VariantIndex int           // Expected variant index
	UnionType    types.SemType // The union type
	Location     source.Location
}

func (u *UnionVariantCheck) mirInstr()             {}
func (u *UnionVariantCheck) Loc() *source.Location { return &u.Location }

// UnionExtract extracts the inner value from a union.
// Assumes the union holds the specified variant (caller must verify).
type UnionExtract struct {
	Result       ValueID       // The extracted value
	Value        ValueID       // The union value
	VariantIndex int           // Variant index to extract
	UnionType    types.SemType // The union type
	Type         types.SemType // The extracted variant type
	Location     source.Location
}

func (u *UnionExtract) mirInstr()             {}
func (u *UnionExtract) Loc() *source.Location { return &u.Location }

// ResultOk wraps a success value.
type ResultOk struct {
	Result   ValueID
	Value    ValueID
	Type     types.SemType
	Location source.Location
}

func (r *ResultOk) mirInstr()             {}
func (r *ResultOk) Loc() *source.Location { return &r.Location }

// ResultErr wraps an error value.
type ResultErr struct {
	Result   ValueID
	Value    ValueID
	Type     types.SemType
	Location source.Location
}

func (r *ResultErr) mirInstr()             {}
func (r *ResultErr) Loc() *source.Location { return &r.Location }

// ResultIsOk checks if a result is ok.
type ResultIsOk struct {
	Result   ValueID
	Value    ValueID
	Location source.Location
}

func (r *ResultIsOk) mirInstr()             {}
func (r *ResultIsOk) Loc() *source.Location { return &r.Location }

// ResultUnwrap extracts the success value from a result.
type ResultUnwrap struct {
	Result     ValueID
	Value      ValueID
	Default    ValueID
	HasDefault bool
	Type       types.SemType
	Location   source.Location
}

func (r *ResultUnwrap) mirInstr()             {}
func (r *ResultUnwrap) Loc() *source.Location { return &r.Location }
