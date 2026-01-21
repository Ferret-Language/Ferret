package typechecker

import (
	"compiler/internal/context_v2"
	"compiler/internal/types"
	"compiler/internal/utils/numeric"

	"fmt"
)

// TypeCompatibility represents the relationship between two types
type TypeCompatibility int

const (
	// Incompatible types cannot be used together (not castable at all)
	Incompatible TypeCompatibility = iota

	// Identical types are exactly the same
	Identical

	// ImplicitCastable means source can be assigned to target (implicit conversion allowed)
	ImplicitCastable

	// ExplicitCastable means conversion requires an explicit cast (but is valid)
	ExplicitCastable
)

func (tc TypeCompatibility) String() string {
	switch tc {
	case Incompatible:
		return "incompatible"
	case Identical:
		return "identical"
	case ImplicitCastable:
		return "implicit castable"
	case ExplicitCastable:
		return "explicit castable"
	default:
		return "unknown"
	}
}

func isImplicitlyCompatible(compatibility TypeCompatibility) bool {
	return compatibility == Identical || compatibility == ImplicitCastable
}

// IsImplicitlyCompatibleTypes reports whether source can be assigned to target without an explicit cast.
func IsImplicitlyCompatibleTypes(ctx *context_v2.CompilerContext, mod *context_v2.Module, source, target types.SemType) bool {
	if source == nil || target == nil {
		return false
	}
	compatibility := checkTypeCompatibilityWithContext(ctx, mod, source, target)
	return isImplicitlyCompatible(compatibility)
}

func isEmptyInterfaceType(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	typ = types.UnwrapType(typ)
	if iface, ok := typ.(*types.InterfaceType); ok {
		return len(iface.Methods) == 0
	}
	return false
}

// methodSignaturesMatch checks if two function types match
// This is used to compare method signatures with interface method signatures
// Note: Methods don't include the receiver in their FuncType - the receiver is separate
// So we can directly compare the FuncTypes
func methodSignaturesMatch(methodType, interfaceType *types.FunctionType) bool {
	// Direct comparison - methods don't have receiver in FuncType
	return methodType.Equals(interfaceType)
}

// implementsInterface checks if a type implements an interface
// Returns true if the type has all required methods with matching signatures
func implementsInterface(ctx *context_v2.CompilerContext, mod *context_v2.Module, implType types.SemType, ifaceType *types.InterfaceType) bool {
	// Empty interface: all types implement it
	if len(ifaceType.Methods) == 0 {
		return true
	}

	// Unwrap NamedType to get underlying type, but keep name for method lookup
	var typeName string
	if named, ok := implType.(*types.NamedType); ok {
		typeName = named.Name
		implType = named.Underlying
	}

	// Only named types can implement interfaces (they have methods)
	if typeName == "" {
		return false
	}

	// Look up the type symbol to get its methods
	typeSym, found := lookupTypeSymbol(ctx, mod, typeName)
	if !found || typeSym.Methods == nil {
		return false
	}

	// Check that all interface methods are implemented
	for _, requiredMethod := range ifaceType.Methods {
		methodInfo, hasMethod := typeSym.Methods[requiredMethod.Name]
		if !hasMethod {
			return false
		}

		// Check method signature matches
		// Methods don't include receiver in FuncType, so direct comparison works
		if !methodSignaturesMatch(methodInfo.FuncType, requiredMethod.FuncType) {
			// Debug: check what's different
			if ctx != nil && ctx.Config.Debug {
				fmt.Printf("      [Interface check: method %s signature mismatch]\n", requiredMethod.Name)
				fmt.Printf("        Method: %s\n", methodInfo.FuncType.String())
				fmt.Printf("        Interface: %s\n", requiredMethod.FuncType.String())
				fmt.Printf("        Method return: %s, Interface return: %s\n",
					methodInfo.FuncType.Return.String(), requiredMethod.FuncType.Return.String())
				fmt.Printf("        Method params: %d, Interface params: %d\n",
					len(methodInfo.FuncType.Params), len(requiredMethod.FuncType.Params))
			}
			return false
		}
	}

	return true
}

