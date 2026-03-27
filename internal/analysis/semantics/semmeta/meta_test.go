package semmeta

import "testing"

func TestValueFlags(t *testing.T) {
	var f ValueFlags
	if f.Mutable() || f.Variadic() {
		t.Fatalf("expected zero flags to be false")
	}
	f = FlagMutable | FlagVariadic
	if !f.Mutable() || !f.Variadic() {
		t.Fatalf("expected all flags to be true")
	}
}

func TestValueSpecDefaultFlag(t *testing.T) {
	spec := ValueSpec[int]{Name: "value", Type: 7, HasDefault: true}
	if !spec.HasDefault {
		t.Fatal("expected value spec to keep default metadata")
	}
}

func TestReceiverKinds(t *testing.T) {
	cases := []struct {
		syntax string
		kind   ReceiverKind
		prefix string
	}{
		{"", ReceiverValue, ""},
		{"&", ReceiverRef, "&"},
		{"&mut ", ReceiverRefMut, "&mut "},
		{"*", ReceiverPtr, "*"},
		{"^", ReceiverRawPtr, "^"},
		{"^const ", ReceiverRawPtr, "^"},
	}
	for _, tc := range cases {
		got := ReceiverKindFromSyntax(tc.syntax)
		if got != tc.kind {
			t.Fatalf("syntax %q: got %v want %v", tc.syntax, got, tc.kind)
		}
		if got.Prefix() != tc.prefix {
			t.Fatalf("syntax %q prefix: got %q want %q", tc.syntax, got.Prefix(), tc.prefix)
		}
	}
}

func TestReceiverKeyString(t *testing.T) {
	key := ReceiverKey{Kind: ReceiverRefMut, TypeName: "Point"}
	if got := key.String(); got != "&mut Point" {
		t.Fatalf("unexpected key string: %q", got)
	}
}
