package ownership

import (
	"fmt"
	"slices"
	"strings"

	cfg "compiler/internal/analysis/cfg/model"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/phase"
	"compiler/internal/core/source"
	"compiler/internal/frontend/ast"
	"compiler/internal/ir/mir"
)

type ownerRef struct {
	localID int
	name    string
}

func ownerLocal(id int) ownerRef { return ownerRef{localID: id} }
func ownerName(name string) ownerRef {
	return ownerRef{localID: -1, name: name}
}
func (o ownerRef) isLocal() bool { return o.localID >= 0 }

type borrowInfo struct {
	owner   ownerRef
	loc     source.Location
	mutable bool
}

type tempInfo struct {
	root   ownerRef
	path   string
	borrow *borrowInfo
	value  mir.Value
}

type ownershipAnalyzer struct {
	ctx       *context.CompilerContext
	mod       *context.Module
	module    *mir.Module
	currentFn *mir.Function
	temps     map[int]tempInfo
	reported  map[string]struct{}
	deferUses cfg.LocalSet
}

func AnalyzeOwnershipModule(ctx *context.CompilerContext, mod *context.Module) {
	if ctx == nil || mod == nil || mod.MIR == nil || mod.CFG == nil {
		return
	}
	a := &ownershipAnalyzer{
		ctx:      ctx,
		mod:      mod,
		module:   mod.MIR,
		temps:    make(map[int]tempInfo),
		reported: make(map[string]struct{}),
	}
	for _, global := range mod.MIR.Globals {
		a.checkGlobal(global)
	}
	for i, fn := range mod.CFG.Functions {
		if i >= len(mod.MIR.Functions) {
			break
		}
		a.checkFunc(fn, mod.MIR.Functions[i])
	}
	mod.Phase = phase.PhaseOwnershipAnalyzed
}

func (a *ownershipAnalyzer) localName(id int) string {
	if a == nil || a.currentFn == nil {
		return ""
	}
	return a.currentFn.LocalName(id)
}

func (a *ownershipAnalyzer) localIDByName(name string) int {
	if a == nil || a.currentFn == nil || name == "" {
		return -1
	}
	for _, local := range a.currentFn.Locals {
		if local != nil && local.Name == name {
			return local.ID
		}
	}
	return -1
}

func (a *ownershipAnalyzer) localType(id int) typeinfo.Type {
	if a == nil || a.currentFn == nil {
		return typeinfo.UnknownType{}
	}
	return a.currentFn.LocalType(id)
}

func (a *ownershipAnalyzer) checkGlobal(global *mir.Global) {
	if global == nil || global.Init == nil {
		return
	}
	scope := newValueScope()
	a.checkValue(scope, global.Init)
	if global.Constant {
		a.reportBorrowEscapeIfNeeded(scope, global.Init, "borrow cannot escape into a module-level constant")
	} else {
		a.reportBorrowEscapeIfNeeded(scope, global.Init, "borrow cannot escape into a module-level binding")
	}
	a.consumeMoveValue(scope, global.Init, global.Type)
}

func (a *ownershipAnalyzer) checkFunc(cfgFn *cfg.Function, mirFn *mir.Function) {
	if cfgFn == nil || mirFn == nil || cfgFn.Entry == nil {
		return
	}
	a.currentFn = mirFn
	a.deferUses = a.collectDeferredLocalUses(mirFn)
	defer func() {
		a.currentFn = nil
		a.deferUses = nil
	}()
	blocks := make(map[int]*mir.Block, len(mirFn.Blocks))
	for _, block := range mirFn.Blocks {
		if block != nil {
			blocks[block.ID] = block
		}
	}
	inStates := map[*cfg.Block]*valueScope{}
	outStates := map[*cfg.Block]*valueScope{}
	queue := []*cfg.Block{cfgFn.Entry}
	inStates[cfgFn.Entry] = a.seedFunctionState(mirFn)
	for len(queue) > 0 {
		block := queue[0]
		queue = queue[1:]
		in := inStates[block]
		if in == nil {
			continue
		}
		out := a.transferBlock(in, block, blocks[block.ID])
		outStates[block] = out
		if block.Terminator == nil {
			continue
		}
		for _, succ := range block.Terminator.Successors() {
			if succ == nil || !succ.Reachable {
				continue
			}
			if a.shouldSkipBackedgeForSingleIterationLoop(cfgFn, blocks, block, succ) {
				continue
			}
			if current, ok := inStates[succ]; ok {
				if current.MergeFrom(out, succ.LiveIn) {
					queue = append(queue, succ)
				}
				continue
			}
			clone := out.Clone()
			clone.TrimToLiveOut(succ.LiveIn)
			inStates[succ] = clone
			queue = append(queue, succ)
		}
	}
	_ = outStates
}

func (a *ownershipAnalyzer) seedFunctionState(fn *mir.Function) *valueScope {
	scope := newValueScope()
	if fn == nil {
		return scope
	}
	for _, param := range fn.Params {
		if param == nil {
			continue
		}
		scope.Declare(param.LocalID, valueInfo{typ: param.Type, mutable: param.IsMutable})
	}
	declareFunctionLocals(scope, fn)
	return scope
}

func declareFunctionLocals(scope *valueScope, fn *mir.Function) {
	if scope == nil || fn == nil {
		return
	}
	for _, local := range fn.Locals {
		if local == nil || local.ID < 0 {
			continue
		}
		scope.Declare(local.ID, valueInfo{typ: local.Type, mutable: local.Mutable, constant: local.Constant})
	}
}

func resetMoveState(slot *valueInfo) {
	if slot == nil {
		return
	}
	slot.moved = false
	slot.moveLoc = source.Location{}
	slot.movedPath = ""
	slot.movedSubs = nil
}

func setConcreteValue(slot *valueInfo, value mir.Value) {
	if slot == nil {
		return
	}
	if ifaceValue, ok := value.(*mir.InterfaceValue); ok {
		slot.concrete = ifaceValue.ConcreteType
		return
	}
	slot.concrete = nil
}

func (a *ownershipAnalyzer) transferBlock(in *valueScope, cfgBlock *cfg.Block, mirBlock *mir.Block) *valueScope {
	state := in.Clone()
	if state == nil {
		state = newValueScope()
	}
	for _, info := range a.temps {
		_ = info
	}
	a.temps = make(map[int]tempInfo)
	if mirBlock != nil {
		liveAfter := a.blockLiveAfterInstrs(cfgBlock, mirBlock)
		for _, instr := range mirBlock.Instructions {
			_ = instr
		}
		for i, instr := range mirBlock.Instructions {
			a.checkMIRInstr(state, instr)
			if i < len(liveAfter) {
				a.releaseDeadBorrows(state, liveAfter[i])
			}
		}
		a.checkMIRTerm(state, mirBlock.Terminator)
	} else {
		switch term := cfgBlock.Terminator.(type) {
		case *cfg.BranchTerm:
			_ = term
		case *cfg.SwitchTerm:
			_ = term
		}
	}
	state.TrimToLiveOut(cfgBlock.LiveOut)
	return state
}

func (a *ownershipAnalyzer) blockLiveAfterInstrs(cfgBlock *cfg.Block, mirBlock *mir.Block) []cfg.LocalSet {
	if mirBlock == nil {
		return nil
	}
	out := make([]cfg.LocalSet, len(mirBlock.Instructions))
	future := cfg.NewLocalSet()
	if cfgBlock != nil && cfgBlock.LiveOut != nil {
		future = cfgBlock.LiveOut.Clone()
	}
	a.collectTermLocalUses(future, mirBlock.Terminator)
	for i := len(mirBlock.Instructions) - 1; i >= 0; i-- {
		out[i] = future.Clone()
		a.collectInstrLocalUses(future, mirBlock.Instructions[i])
	}
	return out
}

