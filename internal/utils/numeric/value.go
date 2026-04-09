package numeric

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

func CleanNumberString(s string) string {
	return strings.ReplaceAll(s, "_", "")
}

func IsImaginary(s string) bool {
	return strings.HasSuffix(CleanNumberString(s), "i")
}

func LooksFloatLike(s string) bool {
	clean := CleanNumberString(s)
	if clean == "" {
		return false
	}
	lower := strings.ToLower(clean)
	if strings.HasPrefix(lower, "0x") || strings.HasPrefix(lower, "0o") || strings.HasPrefix(lower, "0b") {
		return false
	}
	return strings.ContainsAny(clean, ".eE")
}

func ValidateLiteral(s string) error {
	clean := CleanNumberString(s)
	if clean == "" {
		return fmt.Errorf("invalid numeric literal %s", s)
	}
	if IsValidNumber(clean) {
		return nil
	}
	lower := strings.ToLower(clean)
	switch {
	case strings.HasPrefix(lower, "0x"):
		return fmt.Errorf("invalid hexadecimal literal %s", s)
	case strings.HasPrefix(lower, "0o"):
		return fmt.Errorf("invalid octal literal %s", s)
	case strings.HasPrefix(lower, "0b"):
		return fmt.Errorf("invalid binary literal %s", s)
	case LooksFloatLike(clean):
		return fmt.Errorf("invalid float literal %s", s)
	default:
		return fmt.Errorf("invalid integer literal %s", s)
	}
}

func StringToBigInt(s string) (*big.Int, error) {
	clean := CleanNumberString(s)
	if clean == "" {
		return nil, fmt.Errorf("empty integer literal")
	}
	negative := false
	if clean[0] == '-' {
		negative = true
		clean = clean[1:]
	}

	base := 10
	switch {
	case strings.HasPrefix(clean, "0x") || strings.HasPrefix(clean, "0X"):
		base = 16
		clean = clean[2:]
	case strings.HasPrefix(clean, "0o") || strings.HasPrefix(clean, "0O"):
		base = 8
		clean = clean[2:]
	case strings.HasPrefix(clean, "0b") || strings.HasPrefix(clean, "0B"):
		base = 2
		clean = clean[2:]
	}

	value, ok := new(big.Int).SetString(clean, base)
	if !ok {
		return nil, fmt.Errorf("invalid integer literal: %s", s)
	}
	if negative {
		value.Neg(value)
	}
	return value, nil
}

func StringToFloat(s string) (float64, error) {
	clean := CleanNumberString(s)
	return strconv.ParseFloat(clean, 64)
}

func CanonicalizeIntegerLiteral(s string) (string, error) {
	value, err := StringToBigInt(s)
	if err != nil {
		return "", err
	}
	return value.String(), nil
}

func FitsIntegerLiteral(raw string, bitSize int, signed bool) bool {
	value, err := StringToBigInt(raw)
	if err != nil {
		return false
	}
	if signed {
		max := new(big.Int).Lsh(big.NewInt(1), uint(bitSize-1))
		min := new(big.Int).Neg(max)
		max.Sub(max, big.NewInt(1))
		return value.Cmp(min) >= 0 && value.Cmp(max) <= 0
	}
	if value.Sign() < 0 {
		return false
	}
	max := new(big.Int).Lsh(big.NewInt(1), uint(bitSize))
	max.Sub(max, big.NewInt(1))
	return value.Cmp(max) <= 0
}

func FitsFloatLiteral(raw string, bitSize int) bool {
	value, err := StringToFloat(raw)
	if err != nil {
		return false
	}
	switch bitSize {
	case 32:
		f32 := float32(value)
		return !math.IsInf(float64(f32), 0) && !math.IsNaN(float64(f32))
	case 64:
		return !math.IsInf(value, 0) && !math.IsNaN(value)
	default:
		return false
	}
}

func FitsIntegerLiteralInFloat(raw string, bitSize int) bool {
	value, err := StringToBigInt(raw)
	if err != nil {
		return false
	}
	floatValue := new(big.Float).SetInt(value)
	switch bitSize {
	case 32:
		f32, _ := floatValue.Float32()
		return !math.IsInf(float64(f32), 0) && !math.IsNaN(float64(f32))
	case 64:
		f64, _ := floatValue.Float64()
		return !math.IsInf(f64, 0) && !math.IsNaN(f64)
	default:
		return false
	}
}
