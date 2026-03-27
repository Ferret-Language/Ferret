package typechecker

import (
	"fmt"

	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	"compiler/internal/frontend/ast"
)

func canonicalGenericUseMatchesDecl(named *ast.NamedType, decl *ast.TypeDecl) bool {
	if named == nil || decl == nil {
		return false
	}
	if len(decl.TypeParams) == 0 {
		return len(named.TypeArgs) == 0
	}
	if len(named.TypeArgs) != len(decl.TypeParams) {
		return false
	}
	for i, param := range decl.TypeParams {
		if param.Name == nil {
			return false
		}
		arg, ok := named.TypeArgs[i].(*ast.NamedType)
		if !ok || arg == nil || len(arg.Path) != 1 || len(arg.TypeArgs) != 0 || arg.Path[0] != param.Name.Text() {
			return false
		}
	}
	return true
}

func canonicalGenericUseText(decl *ast.TypeDecl) string {
	if decl == nil || decl.Name == nil {
		return ""
	}
	if len(decl.TypeParams) == 0 {
		return decl.Name.Text()
	}
	text := decl.Name.Text() + "<"
	for i, param := range decl.TypeParams {
		if i > 0 {
			text += ", "
		}
		if param.Name == nil {
			text += "_"
			continue
		}
		text += param.Name.Text()
	}
	return text + ">"
}

func (c *checker) reportInvalidGenericUse(named *ast.NamedType, decl *ast.TypeDecl, message string) {
	if c == nil || named == nil || decl == nil {
		return
	}
	loc := named.Loc()
	c.ctx.Diagnostics.Add(
		diagnostics.NewError(message).
			WithCode(diagnostics.ErrInvalidType).
			WithPrimaryLabel(&loc, "use "+canonicalGenericUseText(decl)+" here"),
	)
}

func (c *checker) checkCanonicalGenericSelfUse(mod *context.Module, decl *ast.TypeDecl, expr ast.TypeExpr) {
	ast.WalkType(expr, func(typ ast.TypeExpr) bool {
		named, ok := typ.(*ast.NamedType)
		if !ok || named == nil {
			return true
		}
		resolution := c.lookupTypeResolution(mod, named)
		if resolution != nil && resolution.Symbol != nil && resolution.Symbol.Node == decl && !canonicalGenericUseMatchesDecl(named, decl) {
			c.reportInvalidGenericUse(named, decl, fmt.Sprintf("recursive reference to generic type %q must preserve declaration type parameters", decl.Name.Text()))
		}
		return true
	})
}