func (a *ownershipAnalyzer) releaseDeadBorrows(scope *valueScope, keep cfg.LocalSet) {
	if scope == nil {
		return
	}
	for id, slot := range scope.values {
		if slot == nil || len(slot.borrows) == 0 {
			continue
		}
		if keep.Has(id) || a.deferUses.Has(id) {
			continue
		}
		a.releaseBorrowValue(scope, slot)
	}
}

func (a *ownershipAnalyzer) checkMIRInstr(scope *valueScope, instr mir.Instr) {
	switch inst := instr.(type) {
	case nil:
		return
	case *mir.ComputeInstr:
		a.checkComputedValue(scope, inst)
	case *mir.BindInstr:
		if inst.Value != nil {
			a.checkValue(scope, inst.Value)
			a.consumeMoveValue(scope, inst.Value, inst.Type)
		}
		if id := a.localIDByName(inst.Name); id >= 0 {
			slot, _ := scope.Lookup(id)
			if slot == nil {
				break
			}
			resetMoveState(slot)
			setConcreteValue(slot, inst.Value)
			a.bindBorrowValue(scope, slot, inst.Value)
		}
	case *mir.StoreInstr:
		a.checkAssignmentTarget(scope, inst.Target)
		a.checkValue(scope, inst.Value)
		a.consumeMoveValue(scope, inst.Value, a.placeType(scope, inst.Target))
		a.rebindBorrowAssignment(scope, inst.Target, inst.Value)
	case *mir.StoreFieldInstr:
		target := a.fieldStorePlace(inst)
		a.checkAssignmentTarget(scope, target)
		a.checkValue(scope, inst.Base)
		a.checkValue(scope, inst.Value)
		a.consumeMoveValue(scope, inst.Value, mir.FieldType(valueType(inst.Base), inst.FieldIndex))
	case *mir.AssignInstr:
		if inst.TargetID < 0 {
			return
		}
		target := &mir.LocalPlace{LocalID: inst.TargetID}
		a.checkAssignmentTarget(scope, target)
		a.checkValue(scope, inst.Value)
		a.consumeMoveValue(scope, inst.Value, a.localType(inst.TargetID))
		if slot, _ := scope.Lookup(inst.TargetID); slot != nil {
			setConcreteValue(slot, inst.Value)
		}
		a.rebindBorrowAssignment(scope, target, inst.Value)
	case *mir.EvalInstr:
		a.checkValue(scope, inst.Value)
	case *mir.DeferInstr:
		for _, child := range inst.Body {
			a.checkDeferredInstr(scope, child)
		}
	case *mir.LockInstr:
		a.checkValue(scope, inst.Value)
		if slot, _ := scope.Lookup(inst.LocalID); slot != nil {
			resetMoveState(slot)
		}
	case *mir.UnsafeInstr:
		return
	}
}

func (a *ownershipAnalyzer) checkDeferredInstr(scope *valueScope, instr mir.Instr) {
	switch inst := instr.(type) {
	case nil:
		return
	case *mir.ComputeInstr:
		a.reportBorrowEscapeIfNeeded(scope, inst.Value, "borrow cannot escape into defer")
	case *mir.BindInstr:
		a.reportBorrowEscapeIfNeeded(scope, inst.Value, "borrow cannot escape into defer")
	case *mir.StoreInstr:
		a.reportBorrowEscapeIfNeeded(scope, inst.Value, "borrow cannot escape into defer")
	case *mir.AssignInstr:
		a.reportBorrowEscapeIfNeeded(scope, inst.Value, "borrow cannot escape into defer")
	case *mir.StoreFieldInstr:
		a.reportBorrowEscapeIfNeeded(scope, inst.Base, "borrow cannot escape into defer")
		a.reportBorrowEscapeIfNeeded(scope, inst.Value, "borrow cannot escape into defer")
	case *mir.EvalInstr:
		a.reportBorrowEscapeIfNeeded(scope, inst.Value, "borrow cannot escape into defer")
	case *mir.DeferInstr:
		for _, child := range inst.Body {
			a.checkDeferredInstr(scope, child)
		}
	case *mir.LockInstr:
		a.reportBorrowEscapeIfNeeded(scope, inst.Value, "borrow cannot escape into defer")
	case *mir.UnsafeInstr:
		return
	}
}

func (a *ownershipAnalyzer) collectDeferredLocalUses(fn *mir.Function) cfg.LocalSet {
	used := cfg.NewLocalSet()
	if fn == nil {
		return used
	}
	for _, block := range fn.Blocks {
		if block == nil {
			continue
		}
		for _, instr := range block.Instructions {
			if deferInstr, ok := instr.(*mir.DeferInstr); ok {
				a.collectDeferredInstrLocalUses(used, deferInstr)
			}
		}
	}
	return used
}

func (a *ownershipAnalyzer) collectDeferredInstrLocalUses(used cfg.LocalSet, instr *mir.DeferInstr) {
	if used == nil || instr == nil {
		return
	}
	for _, child := range instr.Body {
		switch inst := child.(type) {
		case nil:
			continue
		case *mir.ComputeInstr:
			a.collectValueLocalUses(used, inst.Value)
		case *mir.BindInstr:
			a.collectValueLocalUses(used, inst.Value)
		case *mir.StoreInstr:
			a.collectPlaceLocalUses(used, inst.Target)
			a.collectValueLocalUses(used, inst.Value)
		case *mir.AssignInstr:
			a.collectValueLocalUses(used, inst.Value)
		case *mir.StoreFieldInstr:
			a.collectValueLocalUses(used, inst.Base)
			a.collectValueLocalUses(used, inst.Value)
		case *mir.EvalInstr:
			a.collectValueLocalUses(used, inst.Value)
		case *mir.DeferInstr:
			a.collectDeferredInstrLocalUses(used, inst)
		case *mir.LockInstr:
			a.collectValueLocalUses(used, inst.Value)
		case *mir.UnsafeInstr:
			continue
		}
	}
}

func (a *ownershipAnalyzer) checkMIRTerm(scope *valueScope, term mir.Terminator) {
	switch t := term.(type) {
	case nil, *mir.JumpTerm, *mir.ExitTerm:
		return
	case *mir.BranchTerm:
		a.checkValue(scope, t.Cond)
	case *mir.SwitchTerm:
		a.checkValue(scope, t.Value)
		for _, kase := range t.Cases {
			a.checkValue(scope, kase.Expr)
		}
	case *mir.ReturnTerm:
		if t.Value != nil {
			a.checkValue(scope, t.Value)
			a.reportBorrowEscapeIfNeeded(scope, t.Value, "borrow cannot be returned")
			a.consumeMoveValue(scope, t.Value, valueType(t.Value))
		}
	case *mir.PanicTerm:
		if t.Value != nil {
			a.checkValue(scope, t.Value)
		}
	}
}

func (a *ownershipAnalyzer) collectInstrLocalUses(used cfg.LocalSet, instr mir.Instr) {
	if used == nil || instr == nil {
		return
	}
	switch inst := instr.(type) {
	case *mir.ComputeInstr:
		a.collectValueLocalUses(used, inst.Value)
	case *mir.BindInstr:
		a.collectValueLocalUses(used, inst.Value)
	case *mir.StoreInstr:
		a.collectPlaceLocalUses(used, inst.Target)
		a.collectValueLocalUses(used, inst.Value)
	case *mir.AssignInstr:
		a.collectValueLocalUses(used, inst.Value)
	case *mir.StoreFieldInstr:
		a.collectValueLocalUses(used, inst.Base)
		a.collectValueLocalUses(used, inst.Value)
	case *mir.EvalInstr:
		a.collectValueLocalUses(used, inst.Value)
	case *mir.DeferInstr:
		// Deferred bodies execute at scope end, not at this point.
	case *mir.LockInstr:
		a.collectValueLocalUses(used, inst.Value)
	case *mir.UnsafeInstr:
	}
}

