package layout

import "testing"

func TestModuleLookup(t *testing.T) {
	if got, ok := (*Module)(nil).Lookup("Point"); ok || got != nil {
		t.Fatalf("nil module lookup must fail")
	}

	m := &Module{}
	if got, ok := m.Lookup("Point"); ok || got != nil {
		t.Fatalf("lookup with nil map must fail")
	}

	layout := &TypeLayout{Name: "Point"}
	m.Named = map[string]*TypeLayout{"Point": layout}

	got, ok := m.Lookup("Point")
	if !ok || got != layout {
		t.Fatalf("expected found layout")
	}

	if got, ok := m.Lookup("Missing"); ok || got != nil {
		t.Fatalf("missing lookup must fail")
	}
}
