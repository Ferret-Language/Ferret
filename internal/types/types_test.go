package types

import (
	"testing"
)

func TestPrimitiveTypeString(t *testing.T) {
	tests := []struct {
		typ  SemType
		want string
	}{
		{TypeI32, "i32"},
		{TypeString, "str"},
		{TypeBool, "bool"},
		{TypeVoid, "void"},
	}

	for _, tt := range tests {
		if got := tt.typ.String(); got != tt.want {
			t.Errorf("Type.String() = %q, want %q", got, tt.want)
		}
	}
}

func TestPrimitiveTypeEquals(t *testing.T) {
	tests := []struct {
		t1    SemType
		t2    SemType
		equal bool
	}{
		{TypeI32, TypeI32, true},
		{TypeI32, TypeI64, false},
		{TypeString, TypeString, true},
		{TypeBool, TypeI32, false},
	}

	for _, tt := range tests {
		if got := tt.t1.Equals(tt.t2); got != tt.equal {
			t.Errorf("%s.Equals(%s) = %v, want %v", tt.t1, tt.t2, got, tt.equal)
		}
	}
}

func TestPrimitiveTypeSize(t *testing.T) {
	tests := []struct {
		typ  SemType
		want int
	}{
		{TypeI8, 1},
		{TypeI32, 4},
		{TypeI64, 8},
		{TypeF32, 4},
		{TypeF64, 8},
		{TypeBool, 1},
		{TypeString, 16}, // pointer + length
		{TypeVoid, 0},
	}

	for _, tt := range tests {
		if got := tt.typ.Size(); got != tt.want {
			t.Errorf("%s.Size() = %d, want %d", tt.typ, got, tt.want)
		}
	}
}

func TestArrayType(t *testing.T) {
	// Fixed-size array: [10]i32
	fixedArray := NewArray(TypeI32, 10)
	if got := fixedArray.String(); got != "[10]i32" {
		t.Errorf("ArrayType.String() = %q, want %q", got, "[10]i32")
	}
	if got := fixedArray.Size(); got != 40 {
		t.Errorf("ArrayType.Size() = %d, want %d", got, 40)
	}

	// Dynamic array: []str
	dynArray := NewArray(TypeString, -1)
	if got := dynArray.String(); got != "[]str" {
		t.Errorf("ArrayType.String() = %q, want %q", got, "[]str")
	}
	if got := dynArray.Size(); got != 16 {
		t.Errorf("ArrayType.Size() = %d, want %d (slice)", got, 16)
	}

	// Nested arrays: [][]i32
	nestedArray := NewArray(NewArray(TypeI32, -1), -1)
	if got := nestedArray.String(); got != "[][]i32" {
		t.Errorf("Nested ArrayType.String() = %q, want %q", got, "[][]i32")
	}
}

func TestArrayTypeEquals(t *testing.T) {
	arr1 := NewArray(TypeI32, 10)
	arr2 := NewArray(TypeI32, 10)
	arr3 := NewArray(TypeI32, 20)
	arr4 := NewArray(TypeI64, 10)
	arr5 := NewArray(TypeI32, -1)

	if !arr1.Equals(arr2) {
		t.Errorf("[10]i32 should equal [10]i32")
	}
	if arr1.Equals(arr3) {
		t.Errorf("[10]i32 should not equal [20]i32")
	}
	if arr1.Equals(arr4) {
		t.Errorf("[10]i32 should not equal [10]i64")
	}
	if arr1.Equals(arr5) {
		t.Errorf("[10]i32 should not equal []i32")
	}
}

func TestMapType(t *testing.T) {
	// map[str]i32
	mapType := NewMap(TypeString, TypeI32)
	if got := mapType.String(); got != "map[str]i32" {
		t.Errorf("MapType.String() = %q, want %q", got, "map[str]i32")
	}
	if got := mapType.Size(); got != 8 {
		t.Errorf("MapType.Size() = %d, want %d", got, 8)
	}

	// Nested: map[str][]i32
	nestedMap := NewMap(TypeString, NewArray(TypeI32, -1))
	if got := nestedMap.String(); got != "map[str][]i32" {
		t.Errorf("MapType.String() = %q, want %q", got, "map[str][]i32")
	}
}

func TestMapTypeEquals(t *testing.T) {
	map1 := NewMap(TypeString, TypeI32)
	map2 := NewMap(TypeString, TypeI32)
	map3 := NewMap(TypeString, TypeI64)
	map4 := NewMap(TypeI32, TypeString)

	if !map1.Equals(map2) {
		t.Errorf("map[str]i32 should equal map[str]i32")
	}
	if map1.Equals(map3) {
		t.Errorf("map[str]i32 should not equal map[str]i64")
	}
	if map1.Equals(map4) {
		t.Errorf("map[str]i32 should not equal map[i32]str")
	}
}

