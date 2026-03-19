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
