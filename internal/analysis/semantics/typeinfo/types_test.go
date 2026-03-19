package typeinfo

import "testing"

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
