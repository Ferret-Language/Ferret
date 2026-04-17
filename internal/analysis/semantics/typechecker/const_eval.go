package typechecker

import (
	"math/big"
	"strings"

	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/source"
	"compiler/internal/frontend/ast"
	"compiler/internal/utils/numeric"
)

func allowsConstValueCache(node ast.Node) bool {
	switch n := node.(type) {
	case *ast.ConstDecl, *ast.ConstStmt:
		return true
	case *ast.LetDecl:
		return n != nil && !n.IsMut
	case *ast.LetStmt:
		return n != nil && !n.IsMut
	default:
		return false
	}
}

func (c *checker) constValueInfo(mod *context.Module) *typeinfo.ModuleInfo {
	if mod != nil && mod.Types != nil {
		return mod.Types
	}
	return c.info
}

func (c *checker) constNodeType(mod *context.Module, node ast.Node) typeinfo.Type {
	if node == nil {
		return nil
	}
	if info := c.constValueInfo(mod); info != nil {
		if typ, ok := info.Nodes[node]; ok {
			return typ
		}
	}
	return nil
}

func (c *checker) constExprWithState(mod *context.Module, expr ast.Expr, state *constEvalState) (typeinfo.ConstValue, bool) {
	if expr == nil {
		return typeinfo.ConstValue{}, false
	}
	if info := c.constValueInfo(mod); info != nil {
		if value, ok := info.LookupConstValue(expr); ok {
			return value, true
		}
	}
	if state == nil {
		state = &constEvalState{
			env:      make(map[symbols.SymbolID]typeinfo.ConstValue),
			activeFn: make(map[*ast.FuncDecl]bool),
			report:   &constEvalReport{},
		}
	}
	value, ok := c.constExprIn(mod, expr, state)
	if ok {
		if info := c.constValueInfo(mod); info != nil {
			info.BindConstValue(expr, value)
		}
	}
	return value, ok
}

func (c *checker) constExpr(mod *context.Module, expr ast.Expr, seen map[ast.Node]bool) (typeinfo.ConstValue, bool) {
	return c.constExprWithState(mod, expr, &constEvalState{
		env:      make(map[symbols.SymbolID]typeinfo.ConstValue),
		seen:     seen,
		activeFn: make(map[*ast.FuncDecl]bool),
		report:   &constEvalReport{},
	})
}

func (c *checker) evalComptimeExpr(mod *context.Module, origin ast.Expr, expr ast.Expr) (typeinfo.ConstValue, bool, bool, bool, string) {
	var originLoc *source.Location
	if origin != nil {
		loc := origin.Loc()
		originLoc = &loc
	}
	state := &constEvalState{
		env:      make(map[symbols.SymbolID]typeinfo.ConstValue),
		activeFn: make(map[*ast.FuncDecl]bool),
		diag:     c.ctx.Diagnostics,
		origin:   originLoc,
		report:   &constEvalReport{},
	}
	value, ok := c.constExprWithState(mod, expr, state)
	return value, ok, state.hasEmittedDiag(), state.hasDeferredInput(), state.currentFailureReason()
}

type constEvalState struct {
	parent   *constEvalState
	env      map[symbols.SymbolID]typeinfo.ConstValue
	seen     map[ast.Node]bool
	activeFn map[*ast.FuncDecl]bool
	diag     *diagnostics.DiagnosticBag
	origin   *source.Location
	report   *constEvalReport
}

type constEvalReport struct {
	emittedDiag   bool
	failReason    string
	deferredInput bool
}

func (s *constEvalState) scoped() *constEvalState {
	if s == nil {
		return &constEvalState{
			env:      make(map[symbols.SymbolID]typeinfo.ConstValue),
			activeFn: make(map[*ast.FuncDecl]bool),
			report:   &constEvalReport{},
		}
	}
	return &constEvalState{
		parent:   s,
		env:      make(map[symbols.SymbolID]typeinfo.ConstValue),
		seen:     s.seen,
		activeFn: s.activeFn,
		diag:     s.diag,
		origin:   s.origin,
		report:   s.report,
	}
}

