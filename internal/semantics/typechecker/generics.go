package typechecker

import (
	"compiler/internal/context_v2"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/semantics/symbols"
	"compiler/internal/tokens"
	"compiler/internal/types"
	"fmt"
	"hash/fnv"
	"strings"
)

type genericCallableInfo struct {
	TypeParams       []*ast.TypeParam
	FuncType         *types.FunctionType
	Decl             *ast.FuncDecl
	DefModule        *context_v2.Module
	IsMethod         bool
	MethodInfo       *symbols.MethodInfo
	TypeSym          *symbols.Symbol
	ReceiverTypeName string
}

func resolveGenericCallable(ctx *context_v2.CompilerContext, mod *context_v2.Module, fun ast.Expression) (genericCallableInfo, bool) {
	if mod == nil || fun == nil {
		return genericCallableInfo{}, false
	}

	switch target := fun.(type) {
	case *ast.IdentifierExpr:
		sym, ok := mod.CurrentScope.Lookup(target.Name)
		if !ok || sym == nil {
			return genericCallableInfo{}, false
		}
		decl, ok := sym.Decl.(*ast.FuncDecl)
		if !ok || decl == nil || len(decl.TypeParams) == 0 {
			return genericCallableInfo{}, false
		}
		return genericCallableInfo{
			TypeParams: decl.TypeParams,
			FuncType:   functionTypeFromSemType(sym.Type),
			Decl:       decl,
			DefModule:  mod,
		}, true

	case *ast.ScopeResolutionExpr:
		sym, ok := resolveScopeResolutionSymbol(ctx, mod, target)
		if !ok || sym == nil {
			return genericCallableInfo{}, false
		}
		decl, ok := sym.Decl.(*ast.FuncDecl)
		if !ok || decl == nil || len(decl.TypeParams) == 0 {
			return genericCallableInfo{}, false
		}
		return genericCallableInfo{
			TypeParams: decl.TypeParams,
			FuncType:   functionTypeFromSemType(sym.Type),
			Decl:       decl,
			DefModule:  resolveScopeResolutionModule(ctx, mod, target),
		}, true

	case *ast.SelectorExpr:
		baseType := inferExprType(ctx, mod, target.X)
		baseType = autoDerefBaseType(target, baseType)
		named, ok := baseType.(*types.NamedType)
		if !ok {
			return genericCallableInfo{}, false
		}
		typeSym, defMod, found := lookupTypeSymbolWithModule(ctx, mod, named.Name)
		if !found || typeSym == nil || typeSym.Methods == nil {
			return genericCallableInfo{}, false
		}
		method, ok := typeSym.Methods[target.Field.Name]
		if !ok || method == nil || method.Decl == nil || len(method.Decl.TypeParams) == 0 {
			return genericCallableInfo{}, false
		}
		return genericCallableInfo{
			TypeParams:       method.Decl.TypeParams,
			FuncType:         method.FuncType,
			IsMethod:         true,
			MethodInfo:       method,
			TypeSym:          typeSym,
			DefModule:        defMod,
			ReceiverTypeName: named.Name,
		}, true
	}

	return genericCallableInfo{}, false
}

func resolveScopeResolutionModule(ctx *context_v2.CompilerContext, mod *context_v2.Module, expr *ast.ScopeResolutionExpr) *context_v2.Module {
	if ctx == nil || mod == nil || expr == nil {
		return nil
	}
	ident, ok := expr.X.(*ast.IdentifierExpr)
	if !ok || ident == nil || ident.Name == "" {
		return nil
	}
	importPath, ok := mod.ImportAliasMap[ident.Name]
	if !ok || importPath == "" {
		return nil
	}
	importedMod, ok := ctx.GetModule(importPath)
	if !ok {
		return nil
	}
	return importedMod
}

