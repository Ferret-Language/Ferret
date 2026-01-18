package gen

import (
	"fmt"

	"compiler/internal/mir"
	"compiler/internal/semantics/symbols"
	"compiler/internal/source"
	"compiler/internal/types"
	ustrings "compiler/internal/utils/strings"
)

func interfaceTypeOf(typ types.SemType) *types.InterfaceType {
	if typ == nil {
		return nil
	}
	if ref, ok := typ.(*types.ReferenceType); ok {
		typ = ref.Inner
	}
	if iface, ok := typ.(*types.InterfaceType); ok {
		return iface
	}
	if named, ok := typ.(*types.NamedType); ok {
		if iface, ok := named.Underlying.(*types.InterfaceType); ok {
			return iface
		}
	}
	return nil
}

func isEmptyInterface(typ types.SemType) bool {
	iface := interfaceTypeOf(typ)
	return iface != nil && len(iface.Methods) == 0
}

func interfaceKey(typ types.SemType) string {
	if typ == nil {
		return ""
	}
	if named, ok := typ.(*types.NamedType); ok {
		if iface, ok := named.Underlying.(*types.InterfaceType); ok {
			if iface.ID != "" {
				return iface.ID
			}
		}
		if named.Name != "" {
			return named.Name
		}
	}
	if iface, ok := typ.(*types.InterfaceType); ok {
		if iface.ID != "" {
			return iface.ID
		}
		return fmt.Sprintf("iface_%p", iface)
	}
	return ""
}

func concreteTypeName(typ types.SemType) string {
	if typ == nil {
		return ""
	}
	if ref, ok := typ.(*types.ReferenceType); ok {
		typ = ref.Inner
	}
	if named, ok := typ.(*types.NamedType); ok {
		return named.Name
	}
	return ""
}

// typeIDString generates a unique string identifier for a type for runtime type checking.
// This is used for interface{} to store type information at runtime.
func typeIDString(typ types.SemType) string {
	if typ == nil {
		return "nil"
	}
	// Use the type's string representation as its unique ID
	// This gives us unique IDs like "i32", "str", "[]i32", "[]interface{}", etc.
	return typ.String()
}

// ensureTypeIDGlobal creates or returns a global string constant for a type ID.
// The returned string is the global variable name (without $).
func (g *Generator) ensureTypeIDGlobal(typeID string) string {
	if g == nil {
		return ""
	}
	if globalName, ok := g.typeIDGlobals[typeID]; ok {
		return globalName
	}

	// Create a new global
	g.typeIDSeq++
	globalName := fmt.Sprintf("__typeid_%d", g.typeIDSeq)
	g.typeIDGlobals[typeID] = globalName

	// The actual global will be emitted during module generation
	// For now, just track that we need it
	return globalName
}

func (g *Generator) lookupTypeSymbol(name string) (*symbols.Symbol, string, bool) {
	if g == nil || g.mod == nil || g.mod.ModuleScope == nil {
		return nil, "", false
	}

	if sym, ok := g.mod.ModuleScope.GetSymbol(name); ok && sym.Kind == symbols.SymbolType {
		return sym, "", true
	}

	if g.ctx == nil || g.mod.ImportAliasMap == nil {
		return nil, "", false
	}

	for alias, importPath := range g.mod.ImportAliasMap {
		if imported, ok := g.ctx.GetModule(importPath); ok && imported.ModuleScope != nil {
			if sym, ok := imported.ModuleScope.GetSymbol(name); ok && sym.Kind == symbols.SymbolType {
				return sym, alias, true
			}
		}
	}

	return nil, "", false
}

func (g *Generator) ensureInterfaceVTable(concreteType, ifaceType types.SemType, loc source.Location) (string, bool) {
	if g == nil {
		return "", false
	}
	iface := interfaceTypeOf(ifaceType)
	if iface == nil || len(iface.Methods) == 0 {
		return "", false
	}

	concreteName := concreteTypeName(concreteType)
	if concreteName == "" {
		if g.ctx != nil {
			g.ctx.ReportError("mir: interface value requires named type", &loc)
		}
		return "", false
	}

	ifaceKey := interfaceKey(ifaceType)
	if ifaceKey == "" {
		if g.ctx != nil {
			g.ctx.ReportError("mir: interface value missing interface key", &loc)
		}
		return "", false
	}

	key := concreteName + ":" + ifaceKey
	if table, ok := g.vtables[key]; ok && table != nil {
		return table.Name, true
	}

	typeSym, alias, ok := g.lookupTypeSymbol(concreteName)
	if !ok || typeSym == nil || typeSym.Methods == nil {
		if g.ctx != nil {
			g.ctx.ReportError("mir: interface value missing methods for type", &loc)
		}
		return "", false
	}

	g.vtableSeq++
	vtableName := fmt.Sprintf("__vtable_%d", g.vtableSeq)
	table := &mir.VTable{Name: vtableName}

	for i, method := range iface.Methods {
		methodInfo, ok := typeSym.Methods[method.Name]
		if !ok || methodInfo == nil {
			if g.ctx != nil {
				g.ctx.ReportError("mir: interface method missing on concrete type", &loc)
			}
			return "", false
		}

		wrapperName := ustrings.ToIdentifier(fmt.Sprintf("__iface_%d_%d", g.vtableSeq, i))
		target := concreteName + "_" + method.Name
		if alias != "" {
			target = alias + "::" + target
		}

		g.buildInterfaceWrapper(wrapperName, target, method, methodInfo, loc)
		table.Methods = append(table.Methods, wrapperName)
	}

	g.vtables[key] = table
	return table.Name, true
}

