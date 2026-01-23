package typechecker

import (
	"compiler/internal/context_v2"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/semantics/narrowing"
	"compiler/internal/semantics/symbols"
	"compiler/internal/semantics/table"
	"compiler/internal/tokens"
	"compiler/internal/types"

	"fmt"
	"strings"
)

// inferLiteralType is the SINGLE source of truth for all literal type inference.
// It handles all rules: default size, contextualization, and fitness checking.
//
// Rules:
//   - If expected type is provided and value fits: use expected type
//   - If expected type is provided and value doesn't fit: return expected type (error reported separately)
//   - If no expected type: use DEFAULT_INT_TYPE or DEFAULT_FLOAT_TYPE (no promotion)
//   - Non-numeric literals (string, bool, byte) return concrete types immediately
func inferLiteralType(lit *ast.BasicLit, expected types.SemType) types.SemType {
	switch lit.Kind {
	case ast.INT:
		// If expected type is provided, try to use it (contextualization)
		// Unwrap NamedType to check underlying type (e.g., type Integer i32)
		expectedUnwrapped := types.UnwrapType(expected)
		if !expected.Equals(types.TypeUnknown) && types.IsInteger(expectedUnwrapped) {
			if _, ok := types.GetPrimitiveName(expectedUnwrapped); ok {
				if fitsInType(lit.Value, expectedUnwrapped) {
					// Return the expected type (which may be a NamedType) to preserve the type name
					return expected
				}
			}
		}

		// No expected type or incompatible: use default without promotion.
		defaultName := types.DEFAULT_INT_TYPE
		return types.FromTypeName(defaultName)

	case ast.FLOAT:
		// If expected type is provided, try to use it (contextualization)
		// Unwrap NamedType to check underlying type (e.g., type Float f64)
		expectedUnwrapped := types.UnwrapType(expected)
		if !expected.Equals(types.TypeUnknown) && types.IsFloat(expectedUnwrapped) {
			if _, ok := types.GetPrimitiveName(expectedUnwrapped); ok {
				if fitsInType(lit.Value, expectedUnwrapped) {
					// Return the expected type (which may be a NamedType) to preserve the type name
					return expected
				}
			}
		}

		// No expected type or incompatible: use default without promotion.
		return types.FromTypeName(types.DEFAULT_FLOAT_TYPE)

	case ast.STRING:
		return types.TypeString
	case ast.BYTE:
		return types.TypeByte
	case ast.CHAR:
		return types.TypeChar
	case ast.BOOL:
		return types.TypeBool
	default:
		return types.TypeUnknown
	}
}

// getTypeRange returns a human-readable range for integer types
func getTypeRange(t types.SemType) string {
	// Extract primitive type name
	name, ok := types.GetPrimitiveName(t)
	if !ok {
		return "unknown range"
	}

	switch name {
	case types.TYPE_I8:
		return "-128 to 127"
	case types.TYPE_I16:
		return "-32,768 to 32,767"
	case types.TYPE_I32:
		return "-2,147,483,648 to 2,147,483,647"
	case types.TYPE_I64:
		return "-9,223,372,036,854,775,808 to 9,223,372,036,854,775,807"
	case types.TYPE_I128:
		return "-170,141,183,460,469,231,731,687,303,715,884,105,728 to 170,141,183,460,469,231,731,687,303,715,884,105,727"
	case types.TYPE_I256:
		return "-2^255 to 2^255 - 1 (256-bit signed)"
	case types.TYPE_U8:
		return "0 to 255"
	case types.TYPE_U16:
		return "0 to 65,535"
	case types.TYPE_U32:
		return "0 to 4,294,967,295"
	case types.TYPE_U64:
		return "0 to 18,446,744,073,709,551,615"
	case types.TYPE_U128:
		return "0 to 340,282,366,920,938,463,463,374,607,431,768,211,455"
	case types.TYPE_U256:
		return "0 to 2^256 - 1 (256-bit unsigned)"
	default:
		return "unknown range"
	}
}

// inferExprType determines the type of an expression based on its structure and value.
// InferExprType infers the type of an expression using semantic analysis.
// This is pure type inference without any compatibility checking.
// Returns TYPE_UNTYPED for literals that haven't been contextualized yet.
// Exported for use by codegen and other packages that need type inference.
func InferExprType(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr ast.Expression) types.SemType {
	return inferExprType(ctx, mod, expr)
}

