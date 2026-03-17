package typeinfo

import (
	"fmt"
	"strings"

	"compiler/internal/frontend/ast"
)

type Type interface {
	String() string
}

type InvalidType struct{}

func (InvalidType) String() string { return "<invalid>" }

type UnknownType struct{}

func (UnknownType) String() string { return "<unknown>" }

type UndefinedType struct{}

func (UndefinedType) String() string { return "undefined" }

const (
	DefaultIntTypeName   = "i32"
	DefaultFloatTypeName = "f32"
)

type BuiltinType struct {
	Name string
}

func (t *BuiltinType) String() string { return t.Name }

type StringType struct{}

func (*StringType) String() string { return "str" }

type NamedType struct {
	ModuleKey string
	Name      string
	Decl      *ast.TypeDecl
}

func (t *NamedType) String() string {
	if t == nil {
		return "<nil>"
	}
	if t.ModuleKey == "" {
		return t.Name
	}
	return t.ModuleKey + "::" + t.Name
}

type PointerType struct {
	Inner Type
}

func (t *PointerType) String() string {
	if t == nil {
		return "<nil>"
	}
	return "*" + typeString(t.Inner)
}

type RefType struct {
	Mutable bool
	Inner   Type
}

func (t *RefType) String() string {
	if t == nil {
		return "<nil>"
	}
	prefix := "&"
	if t.Mutable {
		prefix = "&mut "
	}
	return prefix + typeString(t.Inner)
}

type RawPtrType struct {
	Inner Type
}

func (t *RawPtrType) String() string {
	if t == nil {
		return "<nil>"
	}
	if t.Inner == nil {
		return "^void"
	}
	return "^" + typeString(t.Inner)

}

type OptionalType struct {
	Inner Type
}

func (t *OptionalType) String() string { return "?" + typeString(t.Inner) }

type ErrorUnionType struct {
	Error Type
	Value Type
}

func (t *ErrorUnionType) String() string {
	return typeString(t.Error) + "!" + typeString(t.Value)
}

type ArrayType struct {
	Inner Type
	Len   int64
}

func (t *ArrayType) String() string {
	if t == nil {
		return "[?]<nil>"
	}
	if t.Len == -2 {
		return "[_]" + typeString(t.Inner)
	}
	if t.Len < 0 {
		return "[?]" + typeString(t.Inner)
	}
	return fmt.Sprintf("[%d]%s", t.Len, typeString(t.Inner))
}

type SliceType struct {
	Inner Type
}

func (t *SliceType) String() string {
	if t == nil {
		return "[]<nil>"
	}
	return "[]" + typeString(t.Inner)
}

type TupleType struct {
	Elems []Type
}