func lookupTypeSymbolWithModule(ctx *context_v2.CompilerContext, mod *context_v2.Module, typeName string) (*symbols.Symbol, *context_v2.Module, bool) {
	if mod == nil || typeName == "" {
		return nil, nil, false
	}
	if sym, found := mod.ModuleScope.Lookup(typeName); found {
		return sym, mod, true
	}
	if ctx == nil {
		return nil, nil, false
	}
	for _, importPath := range mod.ImportAliasMap {
		importedMod, exists := ctx.GetModule(importPath)
		if !exists || importedMod == nil || importedMod.ModuleScope == nil {
			continue
		}
		if sym, ok := importedMod.ModuleScope.GetSymbol(typeName); ok && sym.Kind == symbols.SymbolType {
			return sym, importedMod, true
		}
	}
	return nil, nil, false
}

func functionTypeFromSemType(typ types.SemType) *types.FunctionType {
	if typ == nil || typ.Equals(types.TypeUnknown) {
		return nil
	}
	if fn, ok := types.UnwrapType(typ).(*types.FunctionType); ok {
		return fn
	}
	return nil
}

type genericCallInstantiation struct {
	FuncType *types.FunctionType
	TypeArgs []types.SemType
}

func instantiateGenericCallFuncType(
	ctx *context_v2.CompilerContext,
	mod *context_v2.Module,
	call *ast.CallExpr,
	args []ast.Expression,
	baseFuncType *types.FunctionType,
	typeParams []*ast.TypeParam,
	reportErrors bool,
) (genericCallInstantiation, bool) {
	if call == nil || baseFuncType == nil || len(typeParams) == 0 {
		return genericCallInstantiation{}, false
	}

	typeMap := make(map[string]types.SemType, len(typeParams))
	explicitMap := make(map[string]bool, len(typeParams))

	// Explicit type arguments (if present) always take priority.
	if len(call.TypeArgs) > 0 {
		if len(call.TypeArgs) != len(typeParams) {
			if reportErrors && ctx != nil {
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("generic call expects %d type argument(s), got %d", len(typeParams), len(call.TypeArgs))).
						WithCode(diagnostics.ErrTypeMismatch).
						WithPrimaryLabel(call.Loc(), "adjust the number of type arguments"),
				)
			}
			return genericCallInstantiation{}, false
		}
		for i, typeArg := range call.TypeArgs {
			semType := TypeFromTypeNodeWithContext(ctx, mod, typeArg)
			if semType == nil || semType.Equals(types.TypeUnknown) {
				return genericCallInstantiation{}, false
			}
			if typeParams[i] == nil || typeParams[i].Name == nil {
				continue
			}
			name := typeParams[i].Name.Name
			typeMap[name] = semType
			explicitMap[name] = true
		}
	}

	// Infer remaining type arguments from call arguments.
	for i, arg := range args {
		if _, isSpread := arg.(*ast.SpreadExpr); isSpread {
			continue
		}
		paramType := genericParamTypeAt(baseFuncType, i)
		if paramType == nil {
			continue
		}
		argType := ResolvedExprType(ctx, mod, arg)
		if argType == nil || argType.Equals(types.TypeUnknown) {
			argType = inferExprType(ctx, mod, arg)
		}
		if !bindTypeParamFromTypes(paramType, argType, typeMap, explicitMap) {
			if reportErrors && ctx != nil {
				ctx.Diagnostics.Add(
					diagnostics.NewError("could not infer a consistent generic type from call arguments").
						WithCode(diagnostics.ErrTypeMismatch).
						WithPrimaryLabel(arg.Loc(), "argument conflicts with inferred type parameter"),
				)
			}
			return genericCallInstantiation{}, false
		}
	}

	missing := make([]string, 0)
	for _, typeParam := range typeParams {
		if typeParam == nil || typeParam.Name == nil {
			continue
		}
		name := typeParam.Name.Name
		if _, ok := typeMap[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		if reportErrors && ctx != nil {
			ctx.Diagnostics.Add(
				diagnostics.NewError(fmt.Sprintf("cannot infer type argument(s): %s", strings.Join(missing, ", "))).
					WithCode(diagnostics.ErrTypeMismatch).
					WithPrimaryLabel(call.Loc(), "provide explicit type arguments"),
			)
		}
		return genericCallInstantiation{}, false
	}

	for _, typeParam := range typeParams {
		if typeParam == nil || typeParam.Name == nil || typeParam.Constraint == nil {
			continue
		}
		name := typeParam.Name.Name
		actualType := typeMap[name]
		if !satisfiesConstraintExpr(ctx, mod, actualType, typeParam.Constraint, map[string]bool{}) {
			if reportErrors && ctx != nil {
				ctx.Diagnostics.Add(
					diagnostics.NewError(fmt.Sprintf("type argument '%s' does not satisfy constraint for '%s'", actualType.String(), name)).
						WithCode(diagnostics.ErrTypeMismatch).
						WithPrimaryLabel(call.Loc(), "constraint not satisfied"),
				)
			}
			return genericCallInstantiation{}, false
		}
	}

	orderedTypeArgs := make([]types.SemType, 0, len(typeParams))
	for _, typeParam := range typeParams {
		if typeParam == nil || typeParam.Name == nil {
			continue
		}
		concrete, ok := typeMap[typeParam.Name.Name]
		if !ok || concrete == nil {
			continue
		}
		orderedTypeArgs = append(orderedTypeArgs, concrete)
	}

	return genericCallInstantiation{
		FuncType: instantiateFunctionType(baseFuncType, typeMap),
		TypeArgs: orderedTypeArgs,
	}, true
}

