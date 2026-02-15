package numeric

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// Regex pattern components for number formats
const (
	HexDigits = `[0-9a-fA-F]`
	HexNumber = `0[xX]` + HexDigits + `(?:` + HexDigits + `|_` + HexDigits + `)*`

	OctDigits = `[0-7]`
	OctNumber = `0[oO]` + OctDigits + `(?:` + OctDigits + `|_` + OctDigits + `)*`

	BinDigits = `[01]`
	BinNumber = `0[bB]` + BinDigits + `(?:` + BinDigits + `|_` + BinDigits + `)*`

	DecDigits = `[0-9]`
	DecNumber = DecDigits + `(?:` + DecDigits + `|_` + DecDigits + `)*`

	FloatFrac   = `\.` + DecDigits + `(?:` + DecDigits + `|_` + DecDigits + `)*`
	FloatExp    = `[eE][+-]?` + DecDigits + `(?:` + DecDigits + `|_` + DecDigits + `)*`
	FloatNumber = DecNumber + `(?:` + FloatFrac + `)?(?:` + FloatExp + `)?`
	ImagNumber  = FloatNumber + `i\b`

	// Complete number pattern for tokenizing (does not include sign)
	NumberPattern = `(?:` + HexNumber + `|` + OctNumber + `|` + BinNumber + `|` + ImagNumber + `|` + FloatNumber + `)`
)

var (
	// Regular expressions for different number formats (validation with anchors)
	// Allow underscores between digits for readability
	decimalRegex = regexp.MustCompile(`^-?` + DecNumber + `$`)
	hexRegex     = regexp.MustCompile(`^` + HexNumber + `$`)
	octalRegex   = regexp.MustCompile(`^` + OctNumber + `$`)
	binaryRegex  = regexp.MustCompile(`^` + BinNumber + `$`)
	// Float patterns need to handle: 1.23, .123, 1., with optional underscores
	floatRegex = regexp.MustCompile(`^-?` + DecNumber + `\.` + DecDigits + `(?:` + DecDigits + `|_` + DecDigits + `)*$`)
	// Scientific notation: 1e10, 1.2e-10, .2e+10, etc.
	scientificRegex = regexp.MustCompile(`^-?` + DecNumber + `(?:\.` + DecDigits + `(?:` + DecDigits + `|_` + DecDigits + `)*)?` + FloatExp + `$`)
)

// IsFloat checks if the string represents any valid float format
// (decimal point or scientific notation)
func IsFloat(s string) bool {
	return floatRegex.MatchString(s) || scientificRegex.MatchString(s)
}

// IsDecimal checks if the string represents a decimal
func IsDecimal(s string) bool {
	return decimalRegex.MatchString(s)
}

// IsHexadecimal checks if the string represents a hexadecimal integer
func IsHexadecimal(s string) bool {
	return hexRegex.MatchString(s)
}

// IsOctal checks if the string represents an octal integer
func IsOctal(s string) bool {
	return octalRegex.MatchString(s)
}

// IsBinary checks if the string represents a binary integer
func IsBinary(s string) bool {
	return binaryRegex.MatchString(s)
}

func StringToInteger(s string) (int64, error) {
	// Remove any underscores used for readability
	s = strings.ReplaceAll(s, "_", "")
	// Handle different bases
	if IsHexadecimal(s) {
		return strconv.ParseInt(s[2:], 16, 64)
	}
	if IsOctal(s) {
		return strconv.ParseInt(s[2:], 8, 64)
	}
	if IsBinary(s) {
		return strconv.ParseInt(s[2:], 2, 64)
	}
	// Default to decimal
	return strconv.ParseInt(s, 10, 64)
}

// StringToFloat parses a string into a float value, handling decimal and scientific notation
func StringToFloat(s string) (float64, error) {
	// Remove any underscores used for readability
	s = strings.ReplaceAll(s, "_", "")
	return strconv.ParseFloat(s, 64)
}

// StringToBigInt parses a string into a big.Int, handling hex, octal, binary, and decimal formats
// This is used for validating large integer literals (128-bit and 256-bit)
func StringToBigInt(s string) (*big.Int, error) {
	// Remove any underscores used for readability
	s = strings.ReplaceAll(s, "_", "")

	// Determine base and trim prefix
	base := 10
	trimmed := s

	if IsHexadecimal(s) {
		base = 16
		trimmed = s[2:] // Remove "0x" or "0X"
	} else if IsOctal(s) {
		base = 8
		trimmed = s[2:] // Remove "0o" or "0O"
	} else if IsBinary(s) {
		base = 2
		trimmed = s[2:] // Remove "0b" or "0B"
	}

	// Parse as big.Int
	result := new(big.Int)
	_, ok := result.SetString(trimmed, base)
	if !ok {
		return nil, fmt.Errorf("invalid integer literal: %s", s)
	}

	return result, nil
}

// FitsInBitSize checks if a big.Int value fits in the given bit size (signed or unsigned)
// Returns true if the value is within the valid range for the specified bit size
func FitsInBitSize(value *big.Int, bitSize int, signed bool) bool {
	if signed {
		// Signed range: -2^(bitSize-1) to 2^(bitSize-1) - 1
		min := new(big.Int).Lsh(big.NewInt(-1), uint(bitSize-1))
		max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(bitSize-1)), big.NewInt(1))
		return value.Cmp(min) >= 0 && value.Cmp(max) <= 0
	} else {
		// Unsigned range: 0 to 2^bitSize - 1
		if value.Sign() < 0 {
			return false
		}
		max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(bitSize)), big.NewInt(1))
		return value.Cmp(max) <= 0
	}
}

// numeric to ordinal: 1 -> 1st, 2 -> 2nd, 3 -> 3rd, 4 -> 4th, etc.
func NumericToOrdinal(n int) string {
	if n <= 0 {
		return ""
	}

	// Handle special cases for 11, 12, 13
	switch n % 100 {
	case 11, 12, 13:
		return fmt.Sprintf("%dth", n)
	}

	switch n % 10 {
	case 1:
		return fmt.Sprintf("%dst", n)
	case 2:
		return fmt.Sprintf("%dnd", n)
	case 3:
		return fmt.Sprintf("%drd", n)
	default:
		return fmt.Sprintf("%dth", n)
	}
}