func TestFunctionType(t *testing.T) {
	// fn(i32, str) -> bool
	fnType := NewFunction([]ParamType{
		{Name: "a", Type: TypeI32},
		{Name: "b", Type: TypeString},
	}, TypeBool)
	want := "fn(a: i32, b: str) -> bool"
	if got := fnType.String(); got != want {
		t.Errorf("FunctionType.String() = %q, want %q", got, want)
	}

	// fn() -> void
	voidFn := NewFunction([]ParamType{}, TypeVoid)
	if got := voidFn.String(); got != "fn() -> void" {
		t.Errorf("FunctionType.String() = %q, want %q", got, "fn() -> void")
	}
}

func TestFunctionTypeEquals(t *testing.T) {
	fn1 := NewFunction([]ParamType{
		{Name: "a", Type: TypeI32},
		{Name: "b", Type: TypeString},
	}, TypeBool)
	fn2 := NewFunction([]ParamType{
		{Name: "a", Type: TypeI32},
	}, TypeBool)
	fn3 := NewFunction([]ParamType{
		{Name: "a", Type: TypeI32},
		{Name: "b", Type: TypeI32},
	}, TypeBool)
	fn4 := NewFunction([]ParamType{
		{Name: "a", Type: TypeI32},
		{Name: "b", Type: TypeString},
	}, TypeBool)
	fn5 := NewFunction([]ParamType{
		{Name: "x", Type: TypeI32},
		{Name: "y", Type: TypeString},
	}, TypeBool)

	tests := []struct {
		name string
		a, b *FunctionType
		want bool
		msg  string
	}{
		{"fn1 vs fn2", fn1, fn2, false, "expected fn1 to not equal fn2 (different param count)"},
		{"fn1 vs fn3", fn1, fn3, false, "expected fn1 to not equal fn3 (different param types)"},
		{"fn1 vs fn4", fn1, fn4, true, "expected fn1 to equal fn4 (same params and return)"},
		{"fn1 vs fn5", fn1, fn5, true, "expected fn1 to equal fn5 (different param names but same types)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Equals(tt.b); got != tt.want {
				t.Errorf("%s", tt.msg)
			}
		})
	}
}

func TestOptionalType(t *testing.T) {
	// ?i32
	opt := NewOptional(TypeI32)
	if got := opt.String(); got != "?i32" {
		t.Errorf("OptionalType.String() = %q, want %q", got, "?i32")
	}
	if got := opt.Size(); got != 5 { // 4 bytes + 1 flag
		t.Errorf("OptionalType.Size() = %d, want %d", got, 5)
	}

	// ?str
	optStr := NewOptional(TypeString)
	if got := optStr.String(); got != "?str" {
		t.Errorf("OptionalType.String() = %q, want %q", got, "?str")
	}

	// ?[]i32
	optArray := NewOptional(NewArray(TypeI32, -1))
	if got := optArray.String(); got != "?[]i32" {
		t.Errorf("OptionalType.String() = %q, want %q", got, "?[]i32")
	}
}

func TestOptionalTypeEquals(t *testing.T) {
	opt1 := NewOptional(TypeI32)
	opt2 := NewOptional(TypeI32)
	opt3 := NewOptional(TypeString)

	if !opt1.Equals(opt2) {
		t.Errorf("?i32 should equal ?i32")
	}
	if opt1.Equals(opt3) {
		t.Errorf("?i32 should not equal ?str")
	}
}

func TestHeapType(t *testing.T) {
	h := NewHeap(TypeI32)
	if got := h.String(); got != "#i32" {
		t.Errorf("HeapType.String() = %q, want %q", got, "#i32")
	}
	if got := h.Size(); got != 8 {
		t.Errorf("HeapType.Size() = %d, want %d", got, 8)
	}
	if !h.Equals(NewHeap(TypeI32)) {
		t.Errorf("#i32 should equal #i32")
	}
	if h.Equals(NewHeap(TypeI64)) {
		t.Errorf("#i32 should not equal #i64")
	}
}

func TestResultType(t *testing.T) {
	// str ! i32 (error type first, success type second)
	result := NewResult(TypeI32, TypeString)
	if got := result.String(); got != "str ! i32" {
		t.Errorf("ResultType.String() = %q, want %q", got, "str ! i32")
	}

	// Error ! []i32 (using NamedType wrapper)
	errorStruct := NewStruct("", []StructField{{Name: "msg", Type: TypeString}})
	errorType := NewNamed("Error", errorStruct)
	resultArray := NewResult(NewArray(TypeI32, -1), errorType)
	if got := resultArray.String(); got != "Error ! []i32" {
		t.Errorf("ResultType.String() = %q, want %q", got, "Error ! []i32")
	}
}