func mangleGenericFunctionName(baseName string, typeArgs []types.SemType) string {
	if baseName == "" {
		return ""
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(baseName))
	for _, arg := range typeArgs {
		_, _ = h.Write([]byte("|"))
		if arg == nil {
			_, _ = h.Write([]byte("<nil>"))
			continue
		}
		_, _ = h.Write([]byte(arg.String()))
	}
	return fmt.Sprintf("__gen_%s_%x", baseName, h.Sum64())
}

func mangleGenericMethodName(receiverTypeName, methodName string, typeArgs []types.SemType) string {
	if methodName == "" {
		return ""
	}
	h := fnv.New64a()
	if receiverTypeName != "" {
		_, _ = h.Write([]byte(receiverTypeName))
		_, _ = h.Write([]byte("|"))
	}
	_, _ = h.Write([]byte(methodName))
	for _, arg := range typeArgs {
		_, _ = h.Write([]byte("|"))
		if arg == nil {
			_, _ = h.Write([]byte("<nil>"))
			continue
		}
		_, _ = h.Write([]byte(arg.String()))
	}
	return fmt.Sprintf("__genm_%s_%x", methodName, h.Sum64())
}

func genericParamTypeAt(funcType *types.FunctionType, index int) types.SemType {
	if funcType == nil || index < 0 || len(funcType.Params) == 0 {
		return nil
	}
	if index < len(funcType.Params) {
		return funcType.Params[index].Type
	}
	last := funcType.Params[len(funcType.Params)-1]
	if last.IsVariadic {
		return last.Type
	}
	return nil
}