func (a *ownershipAnalyzer) collectTermLocalUses(used cfg.LocalSet, term mir.Terminator) {
	if used == nil || term == nil {
		return
	}
	switch t := term.(type) {
	case *mir.BranchTerm:
		a.collectValueLocalUses(used, t.Cond)
	case *mir.SwitchTerm:
		a.collectValueLocalUses(used, t.Value)
		for _, kase := range t.Cases {
			a.collectValueLocalUses(used, kase.Expr)
		}
	case *mir.ReturnTerm:
		a.collectValueLocalUses(used, t.Value)
	case *mir.PanicTerm:
		a.collectValueLocalUses(used, t.Value)
	}
}

func (a *ownershipAnalyzer) collectPlaceLocalUses(used cfg.LocalSet, place mir.Place) {
	if used == nil || place == nil {
		return
	}
	switch p := place.(type) {
	case *mir.LocalPlace:
		// Pure local assignment target is not a read-use.
	case *mir.FieldPlace:
		a.collectPlaceLocalUses(used, p.Base)
	case *mir.IndexPlace:
		a.collectPlaceLocalUses(used, p.Base)
		a.collectValueLocalUses(used, p.Index)
	case *mir.DerefPlace:
		a.collectValueLocalUses(used, p.Pointer)
	}
}

func (a *ownershipAnalyzer) collectValueLocalUses(used cfg.LocalSet, value mir.Value) {
	if used == nil || value == nil {
		return
	}
	switch v := value.(type) {
	case *mir.LocalValue:
		used.Add(v.LocalID)
	case *mir.AddrOfValue:
		a.collectValueLocalUses(used, v.Source)
	case *mir.LoadValue:
		a.collectValueLocalUses(used, v.Pointer)
	case *mir.UnaryValue:
		a.collectValueLocalUses(used, v.Right)
	case *mir.BinaryValue:
		a.collectValueLocalUses(used, v.Left)
		a.collectValueLocalUses(used, v.Right)
	case *mir.PostfixValue:
		a.collectValueLocalUses(used, v.Left)
	case *mir.CallValue:
		a.collectValueLocalUses(used, v.Callee)
		for _, arg := range v.Args {
			a.collectValueLocalUses(used, arg)
		}
	case *mir.FieldLoadValue:
		a.collectValueLocalUses(used, v.Base)
	case *mir.FieldValue:
		a.collectValueLocalUses(used, v.Base)
	case *mir.CastValue:
		a.collectValueLocalUses(used, v.Left)
	case *mir.TypeTestValue:
		a.collectValueLocalUses(used, v.Left)
	case *mir.CompositeValue:
		for _, item := range v.Items {
			a.collectValueLocalUses(used, item.Value)
		}
	case *mir.InterfaceValue:
		a.collectValueLocalUses(used, v.Value)
	case *mir.IndexValue:
		a.collectValueLocalUses(used, v.Base)
		a.collectValueLocalUses(used, v.Index)
	}
}

func (a *ownershipAnalyzer) checkComputedValue(scope *valueScope, instr *mir.ComputeInstr) {
	if instr == nil {
		return
	}
	a.checkValue(scope, instr.Value)
	if info, ok := a.tempInfoForValue(instr.Value); ok {
		a.temps[instr.TargetID] = info
	}
}

func (a *ownershipAnalyzer) checkValue(scope *valueScope, value mir.Value) {
	switch v := value.(type) {
	case nil, *mir.NumberValue, *mir.BoolValue, *mir.StringValue, *mir.NoneValue:
		return
	case *mir.LocalValue:
		a.checkDirectValueBorrowConflict(scope, v)
		a.requireActiveLocal(scope, v)
	case *mir.NameValue:
		a.requireActiveValue(scope, v)
	case *mir.FieldLoadValue:
		a.checkDirectValueBorrowConflict(scope, v)
		a.checkFieldLoadValue(scope, v)
	case *mir.AddrOfValue:
		a.checkAddrOfValue(scope, v)
	case *mir.LoadValue:
		a.checkLoadValue(scope, v)
	case *mir.UnaryValue:
		switch v.Op {
		case "copy":
			a.checkValue(scope, v.Right)
			loc := v.Loc()
			a.addDiagnostic(
				diagnostics.NewError("`copy` is not yet implemented").
					WithCode(diagnostics.ErrInvalidCopy).
					WithPrimaryLabel(&loc, "deep clone support has not been implemented yet"),
			)
		case "&", "&mut":
			a.checkValue(scope, v.Right)
		default:
			a.checkValue(scope, v.Right)
		}
	case *mir.BinaryValue:
		a.checkValue(scope, v.Left)
		a.checkValue(scope, v.Right)
	case *mir.PostfixValue:
		a.checkValue(scope, v.Left)
	case *mir.CallValue:
		a.checkCall(scope, v)
	case *mir.FieldValue:
		a.checkDirectValueBorrowConflict(scope, v)
		a.checkFieldValue(scope, v)
	case *mir.CastValue:
		a.checkValue(scope, v.Left)
	case *mir.CompositeValue:
		for _, item := range v.Items {
			a.checkValue(scope, item.Value)
		}
	case *mir.InterfaceValue:
		a.checkValue(scope, v.Value)
	}
}

func (a *ownershipAnalyzer) checkDirectValueBorrowConflict(scope *valueScope, value mir.Value) {
	if scope == nil || value == nil {
		return
	}
	root, _, ok := a.localValuePath(value)
	if !ok {
		return
	}
	if a.hasActiveMutableBorrowOf(scope, root) {
		a.reportBorrowConflict(value.Loc(), root, "cannot use value while a mutable borrow is live")
	}
}

func (a *ownershipAnalyzer) checkPlaceValue(scope *valueScope, place mir.Place) {
	switch p := place.(type) {
	case nil:
		return
	case *mir.LocalPlace:
		a.requireActivePath(scope, p.LocalID, "", p.Loc())
	case *mir.FieldPlace:
		if root, path, ok := a.localPlacePath(p); ok {
			a.requireActivePath(scope, root, path, p.Loc())
			return
		}
		a.checkPlaceValue(scope, p.Base)
	case *mir.IndexPlace:
		a.checkPlaceValue(scope, p.Base)
	case *mir.DerefPlace:
		a.checkValue(scope, p.Pointer)
	}
}

func (a *ownershipAnalyzer) checkFieldValue(scope *valueScope, value *mir.FieldValue) {
	if value == nil {
		return
	}
	receiverType := valueType(value.Base)
	name := a.fieldSelectorName(value)
	if a.lookupStructField(receiverType, name) == nil && a.canHaveMethods(receiverType) {
		a.checkValue(scope, value.Base)
		return
	}
	if root, path, ok := a.localValuePath(value); ok {
		a.requireActivePath(scope, root, path, value.Loc())
		return
	}
	a.checkValue(scope, value.Base)
}

func (a *ownershipAnalyzer) checkFieldLoadValue(scope *valueScope, value *mir.FieldLoadValue) {
	if value == nil {
		return
	}
	if root, path, ok := a.localValuePath(value); ok {
		a.requireActivePath(scope, root, path, value.Loc())
		return
	}
	a.checkValue(scope, value.Base)
}