func TestResourceTypeHelpers(t *testing.T) {
	fileHandle := NewResourceNamed("__file", TypeI64)
	if !IsResourceType(fileHandle) {
		t.Fatalf("expected __file to be a resource type")
	}
	if !ContainsResourceType(fileHandle) {
		t.Fatalf("expected __file to contain a resource")
	}

	ref := NewReference(fileHandle)
	if ContainsResourceType(ref) {
		t.Fatalf("references must not be treated as owning resources")
	}

	wrapper := NewStruct("Wrapper", []StructField{
		{Name: "H", Type: fileHandle},
	})
	if !ContainsResourceType(wrapper) {
		t.Fatalf("struct containing resource field must be resource-containing")
	}
}

func TestIsImplicitlyCopyableType(t *testing.T) {
	fileHandle := NewResourceNamed("__file", TypeI64)

	primitiveStruct := NewStruct("", []StructField{
		{Name: "X", Type: TypeI32},
		{Name: "Y", Type: TypeBool},
	})
	resourceStruct := NewStruct("", []StructField{
		{Name: "H", Type: fileHandle},
	})

	tests := []struct {
		name string
		typ  SemType
		want bool
	}{
		{name: "primitive", typ: TypeI32, want: true},
		{name: "string primitive", typ: TypeString, want: true},
		{name: "shared ref", typ: NewReference(TypeI32), want: true},
		{name: "mutable ref", typ: NewMutableReference(TypeI32), want: false},
		{name: "heap owner", typ: NewHeap(TypeI32), want: false},
		{name: "fixed array of copyable", typ: NewArray(TypeI32, 4), want: true},
		{name: "fixed array of non-copyable", typ: NewArray(NewHeap(TypeI32), 4), want: false},
		{name: "dynamic array", typ: NewArray(TypeI32, -1), want: false},
		{name: "map", typ: NewMap(TypeI32, TypeI32), want: false},
		{name: "copyable struct", typ: primitiveStruct, want: true},
		{name: "resource struct", typ: resourceStruct, want: false},
		{name: "named primitive alias", typ: NewNamed("UserId", TypeI64), want: true},
		{name: "resource named", typ: fileHandle, want: false},
		{name: "function type", typ: NewFunction([]ParamType{{Name: "x", Type: TypeI32}}, TypeI32), want: true},
		{name: "interface type", typ: NewInterface(nil), want: false},
		{name: "optional copyable", typ: NewOptional(TypeI32), want: true},
		{name: "optional non-copyable", typ: NewOptional(NewHeap(TypeI32)), want: false},
		{name: "result copyable", typ: NewResult(TypeI32, TypeBool), want: true},
		{name: "result non-copyable", typ: NewResult(NewHeap(TypeI32), TypeBool), want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsImplicitlyCopyableType(tc.typ)
			if got != tc.want {
				t.Fatalf("IsImplicitlyCopyableType(%s) = %v, want %v", tc.typ.String(), got, tc.want)
			}
		})
	}
}

func TestResultTypeEquals(t *testing.T) {
	res1 := NewResult(TypeI32, TypeString)
	res2 := NewResult(TypeI32, TypeString)
	res3 := NewResult(TypeI64, TypeString)
	res4 := NewResult(TypeI32, TypeBool)

	if !res1.Equals(res2) {
		t.Errorf("i32 ! str should equal i32 ! str")
	}
	if res1.Equals(res3) {
		t.Errorf("i32 ! str should not equal i64 ! str")
	}
	if res1.Equals(res4) {
		t.Errorf("i32 ! str should not equal i32 ! bool")
	}
}

func TestStructType(t *testing.T) {
	// Named struct: Point (using NamedType wrapper)
	pointStruct := NewStruct("", []StructField{
		{Name: "x", Type: TypeF64},
		{Name: "y", Type: TypeF64},
	})
	point := NewNamed("Point", pointStruct)
	if got := point.String(); got != "Point" {
		t.Errorf("NamedType.String() = %q, want %q", got, "Point")
	}
	if got := point.Size(); got != 16 {
		t.Errorf("NamedType.Size() = %d, want %d", got, 16)
	}

	// Anonymous struct (no NamedType wrapper)
	anon := NewStruct("", []StructField{
		{Name: "id", Type: TypeI32},
		{Name: "name", Type: TypeString},
	})
	if got := anon.String(); got != "struct { .id: i32, .name: str }" {
		t.Errorf("Anonymous StructType.String() = %q, want %q", got, "struct { .id: i32, .name: str }")
	}
}

