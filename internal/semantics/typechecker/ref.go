package typechecker

import (
	"fmt"

	"compiler/internal/context_v2"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/semantics/symbols"
	"compiler/internal/source"
	"compiler/internal/types"
)

// =============================================================================
// MUTABILITY CHECKING SYSTEM
// =============================================================================
// This section contains the unified mutability checking system that validates
// whether a target expression can be mutated. It handles:
// - Constants (error)
// - Read-only variables like loop indices (error)
// - Immutable references &T (error)
// - Value receivers (warning - modifications don't affect caller)
// - Mutable references &'T (allowed)
// =============================================================================

type MutabilityResult int

const (
	MutabilityAllowed       MutabilityResult = iota // Mutation is allowed
	MutabilityConstant                              // Cannot modify constant
	MutabilityReadOnly                              // Cannot modify read-only variable
	MutabilityImmutableRef                          // Cannot modify through immutable reference
	MutabilityValueReceiver                         // Value receiver - warning only
)

// MutabilityInfo contains information about why mutation is not allowed
type MutabilityInfo struct {
	Result   MutabilityResult
	Symbol   *symbols.Symbol // The symbol that blocks mutation (if any)
	Location *source.Location
}

// checkMutability checks if the target expression can be mutated.
// Returns MutabilityInfo with the result and relevant symbol/location.
func checkMutability(ctx *context_v2.CompilerContext, mod *context_v2.Module, target ast.Expression) MutabilityInfo {
	if ctx == nil || mod == nil || target == nil {
		return MutabilityInfo{Result: MutabilityAllowed}
	}

	// Check for constant or read-only at the root
	switch t := target.(type) {
	case *ast.IdentifierExpr:
		if sym, found := mod.CurrentScope.Lookup(t.Name); found {
			if sym.Kind == symbols.SymbolConstant {
				return MutabilityInfo{Result: MutabilityConstant, Symbol: sym, Location: t.Loc()}
			}
			if sym.IsReadonly {
				return MutabilityInfo{Result: MutabilityReadOnly, Symbol: sym, Location: t.Loc()}
			}
		}
	case *ast.ScopeResolutionExpr:
		if sym, ok := resolveScopeResolutionSymbol(ctx, mod, t); ok && sym != nil {
			if sym.Kind == symbols.SymbolConstant {
				return MutabilityInfo{Result: MutabilityConstant, Symbol: sym, Location: t.Loc()}
			}
			if sym.IsReadonly {
				return MutabilityInfo{Result: MutabilityReadOnly, Symbol: sym, Location: t.Loc()}
			}
		}
	}

	// Check for immutable reference in the expression chain
	sym, loc := findImmutableRefInChain(ctx, mod, target)
	if sym != nil {
		return MutabilityInfo{Result: MutabilityImmutableRef, Symbol: sym, Location: loc}
	}

	// Check for value receiver (warning only)
	recvSym := findValueReceiverInChain(mod, target)
	if recvSym != nil {
		return MutabilityInfo{Result: MutabilityValueReceiver, Symbol: recvSym, Location: target.Loc()}
	}

	return MutabilityInfo{Result: MutabilityAllowed}
}