func (a *ownershipAnalyzer) checkAddrOfValue(scope *valueScope, value *mir.AddrOfValue) {
	if value == nil {
		return
	}
	a.checkValue(scope, value.Source)
	if value.Raw {
		return
	}
	root, _, ok := a.borrowSourcePath(value.Source)
	if !ok || !root.isLocal() || scope == nil {
		return
	}
	owner, _ := scope.Lookup(root.localID)
	if owner == nil {
		return
	}
	if value.Mutable {
		if owner.frozen > 0 {
			a.reportBorrowConflict(value.Loc(), root.localID, "cannot create mutable borrow while another borrow is live")
		}
		return
	}
	if owner.mutFrozen > 0 {
		a.reportBorrowConflict(value.Loc(), root.localID, "cannot create immutable borrow while a mutable borrow is live")
	}
}

func (a *ownershipAnalyzer) checkLoadValue(scope *valueScope, value *mir.LoadValue) {
	if value == nil {
		return
	}
	a.checkValue(scope, value.Pointer)
}

func (a *ownershipAnalyzer) checkCall(scope *valueScope, call *mir.CallValue) {
	if call == nil {
		return
	}
	// Handle normalized method calls: receiver is Args[0], ReceiverType is set.
	if call.ReceiverType != nil && len(call.Args) > 0 {
		a.checkNormalizedMethodCall(scope, call)
		return
	}
	if field, ok := call.Callee.(*mir.FieldValue); ok {
		if handled := a.checkMethodCall(scope, call, field); handled {
			return
		}
	}
	if local, ok := call.Callee.(*mir.LocalValue); ok {
		if info, ok := a.temps[local.LocalID]; ok {
			if field, ok := info.value.(*mir.FieldValue); ok {
				if handled := a.checkMethodCall(scope, call, field); handled {
					return
				}
			}
		}
	}
	a.checkValue(scope, call.Callee)
	fnType, ok := valueType(call.Callee).(*typeinfo.FuncType)
	if !ok {
		a.checkCallArgs(scope, call.Args, nil)
		return
	}
	a.checkCallArgs(scope, call.Args, fnType.Params)
}

// checkNormalizedMethodCall handles method calls that have been normalized in MIR so
// that the receiver is Args[0] and ReceiverType is set on the CallValue.
func (a *ownershipAnalyzer) checkNormalizedMethodCall(scope *valueScope, call *mir.CallValue) {
	receiver := call.Args[0]
	receiverType := call.ReceiverType
	a.checkValue(scope, receiver)

	// Look up the method symbol so we can check if it consumes the receiver.
	methodName := ""
	if name, ok := call.Callee.(*mir.NameValue); ok && len(name.Path) > 0 {
		methodName = name.Path[len(name.Path)-1]
	}
	if methodName != "" {
		if iface, ok := a.interfaceView(receiverType); ok {
			if baseNamed, ok := typeinfo.ReceiverBaseNamedType(receiverType); ok && baseNamed != nil {
				prefix := baseNamed.Name + "__"
				methodName = strings.TrimPrefix(methodName, prefix)
			}
			method := iface.Methods[methodName]
			if method != nil {
				a.checkCallArgs(scope, call.Args[1:], method.Params)
				a.consumeInterfaceReceiver(scope, receiver, iface.MethodReceivers[methodName], receiverType)
				return
			}
		}
	}
	if methodName != "" && !typeinfo.IsInvalid(receiverType) && !typeinfo.IsUnknown(receiverType) {
		if baseNamed, ok := typeinfo.ReceiverBaseNamedType(receiverType); ok && baseNamed != nil {
			prefix := baseNamed.Name + "__"
			methodName = strings.TrimPrefix(methodName, prefix)
		}
		addressable, mutable := a.valueAccess(scope, receiver)
		methodSym, methodType := a.lookupMethod(receiverType, methodName, addressable, mutable)
		if methodType != nil {
			a.checkCallArgs(scope, call.Args[1:], methodType.Params)
			if methodSym != nil {
				if fn, ok := methodSym.Node.(*ast.FuncDecl); ok && a.receiverConsumes(a.findCandidateModuleForSymbol(methodSym), fn) {
					a.consumeMoveValue(scope, receiver, receiverType)
				}
			}
			return
		}
	}
	// Fallback: just check all args normally.
	a.checkCallArgs(scope, call.Args[1:], nil)
}

func (a *ownershipAnalyzer) checkMethodCall(scope *valueScope, call *mir.CallValue, field *mir.FieldValue) bool {
	a.checkValue(scope, field.Base)
	receiverType := valueType(field.Base)
	if typeinfo.IsInvalid(receiverType) || typeinfo.IsUnknown(receiverType) {
		return true
	}
	name := a.fieldSelectorName(field)
	if structField := a.lookupStructField(receiverType, name); structField != nil {
		return false
	}
	if iface, ok := a.interfaceView(receiverType); ok {
		method := iface.Methods[name]
		if method == nil {
			return true
		}
		a.checkCallArgs(scope, call.Args, method.Params)
		a.consumeInterfaceReceiver(scope, field.Base, iface.MethodReceivers[name], receiverType)
		return true
	}
	addressable, mutable := a.valueAccess(scope, field.Base)
	methodSym, methodType := a.lookupMethod(receiverType, name, addressable, mutable)
	if methodType == nil {
		return a.canHaveMethods(receiverType)
	}
	a.checkCallArgs(scope, call.Args, methodType.Params)
	if methodSym != nil {
		if fn, ok := methodSym.Node.(*ast.FuncDecl); ok && a.receiverConsumes(a.findCandidateModuleForSymbol(methodSym), fn) {
			a.consumeMoveValue(scope, field.Base, receiverType)
		}
	}
	return true
}

func (a *ownershipAnalyzer) checkCallArgs(scope *valueScope, args []mir.Value, params []typeinfo.ParamSpec) {
	for i, arg := range args {
		a.checkValue(scope, arg)
		if i < len(params) {
			a.consumeMoveValue(scope, arg, params[i].Type)
		}
	}
}

func (a *ownershipAnalyzer) requireActiveValue(scope *valueScope, value *mir.NameValue) {
	// Globals and other non-local names are not tracked as movable slots.
	_ = scope
	_ = value
}

func (a *ownershipAnalyzer) requireActiveLocal(scope *valueScope, value *mir.LocalValue) {
	if value == nil {
		return
	}
	if info, ok := a.temps[value.LocalID]; ok && info.root.isLocal() {
		a.requireActivePath(scope, info.root.localID, info.path, value.Loc())
		return
	}
	a.requireActivePath(scope, value.LocalID, "", value.Loc())
}

func (a *ownershipAnalyzer) consumeMoveValue(scope *valueScope, value mir.Value, typ typeinfo.Type) {
	if value == nil {
		return
	}
	if ifaceValue, ok := value.(*mir.InterfaceValue); ok {
		a.consumeMoveValue(scope, ifaceValue.Value, ifaceValue.ConcreteType)
		return
	}
	if local, ok := value.(*mir.LocalValue); ok && scope != nil {
		if slot, ok := scope.Lookup(local.LocalID); ok && slot != nil && a.isMoveType(slot.concrete) {
			a.consumeLocalPath(scope, local.LocalID, "", value.Loc())
			return
		}
	}
	if typ == nil || !a.isMoveType(typ) {
		return
	}
	switch v := value.(type) {
	case *mir.UnaryValue:
		if v.Op == "copy" {
			return
		}
	case *mir.LocalValue:
		if info, ok := a.temps[v.LocalID]; ok {
			if !info.root.isLocal() {
				return
			}
			a.consumeLocalPath(scope, info.root.localID, info.path, value.Loc())
			return
		}
		a.consumeLocalPath(scope, v.LocalID, "", value.Loc())
		return
	}
	root, path, ok := a.localValuePath(value)
	if !ok || scope == nil {
		return
	}
	a.consumeLocalPath(scope, root, path, value.Loc())
}

