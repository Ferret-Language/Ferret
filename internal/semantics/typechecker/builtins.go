package typechecker

import (
	"compiler/internal/context_v2"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/tokens"
	"compiler/internal/types"
	"fmt"
)

func builtinCallName(mod *context_v2.Module, expr *ast.CallExpr) (string, bool) {
	if expr == nil {
		return "", false
	}
	ident, ok := expr.Fun.(*ast.IdentifierExpr)
	if !ok {
		return "", false
	}
	if mod == nil || mod.CurrentScope == nil {
		return "", false
	}
	sym, ok := mod.CurrentScope.Lookup(ident.Name)
	if !ok || sym == nil || !sym.IsBuiltin {
		return "", false
	}
	if sym.BuiltinName != "" {
		return sym.BuiltinName, true
	}
	return ident.Name, true
}

func inferBuiltinCallType(name string) types.SemType {
	switch name {
	case "len":
		return types.TypeI32
	case "append":
		return types.TypeBool
	case "self_addr", "addr", "heap_addr":
		return types.TypeU64
	default:
		return types.TypeUnknown
	}
}

func checkBuiltinCallExpr(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.CallExpr, name string) {
	if expr == nil {
		return
	}
	switch name {
	case "len":
		checkBuiltinLen(ctx, mod, expr)
	case "append":
		checkBuiltinAppend(ctx, mod, expr)
	case "self_addr":
		checkBuiltinSelfAddr(ctx, mod, expr)
	case "addr":
		checkBuiltinAddr(ctx, mod, expr)
	case "heap_addr":
		checkBuiltinHeapAddr(ctx, mod, expr)
	default:
		return
	}
}

func checkBuiltinLen(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.CallExpr) {
	argCount := len(expr.Args)
	if argCount != 1 {
		ctx.Diagnostics.Add(
			diagnostics.WrongArgumentCount(mod.FilePath, expr.Loc(), 1, argCount),
		)
	}
	if argCount == 0 {
		reportBuiltinInvalidCatch(ctx, mod, expr, types.TypeI32)
		return
	}

	argType := checkExpr(ctx, mod, expr.Args[0], types.TypeUnknown)
	if argType != nil && !argType.Equals(types.TypeUnknown) {
		if _, ok := types.UnwrapType(argType).(*types.ReferenceType); !ok {
			ctx.Diagnostics.Add(
				diagnostics.NewError("len requires a reference").
					WithCode(diagnostics.ErrInvalidAssignment).
					WithPrimaryLabel(expr.Args[0].Loc(), "expected a reference value").
					WithHelp("use '&' to pass a reference"),
			)
		}
	}
	baseType := builtinArgBaseType(argType)
	if baseType != nil && !baseType.Equals(types.TypeUnknown) {
		if _, ok := baseType.(*types.ArrayType); ok {
			// ok
		} else if _, ok := baseType.(*types.MapType); ok {
			// ok
		} else if prim, ok := types.UnwrapType(baseType).(*types.PrimitiveType); ok && prim.GetName() == types.TYPE_STRING {
			// ok
		} else {
			ctx.Diagnostics.Add(
				diagnostics.NewError("len expects an array, map, or string").
					WithCode(diagnostics.ErrInvalidType).
					WithPrimaryLabel(expr.Args[0].Loc(), fmt.Sprintf("found %s", baseType.String())),
			)
		}
	}

	for i := 1; i < argCount; i++ {
		checkExpr(ctx, mod, expr.Args[i], types.TypeUnknown)
	}

	reportBuiltinInvalidCatch(ctx, mod, expr, types.TypeI32)
}