func (g *Generator) buildInterfaceWrapper(wrapperName, target string, ifaceMethod types.InterfaceMethod, methodInfo *symbols.MethodInfo, loc source.Location) {
	if g == nil {
		return
	}

	retType := ifaceMethod.FuncType.Return
	if retType == nil {
		retType = types.TypeVoid
	}

	fn := &mir.Function{
		Name:     wrapperName,
		Return:   retType,
		Location: loc,
	}

	selfParam := g.newParam("__self", types.NewReference(types.TypeU8), loc)
	selfHeapParam := g.newParam(selfHeapParamName, types.TypeU64, loc)
	fn.Params = append(fn.Params, selfParam, selfHeapParam)
	origParams := g.lowerParams(ifaceMethod.FuncType)
	fn.Params = append(fn.Params, origParams...)

	g.applyRefReturnABI(fn, retType, loc, false)
	g.applyLargeReturnABI(fn, retType, loc)

	retParam := mir.InvalidValue
	outParam := mir.InvalidValue
	outHeapParam := mir.InvalidValue
	for _, p := range fn.Params {
		if p.Name == "__ret" {
			retParam = p.ID
			break
		}
		if p.Name == "__out" {
			outParam = p.ID
		}
		if p.Name == outHeapParamName {
			outHeapParam = p.ID
		}
	}

	entry := &mir.Block{
		ID:       g.nextBlockID(),
		Name:     "entry",
		Location: loc,
	}

	recvType := methodInfo.Receiver
	recvParamType := recvType
	recvIsRef := false
	if _, ok := types.UnwrapType(recvType).(*types.ReferenceType); ok {
		recvIsRef = true
	}
	if needsByRefType(recvParamType) {
		recvParamType = types.NewReference(recvParamType)
	}

	var recvVal mir.ValueID
	if _, ok := recvParamType.(*types.ReferenceType); ok {
		cast := g.nextValueID()
		entry.Instrs = append(entry.Instrs, &mir.Cast{
			Result:   cast,
			X:        selfParam.ID,
			Type:     recvParamType,
			Location: loc,
		})
		recvVal = cast
	} else {
		addr := g.nextValueID()
		entry.Instrs = append(entry.Instrs, &mir.Cast{
			Result:   addr,
			X:        selfParam.ID,
			Type:     types.NewReference(recvParamType),
			Location: loc,
		})
		load := g.nextValueID()
		entry.Instrs = append(entry.Instrs, &mir.Load{
			Result:   load,
			Addr:     addr,
			Type:     recvParamType,
			Location: loc,
		})
		recvVal = load
	}

	callArgs := []mir.ValueID{recvVal}
	if recvIsRef {
		callArgs = append(callArgs, selfHeapParam.ID)
	}
	for _, p := range fn.Params {
		if p.Name == "__self" || p.Name == selfHeapParamName || p.Name == "__ret" || p.Name == "__out" || p.Name == outHeapParamName {
			continue
		}
		callArgs = append(callArgs, p.ID)
	}
	if outParam != mir.InvalidValue {
		if outHeapParam != mir.InvalidValue {
			callArgs = append([]mir.ValueID{outParam, outHeapParam}, callArgs...)
		} else {
			callArgs = append([]mir.ValueID{outParam}, callArgs...)
		}
	}

	call := &mir.Call{
		Result:   mir.InvalidValue,
		Target:   target,
		Args:     callArgs,
		Type:     retType,
		Location: loc,
	}

	if retParam != mir.InvalidValue {
		call.Args = append([]mir.ValueID{retParam}, call.Args...)
		call.Type = types.TypeVoid
		entry.Instrs = append(entry.Instrs, call)
		entry.Term = &mir.Return{HasValue: false, Location: loc}
	} else if retType == nil || retType.Equals(types.TypeVoid) || retType.Equals(types.TypeNone) {
		entry.Instrs = append(entry.Instrs, call)
		entry.Term = &mir.Return{HasValue: false, Location: loc}
	} else {
		call.Result = g.nextValueID()
		entry.Instrs = append(entry.Instrs, call)
		entry.Term = &mir.Return{HasValue: true, Value: call.Result, Location: loc}
	}

	fn.Blocks = []*mir.Block{entry}
	g.extraFns = append(g.extraFns, fn)
}