func (a *ownershipAnalyzer) consumeLocalPath(scope *valueScope, root int, path string, loc source.Location) {
	if scope == nil || root < 0 {
		return
	}
	info, ok := scope.Lookup(root)
	if !ok || info == nil {
		return
	}
	if info.frozen > 0 {
		a.reportBorrowConflict(loc, root, "cannot move a value while a borrow is live")
		return
	}
	if path == "" && len(info.movedSubs) > 0 {
		a.reportPartialMoveUse(root, loc, info)
		return
	}
	if path != "" {
		if !a.isMoveType(a.pathType(info.typ, path)) {
			return
		}
		if movedLoc, movedPath, ok := movedPathConflict(info, path); ok {
			a.reportMovedPathUse(root, path, loc, movedLoc, movedPath)
			return
		}
		markMovedPath(info, path, loc)
		return
	}
	info.moved = true
	info.moveLoc = loc
	info.movedPath = ""
	info.movedSubs = nil
}

func (a *ownershipAnalyzer) tempInfoForValue(value mir.Value) (tempInfo, bool) {
	switch v := value.(type) {
	case *mir.AddrOfValue:
		root, path, ok := a.borrowSourcePath(v.Source)
		if !ok {
			return tempInfo{}, false
		}
		info := tempInfo{root: root, path: path, value: value}
		info.borrow = &borrowInfo{owner: root, loc: v.Loc()}
		return info, true
	case *mir.LoadValue:
		root, path, ok := a.borrowSourcePath(v.Pointer)
		if !ok {
			return tempInfo{}, false
		}
		return tempInfo{root: root, path: path, value: value}, true
	case *mir.UnaryValue:
		if v.Op == "*" {
			root, path, ok := a.borrowSourcePath(v.Right)
			if !ok {
				return tempInfo{}, false
			}
			return tempInfo{root: root, path: path, value: value}, true
		}
	case *mir.FieldLoadValue, *mir.FieldValue, *mir.LocalValue:
		rootID, path, ok := a.localValuePath(value)
		if !ok {
			return tempInfo{}, false
		}
		return tempInfo{root: ownerLocal(rootID), path: path, value: value}, true
	case *mir.InterfaceValue:
		return a.tempInfoForValue(v.Value)
	}
	return tempInfo{}, false
}

func (a *ownershipAnalyzer) borrowSourcePath(value mir.Value) (ownerRef, string, bool) {
	switch v := value.(type) {
	case *mir.NameValue:
		if len(v.Path) == 1 {
			return ownerName(v.Path[0]), "", true
		}
	case *mir.LocalValue:
		if info, ok := a.temps[v.LocalID]; ok && info.root.isLocal() {
			return info.root, info.path, true
		}
		return ownerLocal(v.LocalID), "", true
	case *mir.AddrOfValue:
		return a.borrowSourcePath(v.Source)
	case *mir.LoadValue:
		return a.borrowSourcePath(v.Pointer)
	case *mir.UnaryValue:
		if v.Op == "*" {
			return a.borrowSourcePath(v.Right)
		}
	case *mir.FieldLoadValue:
		if rootID, path, ok := a.localValuePath(v); ok {
			return ownerLocal(rootID), path, true
		}
	case *mir.FieldValue:
		if rootID, path, ok := a.localValuePath(v); ok {
			return ownerLocal(rootID), path, true
		}
	case *mir.InterfaceValue:
		return a.borrowSourcePath(v.Value)
	}
	return ownerRef{}, "", false
}

func (a *ownershipAnalyzer) bindBorrowValue(scope *valueScope, slot *valueInfo, value mir.Value) {
	if scope == nil || slot == nil {
		return
	}
	a.releaseBorrowValue(scope, slot)
	if value == nil {
		return
	}
	borrows, ok := a.borrowValueInfos(scope, value)
	if !ok || len(borrows) == 0 {
		return
	}
	// Validate first to avoid partial freeze updates.
	for _, info := range borrows {
		if !info.owner.isLocal() {
			continue
		}
		owner, _ := scope.Lookup(info.owner.localID)
		if owner == nil {
			continue
		}
		if info.mutable {
			if owner.frozen > 0 {
				a.reportBorrowConflict(info.loc, info.owner.localID, "cannot create mutable borrow while another borrow is live")
				return
			}
		} else if owner.mutFrozen > 0 {
			a.reportBorrowConflict(info.loc, info.owner.localID, "cannot create immutable borrow while a mutable borrow is live")
			return
		}
	}

	// Stable ordering + de-dupe for deterministic state merges.
	slices.SortFunc(borrows, func(a, b borrowInfo) int {
		if a.owner.localID < b.owner.localID {
			return -1
		}
		if a.owner.localID > b.owner.localID {
			return 1
		}
		if !a.mutable && b.mutable {
			return -1
		}
		if a.mutable && !b.mutable {
			return 1
		}
		return 0
	})
	uniq := borrows[:0]
	var lastOwner int = -1
	var lastMut bool
	for _, info := range borrows {
		if !info.owner.isLocal() {
			continue
		}
		if info.owner.localID == lastOwner && info.mutable == lastMut {
			continue
		}
		lastOwner = info.owner.localID
		lastMut = info.mutable
		uniq = append(uniq, info)
	}
	borrows = uniq

	slot.borrows = make([]borrowSlot, 0, len(borrows))
	for _, info := range borrows {
		if !info.owner.isLocal() {
			continue
		}
		owner, _ := scope.Lookup(info.owner.localID)
		if owner == nil {
			continue
		}
		owner.frozen++
		if info.mutable {
			owner.mutFrozen++
		}
		slot.borrows = append(slot.borrows, borrowSlot{owner: info.owner.localID, mutable: info.mutable, loc: info.loc})
	}
}

func (a *ownershipAnalyzer) releaseBorrowValue(scope *valueScope, slot *valueInfo) {
	if scope == nil || slot == nil || len(slot.borrows) == 0 {
		return
	}
	releaseBorrows(scope, slot)
}

func (a *ownershipAnalyzer) rebindBorrowAssignment(scope *valueScope, left mir.Place, right mir.Value) {
	if scope == nil {
		return
	}
	root, path, ok := a.localPlacePath(left)
	if !ok || path != "" {
		return
	}
	slot, ok := scope.Lookup(root)
	if !ok || slot == nil {
		return
	}
	a.bindBorrowValue(scope, slot, right)
}

func (a *ownershipAnalyzer) checkAssignmentTarget(scope *valueScope, left mir.Place) {
	root, path, ok := a.localPlacePath(left)
	if !ok || scope == nil {
		return
	}
	info, ok := scope.Lookup(root)
	if !ok || info == nil {
		return
	}
	if info.frozen > 0 {
		a.reportBorrowConflict(left.Loc(), root, "this value is currently borrowed")
	}
	if path == "" {
		a.releaseBorrowValue(scope, info)
		info.moved = false
		info.moveLoc = source.Location{}
		info.movedPath = ""
		info.movedSubs = nil
		return
	}
	clearMovedPath(info, path)
}

