package ownership

import (
	"fmt"
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

type valueInfo struct {
	typ       typeinfo.Type
	mutable   bool
	constant  bool
	moved     bool
	moveLoc   source.Location
	movedPath string
	movedSubs map[string]source.Location
	frozen    int
	mutBorrow bool
	borrowOf  int
	borrowMut bool
	borrowLoc source.Location
}

type valueScope struct {
	values map[int]*valueInfo
}

func newValueScope() *valueScope {
	return &valueScope{values: make(map[int]*valueInfo)}
}

func (s *valueScope) Declare(id int, info valueInfo) *valueInfo {
	if s == nil || id < 0 {
		return nil
	}
	slot := &valueInfo{
		typ:      info.typ,
		mutable:  info.mutable,
		constant: info.constant,
		borrowOf: -1,
	}
	if len(info.movedSubs) > 0 {
		slot.movedSubs = cloneMovedSubs(info.movedSubs)
	}
	s.values[id] = slot
	return slot
}

func (s *valueScope) Lookup(id int) (*valueInfo, bool) {
	if s == nil {
		return nil, false
	}
	info, ok := s.values[id]
	return info, ok
}

func (s *valueScope) Clone() *valueScope {
	if s == nil {
		return nil
	}
	out := newValueScope()
	for id, info := range s.values {
		if info == nil {
			continue
		}
		clone := *info
		if len(info.movedSubs) > 0 {
			clone.movedSubs = cloneMovedSubs(info.movedSubs)
		}
		out.values[id] = &clone
	}
	return out
}

func (s *valueScope) TrimToLiveOut(live cfg.LocalSet) {
	if s == nil {
		return
	}
	for id, info := range s.values {
		if info == nil || live.Has(id) {
			continue
		}
		if info.borrowOf >= 0 {
			if owner, ok := s.values[info.borrowOf]; ok && owner != nil && owner.frozen > 0 {
				owner.frozen--
			}
		}
		info.moved = false
		info.moveLoc = source.Location{}
		info.movedPath = ""
		info.movedSubs = nil
		info.frozen = 0
		info.mutBorrow = false
		info.borrowOf = -1
		info.borrowMut = false
		info.borrowLoc = source.Location{}
	}
}

func (s *valueScope) MergeFrom(other *valueScope, live cfg.LocalSet) bool {
	if s == nil || other == nil {
		return false
	}
	changed := false
	for id, incoming := range other.values {
		if incoming == nil || (len(live) > 0 && !live.Has(id)) {
			continue
		}
		current, ok := s.values[id]
		if !ok || current == nil {
			clone := *incoming
			s.values[id] = &clone
			changed = true
			continue
		}
		merged := mergeValueInfo(current, incoming)
		if !equalValueInfo(current, merged) {
			s.values[id] = merged
			changed = true
		}
	}
	return changed
}

func equalValueInfo(a, b *valueInfo) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.typ == b.typ &&
		a.mutable == b.mutable &&
		a.constant == b.constant &&
		a.moved == b.moved &&
		a.moveLoc == b.moveLoc &&
		a.movedPath == b.movedPath &&
		equalMovedSubs(a.movedSubs, b.movedSubs) &&
		a.frozen == b.frozen &&
		a.mutBorrow == b.mutBorrow &&
		a.borrowOf == b.borrowOf &&
		a.borrowMut == b.borrowMut &&
		a.borrowLoc == b.borrowLoc
}

func mergeValueInfo(a, b *valueInfo) *valueInfo {
	if a == nil && b == nil {
		return nil
	}
	if a == nil {
		clone := *b
		return &clone
	}
	if b == nil {
		clone := *a
		return &clone
	}
	out := *a
	out.mutable = a.mutable || b.mutable
	out.constant = a.constant && b.constant
	out.moved = a.moved || b.moved
	if out.moved {
		if a.moveLoc.Start != nil {
			out.moveLoc = a.moveLoc
			out.movedPath = a.movedPath
		} else {
			out.moveLoc = b.moveLoc
			out.movedPath = b.movedPath
		}
	}
	if !out.moved {
		out.movedSubs = mergeMovedSubs(a.movedSubs, b.movedSubs)
	} else {
		out.movedSubs = nil
	}
	if b.frozen > out.frozen {
		out.frozen = b.frozen
	}
	out.mutBorrow = a.mutBorrow || b.mutBorrow
	if a.borrowOf == b.borrowOf {
		out.borrowOf = a.borrowOf
		out.borrowMut = a.borrowMut || b.borrowMut
		if a.borrowLoc.Start != nil {
			out.borrowLoc = a.borrowLoc
		} else {
			out.borrowLoc = b.borrowLoc
		}
	} else {
		out.borrowOf = -1
		out.borrowMut = false
		out.borrowLoc = source.Location{}
	}
	return &out
}

