package typeinfo

import (
	"testing"

	"compiler/internal/frontend/ast"
)

func TestRefAndRawTypeString(t *testing.T) {
	immutable := &RefType{Inner: &BuiltinType{Name: "i32"}}
	if got := immutable.String(); got != "&i32" {
		t.Fatalf("expected &i32, got %q", got)
	}
	mutable := &RefType{Mutable: true, Inner: &BuiltinType{Name: "i32"}}
	if got := mutable.String(); got != "&mut i32" {
		t.Fatalf("expected &mut i32, got %q", got)
	}
	raw := &RawPtrType{Inner: &BuiltinType{Name: "u8"}}
	if got := raw.String(); got != "^u8" {
		t.Fatalf("expected ^u8, got %q", got)
	}
	rawConst := &RawPtrType{Const: true, Inner: &BuiltinType{Name: "u8"}}
	if got := rawConst.String(); got != "^const u8" {
		t.Fatalf("expected ^const u8, got %q", got)
	}
}

func TestEqualRefAndRawTypes(t *testing.T) {
	leftRef := &RefType{Inner: &BuiltinType{Name: "i32"}}
	rightRef := &RefType{Inner: &BuiltinType{Name: "i32"}}
	if !Equal(leftRef, rightRef) {
		t.Fatal("expected immutable refs to compare equal")
	}
	if Equal(leftRef, &RefType{Mutable: true, Inner: &BuiltinType{Name: "i32"}}) {
		t.Fatal("expected mutable and immutable refs to differ")
	}
	leftRaw := &RawPtrType{Inner: &BuiltinType{Name: "u8"}}
	rightRaw := &RawPtrType{Inner: &BuiltinType{Name: "u8"}}
	if !Equal(leftRaw, rightRaw) {
		t.Fatal("expected raw ptr types to compare equal")
	}
	if Equal(leftRaw, &RawPtrType{Const: true, Inner: &BuiltinType{Name: "u8"}}) {
		t.Fatal("expected raw mutable and raw const ptrs to differ")
	}
}

func TestCommonNumericTypeRejectsNonNumericEqualTypes(t *testing.T) {
	typeParam := &TypeParam{Name: "T"}
	if got := CommonNumericType(typeParam, typeParam); got != nil {
		t.Fatalf("expected no numeric common type for type param, got %#v", got)
	}
	point := &NamedType{ModuleKey: "main", Name: "Point"}
	if got := CommonNumericType(point, point); got != nil {
		t.Fatalf("expected no numeric common type for named type, got %#v", got)
	}
}

func TestArrayTypePreservesDeferredLengthExpr(t *testing.T) {
	sizeExpr := &ast.BinaryExpr{
		Left:  &ast.Ident{Path: []string{"N"}},
		Op:    "+",
		Right: &ast.NumberLit{Value: "1"},
	}
	arr := &ArrayType{
		Inner:    &BuiltinType{Name: "i32"},
		Len:      ArrayLenDeferred,
		SizeExpr: sizeExpr,
	}
	if got := arr.String(); got != "[N + 1]i32" {
		t.Fatalf("expected deferred array string, got %q", got)
	}
	if !Equal(arr, &ArrayType{
		Inner:    &BuiltinType{Name: "i32"},
		Len:      ArrayLenDeferred,
		SizeExpr: &ast.BinaryExpr{Left: &ast.Ident{Path: []string{"N"}}, Op: "+", Right: &ast.NumberLit{Value: "1"}},
	}) {
		t.Fatal("expected deferred array types with the same size expr to compare equal")
	}
}

func TestReceiverHelpers(t *testing.T) {
	point := &NamedType{ModuleKey: "main", Name: "Point"}

	if got, ok := ReceiverBaseNamedType(&RefType{Mutable: true, Inner: point}); !ok || got != point {
		t.Fatalf("expected ref receiver base Point, got %#v, %v", got, ok)
	}

	if got, ok := ReceiverKeyFromType(&PointerType{Inner: point}); !ok || got != (ReceiverKey{Kind: ReceiverPtr, TypeName: "Point"}) {
		t.Fatalf("expected *Point receiver key, got %#v, %v", got, ok)
	}

	if got := ApplyReceiverShape(point, ReceiverRefMut); got.String() != "&mut Point" {
		t.Fatalf("expected &mut Point receiver shape, got %s", got.String())
	}

	if got := ReceiverTypeFromKey(point, ReceiverKey{Kind: ReceiverRef, TypeName: "Point"}); got == nil || got.String() != "&Point" {
		if got == nil {
			t.Fatal("expected receiver type from key, got nil")
		}
		t.Fatalf("expected &Point receiver type, got %s", got.String())
	}
}

func TestInstantiateTypeHandlesRecursiveStruct(t *testing.T) {
	node := &StructType{}
	field := &StructField{Name: "Next", Type: &PointerType{Inner: node}}
	node.OrderedFields = []*StructField{field}
	node.Fields = map[string]*StructField{"Next": field}

	inst, ok := InstantiateType(node, nil).(*StructType)
	if !ok || inst == nil {
		t.Fatalf("expected struct instantiation, got %#v", inst)
	}
	next := inst.Fields["Next"]
	if next == nil {
		t.Fatal("expected recursive field")
	}
	ptr, ok := next.Type.(*PointerType)
	if !ok || ptr == nil {
		t.Fatalf("expected pointer field, got %#v", next.Type)
	}
	if ptr.Inner != inst {
		t.Fatalf("expected recursive pointer to instantiated struct, got %#v", ptr.Inner)
	}
}
