package numeric

import "regexp"

const (
	HexDigits = `[0-9a-fA-F]`
	HexNumber = `0[xX]` + HexDigits + `(?:` + HexDigits + `|_` + HexDigits + `)*`
	HexToken  = `0[xX][0-9A-Za-z](?:[0-9A-Za-z]|_[0-9A-Za-z])*`

	OctDigits = `[0-7]`
	OctNumber = `0[oO]` + OctDigits + `(?:` + OctDigits + `|_` + OctDigits + `)*`
	OctToken  = `0[oO][0-9A-Za-z](?:[0-9A-Za-z]|_[0-9A-Za-z])*`

	BinDigits = `[01]`
	BinNumber = `0[bB]` + BinDigits + `(?:` + BinDigits + `|_` + BinDigits + `)*`
	BinToken  = `0[bB][0-9A-Za-z](?:[0-9A-Za-z]|_[0-9A-Za-z])*`

	DecDigits = `[0-9]`
	DecNumber = DecDigits + `(?:` + DecDigits + `|_` + DecDigits + `)*`

	FloatFrac   = `\.` + DecDigits + `(?:` + DecDigits + `|_` + DecDigits + `)*`
	FloatExp    = `[eE][+-]?` + DecDigits + `(?:` + DecDigits + `|_` + DecDigits + `)*`
	FloatNumber = DecNumber + `(?:` + FloatFrac + `)?(?:` + FloatExp + `)?`
	ImagNumber  = FloatNumber + `i\b`

	NumberPattern      = `(?:` + HexNumber + `|` + OctNumber + `|` + BinNumber + `|` + ImagNumber + `|` + FloatNumber + `)`
	NumberTokenPattern = `(?:` + HexToken + `|` + OctToken + `|` + BinToken + `|` + ImagNumber + `|` + FloatNumber + `)`
)

var (
	decimalRegex    = regexp.MustCompile(`^-?` + DecNumber + `$`)
	hexRegex        = regexp.MustCompile(`^` + HexNumber + `$`)
	octalRegex      = regexp.MustCompile(`^` + OctNumber + `$`)
	binaryRegex     = regexp.MustCompile(`^` + BinNumber + `$`)
	floatRegex      = regexp.MustCompile(`^-?` + DecNumber + `\.` + DecDigits + `(?:` + DecDigits + `|_` + DecDigits + `)*$`)
	scientificRegex = regexp.MustCompile(`^-?` + DecNumber + `(?:\.` + DecDigits + `(?:` + DecDigits + `|_` + DecDigits + `)*)?` + FloatExp + `$`)
	numberRegex     = regexp.MustCompile(`^(?:` + HexNumber + `|` + OctNumber + `|` + BinNumber + `|` + ImagNumber + `|` + FloatNumber + `)$`)
)

func IsDecimal(s string) bool {
	return decimalRegex.MatchString(s)
}

func IsHexadecimal(s string) bool {
	return hexRegex.MatchString(s)
}

func IsOctal(s string) bool {
	return octalRegex.MatchString(s)
}

func IsBinary(s string) bool {
	return binaryRegex.MatchString(s)
}

func IsFloat(s string) bool {
	return floatRegex.MatchString(s) || scientificRegex.MatchString(s)
}

func IsValidNumber(s string) bool {
	return numberRegex.MatchString(s)
}