func cloneMovedSubs(in map[string]source.Location) map[string]source.Location {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]source.Location, len(in))
	for key, loc := range in {
		out[key] = loc
	}
	return out
}

func equalMovedSubs(a, b map[string]source.Location) bool {
	if len(a) != len(b) {
		return false
	}
	for key, loc := range a {
		other, ok := b[key]
		if !ok || other != loc {
			return false
		}
	}
	return true
}

func mergeMovedSubs(a, b map[string]source.Location) map[string]source.Location {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := cloneMovedSubs(a)
	if out == nil {
		out = make(map[string]source.Location, len(b))
	}
	for key, loc := range b {
		if _, ok := out[key]; !ok {
			out[key] = loc
		}
	}
	return out
}

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

type analyzer struct {
	ctx       *context.CompilerContext
	mod       *context.Module
	module    *mir.Module
	currentFn *mir.Function
	temps     map[int]tempInfo
	reported  map[string]struct{}
}

func AnalyzeModule(ctx *context.CompilerContext, mod *context.Module) {
	if ctx == nil || mod == nil || mod.MIR == nil || mod.CFG == nil {
		return
	}
	a := &analyzer{
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

func (a *analyzer) localName(id int) string {
	if a == nil || a.currentFn == nil {
		return ""
	}
	return a.currentFn.LocalName(id)
}

func (a *analyzer) localIDByName(name string) int {
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

func (a *analyzer) localType(id int) typeinfo.Type {
	if a == nil || a.currentFn == nil {
		return typeinfo.UnknownType{}
	}
	return a.currentFn.LocalType(id)
}

func (a *analyzer) checkGlobal(global *mir.Global) {
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

func (a *analyzer) checkFunc(cfgFn *cfg.Function, mirFn *mir.Function) {
	if cfgFn == nil || mirFn == nil || cfgFn.Entry == nil {
		return
	}
	a.currentFn = mirFn
	defer func() { a.currentFn = nil }()
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

func (a *analyzer) seedFunctionState(fn *mir.Function) *valueScope {
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

func (a *analyzer) transferBlock(in *valueScope, cfgBlock *cfg.Block, mirBlock *mir.Block) *valueScope {
	state := in.Clone()
	if state == nil {
		state = newValueScope()
	}
	for _, info := range a.temps {
		_ = info
	}
	a.temps = make(map[int]tempInfo)
	if mirBlock != nil {
		for _, instr := range mirBlock.Instructions {
			a.checkMIRInstr(state, instr)
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

func (a *analyzer) checkMIRInstr(scope *valueScope, instr mir.Instr) {
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
			slot.moved = false
			slot.moveLoc = source.Location{}
			slot.movedPath = ""
			slot.movedSubs = nil
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
			slot.moved = false
			slot.moveLoc = source.Location{}
			slot.movedPath = ""
			slot.movedSubs = nil
		}
	case *mir.UnsafeInstr:
		return
	}
}

func (a *analyzer) checkDeferredInstr(scope *valueScope, instr mir.Instr) {
	switch inst := instr.(type) {
	case nil:
		return
	case *mir.ComputeInstr:
		a.checkComputedValue(scope, inst)
		a.reportBorrowEscapeIfNeeded(scope, inst.Value, "borrow cannot escape into defer")
	case *mir.BindInstr:
		a.checkValue(scope, inst.Value)
		a.reportBorrowEscapeIfNeeded(scope, inst.Value, "borrow cannot escape into defer")
	case *mir.StoreInstr:
		a.checkPlaceValue(scope, inst.Target)
		a.checkValue(scope, inst.Value)
		a.reportBorrowEscapeIfNeeded(scope, inst.Value, "borrow cannot escape into defer")
	case *mir.AssignInstr:
		a.checkValue(scope, inst.Value)
		a.reportBorrowEscapeIfNeeded(scope, inst.Value, "borrow cannot escape into defer")
	case *mir.StoreFieldInstr:
		a.checkValue(scope, inst.Base)
		a.checkValue(scope, inst.Value)
		a.reportBorrowEscapeIfNeeded(scope, inst.Base, "borrow cannot escape into defer")
		a.reportBorrowEscapeIfNeeded(scope, inst.Value, "borrow cannot escape into defer")
	case *mir.EvalInstr:
		a.checkValue(scope, inst.Value)
		a.reportBorrowEscapeIfNeeded(scope, inst.Value, "borrow cannot escape into defer")
	case *mir.DeferInstr:
		for _, child := range inst.Body {
			a.checkDeferredInstr(scope, child)
		}
	case *mir.LockInstr:
		a.checkValue(scope, inst.Value)
		a.reportBorrowEscapeIfNeeded(scope, inst.Value, "borrow cannot escape into defer")
	case *mir.UnsafeInstr:
		return
	}
}

func (a *analyzer) checkMIRTerm(scope *valueScope, term mir.Terminator) {
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

func (a *analyzer) checkComputedValue(scope *valueScope, instr *mir.ComputeInstr) {
	if instr == nil {
		return
	}
	a.checkValue(scope, instr.Value)
	if info, ok := a.tempInfoForValue(instr.Value); ok {
		a.temps[instr.TargetID] = info
	}
}

func (a *analyzer) checkValue(scope *valueScope, value mir.Value) {
	switch v := value.(type) {
	case nil, *mir.NumberValue, *mir.BoolValue, *mir.StringValue, *mir.NoneValue:
		return
	case *mir.LocalValue:
		a.requireActiveLocal(scope, v)
	case *mir.NameValue:
		a.requireActiveValue(scope, v)
	case *mir.FieldLoadValue:
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

func (a *analyzer) checkPlaceValue(scope *valueScope, place mir.Place) {
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

func (a *analyzer) checkFieldValue(scope *valueScope, value *mir.FieldValue) {
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

func (a *analyzer) checkFieldLoadValue(scope *valueScope, value *mir.FieldLoadValue) {
	if value == nil {
		return
	}
	if root, path, ok := a.localValuePath(value); ok {
		a.requireActivePath(scope, root, path, value.Loc())
		return
	}
	a.checkValue(scope, value.Base)
}

func (a *analyzer) checkAddrOfValue(scope *valueScope, value *mir.AddrOfValue) {
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
	if owner.mutBorrow {
		a.reportBorrowConflict(value.Loc(), root.localID, "cannot create immutable borrow while a mutable borrow is live")
	}
}

func (a *analyzer) checkLoadValue(scope *valueScope, value *mir.LoadValue) {
	if value == nil {
		return
	}
	a.checkValue(scope, value.Pointer)
}

func (a *analyzer) checkCall(scope *valueScope, call *mir.CallValue) {
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
		for _, arg := range call.Args {
			a.checkValue(scope, arg)
		}
		return
	}
	for i, arg := range call.Args {
		a.checkValue(scope, arg)
		if i < len(fnType.Params) {
			a.consumeMoveValue(scope, arg, fnType.Params[i])
		}
	}
}

// checkNormalizedMethodCall handles method calls that have been normalized in MIR so
// that the receiver is Args[0] and ReceiverType is set on the CallValue.
func (a *analyzer) checkNormalizedMethodCall(scope *valueScope, call *mir.CallValue) {
	receiver := call.Args[0]
	receiverType := call.ReceiverType
	a.checkValue(scope, receiver)

	// Look up the method symbol so we can check if it consumes the receiver.
	methodName := ""
	if name, ok := call.Callee.(*mir.NameValue); ok && len(name.Path) > 0 {
		methodName = name.Path[len(name.Path)-1]
	}
	if methodName != "" && !typeinfo.IsInvalid(receiverType) && !typeinfo.IsUnknown(receiverType) {
		if baseNamed, ok := a.receiverBaseNamedType(receiverType); ok && baseNamed != nil {
			prefix := baseNamed.Name + "__"
			methodName = strings.TrimPrefix(methodName, prefix)
		}
		addressable, mutable := a.valueAccess(scope, receiver)
		methodSym, methodType := a.lookupMethod(receiverType, methodName, addressable, mutable)
		if methodType != nil {
			for i, arg := range call.Args[1:] {
				a.checkValue(scope, arg)
				if i < len(methodType.Params) {
					a.consumeMoveValue(scope, arg, methodType.Params[i])
				}
			}
			if methodSym != nil {
				if fn, ok := methodSym.Node.(*ast.FuncDecl); ok && a.receiverConsumes(a.findModuleForSymbol(methodSym), fn) {
					a.consumeMoveValue(scope, receiver, receiverType)
				}
			}
			return
		}
	}
	// Fallback: just check all args normally.
	for _, arg := range call.Args[1:] {
		a.checkValue(scope, arg)
	}
}

func (a *analyzer) checkMethodCall(scope *valueScope, call *mir.CallValue, field *mir.FieldValue) bool {
	a.checkValue(scope, field.Base)
	receiverType := valueType(field.Base)
	if typeinfo.IsInvalid(receiverType) || typeinfo.IsUnknown(receiverType) {
		return true
	}
	name := a.fieldSelectorName(field)
	if structField := a.lookupStructField(receiverType, name); structField != nil {
		return false
	}
	if iface, ok := a.underlying(receiverType).(*typeinfo.InterfaceType); ok {
		method := iface.Methods[name]
		if method == nil {
			return true
		}
		for i, arg := range call.Args {
			a.checkValue(scope, arg)
			if i < len(method.Params) {
				a.consumeMoveValue(scope, arg, method.Params[i])
			}
		}
		return true
	}
	addressable, mutable := a.valueAccess(scope, field.Base)
	methodSym, methodType := a.lookupMethod(receiverType, name, addressable, mutable)
	if methodType == nil {
		return a.canHaveMethods(receiverType)
	}
	for i, arg := range call.Args {
		a.checkValue(scope, arg)
		if i < len(methodType.Params) {
			a.consumeMoveValue(scope, arg, methodType.Params[i])
		}
	}
	if methodSym != nil {
		if fn, ok := methodSym.Node.(*ast.FuncDecl); ok && a.receiverConsumes(a.findModuleForSymbol(methodSym), fn) {
			a.consumeMoveValue(scope, field.Base, receiverType)
		}
	}
	return true
}

func (a *analyzer) requireActiveValue(scope *valueScope, value *mir.NameValue) {
	// Globals and other non-local names are not tracked as movable slots.
	_ = scope
	_ = value
}

func (a *analyzer) requireActiveLocal(scope *valueScope, value *mir.LocalValue) {
	if value == nil {
		return
	}
	if info, ok := a.temps[value.LocalID]; ok && info.root.isLocal() {
		a.requireActivePath(scope, info.root.localID, info.path, value.Loc())
		return
	}
	a.requireActivePath(scope, value.LocalID, "", value.Loc())
}

func (a *analyzer) consumeMoveValue(scope *valueScope, value mir.Value, typ typeinfo.Type) {
	if value == nil || typ == nil || !a.isMoveType(typ) {
		return
	}
	switch v := value.(type) {
	case *mir.UnaryValue:
		if v.Op == "copy" {
			return
		}
	case *mir.InterfaceValue:
		a.consumeMoveValue(scope, v.Value, v.ConcreteType)
		return
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

func (a *analyzer) consumeLocalPath(scope *valueScope, root int, path string, loc source.Location) {
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

func (a *analyzer) tempInfoForValue(value mir.Value) (tempInfo, bool) {
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

func (a *analyzer) borrowSourcePath(value mir.Value) (ownerRef, string, bool) {
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

func (a *analyzer) bindBorrowValue(scope *valueScope, slot *valueInfo, value mir.Value) {
	if scope == nil || slot == nil {
		return
	}
	a.releaseBorrowValue(scope, slot)
	if value == nil {
		return
	}
	info, ok := a.borrowValueInfo(scope, value)
	if !ok || !info.owner.isLocal() {
		return
	}
	owner, _ := scope.Lookup(info.owner.localID)
	if owner == nil {
		return
	}
	if info.mutable {
		if owner.frozen > 0 {
			a.reportBorrowConflict(info.loc, info.owner.localID, "cannot create mutable borrow while another borrow is live")
			return
		}
	} else if owner.mutBorrow {
		a.reportBorrowConflict(info.loc, info.owner.localID, "cannot create immutable borrow while a mutable borrow is live")
		return
	}
	slot.borrowOf = info.owner.localID
	slot.borrowMut = info.mutable
	slot.borrowLoc = info.loc
	owner.frozen++
	if info.mutable {
		owner.mutBorrow = true
	}
}

func (a *analyzer) releaseBorrowValue(scope *valueScope, slot *valueInfo) {
	if scope == nil || slot == nil || slot.borrowOf < 0 {
		return
	}
	if owner, ok := scope.Lookup(slot.borrowOf); ok && owner != nil && owner.frozen > 0 {
		owner.frozen--
		if slot.borrowMut && owner.frozen == 0 {
			owner.mutBorrow = false
		}
	}
	slot.borrowOf = -1
	slot.borrowMut = false
	slot.borrowLoc = source.Location{}
}

func (a *analyzer) rebindBorrowAssignment(scope *valueScope, left mir.Place, right mir.Value) {
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

func (a *analyzer) checkAssignmentTarget(scope *valueScope, left mir.Place) {
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

func (a *analyzer) localValuePath(value mir.Value) (root int, path string, ok bool) {
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

func (a *analyzer) localPlacePath(place mir.Place) (root int, path string, ok bool) {
	switch p := place.(type) {
	case *mir.LocalPlace:
		return p.LocalID, "", true
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

func (a *analyzer) requireActivePath(scope *valueScope, root int, path string, loc source.Location) {
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

func (a *analyzer) reportPartialMoveUse(root int, loc source.Location, info *valueInfo) {
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

func (a *analyzer) reportMovedPathUse(root int, path string, loc source.Location, movedLoc source.Location, movedPath string) {
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

func (a *analyzer) isMoveType(typ typeinfo.Type) bool {
	if typ == nil || typeinfo.IsInvalid(typ) || typeinfo.IsUnknown(typ) {
		return false
	}
	switch t := typ.(type) {
	case *typeinfo.PointerType:
		_ = t
		return true
	case *typeinfo.RawPtrType, *typeinfo.RefType, *typeinfo.BuiltinType, *typeinfo.EnumType, *typeinfo.NamedType:
		return false
	default:
		return false
	}
}

func (a *analyzer) canDeepCopyType(typ typeinfo.Type) (bool, string) {
	return a.canDeepCopyTypeSeen(typ, map[typeinfo.Type]struct{}{}, map[string]struct{}{})
}

func (a *analyzer) canDeepCopyTypeSeen(typ typeinfo.Type, seen map[typeinfo.Type]struct{}, seenNamed map[string]struct{}) (bool, string) {
	if typ == nil || typeinfo.IsInvalid(typ) || typeinfo.IsUnknown(typ) {
		return true, ""
	}
	if named, ok := typ.(*typeinfo.NamedType); ok && named != nil {
		key := named.ModuleKey + "::" + named.Name
		if _, ok := seenNamed[key]; ok {
			return true, ""
		}
		seenNamed[key] = struct{}{}
	}
	if _, ok := seen[typ]; ok {
		return true, ""
	}
	seen[typ] = struct{}{}
	switch t := typ.(type) {
	case *typeinfo.NamedType:
		if t.Decl == nil {
			return true, ""
		}
		owner := a.findModuleForType(t)
		if owner == nil {
			return true, ""
		}
		return a.canDeepCopyTypeSeen(syntaxType(owner, t.Decl.Type), seen, seenNamed)
	case *typeinfo.PointerType:
		return false, fmt.Sprintf("deep copy of owning pointer type %s is not implemented yet", typ.String())
	case *typeinfo.RawPtrType:
		return false, fmt.Sprintf("cannot deep copy raw pointer type %s", typ.String())
	case *typeinfo.RefType:
		return false, fmt.Sprintf("cannot deep copy reference type %s", typ.String())
	case *typeinfo.OptionalType:
		return a.canDeepCopyTypeSeen(t.Inner, seen, seenNamed)
	case *typeinfo.ErrorUnionType:
		if ok, msg := a.canDeepCopyTypeSeen(t.Error, seen, seenNamed); !ok {
			return false, msg
		}
		return a.canDeepCopyTypeSeen(t.Value, seen, seenNamed)
	case *typeinfo.ArrayType:
		return a.canDeepCopyTypeSeen(t.Inner, seen, seenNamed)
	case *typeinfo.SliceType:
		return a.canDeepCopyTypeSeen(t.Inner, seen, seenNamed)
	case *typeinfo.TupleType:
		for _, elem := range t.Elems {
			if ok, msg := a.canDeepCopyTypeSeen(elem, seen, seenNamed); !ok {
				return false, msg
			}
		}
		return true, ""
	case *typeinfo.StructType:
		for _, field := range t.OrderedFields {
			if field == nil {
				continue
			}
			if ok, msg := a.canDeepCopyTypeSeen(field.Type, seen, seenNamed); !ok {
				return false, msg
			}
		}
		for _, field := range t.OrderedStaticFields {
			if field == nil {
				continue
			}
			if ok, msg := a.canDeepCopyTypeSeen(field.Type, seen, seenNamed); !ok {
				return false, msg
			}
		}
		return true, ""
	case *typeinfo.UnionType:
		for _, member := range t.Members {
			if ok, msg := a.canDeepCopyTypeSeen(member, seen, seenNamed); !ok {
				return false, msg
			}
		}
		return true, ""
	default:
		return true, ""
	}
}

func (a *analyzer) reportBorrowEscapeIfNeeded(scope *valueScope, value mir.Value, message string) {
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

func (a *analyzer) borrowValueInfo(scope *valueScope, value mir.Value) (borrowInfo, bool) {
	if value == nil {
		return borrowInfo{}, false
	}
	switch v := value.(type) {
	case *mir.LocalValue:
		if info, ok := a.temps[v.LocalID]; ok && info.borrow != nil {
			return *info.borrow, true
		}
		if scope != nil {
			if slot, _ := scope.Lookup(v.LocalID); slot != nil && slot.borrowOf >= 0 {
				return borrowInfo{owner: ownerLocal(slot.borrowOf), loc: slot.borrowLoc, mutable: slot.borrowMut}, true
			}
		}
	case *mir.AddrOfValue:
		if v.Raw {
			return borrowInfo{}, false
		}
		root, _, ok := a.borrowSourcePath(v.Source)
		if !ok {
			return borrowInfo{}, false
		}
		return borrowInfo{owner: root, loc: v.Loc(), mutable: v.Mutable}, true
	case *mir.UnaryValue:
		if v.Op == "&" || v.Op == "&mut" {
			root, _, ok := a.borrowSourcePath(v.Right)
			if !ok {
				return borrowInfo{}, false
			}
			return borrowInfo{owner: root, loc: v.Loc(), mutable: v.Op == "&mut"}, true
		}
	case *mir.InterfaceValue:
		return a.borrowValueInfo(scope, v.Value)
	}
	return borrowInfo{}, false
}

func (a *analyzer) reportBorrowConflict(loc source.Location, localID int, message string) {
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

func (a *analyzer) fieldSelectorName(value *mir.FieldValue) string {
	if value == nil {
		return ""
	}
	if value.MemberName != "" {
		return value.MemberName
	}
	return mir.FieldName(valueType(value.Base), value.FieldIndex)
}

func (a *analyzer) fieldPathSegment(base mir.Value, index int) string {
	if name := mir.FieldName(valueType(base), index); name != "" {
		return name
	}
	return fmt.Sprintf("#%d", index)
}

func (a *analyzer) fieldPathSegmentFromPlace(base mir.Place, index int) string {
	if local, ok := base.(*mir.LocalPlace); ok {
		if name := mir.FieldName(a.localType(local.LocalID), index); name != "" {
			return name
		}
	}
	return fmt.Sprintf("#%d", index)
}

func (a *analyzer) fieldStorePlace(inst *mir.StoreFieldInstr) mir.Place {
	if inst == nil {
		return nil
	}
	return &mir.FieldPlace{
		Base:       a.placeFromValue(inst.Base),
		FieldIndex: inst.FieldIndex,
	}
}

func (a *analyzer) placeFromValue(value mir.Value) mir.Place {
	switch v := value.(type) {
	case *mir.LocalValue:
		return &mir.LocalPlace{LocalID: v.LocalID}
	case *mir.FieldLoadValue:
		return &mir.FieldPlace{Base: a.placeFromValue(v.Base), FieldIndex: v.FieldIndex}
	default:
		return nil
	}
}

func (a *analyzer) addDiagnostic(diag *diagnostics.Diagnostic) {
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

func (a *analyzer) underlying(typ typeinfo.Type) typeinfo.Type {
	if named, ok := typ.(*typeinfo.NamedType); ok && named.Decl != nil {
		owner := a.findModuleForType(named)
		if owner == nil {
			owner = a.mod
		}
		return syntaxType(owner, named.Decl.Type)
	}
	return typ
}

func (a *analyzer) pathType(root typeinfo.Type, path string) typeinfo.Type {
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

func (a *analyzer) findModuleForType(typ *typeinfo.NamedType) *context.Module {
	if typ == nil {
		return nil
	}
	if mod, ok := a.ctx.GetModule(typ.ModuleKey); ok {
		return mod
	}
	return nil
}

func (a *analyzer) structView(typ typeinfo.Type) (*typeinfo.StructType, bool) {
	base := a.underlying(typ)
	st, ok := base.(*typeinfo.StructType)
	return st, ok
}

func (a *analyzer) derefForSelector(typ typeinfo.Type) typeinfo.Type {
	switch t := typ.(type) {
	case *typeinfo.PointerType:
		return t.Inner
	case *typeinfo.RefType:
		return t.Inner
	default:
		return typ
	}
}

func (a *analyzer) lookupStructField(typ typeinfo.Type, name string) *typeinfo.StructField {
	structType, ok := a.structView(a.derefForSelector(typ))
	if !ok || structType == nil {
		return nil
	}
	return structType.Fields[name]
}

func (a *analyzer) canHaveMethods(typ typeinfo.Type) bool {
	if typ == nil {
		return false
	}
	if _, ok := a.receiverBaseNamedType(typ); ok {
		return true
	}
	_, ok := a.underlying(typ).(*typeinfo.InterfaceType)
	return ok
}

func (a *analyzer) lookupMethod(receiverType typeinfo.Type, name string, addressable bool, mutable bool) (*symbols.Symbol, *typeinfo.FuncType) {
	baseNamed, ok := a.receiverBaseNamedType(receiverType)
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
		if typ, ok := owner.Types.Symbols[sym].(*typeinfo.FuncType); ok {
			return sym, typ
		}
	}
	return nil, nil
}

func (a *analyzer) receiverConsumes(mod *context.Module, fn *ast.FuncDecl) bool {
	if fn == nil || fn.Receiver == nil {
		return false
	}
	if mod == nil {
		mod = a.mod
	}
	return a.isMoveType(syntaxType(mod, fn.Receiver.Type))
}

func (a *analyzer) methodCandidateKeys(receiverType typeinfo.Type, baseName string, addressable bool, mutable bool) []string {
	keys := make([]string, 0, 4)
	seen := make(map[string]struct{})
	add := func(key string) {
		if key == "" {
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
		add(baseName)
	case *typeinfo.RefType:
		if exact, ok := a.receiverKeyFromType(t); ok {
			add(exact)
		}
		if t.Mutable {
			add("&" + baseName)
		}
	case *typeinfo.PointerType:
		if exact, ok := a.receiverKeyFromType(t); ok {
			add(exact)
		}
	}
	return keys
}

func (a *analyzer) receiverBaseNamedType(typ typeinfo.Type) (*typeinfo.NamedType, bool) {
	switch t := typ.(type) {
	case *typeinfo.NamedType:
		return t, true
	case *typeinfo.RefType:
		named, ok := t.Inner.(*typeinfo.NamedType)
		return named, ok
	case *typeinfo.PointerType:
		named, ok := t.Inner.(*typeinfo.NamedType)
		return named, ok
	default:
		return nil, false
	}
}

func (a *analyzer) receiverKeyFromType(typ typeinfo.Type) (string, bool) {
	switch t := typ.(type) {
	case *typeinfo.NamedType:
		return t.Name, true
	case *typeinfo.RefType:
		named, ok := t.Inner.(*typeinfo.NamedType)
		if !ok {
			return "", false
		}
		prefix := "&"
		if t.Mutable {
			prefix = "&mut "
		}
		return prefix + named.Name, true
	case *typeinfo.PointerType:
		named, ok := t.Inner.(*typeinfo.NamedType)
		if !ok {
			return "", false
		}
		return "*" + named.Name, true
	default:
		return "", false
	}
}

func (a *analyzer) valueAccess(scope *valueScope, value mir.Value) (addressable bool, mutable bool) {
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

func (a *analyzer) findModuleForSymbol(sym *symbols.Symbol) *context.Module {
	if sym == nil {
		return nil
	}
	for _, mod := range a.ctx.Modules() {
		if mod == nil {
			continue
		}
		if mod.ModuleScope != nil {
			for _, candidate := range mod.ModuleScope.Symbols() {
				if candidate == sym {
					return mod
				}
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

func syntaxType(mod *context.Module, expr ast.TypeExpr) typeinfo.Type {
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

func (a *analyzer) placeType(scope *valueScope, place mir.Place) typeinfo.Type {
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
