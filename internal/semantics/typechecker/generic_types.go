package typechecker

import (
	"compiler/internal/context_v2"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/semantics/symbols"
	"compiler/internal/types"
	"fmt"
	"hash/fnv"
)

type genericNamedTypeInfo struct {
	Name      string
	Decl      *ast.TypeDecl
	Named     *types.NamedType
	DefModule *context_v2.Module
}

func instantiateAppliedNamedType(
	ctx *context_v2.CompilerContext,
	mod *context_v2.Module,
	applied *ast.AppliedType,
) types.SemType {
	if applied == nil {
		return types.TypeUnknown
	}

	info, ok := resolveGenericNamedType(ctx, mod, applied.Base)
	if !ok || info.Decl == nil || info.Named == nil {
		// If the base resolves to a type but it is not generic, give a targeted error.
		baseType := TypeFromTypeNodeWithContext(ctx, mod, applied.Base)
		if baseType != nil && !baseType.Equals(types.TypeUnknown) && ctx != nil {
			ctx.Diagnostics.Add(
				diagnostics.NewError("type arguments are only valid for generic named types").
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(applied.Loc(), "remove `<...>` or use a generic named type"),
			)
		}
		return types.TypeUnknown
	}

	typeParams := info.Decl.TypeParams
	if len(applied.Args) != len(typeParams) {
		if ctx != nil {
			ctx.Diagnostics.Add(
				diagnostics.NewError(
					fmt.Sprintf("generic type '%s' expects %d type argument(s), got %d", info.Name, len(typeParams), len(applied.Args)),
				).
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(applied.Loc(), "adjust the number of type arguments"),
			)
		}
		return types.TypeUnknown
	}

	typeMap := make(map[string]types.SemType, len(typeParams))
	orderedTypeArgs := make([]types.SemType, 0, len(typeParams))
	for i, argNode := range applied.Args {
		argType := TypeFromTypeNodeWithContext(ctx, mod, argNode)
		if argType == nil || argType.Equals(types.TypeUnknown) {
			return types.TypeUnknown
		}
		if typeParams[i] == nil || typeParams[i].Name == nil {
			continue
		}
		name := typeParams[i].Name.Name
		typeMap[name] = argType
		orderedTypeArgs = append(orderedTypeArgs, argType)
	}

	constraintMod := info.DefModule
	if constraintMod == nil {
		constraintMod = mod
	}
	for _, typeParam := range typeParams {
		if typeParam == nil || typeParam.Name == nil || typeParam.Constraint == nil {
			continue
		}
		name := typeParam.Name.Name
		actualType, ok := typeMap[name]
		if !ok || actualType == nil || actualType.Equals(types.TypeUnknown) {
			return types.TypeUnknown
		}
		if !satisfiesConstraintExpr(ctx, constraintMod, actualType, typeParam.Constraint, map[string]bool{}) {
			if ctx != nil {
				ctx.Diagnostics.Add(
					diagnostics.NewError(
						fmt.Sprintf("type argument '%s' does not satisfy constraint for '%s'", actualType.String(), name),
					).
						WithCode(diagnostics.ErrTypeMismatch).
						WithPrimaryLabel(applied.Loc(), "constraint not satisfied"),
				)
			}
			return types.TypeUnknown
		}
	}

	underlying := instantiateSemType(info.Named.Underlying, typeMap)
	if underlying == nil || underlying.Equals(types.TypeUnknown) {
		return types.TypeUnknown
	}

	instName := mangleGenericTypeName(info.DefModule, info.Name, orderedTypeArgs)
	if instName == "" {
		instName = info.Name
	}
	if info.Named.Resource {
		return types.NewResourceNamed(instName, underlying)
	}
	return types.NewNamed(instName, underlying)
}

func resolveGenericNamedType(
	ctx *context_v2.CompilerContext,
	mod *context_v2.Module,
	base ast.TypeNode,
) (genericNamedTypeInfo, bool) {
	if mod == nil || base == nil {
		return genericNamedTypeInfo{}, false
	}

	switch b := base.(type) {
	case *ast.IdentifierExpr:
		sym, ok := mod.CurrentScope.Lookup(b.Name)
		if !ok || sym == nil || sym.Kind != symbols.SymbolType {
			return genericNamedTypeInfo{}, false
		}
		decl, ok := sym.Decl.(*ast.TypeDecl)
		if !ok || decl == nil || len(decl.TypeParams) == 0 {
			return genericNamedTypeInfo{}, false
		}
		named, ok := sym.Type.(*types.NamedType)
		if !ok || named == nil {
			return genericNamedTypeInfo{}, false
		}
		return genericNamedTypeInfo{
			Name:      b.Name,
			Decl:      decl,
			Named:     named,
			DefModule: mod,
		}, true

	case *ast.ScopeResolutionExpr:
		if ctx == nil {
			return genericNamedTypeInfo{}, false
		}
		moduleIdent, ok := b.X.(*ast.IdentifierExpr)
		if !ok || moduleIdent == nil || b.Selector == nil {
			return genericNamedTypeInfo{}, false
		}
		importPath, ok := mod.ImportAliasMap[moduleIdent.Name]
		if !ok || importPath == "" {
			return genericNamedTypeInfo{}, false
		}
		defMod, ok := ctx.GetModule(importPath)
		if !ok || defMod == nil || defMod.ModuleScope == nil {
			return genericNamedTypeInfo{}, false
		}
		sym, ok := defMod.ModuleScope.GetSymbol(b.Selector.Name)
		if !ok || sym == nil || sym.Kind != symbols.SymbolType {
			return genericNamedTypeInfo{}, false
		}
		decl, ok := sym.Decl.(*ast.TypeDecl)
		if !ok || decl == nil || len(decl.TypeParams) == 0 {
			return genericNamedTypeInfo{}, false
		}
		named, ok := sym.Type.(*types.NamedType)
		if !ok || named == nil {
			return genericNamedTypeInfo{}, false
		}
		return genericNamedTypeInfo{
			Name:      b.Selector.Name,
			Decl:      decl,
			Named:     named,
			DefModule: defMod,
		}, true
	}

	return genericNamedTypeInfo{}, false
}

func mangleGenericTypeName(defMod *context_v2.Module, baseName string, typeArgs []types.SemType) string {
	if baseName == "" {
		return ""
	}
	h := fnv.New64a()
	if defMod != nil && defMod.ImportPath != "" {
		_, _ = h.Write([]byte(defMod.ImportPath))
		_, _ = h.Write([]byte("|"))
	}
	_, _ = h.Write([]byte(baseName))
	for _, arg := range typeArgs {
		_, _ = h.Write([]byte("|"))
		if arg == nil {
			_, _ = h.Write([]byte("<nil>"))
			continue
		}
		_, _ = h.Write([]byte(arg.String()))
	}
	return fmt.Sprintf("__gentype_%s_%x", baseName, h.Sum64())
}
