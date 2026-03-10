package ownership

import (
	"fmt"

	"compiler/internal/cfg"
	"compiler/internal/context"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/middleend/hir"
	"compiler/internal/phase"
	"compiler/internal/semantics/binding"
	"compiler/internal/semantics/symbols"
	"compiler/internal/semantics/typeinfo"
	"compiler/internal/source"
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
	borrowOf  string
	borrowLoc source.Location
}

type valueScope struct {
	values map[string]*valueInfo
}

func newValueScope() *valueScope {
	return &valueScope{values: make(map[string]*valueInfo)}
}

func (s *valueScope) Declare(name string, info valueInfo) *valueInfo {
	if s == nil || name == "" {
		return nil
	}
	slot := &valueInfo{
		typ:      info.typ,
		mutable:  info.mutable,
		constant: info.constant,
	}
	if len(info.movedSubs) > 0 {
		slot.movedSubs = cloneMovedSubs(info.movedSubs)
	}
	s.values[name] = slot
	return slot
}

func (s *valueScope) Lookup(name string) (*valueInfo, bool) {
	if s == nil {
		return nil, false
	}
	info, ok := s.values[name]
	return info, ok
}

func (s *valueScope) Clone() *valueScope {
	if s == nil {
		return nil
	}
	out := newValueScope()
	for name, info := range s.values {
		if info == nil {
			continue
		}
		clone := *info
		if len(info.movedSubs) > 0 {
			clone.movedSubs = cloneMovedSubs(info.movedSubs)
		}
		out.values[name] = &clone
	}
	return out
}

func (s *valueScope) TrimToLiveOut(live cfg.NameSet) {
	if s == nil {
		return
	}
	for name, info := range s.values {
		if info == nil || live.Has(name) {
			continue
		}
		if info.borrowOf != "" {
			if owner, ok := s.values[info.borrowOf]; ok && owner != nil && owner.frozen > 0 {
				owner.frozen--
			}
		}
		info.moved = false
		info.moveLoc = source.Location{}
		info.movedPath = ""
		info.movedSubs = nil
		info.frozen = 0
		info.borrowOf = ""
		info.borrowLoc = source.Location{}
	}
}

