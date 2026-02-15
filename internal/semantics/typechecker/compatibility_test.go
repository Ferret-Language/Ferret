package typechecker

import (
	"compiler/internal/context_v2"
	"compiler/internal/semantics/symbols"
	"compiler/internal/semantics/table"
	"compiler/internal/types"
	"testing"
)

func TestCountSignificantDigits(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"3.14", 3},
		{"3.14159", 6},
		{"0.5", 1},
		{"0.005", 1},
		{"0.0050", 1}, // Trailing zero ignored: 0.005
		{"123.456", 6},
		{"0.123456", 6},
		{"+3.14", 3},
		{"-3.14", 3},
		{"1e10", 1},
		{"1.23e10", 3},
		{"0.0", 0},
		{"1.0", 1},     // Trailing zeros after decimal are ignored
		{"10.0", 2},    // Trailing zeros after decimal are ignored
		{"3.140", 3},   // Trailing zero ignored
		{"3.14000", 3}, // Trailing zeros ignored
		{"100.500", 4}, // Trailing zeros ignored: 100.5 = 4 digits (1,0,0,5)
		{"3.141592653589793238462643383279502884197169399375105820974944592307816406286208", 79},
		{"3.1415926535897932384626433832795028841971693993751058209749445", 62},
		{"3.14159265358979323846", 21},
	}

	for _, tt := range tests {
		result := countSignificantDigits(tt.input)
		if result != tt.expected {
			t.Errorf("countSignificantDigits(%q) = %d, want %d", tt.input, result, tt.expected)
		}
	}
}

func TestGetFloatPrecision(t *testing.T) {
	tests := []struct {
		typeName types.TYPE_NAME
		expected int
	}{
		{types.TYPE_F32, 7},
		{types.TYPE_F64, 16},
		{types.TYPE_F128, 34},
		{types.TYPE_F256, 71},
		{types.TYPE_I32, 0}, // Non-float type
		{types.TYPE_STRING, 0},
	}

	for _, tt := range tests {
		result := getFloatPrecision(tt.typeName)
		if result != tt.expected {
			t.Errorf("getFloatPrecision(%s) = %d, want %d", tt.typeName, result, tt.expected)
		}
	}
}

func TestFitsInTypeFloats(t *testing.T) {
	tests := []struct {
		value    string
		typ      types.SemType
		expected bool
	}{
		// f32: ~7 decimal digits
		{"3.14", types.TypeF32, true},
		{"3.141592", types.TypeF32, true},   // 7 digits exactly
		{"3.1415926", types.TypeF32, false}, // 8 digits
		{"0.5", types.TypeF32, true},

		// f64: ~16 decimal digits
		{"3.14159265358979", types.TypeF64, true},    // 15 digits
		{"3.141592653589793", types.TypeF64, true},   // 16 digits exactly
		{"3.1415926535897932", types.TypeF64, false}, // 17 digits

		// f128: ~34 decimal digits
		{"3.14159265358979323846", types.TypeF128, true},                 // 21 digits
		{"3.1415926535897932384626433832795", types.TypeF128, true},      // 34 digits exactly
		{"3.14159265358979323846264338327950288", types.TypeF128, false}, // 36 digits

		// f256: ~71 decimal digits
		{"3.1415926535897932384626433832795028841971693993751058209749445923078164", types.TypeF256, true},          // 71 digits
		{"3.141592653589793238462643383279502884197169399375105820974944592307816406286208", types.TypeF256, false}, // 79 digits
	}

	for _, tt := range tests {
		result := fitsInType(tt.value, tt.typ)
		if result != tt.expected {
			digits := countSignificantDigits(tt.value)
			t.Errorf("fitsInType(%q, %s) = %v, want %v (has %d digits)", tt.value, tt.typ, result, tt.expected, digits)
		}
	}
}

func TestFitsInTypeIntegers(t *testing.T) {
	tests := []struct {
		value    string
		typ      types.SemType
		expected bool
	}{
		// i8: -128 to 127
		{"127", types.TypeI8, true},
		{"128", types.TypeI8, false},
		{"-128", types.TypeI8, true},
		{"-129", types.TypeI8, false},

		// u8: 0 to 255
		{"255", types.TypeU8, true},
		{"256", types.TypeU8, false},
		{"-1", types.TypeU8, false},

		// i32: -2^31 to 2^31-1
		{"2147483647", types.TypeI32, true},
		{"2147483648", types.TypeI32, false},
		{"-2147483648", types.TypeI32, true},
		{"-2147483649", types.TypeI32, false},

		// i64
		{"9223372036854775807", types.TypeI64, true},
		{"9223372036854775808", types.TypeI64, false},

		// i128 - large values
		{"170141183460469231731687303715884105727", types.TypeI128, true},
		{"170141183460469231731687303715884105728", types.TypeI128, false},

		// i256 - very large values
		{"57896044618658097711785492504343953926634992332820282019728792003956564819967", types.TypeI256, true},
		{"57896044618658097711785492504343953926634992332820282019728792003956564819968", types.TypeI256, false},
	}

	for _, tt := range tests {
		result := fitsInType(tt.value, tt.typ)
		if result != tt.expected {
			t.Errorf("fitsInType(%q, %s) = %v, want %v", tt.value, tt.typ, result, tt.expected)
		}
	}
}

