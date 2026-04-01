package numeric

import "testing"

func TestRegexClassifiers(t *testing.T) {
	if !IsDecimal("123_456") || IsDecimal("0x10") {
		t.Fatalf("decimal classification mismatch")
	}
	if !IsHexadecimal("0x1f") || !IsOctal("0o77") || !IsBinary("0b1010") {
		t.Fatalf("non-decimal classifiers mismatch")
	}
	if !IsFloat("1.25") || !IsFloat("1e3") || IsFloat("1") {
		t.Fatalf("float classification mismatch")
	}
	if !IsValidNumber("0x1f") || !IsValidNumber("0b1010") || !IsValidNumber("1.5e2") {
		t.Fatalf("valid number classification mismatch")
	}
	if IsValidNumber("0b4234") {
		t.Fatalf("expected invalid binary literal rejection")
	}
}
