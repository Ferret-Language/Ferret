package typechecker

import (
	"math/big"

	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/context"
	"compiler/internal/frontend/ast"
	"compiler/internal/utils/numeric"
)

func (c *checker) constExpr(mod *context.Module, expr ast.Expr, seen map[ast.Node]bool) (typeinfo.ConstValue, bool) {
	switch e := expr.(type) {
	case nil:
		return typeinfo.ConstValue{}, false
	case *ast.NumberLit:
		value, err := numeric.StringToBigInt(e.Value)
		if err != nil {
			return typeinfo.ConstValue{}, false
		}
		return typeinfo.ConstValue{Kind: typeinfo.ConstInt, Int: value}, true
	case *ast.StringLit:
		return typeinfo.ConstValue{Kind: typeinfo.ConstString, String: e.Value}, true
	case *ast.NoneLit:
		return typeinfo.ConstValue{Kind: typeinfo.ConstNone}, true
	case *ast.Ident:
		switch e.Text() {
		case "_":
			return typeinfo.ConstValue{}, false
		case "true":
			return typeinfo.ConstValue{Kind: typeinfo.ConstBool, Bool: true}, true
		case "false":
			return typeinfo.ConstValue{Kind: typeinfo.ConstBool, Bool: false}, true
		case "none":
			return typeinfo.ConstValue{Kind: typeinfo.ConstNone}, true
		}
		res := c.lookupTypeResolution(mod, e)
		if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil || res.Symbol.Node == nil {
			return typeinfo.ConstValue{}, false
		}
		owner := c.findModuleForSymbol(res.Symbol)
		if owner == nil {
			owner = mod
		}
		info := c.info
		if owner != nil && owner != c.mod {
			info = owner.Types
		}
		if info != nil {
			if value, ok := info.LookupConstValue(res.Symbol.Node); ok {
				return value, true
			}
		}
		if res.Symbol.Kind != symbols.SymbolConst {
			return typeinfo.ConstValue{}, false
		}
		if seen == nil {
			seen = make(map[ast.Node]bool)
		}
		if seen[res.Symbol.Node] {
			return typeinfo.ConstValue{}, false
		}
		seen[res.Symbol.Node] = true
		defer delete(seen, res.Symbol.Node)
		switch n := res.Symbol.Node.(type) {
		case *ast.ConstDecl:
			return c.constExpr(owner, n.Value, seen)
		case *ast.ConstStmt:
			return c.constExpr(owner, n.Value, seen)
		default:
			return typeinfo.ConstValue{}, false
		}
	case *ast.PrefixExpr:
		right, ok := c.constExpr(mod, e.Right, seen)
		if !ok {
			return typeinfo.ConstValue{}, false
		}
		switch e.Op {
		case "comptime", "copy", "take", "unsafe", "?":
			return right, true
		case "!":
			if right.Kind != typeinfo.ConstBool {
				return typeinfo.ConstValue{}, false
			}
			return typeinfo.ConstValue{Kind: typeinfo.ConstBool, Bool: !right.Bool}, true
		case "-":
			if right.Kind != typeinfo.ConstInt || right.Int == nil {
				return typeinfo.ConstValue{}, false
			}
			return typeinfo.ConstValue{Kind: typeinfo.ConstInt, Int: new(big.Int).Neg(new(big.Int).Set(right.Int))}, true
		case "+":
			if right.Kind != typeinfo.ConstInt || right.Int == nil {
				return typeinfo.ConstValue{}, false
			}
			return typeinfo.ConstValue{Kind: typeinfo.ConstInt, Int: new(big.Int).Set(right.Int)}, true
		default:
			return typeinfo.ConstValue{}, false
		}
	case *ast.BinaryExpr:
		left, ok := c.constExpr(mod, e.Left, seen)
		if !ok {
			return typeinfo.ConstValue{}, false
		}
		right, ok := c.constExpr(mod, e.Right, seen)
		if !ok {
			return typeinfo.ConstValue{}, false
		}
		if left.Kind != typeinfo.ConstInt || left.Int == nil || right.Kind != typeinfo.ConstInt || right.Int == nil {
			return typeinfo.ConstValue{}, false
		}
		switch e.Op {
		case "+":
			return typeinfo.ConstValue{Kind: typeinfo.ConstInt, Int: new(big.Int).Add(left.Int, right.Int)}, true
		case "-":
			return typeinfo.ConstValue{Kind: typeinfo.ConstInt, Int: new(big.Int).Sub(left.Int, right.Int)}, true
		case "*":
			return typeinfo.ConstValue{Kind: typeinfo.ConstInt, Int: new(big.Int).Mul(left.Int, right.Int)}, true
		case "/":
			if right.Int.Sign() == 0 {
				return typeinfo.ConstValue{}, false
			}
			return typeinfo.ConstValue{Kind: typeinfo.ConstInt, Int: new(big.Int).Quo(left.Int, right.Int)}, true
		case "%":
			if right.Int.Sign() == 0 {
				return typeinfo.ConstValue{}, false
			}
			return typeinfo.ConstValue{Kind: typeinfo.ConstInt, Int: new(big.Int).Rem(left.Int, right.Int)}, true
		default:
			return typeinfo.ConstValue{}, false
		}
	case *ast.CastExpr:
		return c.constExpr(mod, e.Left, seen)
	default:
		return typeinfo.ConstValue{}, false
	}
}
