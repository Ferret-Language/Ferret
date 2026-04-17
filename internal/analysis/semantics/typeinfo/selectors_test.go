package typeinfo

import "testing"

func TestDerefForSelector(t *testing.T) {
	named := &NamedType{Name: "T"}

	if got := DerefForSelector(named); got != named {
		t.Fatalf("expected non-pointer type to be unchanged")
	}

	ptr := &PointerType{Inner: named}
	if got := DerefForSelector(ptr); got != named {
		t.Fatalf("expected pointer to deref to inner")
	}

	ref := &RefType{Inner: named}
	if got := DerefForSelector(ref); got != named {
		t.Fatalf("expected ref to deref to inner")
	}
}
