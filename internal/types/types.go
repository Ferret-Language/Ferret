package types

import (
	"fmt"
	"strings"
)

// SemType is the semantic representation of types in the Ferret language.
//
// Design principles:
// - Types are immutable after creation
// - SemType equality is structural (deep comparison)
// - All types can be displayed as strings
type SemType interface {
	// String returns a human-readable representation of the type
	String() string

	// Equals checks structural equality with another type
	Equals(other SemType) bool

	// Size returns the size in bytes (for codegen)
	// Returns -1 for types without known size (void, unknown)
	Size() int

	// isType is a marker method to prevent external implementation
	isType()
}

// Primitive Types (built-in) likely can be represented as numbers

// UntypedKind represents the kind of untyped literal
type UntypedKind int

const (
	UntypedNone  UntypedKind = iota // Not an untyped literal
	UntypedInt                      // Untyped integer literal
	UntypedFloat                    // Untyped float literal
)

// PrimitiveType represents built-in scalar types (i32, str, bool, etc.)
type PrimitiveType struct {
	name        TYPE_NAME
	size        int         // size in bytes
	untypedKind UntypedKind // kind of untyped literal (UntypedNone for typed)
}

func NewPrimitive(name TYPE_NAME) *PrimitiveType {
	size := getPrimitiveSize(name)
	return &PrimitiveType{name: name, size: size, untypedKind: UntypedNone}
}

// NewUntypedInt creates an untyped integer literal type
func NewUntypedInt() *PrimitiveType {
	return &PrimitiveType{name: TYPE_UNTYPED, size: -1, untypedKind: UntypedInt}
}

// NewUntypedFloat creates an untyped float literal type
func NewUntypedFloat() *PrimitiveType {
	return &PrimitiveType{name: TYPE_UNTYPED, size: -1, untypedKind: UntypedFloat}
}

func (p *PrimitiveType) String() string { return string(p.name) }
func (p *PrimitiveType) Size() int      { return p.size }
func (p *PrimitiveType) isType()        {}
func (p *PrimitiveType) Equals(other SemType) bool {
	if o, ok := other.(*PrimitiveType); ok {
		return p.name == o.name && p.untypedKind == o.untypedKind
	}
	return false
}

// GetName returns the primitive type name (for compatibility during migration)
func (p *PrimitiveType) GetName() TYPE_NAME {
	return p.name
}

// IsUntyped returns true if this is an untyped literal type
func (p *PrimitiveType) IsUntyped() bool {
	return p.untypedKind != UntypedNone
}

// IsUntypedInt returns true if this is an untyped integer literal
func (p *PrimitiveType) IsUntypedInt() bool {
	return p.untypedKind == UntypedInt
}

// IsUntypedFloat returns true if this is an untyped float literal
func (p *PrimitiveType) IsUntypedFloat() bool {
	return p.untypedKind == UntypedFloat
}

// GetUntypedKind returns the kind of untyped literal
func (p *PrimitiveType) GetUntypedKind() UntypedKind {
	return p.untypedKind
}

func getPrimitiveSize(name TYPE_NAME) int {
	switch name {
	case TYPE_I8, TYPE_U8, TYPE_BYTE:
		return 1
	case TYPE_I16, TYPE_U16:
		return 2
	case TYPE_I32, TYPE_U32, TYPE_F32, TYPE_CHAR:
		return 4
	case TYPE_I64, TYPE_U64, TYPE_F64:
		return 8
	case TYPE_I128, TYPE_U128, TYPE_F128:
		return 16
	case TYPE_I256, TYPE_U256, TYPE_F256:
		return 32
	case TYPE_BOOL:
		return 1
	case TYPE_STRING:
		return 16 // pointer + length
	case TYPE_VOID, TYPE_NONE:
		return 0
	case TYPE_UNKNOWN, TYPE_UNTYPED:
		return -1
	default:
		return -1
	}
}

// Array Types