// analyzeInterfaceCompatibility checks if sourceType implements targetIface and returns
// detailed information about missing methods if it doesn't.
// Returns:
//   - compatible: true if type implements interface
//   - missingMethods: list of missing/mismatched method names with details
func analyzeInterfaceCompatibility(ctx *context_v2.CompilerContext, mod *context_v2.Module, sourceType types.SemType, targetIface *types.InterfaceType) (compatible bool, missingMethods []string) {
	// Use existing implementsInterface check
	if implementsInterface(ctx, mod, sourceType, targetIface) {
		return true, nil
	}

	// Type doesn't implement interface - build detailed missing method list
	missingMethods = []string{}

	// Get the type symbol to check its methods
	var typeName string
	if namedType, isNamed := sourceType.(*types.NamedType); isNamed {
		typeName = namedType.Name
	}

	typeSym, _ := lookupTypeSymbol(ctx, mod, typeName)

	// Check each required method
	for _, requiredMethod := range targetIface.Methods {
		if typeSym == nil || typeSym.Methods == nil {
			missingMethods = append(missingMethods, requiredMethod.Name)
			continue
		}
		methodInfo, hasMethod := typeSym.Methods[requiredMethod.Name]
		if !hasMethod {
			missingMethods = append(missingMethods, requiredMethod.Name)
		} else if !methodSignaturesMatch(methodInfo.FuncType, requiredMethod.FuncType) {
			if ctx.Config.Debug {
				fmt.Printf("      [Interface compatibility: method %s signature mismatch]\n", requiredMethod.Name)
				fmt.Printf("        Method: %s\n", methodInfo.FuncType.String())
				fmt.Printf("        Interface: %s\n", requiredMethod.FuncType.String())
			}
			missingMethods = append(missingMethods, fmt.Sprintf("%s (signature mismatch)", requiredMethod.Name))
		}
	}

	return false, missingMethods
}

// checkCompositeTypeCompatibility handles composite type compatibility (arrays, maps, optionals)
// Returns the compatibility level between source and target composite types
func checkCompositeTypeCompatibility(source, target types.SemType) TypeCompatibility {
	// Check array compatibility: []T -> []U
	if srcArray, srcIsArray := source.(*types.ArrayType); srcIsArray {
		if tgtArray, tgtIsArray := target.(*types.ArrayType); tgtIsArray {
			// Check lengths match (for fixed-size arrays)
			// Allow dynamic arrays (Length=-1) to be compatible with fixed-size arrays
			// The actual element count validation happens in validateArrayLiteral
			if srcArray.Length >= 0 && tgtArray.Length >= 0 && srcArray.Length != tgtArray.Length {
				return Incompatible
			}
			// Don't allow fixed-size array to be assigned to dynamic array variable
			// (would lose length information)
			if srcArray.Length >= 0 && tgtArray.Length < 0 {
				return Incompatible
			}
			// Recursively check element compatibility
			elemCompat := checkTypeCompatibility(srcArray.Element, tgtArray.Element)
			return elemCompat
		}
		return Incompatible
	}

	// Check map compatibility: map[K1]V1 -> map[K2]V2
	if srcMap, srcIsMap := source.(*types.MapType); srcIsMap {
		if tgtMap, tgtIsMap := target.(*types.MapType); tgtIsMap {
			// Keys must be compatible
			keyCompat := checkTypeCompatibility(srcMap.Key, tgtMap.Key)
			if keyCompat == Incompatible {
				return Incompatible
			}
			// Values must be compatible
			valCompat := checkTypeCompatibility(srcMap.Value, tgtMap.Value)
			if valCompat == Incompatible {
				return Incompatible
			}
			// Return the weaker compatibility level
			if keyCompat == Identical && valCompat == Identical {
				return Identical
			}
			if keyCompat == ExplicitCastable || valCompat == ExplicitCastable {
				return ExplicitCastable
			}
			return ImplicitCastable
		}
		return Incompatible
	}

	// Check optional compatibility: T? -> U?
	if srcOpt, srcIsOpt := source.(*types.OptionalType); srcIsOpt {
		if tgtOpt, tgtIsOpt := target.(*types.OptionalType); tgtIsOpt {
			// Recursively check inner type compatibility
			return checkTypeCompatibility(srcOpt.Inner, tgtOpt.Inner)
		}
		return Incompatible
	}

	// Check result type compatibility: E1!T1 -> E2!T2
	if srcRes, srcIsRes := source.(*types.ResultType); srcIsRes {
		if tgtRes, tgtIsRes := target.(*types.ResultType); tgtIsRes {
			// Both Ok and Err types must be compatible
			okCompat := checkTypeCompatibility(srcRes.Ok, tgtRes.Ok)
			if okCompat == Incompatible {
				return Incompatible
			}
			errCompat := checkTypeCompatibility(srcRes.Err, tgtRes.Err)
			if errCompat == Incompatible {
				return Incompatible
			}
			// Return the weaker compatibility level
			if okCompat == Identical && errCompat == Identical {
				return Identical
			}
			if okCompat == ExplicitCastable || errCompat == ExplicitCastable {
				return ExplicitCastable
			}
			return ImplicitCastable
		}
		return Incompatible
	}

	// Not a composite type or types don't match structure
	return Incompatible
}