func (s *constEvalState) callFrame() *constEvalState {
	if s == nil {
		return &constEvalState{
			env:      make(map[symbols.SymbolID]typeinfo.ConstValue),
			activeFn: make(map[*ast.FuncDecl]bool),
			report:   &constEvalReport{},
		}
	}
	return &constEvalState{
		env:      make(map[symbols.SymbolID]typeinfo.ConstValue),
		seen:     s.seen,
		activeFn: s.activeFn,
		diag:     s.diag,
		origin:   s.origin,
		report:   s.report,
	}
}

func (s *constEvalState) lookup(sym *symbols.Symbol) (typeinfo.ConstValue, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if cur.env == nil || sym == nil {
			continue
		}
		if value, ok := cur.env[sym.ID]; ok {
			return value, true
		}
	}
	return typeinfo.ConstValue{}, false
}

func (s *constEvalState) bind(sym *symbols.Symbol, value typeinfo.ConstValue) {
	if s == nil || sym == nil || !value.Valid() {
		return
	}
	if s.env == nil {
		s.env = make(map[symbols.SymbolID]typeinfo.ConstValue)
	}
	s.env[sym.ID] = value
}

func (s *constEvalState) assign(sym *symbols.Symbol, value typeinfo.ConstValue) bool {
	if s == nil || sym == nil || !value.Valid() {
		return false
	}
	for cur := s; cur != nil; cur = cur.parent {
		if cur.env == nil {
			continue
		}
		if _, ok := cur.env[sym.ID]; ok {
			cur.env[sym.ID] = value
			return true
		}
	}
	return false
}

func (s *constEvalState) setFailureReason(reason string) {
	if s == nil || s.hasEmittedDiag() || s.currentFailureReason() != "" || reason == "" {
		return
	}
	if s.report == nil {
		s.report = &constEvalReport{}
	}
	s.report.failReason = reason
}

func (s *constEvalState) markDeferredInput() {
	if s == nil {
		return
	}
	if s.report == nil {
		s.report = &constEvalReport{}
	}
	s.report.deferredInput = true
}

func (s *constEvalState) hasEmittedDiag() bool {
	return s != nil && s.report != nil && s.report.emittedDiag
}

func (s *constEvalState) hasDeferredInput() bool {
	return s != nil && s.report != nil && s.report.deferredInput
}

func (s *constEvalState) currentFailureReason() string {
	if s == nil || s.report == nil {
		return ""
	}
	return s.report.failReason
}

func (s *constEvalState) raiseDiagnostic(code string, loc source.Location, message string, label string) {
	if s == nil || s.diag == nil {
		return
	}
	if s.report == nil {
		s.report = &constEvalReport{}
	}
	s.report.emittedDiag = true
	msg := strings.TrimSpace(message)
	if msg == "" {
		msg = "compile-time error"
	}
	diag := diagnostics.NewError(msg).WithCode(code)
	if s.origin != nil {
		diag.WithPrimaryLabel(s.origin, "this comptime evaluation failed")
		if s.origin.Filename == nil || loc.Filename == nil || *s.origin.Filename != *loc.Filename || s.origin.Start != loc.Start || s.origin.End != loc.End {
			diag.WithSecondaryLabel(&loc, label)
		}
	} else {
		diag.WithPrimaryLabel(&loc, label)
	}
	s.diag.Add(diag)
}

func (s *constEvalState) raiseCompileTimeError(loc source.Location, message string, label string) {
	s.raiseDiagnostic(diagnostics.ErrInvalidOperation, loc, message, label)
}

func (s *constEvalState) raiseCompileTimePanic(loc source.Location, payload typeinfo.ConstValue, hasPayload bool) {
	msg := "compile-time panic"
	if hasPayload {
		text := constValueText(payload)
		if text != "" {
			msg = "compile-time panic: " + text
		}
	}
	s.raiseCompileTimeError(loc, msg, "panic triggered during compile-time evaluation")
}

type constFlow uint8

const (
	constFlowNone constFlow = iota
	constFlowReturn
	constFlowBreak
	constFlowContinue
)

type constExecResult struct {
	value typeinfo.ConstValue
	flow  constFlow
	ok    bool
}

const constEvalMaxLoopIterations = 10000