func (s *valueScope) MergeFrom(other *valueScope, live cfg.NameSet) bool {
	if s == nil || other == nil {
		return false
	}
	changed := false
	for name, incoming := range other.values {
		if incoming == nil || (len(live) > 0 && !live.Has(name)) {
			continue
		}
		current, ok := s.values[name]
		if !ok || current == nil {
			clone := *incoming
			s.values[name] = &clone
			changed = true
			continue
		}
		merged := mergeValueInfo(current, incoming)
		if !equalValueInfo(current, merged) {
			s.values[name] = merged
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
		a.borrowOf == b.borrowOf &&
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
	if a.borrowOf == b.borrowOf {
		out.borrowOf = a.borrowOf
		if a.borrowLoc.Start != nil {
			out.borrowLoc = a.borrowLoc
		} else {
			out.borrowLoc = b.borrowLoc
		}
	} else {
		out.borrowOf = ""
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

type borrowInfo struct {
	owner string
	loc   source.Location
}

type analyzer struct {
	ctx      *context.CompilerContext
	mod      *context.Module
	module   *hir.Module
	borrows  map[hir.Expr]borrowInfo
	reported map[string]struct{}
}

func AnalyzeModule(ctx *context.CompilerContext, mod *context.Module) {
	if ctx == nil || mod == nil || mod.HIR == nil || mod.CFG == nil {
		return
	}
	a := &analyzer{
		ctx:      ctx,
		mod:      mod,
		module:   mod.HIR,
		borrows:  make(map[hir.Expr]borrowInfo),
		reported: make(map[string]struct{}),
	}
	for _, global := range mod.HIR.Globals {
		a.checkGlobal(global)
	}
	for _, fn := range mod.CFG.Functions {
		a.checkFunc(fn)
	}
	mod.Phase = phase.PhaseOwnershipAnalyzed
}

func (a *analyzer) checkGlobal(global *hir.Global) {
	if global == nil || global.Value == nil {
		return
	}
	scope := newValueScope()
	a.checkExpr(scope, global.Value)
	if global.Constant {
		a.reportBorrowEscapeIfNeeded(scope, global.Value, "borrow cannot escape into a module-level constant")
	} else {
		a.reportBorrowEscapeIfNeeded(scope, global.Value, "borrow cannot escape into a module-level binding")
	}
	a.consumeMoveValue(scope, global.Value, global.Type)
}

func (a *analyzer) checkFunc(fn *cfg.Function) {
	if fn == nil || fn.Source == nil || fn.Entry == nil {
		return
	}
	inStates := map[*cfg.Block]*valueScope{}
	outStates := map[*cfg.Block]*valueScope{}
	queue := []*cfg.Block{fn.Entry}
	inStates[fn.Entry] = a.seedFunctionState(fn.Source)
	for len(queue) > 0 {
		block := queue[0]
		queue = queue[1:]
		in := inStates[block]
		if in == nil {
			continue
		}
		out := a.transferBlock(in, block)
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

func (a *analyzer) seedFunctionState(fn *hir.Func) *valueScope {
	scope := newValueScope()
	if fn == nil {
		return scope
	}
	if fn.Receiver != nil {
		scope.Declare(fn.Receiver.Name, valueInfo{typ: fn.Receiver.Type, mutable: true})
	}
	for _, param := range fn.Params {
		if param == nil {
			continue
		}
		scope.Declare(param.Name, valueInfo{typ: param.Type})
	}
	declareFunctionLocals(scope, fn.Body)
	return scope
}

func declareFunctionLocals(scope *valueScope, block *hir.BlockStmt) {
	if scope == nil || block == nil {
		return
	}
	for _, stmt := range block.Stmts {
		declareStmtLocals(scope, stmt)
	}
}

func declareStmtLocals(scope *valueScope, stmt hir.Stmt) {
	switch s := stmt.(type) {
	case nil:
		return
	case *hir.BlockStmt:
		declareFunctionLocals(scope, s)
	case *hir.LetStmt:
		scope.Declare(s.Name, valueInfo{typ: s.Type, mutable: s.Mutable})
	case *hir.ConstStmt:
		scope.Declare(s.Name, valueInfo{typ: s.Type, constant: true})
	case *hir.IfStmt:
		declareFunctionLocals(scope, s.Then)
		declareStmtLocals(scope, s.Else)
	case *hir.SwitchStmt:
		for _, kase := range s.Cases {
			if kase != nil {
				declareFunctionLocals(scope, kase.Body)
			}
		}
	case *hir.WhileStmt:
		declareFunctionLocals(scope, s.Body)
	case *hir.ForStmt:
		declareStmtLocals(scope, s.Init)
		declareStmtLocals(scope, s.Post)
		declareFunctionLocals(scope, s.Body)
	case *hir.LoopStmt:
		declareStmtLocals(scope, s.Init)
		declareStmtLocals(scope, s.Post)
		declareFunctionLocals(scope, s.Body)
	case *hir.LabelStmt:
		declareStmtLocals(scope, s.Stmt)
	case *hir.DeferStmt:
		declareStmtLocals(scope, s.Body)
	case *hir.LockStmt:
		scope.Declare(s.Name, valueInfo{typ: exprType(s.Value), mutable: true})
		declareFunctionLocals(scope, s.Body)
	case *hir.UnsafeStmt:
		declareFunctionLocals(scope, s.Body)
	}
}

func (a *analyzer) transferBlock(in *valueScope, block *cfg.Block) *valueScope {
	state := in.Clone()
	if state == nil {
		state = newValueScope()
	}
	for _, stmt := range block.Stmts {
		a.checkCFGStmt(state, block, stmt)
	}
	switch term := block.Terminator.(type) {
	case *cfg.BranchTerm:
		a.checkExpr(state, term.Cond)
	case *cfg.SwitchTerm:
		a.checkExpr(state, term.Value)
		for _, edge := range term.Cases {
			a.checkExpr(state, edge.Expr)
		}
	}
	state.TrimToLiveOut(block.LiveOut)
	return state
}

func (a *analyzer) checkCFGStmt(scope *valueScope, block *cfg.Block, stmt hir.Stmt) {
	switch s := stmt.(type) {
	case nil:
		return
	case *hir.LetStmt:
		if s.Value != nil {
			a.checkExpr(scope, s.Value)
			a.consumeMoveValue(scope, s.Value, s.Type)
		}
		if slot, ok := scope.Lookup(s.Name); ok {
			slot.moved = false
			slot.moveLoc = source.Location{}
			a.bindBorrowValue(scope, slot, s.Value)
		}
	case *hir.ConstStmt:
		if s.Value != nil {
			a.checkExpr(scope, s.Value)
			a.consumeMoveValue(scope, s.Value, s.Type)
		}
		if slot, ok := scope.Lookup(s.Name); ok {
			slot.moved = false
			slot.moveLoc = source.Location{}
			a.bindBorrowValue(scope, slot, s.Value)
		}
	case *hir.ReturnStmt:
		if s.Value != nil {
			a.checkExpr(scope, s.Value)
			a.reportBorrowEscapeIfNeeded(scope, s.Value, "borrow cannot be returned")
			a.consumeMoveValue(scope, s.Value, exprType(s.Value))
		}
	case *hir.ExprStmt:
		a.checkExpr(scope, s.Value)
	case *hir.AssignStmt:
		a.checkAssignmentTarget(scope, s.Left)
		a.checkExpr(scope, s.Right)
		a.consumeMoveValue(scope, s.Right, exprType(s.Left))
		a.rebindBorrowAssignment(scope, s.Left, s.Right)
	case *hir.DeferStmt:
		a.checkDeferredStmt(scope, s.Body)
	case *hir.LockStmt:
		a.checkExpr(scope, s.Value)
		if slot, ok := scope.Lookup(s.Name); ok {
			slot.moved = false
			slot.moveLoc = source.Location{}
		}
	case *hir.UnsafeStmt, *hir.BreakStmt, *hir.ContinueStmt:
		return
	}
	_ = block
}

func (a *analyzer) checkDeferredStmt(scope *valueScope, stmt hir.Stmt) {
	switch s := stmt.(type) {
	case nil:
		return
	case *hir.BlockStmt:
		for _, child := range s.Stmts {
			a.checkDeferredStmt(scope, child)
		}
	case *hir.LetStmt:
		a.checkExpr(scope, s.Value)
	case *hir.ConstStmt:
		a.checkExpr(scope, s.Value)
	case *hir.ReturnStmt:
		a.checkExpr(scope, s.Value)
	case *hir.ExprStmt:
		a.checkExpr(scope, s.Value)
	case *hir.AssignStmt:
		a.checkExpr(scope, s.Left)
		a.checkExpr(scope, s.Right)
	case *hir.IfStmt:
		a.checkExpr(scope, s.Cond)
		a.checkDeferredStmt(scope, s.Then)
		a.checkDeferredStmt(scope, s.Else)
	case *hir.SwitchStmt:
		a.checkExpr(scope, s.Value)
		for _, kase := range s.Cases {
			if kase == nil {
				continue
			}
			a.checkExpr(scope, kase.Expr)
			a.checkDeferredStmt(scope, kase.Body)
		}
	case *hir.WhileStmt:
		a.checkExpr(scope, s.Cond)
		a.checkDeferredStmt(scope, s.Body)
	case *hir.ForStmt:
		a.checkDeferredStmt(scope, s.Init)
		a.checkExpr(scope, s.Cond)
		a.checkDeferredStmt(scope, s.Post)
		a.checkDeferredStmt(scope, s.Body)
	case *hir.LoopStmt:
		a.checkDeferredStmt(scope, s.Init)
		a.checkExpr(scope, s.Cond)
		a.checkDeferredStmt(scope, s.Post)
		a.checkDeferredStmt(scope, s.Body)
	case *hir.LabelStmt:
		a.checkDeferredStmt(scope, s.Stmt)
	case *hir.LockStmt:
		a.checkExpr(scope, s.Value)
	case *hir.UnsafeStmt:
		return
	}
}

func (a *analyzer) checkExpr(scope *valueScope, expr hir.Expr) {
	switch e := expr.(type) {
	case nil, *hir.BadExpr, *hir.NumberLit, *hir.StringLit, *hir.NoneLit:
		return
	case *hir.Ident:
		a.requireActiveValue(scope, e)
	case *hir.PrefixExpr:
		switch e.Op {
		case "copy":
			a.checkExpr(scope, e.Right)
			if !a.isCopyableType(exprType(e.Right)) {
				loc := e.Loc()
				a.addDiagnostic(
					diagnostics.NewError(fmt.Sprintf("cannot copy value of type %s", exprType(e.Right).String())).
						WithCode(diagnostics.ErrInvalidCopy).
						WithPrimaryLabel(&loc, "this value does not support copying"),
				)
			}
		case "take":
			a.checkExpr(scope, e.Right)
			if !a.isMoveType(exprType(e.Right)) {
				loc := e.Loc()
				a.addDiagnostic(
					diagnostics.NewError(fmt.Sprintf("cannot take value of type %s", exprType(e.Right).String())).
						WithCode(diagnostics.ErrInvalidOperation).
						WithPrimaryLabel(&loc, "`take` requires a move value"),
				)
				return
			}
			a.consumeMoveValue(scope, e.Right, exprType(e.Right))
		case "&", "&mut":
			a.checkExpr(scope, e.Right)
			a.recordBorrowExpr(e, e.Right)
		default:
			a.checkExpr(scope, e.Right)
		}
	case *hir.UnsafeExpr:
		a.checkExpr(scope, e.Value)
	case *hir.BinaryExpr:
		a.checkExpr(scope, e.Left)
		a.checkExpr(scope, e.Right)
	case *hir.PostfixExpr:
		a.checkExpr(scope, e.Left)
	case *hir.CallExpr:
		a.checkCall(scope, e)
	case *hir.SelectorExpr:
		a.checkSelectorExpr(scope, e)
	case *hir.CastExpr:
		a.checkExpr(scope, e.Left)
	case *hir.CompositeLit:
		for _, item := range e.Items {
			a.checkExpr(scope, item.Value)
		}
	}
}

func (a *analyzer) checkSelectorExpr(scope *valueScope, expr *hir.SelectorExpr) {
	if expr == nil {
		return
	}
	if root, path, ok := localExprPath(expr); ok {
		a.requireActivePath(scope, root, path, expr.Loc())
		return
	}
	a.checkExpr(scope, expr.Left)
}

func (a *analyzer) checkCall(scope *valueScope, call *hir.CallExpr) {
	if call == nil {
		return
	}
	if ident, ok := call.Callee.(*hir.Ident); ok && len(ident.Path) == 1 {
		switch ident.Path[0] {
		case "panic", "recover":
			for _, arg := range call.Args {
				a.checkExpr(scope, arg)
			}
			return
		}
	}

	if selector, ok := call.Callee.(*hir.SelectorExpr); ok {
		if handled := a.checkMethodCall(scope, call, selector); handled {
			return
		}
	}

	a.checkExpr(scope, call.Callee)
	fnType, ok := exprType(call.Callee).(*typeinfo.FuncType)
	if !ok {
		for _, arg := range call.Args {
			a.checkExpr(scope, arg)
		}
		return
	}
	for i, arg := range call.Args {
		a.checkExpr(scope, arg)
		if i < len(fnType.Params) {
			a.consumeMoveValue(scope, arg, fnType.Params[i])
		}
	}
}

func (a *analyzer) checkMethodCall(scope *valueScope, call *hir.CallExpr, selector *hir.SelectorExpr) bool {
	a.checkExpr(scope, selector.Left)
	receiverType := exprType(selector.Left)
	if typeinfo.IsInvalid(receiverType) || typeinfo.IsUnknown(receiverType) {
		return true
	}
	if field := a.lookupStructField(receiverType, selector.Name); field != nil {
		return false
	}
	if iface, ok := a.underlying(receiverType).(*typeinfo.InterfaceType); ok {
		method := iface.Methods[selector.Name]
		if method == nil {
			return true
		}
		for i, arg := range call.Args {
			a.checkExpr(scope, arg)
			if i < len(method.Params) {
				a.consumeMoveValue(scope, arg, method.Params[i])
			}
		}
		return true
	}
	addressable, mutable := a.exprAccess(scope, selector.Left)
	methodSym, methodType := a.lookupMethod(receiverType, selector.Name, addressable, mutable)
	if methodType == nil {
		return a.canHaveMethods(receiverType)
	}
	for i, arg := range call.Args {
		a.checkExpr(scope, arg)
		if i < len(methodType.Params) {
			a.consumeMoveValue(scope, arg, methodType.Params[i])
		}
	}
	if methodSym != nil {
		if fn, ok := methodSym.Node.(*ast.FuncDecl); ok && a.receiverConsumes(a.findModuleForSymbol(methodSym), fn) {
			a.consumeMoveValue(scope, selector.Left, receiverType)
		}
	}
	return true
}

func (a *analyzer) requireActiveValue(scope *valueScope, ident *hir.Ident) {
	if ident == nil || len(ident.Path) != 1 {
		return
	}
	a.requireActivePath(scope, ident.Path[0], "", ident.Loc())
}

func (a *analyzer) consumeMoveValue(scope *valueScope, expr hir.Expr, typ typeinfo.Type) {
	if expr == nil || typ == nil || !a.isMoveType(typ) {
		return
	}
	switch e := expr.(type) {
	case *hir.PrefixExpr:
		if e.Op == "copy" || e.Op == "take" {
			return
		}
	}
	root, path, ok := localExprPath(expr)
	if !ok || scope == nil {
		return
	}
	info, ok := scope.Lookup(root)
	if !ok || info == nil {
		return
	}
	if info.frozen > 0 {
		a.reportBorrowConflict(expr.Loc(), root, "cannot move a value while a borrow is live")
		return
	}
	if path == "" && len(info.movedSubs) > 0 {
		a.reportPartialMoveUse(root, expr.Loc(), info)
		return
	}
	if path != "" {
		if movedLoc, movedPath, ok := movedPathConflict(info, path); ok {
			a.reportMovedPathUse(root, path, expr.Loc(), movedLoc, movedPath)
			return
		}
		markMovedPath(info, path, expr.Loc())
		return
	}
	info.moved = true
	info.moveLoc = expr.Loc()
	info.movedPath = ""
	info.movedSubs = nil
}

func (a *analyzer) recordBorrowExpr(expr hir.Expr, sourceExpr hir.Expr) {
	if expr == nil || sourceExpr == nil {
		return
	}
	name, ok := a.borrowSourceName(sourceExpr)
	if !ok {
		return
	}
	a.borrows[expr] = borrowInfo{owner: name, loc: expr.Loc()}
}

func (a *analyzer) borrowSourceName(expr hir.Expr) (string, bool) {
	switch e := expr.(type) {
	case *hir.Ident:
		if len(e.Path) == 1 {
			return e.Path[0], true
		}
	case *hir.PrefixExpr:
		if e.Op == "*" {
			return a.borrowSourceName(e.Right)
		}
	case *hir.SelectorExpr:
		return a.borrowSourceName(e.Left)
	}
	return "", false
}

func (a *analyzer) bindBorrowValue(scope *valueScope, slot *valueInfo, value hir.Expr) {
	if scope == nil || slot == nil {
		return
	}
	a.releaseBorrowValue(scope, slot)
	if value == nil {
		return
	}
	info, ok := a.borrowValueInfo(scope, value)
	if !ok || info.owner == "" {
		return
	}
	owner, ok := scope.Lookup(info.owner)
	if !ok || owner == nil {
		return
	}
	slot.borrowOf = info.owner
	slot.borrowLoc = info.loc
	if a.isMoveType(owner.typ) {
		owner.frozen++
	}
}

func (a *analyzer) releaseBorrowValue(scope *valueScope, slot *valueInfo) {
	if scope == nil || slot == nil || slot.borrowOf == "" {
		return
	}
	if owner, ok := scope.Lookup(slot.borrowOf); ok && owner != nil && owner.frozen > 0 {
		owner.frozen--
	}
	slot.borrowOf = ""
	slot.borrowLoc = source.Location{}
}

func (a *analyzer) rebindBorrowAssignment(scope *valueScope, left hir.Expr, right hir.Expr) {
	if scope == nil {
		return
	}
	root, path, ok := localExprPath(left)
	if !ok || path != "" {
		return
	}
	slot, ok := scope.Lookup(root)
	if !ok || slot == nil {
		return
	}
	a.bindBorrowValue(scope, slot, right)
}

func (a *analyzer) checkAssignmentTarget(scope *valueScope, left hir.Expr) {
	root, path, ok := localExprPath(left)
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

func localExprPath(expr hir.Expr) (root string, path string, ok bool) {
	switch e := expr.(type) {
	case *hir.Ident:
		if len(e.Path) == 1 {
			return e.Path[0], "", true
		}
	case *hir.SelectorExpr:
		root, path, ok := localExprPath(e.Left)
		if !ok {
			return "", "", false
		}
		if path == "" {
			return root, e.Name, true
		}
		return root, path + "." + e.Name, true
	}
	return "", "", false
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

func (a *analyzer) requireActivePath(scope *valueScope, root, path string, loc source.Location) {
	if scope == nil || root == "" {
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

func (a *analyzer) reportPartialMoveUse(root string, loc source.Location, info *valueInfo) {
	if info == nil || len(info.movedSubs) == 0 {
		return
	}
	var movedPath string
	var movedLoc source.Location
	for path, current := range info.movedSubs {
		movedPath, movedLoc = path, current
		break
	}
	diag := diagnostics.NewError(fmt.Sprintf("use of partially moved value %q", root)).
		WithCode(diagnostics.ErrUseAfterMove).
		WithPrimaryLabel(&loc, "some fields of this value were already moved")
	if movedLoc.Start != nil {
		diag.WithSecondaryLabel(&movedLoc, fmt.Sprintf("field %q moved here", movedPath))
	}
	a.addDiagnostic(diag)
}

func (a *analyzer) reportMovedPathUse(root, path string, loc source.Location, movedLoc source.Location, movedPath string) {
	display := root
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
	case *typeinfo.BuiltinType:
		return false
	case *typeinfo.EnumType:
		return false
	case *typeinfo.PointerType:
		return t.IsOwn
	case *typeinfo.NamedType:
		return a.isMoveType(a.underlying(t))
	default:
		return true
	}
}

func (a *analyzer) isCopyableType(typ typeinfo.Type) bool {
	if typ == nil || typeinfo.IsInvalid(typ) || typeinfo.IsUnknown(typ) {
		return true
	}
	switch t := typ.(type) {
	case *typeinfo.BuiltinType:
		return true
	case *typeinfo.EnumType:
		return true
	case *typeinfo.PointerType:
		return !t.IsOwn
	case *typeinfo.NamedType:
		return a.isCopyableType(a.underlying(t))
	case *typeinfo.OptionalType:
		return a.isCopyableType(t.Inner)
	case *typeinfo.ErrorUnionType:
		return a.isCopyableType(t.Error) && a.isCopyableType(t.Value)
	case *typeinfo.ArrayType:
		return a.isCopyableType(t.Inner)
	case *typeinfo.TupleType:
		for _, elem := range t.Elems {
			if !a.isCopyableType(elem) {
				return false
			}
		}
		return true
	case *typeinfo.StructType:
		for _, field := range t.OrderedFields {
			if field == nil || !a.isCopyableType(field.Type) {
				return false
			}
		}
		return true
	case *typeinfo.UnionType:
		for _, member := range t.Members {
			if !a.isCopyableType(member) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func (a *analyzer) reportBorrowEscapeIfNeeded(scope *valueScope, expr hir.Expr, message string) {
	if expr == nil {
		return
	}
	info, ok := a.borrowValueInfo(scope, expr)
	if !ok {
		return
	}
	loc := expr.Loc()
	diag := diagnostics.NewError(message).
		WithCode(diagnostics.ErrBorrowEscape).
		WithPrimaryLabel(&loc, "this borrow escapes its allowed scope")
	if info.loc.Start != nil {
		diag.WithSecondaryLabel(&info.loc, "borrow created here")
	}
	a.addDiagnostic(diag)
}

func (a *analyzer) borrowValueInfo(scope *valueScope, expr hir.Expr) (borrowInfo, bool) {
	if expr == nil {
		return borrowInfo{}, false
	}
	if info, ok := a.borrows[expr]; ok {
		return info, true
	}
	ident, ok := expr.(*hir.Ident)
	if !ok || len(ident.Path) != 1 || scope == nil {
		return borrowInfo{}, false
	}
	slot, ok := scope.Lookup(ident.Path[0])
	if !ok || slot == nil || slot.borrowOf == "" {
		return borrowInfo{}, false
	}
	return borrowInfo{owner: slot.borrowOf, loc: slot.borrowLoc}, true
}

func (a *analyzer) reportBorrowConflict(loc source.Location, name string, message string) {
	a.addDiagnostic(
		diagnostics.NewError(fmt.Sprintf("cannot use %q here", name)).
			WithCode(diagnostics.ErrBorrowConflict).
			WithPrimaryLabel(&loc, message),
	)
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
	if ptr, ok := typ.(*typeinfo.PointerType); ok {
		return ptr.Inner
	}
	return typ
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
		if addressable {
			add("*" + baseName)
			if mutable {
				add("*mut " + baseName)
			}
		}
	case *typeinfo.PointerType:
		if exact, ok := a.receiverKeyFromType(t); ok {
			add(exact)
		}
		if t.IsOwn {
			add("*mut " + baseName)
			add("*" + baseName)
		} else if t.IsMut {
			add("*" + baseName)
		}
	}
	return keys
}

func (a *analyzer) receiverBaseNamedType(typ typeinfo.Type) (*typeinfo.NamedType, bool) {
	switch t := typ.(type) {
	case *typeinfo.NamedType:
		return t, true
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
	case *typeinfo.PointerType:
		named, ok := t.Inner.(*typeinfo.NamedType)
		if !ok {
			return "", false
		}
		switch {
		case t.IsOwn:
			return "own *" + named.Name, true
		case t.IsRaw:
			return "raw *" + named.Name, true
		case t.IsMut:
			return "*mut " + named.Name, true
		default:
			return "*" + named.Name, true
		}
	default:
		return "", false
	}
}

func (a *analyzer) exprAccess(scope *valueScope, expr hir.Expr) (addressable bool, mutable bool) {
	switch e := expr.(type) {
	case *hir.Ident:
		if len(e.Path) == 1 {
			if info, ok := scope.Lookup(e.Path[0]); ok {
				return true, info.mutable && !info.constant
			}
		}
		src := e.SourceExpr()
		ident, ok := src.(*ast.Ident)
		if !ok {
			return false, false
		}
		res := a.lookupResolution(ident)
		if res == nil || res.Symbol == nil {
			return false, false
		}
		switch node := res.Symbol.Node.(type) {
		case *ast.LetDecl:
			return true, node.IsMut
		case *ast.LetStmt:
			return true, node.IsMut
		case *ast.ConstDecl, *ast.ConstStmt:
			return true, false
		default:
			return true, res.Symbol.Kind == symbols.SymbolVar
		}
	case *hir.SelectorExpr:
		return a.exprAccess(scope, e.Left)
	default:
		return false, false
	}
}

func (a *analyzer) lookupResolution(node ast.Node) *binding.Resolution {
	if a.mod == nil || a.mod.Bindings == nil || node == nil {
		return nil
	}
	return a.mod.Bindings.Nodes[node]
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

func exprType(expr hir.Expr) typeinfo.Type {
	if expr == nil {
		return typeinfo.UnknownType{}
	}
	if typ := expr.Type(); typ != nil {
		return typ
	}
	return typeinfo.UnknownType{}
}