func bindTypeParamFromTypes(
	paramType, argType types.SemType,
	typeMap map[string]types.SemType,
	explicitMap map[string]bool,
) bool {
	if paramType == nil || argType == nil || argType.Equals(types.TypeUnknown) {
		return true
	}

	if typeParam, ok := paramType.(*types.TypeParam); ok {
		return bindTypeParam(typeParam.Name, argType, typeMap, explicitMap)
	}

	paramUnwrapped := types.UnwrapType(paramType)
	argUnwrapped := types.UnwrapType(argType)

	if typeParam, ok := paramUnwrapped.(*types.TypeParam); ok {
		return bindTypeParam(typeParam.Name, argType, typeMap, explicitMap)
	}

	switch p := paramUnwrapped.(type) {
	case *types.ArrayType:
		a, ok := argUnwrapped.(*types.ArrayType)
		if !ok {
			return true
		}
		return bindTypeParamFromTypes(p.Element, a.Element, typeMap, explicitMap)
	case *types.OptionalType:
		a, ok := argUnwrapped.(*types.OptionalType)
		if !ok {
			return true
		}
		return bindTypeParamFromTypes(p.Inner, a.Inner, typeMap, explicitMap)
	case *types.ReferenceType:
		a, ok := argUnwrapped.(*types.ReferenceType)
		if !ok {
			return true
		}
		if p.Mutable != a.Mutable {
			return true
		}
		return bindTypeParamFromTypes(p.Inner, a.Inner, typeMap, explicitMap)
	case *types.HeapType:
		a, ok := argUnwrapped.(*types.HeapType)
		if !ok {
			return true
		}
		return bindTypeParamFromTypes(p.Inner, a.Inner, typeMap, explicitMap)
	case *types.ResultType:
		a, ok := argUnwrapped.(*types.ResultType)
		if !ok {
			return true
		}
		return bindTypeParamFromTypes(p.Ok, a.Ok, typeMap, explicitMap) &&
			bindTypeParamFromTypes(p.Err, a.Err, typeMap, explicitMap)
	case *types.MapType:
		a, ok := argUnwrapped.(*types.MapType)
		if !ok {
			return true
		}
		return bindTypeParamFromTypes(p.Key, a.Key, typeMap, explicitMap) &&
			bindTypeParamFromTypes(p.Value, a.Value, typeMap, explicitMap)
	case *types.FunctionType:
		a, ok := argUnwrapped.(*types.FunctionType)
		if !ok {
			return true
		}
		if len(p.Params) != len(a.Params) {
			return true
		}
		for i := range p.Params {
			if !bindTypeParamFromTypes(p.Params[i].Type, a.Params[i].Type, typeMap, explicitMap) {
				return false
			}
		}
		return bindTypeParamFromTypes(p.Return, a.Return, typeMap, explicitMap)
	}

	return true
}

func bindTypeParam(
	name string,
	candidate types.SemType,
	typeMap map[string]types.SemType,
	explicitMap map[string]bool,
) bool {
	if name == "" {
		return true
	}
	if existing, ok := typeMap[name]; ok {
		if existing.Equals(candidate) {
			return true
		}
		if !explicitMap[name] {
			return false
		}
		if checkTypeCompatibility(existing, candidate) != Incompatible {
			return true
		}
		if checkTypeCompatibility(candidate, existing) != Incompatible {
			return true
		}
		return false
	}
	typeMap[name] = candidate
	return true
}

func instantiateFunctionType(fn *types.FunctionType, typeMap map[string]types.SemType) *types.FunctionType {
	if fn == nil {
		return nil
	}
	params := make([]types.ParamType, len(fn.Params))
	for i, param := range fn.Params {
		params[i] = param
		params[i].Type = instantiateSemType(param.Type, typeMap)
	}
	return types.NewFunction(params, instantiateSemType(fn.Return, typeMap))
}