// reportMutabilityError reports an error or warning based on the mutability check result.
// Returns true if mutation is blocked (error reported), false if allowed or just warning.
func reportMutabilityError(ctx *context_v2.CompilerContext, info MutabilityInfo, target ast.Expression) bool {
	if ctx == nil {
		return false
	}

	switch info.Result {
	case MutabilityAllowed:
		return false

	case MutabilityConstant:
		var declLoc *source.Location
		if info.Symbol != nil && info.Symbol.Decl != nil {
			declLoc = info.Symbol.Decl.Loc()
		}
		diag := diagnostics.NewError(fmt.Sprintf("cannot assign to constant '%s'", info.Symbol.Name)).
			WithCode(diagnostics.ErrInvalidAssignment).
			WithPrimaryLabel(target.Loc(), "cannot modify constant").
			WithHelp("constants are immutable; use 'let' for mutable variables")
		if declLoc != nil {
			diag = diag.WithSecondaryLabel(declLoc, "declared as constant here")
		}
		ctx.Diagnostics.Add(diag)
		return true

	case MutabilityReadOnly:
		ctx.Diagnostics.Add(
			diagnostics.NewError(fmt.Sprintf("cannot modify read-only variable '%s'", info.Symbol.Name)).
				WithCode(diagnostics.ErrInvalidAssignment).
				WithPrimaryLabel(target.Loc(), "read-only variable").
				WithNote("loop indices and catch errors are read-only"),
		)
		return true

	case MutabilityImmutableRef:
		var declLoc *source.Location
		if info.Symbol != nil && info.Symbol.Decl != nil {
			declLoc = info.Symbol.Decl.Loc()
		}

		var kindLabel string
		if info.Symbol != nil {
			switch info.Symbol.Kind {
			case symbols.SymbolReceiver:
				kindLabel = "receiver"
			case symbols.SymbolParameter:
				kindLabel = "parameter"
			default:
				kindLabel = "variable"
			}
		} else {
			kindLabel = "reference"
		}

		symName := "value"
		if info.Symbol != nil {
			symName = info.Symbol.Name
		}

		diag := diagnostics.NewError(fmt.Sprintf("cannot modify through immutable %s '%s'", kindLabel, symName)).
			WithCode(diagnostics.ErrInvalidAssignment).
			WithPrimaryLabel(target.Loc(), "modification through immutable reference").
			WithHelp("use a mutable reference '&mut' to allow modification")
		if declLoc != nil {
			diag = diag.WithSecondaryLabel(declLoc, fmt.Sprintf("%s declared as immutable reference '&' here", kindLabel))
		}
		ctx.Diagnostics.Add(diag)
		return true

	case MutabilityValueReceiver:
		var declLoc *source.Location
		if info.Symbol != nil && info.Symbol.Decl != nil {
			declLoc = info.Symbol.Decl.Loc()
		}
		diag := diagnostics.NewWarning(fmt.Sprintf("modifying value receiver '%s' does not affect the caller", info.Symbol.Name)).
			WithCode(diagnostics.WarnValueReceiverMutation).
			WithPrimaryLabel(target.Loc(), "value receiver is a copy").
			WithHelp("use a '&mut' receiver to mutate the original value")
		if declLoc != nil {
			diag = diag.WithSecondaryLabel(declLoc, "receiver declared here")
		}
		ctx.Diagnostics.Add(diag)
		return false // Warning only, mutation is technically allowed
	}

	return false
}

// findValueReceiverInChain finds a value (non-reference) receiver in the expression chain.
func findValueReceiverInChain(mod *context_v2.Module, expr ast.Expression) *symbols.Symbol {
	if mod == nil || mod.CurrentScope == nil || expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *ast.IdentifierExpr:
		if sym, found := mod.CurrentScope.Lookup(e.Name); found && sym.Kind == symbols.SymbolReceiver {
			// Check if it's a value receiver (not a reference)
			if _, ok := sym.Type.(*types.ReferenceType); !ok {
				return sym
			}
		}
		return nil
	case *ast.SelectorExpr:
		return findValueReceiverInChain(mod, e.X)
	case *ast.IndexExpr:
		return findValueReceiverInChain(mod, e.X)
	case *ast.ParenExpr:
		return findValueReceiverInChain(mod, e.X)
	}
	return nil
}

// findImmutableRefInChain traverses an expression chain (e.g., p.X, arr[i].field)
// and returns the first immutable reference found, along with its symbol and location.
// Returns nil if no immutable reference is found.
func findImmutableRefInChain(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr ast.Expression) (*symbols.Symbol, *source.Location) {
	if ctx == nil || mod == nil || expr == nil {
		return nil, nil
	}

	switch e := expr.(type) {
	case *ast.IdentifierExpr:
		// Check if this identifier is an immutable reference
		if sym, found := mod.CurrentScope.Lookup(e.Name); found {
			if refType, ok := sym.Type.(*types.ReferenceType); ok && !refType.Mutable {
				return sym, e.Loc()
			}
		}
		return nil, nil
	case *ast.ScopeResolutionExpr:
		if sym, ok := resolveScopeResolutionSymbol(ctx, mod, e); ok && sym != nil {
			if refType, ok := sym.Type.(*types.ReferenceType); ok && !refType.Mutable {
				return sym, e.Loc()
			}
		}
		return nil, nil

	case *ast.SelectorExpr:
		// Check base expression first
		if sym, loc := findImmutableRefInChain(ctx, mod, e.X); sym != nil {
			return sym, loc
		}
		// Check the base type itself
		baseType := inferExprType(ctx, mod, e.X)
		if refType, ok := types.UnwrapType(baseType).(*types.ReferenceType); ok && !refType.Mutable {
			// The base expression is an immutable reference
			// Return nil sym but with location - we'll get the sym from further traversal
			return nil, nil
		}
		return nil, nil

	case *ast.IndexExpr:
		// Check base expression
		return findImmutableRefInChain(ctx, mod, e.X)

	case *ast.ParenExpr:
		return findImmutableRefInChain(ctx, mod, e.X)

	default:
		return nil, nil
	}
}