// ArrayType represents array types: [N]T or []T (dynamic)
type ArrayType struct {
	Element SemType
	Length  int // -1 for dynamic arrays []T
}

func NewArray(element SemType, length int) *ArrayType {
	return &ArrayType{Element: element, Length: length}
}

func (a *ArrayType) String() string {
	if a.Length < 0 {
		return fmt.Sprintf("[]%s", a.Element.String())
	}
	return fmt.Sprintf("[%d]%s", a.Length, a.Element.String())
}

func (a *ArrayType) Size() int {
	if a.Length < 0 {
		return 16 // slice: pointer + length + capacity
	}
	return a.Element.Size() * a.Length
}

func (a *ArrayType) isType() {}

func (a *ArrayType) Equals(other SemType) bool {
	if o, ok := other.(*ArrayType); ok {
		return a.Length == o.Length && a.Element.Equals(o.Element)
	}
	return false
}

// Map Types

// MapType represents map types: map[K]V
type MapType struct {
	Key   SemType
	Value SemType
}

func NewMap(key, value SemType) *MapType {
	return &MapType{Key: key, Value: value}
}

func (m *MapType) String() string {
	return fmt.Sprintf("map[%s]%s", m.Key.String(), m.Value.String())
}

func (m *MapType) Size() int {
	return 8 // pointer to map structure (hash table)
}

func (m *MapType) isType() {}

func (m *MapType) Equals(other SemType) bool {
	if o, ok := other.(*MapType); ok {
		return m.Key.Equals(o.Key) && m.Value.Equals(o.Value)
	}
	return false
}

// Function Types

// FunctionType represents function signatures: fn(T1, T2) -> R
type ParamType struct {
	Name       string
	Type       SemType
	IsVariadic bool // true if this is a variadic parameter (...type)
}

func (p *ParamType) String() string {
	if p.IsVariadic {
		return fmt.Sprintf("%s: ...%s", p.Name, p.Type.String())
	}
	return fmt.Sprintf("%s: %s", p.Name, p.Type.String())
}

type FunctionType struct {
	Params []ParamType
	Return SemType
}

func NewFunction(params []ParamType, ret SemType) *FunctionType {
	return &FunctionType{Params: params, Return: ret}
}

func (f *FunctionType) String() string {
	params := make([]string, len(f.Params))
	for i, p := range f.Params {
		params[i] = p.String()
	}
	return fmt.Sprintf("fn(%s) -> %s", strings.Join(params, ", "), f.Return.String())
}

func (f *FunctionType) Size() int {
	return 8 // function pointer
}

func (f *FunctionType) isType() {}

func (f *FunctionType) Equals(other SemType) bool {
	if ft, ok := other.(*FunctionType); ok {
		// Check return type
		if !f.Return.Equals(ft.Return) {
			return false
		}
		// Check parameter count
		if len(f.Params) != len(ft.Params) {
			return false
		}
		// Check each parameter type (structural typing - ignore names)
		// Parameter names are for documentation/error messages only
		for i := range f.Params {
			if !f.Params[i].Type.Equals(ft.Params[i].Type) {
				return false
			}
			// Check variadic flag must match
			if f.Params[i].IsVariadic != ft.Params[i].IsVariadic {
				return false
			}
		}
		return true
	}
	return false
}

// OptionalType represents nullable types: ?T
type OptionalType struct {
	Inner SemType
}

func NewOptional(inner SemType) *OptionalType {
	return &OptionalType{Inner: inner}
}

func (o *OptionalType) String() string {
	return fmt.Sprintf("?%s", o.Inner.String())
}

func (o *OptionalType) Size() int {
	return o.Inner.Size() + 1 // value + presence flag
}

func (o *OptionalType) isType() {}

func (o *OptionalType) Equals(other SemType) bool {
	if ot, ok := other.(*OptionalType); ok {
		return o.Inner.Equals(ot.Inner)
	}
	return false
}

// UnionType represents union types: union { T1, T2, ..., TN }
type UnionType struct {
	Variants []SemType
}