func TestStructTypeEquals(t *testing.T) {
	// Named structs use nominal equality (via NamedType wrapper)
	pointStruct1 := NewStruct("", []StructField{
		{Name: "x", Type: TypeF64},
		{Name: "y", Type: TypeF64},
	})
	pointStruct2 := NewStruct("", []StructField{
		{Name: "x", Type: TypeF64},
		{Name: "y", Type: TypeF64},
	})
	point1 := NewNamed("Point", pointStruct1)
	point2 := NewNamed("Point", pointStruct2)
	point3 := NewNamed("Point3D", pointStruct1)

	if !point1.Equals(point2) {
		t.Errorf("Point should equal Point (same name)")
	}
	if point1.Equals(point3) {
		t.Errorf("Point should not equal Point3D (different names)")
	}

	// Anonymous structs with same structure use structural equality
	anon1 := NewStruct("", []StructField{{Name: "x", Type: TypeI32}})
	anon2 := NewStruct("", []StructField{{Name: "x", Type: TypeI32}})
	if !anon1.Equals(anon2) {
		t.Errorf("Anonymous structs with same structure should equal")
	}
}

func TestEnumType(t *testing.T) {
	// Simple enum: Color (using NamedType wrapper)
	colorEnum := NewEnum("", []EnumVariant{
		{Name: "Red", Type: nil},
		{Name: "Green", Type: nil},
		{Name: "Blue", Type: nil},
	})
	color := NewNamed("Color", colorEnum)
	if got := color.String(); got != "Color" {
		t.Errorf("NamedType.String() = %q, want %q", got, "Color")
	}

	// Enum with data: Option<T> (using NamedType wrapper)
	optionEnum := NewEnum("", []EnumVariant{
		{Name: "Some", Type: TypeI32},
		{Name: "None", Type: nil},
	})
	option := NewNamed("Option", optionEnum)
	if got := option.String(); got != "Option" {
		t.Errorf("NamedType.String() = %q, want %q", got, "Option")
	}
}

func TestComplexNestedTypes(t *testing.T) {
	// map[str][]Point?
	pointStruct := NewStruct("", []StructField{
		{Name: "x", Type: TypeF64},
		{Name: "y", Type: TypeF64},
	})
	point := NewNamed("Point", pointStruct)
	optPoint := NewOptional(point)
	arrayOptPoint := NewArray(optPoint, -1)
	complexMap := NewMap(TypeString, arrayOptPoint)

	want := "map[str][]?Point"
	if got := complexMap.String(); got != want {
		t.Errorf("Complex nested type = %q, want %q", got, want)
	}
}

func TestFromTypeName(t *testing.T) {
	typ := FromTypeName(TYPE_I32)
	if !typ.Equals(TypeI32) {
		t.Errorf("FromTypeName(TYPE_I32) should equal TypeI32")
	}
}

func TestGetPrimitiveName(t *testing.T) {
	name, ok := GetPrimitiveName(TypeI32)
	if !ok {
		t.Errorf("GetPrimitiveName(TypeI32) should return ok=true")
	}
	if name != TYPE_I32 {
		t.Errorf("GetPrimitiveName(TypeI32) = %s, want %s", name, TYPE_I32)
	}

	// Non-primitive type
	arr := NewArray(TypeI32, 10)
	_, ok = GetPrimitiveName(arr)
	if ok {
		t.Errorf("GetPrimitiveName(ArrayType) should return ok=false")
	}
}

func TestTypePredicates(t *testing.T) {
	if !IsPrimitive(TypeI32) {
		t.Errorf("i32 should be primitive")
	}
	if IsPrimitive(NewArray(TypeI32, 10)) {
		t.Errorf("[]i32 should not be primitive")
	}

	if !IsNumeric(TypeI32) {
		t.Errorf("i32 should be numeric")
	}
	if !IsNumeric(TypeF64) {
		t.Errorf("f64 should be numeric")
	}
	if IsNumeric(TypeString) {
		t.Errorf("str should not be numeric")
	}

	if !IsInteger(TypeI32) {
		t.Errorf("i32 should be integer")
	}
	if IsInteger(TypeF64) {
		t.Errorf("f64 should not be integer")
	}

	if !IsFloat(TypeF64) {
		t.Errorf("f64 should be float")
	}
	if IsFloat(TypeI32) {
		t.Errorf("i32 should not be float")
	}
}