// checkTypeCompatibilityWithContext determines if source type can be used where target type is expected
// This version includes context for interface satisfaction checking
func checkTypeCompatibilityWithContext(ctx *context_v2.CompilerContext, mod *context_v2.Module, source, target types.SemType) TypeCompatibility {
	// First try the basic compatibility
	compatibility := checkTypeCompatibility(source, target)
	if compatibility != Incompatible {
		return compatibility
	}

	// Handle interface compatibility
	// For unnamed interfaces, check structural compatibility (does source implement the interface?)
	if targetIface, ok := target.(*types.InterfaceType); ok {
		if implementsInterface(ctx, mod, source, targetIface) {
			return ImplicitCastable
		}
	} else if named, ok := target.(*types.NamedType); ok {
		// For named interface types, use nominal equality
		// Only allow assignment if source is also the same named type
		// This ensures type Int != type Int2 even if both are i32
		// and type any != type any2 even if both are interface{dummy()}
		if iface, ok := named.Underlying.(*types.InterfaceType); ok {
			// Check if source is exactly this named type
			if sourceNamed, ok := source.(*types.NamedType); ok {
				if sourceNamed.Name == named.Name {
					return Identical
				}
				// If source is also a named interface type, they're incompatible (nominal typing)
				if _, sourceIsIface := sourceNamed.Underlying.(*types.InterfaceType); sourceIsIface {
					return Incompatible
				}
				// Source is a named concrete type, check if it implements the interface
				if implementsInterface(ctx, mod, source, iface) {
					return ImplicitCastable
				}
				return Incompatible
			}
			// If source is not a named type but implements the interface,
			// allow implicit cast (e.g., concrete type to named interface)
			if implementsInterface(ctx, mod, source, iface) {
				return ImplicitCastable
			}
		}
	}

	return Incompatible
}

