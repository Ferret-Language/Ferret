package typechecker

import (
	"compiler/internal/context_v2"
	"compiler/internal/frontend/ast"
	"compiler/internal/semantics/symbols"
	"compiler/internal/semantics/table"
	"compiler/internal/types"
)

// PrepareGenericFunctionInstantiation re-checks a generic function body with
// concrete type arguments so downstream lowering sees concrete expression types.
func PrepareGenericFunctionInstantiation(
	ctx *context_v2.CompilerContext,
	mod *context_v2.Module,
	inst *context_v2.GenericFunctionInstantiation,
) bool {
	if ctx == nil || mod == nil || inst == nil || inst.Decl == nil {
		return false
	}
	decl := inst.Decl
	if decl.Scope == nil || decl.Type == nil {
		return false
	}
	funcScope, ok := decl.Scope.(*table.SymbolTable)
	if !ok || funcScope == nil {
		return false
	}
	if len(decl.TypeParams) != len(inst.TypeArgs) {
		return false
	}

	if !bindInstantiatedTypeParams(funcScope, decl.TypeParams, inst.TypeArgs) {
		return false
	}

	defer setupFunctionContext(ctx, mod, funcScope, decl.Type)()

	addParamsToScope(ctx, mod, funcScope, decl.Type.Params)
	checkDefaultParameterValues(ctx, mod, decl.Type.Params)

	if decl.Body != nil {
		checkBlock(ctx, mod, decl.Body)
	}

	return true
}

// PrepareGenericMethodInstantiation re-checks a generic method body with concrete
// type arguments so downstream lowering sees concrete expression types.
func PrepareGenericMethodInstantiation(
	ctx *context_v2.CompilerContext,
	mod *context_v2.Module,
	inst *context_v2.GenericMethodInstantiation,
) bool {
	if ctx == nil || mod == nil || inst == nil || inst.Decl == nil {
		return false
	}
	decl := inst.Decl
	if decl.Scope == nil || decl.Type == nil {
		return false
	}
	methodScope, ok := decl.Scope.(*table.SymbolTable)
	if !ok || methodScope == nil {
		return false
	}
	if len(decl.TypeParams) != len(inst.TypeArgs) {
		return false
	}

	if !bindInstantiatedTypeParams(methodScope, decl.TypeParams, inst.TypeArgs) {
		return false
	}

	defer setupFunctionContext(ctx, mod, methodScope, decl.Type)()

	if decl.Receiver != nil && decl.Receiver.Name != nil {
		receiverSym, ok := methodScope.GetSymbol(decl.Receiver.Name.Name)
		if ok && decl.Receiver.Type != nil {
			receiverType := TypeFromTypeNodeWithContext(ctx, mod, decl.Receiver.Type)
			receiverSym.Type = receiverType
		}
	}

	addParamsToScope(ctx, mod, methodScope, decl.Type.Params)
	checkDefaultParameterValues(ctx, mod, decl.Type.Params)

	if decl.Body != nil {
		checkBlock(ctx, mod, decl.Body)
	}

	return true
}

func bindInstantiatedTypeParams(scope *table.SymbolTable, typeParams []*ast.TypeParam, typeArgs []types.SemType) bool {
	if scope == nil {
		return false
	}
	if len(typeParams) != len(typeArgs) {
		return false
	}

	for i, typeParam := range typeParams {
		if typeParam == nil || typeParam.Name == nil || typeParam.Name.Name == "" {
			return false
		}
		sym, ok := scope.GetSymbol(typeParam.Name.Name)
		if !ok || sym == nil || sym.Kind != symbols.SymbolTypeParameter {
			return false
		}
		concrete := typeArgs[i]
		if concrete == nil {
			concrete = types.TypeUnknown
		}
		sym.Type = concrete
	}

	return true
}
