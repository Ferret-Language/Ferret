package numeric

import "testing"

func TestValueHelpers(t *testing.T) {
	if CleanNumberString("1_2_3") != "123" {
		t.Fatalf("CleanNumberString failed")
	}
	if !IsImaginary("12i") || IsImaginary("12") {
		t.Fatalf("IsImaginary mismatch")
	}
}

func TestStringParsingAndFits(t *testing.T) {
	v, err := StringToBigInt("-0x10")
	if err != nil || v.String() != "-16" {
		t.Fatalf("StringToBigInt mismatch: v=%v err=%v", v, err)
	}
	if _, err := StringToBigInt(""); err == nil {
		t.Fatalf("expected error for empty integer")
	}
	if f, err := StringToFloat("1_2.5"); err != nil || f != 12.5 {
		t.Fatalf("StringToFloat mismatch: %v %v", f, err)
	}

	if !FitsIntegerLiteral("127", 8, true) || FitsIntegerLiteral("128", 8, true) {
		t.Fatalf("signed int fit mismatch")
	}
	if !FitsIntegerLiteral("255", 8, false) || FitsIntegerLiteral("-1", 8, false) {
		t.Fatalf("unsigned int fit mismatch")
	}
	if !FitsFloatLiteral("1.0", 32) || !FitsFloatLiteral("1.0", 64) || FitsFloatLiteral("1.0", 16) {
		t.Fatalf("float fit mismatch")
	}
	if !FitsIntegerLiteralInFloat("1", 32) || !FitsIntegerLiteralInFloat("1", 64) || FitsIntegerLiteralInFloat("1", 16) {
		t.Fatalf("integer-in-float fit mismatch")
	}
}

func TestValidateLiteralAndCanonicalizeInteger(t *testing.T) {
	if err := ValidateLiteral("0b1010"); err != nil {
		t.Fatalf("expected valid binary literal, got %v", err)
	}
	if err := ValidateLiteral("1.5e2"); err != nil {
		t.Fatalf("expected valid scientific literal, got %v", err)
	}
	if err := ValidateLiteral("0b4234"); err == nil || err.Error() != "invalid binary literal 0b4234" {
		t.Fatalf("unexpected invalid-binary result: %v", err)
	}
	if err := ValidateLiteral("1.2e+"); err == nil || err.Error() != "invalid float literal 1.2e+" {
		t.Fatalf("unexpected invalid-float result: %v", err)
	}
	if got, err := CanonicalizeIntegerLiteral("0x10"); err != nil || got != "16" {
		t.Fatalf("CanonicalizeIntegerLiteral(0x10) = %q, %v", got, err)
	}
}