// checkTypeCompatibility determines if source type can be used where target type is expected
func checkTypeCompatibility(source, target types.SemType) TypeCompatibility {
	// Handle unknown types
	if source.Equals(types.TypeUnknown) || target.Equals(types.TypeUnknown) {
		return Incompatible
	}

	// Identical types (check early for performance)
	if source.Equals(target) {
		return Identical
	}

	// Check if target is union (direct or unwrapped) and source matches one of the variants
	// Do this BEFORE other type-specific checks to allow unions to contain any variant type
	// This allows: &Int1 -> union{&Int1, Int1} (exact match)
	// And: i32 -> union{Int, str} where type Int i32 (implicit upcast)
	if unionType, ok := target.(*types.UnionType); ok {
		for _, variant := range unionType.Variants {
			variantCompat := checkTypeCompatibility(source, variant)
			if variantCompat == Identical || variantCompat == ImplicitCastable {
				return ImplicitCastable
			}
		}
		// Source doesn't match any variant
		return Incompatible
	}

	// Check if target unwrapped is union (handles named types wrapping unions)
	tgtUnwrapped := types.UnwrapType(target)
	if unionType, ok := tgtUnwrapped.(*types.UnionType); ok {
		for _, variant := range unionType.Variants {
			variantCompat := checkTypeCompatibility(source, variant)
			if variantCompat == Identical || variantCompat == ImplicitCastable {
				return ImplicitCastable
			}
		}
		return Incompatible
	}

	// Reference type compatibility (both source and target are references)
	if srcRef, ok := types.UnwrapType(source).(*types.ReferenceType); ok {
		if tgtRef, ok := types.UnwrapType(target).(*types.ReferenceType); ok {
			// Allow &mut T to coerce to &T, but never the other way around.
			if tgtRef.Mutable != srcRef.Mutable {
				if srcRef.Mutable && !tgtRef.Mutable {
					innerCompat := checkTypeCompatibility(srcRef.Inner, tgtRef.Inner)
					if innerCompat == Identical || innerCompat == ImplicitCastable {
						return ImplicitCastable
					}
					if isEmptyInterfaceType(tgtRef.Inner) {
						return ImplicitCastable
					}
					return Incompatible
				}
				return Incompatible
			}
			// Check if inner types are identical
			if srcRef.Inner.Equals(tgtRef.Inner) {
				return Identical
			}
			// Check if inner types are compatible (e.g., i32 -> Int1, so &i32 -> &Int1)
			innerCompat := checkTypeCompatibility(srcRef.Inner, tgtRef.Inner)
			if innerCompat == Identical || innerCompat == ImplicitCastable {
				return innerCompat
			}
			// Check if target inner is empty interface
			if isEmptyInterfaceType(tgtRef.Inner) {
				return ImplicitCastable
			}
			return Incompatible
		}
	}

	// Reference type mismatch checks (source is reference, target is not, or vice versa)
	if _, ok := types.UnwrapType(source).(*types.ReferenceType); ok {
		if _, ok := types.UnwrapType(target).(*types.ReferenceType); !ok {
			return Incompatible
		}
	}
	if _, ok := types.UnwrapType(target).(*types.ReferenceType); ok {
		return Incompatible
	}

	// Any type can be assigned to empty interface (any)
	if isEmptyInterfaceType(target) {
		return ImplicitCastable
	}

	// Check composite type compatibility (arrays, maps, optionals, etc.)
	// This handles cases like []T -> []interface{}, map[K]V -> map[interface{}]interface{}, etc.
	if compat := checkCompositeTypeCompatibility(source, target); compat != Incompatible {
		return compat
	}

	// Special handling for none
	// none can be assigned to any optional type (T?) or empty interface (any)
	if source.Equals(types.TypeNone) {
		if _, ok := target.(*types.OptionalType); ok {
			return ImplicitCastable
		}
		// Check if target is an empty interface (interface{})
		if iface, ok := target.(*types.InterfaceType); ok && len(iface.Methods) == 0 {
			return ImplicitCastable
		}
		// Check if target is a named type wrapping an empty interface
		if named, ok := target.(*types.NamedType); ok {
			if iface, ok := named.Underlying.(*types.InterfaceType); ok && len(iface.Methods) == 0 {
				return ImplicitCastable
			}
		}
		return Incompatible
	}

	// Handle untyped literals with union targets
	if types.IsUntyped(source) {
		if unionType, ok := target.(*types.UnionType); ok {
			for _, variant := range unionType.Variants {
				variantUnwrapped := types.UnwrapType(variant)
				if types.IsUntypedInt(source) && types.IsInteger(variantUnwrapped) {
					return ImplicitCastable
				}
				if types.IsUntypedFloat(source) && types.IsFloat(variantUnwrapped) {
					return ImplicitCastable
				}
			}
		} else {
			// Untyped literals can be assigned to compatible primitive types
			if types.IsUntypedInt(source) && types.IsInteger(target) {
				return ImplicitCastable
			}
			if types.IsUntypedFloat(source) && types.IsFloat(target) {
				return ImplicitCastable
			}
		}
		return Incompatible
	}

	// Check numeric conversions
	if types.IsNumericType(source) && types.IsNumericType(target) {
		// Special case: byte and u8 are internally the same size but require explicit cast
		srcName, srcOk := types.GetPrimitiveName(source)
		tgtName, tgtOk := types.GetPrimitiveName(target)
		if srcOk && tgtOk {
			// byte -> u8 or u8 -> byte requires explicit cast
			if (srcName == types.TYPE_BYTE && tgtName == types.TYPE_U8) || (srcName == types.TYPE_U8 && tgtName == types.TYPE_BYTE) {
				return ExplicitCastable
			}
			if types.IsIntegerTypeName(srcName) && types.IsFloatTypeName(tgtName) {
				if isLosslessNumericConversion(source, target) {
					return ImplicitCastable
				}
				return ExplicitCastable
			}
			if types.IsFloatTypeName(srcName) && types.IsIntegerTypeName(tgtName) {
				return ExplicitCastable
			}
		}

		if isLosslessNumericConversion(source, target) {
			return ImplicitCastable
		}
		return ExplicitCastable
	}

	// Check if target is a NamedType and source matches its underlying type
	// This allows: i32 -> Integer where type Integer i32; (implicit)
	// But NOT: Integer -> i32 (reverse) or Integer1 -> Integer2 (named to named) - these require explicit cast
	if targetNamed, ok := target.(*types.NamedType); ok {
		targetUnderlying := targetNamed.Unwrap()
		// Only allow if source is NOT a NamedType (i.e., source is the base type)
		// This ensures we only allow base -> named, not named -> named or named -> base
		if _, sourceIsNamed := source.(*types.NamedType); !sourceIsNamed {
			// Source is a base type, check if it matches the underlying type
			if source.Equals(targetUnderlying) {
				return ImplicitCastable
			}
		}
	}

	// Check for explicit castable cases (named -> base, named -> named with same underlying)
	// These require explicit casting to preserve type safety
	srcUnwrapped := types.UnwrapType(source)

	// If underlying types are compatible, it's explicitly castable (not implicitly)
	if types.IsNumeric(srcUnwrapped) && types.IsNumeric(tgtUnwrapped) {
		// Both are numeric, can be explicitly cast
		return ExplicitCastable
	}

	// If underlying types are the same, can be explicitly cast
	if srcUnwrapped.Equals(tgtUnwrapped) {
		return ExplicitCastable
	}

	// Check struct compatibility (unwrap NamedType to get underlying structure)
	// If target is a NamedType and source matches the underlying structure,
	// allow assignment (e.g., { .x = 1, .y = 2 } can be assigned to Point if Point wraps struct { .x: i32, .y: i32 })
	if srcStruct, srcOk := srcUnwrapped.(*types.StructType); srcOk {
		if tgtStruct, tgtOk := tgtUnwrapped.(*types.StructType); tgtOk {
			// Check if structs are structurally compatible
			if areStructsCompatible(srcStruct, tgtStruct) {
				// If target is a NamedType, this is a structural -> nominal conversion (implicit castable)
				// If both are anonymous or both are named and identical, it's identical
				if source == target || source.Equals(target) {
					return Identical
				}
				return ImplicitCastable
			}
		}
	}

	// Check if target is optional and source matches inner type
	if optType, ok := target.(*types.OptionalType); ok {
		if source.Equals(optType.Inner) {
			return ImplicitCastable
		}
	}

	// No implicit conversion available
	return Incompatible
}