// ResolvedExprType returns the most accurate type for an expression by using any
// type information stored on the module side table (populated during semantic
// analysis) before falling back to inference.
func ResolvedExprType(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr ast.Expression) types.SemType {
	if expr == nil {
		return types.TypeUnknown
	}

	if mod != nil {
		if typ, ok := mod.ExprType(expr); ok && typ != nil && !typ.Equals(types.TypeUnknown) {
			return typ
		}
	}

	return InferExprType(ctx, mod, expr)
}

// inferExprType is the internal implementation of InferExprType.
// This is pure type inference without any compatibility checking.
// Returns TYPE_UNTYPED for literals that haven't been contextualized yet.
func inferExprType(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr ast.Expression) types.SemType {
	if expr == nil {
		return types.TypeUnknown
	}

	if mod != nil && mod.NarrowedExprTypes != nil {
		if key, ok := narrowing.ExprKey(expr); ok {
			if narrowed, ok := mod.NarrowedExprTypes[key]; ok && narrowed != nil {
				return narrowed
			}
		}
	}

	switch e := expr.(type) {
	case *ast.BasicLit:
		// For pure inference (no context), default numeric literals immediately.
		return inferLiteralType(e, types.TypeUnknown)

	case *ast.IdentifierExpr:
		return inferIdentifierType(ctx, mod, e)

	case *ast.BinaryExpr:
		return inferBinaryExprType(ctx, mod, e)

	case *ast.UnaryExpr:
		return inferUnaryExprType(ctx, mod, e)

	case *ast.DerefExpr:
		return inferDerefExprType(ctx, mod, e)

	case *ast.CallExpr:
		return inferCallExprType(ctx, mod, e)

	case *ast.IndexExpr:
		return inferIndexExprType(ctx, mod, e)

	case *ast.SelectorExpr:
		return inferSelectorExprType(ctx, mod, e)

	case *ast.ScopeResolutionExpr:
		return inferScopeResolutionExprType(ctx, mod, e)

	case *ast.CastExpr:
		return inferCastExprType(ctx, mod, e)

	case *ast.ParenExpr:
		return inferExprType(ctx, mod, e.X)

	case *ast.CompositeLit:
		return inferCompositeLitType(ctx, mod, e)

	case *ast.FuncLit:
		return inferFuncLitType(ctx, mod, e)

	case *ast.RangeExpr:
		return inferRangeExprType(ctx, mod, e)

	case *ast.CoalescingExpr:
		return inferCoalescingExprType(ctx, mod, e)

	case *ast.PrefixExpr:
		// Prefix operators (++, --) return the type of the operand
		opType := inferExprType(ctx, mod, e.X)
		if ref, ok := types.UnwrapType(opType).(*types.ReferenceType); ok && autoDerefAllowedExpr(e) {
			return ref.Inner
		}
		return opType

	case *ast.PostfixExpr:
		// Postfix operators (++, --) return the type of the operand
		opType := inferExprType(ctx, mod, e.X)
		if ref, ok := types.UnwrapType(opType).(*types.ReferenceType); ok && autoDerefAllowedExpr(e) {
			return ref.Inner
		}
		return opType

	case *ast.TypeCheckPattern:
		// Type check patterns always return bool (true if type matches)
		return types.TypeBool

	case *ast.RangeCheckPattern:
		// Range check patterns always return bool (true if value in range)
		return types.TypeBool

	default:
		return types.TypeUnknown
	}
}

// inferIdentifierType looks up an identifier's declared type
func inferIdentifierType(_ *context_v2.CompilerContext, mod *context_v2.Module, ident *ast.IdentifierExpr) types.SemType {
	sym, ok := mod.CurrentScope.Lookup(ident.Name)
	if !ok {
		return types.TypeUnknown
	}
	return sym.Type
}