func TestGetMinimumFloatTypeForDigits(t *testing.T) {
	tests := []struct {
		digits   int
		expected types.TYPE_NAME
	}{
		{1, types.TYPE_F32},
		{7, types.TYPE_F32},
		{8, types.TYPE_F64},
		{16, types.TYPE_F64},
		{17, types.TYPE_F128},
		{34, types.TYPE_F128},
		{35, types.TYPE_F256},
		{71, types.TYPE_F256},
		{72, types.TYPE_UNKNOWN},
		{100, types.TYPE_UNKNOWN},
	}

	for _, tt := range tests {
		result := getMinimumFloatTypeForDigits(tt.digits)
		if result != tt.expected {
			t.Errorf("getMinimumFloatTypeForDigits(%d) = %q, want %q", tt.digits, result, tt.expected)
		}
	}
}

func TestCheckTypeCompatibility(t *testing.T) {
	anyIface := types.NewInterface(nil)
	tests := []struct {
		source   types.SemType
		target   types.SemType
		expected TypeCompatibility
	}{
		// Identical types
		{types.TypeI32, types.TypeI32, Identical},
		{types.TypeF64, types.TypeF64, Identical},
		{types.TypeString, types.TypeString, Identical},

		// Untyped int can only be assigned to integer types (not float)
		{types.TypeUntypedInt, types.TypeI32, ImplicitCastable},
		{types.TypeUntypedInt, types.TypeI64, ImplicitCastable},
		{types.TypeUntypedInt, types.TypeF64, Incompatible}, // Requires explicit cast
		{types.TypeUntypedInt, types.TypeString, Incompatible},

		// Untyped float can only be assigned to float types (not int)
		{types.TypeUntypedFloat, types.TypeF32, ImplicitCastable},
		{types.TypeUntypedFloat, types.TypeF64, ImplicitCastable},
		{types.TypeUntypedFloat, types.TypeF128, ImplicitCastable},
		{types.TypeUntypedFloat, types.TypeF256, ImplicitCastable},
		{types.TypeUntypedFloat, types.TypeI32, Incompatible}, // Requires explicit cast

		// ImplicitCastable conversions - integers (widening)
		{types.TypeI8, types.TypeI16, ImplicitCastable},
		{types.TypeI8, types.TypeI32, ImplicitCastable},
		{types.TypeI8, types.TypeI64, ImplicitCastable},
		{types.TypeI16, types.TypeI32, ImplicitCastable},
		{types.TypeU8, types.TypeU16, ImplicitCastable},

		// ImplicitCastable conversions - floats (widening)
		{types.TypeF32, types.TypeF64, ImplicitCastable},
		{types.TypeF32, types.TypeF128, ImplicitCastable},
		{types.TypeF32, types.TypeF256, ImplicitCastable},
		{types.TypeF64, types.TypeF128, ImplicitCastable},
		{types.TypeF64, types.TypeF256, ImplicitCastable},
		{types.TypeF128, types.TypeF256, ImplicitCastable},

		// ImplicitCastable conversions - int to float (safe precision)
		{types.TypeI8, types.TypeF32, ImplicitCastable},
		{types.TypeI32, types.TypeF64, ImplicitCastable},
		{types.TypeI32, types.TypeF128, ImplicitCastable},
		{types.TypeI64, types.TypeF128, ImplicitCastable},
		{types.TypeI128, types.TypeF256, ImplicitCastable},

		// Lossy conversions - narrowing
		{types.TypeI32, types.TypeI16, ExplicitCastable},
		{types.TypeI64, types.TypeI32, ExplicitCastable},
		{types.TypeF64, types.TypeF32, ExplicitCastable},
		{types.TypeF128, types.TypeF64, ExplicitCastable},
		{types.TypeI64, types.TypeF32, ExplicitCastable}, // Loses precision

		// Lossy conversions - float to int (always lossy)
		{types.TypeF32, types.TypeI32, ExplicitCastable},
		{types.TypeF64, types.TypeI64, ExplicitCastable},

		// Empty interface reference compatibility
		{types.NewReference(types.TypeI32), types.NewReference(anyIface), ImplicitCastable},
		{types.NewMutableReference(types.TypeI32), types.NewMutableReference(anyIface), ImplicitCastable},
		{types.NewReference(types.TypeI32), types.NewMutableReference(anyIface), Incompatible},
		{types.NewMutableReference(types.TypeI32), types.NewReference(anyIface), ImplicitCastable},

		// Incompatible
		{types.TypeI32, types.TypeString, Incompatible},
		{types.TypeF64, types.TypeBool, Incompatible},
		{types.TypeString, types.TypeI32, Incompatible},
	}

	for _, tt := range tests {
		result := checkTypeCompatibility(tt.source, tt.target)
		if result != tt.expected {
			t.Errorf("checkTypeCompatibility(%s, %s) = %s, want %s",
				tt.source, tt.target, result, tt.expected)
		}
	}
}

