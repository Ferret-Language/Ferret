package narrowing

import (
	"compiler/internal/frontend/ast"
)

// ExprKey returns a stable key for simple expressions (ident, selector, index).
// It is used for narrowing values like a.b or arr[i].
func ExprKey(expr ast.Expression) (string, bool) {
	if expr == nil {
		return "", false
	}

	switch e := expr.(type) {
	case *ast.IdentifierExpr:
		return e.Name, true
	case *ast.BasicLit:
		return e.Value, true
	case *ast.SelectorExpr:
		base, ok := ExprKey(e.X)
		if !ok {
			return "", false
		}
		if e.Field == nil {
			return "", false
		}
		return base + "." + e.Field.Name, true
	case *ast.IndexExpr:
		base, ok := ExprKey(e.X)
		if !ok {
			return "", false
		}
		indexKey, ok := ExprKey(e.Index)
		if !ok {
			return "", false
		}
		return base + "[" + indexKey + "]", true
	case *ast.ParenExpr:
		return ExprKey(e.X)
	case *ast.DerefExpr:
		base, ok := ExprKey(e.X)
		if !ok {
			return "", false
		}
		return "*" + base, true
	case *ast.SpreadExpr:
		return ExprKey(e.X)
	default:
		return "", false
	}
}