func (t *TupleType) String() string {
	parts := make([]string, 0, len(t.Elems))
	for _, elem := range t.Elems {
		parts = append(parts, typeString(elem))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

type StructField struct {
	Name       string
	Type       Type
	HasDefault bool
}

type StructType struct {
	Fields              map[string]*StructField
	OrderedFields       []*StructField
	StaticFields        map[string]*StructField
	OrderedStaticFields []*StructField
}

func (t *StructType) String() string { return "struct" }

type EnumType struct {
	Variants        map[string]struct{}
	OrderedVariants []string
	VariantOrdinals map[string]int
}

func (t *EnumType) String() string { return "enum" }

type ErrorSetType struct {
	Members        map[string]struct{}
	OrderedMembers []string
	MemberOrdinals map[string]int
}

func (t *ErrorSetType) String() string { return "error" }

type UnionType struct {
	Members []Type
}

func (t *UnionType) String() string { return "union" }

type InterfaceType struct {
	Methods         map[string]*FuncType
	MethodReceivers map[string]string
	OrderedMethods  []*InterfaceMethod
}

func (t *InterfaceType) String() string { return "interface" }

type FuncType struct {
	IsUnsafe       bool
	Params         []Type
	ComptimeParams []bool
	Result         Type
}

func (t *FuncType) String() string {
	parts := make([]string, 0, len(t.Params))
	for _, param := range t.Params {
		parts = append(parts, typeString(param))
	}
	prefix := "fn"
	if t.IsUnsafe {
		prefix = "unsafe fn"
	}
	return fmt.Sprintf("%s(%s) %s", prefix, strings.Join(parts, ", "), typeString(t.Result))
}

func typeString(t Type) string {
	if t == nil {
		return "void"
	}
	return t.String()
}

func DefaultIntType() *BuiltinType {
	return &BuiltinType{Name: DefaultIntTypeName}
}

func DefaultFloatType() *BuiltinType {
	return &BuiltinType{Name: DefaultFloatTypeName}
}

func IsInvalid(t Type) bool {
	_, ok := t.(InvalidType)
	return ok
}

func IsUnknown(t Type) bool {
	_, ok := t.(UnknownType)
	return ok
}

func IsBuiltinNamed(t Type, name string) bool {
	if name == "str" {
		_, ok := t.(*StringType)
		return ok
	}
	b, ok := t.(*BuiltinType)
	return ok && b.Name == name
}

func IsNumeric(t Type) bool {
	b, ok := t.(*BuiltinType)
	if !ok {
		return false
	}
	switch b.Name {
	case "u8", "u16", "u32", "u64", "usize", "i8", "i16", "i32", "i64", "isize", "f32", "f64":
		return true
	default:
		return false
	}
}

func Equal(a, b Type) bool {
	switch at := a.(type) {
	case nil:
		return b == nil
	case InvalidType:
		_, ok := b.(InvalidType)
		return ok
	case UnknownType:
		_, ok := b.(UnknownType)
		return ok
	case *BuiltinType:
		bt, ok := b.(*BuiltinType)
		return ok && at.Name == bt.Name
	case *StringType:
		_, ok := b.(*StringType)
		return ok
	case *NamedType:
		bt, ok := b.(*NamedType)
		return ok && at.ModuleKey == bt.ModuleKey && at.Name == bt.Name
	case *PointerType:
		bt, ok := b.(*PointerType)
		return ok && Equal(at.Inner, bt.Inner)
	case *RefType:
		bt, ok := b.(*RefType)
		return ok && at.Mutable == bt.Mutable && Equal(at.Inner, bt.Inner)
	case *RawPtrType:
		bt, ok := b.(*RawPtrType)
		return ok && Equal(at.Inner, bt.Inner)
	case *OptionalType:
		bt, ok := b.(*OptionalType)
		return ok && Equal(at.Inner, bt.Inner)
	case *ErrorUnionType:
		bt, ok := b.(*ErrorUnionType)
		return ok && Equal(at.Error, bt.Error) && Equal(at.Value, bt.Value)
	case *ArrayType:
		bt, ok := b.(*ArrayType)
		return ok && at.Len == bt.Len && Equal(at.Inner, bt.Inner)
	case *SliceType:
		bt, ok := b.(*SliceType)
		return ok && Equal(at.Inner, bt.Inner)
	case *TupleType:
		bt, ok := b.(*TupleType)
		if !ok || len(at.Elems) != len(bt.Elems) {
			return false
		}
		for i := range at.Elems {
			if !Equal(at.Elems[i], bt.Elems[i]) {
				return false
			}
		}
		return true
	case *FuncType:
		bt, ok := b.(*FuncType)
		if !ok || at.IsUnsafe != bt.IsUnsafe || len(at.Params) != len(bt.Params) || len(at.ComptimeParams) != len(bt.ComptimeParams) || !Equal(at.Result, bt.Result) {
			return false
		}
		for i := range at.Params {
			if !Equal(at.Params[i], bt.Params[i]) {
				return false
			}
		}
		for i := range at.ComptimeParams {
			if at.ComptimeParams[i] != bt.ComptimeParams[i] {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

func Assignable(dst, src Type) bool {
	if dst == nil && src == nil {
		return true
	}
	if IsInvalid(dst) || IsInvalid(src) || IsUnknown(dst) || IsUnknown(src) {
		return true
	}
	if Equal(dst, src) {
		return true
	}
	if IsImplicitNumericWidening(dst, src) {
		return true
	}
	if opt, ok := dst.(*OptionalType); ok && src != nil {
		return Assignable(opt.Inner, src)
	}
	if arrDst, ok := dst.(*ArrayType); ok {
		if arrSrc, ok := src.(*ArrayType); ok {
			if arrDst.Len == -2 && Equal(arrDst.Inner, arrSrc.Inner) {
				return true
			}
		}
	}
	return false
}

type NumericFamily int

const (
	NumericInvalid NumericFamily = iota
	NumericSigned
	NumericUnsigned
	NumericFloat
)

func NumericInfo(t Type) (family NumericFamily, bits int, ok bool) {
	b, ok := t.(*BuiltinType)
	if !ok {
		return NumericInvalid, 0, false
	}
	switch b.Name {
	case "i8":
		return NumericSigned, 8, true
	case "i16":
		return NumericSigned, 16, true
	case "i32":
		return NumericSigned, 32, true
	case "i64":
		return NumericSigned, 64, true
	case "isize":
		return NumericSigned, 64, true
	case "u8":
		return NumericUnsigned, 8, true
	case "u16":
		return NumericUnsigned, 16, true
	case "u32":
		return NumericUnsigned, 32, true
	case "u64":
		return NumericUnsigned, 64, true
	case "usize":
		return NumericUnsigned, 64, true
	case "f32":
		return NumericFloat, 32, true
	case "f64":
		return NumericFloat, 64, true
	default:
		return NumericInvalid, 0, false
	}
}

func IsImplicitNumericWidening(dst, src Type) bool {
	dstFamily, dstBits, okDst := NumericInfo(dst)
	srcFamily, srcBits, okSrc := NumericInfo(src)
	if !okDst || !okSrc {
		return false
	}
	return dstFamily == srcFamily && dstBits >= srcBits
}

func CommonNumericType(a, b Type) Type {
	if Equal(a, b) {
		return a
	}
	if IsImplicitNumericWidening(a, b) {
		return a
	}
	if IsImplicitNumericWidening(b, a) {
		return b
	}
	return nil
}