// inferBinaryExprType determines the result type of a binary expression
func inferBinaryExprType(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.BinaryExpr) types.SemType {
	lhsType := inferExprType(ctx, mod, expr.X)
	rhsType := inferExprType(ctx, mod, expr.Y)
	lhsType = types.UnwrapType(lhsType)
	rhsType = types.UnwrapType(rhsType)

	switch expr.Op.Kind {
	case tokens.DOUBLE_EQUAL_TOKEN, tokens.NOT_EQUAL_TOKEN,
		tokens.LESS_TOKEN, tokens.LESS_EQUAL_TOKEN,
		tokens.GREATER_TOKEN, tokens.GREATER_EQUAL_TOKEN,
		tokens.IN_TOKEN:
		// Comparison and range check: result is always bool
		return types.TypeBool

	case tokens.AND_TOKEN, tokens.OR_TOKEN:
		// Logical: result is always bool
		return types.TypeBool
	}

	lhsUntyped := types.IsUntyped(lhsType)
	rhsUntyped := types.IsUntyped(rhsType)

	if lhsUntyped && rhsUntyped {
		// Both untyped - preserve untyped, prioritize float over int
		if types.IsUntypedFloat(lhsType) || types.IsUntypedFloat(rhsType) {
			return types.TypeUntypedFloat
		}
		return types.TypeUntypedInt
	}
	if lhsUntyped {
		return rhsType
	}
	if rhsUntyped {
		return lhsType
	}

	// Arithmetic operators - result depends on operand types
	switch expr.Op.Kind {
	case tokens.PLUS_TOKEN:
		// PLUS can be numeric addition or string concatenation
		// String concatenation: str + str, str + number, str + bool -> str
		if lhsType.Equals(types.TypeString) {
			return types.TypeString
		}
		if rhsType.Equals(types.TypeString) {
			return types.TypeString
		}
		// Otherwise numeric addition: result is the wider of the two types
		return widerType(lhsType, rhsType)

	case tokens.MINUS_TOKEN, tokens.MUL_TOKEN, tokens.MOD_TOKEN:
		// Arithmetic: result is the wider of the two types
		return widerType(lhsType, rhsType)
	case tokens.DIV_TOKEN:
		// For floats, preserve the wider float type (f32..f256).
		if types.IsFloat(lhsType) || types.IsFloat(rhsType) {
			return widerType(lhsType, rhsType)
		}
		// Large integer division stays in the large integer domain.
		if isLargeIntType(lhsType) || isLargeIntType(rhsType) {
			return widerType(lhsType, rhsType)
		}
		// Small integer division produces float: i32/i32 -> f32, i64/i64 -> f64.
		wider := widerType(lhsType, rhsType)
		if primType, ok := wider.(*types.PrimitiveType); ok {
			bitWidth := types.GetNumberBitSize(primType.GetName())
			if bitWidth > 0 && bitWidth <= 32 {
				return types.TypeF32
			}
		}
		return types.TypeF64
	case tokens.EXP_TOKEN:
		// Power operator **: returns double (f64)
		// Always returns f64 for accuracy (can be cast to int if needed)
		return types.TypeF64
	case tokens.BIT_AND_TOKEN, tokens.BIT_OR_TOKEN, tokens.BIT_XOR_TOKEN:
		// Bitwise operators preserve operand type
		return lhsType
	default:
		return types.TypeUnknown
	}
}

// inferUnaryExprType determines the result type of a unary expression
func inferUnaryExprType(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.UnaryExpr) types.SemType {
	xType := inferExprType(ctx, mod, expr.X)

	switch expr.Op.Kind {
	case tokens.MINUS_TOKEN, tokens.PLUS_TOKEN:
		// Unary +/- preserves numeric type
		return xType

	case tokens.NOT_TOKEN:
		// Logical not returns bool
		return types.TypeBool
	case tokens.BIT_NOT_TOKEN:
		// Bitwise not preserves integer type
		return xType
	case tokens.BIT_AND_TOKEN:
		// Address-of returns immutable reference type
		if _, ok := xType.(*types.ReferenceType); ok {
			return types.TypeUnknown
		}
		return types.NewReference(xType)
	case tokens.MUT_TOKEN:
		// &mut returns mutable reference type
		if _, ok := xType.(*types.ReferenceType); ok {
			return types.TypeUnknown
		}
		return types.NewMutableReference(xType)
	case tokens.AT_TOKEN:
		// Move operator keeps the operand type.
		return xType
	case tokens.HASH_TOKEN:
		// Heap operator keeps the operand type.
		return xType

	default:
		return xType
	}
}

// inferDerefExprType determines the result type of a dereference expression (*x)
func inferDerefExprType(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.DerefExpr) types.SemType {
	xType := inferExprType(ctx, mod, expr.X)

	// Unwrap to get the actual type
	unwrapped := types.UnwrapType(xType)

	// Check if it's a reference type
	if refType, ok := unwrapped.(*types.ReferenceType); ok {
		return refType.Inner
	}

	// Not a reference - type error will be reported in checkExpr
	return types.TypeUnknown
}

