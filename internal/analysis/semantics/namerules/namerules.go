package namerules

import (
	"fmt"

	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/source"
	"compiler/internal/tokens"
)

// Validate reports whether a user-declared symbol name is allowed.
// It rejects language keywords and names that collide with implicit globals.
func Validate(ctx *context.CompilerContext, entity, name string, loc source.Location) bool {
	if ctx == nil || name == "" {
		return true
	}
	if tokens.IsKeyword(name) {
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("%s name %q is a reserved keyword", entity, name)).
				WithCode(diagnostics.ErrInvalidDeclaration).
				WithPrimaryLabel(&loc, "choose a non-keyword name"),
		)
		return false
	}
	global, ok := lookupUniverse(ctx, name)
	if !ok {
		return true
	}
	diag := diagnostics.NewError(fmt.Sprintf("%s name %q conflicts with implicit global %q", entity, name, name)).
		WithCode(diagnostics.ErrRedeclaredSymbol).
		WithPrimaryLabel(&loc, "rename this declaration to avoid global collision")
	if global != nil && global.Location.Start != nil && global.Location.End != nil {
		prev := global.Location
		diag.WithSecondaryLabel(&prev, "global symbol is declared here")
	}
	ctx.Diagnostics.Add(diag)
	return false
}

func lookupUniverse(ctx *context.CompilerContext, name string) (*symbols.Symbol, bool) {
	if ctx == nil || ctx.Universe == nil || name == "" {
		return nil, false
	}
	return ctx.Universe.Lookup(name)
}