func TestCheckTypeCompatibilityWithContextReferenceToInterface(t *testing.T) {
	ctx := context_v2.New(&context_v2.Config{Extension: ".fer"}, false)
	modScope := table.NewSymbolTable(ctx.Universe)
	mod := &context_v2.Module{
		ImportPath:     "test",
		FilePath:       "/test/main.fer",
		Type:           context_v2.ModuleLocal,
		ModuleScope:    modScope,
		CurrentScope:   modScope,
		Imports:        make(map[string]*context_v2.Import),
		ImportAliasMap: make(map[string]string),
	}
	ctx.AddModule(mod.ImportPath, mod)

	writeFn := types.NewFunction([]types.ParamType{
		{Name: "buf", Type: types.NewArray(types.TypeByte, -1)},
	}, types.NewResult(types.TypeI32, types.TypeString))

	writerIface := types.NewInterface([]types.InterfaceMethod{
		{Name: "Write", FuncType: writeFn},
	})
	writerNamed := types.NewNamed("Writer", writerIface)

	streamWriter := types.NewNamed("StreamWriter", types.NewStruct("StreamWriter", nil))
	streamWriterSym := &symbols.Symbol{
		Name: "StreamWriter",
		Kind: symbols.SymbolType,
		Type: streamWriter,
		Methods: map[string]*symbols.MethodInfo{
			"Write": {
				Name:     "Write",
				FuncType: writeFn,
				Receiver: types.NewMutableReference(streamWriter),
			},
		},
	}
	if err := mod.ModuleScope.Declare("StreamWriter", streamWriterSym); err != nil {
		t.Fatalf("failed to declare StreamWriter: %v", err)
	}

	tests := []struct {
		name     string
		source   types.SemType
		target   types.SemType
		expected TypeCompatibility
	}{
		{
			name:     "&mut concrete -> &mut named interface",
			source:   types.NewMutableReference(streamWriter),
			target:   types.NewMutableReference(writerNamed),
			expected: ImplicitCastable,
		},
		{
			name:     "&mut concrete -> &named interface",
			source:   types.NewMutableReference(streamWriter),
			target:   types.NewReference(writerNamed),
			expected: ImplicitCastable,
		},
		{
			name:     "&concrete -> &mut named interface",
			source:   types.NewReference(streamWriter),
			target:   types.NewMutableReference(writerNamed),
			expected: Incompatible,
		},
	}

	for _, tt := range tests {
		got := checkTypeCompatibilityWithContext(ctx, mod, tt.source, tt.target)
		if got != tt.expected {
			t.Errorf("%s: checkTypeCompatibilityWithContext(%s, %s) = %s, want %s",
				tt.name, tt.source, tt.target, got, tt.expected)
		}
	}

	t.Run("imported module type also satisfies &mut interface", func(t *testing.T) {
		ctx2 := context_v2.New(&context_v2.Config{Extension: ".fer"}, false)

		ioScope := table.NewSymbolTable(ctx2.Universe)
		ioMod := &context_v2.Module{
			ImportPath:     "std/io",
			FilePath:       "/std/io.fer",
			Type:           context_v2.ModuleBuiltin,
			ModuleScope:    ioScope,
			CurrentScope:   ioScope,
			Imports:        make(map[string]*context_v2.Import),
			ImportAliasMap: make(map[string]string),
		}
		ctx2.AddModule(ioMod.ImportPath, ioMod)

		mainScope := table.NewSymbolTable(ctx2.Universe)
		mainMod := &context_v2.Module{
			ImportPath:   "app/main",
			FilePath:     "/app/main.fer",
			Type:         context_v2.ModuleLocal,
			ModuleScope:  mainScope,
			CurrentScope: mainScope,
			Imports: map[string]*context_v2.Import{
				"std/io": {Path: "std/io", Alias: "io"},
			},
			ImportAliasMap: map[string]string{
				"io": "std/io",
			},
		}
		ctx2.AddModule(mainMod.ImportPath, mainMod)

		ioWriterIface := types.NewInterface([]types.InterfaceMethod{
			{Name: "Write", FuncType: writeFn},
		})
		ioWriterNamed := types.NewNamed("Writer", ioWriterIface)
		ioStreamWriter := types.NewNamed("StreamWriter", types.NewStruct("StreamWriter", nil))

		if err := ioMod.ModuleScope.Declare("Writer", &symbols.Symbol{
			Name:    "Writer",
			Kind:    symbols.SymbolType,
			Type:    ioWriterNamed,
			Methods: map[string]*symbols.MethodInfo{},
		}); err != nil {
			t.Fatalf("failed to declare Writer: %v", err)
		}
		if err := ioMod.ModuleScope.Declare("StreamWriter", &symbols.Symbol{
			Name: "StreamWriter",
			Kind: symbols.SymbolType,
			Type: ioStreamWriter,
			Methods: map[string]*symbols.MethodInfo{
				"Write": {
					Name:     "Write",
					FuncType: writeFn,
					Receiver: types.NewMutableReference(ioStreamWriter),
				},
			},
		}); err != nil {
			t.Fatalf("failed to declare StreamWriter: %v", err)
		}

		got := checkTypeCompatibilityWithContext(
			ctx2,
			mainMod,
			types.NewMutableReference(ioStreamWriter),
			types.NewMutableReference(ioWriterNamed),
		)
		if got != ImplicitCastable {
			t.Fatalf("imported ref/interface compatibility = %s, want %s", got, ImplicitCastable)
		}
	})
}

