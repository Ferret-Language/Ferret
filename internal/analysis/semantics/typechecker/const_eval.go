package typechecker

import (
	"math/big"

	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/core/context"
	"compiler/internal/frontend/ast"
	"compiler/internal/utils/numeric"
)

type constValueKind uint8

const (
	constInvalid constValueKind = iota
	constInt
	constBool
	constString
	constNone
)

type constValue struct {
	kind   constValueKind
	intVal *big.Int
	boolV  bool
	strVal string
}

func (v constValue) nonNegativeInt64() (int64, bool) {
	if v.kind != constInt || v.intVal == nil || v.intVal.Sign() < 0 || !v.intVal.IsInt64() {
		return 0, false
	}
	return v.intVal.Int64(), true
}

func (c *checker) constExpr(mod *context.Module, expr ast.Expr, seen map[ast.Node]bool) (constValue, bool) {
	switch e := expr.(type) {
	case nil:
		return constValue{}, false
	case *ast.NumberLit:
		value, err := numeric.StringToBigInt(e.Value)
		if err != nil {
			return constValue{}, false
		}
		return constValue{kind: constInt, intVal: value}, true
	case *ast.StringLit:
		return constValue{kind: constString, strVal: e.Value}, true
	case *ast.NoneLit:
		return constValue{kind: constNone}, true
	case *ast.Ident:
		switch e.Text() {
		case "_":
			return constValue{}, false
		case "true":
			return constValue{kind: constBool, boolV: true}, true
		case "false":
			return constValue{kind: constBool, boolV: false}, true
		case "none":
			return constValue{kind: constNone}, true
		}
		res := c.lookupTypeResolution(mod, e)
		if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil || res.Symbol.Kind != symbols.SymbolConst || res.Symbol.Node == nil {
			return constValue{}, false
		}
		owner := c.findModuleForSymbol(res.Symbol)
		if owner == nil {
			owner = mod
		}
		if seen == nil {
			seen = make(map[ast.Node]bool)
		}
		if seen[res.Symbol.Node] {
			return constValue{}, false
		}
		seen[res.Symbol.Node] = true
		defer delete(seen, res.Symbol.Node)
		switch n := res.Symbol.Node.(type) {
		case *ast.ConstDecl:
			return c.constExpr(owner, n.Value, seen)
		case *ast.ConstStmt:
			return c.constExpr(owner, n.Value, seen)
		default:
			return constValue{}, false
		}
	case *ast.PrefixExpr:
		right, ok := c.constExpr(mod, e.Right, seen)
		if !ok {
			return constValue{}, false
		}
		switch e.Op {
		case "comptime", "copy", "take", "unsafe", "?":
			return right, true
		case "!":
			if right.kind != constBool {
				return constValue{}, false
			}
			return constValue{kind: constBool, boolV: !right.boolV}, true
		case "-":
			if right.kind != constInt || right.intVal == nil {
				return constValue{}, false
			}
			return constValue{kind: constInt, intVal: new(big.Int).Neg(new(big.Int).Set(right.intVal))}, true
		case "+":
			if right.kind != constInt || right.intVal == nil {
				return constValue{}, false
			}
			return constValue{kind: constInt, intVal: new(big.Int).Set(right.intVal)}, true
		default:
			return constValue{}, false
		}
	case *ast.BinaryExpr:
		left, ok := c.constExpr(mod, e.Left, seen)
		if !ok {
			return constValue{}, false
		}
		right, ok := c.constExpr(mod, e.Right, seen)
		if !ok {
			return constValue{}, false
		}
		if left.kind != constInt || left.intVal == nil || right.kind != constInt || right.intVal == nil {
			return constValue{}, false
		}
		switch e.Op {
		case "+":
			return constValue{kind: constInt, intVal: new(big.Int).Add(left.intVal, right.intVal)}, true
		case "-":
			return constValue{kind: constInt, intVal: new(big.Int).Sub(left.intVal, right.intVal)}, true
		case "*":
			return constValue{kind: constInt, intVal: new(big.Int).Mul(left.intVal, right.intVal)}, true
		case "/":
			if right.intVal.Sign() == 0 {
				return constValue{}, false
			}
			return constValue{kind: constInt, intVal: new(big.Int).Quo(left.intVal, right.intVal)}, true
		case "%":
			if right.intVal.Sign() == 0 {
				return constValue{}, false
			}
			return constValue{kind: constInt, intVal: new(big.Int).Rem(left.intVal, right.intVal)}, true
		default:
			return constValue{}, false
		}
	case *ast.CastExpr:
		return c.constExpr(mod, e.Left, seen)
	default:
		return constValue{}, false
	}
}