// inferCallExprType determines the return type of a function call
func inferCallExprType(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.CallExpr) types.SemType {
	// Get the type of the called expression (function or method)
	funType := inferExprType(ctx, mod, expr.Fun)

	// If it's a function type, get its return type
	if funcType, ok := funType.(*types.FunctionType); ok {
		returnType := funcType.Return

		// If the function returns a Result type and has a catch clause,
		// unwrap to get the success type (T ! E -> T)
		if resultType, isResult := returnType.(*types.ResultType); isResult && expr.Catch != nil {
			return resultType.Ok
		}

		return returnType
	}

	// Fallback: handle direct function name lookup (legacy path)
	if ident, ok := expr.Fun.(*ast.IdentifierExpr); ok {
		sym, exists := mod.CurrentScope.Lookup(ident.Name)
		if !exists {
			return types.TypeUnknown
		}

		// Get the function declaration
		if funcDecl, ok := sym.Decl.(*ast.FuncDecl); ok {
			if funcDecl.Type != nil && funcDecl.Type.Result != nil {
				returnType := TypeFromTypeNodeWithContext(ctx, mod, funcDecl.Type.Result)

				// Unwrap Result type if catch clause present
				if resultType, isResult := returnType.(*types.ResultType); isResult && expr.Catch != nil {
					return resultType.Ok
				}

				return returnType
			}
		}
	}

	return types.TypeUnknown
}

// inferIndexExprType determines the element type of an index expression
// For arrays: returns the element type T for []T or [N]T
// For maps: returns optional value type V? for map[K]V (since key might not exist)
func inferIndexExprType(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.IndexExpr) types.SemType {
	// Infer the type of the base expression (the thing being indexed)
	baseType := inferExprType(ctx, mod, expr.X)

	// Unwrap any named types to get to the underlying structure
	// Allow implicit deref for index expressions.
	baseType = autoDerefBaseType(expr, baseType)

	switch bt := baseType.(type) {
	case *types.ArrayType:
		// Array indexing: arr[i] returns element type T
		return bt.Element

	case *types.MapType:
		// Map indexing: map[key] returns value type V (panics if key not found)
		return bt.Value
	case *types.PrimitiveType:
		if bt.GetName() == types.TYPE_STRING {
			// String indexing returns a byte
			return types.TypeByte
		}
		return types.TypeUnknown

	default:
		// Not an indexable type (error will be reported by type checker)
		return types.TypeUnknown
	}
}

// inferSelectorExprType determines the type of a field access or method access
func inferSelectorExprType(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.SelectorExpr) types.SemType {
	// Infer the type of the base expression (e.g., p in p.x)
	baseType := inferExprType(ctx, mod, expr.X)

	// If base type is unknown, return unknown
	if baseType.Equals(types.TypeUnknown) {
		return types.TypeUnknown
	}

	// Auto-dereference for selector expressions (method calls and field access)
	// This allows: ref.field instead of (*ref).field
	baseType = autoDerefBaseType(expr, baseType)

	fieldName := expr.Field.Name

	// Check if baseType is an interface type (or NamedType wrapping an interface)
	var interfaceType *types.InterfaceType
	if iface, ok := baseType.(*types.InterfaceType); ok {
		interfaceType = iface
	} else if named, ok := baseType.(*types.NamedType); ok {
		if iface, ok := named.Underlying.(*types.InterfaceType); ok {
			interfaceType = iface
		}
	}

	// If it's an interface, return the method's function type from the interface
	if interfaceType != nil {
		for _, method := range interfaceType.Methods {
			if method.Name == fieldName {
				return method.FuncType
			}
		}
		// Method not found in interface - error will be reported by type checker
		return types.TypeUnknown
	}

	// If baseType is a NamedType, we might have methods attached to the type symbol
	// We need to check both fields (on the underlying struct) and methods (on the named type)
	var typeSym *symbols.Symbol
	var structType *types.StructType

	if namedType, ok := baseType.(*types.NamedType); ok {
		// It's a named type - look up the type symbol for method resolution
		typeName := namedType.Name
		if sym, found := lookupTypeSymbol(ctx, mod, typeName); found {
			typeSym = sym
		}

		// Get the underlying struct for field access
		underlying := types.UnwrapType(namedType)
		structType, _ = underlying.(*types.StructType)
	} else if st, ok := baseType.(*types.StructType); ok {
		// Anonymous struct - no methods, only fields
		structType = st
	}

	// Check for struct fields first
	if structType != nil {
		for _, field := range structType.Fields {
			if field.Name == fieldName {
				return field.Type
			}
		}
	}

	// Check for methods on named types
	if typeSym != nil && typeSym.Methods != nil {
		if method, ok := typeSym.Methods[fieldName]; ok {
			return method.FuncType
		}
	}

	// Field/method not found - error will be reported by type checker
	return types.TypeUnknown
}