func checkBuiltinAppend(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.CallExpr) {
	argCount := len(expr.Args)
	if argCount != 2 {
		ctx.Diagnostics.Add(
			diagnostics.WrongArgumentCount(mod.FilePath, expr.Loc(), 2, argCount),
		)
	}

	elemType := types.TypeUnknown
	if argCount > 0 {
		targetExpr := unwrapBorrowTarget(expr.Args[0])
		arrType := checkExpr(ctx, mod, expr.Args[0], types.TypeUnknown)
		baseType := builtinArgBaseType(arrType)

		if baseType != nil && !baseType.Equals(types.TypeUnknown) {
			if arr, ok := baseType.(*types.ArrayType); ok && arr.Length < 0 {
				elemType = arr.Element
				refType, isRef := types.UnwrapType(arrType).(*types.ReferenceType)
				if !isRef {
					ctx.Diagnostics.Add(
						diagnostics.NewError("append requires a mutable reference").
							WithCode(diagnostics.ErrInvalidAssignment).
							WithPrimaryLabel(expr.Args[0].Loc(), "expected a mutable reference").
							WithHelp("use \"&mut\" to pass a mutable reference"),
					)
					elemType = types.TypeUnknown
				} else if !refType.Mutable {
					ctx.Diagnostics.Add(
						diagnostics.NewError("append requires a mutable reference").
							WithCode(diagnostics.ErrInvalidAssignment).
							WithPrimaryLabel(expr.Args[0].Loc(), "expected a mutable reference").
							WithHelp("use \"&mut\" to pass a mutable reference"),
					)
					elemType = types.TypeUnknown
				} else if targetExpr != nil && !isBorrowableTarget(ctx, mod, targetExpr) {
					ctx.Diagnostics.Add(
						diagnostics.NewError("append requires a mutable borrow of an addressable value").
							WithCode(diagnostics.ErrInvalidOperation).
							WithPrimaryLabel(expr.Args[0].Loc(), "not an addressable value").
							WithHelp("assign the array to a variable first, then append"),
					)
				} else if targetExpr != nil {
					if reportMutabilityError(ctx, checkMutability(ctx, mod, targetExpr), targetExpr) {
						elemType = types.TypeUnknown
					}
				}
			} else {
				ctx.Diagnostics.Add(
					diagnostics.NewError("append expects a dynamic array").
						WithCode(diagnostics.ErrInvalidType).
						WithPrimaryLabel(expr.Args[0].Loc(), fmt.Sprintf("found %s", baseType.String())),
				)
			}
		}
	}

	if argCount > 1 {
		valueType := checkExpr(ctx, mod, expr.Args[1], elemType)
		if isReferenceType(valueType) {
			ctx.Diagnostics.Add(
				diagnostics.NewError("append expects a value, not a reference").
					WithCode(diagnostics.ErrInvalidType).
					WithPrimaryLabel(expr.Args[1].Loc(), "remove '&' to pass the value directly"),
			)
		}
		if elemType != nil && !elemType.Equals(types.TypeUnknown) && valueType != nil {
			compatibility := checkTypeCompatibilityWithContext(ctx, mod, valueType, elemType)
			if !isImplicitlyCompatible(compatibility) {
				argTypeDesc := types.ResolveUntypedType(valueType, elemType)
				diag := diagnostics.ArgumentTypeMismatch(
					mod.FilePath,
					expr.Args[1].Loc(),
					"value",
					elemType.String(),
					argTypeDesc.String(),
				)
				diag = addExplicitCastHint(ctx, diag, elemType, compatibility, expr.Args[1])
				ctx.Diagnostics.Add(diag)
			}
		}
	}

	for i := 2; i < argCount; i++ {
		checkExpr(ctx, mod, expr.Args[i], types.TypeUnknown)
	}

	reportBuiltinInvalidCatch(ctx, mod, expr, types.TypeBool)
}