func (c *checker) constExprIn(mod *context.Module, expr ast.Expr, state *constEvalState) (typeinfo.ConstValue, bool) {
	switch e := expr.(type) {
	case nil:
		return typeinfo.ConstValue{}, false
	case *ast.NumberLit:
		value, err := numeric.StringToBigInt(e.Value)
		if err != nil {
			return typeinfo.ConstValue{}, false
		}
		return typeinfo.ConstValue{Kind: typeinfo.ConstInt, Int: value}, true
	case *ast.StringLit:
		return typeinfo.ConstValue{Kind: typeinfo.ConstString, String: e.Value}, true
	case *ast.CharLit:
		runes := []rune(e.Value)
		if len(runes) != 1 {
			return typeinfo.ConstValue{}, false
		}
		return typeinfo.ConstValue{Kind: typeinfo.ConstInt, Int: big.NewInt(int64(runes[0]))}, true
	case *ast.NoneLit:
		return typeinfo.ConstValue{Kind: typeinfo.ConstNone}, true
	case *ast.Ident:
		switch e.Text() {
		case "_":
			return typeinfo.ConstValue{}, false
		case "true":
			return typeinfo.ConstValue{Kind: typeinfo.ConstBool, Bool: true}, true
		case "false":
			return typeinfo.ConstValue{Kind: typeinfo.ConstBool, Bool: false}, true
		case "none":
			return typeinfo.ConstValue{Kind: typeinfo.ConstNone}, true
		}
		res := c.lookupTypeResolution(mod, e)
		if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
			return typeinfo.ConstValue{}, false
		}
		if value, ok := state.lookup(res.Symbol); ok {
			return value, true
		}
		if res.Symbol.Node == nil {
			return typeinfo.ConstValue{}, false
		}
		owner := c.findOwnerModuleForSymbol(res.Symbol)
		if owner == nil {
			owner = mod
		}
		info := c.info
		if owner != nil && owner != c.mod {
			info = owner.Types
		}
		if info != nil && allowsConstValueCache(res.Symbol.Node) {
			if value, ok := info.LookupConstValue(res.Symbol.Node); ok {
				return value, true
			}
		}
		if res.Symbol.Kind == symbols.SymbolParam {
			state.markDeferredInput()
		}
		switch res.Symbol.Kind {
		case symbols.SymbolConst:
		case symbols.SymbolVariant:
			if ordinal, ok := typeinfo.LookupEnumOrdinal(c.constNodeType(mod, e), res.Symbol.Name); ok {
				return typeinfo.ConstValue{Kind: typeinfo.ConstInt, Int: big.NewInt(int64(ordinal))}, true
			}
			return typeinfo.ConstValue{}, false
		case symbols.SymbolError:
			if ordinal, ok := typeinfo.LookupErrorOrdinal(c.constNodeType(mod, e), res.Symbol.Name); ok {
				return typeinfo.ConstValue{Kind: typeinfo.ConstInt, Int: big.NewInt(int64(ordinal))}, true
			}
			return typeinfo.ConstValue{}, false
		default:
			return typeinfo.ConstValue{}, false
		}
		if state.seen == nil {
			state.seen = make(map[ast.Node]bool)
		}
		if state.seen[res.Symbol.Node] {
			return typeinfo.ConstValue{}, false
		}
		state.seen[res.Symbol.Node] = true
		defer delete(state.seen, res.Symbol.Node)
		switch n := res.Symbol.Node.(type) {
		case *ast.ConstDecl:
			return c.constExprIn(owner, n.Value, state)
		case *ast.ConstStmt:
			return c.constExprIn(owner, n.Value, state)
		default:
			return typeinfo.ConstValue{}, false
		}
	case *ast.PrefixExpr:
		right, ok := c.constExprIn(mod, e.Right, state)
		if !ok {
			if e.Op == "comptime" && state != nil && state.diag != nil && !state.hasEmittedDiag() && !state.hasDeferredInput() {
				msg := "`comptime` expression must be compile-time evaluable"
				if reason := state.currentFailureReason(); reason != "" {
					msg += ": " + reason
				}
				state.raiseDiagnostic(diagnostics.ErrTypeMismatch, e.Location, msg, "this expression is not compile-time evaluable")
			}
			return typeinfo.ConstValue{}, false
		}
		return typeinfo.ApplyConstUnary(e.Op, right)
	case *ast.BinaryExpr:
		left, ok := c.constExprIn(mod, e.Left, state)
		if !ok {
			return typeinfo.ConstValue{}, false
		}
		switch e.Op {
		case "&&":
			if left.Kind != typeinfo.ConstBool {
				return typeinfo.ConstValue{}, false
			}
			if !left.Bool {
				return typeinfo.ConstValue{Kind: typeinfo.ConstBool, Bool: false}, true
			}
		case "||":
			if left.Kind != typeinfo.ConstBool {
				return typeinfo.ConstValue{}, false
			}
			if left.Bool {
				return typeinfo.ConstValue{Kind: typeinfo.ConstBool, Bool: true}, true
			}
		}
		right, ok := c.constExprIn(mod, e.Right, state)
		if !ok {
			return typeinfo.ConstValue{}, false
		}
		return typeinfo.ApplyConstBinary(e.Op, left, right)
	case *ast.CallExpr:
		return c.constCall(mod, e, state)
	case *ast.CompositeLit:
		typ := c.underlying(c.constNodeType(mod, e))
		if e.Tuple {
			elems := make([]typeinfo.ConstValue, 0, len(e.Items))
			for _, item := range e.Items {
				value, ok := c.constExprIn(mod, item.Value, state)
				if !ok {
					return typeinfo.ConstValue{}, false
				}
				elems = append(elems, value)
			}
			return typeinfo.ConstValue{Kind: typeinfo.ConstSequence, Elems: elems}, true
		}
		switch t := typ.(type) {
		case *typeinfo.ArrayType, *typeinfo.SliceType:
			elems := make([]typeinfo.ConstValue, 0, len(e.Items))
			for _, item := range e.Items {
				value, ok := c.constExprIn(mod, item.Value, state)
				if !ok {
					return typeinfo.ConstValue{}, false
				}
				elems = append(elems, value)
			}
			return typeinfo.ConstValue{Kind: typeinfo.ConstSequence, Elems: elems}, true
		case *typeinfo.StructType:
			names := make([]string, 0, len(t.OrderedFields))
			fieldIndex := make(map[string]int, len(t.OrderedFields))
			for i, field := range t.OrderedFields {
				if field == nil {
					continue
				}
				names = append(names, field.Name)
				fieldIndex[field.Name] = i
			}
			fields := make([]typeinfo.ConstValue, len(names))
			used := make(map[string]bool, len(names))
			nextPos := 0
			for _, item := range e.Items {
				name := ast.ExprText(item.Name)
				if name == "" {
					for nextPos < len(names) && used[names[nextPos]] {
						nextPos++
					}
					if nextPos >= len(names) {
						return typeinfo.ConstValue{}, false
					}
					name = names[nextPos]
				}
				idx, ok := fieldIndex[name]
				if !ok {
					return typeinfo.ConstValue{}, false
				}
				value, ok := c.constExprIn(mod, item.Value, state)
				if !ok {
					return typeinfo.ConstValue{}, false
				}
				fields[idx] = value
				used[name] = true
			}
			return typeinfo.ConstValue{Kind: typeinfo.ConstObject, FieldNames: names, Fields: fields}, true
		default:
			return typeinfo.ConstValue{}, false
		}
	case *ast.IndexExpr:
		base, ok := c.constExprIn(mod, e.Left, state)
		if !ok || base.Kind != typeinfo.ConstSequence {
			return typeinfo.ConstValue{}, false
		}
		index, ok := c.constExprIn(mod, e.Index, state)
		if !ok || index.Kind != typeinfo.ConstInt || index.Int == nil || !index.Int.IsInt64() {
			return typeinfo.ConstValue{}, false
		}
		i := int(index.Int.Int64())
		if i < 0 || i >= len(base.Elems) {
			return typeinfo.ConstValue{}, false
		}
		return base.Elems[i], true
	case *ast.SelectorExpr:
		if res := c.lookupTypeResolution(mod, e); res != nil && res.Kind == binding.ResolutionSymbol && res.Symbol != nil {
			switch res.Symbol.Kind {
			case symbols.SymbolVariant:
				if ordinal, ok := typeinfo.LookupEnumOrdinal(c.constNodeType(mod, e), res.Symbol.Name); ok {
					return typeinfo.ConstValue{Kind: typeinfo.ConstInt, Int: big.NewInt(int64(ordinal))}, true
				}
				return typeinfo.ConstValue{}, false
			case symbols.SymbolError:
				if ordinal, ok := typeinfo.LookupErrorOrdinal(c.constNodeType(mod, e), res.Symbol.Name); ok {
					return typeinfo.ConstValue{Kind: typeinfo.ConstInt, Int: big.NewInt(int64(ordinal))}, true
				}
				return typeinfo.ConstValue{}, false
			}
		}
		base, ok := c.constExprIn(mod, e.Left, state)
		if !ok || base.Kind != typeinfo.ConstObject {
			return typeinfo.ConstValue{}, false
		}
		name := e.Name.Text()
		for i, field := range base.FieldNames {
			if field == name && i < len(base.Fields) {
				return base.Fields[i], true
			}
		}
		return typeinfo.ConstValue{}, false
	case *ast.CastExpr:
		return c.constExprIn(mod, e.Left, state)
	default:
		return typeinfo.ConstValue{}, false
	}
}

