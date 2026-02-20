package typechecker

import (
	"compiler/internal/context_v2"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/semantics/symbols"
	"compiler/internal/semantics/table"
	"compiler/internal/source"
	"compiler/internal/types"
	"fmt"
	"strings"
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

	before := snapshotDiagnostics(ctx)
	if decl.Body != nil {
		checkBlock(ctx, mod, decl.Body)
	}
	annotateGenericInstantiationDiagnostics(
		ctx,
		before,
		inst.CallSite,
		"instantiated from this call",
		formatTypeArgBindings(decl.TypeParams, inst.TypeArgs),
	)

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

	declareReceiverGenericTypeParams(ctx, mod, methodScope, decl.Receiver.Type)
	bindReceiverInstantiatedTypeParams(ctx, mod, methodScope, decl.Receiver.Type, inst.ReceiverTypeName)
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

	before := snapshotDiagnostics(ctx)
	if decl.Body != nil {
		checkBlock(ctx, mod, decl.Body)
	}
	annotateGenericInstantiationDiagnostics(
		ctx,
		before,
		inst.CallSite,
		"instantiated from this call",
		formatTypeArgBindings(decl.TypeParams, inst.TypeArgs),
	)

	return true
}

func snapshotDiagnostics(ctx *context_v2.CompilerContext) map[*diagnostics.Diagnostic]struct{} {
	seen := make(map[*diagnostics.Diagnostic]struct{})
	if ctx == nil || ctx.Diagnostics == nil {
		return seen
	}
	for _, diag := range ctx.Diagnostics.Diagnostics() {
		if diag == nil {
			continue
		}
		seen[diag] = struct{}{}
	}
	return seen
}

func annotateGenericInstantiationDiagnostics(
	ctx *context_v2.CompilerContext,
	before map[*diagnostics.Diagnostic]struct{},
	callSite *source.Location,
	callSiteLabel string,
	typeArgSummary string,
) {
	if ctx == nil || ctx.Diagnostics == nil || callSite == nil {
		return
	}
	for _, diag := range ctx.Diagnostics.Diagnostics() {
		if diag == nil {
			continue
		}
		if _, existed := before[diag]; existed {
			continue
		}
		if diag.Severity != diagnostics.Error {
			continue
		}
		if !hasPrimaryLabel(diag) {
			continue
		}
		if !hasSecondaryLabel(diag, callSite, callSiteLabel) {
			diag.WithSecondaryLabel(callSite, callSiteLabel)
		}
		if typeArgSummary != "" {
			diag.WithNote(fmt.Sprintf("generic instantiation: %s", typeArgSummary))
		}
	}
}

func hasPrimaryLabel(diag *diagnostics.Diagnostic) bool {
	if diag == nil {
		return false
	}
	for _, label := range diag.Labels {
		if label.Style == diagnostics.Primary {
			return true
		}
	}
	return false
}

func hasSecondaryLabel(diag *diagnostics.Diagnostic, loc *source.Location, msg string) bool {
	if diag == nil || loc == nil {
		return false
	}
	for _, label := range diag.Labels {
		if label.Style != diagnostics.Secondary {
			continue
		}
		if label.Message != msg {
			continue
		}
		if sameLocation(label.Location, loc) {
			return true
		}
	}
	return false
}

func sameLocation(a, b *source.Location) bool {
	if a == nil || b == nil {
		return false
	}
	if a.Start == nil || a.End == nil || b.Start == nil || b.End == nil {
		return false
	}
	aFile := ""
	if a.Filename != nil {
		aFile = *a.Filename
	}
	bFile := ""
	if b.Filename != nil {
		bFile = *b.Filename
	}
	return aFile == bFile &&
		a.Start.Line == b.Start.Line &&
		a.Start.Column == b.Start.Column &&
		a.End.Line == b.End.Line &&
		a.End.Column == b.End.Column
}

func formatTypeArgBindings(typeParams []*ast.TypeParam, typeArgs []types.SemType) string {
	if len(typeParams) == 0 || len(typeArgs) == 0 {
		return ""
	}
	limit := len(typeParams)
	if len(typeArgs) < limit {
		limit = len(typeArgs)
	}
	parts := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		name := "?"
		if tp := typeParams[i]; tp != nil && tp.Name != nil && tp.Name.Name != "" {
			name = tp.Name.Name
		}
		arg := "unknown"
		if typeArgs[i] != nil {
			arg = typeArgs[i].String()
		}
		parts = append(parts, fmt.Sprintf("%s = %s", name, arg))
	}
	return strings.Join(parts, ", ")
}

func bindReceiverInstantiatedTypeParams(
	ctx *context_v2.CompilerContext,
	mod *context_v2.Module,
	scope *table.SymbolTable,
	receiverType ast.TypeNode,
	receiverTypeName string,
) {
	if scope == nil || receiverType == nil || receiverTypeName == "" || mod == nil {
		return
	}
	applied := unwrapAppliedReceiverTypeNode(receiverType)
	if applied == nil {
		return
	}
	info, ok := resolveGenericNamedType(ctx, mod, applied.Base)
	if !ok || info.Decl == nil || len(info.Decl.TypeParams) == 0 {
		return
	}
	inst, ok := mod.GenericNamedTypeInstantiation(receiverTypeName)
	if !ok || inst == nil || len(inst.TypeArgs) == 0 {
		return
	}
	if len(inst.TypeArgs) < len(info.Decl.TypeParams) {
		return
	}

	for i, typeParam := range info.Decl.TypeParams {
		if typeParam == nil || typeParam.Name == nil || typeParam.Name.Name == "" {
			continue
		}
		sym, ok := scope.GetSymbol(typeParam.Name.Name)
		if !ok || sym == nil || sym.Kind != symbols.SymbolTypeParameter {
			continue
		}
		concrete := inst.TypeArgs[i]
		if concrete == nil {
			concrete = types.TypeUnknown
		}
		sym.Type = concrete
	}
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