// isLosslessNumericConversion checks if converting from source to target is lossless
func isLosslessNumericConversion(source, target types.SemType) bool {
	// Extract primitive names
	srcName, srcOk := types.GetPrimitiveName(source)
	tgtName, tgtOk := types.GetPrimitiveName(target)
	if !srcOk || !tgtOk {
		return false
	}

	// Widening conversions that don't lose precision
	losslessConversions := map[types.TYPE_NAME][]types.TYPE_NAME{
		types.TYPE_I8:   {types.TYPE_I16, types.TYPE_I32, types.TYPE_I64, types.TYPE_I128, types.TYPE_I256, types.TYPE_F32, types.TYPE_F64, types.TYPE_F128, types.TYPE_F256},
		types.TYPE_I16:  {types.TYPE_I32, types.TYPE_I64, types.TYPE_I128, types.TYPE_I256, types.TYPE_F32, types.TYPE_F64, types.TYPE_F128, types.TYPE_F256},
		types.TYPE_I32:  {types.TYPE_I64, types.TYPE_I128, types.TYPE_I256, types.TYPE_F64, types.TYPE_F128, types.TYPE_F256},
		types.TYPE_I64:  {types.TYPE_I128, types.TYPE_I256, types.TYPE_F64, types.TYPE_F128, types.TYPE_F256},
		types.TYPE_I128: {types.TYPE_I256, types.TYPE_F128, types.TYPE_F256},
		types.TYPE_I256: {types.TYPE_F256},
		types.TYPE_U8:   {types.TYPE_U16, types.TYPE_U32, types.TYPE_U64, types.TYPE_I16, types.TYPE_I32, types.TYPE_I64, types.TYPE_F32, types.TYPE_F64, types.TYPE_F128},
		types.TYPE_U16:  {types.TYPE_U32, types.TYPE_U64, types.TYPE_I32, types.TYPE_I64, types.TYPE_F32, types.TYPE_F64, types.TYPE_F128},
		types.TYPE_U32:  {types.TYPE_U64, types.TYPE_I64, types.TYPE_F64, types.TYPE_F128},
		types.TYPE_U64:  {types.TYPE_F64, types.TYPE_F128},
		types.TYPE_U128: {types.TYPE_F128, types.TYPE_F256},
		types.TYPE_U256: {types.TYPE_F256},
		types.TYPE_F32:  {types.TYPE_F64, types.TYPE_F128, types.TYPE_F256},
		types.TYPE_F64:  {types.TYPE_F128, types.TYPE_F256},
		types.TYPE_F128: {types.TYPE_F256},
	}

	if targets, ok := losslessConversions[srcName]; ok {
		for _, t := range targets {
			if t == tgtName {
				return true
			}
		}
	}

	return false
}