func (c *checker) constCall(mod *context.Module, call *ast.CallExpr, state *constEvalState) (typeinfo.ConstValue, bool) {
	if call == nil || state == nil {
		return typeinfo.ConstValue{}, false
	}
	if c.isCompileErrorCall(call.Callee) {
		return c.constCompileError(mod, call, state)
	}
	if c.isNoopCTFECall(mod, call.Callee) {
		for _, arg := range call.Args {
			if _, ok := c.constExprIn(mod, arg, state); !ok {
				return typeinfo.ConstValue{}, false
			}
		}
		return typeinfo.ConstValue{Kind: typeinfo.ConstNone}, true
	}
	args := make([]typeinfo.ConstValue, 0, len(call.Args))
	for _, arg := range call.Args {
		value, ok := c.constExprIn(mod, arg, state)
		if !ok {
			return typeinfo.ConstValue{}, false
		}
		args = append(args, value)
	}
	if c.isForeignLenCall(mod, call.Callee) {
		if len(args) != 1 {
			return typeinfo.ConstValue{}, false
		}
		switch args[0].Kind {
		case typeinfo.ConstString:
			return typeinfo.ConstValue{Kind: typeinfo.ConstInt, Int: big.NewInt(int64(len(args[0].String)))}, true
		case typeinfo.ConstSequence:
			return typeinfo.ConstValue{Kind: typeinfo.ConstInt, Int: big.NewInt(int64(len(args[0].Elems)))}, true
		default:
			return typeinfo.ConstValue{}, false
		}
	}
	res := c.lookupTypeResolution(mod, call.Callee)
	if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
		state.setFailureReason("expression cannot run at compile time")
		return typeinfo.ConstValue{}, false
	}
	fn, ok := res.Symbol.Node.(*ast.FuncDecl)
	if !ok || fn == nil || fn.IsExtern || fn.Body == nil {
		state.setFailureReason("call to " + res.Symbol.Name + " cannot run at compile time")
		return typeinfo.ConstValue{}, false
	}
	owner := c.findOwnerModuleForSymbol(res.Symbol)
	if owner == nil {
		owner = mod
	}
	if state.activeFn[fn] {
		state.setFailureReason("compile-time evaluation hit recursive call")
		return typeinfo.ConstValue{}, false
	}
	frame := state.callFrame()
	state.activeFn[fn] = true
	defer delete(state.activeFn, fn)
	var receiverParam *symbols.Symbol
	var receiverTarget *symbols.Symbol
	if selector, ok := call.Callee.(*ast.SelectorExpr); ok && selector != nil && !fn.IsStatic {
		if fn.Receiver == nil || fn.Receiver.Name == nil {
			return typeinfo.ConstValue{}, false
		}
		receiverValue, ok := c.constExprIn(mod, selector.Left, state)
		if !ok {
			return typeinfo.ConstValue{}, false
		}
		receiverRes := c.lookupTypeResolution(owner, fn.Receiver.Name)
		if receiverRes == nil || receiverRes.Symbol == nil {
			return typeinfo.ConstValue{}, false
		}
		receiverParam = receiverRes.Symbol
		if baseIdent, ok := selector.Left.(*ast.Ident); ok {
			if baseRes := c.lookupTypeResolution(mod, baseIdent); baseRes != nil && baseRes.Symbol != nil {
				receiverTarget = baseRes.Symbol
			}
		}
		frame.bind(receiverRes.Symbol, receiverValue)
	}
	if len(fn.Params) != len(args) {
		return typeinfo.ConstValue{}, false
	}
	for i, param := range fn.Params {
		res := c.lookupTypeResolution(owner, param.Name)
		if res == nil || res.Symbol == nil {
			return typeinfo.ConstValue{}, false
		}
		frame.bind(res.Symbol, args[i])
	}
	result := c.constBlockResult(owner, fn.Body, frame)
	if receiverParam != nil && receiverTarget != nil {
		if updated, ok := frame.lookup(receiverParam); ok {
			state.assign(receiverTarget, updated)
		}
	}
	if !result.ok {
		return typeinfo.ConstValue{}, false
	}
	if result.flow == constFlowReturn {
		return result.value, true
	}
	if typeinfo.IsBuiltinNamed(c.funcResultType(owner, fn), "void") {
		return typeinfo.ConstValue{Kind: typeinfo.ConstNone}, true
	}
	name := "<function>"
	if fn.Name != nil && fn.Name.Text() != "" {
		name = fn.Name.Text()
	}
	state.setFailureReason("call to " + name + " cannot run at compile time")
	return typeinfo.ConstValue{}, false
}

