package packages

import "testing"

func TestParseVersionAndCompare(t *testing.T) {
	v, err := ParseVersion("v1.2.3")
	if err != nil {
		t.Fatalf("ParseVersion error: %v", err)
	}
	if v.Major != 1 || v.Minor != 2 || v.Patch != 3 {
		t.Fatalf("unexpected version parsed: %#v", v)
	}
	if _, err := ParseVersion("latest"); err == nil {
		t.Fatalf("expected latest to be rejected by ParseVersion")
	}
	if _, err := ParseVersion("bad"); err == nil {
		t.Fatalf("expected invalid format error")
	}

	if got := v.Compare(&Version{Major: 1, Minor: 2, Patch: 2}); got <= 0 {
		t.Fatalf("expected v1.2.3 > v1.2.2")
	}
}

func TestVersionConstraintsAndBestMatch(t *testing.T) {
	ok, err := MatchesConstraint("1.2.3", ">=1.2.0")
	if err != nil || !ok {
		t.Fatalf(">= constraint failed: ok=%v err=%v", ok, err)
	}
	ok, err = MatchesConstraint("1.2.3", "~1.2.0")
	if err != nil || !ok {
		t.Fatalf("~ constraint failed: ok=%v err=%v", ok, err)
	}
	ok, err = MatchesConstraint("1.2.3", "^1.0.0")
	if err != nil || !ok {
		t.Fatalf("^ constraint failed: ok=%v err=%v", ok, err)
	}
	ok, err = MatchesConstraint("1.2.3", "<1.0.0")
	if err != nil || ok {
		t.Fatalf("expected <1.0.0 to fail")
	}

	versions := []string{"1.0.0", "1.2.0", "1.2.5", "2.0.0"}
	best, err := FindBestMatch(versions, "~1.2.0")
	if err != nil || best != "1.2.5" {
		t.Fatalf("best ~1.2.0 mismatch: %q err=%v", best, err)
	}
	best, err = FindBestMatchMultipleConstraints(versions, []string{">=1.2.0", "<2.0.0"})
	if err != nil || best != "1.2.5" {
		t.Fatalf("best multi-constraint mismatch: %q err=%v", best, err)
	}
}