func (a *ownershipAnalyzer) localValuePath(value mir.Value) (root int, path string, ok bool) {
	switch v := value.(type) {
	case *mir.LocalValue:
		if info, ok := a.temps[v.LocalID]; ok && info.root.isLocal() {
			return info.root.localID, info.path, true
		}
		return v.LocalID, "", true
	case *mir.FieldLoadValue:
		root, path, ok := a.localValuePath(v.Base)
		if !ok {
			return -1, "", false
		}
		segment := a.fieldPathSegment(v.Base, v.FieldIndex)
		if path == "" {
			return root, segment, true
		}
		return root, path + "." + segment, true
	case *mir.FieldValue:
		root, path, ok := a.localValuePath(v.Base)
		if !ok {
			return -1, "", false
		}
		segment := a.fieldPathSegment(v.Base, v.FieldIndex)
		if segment == "" {
			return root, path, true
		}
		if path == "" {
			return root, segment, true
		}
		return root, path + "." + segment, true
	case *mir.InterfaceValue:
		return a.localValuePath(v.Value)
	}
	return -1, "", false
}

func (a *ownershipAnalyzer) localPlacePath(place mir.Place) (root int, path string, ok bool) {
	switch p := place.(type) {
	case *mir.LocalPlace:
		return p.LocalID, "", true
	case *mir.DerefPlace:
		// Local/field assignments lower to StoreInstr through a synthetic
		// DerefPlace whose pointer is AddrOf(resolved-place). Recover that
		// original local path so reassignment can reset moved/borrow state.
		addr, ok := p.Pointer.(*mir.AddrOfValue)
		if !ok || addr == nil {
			return -1, "", false
		}
		if name, ok := addr.Source.(*mir.NameValue); ok && len(name.Path) == 1 {
			if id := a.localIDByName(name.Path[0]); id >= 0 {
				return id, "", true
			}
		}
		return a.localValuePath(addr.Source)
	case *mir.FieldPlace:
		root, path, ok := a.localPlacePath(p.Base)
		if !ok {
			return -1, "", false
		}
		segment := a.fieldPathSegmentFromPlace(p.Base, p.FieldIndex)
		if path == "" {
			return root, segment, true
		}
		return root, path + "." + segment, true
	}
	return -1, "", false
}

func movedPathConflict(info *valueInfo, path string) (source.Location, string, bool) {
	if info == nil {
		return source.Location{}, "", false
	}
	if info.moved {
		return info.moveLoc, info.movedPath, true
	}
	if loc, ok := info.movedSubs[path]; ok {
		return loc, path, true
	}
	prefix := path + "."
	for movedPath, loc := range info.movedSubs {
		if len(movedPath) > len(prefix) && movedPath[:len(prefix)] == prefix {
			return loc, movedPath, true
		}
	}
	for ancestor := parentPath(path); ancestor != ""; ancestor = parentPath(ancestor) {
		if loc, ok := info.movedSubs[ancestor]; ok {
			return loc, ancestor, true
		}
	}
	return source.Location{}, "", false
}

func markMovedPath(info *valueInfo, path string, loc source.Location) {
	if info == nil || path == "" {
		return
	}
	if info.movedSubs == nil {
		info.movedSubs = make(map[string]source.Location)
	}
	info.movedSubs[path] = loc
}

func clearMovedPath(info *valueInfo, path string) {
	if info == nil || path == "" || len(info.movedSubs) == 0 {
		return
	}
	delete(info.movedSubs, path)
	prefix := path + "."
	for movedPath := range info.movedSubs {
		if len(movedPath) > len(prefix) && movedPath[:len(prefix)] == prefix {
			delete(info.movedSubs, movedPath)
		}
	}
	if len(info.movedSubs) == 0 {
		info.movedSubs = nil
	}
}

func parentPath(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			return path[:i]
		}
	}
	return ""
}

func (a *ownershipAnalyzer) requireActivePath(scope *valueScope, root int, path string, loc source.Location) {
	if scope == nil || root < 0 {
		return
	}
	info, ok := scope.Lookup(root)
	if !ok || info == nil {
		return
	}
	if info.moved {
		a.reportMovedPathUse(root, path, loc, info.moveLoc, info.movedPath)
		return
	}
	if a.hasActiveMutableBorrowOf(scope, root) {
		a.reportBorrowConflict(loc, root, "cannot use value while a mutable borrow is live")
		return
	}
	if path == "" {
		if len(info.movedSubs) > 0 {
			a.reportPartialMoveUse(root, loc, info)
		}
		return
	}
	if movedLoc, movedPath, ok := movedPathConflict(info, path); ok {
		a.reportMovedPathUse(root, path, loc, movedLoc, movedPath)
	}
}

func (a *ownershipAnalyzer) hasActiveMutableBorrowOf(scope *valueScope, root int) bool {
	if scope == nil || root < 0 {
		return false
	}
	slot, _ := scope.Lookup(root)
	return slot != nil && slot.mutFrozen > 0
}

func (a *ownershipAnalyzer) reportPartialMoveUse(root int, loc source.Location, info *valueInfo) {
	if info == nil || len(info.movedSubs) == 0 {
		return
	}
	var movedPath string
	var movedLoc source.Location
	for path, current := range info.movedSubs {
		movedPath, movedLoc = path, current
		break
	}
	display := a.localName(root)
	if display == "" {
		display = fmt.Sprintf("local#%d", root)
	}
	diag := diagnostics.NewError(fmt.Sprintf("use of partially moved value %q", display)).
		WithCode(diagnostics.ErrUseAfterMove).
		WithPrimaryLabel(&loc, "some fields of this value were already moved")
	if movedLoc.Start != nil {
		diag.WithSecondaryLabel(&movedLoc, fmt.Sprintf("field %q moved here", movedPath))
	}
	a.addDiagnostic(diag)
}

func (a *ownershipAnalyzer) reportMovedPathUse(root int, path string, loc source.Location, movedLoc source.Location, movedPath string) {
	display := a.localName(root)
	if display == "" {
		display = fmt.Sprintf("local#%d", root)
	}
	if path != "" {
		display += "." + path
	}
	diag := diagnostics.NewError(fmt.Sprintf("use of moved value %q", display)).
		WithCode(diagnostics.ErrUseAfterMove).
		WithPrimaryLabel(&loc, "this value was already moved")
	if movedLoc.Start != nil {
		label := "value moved here"
		if movedPath != "" {
			label = fmt.Sprintf("path %q moved here", movedPath)
		}
		diag.WithSecondaryLabel(&movedLoc, label)
	}
	a.addDiagnostic(diag)
}

func (a *ownershipAnalyzer) isMoveType(typ typeinfo.Type) bool {
	return a.isMoveTypeSeen(typ, make(map[string]struct{}))
}

func (a *ownershipAnalyzer) isMoveTypeSeen(typ typeinfo.Type, seen map[string]struct{}) bool {
	if typ == nil || typeinfo.IsInvalid(typ) || typeinfo.IsUnknown(typ) {
		return false
	}
	switch t := typ.(type) {
	case *typeinfo.PointerType:
		return true
	case *typeinfo.NamedType:
		key := t.ModuleKey + "::" + t.String()
		if _, ok := seen[key]; ok {
			return false
		}
		seen[key] = struct{}{}
		return a.isMoveTypeSeen(a.underlying(t), seen)
	case *typeinfo.StructType:
		for _, field := range t.OrderedFields {
			if field != nil && a.isMoveTypeSeen(field.Type, seen) {
				return true
			}
		}
		return false
	case *typeinfo.UnionType:
		for _, member := range t.Members {
			if a.isMoveTypeSeen(member, seen) {
				return true
			}
		}
		return false
	case *typeinfo.TupleType:
		for _, elem := range t.Elems {
			if a.isMoveTypeSeen(elem, seen) {
				return true
			}
		}
		return false
	case *typeinfo.ArrayType:
		return a.isMoveTypeSeen(t.Inner, seen)
	case *typeinfo.OptionalType:
		return a.isMoveTypeSeen(t.Inner, seen)
	case *typeinfo.ErrorUnionType:
		return a.isMoveTypeSeen(t.Error, seen) || a.isMoveTypeSeen(t.Value, seen)
	case *typeinfo.ApproxType:
		return a.isMoveTypeSeen(t.Inner, seen)
	case *typeinfo.RawPtrType, *typeinfo.RefType, *typeinfo.BuiltinType, *typeinfo.EnumType, *typeinfo.InterfaceType, *typeinfo.SliceType, *typeinfo.StringType:
		return false
	default:
		return false
	}
}