// inferScopeResolutionExprType determines the type of a module::symbol or enum::variant access
func inferScopeResolutionExprType(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.ScopeResolutionExpr) types.SemType {
	// Handle module::symbol or enum::variant resolution
	// X should be an identifier representing the module name/alias or enum type name
	if ident, ok := expr.X.(*ast.IdentifierExpr); ok {
		leftName := ident.Name
		rightName := expr.Selector.Name

		// First check if leftName is a type (enum) in the current scope
		if typeSym, ok := mod.CurrentScope.Lookup(leftName); ok && typeSym.Kind == symbols.SymbolType {
			// This is EnumType::Variant access
			// Look up the qualified variant name
			qualifiedName := leftName + "::" + rightName
			if variantSym, ok := mod.ModuleScope.GetSymbol(qualifiedName); ok {
				return variantSym.Type
			}
			// Variant not found - error already reported by resolver
			return types.TypeUnknown
		}

		// Otherwise, check if it's a module import
		// Look up the import path from the alias/name
		importPath, ok := mod.ImportAliasMap[leftName]
		if !ok {
			// Not a module import - error already reported by resolver
			return types.TypeUnknown
		}

		// Get the imported module from context
		importedMod, exists := ctx.GetModule(importPath)
		if !exists {
			// Module not loaded - error already reported by resolver
			return types.TypeUnknown
		}

		// Look up the symbol in the imported module's module scope
		// Use ModuleScope.GetSymbol (not Lookup) to get module-level symbols only
		sym, ok := importedMod.ModuleScope.GetSymbol(rightName)
		if !ok {
			// Symbol not found - error already reported by resolver
			return types.TypeUnknown
		}

		// Return the symbol's type
		return sym.Type
	}

	// For non-identifier X (e.g., anonymous enum{...}::variant)
	// Handle anonymous enum type validation
	if enumType, ok := expr.X.(*ast.EnumType); ok {
		rightName := expr.Selector.Name

		// Check if the variant exists in the anonymous enum
		found := false
		for _, variant := range enumType.Variants {
			if variant.Name.Name == rightName {
				found = true
				break
			}
		}

		if !found {
			// todo: code smell
			// Build enum string representation with truncation (like struct)
			var enumStr string
			if len(enumType.Variants) == 0 {
				enumStr = "enum {}"
			} else if len(enumType.Variants) <= 3 {
				// Show all variants if 3 or fewer
				parts := make([]string, len(enumType.Variants))
				for i, v := range enumType.Variants {
					parts[i] = v.Name.Name
				}
				enumStr = fmt.Sprintf("enum { %s }", strings.Join(parts, ", "))
			} else {
				// Truncate: first 2, ..., last 1
				enumStr = fmt.Sprintf("enum { %s, %s, ..., %s }",
					enumType.Variants[0].Name.Name,
					enumType.Variants[1].Name.Name,
					enumType.Variants[len(enumType.Variants)-1].Name.Name)
			}

			// Build list of available variants
			variantNames := make([]string, len(enumType.Variants))
			for i, v := range enumType.Variants {
				variantNames[i] = v.Name.Name
			}

			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("variant '%s' not found in %s", rightName, enumStr)).
					WithPrimaryLabel(expr.Selector.Loc(), fmt.Sprintf("'%s' is not a valid variant", rightName)).
					WithHelp(fmt.Sprintf("available variants: %s", strings.Join(variantNames, ", "))),
			)
			return types.TypeUnknown
		}

		// Valid variant - return unknown for anonymous enums (they don't have a semantic type)
		// This is acceptable since anonymous enums are primarily for compile-time constants
		return types.TypeUnknown
	}

	// For other expression types, recurse
	return inferExprType(ctx, mod, expr.X)
}

// inferCastExprType determines the target type of a cast
func inferCastExprType(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.CastExpr) types.SemType {
	return TypeFromTypeNodeWithContext(ctx, mod, expr.Type)
}