func (c *checker) constCompileError(mod *context.Module, call *ast.CallExpr, state *constEvalState) (typeinfo.ConstValue, bool) {
	if call == nil {
		return typeinfo.ConstValue{}, false
	}
	if len(call.Args) != 1 {
		loc := call.Location
		state.raiseCompileTimeError(loc, "compile_error requires exactly one argument", "compile_error invoked during compile-time evaluation")
		return typeinfo.ConstValue{}, false
	}
	msg, ok := c.constExprIn(mod, call.Args[0], state)
	if !ok {
		if state != nil && state.diag != nil {
			state.raiseCompileTimeError(call.Args[0].Loc(), "compile_error message must be compile-time evaluable", "compute the compile_error message at compile time")
		}
		return typeinfo.ConstValue{}, false
	}
	if msg.Kind != typeinfo.ConstString {
		state.raiseCompileTimeError(call.Args[0].Loc(), "compile_error message must be string", "provide a string compile_error message")
		return typeinfo.ConstValue{}, false
	}
	text := strings.TrimSpace(msg.String)
	if text == "" {
		text = "compile-time error"
	} else {
		text = "compile-time error: " + text
	}
	state.raiseCompileTimeError(call.Location, text, "compile_error invoked during compile-time evaluation")
	return typeinfo.ConstValue{}, false
}