// getConversionError returns a human-readable error message for type incompatibility
// getExplicitCastHint returns a help message suggesting an explicit cast
func getExplicitCastHint(exprText string, targetType types.SemType) string {
	if exprText == "" {
		exprText = "expression"
	}
	return fmt.Sprintf("use an explicit cast: %s as %s", exprText, targetType.String())
}

// getConversionHint returns a hint message based on compatibility result
// Returns empty string if no hint is needed (Identical, ImplicitCastable) or incompatible
func getConversionHint(target types.SemType, compatibility TypeCompatibility, exprText string) string {
	switch compatibility {
	case Identical, ImplicitCastable:
		// No hint needed - these work implicitly
		return ""
	case ExplicitCastable:
		// Suggest explicit cast
		return getExplicitCastHint(exprText, target)
	case Incompatible:
		// No hint - cannot be cast
		return ""
	default:
		return ""
	}
}

func getConversionError(source, target types.SemType, compatibility TypeCompatibility) string {
	switch compatibility {
	case Incompatible:
		return fmt.Sprintf("cannot use type '%s' as type '%s'", source, target)
	case ExplicitCastable:
		// Check if target has smaller bit size than source
		srcName, srcOk := types.GetPrimitiveName(source)
		tgtName, tgtOk := types.GetPrimitiveName(target)

		msg := fmt.Sprintf("cannot implicitly convert '%s' to '%s'", source, target)

		if srcOk && tgtOk {
			srcBits := types.GetNumberBitSize(srcName)
			tgtBits := types.GetNumberBitSize(tgtName)
			if tgtBits < srcBits {
				return fmt.Sprintf("%s (possible data loss)", msg)
			}
		}
		// Same size but different types (e.g., byte vs u8, or named types)
		return msg

	case Identical, ImplicitCastable:
		// These are not errors
		return ""

	default:
		return fmt.Sprintf("type mismatch: '%s' and '%s'", source, target)
	}
}

// countSignificantDigits counts the number of significant digits in a float literal string
// Ignores leading zeros before first non-zero digit and trailing zeros after decimal point
func countSignificantDigits(floatStr string) int {
	// Remove sign
	if len(floatStr) > 0 && (floatStr[0] == '+' || floatStr[0] == '-') {
		floatStr = floatStr[1:]
	}

	// Find the exponent part if exists
	expIndex := -1
	for i, ch := range floatStr {
		if ch == 'e' || ch == 'E' {
			expIndex = i
			break
		}
	}

	// Work with the mantissa only (before exponent)
	mantissa := floatStr
	if expIndex != -1 {
		mantissa = floatStr[:expIndex]
	}

	// Remove trailing zeros after decimal point
	if decimalIndex := -1; true {
		for i, ch := range mantissa {
			if ch == '.' {
				decimalIndex = i
				break
			}
		}
		if decimalIndex != -1 {
			// Has decimal point - trim trailing zeros
			mantissa = trimTrailingZeros(mantissa)
		}
	}

	// Count significant digits (ignoring leading zeros and decimal point)
	count := 0
	leadingZeros := true

	for _, ch := range mantissa {
		if ch == '.' {
			continue
		}
		if ch >= '0' && ch <= '9' {
			if ch != '0' {
				leadingZeros = false
			}
			if !leadingZeros {
				count++
			}
		}
	}

	return count
}

// trimTrailingZeros removes trailing zeros after the decimal point
func trimTrailingZeros(s string) string {
	// Find decimal point
	decimalIndex := -1
	for i, ch := range s {
		if ch == '.' {
			decimalIndex = i
			break
		}
	}
	if decimalIndex == -1 {
		return s // No decimal point
	}

	// Trim from the end
	end := len(s)
	for end > decimalIndex+1 && s[end-1] == '0' {
		end--
	}

	// If we trimmed everything after decimal, keep the decimal point
	return s[:end]
}