func checkBuiltinAddr(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.CallExpr) {
	argCount := len(expr.Args)
	if argCount != 1 {
		ctx.Diagnostics.Add(
			diagnostics.WrongArgumentCount(mod.FilePath, expr.Loc(), 1, argCount),
		)
	}
	if argCount == 0 {
		reportBuiltinInvalidCatch(ctx, mod, expr, types.TypeU64)
		return
	}

	arg := expr.Args[0]
	argType := checkExpr(ctx, mod, arg, types.TypeUnknown)
	if argType != nil && !argType.Equals(types.TypeUnknown) && !isReferenceType(argType) {
		ctx.Diagnostics.Add(
			diagnostics.NewError("addr expects a reference").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(arg.Loc(), "expected a reference value").
				WithHelp("use '&' to pass a reference"),
		)
	}

	for i := 1; i < argCount; i++ {
		checkExpr(ctx, mod, expr.Args[i], types.TypeUnknown)
	}

	reportBuiltinInvalidCatch(ctx, mod, expr, types.TypeU64)
}

func checkBuiltinSelfAddr(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.CallExpr) {
	argCount := len(expr.Args)
	if argCount != 1 {
		ctx.Diagnostics.Add(
			diagnostics.WrongArgumentCount(mod.FilePath, expr.Loc(), 1, argCount),
		)
	}
	if argCount == 0 {
		reportBuiltinInvalidCatch(ctx, mod, expr, types.TypeU64)
		return
	}

	arg := expr.Args[0]
	argType := checkExpr(ctx, mod, arg, types.TypeUnknown)
	if argType != nil && !argType.Equals(types.TypeUnknown) && !isReferenceType(argType) {
		ctx.Diagnostics.Add(
			diagnostics.NewError("self_addr expects a reference").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(arg.Loc(), "expected a reference value").
				WithHelp("use '&' to pass a reference"),
		)
	}

	for i := 1; i < argCount; i++ {
		checkExpr(ctx, mod, expr.Args[i], types.TypeUnknown)
	}

	reportBuiltinInvalidCatch(ctx, mod, expr, types.TypeU64)
}

func checkBuiltinHeapAddr(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.CallExpr) {
	argCount := len(expr.Args)
	if argCount != 1 {
		ctx.Diagnostics.Add(
			diagnostics.WrongArgumentCount(mod.FilePath, expr.Loc(), 1, argCount),
		)
	}
	if argCount == 0 {
		reportBuiltinInvalidCatch(ctx, mod, expr, types.TypeU64)
		return
	}

	arg := expr.Args[0]
	argType := checkExpr(ctx, mod, arg, types.TypeUnknown)
	if argType != nil && !argType.Equals(types.TypeUnknown) && !isReferenceType(argType) {
		ctx.Diagnostics.Add(
			diagnostics.NewError("heap_addr expects a reference").
				WithCode(diagnostics.ErrInvalidOperation).
				WithPrimaryLabel(arg.Loc(), "expected a reference value").
				WithHelp("use '&' to pass a reference"),
		)
	}

	for i := 1; i < argCount; i++ {
		checkExpr(ctx, mod, expr.Args[i], types.TypeUnknown)
	}

	reportBuiltinInvalidCatch(ctx, mod, expr, types.TypeU64)
}

func reportBuiltinInvalidCatch(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.CallExpr, retType types.SemType) {
	if expr.Catch == nil {
		return
	}
	ctx.Diagnostics.Add(
		diagnostics.InvalidCatch(mod.FilePath, expr.Catch.Loc(), retType.String()),
	)
}

func builtinArgBaseType(typ types.SemType) types.SemType {
	if typ == nil {
		return nil
	}
	base := types.UnwrapType(typ)
	base = dereferenceType(base)
	base = types.UnwrapType(base)
	return base
}

func unwrapBorrowTarget(expr ast.Expression) ast.Expression {
	if expr == nil {
		return nil
	}
	if unary, ok := expr.(*ast.UnaryExpr); ok {
		if unary.Op.Kind == tokens.BIT_AND_TOKEN || unary.Op.Kind == tokens.MUT_TOKEN {
			return unary.X
		}
	}
	return expr
}