// inferCompositeLitType determines the type of a composite literal
func inferCompositeLitType(ctx *context_v2.CompilerContext, mod *context_v2.Module, lit *ast.CompositeLit) types.SemType {
	if lit.Type != nil {
		return TypeFromTypeNodeWithContext(ctx, mod, lit.Type)
	}

	// Infer type from elements
	// Distinguish between struct literals (.field = value) and map literals (key => value)
	if len(lit.Elts) > 0 {
		allKeyValue := true
		hasStructSyntax := false // .field = value
		hasMapSyntax := false    // key => value

		for _, elem := range lit.Elts {
			kv, ok := elem.(*ast.KeyValueExpr)
			if !ok {
				allKeyValue = false
				break
			}

			// Check if key is an identifier (struct field or map key)
			if _, ok := kv.Key.(*ast.IdentifierExpr); ok {
				// Check if it looks like struct field syntax (.field)
				// Parser doesn't store dot prefix, so we infer from context
				// For now, treat plain identifiers as struct fields
				// Map keys should be arbitrary expressions
				hasStructSyntax = true
			} else {
				// Non-identifier key (expression) means map syntax
				hasMapSyntax = true
			}
		}

		// Array literal: [1, 2, 3] - elements without keys
		if !allKeyValue {
			if len(lit.Elts) == 0 {
				// Empty array literal - type unknown without context
				return types.TypeUnknown
			}

			// Infer type from first element
			firstElemType := inferExprType(ctx, mod, lit.Elts[0])

			// If first element is untyped literal, infer its type
			if types.IsUntyped(firstElemType) {
				if lit, ok := lit.Elts[0].(*ast.BasicLit); ok {
					firstElemType = inferLiteralType(lit, types.TypeUnknown)
				} else {
					firstElemType = types.ResolveUntypedType(firstElemType, types.TypeUnknown)
				}
			}

			// All subsequent elements must match the first element's type
			// This is already enforced by type checking, so we just use first element's type

			if !firstElemType.Equals(types.TypeUnknown) {
				// Create dynamic array type: []T
				return types.NewArray(firstElemType, -1)
			}
		}

		if allKeyValue {
			// Determine if this is a struct or map literal
			// Struct: all keys are identifiers (field names)
			// Map: keys are arbitrary expressions
			if hasStructSyntax && !hasMapSyntax {
				// Struct literal: { .x: 10, .y: 20 }
				fields := make([]types.StructField, 0, len(lit.Elts))
				for _, elem := range lit.Elts {
					kv := elem.(*ast.KeyValueExpr)
					if key, ok := kv.Key.(*ast.IdentifierExpr); ok {
						fieldName := key.Name
						fieldType := inferExprType(ctx, mod, kv.Value)
						fields = append(fields, types.StructField{
							Name: fieldName,
							Type: fieldType,
						})
					}
				}

				if len(fields) > 0 {
					// Create anonymous struct type (empty name)
					return types.NewStruct("", fields)
				}
			} else if hasMapSyntax || len(lit.Elts) > 0 {
				// Map literal: { "key" => value, "key2" => value2 }
				// Infer key and value types by widening numeric types and preserving identical non-numeric types.
				var keyType, valueType types.SemType = types.TypeUnknown, types.TypeUnknown

				for _, elem := range lit.Elts {
					kv := elem.(*ast.KeyValueExpr)
					elemKeyType := inferExprType(ctx, mod, kv.Key)
					elemValueType := inferExprType(ctx, mod, kv.Value)

					// Resolve untyped literals to their default types
					if types.IsUntyped(elemKeyType) {
						if lit, ok := kv.Key.(*ast.BasicLit); ok {
							elemKeyType = inferLiteralType(lit, types.TypeUnknown)
						} else {
							elemKeyType = types.ResolveUntypedType(elemKeyType, types.TypeUnknown)
						}
					}
					if types.IsUntyped(elemValueType) {
						if lit, ok := kv.Value.(*ast.BasicLit); ok {
							elemValueType = inferLiteralType(lit, types.TypeUnknown)
						} else {
							elemValueType = types.ResolveUntypedType(elemValueType, types.TypeUnknown)
						}
					}

					// Merge key/value types: widen numeric types, keep identical non-numeric types.
					if keyType.Equals(types.TypeUnknown) {
						keyType = elemKeyType
					} else if !elemKeyType.Equals(types.TypeUnknown) {
						if types.IsNumericType(keyType) && types.IsNumericType(elemKeyType) {
							keyType = widerType(keyType, elemKeyType)
						} else if keyType.Equals(elemKeyType) {
							keyType = elemKeyType
						}
					}

					if valueType.Equals(types.TypeUnknown) {
						valueType = elemValueType
					} else if !elemValueType.Equals(types.TypeUnknown) {
						if types.IsNumericType(valueType) && types.IsNumericType(elemValueType) {
							valueType = widerType(valueType, elemValueType)
						} else if valueType.Equals(elemValueType) {
							valueType = elemValueType
						}
					}
				}

				if !keyType.Equals(types.TypeUnknown) && !valueType.Equals(types.TypeUnknown) {
					return types.NewMap(keyType, valueType)
				}
			}
		}
	}

	return types.TypeUnknown
}

