package consteval

import (
	"compiler/internal/context_v2"
	"compiler/internal/hir"
	"compiler/internal/tokens"
	"compiler/internal/types"
	"math/big"
	"strconv"
)

// EvaluateHIRExpr attempts to evaluate a HIR expression to a compile-time constant value.
// Returns nil if the expression cannot be evaluated at compile-time.
func EvaluateHIRExpr(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr hir.Expr) *ConstValue {
	if expr == nil {
		return nil
	}

	switch e := expr.(type) {
	case *hir.Literal:
		return evaluateHIRLiteral(e)

	case *hir.Ident:
		return evaluateHIRIdentifier(ctx, mod, e)

	case *hir.UnaryExpr:
		return evaluateHIRUnary(ctx, mod, e)

	case *hir.DerefExpr:
		// Cannot evaluate dereferences at compile time
		return nil

	case *hir.BinaryExpr:
		return evaluateHIRBinary(ctx, mod, e)

	case *hir.ParenExpr:
		return EvaluateHIRExpr(ctx, mod, e.X)

	default:
		// Cannot evaluate: function calls, indexing, field access, etc.
		return nil
	}
}

// evaluateHIRLiteral extracts the constant value from a HIR literal.
func evaluateHIRLiteral(lit *hir.Literal) *ConstValue {
	switch lit.Kind {
	case hir.LiteralInt:
		// Parse as big integer.
		val, ok := new(big.Int).SetString(lit.Value, 0)
		if !ok {
			return nil
		}
		// Determine type based on value size, unless HIR already provides a concrete type.
		typ := lit.Type
		if typ == nil || typ.Equals(types.TypeUnknown) {
			typ = inferIntType(val)
		}
		return NewBigIntValue(val, typ)

	case hir.LiteralFloat:
		// Parse as big float.
		val, _, err := big.ParseFloat(lit.Value, 10, 256, big.ToNearestEven)
		if err != nil {
			return nil
		}
		// Default to f64 for untyped floats, unless HIR has a concrete type.
		typ := lit.Type
		if typ == nil || typ.Equals(types.TypeUnknown) {
			typ = types.TypeF64
		}
		return NewBigFloatValue(val, typ)

	case hir.LiteralString:
		// String literal - remove quotes.
		str, err := strconv.Unquote(lit.Value)
		if err != nil {
			// If unquote fails, use raw value without quotes.
			str = lit.Value
			if len(str) >= 2 && str[0] == '"' && str[len(str)-1] == '"' {
				str = str[1 : len(str)-1]
			}
		}
		return NewStringValue(str)

	case hir.LiteralBool:
		val := lit.Value == "true"
		return NewBoolValue(val)

	default:
		return nil
	}
}

// evaluateHIRIdentifier looks up a variable/constant and returns its constant value if known.
func evaluateHIRIdentifier(_ *context_v2.CompilerContext, mod *context_v2.Module, ident *hir.Ident) *ConstValue {
	if ident == nil {
		return nil
	}

	// Special case: true and false are parsed as identifiers but are boolean literals.
	if ident.Name == "true" {
		return NewBoolValue(true)
	}
	if ident.Name == "false" {
		return NewBoolValue(false)
	}

	// Prefer resolved symbol on HIR.
	if ident.Symbol != nil && ident.Symbol.ConstValue != nil && ident.Symbol.ConstValue.IsConstant() {
		if cv, ok := ident.Symbol.ConstValue.(*ConstValue); ok {
			return cv
		}
	}

	// Fallback: look up in current scope if available.
	if mod == nil || mod.CurrentScope == nil {
		return nil
	}

	sym, ok := mod.CurrentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return nil
	}
	if sym.ConstValue != nil && sym.ConstValue.IsConstant() {
		if cv, ok := sym.ConstValue.(*ConstValue); ok {
			return cv
		}
	}

	return nil
}

// evaluateHIRUnary evaluates unary operations (-x, !x, +x).
func evaluateHIRUnary(ctx *context_v2.CompilerContext, mod *context_v2.Module, unary *hir.UnaryExpr) *ConstValue {
	operand := EvaluateHIRExpr(ctx, mod, unary.X)
	if operand == nil {
		return nil
	}

	switch unary.Op.Kind {
	case tokens.MINUS_TOKEN:
		// Numeric negation.
		if val, ok := operand.AsInt(); ok {
			negated := new(big.Int).Neg(val)
			return NewBigIntValue(negated, operand.Type)
		}
		if val, ok := operand.AsFloat(); ok {
			negated := new(big.Float).Neg(val)
			return NewBigFloatValue(negated, operand.Type)
		}

	case tokens.PLUS_TOKEN:
		// Unary plus - just return the operand.
		if operand.Kind == ConstInt || operand.Kind == ConstFloat {
			return operand
		}

	case tokens.NOT_TOKEN:
		// Logical negation.
		if val, ok := operand.AsBool(); ok {
			return NewBoolValue(!val)
		}
	}

	return nil
}

// evaluateHIRBinary evaluates binary operations.
func evaluateHIRBinary(ctx *context_v2.CompilerContext, mod *context_v2.Module, binary *hir.BinaryExpr) *ConstValue {
	left := EvaluateHIRExpr(ctx, mod, binary.X)
	right := EvaluateHIRExpr(ctx, mod, binary.Y)

	if left == nil || right == nil {
		return nil
	}

	// Type compatibility check.
	if left.Kind != right.Kind {
		return nil // Cannot operate on different types.
	}

	switch binary.Op.Kind {
	// Arithmetic operations.
	case tokens.PLUS_TOKEN:
		return evalAdd(left, right)
	case tokens.MINUS_TOKEN:
		return evalSubtract(left, right)
	case tokens.MUL_TOKEN:
		return evalMultiply(left, right)
	case tokens.DIV_TOKEN:
		return evalDivide(left, right)
	case tokens.MOD_TOKEN:
		return evalModulo(left, right)

	// Comparison operations.
	case tokens.DOUBLE_EQUAL_TOKEN:
		return NewBoolValue(left.Equals(right))
	case tokens.NOT_EQUAL_TOKEN:
		return NewBoolValue(!left.Equals(right))
	case tokens.LESS_TOKEN:
		return evalLessThan(left, right)
	case tokens.GREATER_TOKEN:
		return evalGreaterThan(left, right)
	case tokens.LESS_EQUAL_TOKEN:
		return evalLessThanEqual(left, right)
	case tokens.GREATER_EQUAL_TOKEN:
		return evalGreaterThanEqual(left, right)

	// Logical operations.
	case tokens.AND_TOKEN:
		return evalLogicalAnd(left, right)
	case tokens.OR_TOKEN:
		return evalLogicalOr(left, right)
	}

	return nil
}
