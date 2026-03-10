package ir

import (
	"compiler/internal/semantics/typeinfo"
	"compiler/internal/source"
)

type Module struct {
	Key        string
	ImportPath string
	FilePath   string
	Globals    []*Global
	Functions  []*Function
}

type Global struct {
	Name     string
	Mutable  bool
	Constant bool
	Type     typeinfo.Type
	Init     Value
	Location source.Location
}

type Function struct {
	Name     string
	Receiver *Param
	Params   []*Param
	Result   typeinfo.Type
	EntryID  int
	ExitID   int
	Blocks   []*Block
	Location source.Location
}

type Param struct {
	Name       string
	Type       typeinfo.Type
	IsComptime bool
	Location   source.Location
}

type Block struct {
	ID           int
	Instructions []Instr
	Terminator   Terminator
	Location     source.Location
}

type Node interface {
	Loc() source.Location
	node()
}

type Value interface {
	Node
	Type() typeinfo.Type
	valueNode()
}

type Instr interface {
	Node
	instrNode()
}

type Terminator interface {
	Node
	termNode()
}

type Place interface {
	Node
	placeNode()
}

type baseValue struct {
	Location source.Location
	ExprType typeinfo.Type
}

func (v *baseValue) Loc() source.Location { return v.Location }
func (v *baseValue) Type() typeinfo.Type  { return v.ExprType }
func (v *baseValue) node()                {}

type baseInstr struct{ Location source.Location }

func (i *baseInstr) Loc() source.Location { return i.Location }
func (i *baseInstr) node()                {}

type baseTerm struct{ Location source.Location }

func (t *baseTerm) Loc() source.Location { return t.Location }
func (t *baseTerm) node()                {}

type basePlace struct{ Location source.Location }

func (p *basePlace) Loc() source.Location { return p.Location }
func (p *basePlace) node()                {}

type NameValue struct {
	baseValue
	Path []string
}

func (*NameValue) valueNode() {}

type NumberValue struct {
	baseValue
	Value string
}

func (*NumberValue) valueNode() {}

type StringValue struct {
	baseValue
	Value string
}

func (*StringValue) valueNode() {}

type NoneValue struct{ baseValue }

func (*NoneValue) valueNode() {}

type UnaryValue struct {
	baseValue
	Op    string
	Right Value
}

func (*UnaryValue) valueNode() {}

type BinaryValue struct {
	baseValue
	Left  Value
	Op    string
	Right Value
}

func (*BinaryValue) valueNode() {}

type PostfixValue struct {
	baseValue
	Left Value
	Op   string
}

func (*PostfixValue) valueNode() {}

type CallValue struct {
	baseValue
	Callee Value
	Args   []Value
}

func (*CallValue) valueNode() {}

type FieldValue struct {
	baseValue
	Base Value
	Name string
}

func (*FieldValue) valueNode() {}

type CastValue struct {
	baseValue
	Left Value
}

func (*CastValue) valueNode() {}

type CompositeItem struct {
	Name  string
	Value Value
}

type CompositeValue struct {
	baseValue
	Items []CompositeItem
}

func (*CompositeValue) valueNode() {}

type LocalPlace struct {
	basePlace
	Name string
}

func (*LocalPlace) placeNode() {}

type FieldPlace struct {
	basePlace
	Base Place
	Name string
}

func (*FieldPlace) placeNode() {}

type BindInstr struct {
	baseInstr
	Name     string
	Mutable  bool
	Constant bool
	Type     typeinfo.Type
	Value    Value
}

func (*BindInstr) instrNode() {}

type StoreInstr struct {
	baseInstr
	Target Place
	Value  Value
}

func (*StoreInstr) instrNode() {}

type EvalInstr struct {
	baseInstr
	Value Value
}

func (*EvalInstr) instrNode() {}

type DeferInstr struct {
	baseInstr
	Body []Instr
}

func (*DeferInstr) instrNode() {}

type LockInstr struct {
	baseInstr
	Value Value
	Name  string
}

func (*LockInstr) instrNode() {}

type UnsafeInstr struct{ baseInstr }

func (*UnsafeInstr) instrNode() {}

type JumpTerm struct {
	baseTerm
	TargetID int
}

func (*JumpTerm) termNode() {}

type BranchTerm struct {
	baseTerm
	Cond    Value
	TrueID  int
	FalseID int
}

func (*BranchTerm) termNode() {}

type SwitchCase struct {
	Expr     Value
	TargetID int
}

type SwitchTerm struct {
	baseTerm
	Value     Value
	Cases     []SwitchCase
	DefaultID int
}

func (*SwitchTerm) termNode() {}

type ReturnTerm struct {
	baseTerm
	Value Value
}

func (*ReturnTerm) termNode() {}

type ExitTerm struct{ baseTerm }

func (*ExitTerm) termNode() {}