func NewUnion(variants []SemType) *UnionType {
	return &UnionType{Variants: variants}
}

func (u *UnionType) String() string {
	if len(u.Variants) == 0 {
		return "union {}"
	}
	parts := make([]string, len(u.Variants))
	for i, v := range u.Variants {
		parts[i] = v.String()
	}
	return fmt.Sprintf("union {%s}", strings.Join(parts, ", "))
}

func (u *UnionType) Size() int {
	// Union size is the max size of variants
	maxSize := 0
	for _, v := range u.Variants {
		if s := v.Size(); s > maxSize {
			maxSize = s
		}
	}
	return maxSize + 4 // max size + discriminant (assuming 4 bytes for tag)
}

func (u *UnionType) isType() {}

func (u *UnionType) Equals(other SemType) bool {
	if ut, ok := other.(*UnionType); ok {
		if len(u.Variants) != len(ut.Variants) {
			return false
		}
		for i, v := range u.Variants {
			if !v.Equals(ut.Variants[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// ReferenceType represents reference types: &T
type ReferenceType struct {
	Inner   SemType
	Mutable bool
}

func NewReference(inner SemType) *ReferenceType {
	return &ReferenceType{Inner: inner}
}

func NewMutableReference(inner SemType) *ReferenceType {
	return &ReferenceType{Inner: inner, Mutable: true}
}

func (r *ReferenceType) String() string {
	if r.Mutable {
		return fmt.Sprintf("&mut %s", r.Inner.String())
	}
	return fmt.Sprintf("&%s", r.Inner.String())
}

func (r *ReferenceType) Size() int {
	return 8 // pointer size
}

func (r *ReferenceType) isType() {}

func (r *ReferenceType) Equals(other SemType) bool {
	if rt, ok := other.(*ReferenceType); ok {
		return r.Mutable == rt.Mutable && r.Inner.Equals(rt.Inner)
	}
	return false
}

// ResultType represents result types with error handling: E ! T
// Error type first, then success type (consistent with expr! syntax)
type ResultType struct {
	Ok  SemType // Success type
	Err SemType // Error type
}

func NewResult(ok, err SemType) *ResultType {
	return &ResultType{Ok: ok, Err: err}
}

func (r *ResultType) String() string {
	return fmt.Sprintf("%s ! %s", r.Err.String(), r.Ok.String())
}

func (r *ResultType) Size() int {
	okSize := r.Ok.Size()
	errSize := r.Err.Size()
	if okSize > errSize {
		return okSize + 1 // larger variant + discriminant
	}
	return errSize + 1
}

func (r *ResultType) isType() {}

func (r *ResultType) Equals(other SemType) bool {
	if rt, ok := other.(*ResultType); ok {
		return r.Ok.Equals(rt.Ok) && r.Err.Equals(rt.Err)
	}
	return false
}

// StructField represents a field in a struct
type StructField struct {
	Name string
	Type SemType
}

// StructType represents struct types (always anonymous at this level)
// Named types like "type Point struct{}" use NamedType wrapper
type StructType struct {
	ID     string // Unique ID for type identity (from AST or generated)
	Fields []StructField
}

func NewStruct(name string, fields []StructField) *StructType {
	// Note: 'name' parameter is deprecated - use NamedType wrapper instead
	return &StructType{Fields: fields}
}

func (s *StructType) String() string {
	// Anonymous struct - show structure with smart truncation
	const maxFieldsToShow = 3 // Show first 2 and last 1 if there are many fields

	fields := make([]string, 0, len(s.Fields))
	for i, f := range s.Fields {
		fieldTypeStr := f.Type.String()

		// Replace "untyped" with concrete default type for better error messages
		if fieldTypeStr == "untyped" {
			if p, ok := f.Type.(*PrimitiveType); ok {
				switch p.untypedKind {
				case UntypedInt:
					fieldTypeStr = string(DEFAULT_INT_TYPE)
				case UntypedFloat:
					fieldTypeStr = string(DEFAULT_FLOAT_TYPE)
				}
			}
		}

		// For long field lists, show: first 2, ..., last 1
		if len(s.Fields) > maxFieldsToShow+1 {
			if i < 2 {
				fields = append(fields, fmt.Sprintf(".%s: %s", f.Name, fieldTypeStr))
			} else if i == 2 {
				fields = append(fields, "...")
			} else if i == len(s.Fields)-1 {
				fields = append(fields, fmt.Sprintf(".%s: %s", f.Name, fieldTypeStr))
			}
		} else {
			fields = append(fields, fmt.Sprintf(".%s: %s", f.Name, fieldTypeStr))
		}
	}

	return fmt.Sprintf("struct { %s }", strings.Join(fields, ", "))
}

func (s *StructType) Size() int {
	total := 0
	for _, f := range s.Fields {
		total += f.Type.Size()
	}
	return total
}

func (s *StructType) isType() {}

func (s *StructType) Equals(other SemType) bool {
	if st, ok := other.(*StructType); ok {
		// Structs use structural equality (compare field structure)
		if len(s.Fields) != len(st.Fields) {
			return false
		}
		for i := range s.Fields {
			if s.Fields[i].Name != st.Fields[i].Name {
				return false
			}
			if !s.Fields[i].Type.Equals(st.Fields[i].Type) {
				return false
			}
		}
		return true
	}
	return false
}

// EnumVariant represents a variant in an enum
type EnumVariant struct {
	Name  string
	Value int64   // numeric value for this variant
	Type  SemType // nil for simple numeric enums, non-nil for complex enums (future)
}

// EnumType represents enum types (sum types/tagged unions)
// Named types like "type Color enum{}" use NamedType wrapper
type EnumType struct {
	ID       string // Unique ID for type identity
	Variants []EnumVariant
}

func NewEnum(name string, variants []EnumVariant) *EnumType {
	// Note: 'name' parameter is deprecated - use NamedType wrapper instead
	return &EnumType{Variants: variants}
}

func (e *EnumType) String() string {
	// Anonymous enum - show variants
	variantNames := make([]string, len(e.Variants))
	for i, v := range e.Variants {
		variantNames[i] = v.Name
	}
	return fmt.Sprintf("enum { %s }", strings.Join(variantNames, ", "))
}

func (e *EnumType) Size() int {
	// Discriminant + largest variant
	maxSize := 0
	for _, v := range e.Variants {
		if v.Type != nil {
			if s := v.Type.Size(); s > maxSize {
				maxSize = s
			}
		}
	}
	return 4 + maxSize // 4 bytes for discriminant
}

func (e *EnumType) isType() {}

func (e *EnumType) Equals(other SemType) bool {
	if et, ok := other.(*EnumType); ok {
		// Enums use structural equality (compare variants)
		if len(e.Variants) != len(et.Variants) {
			return false
		}
		for i := range e.Variants {
			if e.Variants[i].Name != et.Variants[i].Name {
				return false
			}
			// Check variant types
			if (e.Variants[i].Type == nil) != (et.Variants[i].Type == nil) {
				return false
			}
			if e.Variants[i].Type != nil && !e.Variants[i].Type.Equals(et.Variants[i].Type) {
				return false
			}
		}
		return true
	}
	return false
}

// InterfaceMethod represents a method signature in an interface
type InterfaceMethod struct {
	Name     string        // Method name
	FuncType *FunctionType // Method signature
}

// InterfaceType represents interface types: interface { method1(...), method2(...) }
// Named interfaces like "type Shape any" use NamedType wrapper
type InterfaceType struct {
	ID      string            // Unique ID for type identity (from AST or generated)
	Methods []InterfaceMethod // Required methods
}

func NewInterface(methods []InterfaceMethod) *InterfaceType {
	return &InterfaceType{Methods: methods}
}

func (i *InterfaceType) String() string {
	if len(i.Methods) == 0 {
		return "interface{}" // Empty interface
	}
	methodStrs := make([]string, len(i.Methods))
	for j, m := range i.Methods {
		methodStrs[j] = fmt.Sprintf("%s%s", m.Name, m.FuncType.String()[2:]) // Remove "fn" prefix
	}
	return fmt.Sprintf("interface { %s }", strings.Join(methodStrs, ", "))
}

func (i *InterfaceType) Size() int {
	return 16 // Interface value: pointer + type info
}

func (i *InterfaceType) isType() {}

func (i *InterfaceType) Equals(other SemType) bool {
	if it, ok := other.(*InterfaceType); ok {
		// Interfaces use structural equality (compare methods)
		if len(i.Methods) != len(it.Methods) {
			return false
		}
		// Build method maps for comparison
		thisMethods := make(map[string]*FunctionType)
		thatMethods := make(map[string]*FunctionType)
		for _, m := range i.Methods {
			thisMethods[m.Name] = m.FuncType
		}
		for _, m := range it.Methods {
			thatMethods[m.Name] = m.FuncType
		}
		// Check all methods match
		for name, funcType := range thisMethods {
			if otherFuncType, ok := thatMethods[name]; !ok || !funcType.Equals(otherFuncType) {
				return false
			}
		}
		return true
	}
	return false
}

// NamedType represents a named type declaration: type Name UnderlyingType
// This wraps any SemType with a name, enabling:
// - Nominal typing for user-defined types
// - Type aliases that preserve identity (type A X; type B A)
// - Method attachment to named types
type NamedType struct {
	Name       string  // The declared name (e.g., "Point", "UserId")
	Underlying SemType // The actual type (struct, primitive, another NamedType, etc.)
}

func NewNamed(name string, underlying SemType) *NamedType {
	return &NamedType{Name: name, Underlying: underlying}
}

func (n *NamedType) String() string {
	return n.Name // Named types display their name
}

func (n *NamedType) Size() int {
	return n.Underlying.Size()
}

func (n *NamedType) isType() {}

func (n *NamedType) Equals(other SemType) bool {
	// Named types use nominal equality - must have the same name
	if nt, ok := other.(*NamedType); ok {
		return n.Name == nt.Name
	}
	return false
}

// Unwrap returns the underlying type, following the chain if nested
// (e.g., type A X; type B A; type C B -> C.Unwrap() returns X)
func (n *NamedType) Unwrap() SemType {
	current := n.Underlying
	for {
		if named, ok := current.(*NamedType); ok {
			current = named.Underlying
		} else {
			return current
		}
	}
}

// Commonly used types (initialized in init())
var (
	TypeI8      SemType
	TypeI16     SemType
	TypeI32     SemType
	TypeI64     SemType
	TypeI128    SemType
	TypeI256    SemType
	TypeU8      SemType
	TypeU16     SemType
	TypeU32     SemType
	TypeU64     SemType
	TypeU128    SemType
	TypeU256    SemType
	TypeF32     SemType
	TypeF64     SemType
	TypeF128    SemType
	TypeF256    SemType
	TypeBool    SemType
	TypeString  SemType
	TypeNone    SemType
	TypeVoid    SemType
	TypeByte    SemType
	TypeChar    SemType
	TypeUnknown SemType

	// Untyped literal types
	TypeUntypedInt   SemType
	TypeUntypedFloat SemType

	// PrimitiveTypeByName maps primitive type names to their canonical instances.
	PrimitiveTypeByName map[TYPE_NAME]SemType

	// BuiltinTypeNameSet is a fast lookup for builtin type names.
	BuiltinTypeNameSet map[string]struct{}

	// BuiltinTypes lists the builtin types registered in the universe scope.
	BuiltinTypes []SemType
)

func init() {
	TypeI8 = NewPrimitive(TYPE_I8)
	TypeI16 = NewPrimitive(TYPE_I16)
	TypeI32 = NewPrimitive(TYPE_I32)
	TypeI64 = NewPrimitive(TYPE_I64)
	TypeI128 = NewPrimitive(TYPE_I128)
	TypeI256 = NewPrimitive(TYPE_I256)
	TypeU8 = NewPrimitive(TYPE_U8)
	TypeU16 = NewPrimitive(TYPE_U16)
	TypeU32 = NewPrimitive(TYPE_U32)
	TypeU64 = NewPrimitive(TYPE_U64)
	TypeU128 = NewPrimitive(TYPE_U128)
	TypeU256 = NewPrimitive(TYPE_U256)
	TypeF32 = NewPrimitive(TYPE_F32)
	TypeF64 = NewPrimitive(TYPE_F64)
	TypeF128 = NewPrimitive(TYPE_F128)
	TypeF256 = NewPrimitive(TYPE_F256)
	TypeBool = NewPrimitive(TYPE_BOOL)
	TypeString = NewPrimitive(TYPE_STRING)
	TypeNone = NewPrimitive(TYPE_NONE)
	TypeVoid = NewPrimitive(TYPE_VOID)
	TypeByte = NewPrimitive(TYPE_BYTE)
	TypeChar = NewPrimitive(TYPE_CHAR)
	TypeUnknown = NewPrimitive(TYPE_UNKNOWN)

	TypeUntypedInt = NewUntypedInt()
	TypeUntypedFloat = NewUntypedFloat()

	PrimitiveTypeByName = map[TYPE_NAME]SemType{
		TYPE_I8:      TypeI8,
		TYPE_I16:     TypeI16,
		TYPE_I32:     TypeI32,
		TYPE_I64:     TypeI64,
		TYPE_I128:    TypeI128,
		TYPE_I256:    TypeI256,
		TYPE_U8:      TypeU8,
		TYPE_U16:     TypeU16,
		TYPE_U32:     TypeU32,
		TYPE_U64:     TypeU64,
		TYPE_U128:    TypeU128,
		TYPE_U256:    TypeU256,
		TYPE_F32:     TypeF32,
		TYPE_F64:     TypeF64,
		TYPE_F128:    TypeF128,
		TYPE_F256:    TypeF256,
		TYPE_BOOL:    TypeBool,
		TYPE_STRING:  TypeString,
		TYPE_NONE:    TypeNone,
		TYPE_VOID:    TypeVoid,
		TYPE_BYTE:    TypeByte,
		TYPE_CHAR:    TypeChar,
		TYPE_UNKNOWN: TypeUnknown,
	}

	BuiltinTypeNameSet = make(map[string]struct{}, len(BuiltinTypeNames))
	BuiltinTypes = make([]SemType, 0, len(BuiltinTypeNames))
	for _, name := range BuiltinTypeNames {
		BuiltinTypeNameSet[string(name)] = struct{}{}
		if typ, ok := PrimitiveTypeByName[name]; ok && typ != nil {
			BuiltinTypes = append(BuiltinTypes, typ)
		}
	}
}

// UnwrapType recursively unwraps NamedTypes to get the underlying structural type
// This is useful when you need to work with the actual structure (e.g., struct fields)
func UnwrapType(t SemType) SemType {
	if named, ok := t.(*NamedType); ok {
		return named.Unwrap()
	}
	return t
}

// GetTypeName returns the name of a type if it's a NamedType, empty string otherwise
func GetTypeName(t SemType) string {
	if named, ok := t.(*NamedType); ok {
		return named.Name
	}
	return ""
}

// FromTypeName converts TYPE_NAME to Type (for backward compatibility during migration)
func FromTypeName(name TYPE_NAME) SemType {
	// Only create primitives for known type names
	// Note: TYPE_STRUCT, TYPE_ENUM, TYPE_MAP, TYPE_ARRAY, TYPE_FUNC, TYPE_INTERFACE
	// are keyword tokens used by parser, NOT valid primitive type names.
	// Complex types (struct{}, enum{}, map[], []T, fn()) are created from AST nodes.
	switch name {
	case TYPE_I8, TYPE_I16, TYPE_I32, TYPE_I64, TYPE_I128, TYPE_I256,
		TYPE_U8, TYPE_U16, TYPE_U32, TYPE_U64, TYPE_U128, TYPE_U256,
		TYPE_F32, TYPE_F64, TYPE_F128, TYPE_F256,
		TYPE_STRING, TYPE_BOOL, TYPE_NONE, TYPE_VOID, TYPE_BYTE, TYPE_CHAR,
		TYPE_UNTYPED, TYPE_UNKNOWN:
		return NewPrimitive(name)
	default:
		// Unknown type name - not a primitive
		return TypeUnknown
	}
}

// IsPrimitive checks if a type is a primitive type
func IsPrimitive(t SemType) bool {
	_, ok := t.(*PrimitiveType)
	return ok
}

// IsNumeric checks if a type is a numeric type
func IsNumeric(t SemType) bool {
	if p, ok := t.(*PrimitiveType); ok {
		return IsNumericTypeName(p.name)
	}
	return false
}

// IsInteger checks if a type is an integer type
func IsInteger(t SemType) bool {
	if p, ok := t.(*PrimitiveType); ok {
		return IsIntegerTypeName(p.name)
	}
	return false
}

// IsFloat checks if a type is a floating-point type
func IsFloat(t SemType) bool {
	if p, ok := t.(*PrimitiveType); ok {
		return IsFloatTypeName(p.name)
	}
	return false
}

// GetPrimitiveName extracts TYPE_NAME from PrimitiveType (for compatibility)
func GetPrimitiveName(t SemType) (TYPE_NAME, bool) {
	if p, ok := t.(*PrimitiveType); ok {
		return p.name, true
	}
	return TYPE_UNKNOWN, false
}

// IsUntyped checks if a type is an untyped literal type
func IsUntyped(t SemType) bool {
	if p, ok := t.(*PrimitiveType); ok {
		return p.IsUntyped()
	}
	return false
}

// IsUntypedInt checks if a type is an untyped integer literal
func IsUntypedInt(t SemType) bool {
	if p, ok := t.(*PrimitiveType); ok {
		return p.IsUntypedInt()
	}
	return false
}

// IsUntypedFloat checks if a type is an untyped float literal
func IsUntypedFloat(t SemType) bool {
	if p, ok := t.(*PrimitiveType); ok {
		return p.IsUntypedFloat()
	}
	return false
}

// ResolveUntypedType resolves an untyped numeric type using the expected type or defaults.
func ResolveUntypedType(untypedType SemType, expected SemType) SemType {
	if !IsUntyped(untypedType) {
		return untypedType
	}
	if expected == nil {
		expected = TypeUnknown
	}
	if !expected.Equals(TypeUnknown) {
		if IsUntypedInt(untypedType) && IsNumeric(expected) {
			return expected
		}
		if IsUntypedFloat(untypedType) && IsFloat(expected) {
			return expected
		}
	}
	if primType, ok := untypedType.(*PrimitiveType); ok {
		if primType.IsUntypedInt() {
			return FromTypeName(DEFAULT_INT_TYPE)
		} else if primType.IsUntypedFloat() {
			return FromTypeName(DEFAULT_FLOAT_TYPE)
		}
	}
	return untypedType
}

// IsNumericType checks if a type is a numeric type (integer or float)
func IsNumericType(t SemType) bool {
	if p, ok := t.(*PrimitiveType); ok {
		name := p.name
		// Check if it's a typed numeric type
		isTypedNumeric := IsNumericTypeName(name)

		// Check if it's an untyped numeric literal
		isUntypedNumeric := name == TYPE_UNTYPED && (p.untypedKind == UntypedInt || p.untypedKind == UntypedFloat)

		return isTypedNumeric || isUntypedNumeric
	}
	return false
}

// IsOptional checks if a type is an optional type
func IsOptional(t SemType) bool {
	if t == nil {
		return false
	}
	_, ok := t.(*OptionalType)
	return ok
}