// widerType returns the wider of two numeric types for implicit conversion
func widerType(a, b types.SemType) types.SemType {
	// If types are the same, return either
	if a.Equals(b) {
		return a
	}

	// Extract primitive names for comparison
	aName, aOk := types.GetPrimitiveName(a)
	bName, bOk := types.GetPrimitiveName(b)
	if !aOk || !bOk {
		return types.TypeUnknown
	}

	// Order of widening: i8 < i16 < i32 < i64, u8 < u16 < u32 < u64, f32 < f64
	typeRank := map[types.TYPE_NAME]int{
		types.TYPE_I8:   1,
		types.TYPE_I16:  2,
		types.TYPE_I32:  3,
		types.TYPE_I64:  4,
		types.TYPE_I128: 5,
		types.TYPE_I256: 6,
		types.TYPE_U8:   1,
		types.TYPE_U16:  2,
		types.TYPE_U32:  3,
		types.TYPE_U64:  4,
		types.TYPE_U128: 5,
		types.TYPE_U256: 6,
		types.TYPE_F32:  7,
		types.TYPE_F64:  8,
		types.TYPE_F128: 9,
		types.TYPE_F256: 10,
	}

	rankA, okA := typeRank[aName]
	rankB, okB := typeRank[bName]

	if !okA || !okB {
		// If either type is not numeric, return unknown
		return types.TypeUnknown
	}

	if rankA > rankB {
		return a
	}
	return b
}

func isLargeIntType(t types.SemType) bool {
	if t == nil {
		return false
	}
	t = types.UnwrapType(t)
	prim, ok := t.(*types.PrimitiveType)
	if !ok {
		return false
	}
	switch prim.GetName() {
	case types.TYPE_I128, types.TYPE_U128, types.TYPE_I256, types.TYPE_U256:
		return true
	default:
		return false
	}
}

// resolveType resolves an untyped type to a concrete type.
// For expressions without literals (e.g., binary expr results), uses contextual or default types.
// inferFuncLitType infers the type of a function literal
func inferFuncLitType(ctx *context_v2.CompilerContext, mod *context_v2.Module, lit *ast.FuncLit) types.SemType {
	if lit.Type == nil {
		return types.TypeUnknown
	}

	// Enter function literal scope for type checking the body
	if lit.Scope != nil {
		defer mod.EnterScope(lit.Scope.(*table.SymbolTable))()
	}

	// Build parameter types
	params := make([]types.ParamType, len(lit.Type.Params))
	for i, param := range lit.Type.Params {
		paramType := TypeFromTypeNodeWithContext(ctx, mod, param.Type)
		params[i] = types.ParamType{
			Name: param.Name.Name,
			Type: paramType,
		}

		// Update symbol table with the parameter's type
		if sym, ok := mod.CurrentScope.GetSymbol(param.Name.Name); ok {
			sym.Type = paramType
		}
	}

	// Get return type (default to void when omitted)
	returnType := types.TypeVoid
	if lit.Type.Result != nil {
		returnType = TypeFromTypeNodeWithContext(ctx, mod, lit.Type.Result)
	}

	// Set the current function return type for validating return statements
	// Save the parent function's return type and restore it after
	prevReturnType := mod.CurrentFunctionReturnType
	mod.CurrentFunctionReturnType = returnType
	defer func() { mod.CurrentFunctionReturnType = prevReturnType }()

	// Type check the function body
	if lit.Body != nil {
		checkBlock(ctx, mod, lit.Body)
	}

	return types.NewFunction(params, returnType)
}