func instantiateSemType(typ types.SemType, typeMap map[string]types.SemType) types.SemType {
	if typ == nil {
		return types.TypeUnknown
	}

	if typeParam, ok := typ.(*types.TypeParam); ok {
		if concrete, exists := typeMap[typeParam.Name]; exists {
			return concrete
		}
		return typeParam
	}

	switch t := typ.(type) {
	case *types.ArrayType:
		return types.NewArray(instantiateSemType(t.Element, typeMap), t.Length)
	case *types.OptionalType:
		return types.NewOptional(instantiateSemType(t.Inner, typeMap))
	case *types.ReferenceType:
		if t.Mutable {
			return types.NewMutableReference(instantiateSemType(t.Inner, typeMap))
		}
		return types.NewReference(instantiateSemType(t.Inner, typeMap))
	case *types.HeapType:
		return types.NewHeap(instantiateSemType(t.Inner, typeMap))
	case *types.ResultType:
		return types.NewResult(instantiateSemType(t.Ok, typeMap), instantiateSemType(t.Err, typeMap))
	case *types.MapType:
		return types.NewMap(instantiateSemType(t.Key, typeMap), instantiateSemType(t.Value, typeMap))
	case *types.UnionType:
		variants := make([]types.SemType, len(t.Variants))
		for i, v := range t.Variants {
			variants[i] = instantiateSemType(v, typeMap)
		}
		return types.NewUnion(variants)
	case *types.StructType:
		fields := make([]types.StructField, len(t.Fields))
		for i, f := range t.Fields {
			fields[i] = types.StructField{
				Name: f.Name,
				Type: instantiateSemType(f.Type, typeMap),
			}
		}
		st := types.NewStruct("", fields)
		st.ID = t.ID
		return st
	case *types.FunctionType:
		return instantiateFunctionType(t, typeMap)
	case *types.NamedType:
		if t.Resource {
			return types.NewResourceNamed(t.Name, instantiateSemType(t.Underlying, typeMap))
		}
		return types.NewNamed(t.Name, instantiateSemType(t.Underlying, typeMap))
	default:
		return typ
	}
}

func satisfiesConstraintExpr(
	ctx *context_v2.CompilerContext,
	mod *context_v2.Module,
	actualType types.SemType,
	expr ast.ConstraintExpr,
	visited map[string]bool,
) bool {
	if expr == nil || actualType == nil || actualType.Equals(types.TypeUnknown) {
		return true
	}

	switch c := expr.(type) {
	case *ast.ConstraintBinaryExpr:
		if c.Op != tokens.BIT_AND_TOKEN {
			return false
		}
		return satisfiesConstraintExpr(ctx, mod, actualType, c.Left, visited) &&
			satisfiesConstraintExpr(ctx, mod, actualType, c.Right, visited)

	case *ast.ConstraintUnionExpr:
		for _, term := range c.Terms {
			if satisfiesConstraintExpr(ctx, mod, actualType, term, visited) {
				return true
			}
		}
		return false

	case *ast.ConstraintTypeTerm:
		if c.Type == nil {
			return false
		}

		// Named constraint references: recurse into the referenced declaration.
		if sym, ok := resolveConstraintNamedSymbol(ctx, mod, c.Type); ok && sym != nil && sym.Kind == symbols.SymbolConstraint {
			if visited[sym.Name] {
				return true
			}
			visited[sym.Name] = true
			constraintDecl, ok := sym.Decl.(*ast.ConstraintDecl)
			if !ok || constraintDecl == nil {
				return false
			}
			return satisfiesConstraintExpr(ctx, mod, actualType, constraintDecl.Expr, visited)
		}

		targetType := TypeFromTypeNodeWithContext(ctx, mod, c.Type)
		if targetType == nil || targetType.Equals(types.TypeUnknown) {
			return false
		}

		if c.Approx {
			return approxConstraintMatch(actualType, targetType)
		}

		// Non-approximate constraint terms are exact type-set membership checks,
		// except interfaces which use implementation compatibility.
		targetUnwrapped := types.UnwrapType(targetType)
		if _, isIface := targetUnwrapped.(*types.InterfaceType); isIface {
			return checkTypeCompatibilityWithContext(ctx, mod, actualType, targetType) != Incompatible
		}

		return actualType.Equals(targetType)
	}

	return false
}

func approxConstraintMatch(actual, target types.SemType) bool {
	if actual == nil || target == nil {
		return false
	}
	actual = types.UnwrapType(actual)
	target = types.UnwrapType(target)

	if named, ok := actual.(*types.NamedType); ok {
		actual = types.UnwrapType(named.Underlying)
	}
	if named, ok := target.(*types.NamedType); ok {
		target = types.UnwrapType(named.Underlying)
	}
	return actual.Equals(target)
}