func (c *checker) isNoopCTFECall(mod *context.Module, callee ast.Expr) bool {
	res := c.lookupTypeResolution(mod, callee)
	if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil {
		return false
	}
	switch res.Symbol.Name {
	case "print":
		return true
	default:
		return false
	}
}

func (c *checker) constBlockResult(mod *context.Module, block *ast.BlockStmt, state *constEvalState) constExecResult {
	if block == nil {
		return constExecResult{ok: false}
	}
	scope := state.scoped()
	for _, stmt := range block.Stmts {
		result := c.constStmtResult(mod, stmt, scope)
		if !result.ok || result.flow != constFlowNone {
			return result
		}
	}
	return constExecResult{ok: true}
}

func (c *checker) constStmtResult(mod *context.Module, stmt ast.Stmt, state *constEvalState) constExecResult {
	switch s := stmt.(type) {
	case nil:
		return constExecResult{ok: true}
	case *ast.BlockStmt:
		return c.constBlockResult(mod, s, state)
	case *ast.LetStmt:
		value, ok := c.constExprIn(mod, s.Value, state)
		if !ok {
			return constExecResult{ok: false}
		}
		if res := c.lookupTypeResolution(mod, s.Name); res != nil && res.Symbol != nil {
			state.bind(res.Symbol, value)
		}
		return constExecResult{ok: true}
	case *ast.ConstStmt:
		value, ok := c.constExprIn(mod, s.Value, state)
		if !ok {
			return constExecResult{ok: false}
		}
		if res := c.lookupTypeResolution(mod, s.Name); res != nil && res.Symbol != nil {
			state.bind(res.Symbol, value)
		}
		return constExecResult{ok: true}
	case *ast.ReturnStmt:
		value, ok := c.constExprIn(mod, s.Value, state)
		return constExecResult{value: value, flow: constFlowReturn, ok: ok}
	case *ast.ExprStmt:
		_, ok := c.constExprIn(mod, s.Value, state)
		return constExecResult{ok: ok}
	case *ast.AssignStmt:
		value, ok := c.constExprIn(mod, s.Right, state)
		if !ok {
			return constExecResult{ok: false}
		}
		switch left := s.Left.(type) {
		case *ast.Ident:
			res := c.lookupTypeResolution(mod, left)
			if res == nil || res.Symbol == nil || !state.assign(res.Symbol, value) {
				return constExecResult{ok: false}
			}
			return constExecResult{ok: true}
		case *ast.SelectorExpr:
			baseIdent, ok := left.Left.(*ast.Ident)
			if !ok {
				return constExecResult{ok: false}
			}
			res := c.lookupTypeResolution(mod, baseIdent)
			if res == nil || res.Symbol == nil {
				return constExecResult{ok: false}
			}
			base, ok := state.lookup(res.Symbol)
			if !ok || base.Kind != typeinfo.ConstObject {
				return constExecResult{ok: false}
			}
			fieldName := left.Name.Text()
			updated := false
			for i, name := range base.FieldNames {
				if name == fieldName && i < len(base.Fields) {
					base.Fields[i] = value
					updated = true
					break
				}
			}
			if !updated || !state.assign(res.Symbol, base) {
				return constExecResult{ok: false}
			}
			return constExecResult{ok: true}
		default:
			return constExecResult{ok: false}
		}
	case *ast.IfStmt:
		cond, ok := c.constExprIn(mod, s.Cond, state)
		if !ok || cond.Kind != typeinfo.ConstBool {
			return constExecResult{ok: false}
		}
		if cond.Bool {
			return c.constBlockResult(mod, s.Then, state)
		}
		if s.Else == nil {
			return constExecResult{ok: true}
		}
		return c.constStmtResult(mod, s.Else, state)
	case *ast.WhileStmt:
		for i := 0; i < constEvalMaxLoopIterations; i++ {
			cond, ok := c.constExprIn(mod, s.Cond, state)
			if !ok || cond.Kind != typeinfo.ConstBool {
				return constExecResult{ok: false}
			}
			if !cond.Bool {
				return constExecResult{ok: true}
			}
			result := c.constBlockResult(mod, s.Body, state)
			if !result.ok {
				return result
			}
			switch result.flow {
			case constFlowNone, constFlowContinue:
				continue
			case constFlowBreak:
				return constExecResult{ok: true}
			case constFlowReturn:
				return result
			}
		}
		return constExecResult{ok: false}
	case *ast.BreakStmt:
		return constExecResult{flow: constFlowBreak, ok: true}
	case *ast.ContinueStmt:
		return constExecResult{flow: constFlowContinue, ok: true}
	case *ast.ForStmt:
		iterable, ok := c.constExprIn(mod, s.Iterable, state)
		if !ok || iterable.Kind != typeinfo.ConstSequence {
			return constExecResult{ok: false}
		}
		for i, elem := range iterable.Elems {
			loopState := state.scoped()
			if s.Index != nil {
				if res := c.lookupTypeResolution(mod, s.Index); res != nil && res.Symbol != nil {
					loopState.bind(res.Symbol, typeinfo.ConstValue{Kind: typeinfo.ConstInt, Int: big.NewInt(int64(i))})
				}
			}
			if s.Value != nil {
				if res := c.lookupTypeResolution(mod, s.Value); res != nil && res.Symbol != nil {
					loopState.bind(res.Symbol, elem)
				}
			}
			result := c.constBlockResult(mod, s.Body, loopState)
			if !result.ok {
				return result
			}
			switch result.flow {
			case constFlowNone, constFlowContinue:
				continue
			case constFlowBreak:
				return constExecResult{ok: true}
			case constFlowReturn:
				return result
			}
		}
		return constExecResult{ok: true}
	case *ast.PanicStmt:
		if s.Value == nil {
			return constExecResult{ok: false}
		}
		value, ok := c.constExprIn(mod, s.Value, state)
		if !ok {
			return constExecResult{ok: false}
		}
		state.raiseCompileTimePanic(s.Location, value, true)
		return constExecResult{ok: false}
	default:
		return constExecResult{ok: false}
	}
}

func constValueText(v typeinfo.ConstValue) string {
	switch v.Kind {
	case typeinfo.ConstString:
		return v.String
	case typeinfo.ConstInt:
		if v.Int == nil {
			return ""
		}
		return v.Int.String()
	case typeinfo.ConstBool:
		if v.Bool {
			return "true"
		}
		return "false"
	case typeinfo.ConstNone:
		return "none"
	default:
		return ""
	}
}