// getFloatPrecision returns the approximate decimal precision for float types
func getFloatPrecision(name types.TYPE_NAME) int {
	switch name {
	case types.TYPE_F32:
		return 7 // ~7 decimal digits
	case types.TYPE_F64:
		return 16 // ~15-16 decimal digits
	case types.TYPE_F128:
		return 34 // ~34 decimal digits
	case types.TYPE_F256:
		return 71 // ~71 decimal digits
	default:
		return 0
	}
}

// fitsInType checks if a string literal value fits in a given type.
// Uses NumericValue interface which automatically chooses int64 (fast) or big.Int (precise) representation.
func fitsInType(valueStr string, t types.SemType) bool {
	// Extract primitive type name
	name, ok := types.GetPrimitiveName(t)
	if !ok {
		return false
	}

	// Handle float types - check precision
	if types.IsFloatTypeName(name) {
		digits := countSignificantDigits(valueStr)
		precision := getFloatPrecision(name)
		return digits <= precision
	}

	// Handle integer types - check range
	if types.IsIntegerTypeName(name) {
		numValue, err := numeric.NewNumericValue(valueStr)
		if err != nil {
			return false
		}
		bitSize := types.GetNumberBitSize(name)
		if bitSize == 0 {
			return false
		}
		signed := types.IsSigned(name)
		return numValue.FitsInBitSize(int(bitSize), signed)
	}

	return false
}

// getMinimumTypeOptionsForValue returns the smallest unsigned and signed types that can hold the value.
// Unsigned is only returned for non-negative values.
// Uses NumericValue interface which automatically chooses optimal representation.
func getMinimumTypeOptionsForValue(valueStr string) (types.TYPE_NAME, types.TYPE_NAME) {
	// Use NumericValue interface (automatically chooses int64 fast path or big.Int)
	numValue, err := numeric.NewNumericValue(valueStr)
	if err != nil {
		return types.TYPE_UNKNOWN, types.TYPE_UNKNOWN
	}

	// Check signed types first if negative
	if numValue.IsNegative() {
		signedTypes := []struct {
			name    types.TYPE_NAME
			bitSize int
		}{
			{types.TYPE_I8, 8},
			{types.TYPE_I16, 16},
			{types.TYPE_I32, 32},
			{types.TYPE_I64, 64},
			{types.TYPE_I128, 128},
			{types.TYPE_I256, 256},
		}

		for _, t := range signedTypes {
			if numValue.FitsInBitSize(t.bitSize, true) {
				return types.TYPE_UNKNOWN, t.name
			}
		}
		return types.TYPE_UNKNOWN, types.TYPE_UNKNOWN
	}

	unsignedTypes := []struct {
		name    types.TYPE_NAME
		bitSize int
	}{
		{types.TYPE_U8, 8},
		{types.TYPE_U16, 16},
		{types.TYPE_U32, 32},
		{types.TYPE_U64, 64},
		{types.TYPE_U128, 128},
		{types.TYPE_U256, 256},
	}

	for _, t := range unsignedTypes {
		if numValue.FitsInBitSize(t.bitSize, false) {
			return t.name, getMinimumSignedTypeForValue(numValue)
		}
	}

	return types.TYPE_UNKNOWN, getMinimumSignedTypeForValue(numValue)
}

func getMinimumSignedTypeForValue(numValue numeric.NumericValue) types.TYPE_NAME {
	signedTypes := []struct {
		name    types.TYPE_NAME
		bitSize int
	}{
		{types.TYPE_I8, 8},
		{types.TYPE_I16, 16},
		{types.TYPE_I32, 32},
		{types.TYPE_I64, 64},
		{types.TYPE_I128, 128},
		{types.TYPE_I256, 256},
	}

	for _, t := range signedTypes {
		if numValue.FitsInBitSize(t.bitSize, true) {
			return t.name
		}
	}

	return types.TYPE_UNKNOWN
}

// getMinimumFloatTypeForDigits returns the smallest float type that can hold the given precision
func getMinimumFloatTypeForDigits(digits int) types.TYPE_NAME {
	if digits <= 7 {
		return types.TYPE_F32
	}
	if digits <= 16 {
		return types.TYPE_F64
	}
	if digits <= 34 {
		return types.TYPE_F128
	}
	if digits <= 71 {
		return types.TYPE_F256
	}
	return types.TYPE_UNKNOWN
}
