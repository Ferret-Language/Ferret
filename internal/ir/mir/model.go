package mir

import (
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/source"
)

type Module struct {
	Key        string
	ImportPath string
	FilePath   string
	Types      []*TypeDecl
	Globals    []*Global
	Functions  []*Function
}

type TypeDecl struct {
	Name       string
	Named      *typeinfo.NamedType
	Underlying typeinfo.Type
	Struct     *StructTypeDecl
	Interface  *InterfaceTypeDecl
	Enum       *EnumTypeDecl
	Union      *UnionTypeDecl
	Error      *ErrorTypeDecl
	Location   source.Location
}

type StructTypeDecl struct {
	Fields       []*StructFieldDecl
	StaticFields []*StructFieldDecl
}

type StructFieldDecl struct {
	Name     string
	Type     typeinfo.Type
	Default  Value
	Location source.Location
}

type InterfaceTypeDecl struct {
	Methods []*InterfaceMethodDecl
}

type InterfaceMethodDecl struct {
	Receiver string
	Static   bool
	Name     string
	Params   []*Param
	Result   typeinfo.Type
	Location source.Location
}

type EnumTypeDecl struct {
	Variants []string
}

type UnionTypeDecl struct {
	Members []typeinfo.Type
}

type ErrorTypeDecl struct {
	Members []string
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
	Name       string
	LinkName   string
	IsUnsafe   bool
	IsBuiltin  bool
	IsExtern   bool
	ExternName string
	Params     []*Param
	Result     typeinfo.Type
	EntryID    int
	ExitID     int
	Locals     []*Local
	Blocks     []*Block
	Location   source.Location
}

type Param struct {
	Name       string
	LocalID    int
	Type       typeinfo.Type
	IsComptime bool
	IsMutable  bool
	Location   source.Location
}

type Local struct {
	ID       int
	Name     string
	Type     typeinfo.Type
	Mutable  bool
	Constant bool
	IsTemp   bool
	Location source.Location
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
	Path     []string
	LinkName string
}

func (*NameValue) valueNode() {}

type LocalValue struct {
	baseValue
	LocalID int
}

func (*LocalValue) valueNode() {}

type TempValue struct {
	baseValue
	Name string
}

func (*TempValue) valueNode() {}

type NumberValue struct {
	baseValue
	Value string
}

func (*NumberValue) valueNode() {}

type BoolValue struct {
	baseValue
	Value bool
}

func (*BoolValue) valueNode() {}

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

type AddrOfValue struct {
	baseValue
	Source  Value
	Mutable bool
	Raw     bool
}

func (*AddrOfValue) valueNode() {}

type LoadValue struct {
	baseValue
	Pointer Value
}

func (*LoadValue) valueNode() {}

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
	Callee        Value
	Args          []Value
	ReceiverType  typeinfo.Type // non-nil when this is a normalized method call; Args[0] is the receiver
	IsConstructor bool
}

func (*CallValue) valueNode() {}

type FieldLoadValue struct {
	baseValue
	Base       Value
	FieldIndex int
}

func (*FieldLoadValue) valueNode() {}

type FieldValue struct {
	baseValue
	Base       Value
	FieldIndex int
	MemberName string
}

func (*FieldValue) valueNode() {}

type CastValue struct {
	baseValue
	Left Value
}

func (*CastValue) valueNode() {}

type TypeTestValue struct {
	baseValue
	Left   Value
	Target typeinfo.Type
}

func (*TypeTestValue) valueNode() {}

type CompositeItem struct {
	Name  string
	Value Value
}

type InterfaceMethodLink struct {
	Name string
	Path []string
}

type CompositeValue struct {
	baseValue
	Items           []CompositeItem
	ConstructorPath []string
}

func (*CompositeValue) valueNode() {}

type InterfaceValue struct {
	baseValue
	Value        Value
	ConcreteType typeinfo.Type
	Methods      []InterfaceMethodLink
}

func (*InterfaceValue) valueNode() {}

// IndexValue represents arr[index] loaded as an rvalue.
type IndexValue struct {
	baseValue
	Base  Value
	Index Value
}

func (*IndexValue) valueNode() {}

type LocalPlace struct {
	basePlace
	LocalID int
}

func (*LocalPlace) placeNode() {}

type FieldPlace struct {
	basePlace
	Base       Place
	FieldIndex int
}

func (*FieldPlace) placeNode() {}

// IndexPlace represents the lvalue arr[index].
type IndexPlace struct {
	basePlace
	Base  Place
	Index Value
}

func (*IndexPlace) placeNode() {}

// DerefPlace represents the lvalue *ptr (pointer dereference).
type DerefPlace struct {
	basePlace
	Pointer Value // address to write through
}

func (*DerefPlace) placeNode() {}

type BindInstr struct {
	baseInstr
	Name     string
	Mutable  bool
	Constant bool
	Type     typeinfo.Type
	Value    Value
}

func (*BindInstr) instrNode() {}

type ComputeInstr struct {
	baseInstr
	TargetID int
	Type     typeinfo.Type
	Value    Value
}

func (*ComputeInstr) instrNode() {}

type AssignInstr struct {
	baseInstr
	TargetID int
	Value    Value
}

func (*AssignInstr) instrNode() {}

type StoreInstr struct {
	baseInstr
	Target Place
	Value  Value
}

func (*StoreInstr) instrNode() {}

type StoreFieldInstr struct {
	baseInstr
	Base       Value
	FieldIndex int
	Value      Value
}

func (*StoreFieldInstr) instrNode() {}

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
	Value   Value
	LocalID int
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
	Value     Value
	CleanupID int
}

func (*ReturnTerm) termNode() {}

type PanicTerm struct {
	baseTerm
	Value     Value
	CleanupID int
}

func (*PanicTerm) termNode() {}

type ExitTerm struct{ baseTerm }

func (*ExitTerm) termNode() {}