func TestIsLosslessNumericConversion(t *testing.T) {
	tests := []struct {
		source   types.SemType
		target   types.SemType
		expected bool
	}{
		// Integer widening
		{types.TypeI8, types.TypeI16, true},
		{types.TypeI8, types.TypeI32, true},
		{types.TypeI8, types.TypeI64, true},
		{types.TypeI8, types.TypeI128, true},
		{types.TypeI8, types.TypeI256, true},
		{types.TypeI16, types.TypeI32, true},
		{types.TypeI32, types.TypeI64, true},
		{types.TypeI64, types.TypeI128, true},
		{types.TypeI128, types.TypeI256, true},

		// Unsigned widening
		{types.TypeU8, types.TypeU16, true},
		{types.TypeU16, types.TypeU32, true},
		{types.TypeU32, types.TypeU64, true},

		// Float widening
		{types.TypeF32, types.TypeF64, true},
		{types.TypeF64, types.TypeF128, true},
		{types.TypeF128, types.TypeF256, true},
		{types.TypeF32, types.TypeF128, true},
		{types.TypeF32, types.TypeF256, true},
		{types.TypeF64, types.TypeF256, true},

		// Integer to float (where lossless)
		{types.TypeI8, types.TypeF32, true},
		{types.TypeI8, types.TypeF64, true},
		{types.TypeI16, types.TypeF64, true},
		{types.TypeI32, types.TypeF128, true},
		{types.TypeI64, types.TypeF128, true},
		{types.TypeI128, types.TypeF256, true},
		{types.TypeU8, types.TypeF32, true},

		// NOT lossless (narrowing)
		{types.TypeI32, types.TypeI16, false},
		{types.TypeI64, types.TypeI32, false},
		{types.TypeF64, types.TypeF32, false},
		{types.TypeF128, types.TypeF64, false},
		{types.TypeF256, types.TypeF128, false},

		// Float to int (never lossless)
		{types.TypeF32, types.TypeI32, false},
		{types.TypeF64, types.TypeI64, false},
	}

	for _, tt := range tests {
		result := isLosslessNumericConversion(tt.source, tt.target)
		if result != tt.expected {
			t.Errorf("isLosslessNumericConversion(%s, %s) = %v, want %v",
				tt.source, tt.target, result, tt.expected)
		}
	}
}

func TestGetConversionError(t *testing.T) {
	tests := []struct {
		source        types.SemType
		target        types.SemType
		compatibility TypeCompatibility
		expectError   bool
	}{
		{types.TypeI32, types.TypeString, Incompatible, true},
		{types.TypeI64, types.TypeI32, ExplicitCastable, true},
		{types.TypeI32, types.TypeI32, Identical, false},
		{types.TypeUntypedInt, types.TypeI32, ImplicitCastable, false},
		{types.TypeI32, types.TypeI64, ImplicitCastable, false},
	}

	for _, tt := range tests {
		result := getConversionError(tt.source, tt.target, tt.compatibility)
		hasError := result != ""
		if hasError != tt.expectError {
			t.Errorf("getConversionError(%s, %s, %s) returned error=%v, want error=%v (msg: %q)",
				tt.source, tt.target, tt.compatibility, hasError, tt.expectError, result)
		}
	}
}