func (a *ownershipAnalyzer) consumeInterfaceReceiver(scope *valueScope, receiver mir.Value, receiverKind typeinfo.ReceiverKind, receiverType typeinfo.Type) {
	if receiverKind == typeinfo.ReceiverPtr {
		if root, path, ok := a.localValuePath(receiver); ok && path == "" {
			a.consumeLocalPath(scope, root, "", receiver.Loc())
		}
		return
	}
	if receiverKind != typeinfo.ReceiverValue {
		return
	}
	if root, path, ok := a.localValuePath(receiver); ok && path == "" && scope != nil {
		if info, ok := scope.Lookup(root); ok && info != nil {
			if info.concrete != nil {
				if a.isMoveType(info.concrete) {
					a.consumeLocalPath(scope, root, "", receiver.Loc())
				}
				return
			}
			if _, ok := a.interfaceView(info.typ); ok {
				a.consumeLocalPath(scope, root, "", receiver.Loc())
				return
			}
		}
	}
	a.consumeMoveValue(scope, receiver, receiverType)
}

func (a *ownershipAnalyzer) reportBorrowEscapeIfNeeded(scope *valueScope, value mir.Value, message string) {
	if value == nil {
		return
	}
	info, ok := a.borrowValueInfo(scope, value)
	if !ok {
		return
	}
	loc := value.Loc()
	diag := diagnostics.NewError(message).
		WithCode(diagnostics.ErrBorrowEscape).
		WithPrimaryLabel(&loc, "this borrow escapes its allowed scope")
	if info.loc.Start != nil {
		diag.WithSecondaryLabel(&info.loc, "borrow created here")
	}
	a.addDiagnostic(diag)
}

func (a *ownershipAnalyzer) borrowValueInfo(scope *valueScope, value mir.Value) (borrowInfo, bool) {
	infos, ok := a.borrowValueInfos(scope, value)
	if !ok || len(infos) == 0 {
		return borrowInfo{}, false
	}
	return infos[0], true
}

func (a *ownershipAnalyzer) borrowValueInfos(scope *valueScope, value mir.Value) ([]borrowInfo, bool) {
	if value == nil {
		return nil, false
	}
	switch v := value.(type) {
	case *mir.LocalValue:
		if info, ok := a.temps[v.LocalID]; ok && info.borrow != nil {
			return []borrowInfo{*info.borrow}, true
		}
		if scope != nil {
			if slot, _ := scope.Lookup(v.LocalID); slot != nil && len(slot.borrows) > 0 {
				out := make([]borrowInfo, 0, len(slot.borrows))
				for _, b := range slot.borrows {
					if b.owner < 0 {
						continue
					}
					out = append(out, borrowInfo{owner: ownerLocal(b.owner), loc: b.loc, mutable: b.mutable})
				}
				if len(out) > 0 {
					return out, true
				}
			}
		}
	case *mir.AddrOfValue:
		if v.Raw {
			return nil, false
		}
		root, _, ok := a.borrowSourcePath(v.Source)
		if !ok {
			return nil, false
		}
		return []borrowInfo{{owner: root, loc: v.Loc(), mutable: v.Mutable}}, true
	case *mir.UnaryValue:
		if v.Op == "&" || v.Op == "&mut" {
			root, _, ok := a.borrowSourcePath(v.Right)
			if !ok {
				return nil, false
			}
			return []borrowInfo{{owner: root, loc: v.Loc(), mutable: v.Op == "&mut"}}, true
		}
	case *mir.ClosureValue:
		var out []borrowInfo
		for _, cap := range v.Captures {
			infos, ok := a.borrowValueInfos(scope, cap)
			if !ok || len(infos) == 0 {
				continue
			}
			out = append(out, infos...)
		}
		if len(out) > 0 {
			return out, true
		}
	case *mir.CompositeValue:
		var out []borrowInfo
		for _, item := range v.Items {
			infos, ok := a.borrowValueInfos(scope, item.Value)
			if !ok || len(infos) == 0 {
				continue
			}
			out = append(out, infos...)
		}
		if len(out) > 0 {
			return out, true
		}
	case *mir.InterfaceValue:
		return a.borrowValueInfos(scope, v.Value)
	}
	return nil, false
}

func (a *ownershipAnalyzer) reportBorrowConflict(loc source.Location, localID int, message string) {
	name := a.localName(localID)
	if name == "" {
		name = fmt.Sprintf("local#%d", localID)
	}
	a.addDiagnostic(
		diagnostics.NewError(fmt.Sprintf("cannot use %q here", name)).
			WithCode(diagnostics.ErrBorrowConflict).
			WithPrimaryLabel(&loc, message),
	)
}

func (a *ownershipAnalyzer) fieldSelectorName(value *mir.FieldValue) string {
	if value == nil {
		return ""
	}
	if value.MemberName != "" {
		return value.MemberName
	}
	return mir.FieldName(valueType(value.Base), value.FieldIndex)
}

func (a *ownershipAnalyzer) fieldPathSegment(base mir.Value, index int) string {
	if name := mir.FieldName(valueType(base), index); name != "" {
		return name
	}
	return fmt.Sprintf("#%d", index)
}

func (a *ownershipAnalyzer) fieldPathSegmentFromPlace(base mir.Place, index int) string {
	if local, ok := base.(*mir.LocalPlace); ok {
		if name := mir.FieldName(a.localType(local.LocalID), index); name != "" {
			return name
		}
	}
	return fmt.Sprintf("#%d", index)
}

func (a *ownershipAnalyzer) fieldStorePlace(inst *mir.StoreFieldInstr) mir.Place {
	if inst == nil {
		return nil
	}
	return &mir.FieldPlace{
		Base:       a.placeFromValue(inst.Base),
		FieldIndex: inst.FieldIndex,
	}
}

func (a *ownershipAnalyzer) placeFromValue(value mir.Value) mir.Place {
	switch v := value.(type) {
	case *mir.LocalValue:
		return &mir.LocalPlace{LocalID: v.LocalID}
	case *mir.FieldLoadValue:
		return &mir.FieldPlace{Base: a.placeFromValue(v.Base), FieldIndex: v.FieldIndex}
	default:
		return nil
	}
}

func (a *ownershipAnalyzer) addDiagnostic(diag *diagnostics.Diagnostic) {
	if diag == nil {
		return
	}
	key := diag.Code + ":" + diag.Message
	for _, label := range diag.Labels {
		if label.Location == nil || label.Location.Start == nil {
			continue
		}
		file := ""
		if label.Location.Filename != nil {
			file = *label.Location.Filename
		}
		key += fmt.Sprintf(":%s:%d:%d:%s", file, label.Location.Start.Line, label.Location.Start.Column, label.Message)
	}
	if _, ok := a.reported[key]; ok {
		return
	}
	a.reported[key] = struct{}{}
	a.ctx.Diagnostics.Add(diag)
}

func (a *ownershipAnalyzer) underlying(typ typeinfo.Type) typeinfo.Type {
	if named, ok := typ.(*typeinfo.NamedType); ok && named.Decl != nil {
		owner := a.findModuleForType(named)
		if owner == nil {
			owner = a.mod
		}
		return typeFromTypeExpr(owner, named.Decl.Type)
	}
	return typ
}