// inferRangeExprType infers the type of a range expression (start..end[:incr])
// Returns an array type with element type inferred from start/end
func inferRangeExprType(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.RangeExpr) types.SemType {
	// Infer types from start and end expressions
	startType := inferExprType(ctx, mod, expr.Start)
	endType := inferExprType(ctx, mod, expr.End)
	var stepType types.SemType = types.TypeUnknown
	if expr.Incr != nil {
		stepType = inferExprType(ctx, mod, expr.Incr)
	}

	// Contextualize untyped types
	if startLit, ok := expr.Start.(*ast.BasicLit); ok && types.IsUntyped(startType) {
		startType = inferLiteralType(startLit, types.TypeUnknown)
	} else {
		startType = types.ResolveUntypedType(startType, types.TypeUnknown)
	}
	if endLit, ok := expr.End.(*ast.BasicLit); ok && types.IsUntyped(endType) {
		endType = inferLiteralType(endLit, types.TypeUnknown)
	} else {
		endType = types.ResolveUntypedType(endType, types.TypeUnknown)
	}
	if expr.Incr != nil {
		if stepLit, ok := expr.Incr.(*ast.BasicLit); ok && types.IsUntyped(stepType) {
			stepType = inferLiteralType(stepLit, types.TypeUnknown)
		} else {
			stepType = types.ResolveUntypedType(stepType, types.TypeUnknown)
		}
	}

	// Validate that ranges only use integer types
	if !types.IsInteger(startType) {
		ctx.Diagnostics.Add(
			diagnostics.NewError("invalid start value in range").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(expr.Start.Loc(), "range must start with integer value").
				WithHelp("ranges only support integer types (i8, i16, i32, i64..., u8, u16, u32, u64...)"),
		)
	}
	if !types.IsInteger(endType) {
		ctx.Diagnostics.Add(
			diagnostics.NewError("invalid end value in range").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(expr.End.Loc(), "range must end with integer value").
				WithHelp("ranges only support integer types (i8, i16, i32, i64..., u8, u16, u32, u64...)"),
		)
	}
	if expr.Incr != nil && !types.IsInteger(stepType) {
		ctx.Diagnostics.Add(
			diagnostics.NewError("invalid step value in range").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(expr.Incr.Loc(), "range step must be an integer value").
				WithHelp("ranges only support integer types (i8, i16, i32, i64..., u8, u16, u32, u64...)"),
		)
	}

	// Return array type with element type from range
	// Use the wider type to accommodate both start and end values
	elementType := widerType(startType, endType)
	if expr.Incr != nil {
		elementType = widerType(elementType, stepType)
	}
	length := -1
	if constLength, ok := computeRangeConstLength(expr); ok {
		length = constLength
	}
	return &types.ArrayType{Element: elementType, Length: length}
}

// inferCoalescingExprType determines the result type of an coalescing expression (a ?? b)
// The coalescing operator unwraps optional types:
// - If 'a' has type T?, the result is T (non-optional)
// - The fallback 'b' should have type T to match
// - Result type is always the non-optional inner type
func inferCoalescingExprType(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.CoalescingExpr) types.SemType {
	condType := inferExprType(ctx, mod, expr.Cond)

	// Check if condition is an optional type
	if optType, ok := condType.(*types.OptionalType); ok {
		// Coalescing operator unwraps the optional: T? ?? T → T
		return optType.Inner
	}

	// Error: coalescing operator requires optional type on left side
	// Resolve untyped to actual type for better error message
	displayType := condType
	if types.IsUntyped(condType) {
		// Infer the actual type from the literal
		if lit, ok := expr.Cond.(*ast.BasicLit); ok {
			displayType = inferLiteralType(lit, types.TypeUnknown)
		} else {
			displayType = types.ResolveUntypedType(condType, types.TypeUnknown)
		}
	}

	ctx.Diagnostics.Add(
		diagnostics.NewError(fmt.Sprintf("invalid operation: '??' operator requires optional type, got %s", displayType.String())).
			WithCode(diagnostics.ErrTypeMismatch).
			WithPrimaryLabel(expr.Cond.Loc(), fmt.Sprintf("this has type %s, which is not optional", displayType.String())).
			WithHelp("the '??' operator unwraps optional values; use it with optional types like 'i32?', 'str?', etc."),
	)

	// Return TypeUnknown to signal an error
	return types.TypeUnknown
}