func (a *ownershipAnalyzer) pathType(root typeinfo.Type, path string) typeinfo.Type {
	if root == nil || path == "" {
		return root
	}
	typ := root
	for _, name := range splitPath(path) {
		field := a.lookupStructField(typ, name)
		if field == nil {
			return typeinfo.UnknownType{}
		}
		typ = field.Type
	}
	if typ == nil {
		return typeinfo.UnknownType{}
	}
	return typ
}

func (a *ownershipAnalyzer) findModuleForType(typ *typeinfo.NamedType) *context.Module {
	if typ == nil {
		return nil
	}
	if mod, ok := a.ctx.GetModule(typ.ModuleKey); ok {
		return mod
	}
	return nil
}

func (a *ownershipAnalyzer) structView(typ typeinfo.Type) (*typeinfo.StructType, bool) {
	base := a.underlying(typ)
	st, ok := base.(*typeinfo.StructType)
	return st, ok
}

func (a *ownershipAnalyzer) lookupStructField(typ typeinfo.Type, name string) *typeinfo.StructField {
	structType, ok := a.structView(typeinfo.DerefForSelector(typ))
	if !ok || structType == nil {
		return nil
	}
	return structType.Fields[name]
}

func (a *ownershipAnalyzer) canHaveMethods(typ typeinfo.Type) bool {
	if typ == nil {
		return false
	}
	if _, ok := typeinfo.ReceiverBaseNamedType(typ); ok {
		return true
	}
	_, ok := a.interfaceView(typ)
	return ok
}

func (a *ownershipAnalyzer) interfaceView(typ typeinfo.Type) (*typeinfo.InterfaceType, bool) {
	if typ == nil {
		return nil, false
	}
	if iface, ok := a.underlying(typ).(*typeinfo.InterfaceType); ok {
		return iface, true
	}
	typeParam, ok := typ.(*typeinfo.TypeParam)
	if !ok || typeParam == nil || typeParam.Constraint == nil {
		return nil, false
	}
	if iface, ok := a.underlying(typeParam.Constraint).(*typeinfo.InterfaceType); ok {
		return iface, true
	}
	return nil, false
}

func (a *ownershipAnalyzer) lookupMethod(receiverType typeinfo.Type, name string, addressable bool, mutable bool) (*symbols.Symbol, *typeinfo.FuncType) {
	baseNamed, ok := typeinfo.ReceiverBaseNamedType(receiverType)
	if !ok {
		return nil, nil
	}
	owner := a.findModuleForType(baseNamed)
	if owner == nil || owner.MethodSets == nil {
		return nil, nil
	}
	for _, key := range a.methodCandidateKeys(receiverType, baseNamed.Name, addressable, mutable) {
		methods := owner.MethodSets[key]
		if methods == nil {
			continue
		}
		sym := methods[name]
		if sym == nil {
			continue
		}
		if owner.Types == nil {
			continue
		}
		if typ, ok := owner.Types.Symbols[sym.ID].(*typeinfo.FuncType); ok {
			return sym, typ
		}
	}
	return nil, nil
}

func (a *ownershipAnalyzer) receiverConsumes(mod *context.Module, fn *ast.FuncDecl) bool {
	if fn == nil || fn.Receiver == nil {
		return false
	}
	if mod == nil {
		mod = a.mod
	}
	return a.isMoveType(typeFromTypeExpr(mod, fn.Receiver.Type))
}

func (a *ownershipAnalyzer) methodCandidateKeys(receiverType typeinfo.Type, baseName string, addressable bool, mutable bool) []typeinfo.ReceiverKey {
	keys := make([]typeinfo.ReceiverKey, 0, 4)
	seen := make(map[typeinfo.ReceiverKey]struct{})
	add := func(key typeinfo.ReceiverKey) {
		if key.TypeName == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	switch t := receiverType.(type) {
	case *typeinfo.NamedType:
		add(typeinfo.ReceiverKey{TypeName: baseName})
	case *typeinfo.RefType:
		if exact, ok := typeinfo.ReceiverKeyFromType(t); ok {
			add(exact)
		}
		if t.Mutable {
			add(typeinfo.ReceiverKey{Kind: typeinfo.ReceiverRef, TypeName: baseName})
		}
	case *typeinfo.PointerType:
		if exact, ok := typeinfo.ReceiverKeyFromType(t); ok {
			add(exact)
		}
	}
	return keys
}

func (a *ownershipAnalyzer) valueAccess(scope *valueScope, value mir.Value) (addressable bool, mutable bool) {
	switch v := value.(type) {
	case *mir.NameValue:
		if len(v.Path) != 1 {
			return false, false
		}
		id := a.localIDByName(v.Path[0])
		if id < 0 {
			return false, false
		}
		if info, ok := scope.Lookup(id); ok && info != nil {
			return true, info.mutable && !info.constant
		}
		return false, false
	case *mir.LocalValue:
		if info, ok := scope.Lookup(v.LocalID); ok && info != nil {
			return true, info.mutable && !info.constant
		}
		return false, false
	case *mir.FieldLoadValue:
		return a.valueAccess(scope, v.Base)
	case *mir.LoadValue:
		switch t := valueType(v).(type) {
		case *typeinfo.PointerType:
			_ = t
			return true, true
		default:
			return true, false
		}
	case *mir.FieldValue:
		return a.valueAccess(scope, v.Base)
	case *mir.UnaryValue:
		if v.Op == "*" {
			switch t := valueType(v).(type) {
			case *typeinfo.PointerType:
				_ = t
				return true, true
			default:
				return true, false
			}
		}
		return false, false
	default:
		return false, false
	}
}

func (a *ownershipAnalyzer) findCandidateModuleForSymbol(sym *symbols.Symbol) *context.Module {
	if sym == nil {
		return nil
	}
	for _, mod := range a.ctx.Modules() {
		if mod == nil {
			continue
		}
		if mod.ModuleScope != nil {
			if slices.Contains(mod.ModuleScope.Symbols(), sym) {
				return mod
			}
		}
		for _, methods := range mod.MethodSets {
			for _, candidate := range methods {
				if candidate == sym {
					return mod
				}
			}
		}
	}
	return nil
}

func typeFromTypeExpr(mod *context.Module, expr ast.TypeExpr) typeinfo.Type {
	if mod == nil || mod.Types == nil || expr == nil {
		return nil
	}
	return mod.Types.Nodes[expr]
}

func valueType(value mir.Value) typeinfo.Type {
	if value == nil {
		return typeinfo.UnknownType{}
	}
	if typ := value.Type(); typ != nil {
		return typ
	}
	return typeinfo.UnknownType{}
}

func (a *ownershipAnalyzer) placeType(scope *valueScope, place mir.Place) typeinfo.Type {
	if place == nil {
		return typeinfo.UnknownType{}
	}
	root, path, ok := a.localPlacePath(place)
	if !ok || scope == nil {
		return typeinfo.UnknownType{}
	}
	info, ok := scope.Lookup(root)
	if !ok || info == nil || info.typ == nil {
		return typeinfo.UnknownType{}
	}
	typ := info.typ
	if path == "" {
		return typ
	}
	for _, name := range splitPath(path) {
		field := a.lookupStructField(typ, name)
		if field == nil {
			return typeinfo.UnknownType{}
		}
		typ = field.Type
	}
	if typ == nil {
		return typeinfo.UnknownType{}
	}
	return typ
}

func splitPath(path string) []string {
	if path == "" {
		return nil
	}
	parts := make([]string, 0, 4)
	start := 0
	for i := 0; i <= len(path); i++ {
		if i != len(path) && path[i] != '.' {
			continue
		}
		parts = append(parts, path[start:i])
		start = i + 1
	}
	return parts
}
